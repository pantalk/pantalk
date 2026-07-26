// Package claude implements a small client for non-interactive Claude Code
// sessions.
package claude

import (
	"errors"
	"fmt"
	"io"
)

// Config controls each managed Claude Code CLI invocation.
//
// The child inherits nothing from the daemon: Env is the complete environment
// of the process. Local Claude Code authentication, settings, CLAUDE.md files,
// skills, and MCP configuration are found through HOME, so a definition that
// wants them must inherit HOME explicitly. Env is also how a configuration
// points the CLI at any endpoint speaking the Anthropic Messages protocol.
type Config struct {
	Binary string

	// Args are prefix arguments placed before the arguments this client builds
	// for itself. They carry the container invocation when the agent is
	// isolated, so Binary is the runtime and the harness lives in the image.
	Args []string

	Workdir         string
	Model           string
	Effort          string
	PermissionMode  string
	Instructions    string
	AllowedTools    []string
	DisallowedTools []string
	Env             map[string]string
	Stderr          io.Writer
}

// TurnResult is the terminal result emitted by Claude Code.
type TurnResult struct {
	SessionID string
	Text      string
	Subtype   string
}

// ProcessError reports an unsuccessful Claude Code invocation.
type ProcessError struct {
	Err    error
	Stderr string
}

func (e *ProcessError) Error() string {
	if e == nil {
		return ""
	}
	if e.Stderr == "" {
		return fmt.Sprintf("claude command failed: %v", e.Err)
	}
	return fmt.Sprintf("claude command failed: %v: %s", e.Err, e.Stderr)
}

func (e *ProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	// ErrClosed is returned after the client has been closed.
	ErrClosed = errors.New("claude client is closed")
)
