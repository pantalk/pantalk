package agent

import (
	"testing"

	"github.com/pantalk/pantalk/internal/protocol"
)

func TestConversationKeySeparatesProviderAndBot(t *testing.T) {
	base := protocol.Event{
		Service: "slack",
		Bot:     "engineering",
		Channel: "C1",
		Thread:  "T1",
	}

	first, err := ConversationKey(base)
	if err != nil {
		t.Fatalf("first key: %v", err)
	}

	otherBot := base
	otherBot.Bot = "operations"
	second, err := ConversationKey(otherBot)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}

	otherService := base
	otherService.Service = "discord"
	third, err := ConversationKey(otherService)
	if err != nil {
		t.Fatalf("third key: %v", err)
	}

	if first == second || first == third || second == third {
		t.Fatalf("provider-scoped conversations collided: %q %q %q", first, second, third)
	}
}

func TestConversationKeyScopes(t *testing.T) {
	tests := []struct {
		name  string
		event protocol.Event
	}{
		{
			name: "thread",
			event: protocol.Event{
				Service: "slack",
				Bot:     "engineering",
				Channel: "C1",
				Thread:  "T1",
				User:    "U1",
			},
		},
		{
			name: "direct target",
			event: protocol.Event{
				Service: "local",
				Bot:     "test",
				Target:  "user:alice",
				User:    "alice",
			},
		},
		{
			name: "direct channel",
			event: protocol.Event{
				Service: "slack",
				Bot:     "engineering",
				Channel: "D123",
				User:    "U1",
			},
		},
		{
			name: "flat channel",
			event: protocol.Event{
				Service: "irc",
				Bot:     "engineering",
				Channel: "#engineering",
			},
		},
		{
			name: "generic target",
			event: protocol.Event{
				Service: "custom",
				Bot:     "engineering",
				Target:  "room-1",
			},
		},
	}

	keys := make(map[string]string)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, err := ConversationKey(test.event)
			if err != nil {
				t.Fatalf("conversation key: %v", err)
			}
			if previous, exists := keys[key]; exists {
				t.Fatalf("key collided with %s: %s", previous, key)
			}
			keys[key] = test.name
		})
	}
}

func TestConversationKeyRejectsIncompleteEvents(t *testing.T) {
	tests := []protocol.Event{
		{},
		{Service: "local", Bot: "test"},
		{Service: "local", Bot: "test", Direct: true},
	}

	for _, event := range tests {
		if _, err := ConversationKey(event); err == nil {
			t.Fatalf("expected incomplete event to fail: %+v", event)
		}
	}
}
