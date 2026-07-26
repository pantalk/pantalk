package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/pantalk/pantalk/internal/procenv"
)

const (
	maxMessageBytes = 8 << 20
	stderrTailBytes = 16 << 10
)

type commandFactory func(context.Context, string, []string) (*exec.Cmd, error)

// Client runs one Claude Code print-mode process per turn. Claude's session ID
// makes the conversation durable even though the transport process is not.
type Client struct {
	cfg     Config
	factory commandFactory

	mu     sync.Mutex
	closed bool
}

type streamMessage struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	IsError   bool            `json:"is_error"`
	Result    json.RawMessage `json:"result"`
}

// New creates a Claude Code client after confirming the configured executable
// can be resolved. No process is started until RunTurn.
func New(cfg Config) (*Client, error) {
	binary := strings.TrimSpace(cfg.Binary)
	if binary == "" {
		binary = "claude"
	}
	// Binary is whatever is executed on this host. Under isolation that is the
	// container runtime, and the harness itself lives in the image — so this
	// must never be resolved against the host PATH.
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("find claude executable %q: %w", binary, err)
	}
	cfg.Binary = resolved
	return newClient(cfg, newCommand), nil
}

func newClient(cfg Config, factory commandFactory) *Client {
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "claude"
	}
	return &Client{cfg: cfg, factory: factory}
}

func newCommand(ctx context.Context, binary string, args []string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, binary, args...), nil
}

// RunTurn sends one prompt, optionally resuming an existing Claude Code
// session, and returns the authoritative result message.
func (c *Client) RunTurn(ctx context.Context, sessionID, prompt string) (TurnResult, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return TurnResult{}, ErrClosed
	}
	if strings.TrimSpace(prompt) == "" {
		return TurnResult{}, errors.New("claude prompt is required")
	}

	args := append(append([]string{}, c.cfg.Args...), c.commandArgs(strings.TrimSpace(sessionID))...)
	cmd, err := c.factory(ctx, c.cfg.Binary, args)
	if err != nil {
		return TurnResult{}, fmt.Errorf("create claude command: %w", err)
	}
	if strings.TrimSpace(c.cfg.Workdir) != "" {
		cmd.Dir = c.cfg.Workdir
	}
	procenv.Apply(cmd, c.cfg.Env)
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TurnResult{}, fmt.Errorf("open claude stdout: %w", err)
	}

	stderr := &tailBuffer{limit: stderrTailBytes}
	if c.cfg.Stderr == nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = io.MultiWriter(c.cfg.Stderr, stderr)
	}

	if err := cmd.Start(); err != nil {
		return TurnResult{}, fmt.Errorf("start claude command: %w", err)
	}

	result, scanErr := readResult(stdout)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return TurnResult{}, scanErr
	}
	if waitErr != nil {
		return TurnResult{}, &ProcessError{
			Err:    waitErr,
			Stderr: strings.TrimSpace(stderr.String()),
		}
	}
	if result.SessionID == "" {
		return TurnResult{}, errors.New("claude returned an empty session id")
	}
	return result, nil
}

func (c *Client) commandArgs(sessionID string) []string {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if value := strings.TrimSpace(c.cfg.Model); value != "" {
		args = append(args, "--model", value)
	}
	if value := strings.TrimSpace(c.cfg.Effort); value != "" {
		args = append(args, "--effort", value)
	}
	if value := strings.TrimSpace(c.cfg.PermissionMode); value != "" {
		args = append(args, "--permission-mode", value)
	}
	if value := strings.TrimSpace(c.cfg.Instructions); value != "" {
		args = append(args, "--append-system-prompt", value)
	}
	if values := cleanList(c.cfg.AllowedTools); len(values) > 0 {
		args = append(args, "--allowed-tools", strings.Join(values, ","))
	}
	if values := cleanList(c.cfg.DisallowedTools); len(values) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(values, ","))
	}
	return args
}

func cleanList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func readResult(reader io.Reader) (TurnResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)

	var result TurnResult
	var resultError error
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var message streamMessage
		if err := json.Unmarshal(line, &message); err != nil {
			return TurnResult{}, fmt.Errorf("decode claude stream message: %w", err)
		}
		if message.SessionID != "" {
			result.SessionID = message.SessionID
		}
		if message.Type != "result" {
			continue
		}

		result.Subtype = message.Subtype
		result.Text = decodeResultText(message.Result)
		if message.IsError || (message.Subtype != "" && message.Subtype != "success") {
			detail := strings.TrimSpace(result.Text)
			if detail == "" {
				detail = message.Subtype
			}
			resultError = fmt.Errorf("claude result failed: %s", detail)
		}
	}
	if err := scanner.Err(); err != nil {
		return TurnResult{}, fmt.Errorf("read claude stream: %w", err)
	}
	if resultError != nil {
		return TurnResult{}, resultError
	}
	if result.Subtype == "" {
		return TurnResult{}, errors.New("claude stream ended without a result message")
	}
	return result, nil
}

func decodeResultText(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

// Close prevents new turns. Active commands are owned by their turn contexts
// and are canceled when the runtime stops.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
