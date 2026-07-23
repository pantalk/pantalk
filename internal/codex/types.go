// Package codex implements a small native client for the Codex app-server.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	defaultClientName    = "pantalk"
	defaultClientTitle   = "Pantalk"
	defaultClientVersion = "dev"
)

// Config controls the managed Codex app-server process.
//
// The child inherits the parent's environment, including CODEX_HOME and the
// user's existing Codex authentication and configuration.
type Config struct {
	// Binary is the path or name of the Codex executable. It defaults to codex.
	Binary string

	ClientName    string
	ClientTitle   string
	ClientVersion string

	// Stderr receives app-server diagnostic output. A bounded copy is always
	// retained and included in process errors.
	Stderr io.Writer

	// RequestHandler handles requests initiated by app-server, such as approval
	// prompts. Known approval requests are declined when this is nil.
	RequestHandler ServerRequestHandler

	// ShutdownTimeout is how long Close waits for app-server to exit after its
	// stdin is closed before killing it.
	ShutdownTimeout time.Duration
}

// ServerRequest is a request initiated by app-server.
type ServerRequest struct {
	Method string
	Params json.RawMessage
}

// ServerRequestHandler returns the JSON-serializable result for an app-server
// request. Returning an error sends a JSON-RPC error response.
type ServerRequestHandler func(context.Context, ServerRequest) (any, error)

// Info describes the initialized app-server process.
type Info struct {
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
	UserAgent      string `json:"userAgent"`
}

// ThreadOptions contains stable overrides for a new or resumed Codex thread.
// Empty fields inherit the user's Codex configuration.
type ThreadOptions struct {
	CWD   string
	Model string
	// Sandbox overrides the Codex sandbox mode (for example, "workspace-write").
	Sandbox string
	// ApprovalPolicy overrides when Codex asks for approval (for example,
	// "on-request").
	ApprovalPolicy        string
	DeveloperInstructions string
}

// TurnOptions contains stable overrides for one Codex turn.
type TurnOptions struct {
	// Effort overrides reasoning effort for the turn (for example, "medium").
	Effort string
}

// Thread identifies a Codex conversation.
type Thread struct {
	ID    string
	CWD   string
	Model string
}

// EventType identifies a normalized turn event.
type EventType string

const (
	// EventTextDelta carries streamed assistant text. It may include commentary;
	// consumers that only need the final answer should call Turn.Wait.
	EventTextDelta EventType = "text_delta"
	// EventCompleted carries the authoritative final answer and terminal status.
	EventCompleted EventType = "completed"
	// EventError carries a non-retrying or terminal turn error.
	EventError EventType = "error"
)

// Event is a normalized subset of Codex turn notifications.
type Event struct {
	Type     EventType
	ThreadID string
	TurnID   string
	Text     string
	Status   string
	Err      error
}

// TurnResult is the terminal result of a turn.
type TurnResult struct {
	ThreadID string
	TurnID   string
	Text     string
	Status   string
}

// RPCError is an error response returned by app-server.
type RPCError struct {
	Code    int64
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

// ProcessError reports an unexpected app-server exit or transport failure.
type ProcessError struct {
	Err    error
	Stderr string
}

func (e *ProcessError) Error() string {
	if e == nil {
		return ""
	}
	if e.Stderr == "" {
		return fmt.Sprintf("codex app-server stopped: %v", e.Err)
	}
	return fmt.Sprintf("codex app-server stopped: %v: %s", e.Err, e.Stderr)
}

func (e *ProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	// ErrClosed is returned after the client has been closed.
	ErrClosed = errors.New("codex app-server client is closed")
)
