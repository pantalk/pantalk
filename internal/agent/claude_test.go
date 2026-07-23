package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/claude"
	"github.com/pantalk/pantalk/internal/protocol"
)

type fakeClaudeClient struct {
	mu sync.Mutex

	sessions []string
	prompts  []string
	nextID   string
	response string
	closed   bool
}

func (f *fakeClaudeClient) RunTurn(
	_ context.Context,
	sessionID string,
	prompt string,
) (claude.TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, sessionID)
	f.prompts = append(f.prompts, prompt)
	resultID := sessionID
	if resultID == "" {
		resultID = f.nextID
	}
	return claude.TurnResult{
		SessionID: resultID,
		Text:      f.response,
		Subtype:   "success",
	}, nil
}

func (f *fakeClaudeClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func TestClaudeRuntimeStartsSessionPersistsAndResumes(t *testing.T) {
	client := &fakeClaudeClient{
		nextID:   "claude-session-1",
		response: "  Claude reply  ",
	}
	sessions := &memorySessions{}
	replies := make(chan string, 2)
	runtime, err := NewClaudeRuntime(context.Background(), ClaudeRuntimeConfig{
		Name:    "claude-engineering",
		Timeout: time.Second,
	}, client, sessions, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	event := protocol.Event{
		Service:   "local",
		Bot:       "local-test",
		Kind:      "message",
		Direction: "in",
		User:      "alice",
		Target:    "user:alice",
		Direct:    true,
		Notify:    true,
		Text:      "hello",
	}
	runtime.Handle(event)
	select {
	case reply := <-replies:
		if reply != "Claude reply" {
			t.Fatalf("unexpected first reply %q", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first reply")
	}

	event.Text = "continue"
	runtime.Handle(event)
	select {
	case <-replies:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resumed reply")
	}
	runtime.Stop()

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.sessions) != 2 ||
		client.sessions[0] != "" ||
		client.sessions[1] != "claude-session-1" {
		t.Fatalf("unexpected session sequence: %v", client.sessions)
	}
	if len(client.prompts) != 2 ||
		client.prompts[0] != "hello" ||
		client.prompts[1] != "continue" {
		t.Fatalf("unexpected prompts: %v", client.prompts)
	}
	if !client.closed {
		t.Fatal("Claude client was not closed")
	}

	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}
	sessions.mu.Lock()
	saved := sessions.threads["claude-engineering\x00"+key]
	sessions.mu.Unlock()
	if saved != "claude-session-1" {
		t.Fatalf("persisted session = %q, want claude-session-1", saved)
	}
}

func TestClaudeRuntimeUsesPersistedSession(t *testing.T) {
	event := protocol.Event{
		Service:   "slack",
		Bot:       "engineering-slack",
		Kind:      "message",
		Direction: "in",
		User:      "U1",
		Channel:   "C1",
		Thread:    "T1",
		Notify:    true,
		Text:      "continue",
	}
	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}

	client := &fakeClaudeClient{response: "done"}
	sessions := &memorySessions{threads: map[string]string{
		"claude-engineering\x00" + key: "claude-existing",
	}}
	replied := make(chan struct{}, 1)
	runtime, err := NewClaudeRuntime(context.Background(), ClaudeRuntimeConfig{
		Name: "claude-engineering",
	}, client, sessions, func(context.Context, protocol.Event, string) error {
		replied <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Stop()

	runtime.Handle(event)
	select {
	case <-replied:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.sessions) != 1 || client.sessions[0] != "claude-existing" {
		t.Fatalf("unexpected resumed sessions: %v", client.sessions)
	}
}
