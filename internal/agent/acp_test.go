package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/acp"
	"github.com/pantalk/pantalk/internal/protocol"
)

type fakeACPClient struct {
	mu sync.Mutex

	created  []string
	loaded   []string
	sessions []string
	prompts  []string
	nextID   string
	response string
	loadErr  error
	runErr   error
	closed   bool
}

func (f *fakeACPClient) NewSession(_ context.Context, cwd string) (acp.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, cwd)
	return acp.Session{ID: f.nextID}, nil
}

func (f *fakeACPClient) LoadSession(_ context.Context, sessionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = append(f.loaded, sessionID)
	return f.loadErr
}

func (f *fakeACPClient) RunTurn(_ context.Context, sessionID, prompt string) (acp.TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, sessionID)
	f.prompts = append(f.prompts, prompt)
	if f.runErr != nil {
		return acp.TurnResult{}, f.runErr
	}
	return acp.TurnResult{
		SessionID:  sessionID,
		Text:       f.response,
		StopReason: "end_turn",
	}, nil
}

func (f *fakeACPClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func acpTestEvent() protocol.Event {
	return protocol.Event{
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
}

func TestACPRuntimeStartsSessionPrependsInstructionsAndPersists(t *testing.T) {
	client := &fakeACPClient{nextID: "acp-session-1", response: "  agent reply  "}
	sessions := &memorySessions{}
	replies := make(chan string, 2)

	runtime, err := NewACPRuntime(context.Background(), ACPRuntimeConfig{
		Name:         "acp-engineering",
		Workdir:      "/workspace/project",
		Instructions: "Be helpful.",
		Timeout:      time.Second,
	}, client, sessions, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	event := acpTestEvent()
	runtime.Handle(event)
	select {
	case reply := <-replies:
		if reply != "agent reply" {
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
		t.Fatal("timed out waiting for second reply")
	}
	runtime.Stop()

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.created) != 1 || client.created[0] != "/workspace/project" {
		t.Fatalf("unexpected session creations: %v", client.created)
	}
	if len(client.sessions) != 2 ||
		client.sessions[0] != "acp-session-1" ||
		client.sessions[1] != "acp-session-1" {
		t.Fatalf("unexpected session sequence: %v", client.sessions)
	}
	// Instructions ride only on the first turn of a fresh session.
	if len(client.prompts) != 2 ||
		client.prompts[0] != "Be helpful.\n\nhello" ||
		client.prompts[1] != "continue" {
		t.Fatalf("unexpected prompts: %v", client.prompts)
	}
	if !client.closed {
		t.Fatal("ACP client was not closed")
	}

	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}
	sessions.mu.Lock()
	saved := sessions.threads["acp-engineering\x00"+key]
	sessions.mu.Unlock()
	if saved != "acp-session-1" {
		t.Fatalf("persisted session = %q, want acp-session-1", saved)
	}
}

func TestACPRuntimeLoadsPersistedSession(t *testing.T) {
	event := acpTestEvent()
	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}

	client := &fakeACPClient{nextID: "unused", response: "resumed reply"}
	sessions := &memorySessions{threads: map[string]string{
		"acp-engineering\x00" + key: "acp-session-9",
	}}
	replies := make(chan string, 1)

	runtime, err := NewACPRuntime(context.Background(), ACPRuntimeConfig{
		Name:         "acp-engineering",
		Instructions: "Be helpful.",
		Timeout:      time.Second,
	}, client, sessions, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	runtime.Handle(event)
	select {
	case <-replies:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
	runtime.Stop()

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.loaded) != 1 || client.loaded[0] != "acp-session-9" {
		t.Fatalf("unexpected loads: %v", client.loaded)
	}
	if len(client.created) != 0 {
		t.Fatalf("unexpected session creations: %v", client.created)
	}
	// A restored session already has its context; no instructions prefix.
	if len(client.prompts) != 1 || client.prompts[0] != "hello" {
		t.Fatalf("unexpected prompts: %v", client.prompts)
	}
}

func TestACPRuntimeStartsReplacementWhenLoadFails(t *testing.T) {
	event := acpTestEvent()
	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}

	client := &fakeACPClient{
		nextID:   "acp-session-2",
		response: "fresh reply",
		loadErr:  errors.New("session not found"),
	}
	sessions := &memorySessions{threads: map[string]string{
		"acp-engineering\x00" + key: "acp-session-stale",
	}}
	replies := make(chan string, 1)

	runtime, err := NewACPRuntime(context.Background(), ACPRuntimeConfig{
		Name:         "acp-engineering",
		Instructions: "Be helpful.",
		Timeout:      time.Second,
	}, client, sessions, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	runtime.Handle(event)
	select {
	case <-replies:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
	runtime.Stop()

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.loaded) != 1 || len(client.created) != 1 {
		t.Fatalf("unexpected load/create sequence: loads=%v creates=%v", client.loaded, client.created)
	}
	// The replacement session is fresh, so instructions are prepended.
	if len(client.prompts) != 1 || client.prompts[0] != "Be helpful.\n\nhello" {
		t.Fatalf("unexpected prompts: %v", client.prompts)
	}

	sessions.mu.Lock()
	saved := sessions.threads["acp-engineering\x00"+key]
	sessions.mu.Unlock()
	if saved != "acp-session-2" {
		t.Fatalf("persisted session = %q, want acp-session-2", saved)
	}
}

func TestACPRuntimeRepliesWithSafeHintWhenTurnFails(t *testing.T) {
	client := &fakeACPClient{
		nextID: "acp-session-1",
		runErr: errors.New("acp agent exploded: /home/user/.agent/config.toml"),
	}
	replies := make(chan string, 1)

	runtime, err := NewACPRuntime(context.Background(), ACPRuntimeConfig{
		Name:    "acp-engineering",
		Timeout: time.Second,
	}, client, &memorySessions{}, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	runtime.Handle(acpTestEvent())
	select {
	case reply := <-replies:
		if reply != acpFailureReply {
			t.Fatalf("unexpected failure reply %q", reply)
		}
		if strings.Contains(reply, "config.toml") {
			t.Fatalf("failure reply leaked error detail: %q", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure reply")
	}
	runtime.Stop()
}
