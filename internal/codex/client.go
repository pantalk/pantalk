package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	eventBufferSize = 64

	// processExitGrace bounds how long a failed write waits for the process
	// exit to be recorded. It is only ever paid on an error path.
	processExitGrace = time.Second
)

type commandFactory func(context.Context, Config) (*exec.Cmd, error)

// Client owns one persistent Codex app-server process and multiplexes requests,
// threads, and turns over its JSONL stdio transport.
type Client struct {
	cfg Config

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	processCtx    context.Context
	cancelProcess context.CancelFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]*pendingRequest
	turns   map[string]*Turn
	fatal   error

	nextID  atomic.Int64
	closing atomic.Bool

	done   chan struct{}
	stderr *tailBuffer
	info   Info
}

// Turn is an active or completed Codex turn.
//
// Events are best-effort and intended for optional progress rendering. Wait
// always retains and returns the complete final answer independently.
type Turn struct {
	client   *Client
	threadID string
	id       string

	mu           sync.Mutex
	finalText    string
	fallbackText string
	deltas       map[string]*strings.Builder
	deltaOrder   []string
	result       TurnResult
	err          error
	finished     bool
	dropped      bool

	events chan Event
	done   chan struct{}
	once   sync.Once
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type pendingRequest struct {
	ch       chan rpcResponse
	onResult func(json.RawMessage) error
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
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type notificationMessage struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

type responseMessage struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type threadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type agentMessageDelta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type itemCompleted struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     struct {
		ID    string  `json:"id"`
		Type  string  `json:"type"`
		Text  string  `json:"text"`
		Phase *string `json:"phase"`
	} `json:"item"`
}

type turnCompleted struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		Items []struct {
			ID    string  `json:"id"`
			Type  string  `json:"type"`
			Text  string  `json:"text"`
			Phase *string `json:"phase"`
		} `json:"items"`
	} `json:"turn"`
}

type errorNotification struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	WillRetry bool   `json:"willRetry"`
	Error     struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Start launches "codex app-server --stdio", initializes the connection, and
// returns a ready client.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	return start(ctx, cfg, newCommand)
}

func newCommand(processCtx context.Context, cfg Config) (*exec.Cmd, error) {
	binary := cfg.Binary
	if binary == "" {
		binary = "codex"
	}
	args := append(append([]string{}, cfg.Args...), "app-server", "--stdio")
	return exec.CommandContext(processCtx, binary, args...), nil
}

func start(ctx context.Context, cfg Config, factory commandFactory) (*Client, error) {
	applyConfigDefaults(&cfg)

	processCtx, cancelProcess := context.WithCancel(context.Background())
	cmd, err := factory(processCtx, cfg)
	if err != nil {
		cancelProcess()
		return nil, fmt.Errorf("create codex app-server command: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelProcess()
		return nil, fmt.Errorf("open codex app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProcess()
		_ = stdin.Close()
		return nil, fmt.Errorf("open codex app-server stdout: %w", err)
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
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		processCtx:    processCtx,
		cancelProcess: cancelProcess,
		pending:       make(map[string]*pendingRequest),
		turns:         make(map[string]*Turn),
		done:          make(chan struct{}),
		stderr:        stderr,
	}

	if err := cmd.Start(); err != nil {
		cancelProcess()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	go c.readLoop()
	go c.waitLoop()

	params := map[string]any{
		"clientInfo": map[string]string{
			"name":    cfg.ClientName,
			"title":   cfg.ClientTitle,
			"version": cfg.ClientVersion,
		},
	}
	if err := c.call(ctx, "initialize", params, &c.info); err != nil {
		c.closeAfterStartFailure()
		return nil, fmt.Errorf("initialize codex app-server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		err = c.writeFailure(ctx, err)
		c.closeAfterStartFailure()
		return nil, fmt.Errorf("acknowledge codex app-server initialization: %w", err)
	}

	return c, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.ClientName == "" {
		cfg.ClientName = defaultClientName
	}
	if cfg.ClientTitle == "" {
		cfg.ClientTitle = defaultClientTitle
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = defaultClientVersion
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 2 * time.Second
	}
}

// Info returns metadata from the initialization handshake.
func (c *Client) Info() Info {
	return c.info
}

// Done is closed after the app-server process exits.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Err returns an unexpected process or transport error after Done is closed.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fatal
}

// StartThread creates a persistent Codex thread.
func (c *Client) StartThread(ctx context.Context, opts ThreadOptions) (*Thread, error) {
	var response threadResponse
	if err := c.call(ctx, "thread/start", threadParams(opts), &response); err != nil {
		return nil, err
	}
	if response.Thread.ID == "" {
		return nil, errors.New("codex app-server returned an empty thread id")
	}
	return &Thread{ID: response.Thread.ID, CWD: response.CWD, Model: response.Model}, nil
}

// ResumeThread resumes a persisted Codex thread by ID.
func (c *Client) ResumeThread(ctx context.Context, threadID string, opts ThreadOptions) (*Thread, error) {
	if threadID == "" {
		return nil, errors.New("codex thread id is required")
	}
	params := threadParams(opts)
	params["threadId"] = threadID

	var response threadResponse
	if err := c.call(ctx, "thread/resume", params, &response); err != nil {
		return nil, err
	}
	if response.Thread.ID == "" {
		return nil, errors.New("codex app-server returned an empty thread id")
	}
	return &Thread{ID: response.Thread.ID, CWD: response.CWD, Model: response.Model}, nil
}

func threadParams(opts ThreadOptions) map[string]any {
	params := make(map[string]any)
	if opts.CWD != "" {
		params["cwd"] = opts.CWD
	}
	if opts.Model != "" {
		params["model"] = opts.Model
	}
	if opts.Sandbox != "" {
		params["sandbox"] = opts.Sandbox
	}
	if opts.ApprovalPolicy != "" {
		params["approvalPolicy"] = opts.ApprovalPolicy
	}
	if opts.DeveloperInstructions != "" {
		params["developerInstructions"] = opts.DeveloperInstructions
	}
	return params
}

// StartTurn starts a text-only turn. Use Events for optional progress and Wait
// for the complete final response.
func (c *Client) StartTurn(ctx context.Context, threadID, prompt string) (*Turn, error) {
	return c.StartTurnWithOptions(ctx, threadID, prompt, TurnOptions{})
}

// StartTurnWithOptions starts a text-only turn with stable per-turn overrides.
func (c *Client) StartTurnWithOptions(
	ctx context.Context,
	threadID, prompt string,
	opts TurnOptions,
) (*Turn, error) {
	if threadID == "" {
		return nil, errors.New("codex thread id is required")
	}
	if prompt == "" {
		return nil, errors.New("codex prompt is required")
	}

	var turn *Turn
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]string{{
			"type": "text",
			"text": prompt,
		}},
	}
	if opts.Effort != "" {
		params["effort"] = opts.Effort
	}

	err := c.callWithResultHook(ctx, "turn/start", params, nil, func(raw json.RawMessage) error {
		var response turnStartResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode turn/start response: %w", err)
		}
		if response.Turn.ID == "" {
			return errors.New("codex app-server returned an empty turn id")
		}

		turn = &Turn{
			client:   c,
			threadID: threadID,
			id:       response.Turn.ID,
			deltas:   make(map[string]*strings.Builder),
			events:   make(chan Event, eventBufferSize),
			done:     make(chan struct{}),
		}
		c.mu.Lock()
		c.turns[turn.id] = turn
		c.mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return turn, nil
}

// RunTurn starts a turn and waits for its complete final response.
func (c *Client) RunTurn(ctx context.Context, threadID, prompt string) (TurnResult, error) {
	return c.RunTurnWithOptions(ctx, threadID, prompt, TurnOptions{})
}

// RunTurnWithOptions starts a turn with overrides and waits for its complete
// final response.
func (c *Client) RunTurnWithOptions(
	ctx context.Context,
	threadID, prompt string,
	opts TurnOptions,
) (TurnResult, error) {
	turn, err := c.StartTurnWithOptions(ctx, threadID, prompt, opts)
	if err != nil {
		return TurnResult{}, err
	}
	result, err := turn.Wait(ctx)
	if err != nil && ctx.Err() != nil {
		interruptCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.Interrupt(interruptCtx, threadID, turn.ID())
		cancel()
	}
	return result, err
}

// Interrupt requests cancellation of an active turn.
func (c *Client) Interrupt(ctx context.Context, threadID, turnID string) error {
	if threadID == "" || turnID == "" {
		return errors.New("codex thread id and turn id are required")
	}
	return c.call(ctx, "turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	}, nil)
}

// Close gracefully shuts down app-server by closing stdin, then kills it if it
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

// ID returns the Codex turn identifier.
func (t *Turn) ID() string {
	return t.id
}

// ThreadID returns the parent Codex thread identifier.
func (t *Turn) ThreadID() string {
	return t.threadID
}

// Events returns best-effort normalized progress events. If a consumer is too
// slow, deltas may be dropped while Wait still retains the final response.
func (t *Turn) Events() <-chan Event {
	return t.events
}

// EventsDropped reports whether the optional progress consumer was too slow.
// It has no effect on the final answer returned by Wait.
func (t *Turn) EventsDropped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

// Wait waits for terminal turn completion and returns the final agent answer.
func (t *Turn) Wait(ctx context.Context) (TurnResult, error) {
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	case <-t.done:
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.result, t.err
	}
}

// Interrupt requests cancellation of this turn.
func (t *Turn) Interrupt(ctx context.Context) error {
	return t.client.Interrupt(ctx, t.threadID, t.id)
}

func (c *Client) call(ctx context.Context, method string, params, target any) error {
	return c.callWithResultHook(ctx, method, params, target, nil)
}

func (c *Client) callWithResultHook(
	ctx context.Context,
	method string,
	params, target any,
	onResult func(json.RawMessage) error,
) error {
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
	c.pending[key] = &pendingRequest{ch: responseCh, onResult: onResult}
	c.mu.Unlock()

	if err := c.write(requestMessage{ID: id, Method: method, Params: params}); err != nil {
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
	return c.write(notificationMessage{Method: method, Params: params})
}

// writeFailure upgrades a transport error into the terminal process error once
// the app-server has exited. A write that races the exit fails with EPIPE,
// which says nothing about why the process died, while the process error
// carries its exit status and captured stderr. The wait is bounded because a
// live process can also refuse a write.
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
		return fmt.Errorf("encode codex app-server message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing.Load() {
		return ErrClosed
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write codex app-server message: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		var message wireMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			c.fail(&ProcessError{
				Err:    fmt.Errorf("decode app-server message: %w", err),
				Stderr: c.stderr.String(),
			})
			c.cancelProcess()
			return
		}
		c.handleMessage(message)
	}
	if err := scanner.Err(); err != nil && !c.closing.Load() {
		c.fail(&ProcessError{Err: fmt.Errorf("read app-server stdout: %w", err), Stderr: c.stderr.String()})
		c.cancelProcess()
	} else if !c.closing.Load() {
		// A normal process exit closes stdout just before Wait returns. Give the
		// wait loop a brief opportunity to preserve the more useful exit status.
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
		c.handleServerRequest(message)
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
			Data:    message.Error.Data,
		}
	} else if pending.onResult != nil {
		if err := pending.onResult(message.Result); err != nil {
			response.err = &RPCError{Code: -32098, Message: err.Error()}
		}
	}
	pending.ch <- response
}

func (c *Client) handleServerRequest(message wireMessage) {
	request := ServerRequest{Method: message.Method, Params: message.Params}
	go func() {
		var (
			result any
			err    error
		)
		if c.cfg.RequestHandler == nil {
			result, err = defaultServerRequestResult(request)
		} else {
			result, err = c.cfg.RequestHandler(c.processCtx, request)
		}

		response := responseMessage{ID: message.ID, Result: result}
		if err != nil {
			response.Result = nil
			response.Error = &wireError{Code: -32000, Message: err.Error()}
		}
		_ = c.write(response)
	}()
}

func defaultServerRequestResult(request ServerRequest) (any, error) {
	switch request.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]string{"decision": "decline"}, nil
	case "mcpServer/elicitation/request":
		return map[string]string{"action": "decline"}, nil
	case "item/tool/requestUserInput":
		return map[string]any{"answers": map[string]any{}}, nil
	default:
		return nil, fmt.Errorf("Pantalk does not handle app-server request %q", request.Method)
	}
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "item/agentMessage/delta":
		var delta agentMessageDelta
		if json.Unmarshal(params, &delta) == nil {
			if turn := c.turn(delta.TurnID); turn != nil {
				turn.addDelta(delta)
			}
		}
	case "item/completed":
		var completed itemCompleted
		if json.Unmarshal(params, &completed) == nil && completed.Item.Type == "agentMessage" {
			if turn := c.turn(completed.TurnID); turn != nil {
				turn.addCompletedMessage(completed)
			}
		}
	case "error":
		var notification errorNotification
		if json.Unmarshal(params, &notification) == nil {
			if turn := c.turn(notification.TurnID); turn != nil {
				turn.addError(notification)
			}
		}
	case "turn/completed":
		var completed turnCompleted
		if json.Unmarshal(params, &completed) == nil {
			if turn := c.removeTurn(completed.Turn.ID); turn != nil {
				turn.complete(completed)
			}
		}
	}
}

func (c *Client) turn(id string) *Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turns[id]
}

func (c *Client) removeTurn(id string) *Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	turn := c.turns[id]
	delete(c.turns, id)
	return turn
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
	turns := c.turns
	c.pending = make(map[string]*pendingRequest)
	c.turns = make(map[string]*Turn)
	c.mu.Unlock()

	for _, request := range pending {
		request.ch <- rpcResponse{err: err}
	}
	for _, turn := range turns {
		turn.fail(err)
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

func (t *Turn) addDelta(delta agentMessageDelta) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	builder := t.deltas[delta.ItemID]
	if builder == nil {
		builder = &strings.Builder{}
		t.deltas[delta.ItemID] = builder
		t.deltaOrder = append(t.deltaOrder, delta.ItemID)
	}
	builder.WriteString(delta.Delta)
	t.emitLocked(Event{
		Type:     EventTextDelta,
		ThreadID: delta.ThreadID,
		TurnID:   delta.TurnID,
		Text:     delta.Delta,
	})
	t.mu.Unlock()
}

func (t *Turn) addCompletedMessage(completed itemCompleted) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	switch {
	case completed.Item.Phase != nil && *completed.Item.Phase == "final_answer":
		t.finalText = completed.Item.Text
	case completed.Item.Phase == nil:
		t.fallbackText = completed.Item.Text
	}
	t.mu.Unlock()
}

func (t *Turn) addError(notification errorNotification) {
	err := errors.New(notification.Error.Message)
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	if !notification.WillRetry {
		t.err = err
	}
	t.emitLocked(Event{
		Type:     EventError,
		ThreadID: notification.ThreadID,
		TurnID:   notification.TurnID,
		Err:      err,
	})
	t.mu.Unlock()
}

func (t *Turn) complete(completed turnCompleted) {
	t.once.Do(func() {
		t.mu.Lock()
		t.selectFinalText(completed)
		t.result = TurnResult{
			ThreadID: completed.ThreadID,
			TurnID:   completed.Turn.ID,
			Text:     t.finalText,
			Status:   completed.Turn.Status,
		}
		if completed.Turn.Error != nil {
			t.err = errors.New(completed.Turn.Error.Message)
		} else if completed.Turn.Status == "failed" && t.err == nil {
			t.err = errors.New("codex turn failed")
		}
		t.finished = true
		if t.err != nil {
			t.emitTerminalLocked(Event{
				Type:     EventError,
				ThreadID: completed.ThreadID,
				TurnID:   completed.Turn.ID,
				Err:      t.err,
			})
		}
		t.emitTerminalLocked(Event{
			Type:     EventCompleted,
			ThreadID: completed.ThreadID,
			TurnID:   completed.Turn.ID,
			Text:     t.finalText,
			Status:   completed.Turn.Status,
			Err:      t.err,
		})
		close(t.events)
		close(t.done)
		t.mu.Unlock()
	})
}

func (t *Turn) selectFinalText(completed turnCompleted) {
	var fallback string
	for _, item := range completed.Turn.Items {
		if item.Type != "agentMessage" {
			continue
		}
		if item.Phase != nil && *item.Phase == "final_answer" {
			t.finalText = item.Text
		}
		if item.Phase == nil {
			fallback = item.Text
		}
	}
	if t.finalText != "" {
		return
	}
	if fallback != "" {
		t.finalText = fallback
		return
	}
	if t.fallbackText != "" {
		t.finalText = t.fallbackText
		return
	}
	if len(t.deltaOrder) > 0 {
		t.finalText = t.deltas[t.deltaOrder[len(t.deltaOrder)-1]].String()
	}
}

func (t *Turn) fail(err error) {
	t.once.Do(func() {
		t.mu.Lock()
		t.err = err
		t.finished = true
		t.emitTerminalLocked(Event{
			Type:     EventError,
			ThreadID: t.threadID,
			TurnID:   t.id,
			Err:      err,
		})
		close(t.events)
		close(t.done)
		t.mu.Unlock()
	})
}

func (t *Turn) emitLocked(event Event) {
	select {
	case t.events <- event:
	default:
		t.dropped = true
	}
}

func (t *Turn) emitTerminalLocked(event Event) {
	select {
	case t.events <- event:
		return
	default:
	}

	// Prefer terminal state over an old progress delta when the optional event
	// consumer falls behind. The complete final text remains separately retained.
	select {
	case <-t.events:
		t.dropped = true
	default:
	}
	select {
	case t.events <- event:
	default:
		t.dropped = true
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
