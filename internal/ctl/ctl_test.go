package ctl

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pantalk/pantalk/internal/config"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pantalk.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	os.Stdout = originalStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

func TestRunConfigAddBot_Matrix(t *testing.T) {
	configPath := writeTestConfig(t, `
bots:
  - name: existing
    type: discord
    bot_token: discord-token
`)

	err := runConfigAddBot([]string{
		"--config", configPath,
		"--name", "matrix-bot",
		"--type", "matrix",
		"--endpoint", "https://matrix.example.com",
		"--access-token", "matrix-access-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(cfg.Bots))
	}

	var matrix config.BotConfig
	for _, bot := range cfg.Bots {
		if bot.Name == "matrix-bot" {
			matrix = bot
			break
		}
	}

	if matrix.Type != "matrix" {
		t.Fatalf("expected matrix type, got %q", matrix.Type)
	}
	if matrix.Endpoint != "https://matrix.example.com" {
		t.Fatalf("unexpected matrix endpoint: %q", matrix.Endpoint)
	}
	if matrix.AccessToken != "matrix-access-token" {
		t.Fatalf("unexpected matrix access token: %q", matrix.AccessToken)
	}
}

func TestChooseProvider_MatrixByNumber(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("7\n"))
	provider, err := chooseProvider(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != "matrix" {
		t.Fatalf("expected matrix provider, got %q", provider)
	}
}

func TestRunConfigListBots_Text(t *testing.T) {
	configPath := writeTestConfig(t, `
bots:
  - name: primary
    type: slack
    display_name: Primary Bot
    bot_token: slack-token
    app_level_token: app-token
  - name: backup
    type: telegram
    bot_token: telegram-token
`)

	output := captureStdout(t, func() {
		if err := runConfigListBots([]string{"--config", configPath}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d (%q)", len(lines), output)
	}
	if lines[0] != "primary\tslack\tPrimary Bot" {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if lines[1] != "backup\ttelegram\t" {
		t.Fatalf("unexpected second line: %q", lines[1])
	}
}

func TestRunConfigListBots_JSONRedactsCredentials(t *testing.T) {
	configPath := writeTestConfig(t, `
bots:
  - name: secure
    type: slack
    display_name: Secure Bot
    bot_token: super-secret
    app_level_token: app-secret
`)

	output := captureStdout(t, func() {
		if err := runConfigListBots([]string{"--config", configPath, "--json"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var listed []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 listed bot, got %d", len(listed))
	}
	if listed[0].Name != "secure" || listed[0].Type != "slack" || listed[0].DisplayName != "Secure Bot" {
		t.Fatalf("unexpected bot summary: %+v", listed[0])
	}
	if strings.Contains(output, "super-secret") || strings.Contains(output, "app-secret") {
		t.Fatalf("json output must not include credentials: %q", output)
	}
}
