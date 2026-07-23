package agent

import (
	"testing"

	"github.com/pantalk/pantalk/internal/protocol"
)

func TestMatcherFiltersBotsBeforeExpression(t *testing.T) {
	matcher, err := NewMatcher("codex", "direct || mentions", []string{"engineering"})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	event := protocol.Event{
		Service:   "slack",
		Bot:       "engineering",
		Kind:      "message",
		Direction: "in",
		Direct:    true,
	}
	if !matcher.Matches(event) {
		t.Fatal("expected selected bot to match")
	}

	event.Bot = "operations"
	if matcher.Matches(event) {
		t.Fatal("unexpected match for unselected bot")
	}
}

func TestMatcherDefaultsToNotifications(t *testing.T) {
	matcher, err := NewMatcher("codex", "", []string{"local-test"})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	event := protocol.Event{
		Service:   "local",
		Bot:       "local-test",
		Kind:      "message",
		Direction: "in",
		Notify:    true,
	}
	if !matcher.Matches(event) {
		t.Fatal("expected notification to match default expression")
	}

	event.Notify = false
	if matcher.Matches(event) {
		t.Fatal("unexpected match for unrelated message")
	}
}
