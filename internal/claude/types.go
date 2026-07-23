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
// The child inherits the parent's environment, including the user's existing
// Claude Code authentication, settings, CLAUDE.md files, skills, and MCP
// configuration.
type Config struct {
	Binary          string
	Workdir         string
	Model           string
	Effort          string
	PermissionMode  string
	Instructions    string
	AllowedTools    []string
	DisallowedTools []string
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
