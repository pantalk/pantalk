package acp

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
	"testing"
	"time"
)

const helperProcessEnv = "PANTALK_ACP_HELPER_PROCESS"

func TestNewCommandUsesConfiguredBinaryAndArgs(t *testing.T) {
	cmd, err := newCommand(context.Background(), Config{Binary: os.Args[0], Args: []string{"acp"}})
	if err != nil {
		t.Fatalf("newCommand returned an error: %v", err)
	}
	want := []string{os.Args[0], "acp"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command arguments = %#v, want %#v", cmd.Args, want)
	}
}

func TestNewCommandRejectsMissingBinary(t *testing.T) {
	_, err := newCommand(context.Background(), Config{Binary: t.TempDir() + "/missing-kimi"})
	if err == nil || !strings.Contains(err.Error(), "find acp agent executable") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestClientLifecycle(t *testing.T) {
	client := startFakeClient(t, "lifecycle", Config{Model: "kimi-k3"})
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close returned an error: %v", err)
		}
	}()

	info := client.Info()
	if info.ProtocolVersion != 1 || info.AgentName != "kimi-cli" || info.AgentVersion != "9.9" || !info.LoadSession {
		t.Fatalf("unexpected initialization info: %#v", info)
	}

	session, err := client.NewSession(context.Background(), "/workspace/project")
	if err != nil {
		t.Fatalf("NewSession returned an error: %v", err)
	}
	if session.ID != "S1" {
		t.Fatalf("session ID = %q, want S1", session.ID)
	}

	result, err := client.RunTurn(context.Background(), session.ID, "hello")
	if err != nil {
		t.Fatalf("RunTurn returned an error: %v", err)
	}
	if result.Text != "Hello world." || result.StopReason != "end_turn" {
		t.Fatalf("unexpected turn result: %#v", result)
	}

	if err := client.LoadSession(context.Background(), "S-old", "/workspace/project"); err != nil {
		t.Fatalf("LoadSession returned an error: %v", err)
	}

	second, err := client.RunTurn(context.Background(), "S-old", "again")
	if err != nil {
		t.Fatalf("second RunTurn returned an error: %v", err)
	}
	// Replayed session/load chunks must not leak into the next turn.
	if second.Text != "Second." {
		t.Fatalf("second turn text = %q, want Second.", second.Text)
	}
}

func TestClientApprovesPermissionsWhenConfigured(t *testing.T) {
	client := startFakeClient(t, "approve", Config{Approval: ApprovalApprove})
	defer func() { _ = client.Close() }()

	session, err := client.NewSession(context.Background(), "/workspace/project")
	if err != nil {
		t.Fatalf("NewSession returned an error: %v", err)
	}
	result, err := client.RunTurn(context.Background(), session.ID, "run the tests")
	if err != nil {
		t.Fatalf("RunTurn returned an error: %v", err)
	}
	if result.Text != "Approved and done." {
		t.Fatalf("unexpected turn text: %q", result.Text)
	}
}

func TestStartReportsInitializeFailureWithStderr(t *testing.T) {
	_, err := startFakeClientResult(t, "fail-initialize", Config{})
	if err == nil || !strings.Contains(err.Error(), "initialize acp agent") {
		t.Fatalf("expected initialize error, got %v", err)
	}
	if !strings.Contains(err.Error(), "fake kimi failed") {
		t.Fatalf("expected stderr detail in error, got %v", err)
	}
}

func TestStartRejectsProtocolMismatch(t *testing.T) {
	_, err := startFakeClientResult(t, "protocol-mismatch", Config{})
	if err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("expected protocol version error, got %v", err)
	}
}

func TestRunTurnReportsCancelledStopReason(t *testing.T) {
	client := startFakeClient(t, "cancelled-turn", Config{})
	defer func() { _ = client.Close() }()

	session, err := client.NewSession(context.Background(), "/workspace/project")
	if err != nil {
		t.Fatalf("NewSession returned an error: %v", err)
	}
	_, err = client.RunTurn(context.Background(), session.ID, "hello")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestNewSessionRejectsEmptySessionID(t *testing.T) {
	client := startFakeClient(t, "empty-session", Config{})
	defer func() { _ = client.Close() }()

	_, err := client.NewSession(context.Background(), "/workspace/project")
	if err == nil || !strings.Contains(err.Error(), "empty session id") {
		t.Fatalf("expected empty session id error, got %v", err)
	}
}

func TestLoadSessionRequiresCapability(t *testing.T) {
	client := startFakeClient(t, "no-load-capability", Config{})
	defer func() { _ = client.Close() }()

	err := client.LoadSession(context.Background(), "S1", "/workspace/project")
	if !errors.Is(err, ErrLoadUnsupported) {
		t.Fatalf("expected ErrLoadUnsupported, got %v", err)
	}
}

// An agent that resolves its own model (zot, for one) answers the unstable
// session/set_model with "method not found". That is not a broken session: the
// turn still runs on the model the agent was configured with.
func TestNewSessionToleratesUnsupportedModelSelection(t *testing.T) {
	client := startFakeClient(t, "set-model-unsupported", Config{Model: "kimi-k3"})
	defer func() { _ = client.Close() }()

	session, err := client.NewSession(context.Background(), "/workspace/project")
	if err != nil {
		t.Fatalf("NewSession returned an error: %v", err)
	}
	if session.ID != "S1" {
		t.Fatalf("session ID = %q, want S1", session.ID)
	}
}

// Any other failure from session/set_model still fails the session: the agent
// implements the method and rejected this model.
func TestNewSessionFailsOnRejectedModel(t *testing.T) {
	client := startFakeClient(t, "set-model-failed", Config{Model: "kimi-k3"})
	defer func() { _ = client.Close() }()

	_, err := client.NewSession(context.Background(), "/workspace/project")
	if err == nil || !strings.Contains(err.Error(), `select acp model "kimi-k3"`) {
		t.Fatalf("expected a model selection error, got %v", err)
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
			"-test.run=TestACPHelperProcess",
			"--",
			scenario,
		), nil
	})
}

// withHelperMarker marks the re-executed test binary as the fake agent. The
// child inherits nothing from this process, so the marker has to travel
// through the configured environment like any other variable.
func withHelperMarker(env map[string]string) map[string]string {
	marked := make(map[string]string, len(env)+1)
	for key, value := range env {
		marked[key] = value
	}
	marked[helperProcessEnv] = "1"
	return marked
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	scenario := os.Args[len(os.Args)-1]
	agent := fakeAgent{
		t:       t,
		scanner: bufio.NewScanner(os.Stdin),
	}

	switch scenario {
	case "lifecycle":
		agent.initialize(true)
		agent.lifecycle()
	case "approve":
		agent.initialize(true)
		agent.approve()
	case "fail-initialize":
		fmt.Fprintln(os.Stderr, "fake kimi failed")
		os.Exit(42)
	case "protocol-mismatch":
		initialize := agent.expectMethod("initialize")
		agent.respond(initialize.ID, map[string]any{"protocolVersion": 2})
		agent.drain()
	case "cancelled-turn":
		agent.initialize(true)
		agent.newSession("S1")
		prompt := agent.expectMethod("session/prompt")
		agent.respond(prompt.ID, map[string]any{"stopReason": "cancelled"})
		agent.drain()
	case "empty-session":
		agent.initialize(true)
		agent.newSession("")
		agent.drain()
	case "no-load-capability":
		agent.initialize(false)
		agent.drain()
	case "set-model-unsupported":
		agent.initialize(true)
		agent.newSession("S1")
		setModel := agent.expectMethod("session/set_model")
		agent.respondError(setModel.ID, -32601, "Method not found")
		agent.drain()
	case "set-model-failed":
		agent.initialize(true)
		agent.newSession("S1")
		setModel := agent.expectMethod("session/set_model")
		agent.respondError(setModel.ID, -32602, "no such model")
		agent.drain()
	default:
		t.Fatalf("unknown helper process scenario %q", scenario)
	}
}

type fakeAgent struct {
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

func (a fakeAgent) initialize(loadSession bool) {
	initialize := a.expectMethod("initialize")
	var params struct {
		ProtocolVersion    int `json:"protocolVersion"`
		ClientCapabilities struct {
			FS struct {
				ReadTextFile  bool `json:"readTextFile"`
				WriteTextFile bool `json:"writeTextFile"`
			} `json:"fs"`
			Terminal bool `json:"terminal"`
		} `json:"clientCapabilities"`
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(initialize.Params, &params); err != nil {
		a.t.Fatalf("decode initialize params: %v", err)
	}
	if params.ProtocolVersion != 1 {
		a.t.Fatalf("protocolVersion = %d, want 1", params.ProtocolVersion)
	}
	if params.ClientCapabilities.FS.ReadTextFile ||
		params.ClientCapabilities.FS.WriteTextFile ||
		params.ClientCapabilities.Terminal {
		a.t.Fatalf("client unexpectedly advertised fs/terminal capabilities: %#v", params.ClientCapabilities)
	}
	if params.ClientInfo.Name != defaultClientName {
		a.t.Fatalf("unexpected client info: %#v", params.ClientInfo)
	}
	a.respond(initialize.ID, map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession":        loadSession,
			"promptCapabilities": map[string]bool{"image": true, "audio": false, "embeddedContext": true},
		},
		"authMethods": []any{},
		"agentInfo":   map[string]string{"name": "kimi-cli", "version": "9.9"},
	})
}

func (a fakeAgent) newSession(sessionID string) fakeMessage {
	create := a.expectMethod("session/new")
	var params struct {
		CWD        string `json:"cwd"`
		MCPServers []any  `json:"mcpServers"`
	}
	if err := json.Unmarshal(create.Params, &params); err != nil {
		a.t.Fatalf("decode session/new params: %v", err)
	}
	if params.CWD != "/workspace/project" {
		a.t.Fatalf("session cwd = %q, want /workspace/project", params.CWD)
	}
	if params.MCPServers == nil {
		a.t.Fatal("session/new params are missing mcpServers")
	}
	a.respond(create.ID, map[string]any{
		"sessionId": sessionID,
		"modes": map[string]any{
			"currentModeId":  "default",
			"availableModes": []any{map[string]string{"id": "default", "name": "Default"}},
		},
		"models": map[string]any{
			"currentModelId": "k2",
			"availableModels": []any{
				map[string]string{"modelId": "k2", "name": "kimi-k2.7-code"},
				map[string]string{"modelId": "k3", "name": "kimi-k3"},
			},
		},
	})
	return create
}

func (a fakeAgent) lifecycle() {
	a.newSession("S1")

	setModel := a.expectMethod("session/set_model")
	var modelParams struct {
		SessionID string `json:"sessionId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(setModel.Params, &modelParams); err != nil {
		a.t.Fatalf("decode session/set_model params: %v", err)
	}
	if modelParams.SessionID != "S1" || modelParams.ModelID != "k3" {
		a.t.Fatalf("unexpected set_model params: %#v", modelParams)
	}
	a.respond(setModel.ID, nil)

	// Sent by kimi right after session creation; the client must ignore it.
	a.notify("session/update", map[string]any{
		"sessionId": "S1",
		"update": map[string]any{
			"sessionUpdate":     "available_commands_update",
			"availableCommands": []any{},
		},
	})

	prompt := a.expectMethod("session/prompt")
	var promptParams struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(prompt.Params, &promptParams); err != nil {
		a.t.Fatalf("decode session/prompt params: %v", err)
	}
	if promptParams.SessionID != "S1" ||
		len(promptParams.Prompt) != 1 ||
		promptParams.Prompt[0].Type != "text" ||
		promptParams.Prompt[0].Text != "hello" {
		a.t.Fatalf("unexpected session/prompt params: %#v", promptParams)
	}

	a.notifyChunk("S1", "agent_thought_chunk", "thinking...")
	a.notifyChunk("S1", "agent_message_chunk", "Hello ")

	// Default policy must pick the reject option.
	a.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      "perm-1",
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "S1",
			"toolCall":  map[string]any{"toolCallId": "t1", "title": "Run tests"},
			"options": []any{
				map[string]string{"optionId": "approve", "name": "Approve once", "kind": "allow_once"},
				map[string]string{"optionId": "approve_for_session", "name": "Approve for session", "kind": "allow_always"},
				map[string]string{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
			},
		},
	})
	permission := a.read()
	if string(permission.ID) != `"perm-1"` {
		a.t.Fatalf("permission response id = %s, want perm-1", permission.ID)
	}
	var permissionResult struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(permission.Result, &permissionResult); err != nil {
		a.t.Fatalf("decode permission response: %v", err)
	}
	if permissionResult.Outcome.Outcome != "selected" || permissionResult.Outcome.OptionID != "reject" {
		a.t.Fatalf("permission outcome = %#v, want selected/reject", permissionResult.Outcome)
	}

	// The client advertises no fs capability, so out-of-contract requests get
	// a method-not-found error.
	a.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      "fs-1",
		"method":  "fs/read_text_file",
		"params":  map[string]any{"sessionId": "S1", "path": "/etc/passwd"},
	})
	fsResponse := a.read()
	var fsError struct {
		Code int64 `json:"code"`
	}
	if err := json.Unmarshal(fsResponse.Error, &fsError); err != nil {
		a.t.Fatalf("decode fs error response %q: %v", fsResponse.Error, err)
	}
	if fsError.Code != -32601 {
		a.t.Fatalf("fs error code = %d, want -32601", fsError.Code)
	}

	a.notifyChunk("S1", "agent_message_chunk", "world.")
	a.respond(prompt.ID, map[string]any{"stopReason": "end_turn"})

	load := a.expectMethod("session/load")
	var loadParams struct {
		SessionID string `json:"sessionId"`
		CWD       string `json:"cwd"`
		MCPServer []any  `json:"mcpServers"`
	}
	if err := json.Unmarshal(load.Params, &loadParams); err != nil {
		a.t.Fatalf("decode session/load params: %v", err)
	}
	if loadParams.SessionID != "S-old" || loadParams.CWD != "/workspace/project" {
		a.t.Fatalf("unexpected session/load params: %#v", loadParams)
	}
	// Replay arrives before the load response and outside any active turn.
	a.notifyChunk("S-old", "agent_message_chunk", "replayed")
	a.respond(load.ID, nil)

	second := a.expectMethod("session/prompt")
	a.notifyChunk("S-old", "agent_message_chunk", "Second.")
	a.respond(second.ID, map[string]any{"stopReason": "end_turn"})

	a.drain()
}

func (a fakeAgent) approve() {
	a.newSession("S1")

	prompt := a.expectMethod("session/prompt")
	a.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      "perm-1",
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "S1",
			"toolCall":  map[string]any{"toolCallId": "t1", "title": "Run tests"},
			"options": []any{
				map[string]string{"optionId": "approve", "name": "Approve once", "kind": "allow_once"},
				map[string]string{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
			},
		},
	})
	permission := a.read()
	var permissionResult struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(permission.Result, &permissionResult); err != nil {
		a.t.Fatalf("decode permission response: %v", err)
	}
	if permissionResult.Outcome.OptionID != "approve" {
		a.t.Fatalf("permission option = %q, want approve", permissionResult.Outcome.OptionID)
	}

	a.notifyChunk("S1", "agent_message_chunk", "Approved and done.")
	a.respond(prompt.ID, map[string]any{"stopReason": "end_turn"})
	a.drain()
}

func (a fakeAgent) notifyChunk(sessionID, kind, text string) {
	a.notify("session/update", map[string]any{
		"sessionId": sessionID,
		"update": map[string]any{
			"sessionUpdate": kind,
			"content":       map[string]string{"type": "text", "text": text},
		},
	})
}

func (a fakeAgent) expectMethod(method string) fakeMessage {
	message := a.read()
	if message.Method != method {
		a.t.Fatalf("method = %q, want %q", message.Method, method)
	}
	return message
}

func (a fakeAgent) read() fakeMessage {
	if !a.scanner.Scan() {
		if err := a.scanner.Err(); err != nil {
			a.t.Fatalf("read client message: %v", err)
		}
		a.t.Fatal("client transport closed unexpectedly")
	}
	var message fakeMessage
	if err := json.Unmarshal(a.scanner.Bytes(), &message); err != nil {
		a.t.Fatalf("decode client message %q: %v", a.scanner.Text(), err)
	}
	return message
}

func (a fakeAgent) drain() {
	for a.scanner.Scan() {
	}
	if err := a.scanner.Err(); err != nil {
		a.t.Fatalf("drain client transport: %v", err)
	}
}

func (a fakeAgent) respond(id json.RawMessage, result any) {
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if result != nil {
		message["result"] = result
	} else {
		message["result"] = nil
	}
	a.send(message)
}

func (a fakeAgent) respondError(id json.RawMessage, code int64, message string) {
	a.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func (a fakeAgent) notify(method string, params any) {
	a.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (a fakeAgent) send(message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		a.t.Fatalf("encode agent message: %v", err)
	}
	fmt.Fprintln(os.Stdout, string(payload))
}
