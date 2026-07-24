package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestClientStartsAndResumesSession(t *testing.T) {
	var calls [][]string
	factory := func(ctx context.Context, _ string, args []string) (*exec.Cmd, error) {
		calls = append(calls, append([]string(nil), args...))
		helperArgs := append([]string{"-test.run=TestClaudeHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_CLAUDE_HELPER_PROCESS=1")
		return cmd, nil
	}

	client := newClient(Config{
		Binary:          "/opt/claude",
		Workdir:         t.TempDir(),
		Model:           "sonnet",
		Effort:          "high",
		PermissionMode:  "plan",
		Instructions:    "Be concise.",
		AllowedTools:    []string{"Read", " Grep "},
		DisallowedTools: []string{"Edit", "Write"},
	}, factory)

	first, err := client.RunTurn(context.Background(), "", "first prompt")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if first.SessionID != "11111111-1111-4111-8111-111111111111" ||
		first.Text != "reply to first prompt" ||
		first.Subtype != "success" {
		t.Fatalf("unexpected first result: %+v", first)
	}

	second, err := client.RunTurn(context.Background(), first.SessionID, "second prompt")
	if err != nil {
		t.Fatalf("resumed turn: %v", err)
	}
	if second.SessionID != first.SessionID || second.Text != "reply to second prompt" {
		t.Fatalf("unexpected resumed result: %+v", second)
	}

	if len(calls) != 2 {
		t.Fatalf("command calls = %d, want 2", len(calls))
	}
	firstArgs := calls[0]
	for _, pair := range [][]string{
		{"--output-format", "stream-json"},
		{"--model", "sonnet"},
		{"--effort", "high"},
		{"--permission-mode", "plan"},
		{"--append-system-prompt", "Be concise."},
		{"--allowed-tools", "Read,Grep"},
		{"--disallowed-tools", "Edit,Write"},
	} {
		if !containsPair(firstArgs, pair[0], pair[1]) {
			t.Fatalf("first command args missing %q %q: %v", pair[0], pair[1], firstArgs)
		}
	}
	if slices.Contains(firstArgs, "--resume") {
		t.Fatalf("first turn unexpectedly resumed a session: %v", firstArgs)
	}
	if !containsPair(calls[1], "--resume", first.SessionID) {
		t.Fatalf("second command did not resume session: %v", calls[1])
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := client.RunTurn(context.Background(), "", "after close"); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestClientReportsUnsuccessfulResult(t *testing.T) {
	factory := func(ctx context.Context, _ string, args []string) (*exec.Cmd, error) {
		helperArgs := append([]string{"-test.run=TestClaudeHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_CLAUDE_HELPER_PROCESS=1")
		return cmd, nil
	}
	client := newClient(Config{}, factory)

	_, err := client.RunTurn(context.Background(), "", "return-error")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected result error, got %v", err)
	}
}

func TestClientInjectsConfiguredEnvironment(t *testing.T) {
	factory := func(ctx context.Context, _ string, args []string) (*exec.Cmd, error) {
		helperArgs := append([]string{"-test.run=TestClaudeHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_CLAUDE_HELPER_PROCESS=1")
		return cmd, nil
	}
	client := newClient(Config{
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":   "https://api.example.com/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "secret-token",
		},
	}, factory)

	result, err := client.RunTurn(context.Background(), "", "env-check")
	if err != nil {
		t.Fatalf("env-check turn: %v", err)
	}
	if result.Text != "base_url=https://api.example.com/anthropic token=secret-token" {
		t.Fatalf("child did not observe injected environment: %q", result.Text)
	}
}

func TestNewRejectsMissingBinary(t *testing.T) {
	_, err := New(Config{Binary: t.TempDir() + "/missing-claude"})
	if err == nil || !strings.Contains(err.Error(), "find claude executable") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestReadResultRejectsMissingResult(t *testing.T) {
	_, err := readResult(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"11111111-1111-4111-8111-111111111111"}` + "\n",
	))
	if err == nil || !strings.Contains(err.Error(), "without a result") {
		t.Fatalf("expected missing result error, got %v", err)
	}
}

func containsPair(args []string, name, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name && args[index+1] == value {
			return true
		}
	}
	return false
}

func TestClaudeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLAUDE_HELPER_PROCESS") != "1" {
		return
	}

	prompt, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	sessionID := "11111111-1111-4111-8111-111111111111"
	args := os.Args
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--resume" {
			sessionID = args[index+1]
		}
	}

	emitClaudeHelperMessage(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": sessionID,
	})
	if string(prompt) == "env-check" {
		emitClaudeHelperMessage(map[string]any{
			"type":     "result",
			"subtype":  "success",
			"is_error": false,
			"result": fmt.Sprintf(
				"base_url=%s token=%s",
				os.Getenv("ANTHROPIC_BASE_URL"),
				os.Getenv("ANTHROPIC_AUTH_TOKEN"),
			),
			"session_id": sessionID,
		})
		os.Exit(0)
	}
	if string(prompt) == "return-error" {
		emitClaudeHelperMessage(map[string]any{
			"type":       "result",
			"subtype":    "error_during_execution",
			"is_error":   true,
			"result":     "permission denied",
			"session_id": sessionID,
		})
		os.Exit(0)
	}
	emitClaudeHelperMessage(map[string]any{
		"type":       "result",
		"subtype":    "success",
		"is_error":   false,
		"result":     fmt.Sprintf("reply to %s", prompt),
		"session_id": sessionID,
	})
	os.Exit(0)
}

func emitClaudeHelperMessage(message map[string]any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		os.Exit(2)
	}
	fmt.Println(string(encoded))
}
