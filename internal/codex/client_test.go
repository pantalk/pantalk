package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const helperProcessEnv = "PANTALK_CODEX_HELPER_PROCESS"

func TestNewCommandUsesConfiguredCodexBinaryAndStdio(t *testing.T) {
	cmd, err := newCommand(context.Background(), Config{Binary: "/opt/codex"})
	if err != nil {
		t.Fatalf("newCommand returned an error: %v", err)
	}

	want := []string{"/opt/codex", "app-server", "--stdio"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command arguments = %#v, want %#v", cmd.Args, want)
	}
}

func TestClientLifecycleAndFinalResponse(t *testing.T) {
	client := startFakeClient(t, "lifecycle", Config{})
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close returned an error: %v", err)
		}
	}()

	if got := client.Info(); got.UserAgent != "fake-codex/1.0" || got.PlatformOS != "linux" {
		t.Fatalf("unexpected initialization info: %#v", got)
	}

	thread, err := client.StartThread(context.Background(), ThreadOptions{
		CWD:                   "/workspace/project",
		Model:                 "test-model",
		Sandbox:               "workspace-write",
		ApprovalPolicy:        "on-request",
		DeveloperInstructions: "Be concise.",
	})
	if err != nil {
		t.Fatalf("StartThread returned an error: %v", err)
	}
	if thread.ID != "thread-new" || thread.CWD != "/workspace/project" || thread.Model != "test-model" {
		t.Fatalf("unexpected started thread: %#v", thread)
	}

	resumed, err := client.ResumeThread(context.Background(), "thread-existing", ThreadOptions{})
	if err != nil {
		t.Fatalf("ResumeThread returned an error: %v", err)
	}
	if resumed.ID != "thread-existing" {
		t.Fatalf("resumed thread ID = %q, want thread-existing", resumed.ID)
	}

	turn, err := client.StartTurnWithOptions(
		context.Background(),
		thread.ID,
		"Summarize the repository.",
		TurnOptions{Effort: "medium"},
	)
	if err != nil {
		t.Fatalf("StartTurn returned an error: %v", err)
	}

	result, err := turn.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait returned an error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", result.Status)
	}
	if result.Text != "Final answer." {
		t.Fatalf("final response = %q, want %q", result.Text, "Final answer.")
	}

	var events []Event
	for event := range turn.Events() {
		events = append(events, event)
	}
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %#v", len(events), events)
	}
	if events[0].Type != EventTextDelta || events[0].Text != "Working..." {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[len(events)-1].Type != EventCompleted || events[len(events)-1].Text != "Final answer." {
		t.Fatalf("unexpected completion event: %#v", events[len(events)-1])
	}

	if err := client.Interrupt(context.Background(), thread.ID, turn.ID()); err != nil {
		t.Fatalf("Interrupt returned an error: %v", err)
	}
}

func TestClientCorrelatesConcurrentResponses(t *testing.T) {
	client := startFakeClient(t, "correlation", Config{})
	defer client.Close()

	type outcome struct {
		thread *Thread
		err    error
	}
	results := make(chan outcome, 2)
	var start sync.WaitGroup
	start.Add(2)

	for _, cwd := range []string{"/workspace/first", "/workspace/second"} {
		cwd := cwd
		go func() {
			start.Done()
			start.Wait()
			thread, err := client.StartThread(context.Background(), ThreadOptions{CWD: cwd})
			results <- outcome{thread: thread, err: err}
		}()
	}

	got := make(map[string]string)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("StartThread returned an error: %v", result.err)
		}
		got[result.thread.CWD] = result.thread.ID
	}

	want := map[string]string{
		"/workspace/first":  "thread:/workspace/first",
		"/workspace/second": "thread:/workspace/second",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("correlated responses = %#v, want %#v", got, want)
	}
}

func TestServerRequestHandler(t *testing.T) {
	handled := make(chan ServerRequest, 1)
	client := startFakeClient(t, "custom-request", Config{
		RequestHandler: func(_ context.Context, request ServerRequest) (any, error) {
			handled <- request
			return map[string]string{"answer": "handled"}, nil
		},
	})
	defer client.Close()

	select {
	case request := <-handled:
		if request.Method != "custom/request" {
			t.Fatalf("handled method = %q, want custom/request", request.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("server request was not handled")
	}

	select {
	case <-client.Done():
		if err := client.Err(); err != nil {
			t.Fatalf("client stopped unexpectedly: %v", err)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRequestContextCancellation(t *testing.T) {
	client := startFakeClient(t, "swallow-request", Config{})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.StartThread(ctx, ThreadOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartThread error = %v, want context deadline exceeded", err)
	}
}

func TestUnexpectedProcessExitIncludesStderr(t *testing.T) {
	_, err := startFakeClientResult(t, "fail-initialize", Config{})
	if err == nil {
		t.Fatal("Start unexpectedly succeeded")
	}

	var processErr *ProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("error type = %T, want *ProcessError: %v", err, err)
	}
	if !strings.Contains(processErr.Stderr, "fake app-server failed") {
		t.Fatalf("stderr = %q, want fake process diagnostic", processErr.Stderr)
	}
}

func TestRunTurnCancellationInterruptsTurn(t *testing.T) {
	client := startFakeClient(t, "cancel-turn", Config{})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.RunTurn(ctx, "thread-cancel", "Keep working.")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunTurn error = %v, want context deadline exceeded", err)
	}
}

func TestSlowEventConsumerStillGetsFinalResponseAndCompletion(t *testing.T) {
	client := startFakeClient(t, "event-overflow", Config{})
	defer client.Close()

	turn, err := client.StartTurn(context.Background(), "thread-overflow", "Reply.")
	if err != nil {
		t.Fatalf("StartTurn returned an error: %v", err)
	}
	result, err := turn.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait returned an error: %v", err)
	}
	if result.Text != "Authoritative final response." {
		t.Fatalf("final response = %q, want authoritative response", result.Text)
	}
	if !turn.EventsDropped() {
		t.Fatal("EventsDropped = false, want true")
	}

	var last Event
	for event := range turn.Events() {
		last = event
	}
	if last.Type != EventCompleted || last.Text != result.Text {
		t.Fatalf("last event = %#v, want completion with final response", last)
	}
}

func TestCloseUnblocksActiveTurn(t *testing.T) {
	client := startFakeClient(t, "open-turn", Config{})
	turn, err := client.StartTurn(context.Background(), "thread-open", "Keep working.")
	if err != nil {
		t.Fatalf("StartTurn returned an error: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	_, err = turn.Wait(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Wait error = %v, want ErrClosed", err)
	}
}

func startFakeClient(t *testing.T, scenario string, cfg Config) *Client {
	t.Helper()
	client, err := startFakeClientResult(t, scenario, cfg)
	if err != nil {
		t.Fatalf("start returned an error: %v", err)
	}
	return client
}

func startFakeClientResult(t *testing.T, scenario string, cfg Config) (*Client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg.Env = withHelperMarker(cfg.Env)
	return start(ctx, cfg, func(processCtx context.Context, _ Config) (*exec.Cmd, error) {
		return exec.CommandContext(
			processCtx,
			os.Args[0],
			"-test.run=TestCodexHelperProcess",
			"--",
			scenario,
		), nil
	})
}

// withHelperMarker marks the re-executed test binary as the fake app-server.
// The child inherits nothing from this process, so the marker has to travel
// through the configured environment like any other variable.
func withHelperMarker(env map[string]string) map[string]string {
	marked := make(map[string]string, len(env)+1)
	for key, value := range env {
		marked[key] = value
	}
	marked[helperProcessEnv] = "1"
	return marked
}

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	scenario := os.Args[len(os.Args)-1]
	server := fakeServer{
		t:       t,
		scanner: bufio.NewScanner(os.Stdin),
	}

	switch scenario {
	case "lifecycle":
		server.initialize()
		server.lifecycle()
	case "correlation":
		server.initialize()
		server.correlation()
	case "custom-request":
		server.initialize()
		server.customRequest()
	case "swallow-request":
		server.initialize()
		server.expectMethod("thread/start")
		server.drain()
	case "fail-initialize":
		fmt.Fprintln(os.Stderr, "fake app-server failed")
		os.Exit(42)
	case "cancel-turn":
		server.initialize()
		start := server.expectMethod("turn/start")
		server.respond(start.ID, map[string]any{
			"turn": map[string]any{"id": "turn-cancel", "status": "inProgress", "items": []any{}},
		})
		interrupt := server.expectMethod("turn/interrupt")
		server.respond(interrupt.ID, map[string]any{})
		server.drain()
	case "event-overflow":
		server.initialize()
		server.eventOverflow()
	case "open-turn":
		server.initialize()
		start := server.expectMethod("turn/start")
		server.respond(start.ID, map[string]any{
			"turn": map[string]any{"id": "turn-open", "status": "inProgress", "items": []any{}},
		})
		server.drain()
	default:
		t.Fatalf("unknown helper process scenario %q", scenario)
	}
}

type fakeServer struct {
	t       *testing.T
	scanner *bufio.Scanner
}

type fakeMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (s fakeServer) initialize() {
	initialize := s.expectMethod("initialize")
	var params struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"clientInfo"`
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(initialize.Params, &params); err != nil {
		s.t.Fatalf("decode initialize params: %v", err)
	}
	if params.ClientInfo.Name != defaultClientName || params.ClientInfo.Title != defaultClientTitle {
		s.t.Fatalf("unexpected client info: %#v", params.ClientInfo)
	}
	if len(params.Capabilities) != 0 {
		s.t.Fatalf("stable client unexpectedly sent capabilities: %s", params.Capabilities)
	}
	s.respond(initialize.ID, map[string]string{
		"codexHome":      "/tmp/codex-home",
		"platformFamily": "unix",
		"platformOs":     "linux",
		"userAgent":      "fake-codex/1.0",
	})
	s.expectMethod("initialized")
}

func (s fakeServer) lifecycle() {
	start := s.expectMethod("thread/start")
	var startParams map[string]any
	if err := json.Unmarshal(start.Params, &startParams); err != nil {
		s.t.Fatalf("decode thread/start params: %v", err)
	}
	if startParams["cwd"] != "/workspace/project" ||
		startParams["model"] != "test-model" ||
		startParams["sandbox"] != "workspace-write" ||
		startParams["approvalPolicy"] != "on-request" ||
		startParams["developerInstructions"] != "Be concise." {
		s.t.Fatalf("unexpected thread/start params: %#v", startParams)
	}
	s.respond(start.ID, map[string]any{
		"thread": map[string]any{"id": "thread-new"},
		"cwd":    "/workspace/project",
		"model":  "test-model",
	})

	resume := s.expectMethod("thread/resume")
	var resumeParams map[string]any
	if err := json.Unmarshal(resume.Params, &resumeParams); err != nil {
		s.t.Fatalf("decode thread/resume params: %v", err)
	}
	if resumeParams["threadId"] != "thread-existing" {
		s.t.Fatalf("unexpected thread/resume params: %#v", resumeParams)
	}
	s.respond(resume.ID, map[string]any{
		"thread": map[string]any{"id": "thread-existing"},
		"cwd":    "/workspace/project",
		"model":  "test-model",
	})

	turn := s.expectMethod("turn/start")
	var turnParams map[string]any
	if err := json.Unmarshal(turn.Params, &turnParams); err != nil {
		s.t.Fatalf("decode turn/start params: %v", err)
	}
	if turnParams["effort"] != "medium" {
		s.t.Fatalf("turn effort = %#v, want medium", turnParams["effort"])
	}
	s.respond(turn.ID, map[string]any{
		"turn": map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}},
	})

	s.send(map[string]any{
		"id":     "approval-1",
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"threadId": "thread-new", "turnId": "turn-1"},
	})
	approval := s.read()
	if string(approval.ID) != `"approval-1"` {
		s.t.Fatalf("approval response id = %s, want approval-1", approval.ID)
	}
	var approvalResult map[string]string
	if err := json.Unmarshal(approval.Result, &approvalResult); err != nil {
		s.t.Fatalf("decode approval response: %v", err)
	}
	if approvalResult["decision"] != "decline" {
		s.t.Fatalf("approval decision = %q, want decline", approvalResult["decision"])
	}

	s.notify("item/agentMessage/delta", map[string]any{
		"threadId": "thread-new", "turnId": "turn-1", "itemId": "commentary-1", "delta": "Working...",
	})
	s.notify("item/completed", map[string]any{
		"threadId": "thread-new", "turnId": "turn-1", "completedAtMs": 1,
		"item": map[string]any{
			"id": "commentary-1", "type": "agentMessage", "text": "Working...", "phase": "commentary",
		},
	})
	s.notify("item/agentMessage/delta", map[string]any{
		"threadId": "thread-new", "turnId": "turn-1", "itemId": "final-1", "delta": "Final ",
	})
	s.notify("item/agentMessage/delta", map[string]any{
		"threadId": "thread-new", "turnId": "turn-1", "itemId": "final-1", "delta": "answer.",
	})
	s.notify("item/completed", map[string]any{
		"threadId": "thread-new", "turnId": "turn-1", "completedAtMs": 2,
		"item": map[string]any{
			"id": "final-1", "type": "agentMessage", "text": "Final answer.", "phase": "final_answer",
		},
	})
	s.notify("turn/completed", map[string]any{
		"threadId": "thread-new",
		"turn": map[string]any{
			"id": "turn-1", "status": "completed",
			"items": []any{
				map[string]any{"id": "commentary-1", "type": "agentMessage", "text": "Working...", "phase": "commentary"},
				map[string]any{"id": "final-1", "type": "agentMessage", "text": "Final answer.", "phase": "final_answer"},
			},
		},
	})

	interrupt := s.expectMethod("turn/interrupt")
	s.respond(interrupt.ID, map[string]any{})
	s.drain()
}

func (s fakeServer) correlation() {
	first := s.expectMethod("thread/start")
	second := s.expectMethod("thread/start")
	requests := []fakeMessage{first, second}
	for i := len(requests) - 1; i >= 0; i-- {
		request := requests[i]
		var params struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			s.t.Fatalf("decode thread params: %v", err)
		}
		s.respond(request.ID, map[string]any{
			"thread": map[string]any{"id": "thread:" + params.CWD},
			"cwd":    params.CWD,
			"model":  "test-model",
		})
	}
	s.drain()
}

func (s fakeServer) customRequest() {
	s.send(map[string]any{
		"id":     "custom-1",
		"method": "custom/request",
		"params": map[string]string{"question": "test"},
	})
	response := s.read()
	var result map[string]string
	if err := json.Unmarshal(response.Result, &result); err != nil {
		s.t.Fatalf("decode custom response: %v", err)
	}
	if result["answer"] != "handled" {
		s.t.Fatalf("custom response = %#v, want handled", result)
	}
	s.drain()
}

func (s fakeServer) eventOverflow() {
	start := s.expectMethod("turn/start")
	s.respond(start.ID, map[string]any{
		"turn": map[string]any{"id": "turn-overflow", "status": "inProgress", "items": []any{}},
	})
	for i := range eventBufferSize + 20 {
		s.notify("item/agentMessage/delta", map[string]any{
			"threadId": "thread-overflow",
			"turnId":   "turn-overflow",
			"itemId":   "final-overflow",
			"delta":    fmt.Sprintf("%d ", i),
		})
	}
	s.notify("item/completed", map[string]any{
		"threadId": "thread-overflow", "turnId": "turn-overflow", "completedAtMs": 1,
		"item": map[string]any{
			"id":    "final-overflow",
			"type":  "agentMessage",
			"text":  "Authoritative final response.",
			"phase": "final_answer",
		},
	})
	s.notify("turn/completed", map[string]any{
		"threadId": "thread-overflow",
		"turn": map[string]any{
			"id": "turn-overflow", "status": "completed",
			"items": []any{
				map[string]any{
					"id":    "final-overflow",
					"type":  "agentMessage",
					"text":  "Authoritative final response.",
					"phase": "final_answer",
				},
			},
		},
	})
	s.drain()
}

func (s fakeServer) expectMethod(method string) fakeMessage {
	message := s.read()
	if message.Method != method {
		s.t.Fatalf("method = %q, want %q", message.Method, method)
	}
	return message
}

func (s fakeServer) read() fakeMessage {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			s.t.Fatalf("read client message: %v", err)
		}
		s.t.Fatal("client transport closed unexpectedly")
	}
	var message fakeMessage
	if err := json.Unmarshal(s.scanner.Bytes(), &message); err != nil {
		s.t.Fatalf("decode client message %q: %v", s.scanner.Text(), err)
	}
	return message
}

func (s fakeServer) drain() {
	for s.scanner.Scan() {
	}
	if err := s.scanner.Err(); err != nil {
		s.t.Fatalf("drain client transport: %v", err)
	}
}

func (s fakeServer) respond(id json.RawMessage, result any) {
	s.send(map[string]any{"id": id, "result": result})
}

func (s fakeServer) notify(method string, params any) {
	s.send(map[string]any{"method": method, "params": params})
}

func (s fakeServer) send(message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		s.t.Fatalf("encode server message: %v", err)
	}
	fmt.Fprintln(os.Stdout, string(payload))
}
