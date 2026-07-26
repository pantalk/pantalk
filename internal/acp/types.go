// Package acp implements a minimal client for coding agents that speak the
// Agent Client Protocol (ACP) over stdio, such as Kimi Code's `kimi acp`
// server. The client advertises no filesystem or terminal capabilities, so a
// conforming agent performs all work with its own local tools.
package acp

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// ProtocolVersion is the ACP major version this client speaks.
const ProtocolVersion = 1

// Approval policies for agent permission requests. Reject mirrors the safety
// posture of the other native drivers: nothing runs unless the operator opts
// in.
const (
	ApprovalReject     = "reject"
	ApprovalApprove    = "approve"
	ApprovalApproveAll = "approve-for-session"
)

// Config controls one managed ACP agent process.
type Config struct {
	Binary   string            // agent executable
	Args     []string          // argv after the binary, e.g. ["acp"]
	Model    string            // optional model applied to each new session
	Approval string            // permission responses: reject (default), approve, or approve-for-session
	Env      map[string]string // the complete agent process environment; nothing is inherited

	ClientName    string
	ClientVersion string

	Stderr          io.Writer
	ShutdownTimeout time.Duration
}

// Info summarizes the initialization handshake.
type Info struct {
	ProtocolVersion int
	AgentName       string
	AgentVersion    string
	LoadSession     bool
}

// Session identifies a durable agent-side conversation.
type Session struct {
	ID string
}

// TurnResult is the outcome of one completed prompt turn.
type TurnResult struct {
	SessionID  string
	Text       string
	StopReason string
}

// RPCError is a JSON-RPC error returned by the agent.
type RPCError struct {
	Code    int64
	Message string
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("acp agent error %d: %s", e.Code, e.Message)
}

// ProcessError reports an agent process that failed outside the protocol.
type ProcessError struct {
	Err    error
	Stderr string
}

func (e *ProcessError) Error() string {
	if e == nil {
		return ""
	}
	if e.Stderr == "" {
		return fmt.Sprintf("acp agent process failed: %v", e.Err)
	}
	return fmt.Sprintf("acp agent process failed: %v: %s", e.Err, e.Stderr)
}

func (e *ProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	// ErrClosed is returned after the client has been closed.
	ErrClosed = errors.New("acp client is closed")

	// ErrLoadUnsupported is returned by LoadSession when the agent did not
	// advertise the loadSession capability.
	ErrLoadUnsupported = errors.New("acp agent does not support loading sessions")

	// AuthRequiredCode is the ACP error code agents return when the operator
	// must authenticate outside the protocol (for Kimi: `kimi login`).
	AuthRequiredCode int64 = -32000

	// MethodNotFoundCode is the JSON-RPC code for a method the agent does not
	// implement. Optional and unstable ACP methods are answered with it, so the
	// client can tell "unsupported" apart from "failed".
	MethodNotFoundCode int64 = -32601
)

// isMethodNotFound reports whether err is the agent declining a method it does
// not implement.
func isMethodNotFound(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == MethodNotFoundCode
}
