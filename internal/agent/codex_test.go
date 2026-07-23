package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/codex"
	"github.com/pantalk/pantalk/internal/protocol"
)

type fakeCodexClient struct {
	mu sync.Mutex

	started    []codex.ThreadOptions
	resumed    []string
	runThreads []string
	prompts    []string
	turnOpts   []codex.TurnOptions

	resumeErr error
	nextID    string
	response  string
	closed    bool
	ran       chan struct{}
}

func (f *fakeCodexClient) StartThread(_ context.Context, opts codex.ThreadOptions) (*codex.Thread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, opts)
	id := f.nextID
	if id == "" {
		id = "thread-new"
	}
	return &codex.Thread{ID: id}, nil
}

func (f *fakeCodexClient) ResumeThread(_ context.Context, id string, _ codex.ThreadOptions) (*codex.Thread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, id)
	if f.resumeErr != nil {
		return nil, f.resumeErr
	}
	return &codex.Thread{ID: id}, nil
}

func (f *fakeCodexClient) RunTurnWithOptions(
	_ context.Context,
	threadID string,
	prompt string,
	opts codex.TurnOptions,
) (codex.TurnResult, error) {
	f.mu.Lock()
	f.runThreads = append(f.runThreads, threadID)
	f.prompts = append(f.prompts, prompt)
	f.turnOpts = append(f.turnOpts, opts)
	response := f.response
	ran := f.ran
	f.mu.Unlock()
	if ran != nil {
		ran <- struct{}{}
	}
	return codex.TurnResult{ThreadID: threadID, TurnID: "turn-1", Text: response, Status: "completed"}, nil
}

func (f *fakeCodexClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

type memorySessions struct {
	mu      sync.Mutex
	threads map[string]string
}

func (s *memorySessions) AgentSession(agent, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.threads[agent+"\x00"+key]
	return id, ok, nil
}

func (s *memorySessions) SaveAgentSession(agent, key, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.threads == nil {
		s.threads = make(map[string]string)
	}
	s.threads[agent+"\x00"+key] = id
	return nil
}

func TestCodexRuntimeStartsThreadRunsTurnAndReplies(t *testing.T) {
	client := &fakeCodexClient{nextID: "thread-1", response: "  final answer  "}
	sessions := &memorySessions{}
	replies := make(chan string, 1)

	runtime, err := NewCodexRuntime(context.Background(), CodexRuntimeConfig{
		Name:         "engineering",
		Bots:         []string{"local-test"},
		Workdir:      "/workspace/project",
		Instructions: "Be helpful.",
		Timeout:      time.Second,
		Model:        "gpt-test",
		Effort:       "high",
		Sandbox:      "workspace-write",
		Approval:     "on-request",
	}, client, sessions, func(_ context.Context, _ protocol.Event, text string) error {
		replies <- text
		return nil
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Stop()

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
	if !runtime.Matches(event) {
		t.Fatal("expected direct event on selected bot to match")
	}
	runtime.Handle(event)

	select {
	case reply := <-replies:
		if reply != "final answer" {
			t.Fatalf("unexpected reply %q", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.started) != 1 {
		t.Fatalf("expected one started thread, got %d", len(client.started))
	}
	threadOpts := client.started[0]
	if threadOpts.CWD != "/workspace/project" ||
		threadOpts.Model != "gpt-test" ||
		threadOpts.DeveloperInstructions != "Be helpful." ||
		threadOpts.Sandbox != "workspace-write" ||
		threadOpts.ApprovalPolicy != "on-request" {
		t.Fatalf("unexpected thread options: %+v", threadOpts)
	}
	if len(client.prompts) != 1 || client.prompts[0] != "hello" {
		t.Fatalf("unexpected prompts: %v", client.prompts)
	}
	if len(client.turnOpts) != 1 || client.turnOpts[0].Effort != "high" {
		t.Fatalf("unexpected turn options: %+v", client.turnOpts)
	}
}

func TestCodexRuntimeResumesPersistedThread(t *testing.T) {
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

	client := &fakeCodexClient{response: "done"}
	sessions := &memorySessions{threads: map[string]string{
		"engineering\x00" + key: "thread-existing",
	}}
	replied := make(chan struct{}, 1)
	runtime, err := NewCodexRuntime(context.Background(), CodexRuntimeConfig{
		Name: "engineering",
		Bots: []string{"engineering-slack"},
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
	if len(client.resumed) != 1 || client.resumed[0] != "thread-existing" {
		t.Fatalf("unexpected resumed threads: %v", client.resumed)
	}
	if len(client.started) != 0 {
		t.Fatalf("did not expect a replacement thread: %+v", client.started)
	}
	if len(client.runThreads) != 1 || client.runThreads[0] != "thread-existing" {
		t.Fatalf("turn did not use resumed thread: %v", client.runThreads)
	}
}

func TestCodexRuntimeReplacesUnresumableThread(t *testing.T) {
	event := protocol.Event{
		Service:   "local",
		Bot:       "local-test",
		Kind:      "message",
		Direction: "in",
		User:      "alice",
		Target:    "user:alice",
		Notify:    true,
		Text:      "hello",
	}
	key, err := ConversationKey(event)
	if err != nil {
		t.Fatalf("conversation key: %v", err)
	}

	client := &fakeCodexClient{
		nextID:    "thread-replacement",
		response:  "done",
		resumeErr: errors.New("not found"),
	}
	sessions := &memorySessions{threads: map[string]string{
		"engineering\x00" + key: "thread-stale",
	}}
	replied := make(chan struct{}, 1)
	runtime, err := NewCodexRuntime(context.Background(), CodexRuntimeConfig{
		Name: "engineering",
		Bots: []string{"local-test"},
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

	sessions.mu.Lock()
	saved := sessions.threads["engineering\x00"+key]
	sessions.mu.Unlock()
	if saved != "thread-replacement" {
		t.Fatalf("expected replacement thread to be persisted, got %q", saved)
	}
}
