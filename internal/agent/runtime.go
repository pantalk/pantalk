package agent

import (
	"context"
	"log"
	"time"

	"github.com/pantalk/pantalk/internal/protocol"
)

const (
	agentFailureReplyTimeout = 30 * time.Second
	codexFailureReply        = "The Codex agent could not respond. Check that Codex is installed and authenticated (`codex login status`), then review the Pantalk daemon logs."
	claudeFailureReply       = "The Claude Code agent could not respond. Check that Claude Code is installed and authenticated (`claude auth status`), then review the Pantalk daemon logs."
	acpFailureReply          = "The agent could not respond. Check that the configured ACP agent is installed and authenticated, then review the Pantalk daemon logs."
)

// Runtime is the common lifecycle used by command and persistent agent
// drivers. Implementations decide whether they launch per trigger or maintain a
// long-running agent process, while the server retains one routing path.
type Runtime interface {
	Name() string
	Handle(protocol.Event)
	Stop()
}

var _ Runtime = (*Runner)(nil)

// deliverAgentFailure gives the originating conversation a safe, actionable
// response when a native driver fails. The detailed error remains in daemon
// logs so credentials, paths, or provider diagnostics are not exposed to chat.
func deliverAgentFailure(
	parent context.Context,
	agentName string,
	reply ReplyFunc,
	event protocol.Event,
	message string,
) {
	if parent.Err() != nil {
		return
	}

	ctx, cancel := context.WithTimeout(parent, agentFailureReplyTimeout)
	defer cancel()
	if err := reply(ctx, event, message); err != nil {
		log.Printf("[agent:%s] deliver failure hint: %v", agentName, err)
	}
}
