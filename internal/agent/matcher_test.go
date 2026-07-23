package agent

import (
	"testing"

	"github.com/pantalk/pantalk/internal/protocol"
)

func TestMatcherEvaluatesBindingExpression(t *testing.T) {
	matcher, err := NewMatcher("codex", "direct || mentions")
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	event := protocol.Event{
		Service:   "slack",
		Kind:      "message",
		Direction: "in",
		Direct:    true,
	}
	if !matcher.Matches(event) {
		t.Fatal("expected direct message to match")
	}

	event.Direct = false
	if matcher.Matches(event) {
		t.Fatal("unexpected match for unrelated message")
	}
}

func TestMatcherDefaultsToNotifications(t *testing.T) {
	matcher, err := NewMatcher("codex", "")
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
