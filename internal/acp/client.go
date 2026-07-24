package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pantalk/pantalk/internal/procenv"
)

const (
	maxMessageBytes = 8 << 20
	stderrTailBytes = 16 << 10

	defaultClientName    = "pantalk"
	defaultClientVersion = "dev"

	// processExitGrace bounds how long a failed write waits for the process
	// exit to be recorded. It is only ever paid on an error path.
	processExitGrace = time.Second
)

type commandFactory func(context.Context, Config) (*exec.Cmd, error)

// Client owns one persistent ACP agent process and multiplexes sessions and
// turns over its newline-delimited JSON-RPC stdio transport.
type Client struct {
	cfg     Config
	factory commandFactory

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	processCtx    context.Context
	cancelProcess context.CancelFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]*pendingRequest
	turns   map[string]*strings.Builder
	fatal   error

	nextID  atomic.Int64
	closing atomic.Bool

	done   chan struct{}
	stderr *tailBuffer
	info   Info
}

type pendingRequest struct {
	ch chan rpcResponse
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type requestMessage struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type notificationMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type responseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

type initializeResponse struct {
	ProtocolVersion   int `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession bool `json:"loadSession"`
	} `json:"agentCapabilities"`
	AgentInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"agentInfo"`
}

type newSessionResponse struct {
	SessionID string `json:"sessionId"`
	Models    struct {
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
}

type promptResponse struct {
	StopReason string `json:"stopReason"`
}

type sessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"update"`
}

type permissionRequest struct {
	SessionID string `json:"sessionId"`
	Options   []struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// Start launches the configured ACP agent process and completes the
// initialization handshake.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	return start(ctx, cfg, newCommand)
}

func newCommand(processCtx context.Context, cfg Config) (*exec.Cmd, error) {
	if strings.TrimSpace(cfg.Binary) == "" {
		return nil, errors.New("acp agent binary is required")
	}
	resolved, err := exec.LookPath(cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("find acp agent executable %q: %w", cfg.Binary, err)
	}
	return exec.CommandContext(processCtx, resolved, cfg.Args...), nil
}

func start(ctx context.Context, cfg Config, factory commandFactory) (*Client, error) {
	applyConfigDefaults(&cfg)

	processCtx, cancelProcess := context.WithCancel(context.Background())
	cmd, err := factory(processCtx, cfg)
	if err != nil {
		cancelProcess()
		return nil, fmt.Errorf("create acp agent command: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelProcess()
		return nil, fmt.Errorf("open acp agent stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProcess()
		_ = stdin.Close()
		return nil, fmt.Errorf("open acp agent stdout: %w", err)
	}

	procenv.Apply(cmd, cfg.Env)

	stderr := &tailBuffer{limit: stderrTailBytes}
	if cfg.Stderr == nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = io.MultiWriter(cfg.Stderr, stderr)
	}

	c := &Client{
		cfg:           cfg,
		factory:       factory,
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		processCtx:    processCtx,
		cancelProcess: cancelProcess,
		pending:       make(map[string]*pendingRequest),
		turns:         make(map[string]*strings.Builder),
		done:          make(chan struct{}),
		stderr:        stderr,
	}

	if err := cmd.Start(); err != nil {
		cancelProcess()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start acp agent: %w", err)
	}

	go c.readLoop()
	go c.waitLoop()

	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs": map[string]bool{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
		"clientInfo": map[string]string{
			"name":    cfg.ClientName,
			"version": cfg.ClientVersion,
		},
	}
	var response initializeResponse
	if err := c.call(ctx, "initialize", params, &response); err != nil {
		c.closeAfterStartFailure()
		return nil, fmt.Errorf("initialize acp agent: %w", err)
	}
	if response.ProtocolVersion != ProtocolVersion {
		c.closeAfterStartFailure()
		return nil, fmt.Errorf(
			"acp agent speaks protocol version %d, this client requires %d",
			response.ProtocolVersion, ProtocolVersion,
		)
	}

	c.info = Info{
		ProtocolVersion: response.ProtocolVersion,
		AgentName:       response.AgentInfo.Name,
		AgentVersion:    response.AgentInfo.Version,
		LoadSession:     response.AgentCapabilities.LoadSession,
	}
	return c, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.ClientName == "" {
		cfg.ClientName = defaultClientName
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = defaultClientVersion
	}
	if cfg.Approval == "" {
		cfg.Approval = ApprovalReject
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 2 * time.Second
	}
}

// Info returns metadata from the initialization handshake.
func (c *Client) Info() Info {
	return c.info
}

// Done is closed after the agent process exits.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Err returns an unexpected process or transport error after Done is closed.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fatal
}

// NewSession creates a durable agent session rooted at cwd and applies the
// configured model, if any.
func (c *Client) NewSession(ctx context.Context, cwd string) (Session, error) {
	resolved, err := resolveCWD(cwd)
	if err != nil {
		return Session{}, err
	}

	var response newSessionResponse
	err = c.call(ctx, "session/new", map[string]any{
		"cwd":        resolved,
		"mcpServers": []any{},
	}, &response)
	if err != nil {
		return Session{}, err
	}
	if response.SessionID == "" {
		return Session{}, errors.New("acp agent returned an empty session id")
	}

	if model := strings.TrimSpace(c.cfg.Model); model != "" {
		modelID := model
		for _, available := range response.Models.AvailableModels {
			if available.ModelID == model || available.Name == model {
				modelID = available.ModelID
				break
			}
		}
		err = c.call(ctx, "session/set_model", map[string]any{
			"sessionId": response.SessionID,
			"modelId":   modelID,
		}, nil)
		if err != nil {
			return Session{}, fmt.Errorf("select acp model %q: %w", model, err)
		}
	}

	return Session{ID: response.SessionID}, nil
}

// LoadSession restores a persisted session by ID. The agent replays the prior
// conversation as session/update notifications, which this client discards
// because no turn is active.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("acp session id is required")
	}
	if !c.info.LoadSession {
		return ErrLoadUnsupported
	}
	resolved, err := resolveCWD(cwd)
	if err != nil {
		return err
	}
	return c.call(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        resolved,
		"mcpServers": []any{},
	}, nil)
}

func resolveCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		resolved, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve acp session cwd: %w", err)
		}
		return resolved, nil
	}
	resolved, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve acp session cwd: %w", err)
	}
	return resolved, nil
}

// RunTurn sends one prompt and blocks until the agent finishes the turn. The
// returned text is accumulated from agent_message_chunk updates. Only one turn
// may be active per session.
func (c *Client) RunTurn(ctx context.Context, sessionID, prompt string) (TurnResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return TurnResult{}, errors.New("acp session id is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return TurnResult{}, errors.New("acp prompt is required")
	}

	c.mu.Lock()
	if c.turns[sessionID] != nil {
		c.mu.Unlock()
		return TurnResult{}, fmt.Errorf("acp session %s already has an active turn", sessionID)
	}
	builder := &strings.Builder{}
	c.turns[sessionID] = builder
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.turns, sessionID)
		c.mu.Unlock()
	}()

	var response promptResponse
	err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{{
			"type": "text",
			"text": prompt,
		}},
	}, &response)
	if err != nil {
		if ctx.Err() != nil {
			// Best-effort cancellation; the agent must still resolve the
			// in-flight prompt with stopReason "cancelled".
			_ = c.notify("session/cancel", map[string]string{"sessionId": sessionID})
		}
		return TurnResult{}, err
	}

	c.mu.Lock()
	text := builder.String()
	c.mu.Unlock()

	if response.StopReason == "cancelled" {
		return TurnResult{}, errors.New("acp turn was cancelled")
	}
	return TurnResult{
		SessionID:  sessionID,
		Text:       text,
		StopReason: response.StopReason,
	}, nil
}

// Close gracefully shuts the agent down by closing stdin, then kills it if it
// does not exit within ShutdownTimeout.
func (c *Client) Close() error {
	if c.closing.CompareAndSwap(false, true) {
		c.stopOutstanding(ErrClosed, false)
		_ = c.stdin.Close()
	}

	timer := time.NewTimer(c.cfg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-c.done:
		return nil
	case <-timer.C:
		c.cancelProcess()
		<-c.done
		return nil
	}
}

func (c *Client) call(ctx context.Context, method string, params, target any) error {
	id := c.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	responseCh := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.fatal != nil {
		err := c.fatal
		c.mu.Unlock()
		return err
	}
	if c.closing.Load() {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[key] = &pendingRequest{ch: responseCh}
	c.mu.Unlock()

	if err := c.write(requestMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.removePending(key)
		return c.writeFailure(ctx, err)
	}

	select {
	case <-ctx.Done():
		c.removePending(key)
		return ctx.Err()
	case <-c.done:
		c.removePending(key)
		if err := c.Err(); err != nil {
			return err
		}
		return ErrClosed
	case response := <-responseCh:
		if response.err != nil {
			return response.err
		}
		if target == nil || len(response.result) == 0 || bytes.Equal(response.result, []byte("null")) {
			return nil
		}
		if err := json.Unmarshal(response.result, target); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(notificationMessage{JSONRPC: "2.0", Method: method, Params: params})
}

// writeFailure upgrades a transport error into the terminal process error once
// the agent has exited. A write that races the exit fails with EPIPE, which
// says nothing about why the agent died, while the process error carries its
// exit status and captured stderr. The wait is bounded because a live agent
// can also refuse a write.
func (c *Client) writeFailure(ctx context.Context, err error) error {
	if errors.Is(err, ErrClosed) {
		return err
	}

	timer := time.NewTimer(processExitGrace)
	defer timer.Stop()

	select {
	case <-c.done:
		// fail runs before done is closed, so a fatal error is already
		// recorded here. Close leaves it nil, which keeps the write error.
		if fatal := c.Err(); fatal != nil {
			return fatal
		}
	case <-ctx.Done():
	case <-timer.C:
	}
	return err
}

func (c *Client) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode acp message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing.Load() {
		return ErrClosed
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write acp message: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message wireMessage
		if err := json.Unmarshal(line, &message); err != nil {
			c.fail(&ProcessError{
				Err:    fmt.Errorf("decode acp message: %w", err),
				Stderr: c.stderr.String(),
			})
			c.cancelProcess()
			return
		}
		c.handleMessage(message)
	}
	if err := scanner.Err(); err != nil && !c.closing.Load() {
		c.fail(&ProcessError{Err: fmt.Errorf("read acp stdout: %w", err), Stderr: c.stderr.String()})
		c.cancelProcess()
	} else if !c.closing.Load() {
		// A normal process exit closes stdout just before Wait returns. Give
		// the wait loop a brief opportunity to preserve the exit status.
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-c.done:
		case <-timer.C:
			c.fail(&ProcessError{Err: io.ErrUnexpectedEOF, Stderr: c.stderr.String()})
			c.cancelProcess()
		}
	}
}

func (c *Client) handleMessage(message wireMessage) {
	if len(message.ID) > 0 && message.Method != "" {
		c.handleAgentRequest(message)
		return
	}
	if len(message.ID) > 0 {
		c.handleResponse(message)
		return
	}
	if message.Method != "" {
		c.handleNotification(message.Method, message.Params)
	}
}

func (c *Client) handleResponse(message wireMessage) {
	key := string(message.ID)
	c.mu.Lock()
	pending := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if pending == nil {
		return
	}

	response := rpcResponse{result: message.Result}
	if message.Error != nil {
		response.err = &RPCError{
			Code:    message.Error.Code,
			Message: message.Error.Message,
		}
	}
	pending.ch <- response
}

func (c *Client) handleAgentRequest(message wireMessage) {
	go func() {
		response := responseMessage{JSONRPC: "2.0", ID: message.ID}
		switch message.Method {
		case "session/request_permission":
			response.Result = c.permissionResult(message.Params)
		default:
			// The client advertises no fs or terminal capabilities, so any
			// other request is out of contract.
			response.Error = &wireError{
				Code:    -32601,
				Message: fmt.Sprintf("Pantalk does not handle acp request %q", message.Method),
			}
		}
		_ = c.write(response)
	}()
}

// permissionResult selects an option matching the configured approval policy.
// When the agent offers no option of the preferred kinds, the request is
// answered as cancelled, which agents treat as a denial.
func (c *Client) permissionResult(params json.RawMessage) map[string]any {
	var request permissionRequest
	_ = json.Unmarshal(params, &request)

	var kinds []string
	switch c.cfg.Approval {
	case ApprovalApprove:
		kinds = []string{"allow_once", "allow_always"}
	case ApprovalApproveAll:
		kinds = []string{"allow_always", "allow_once"}
	default:
		kinds = []string{"reject_once", "reject_always"}
	}

	for _, kind := range kinds {
		for _, option := range request.Options {
			if option.Kind == kind {
				return map[string]any{
					"outcome": map[string]string{
						"outcome":  "selected",
						"optionId": option.OptionID,
					},
				}
			}
		}
	}
	return map[string]any{
		"outcome": map[string]string{"outcome": "cancelled"},
	}
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var update sessionUpdate
	if json.Unmarshal(params, &update) != nil {
		return
	}
	if update.Update.SessionUpdate != "agent_message_chunk" || update.Update.Content.Type != "text" {
		return
	}

	c.mu.Lock()
	// Updates outside an active turn (e.g. session/load replay) are dropped.
	if builder := c.turns[update.SessionID]; builder != nil {
		builder.WriteString(update.Update.Content.Text)
	}
	c.mu.Unlock()
}

func (c *Client) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) fail(err error) {
	c.stopOutstanding(err, true)
}

func (c *Client) stopOutstanding(err error, fatal bool) {
	c.mu.Lock()
	if fatal && c.fatal == nil {
		c.fatal = err
	}
	pending := c.pending
	c.pending = make(map[string]*pendingRequest)
	c.turns = make(map[string]*strings.Builder)
	c.mu.Unlock()

	for _, request := range pending {
		request.ch <- rpcResponse{err: err}
	}
}

func (c *Client) waitLoop() {
	err := c.cmd.Wait()
	c.cancelProcess()
	if !c.closing.Load() {
		if err == nil {
			err = errors.New("process exited unexpectedly")
		}
		c.fail(&ProcessError{Err: err, Stderr: c.stderr.String()})
	}
	close(c.done)
}

func (c *Client) closeAfterStartFailure() {
	c.closing.Store(true)
	_ = c.stdin.Close()
	timer := time.NewTimer(c.cfg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-timer.C:
		c.cancelProcess()
		<-c.done
	}
}

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}
