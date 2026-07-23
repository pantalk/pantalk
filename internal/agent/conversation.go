package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pantalk/pantalk/internal/protocol"
)

// conversationIdentity is serialized as the durable key used to associate a
// normalized messaging conversation with an agent runtime thread. Keeping the
// provider identifiers in the key prevents coincidentally equal channel or
// thread IDs from different bots from sharing context.
type conversationIdentity struct {
	Service string `json:"service"`
	Bot     string `json:"bot"`
	Scope   string `json:"scope"`
	Target  string `json:"target,omitempty"`
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	User    string `json:"user,omitempty"`
}

// ConversationKey derives session identity from normalized provider fields.
// Threaded conversations are isolated first, then direct conversations, then
// flat channels. There is intentionally no user-configurable session scope:
// connectors provide the identifiers and Pantalk applies one consistent rule.
func ConversationKey(event protocol.Event) (string, error) {
	identity := conversationIdentity{
		Service: strings.TrimSpace(event.Service),
		Bot:     strings.TrimSpace(event.Bot),
	}
	if identity.Service == "" || identity.Bot == "" {
		return "", fmt.Errorf("conversation requires service and bot")
	}

	switch {
	case strings.TrimSpace(event.Thread) != "":
		identity.Scope = "thread"
		identity.Target = strings.TrimSpace(event.Target)
		identity.Channel = strings.TrimSpace(event.Channel)
		identity.Thread = strings.TrimSpace(event.Thread)
	case event.Direct || isDirectToAgentEvent(event):
		identity.Scope = "direct"
		identity.Target = strings.TrimSpace(event.Target)
		identity.Channel = strings.TrimSpace(event.Channel)
		identity.User = strings.TrimSpace(event.User)
		if identity.Target == "" && identity.Channel == "" && identity.User == "" {
			return "", fmt.Errorf("direct conversation requires target, channel, or user")
		}
	case strings.TrimSpace(event.Channel) != "":
		identity.Scope = "channel"
		identity.Channel = strings.TrimSpace(event.Channel)
	case strings.TrimSpace(event.Target) != "":
		identity.Scope = "target"
		identity.Target = strings.TrimSpace(event.Target)
	default:
		return "", fmt.Errorf("conversation requires thread, direct peer, channel, or target")
	}

	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode conversation identity: %w", err)
	}
	return string(encoded), nil
}

// isDirectToAgentEvent mirrors the normalized direct-message conventions used
// by the server. It keeps ConversationKey useful in focused package tests and
// for callers that construct an event before the server annotates Direct.
func isDirectToAgentEvent(event protocol.Event) bool {
	target := strings.ToLower(strings.TrimSpace(event.Target))
	if strings.HasPrefix(target, "dm:") ||
		strings.HasPrefix(target, "direct:") ||
		strings.HasPrefix(target, "user:") {
		return true
	}

	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(event.Channel)), "D") {
		return true
	}

	return event.Kind == "dm"
}
