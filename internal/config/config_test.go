package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCredential_Literal(t *testing.T) {
	val, err := resolveCredentialHelper(t, "xoxb-token-value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "xoxb-token-value" {
		t.Fatalf("expected literal value, got %q", val)
	}
}

func TestResolveCredential_LiteralWithWhitespace(t *testing.T) {
	val, err := resolveCredentialHelper(t, "  xoxb-token  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "xoxb-token" {
		t.Fatalf("expected trimmed value, got %q", val)
	}
}

func TestResolveCredential_EnvVar(t *testing.T) {
	t.Setenv("PANTALK_TEST_TOKEN", "secret-from-env")
	val, err := resolveCredentialHelper(t, "$PANTALK_TEST_TOKEN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "secret-from-env" {
		t.Fatalf("expected env value, got %q", val)
	}
}

func TestResolveCredential_EnvVarBraces(t *testing.T) {
	t.Setenv("PANTALK_TEST_TOKEN2", "braced-value")
	val, err := resolveCredentialHelper(t, "${PANTALK_TEST_TOKEN2}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "braced-value" {
		t.Fatalf("expected env value, got %q", val)
	}
}

func TestResolveCredential_Empty(t *testing.T) {
	_, err := ResolveCredential("")
	if err == nil {
		t.Fatal("expected error for empty credential")
	}
}

func TestResolveCredential_EnvNotSet(t *testing.T) {
	_, err := ResolveCredential("$PANTALK_NONEXISTENT_VAR_12345")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestResolveCredential_DollarOnly(t *testing.T) {
	_, err := ResolveCredential("$")
	if err == nil {
		t.Fatal("expected error for bare dollar sign")
	}
}

func resolveCredentialHelper(t *testing.T, value string) (string, error) {
	t.Helper()
	return ResolveCredential(value)
}

// --- Load / Validate tests ---

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pantalk.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoad_MinimalSlackConfig(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot-a
    type: slack
    bot_token: literal-token
    app_level_token: xapp-token
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(cfg.Bots))
	}
	if cfg.Bots[0].Name != "bot-a" {
		t.Fatalf("unexpected bot name: %s", cfg.Bots[0].Name)
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot-x
    type: discord
    bot_token: discord-token
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.SocketPath != DefaultSocketPath() {
		t.Fatalf("expected default socket path, got %q", cfg.Server.SocketPath)
	}
	if cfg.Server.DBPath != DefaultDBPath() {
		t.Fatalf("expected default db path, got %q", cfg.Server.DBPath)
	}
	if cfg.Server.HistorySize != defaultHistory {
		t.Fatalf("expected default history %d, got %d", defaultHistory, cfg.Server.HistorySize)
	}
}

func TestLoad_ExplicitServerConfig(t *testing.T) {
	path := writeConfig(t, `
server:
  socket_path: /custom/sock
  db_path: /custom/db
  notification_history_size: 2000
bots:
  - name: alerts
    type: telegram
    bot_token: tg-token
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.SocketPath != "/custom/sock" {
		t.Fatalf("expected custom socket path, got %q", cfg.Server.SocketPath)
	}
	if cfg.Server.DBPath != "/custom/db" {
		t.Fatalf("expected custom db path, got %q", cfg.Server.DBPath)
	}
	if cfg.Server.HistorySize != 2000 {
		t.Fatalf("expected 2000 history size, got %d", cfg.Server.HistorySize)
	}
}

func TestLoad_NoBots(t *testing.T) {
	path := writeConfig(t, `
bots: []
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty bots")
	}
}

func TestLoad_BotMissingType(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    bot_token: tok
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for bot missing type")
	}
}

func TestLoad_DuplicateBot(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: same
    type: discord
    bot_token: t1
  - name: same
    type: discord
    bot_token: t2
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate bot name")
	}
}

func TestLoad_DuplicateBotAcrossTypes(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: ops-bot
    type: slack
    bot_token: t1
    app_level_token: a1
  - name: ops-bot
    type: discord
    bot_token: t2
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate bot name across types")
	}
	if !strings.Contains(err.Error(), "ops-bot") {
		t.Fatalf("error should mention bot name, got: %v", err)
	}
}

func TestLoad_SlackMissingAppLevelToken(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    type: slack
    bot_token: tok
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for slack bot missing app_level_token")
	}
}

func TestLoad_MattermostRequiresEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    type: mattermost
    bot_token: tok
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for mattermost without endpoint")
	}
}

func TestLoad_MattermostWithEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    type: mattermost
    bot_token: tok
    endpoint: https://mm.example.com
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Bots[0].Endpoint != "https://mm.example.com" {
		t.Fatalf("unexpected endpoint: %s", cfg.Bots[0].Endpoint)
	}
}

func TestLoad_CustomTypeRequiresTransportAndEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    type: custom-chat
    bot_token: tok
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for custom type without transport")
	}
}

func TestLoad_UnknownField(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: bot
    type: slack
    bogus_field: true
    bot_token: tok
    app_level_token: at
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown YAML field")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/pantalk.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_MultiType(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: s1
    type: slack
    bot_token: t1
    app_level_token: a1
  - name: d1
    type: discord
    bot_token: t1
  - name: tg1
    type: telegram
    bot_token: t1
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 3 {
		t.Fatalf("expected 3 bots, got %d", len(cfg.Bots))
	}
}

func TestLoad_MultiBotSameType(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: ops-bot
    type: slack
    bot_token: t1
    app_level_token: a1
  - name: eng-bot
    type: slack
    bot_token: t2
    app_level_token: a2
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(cfg.Bots))
	}
}

func TestLoad_BotEmptyName(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: ""
    type: discord
    bot_token: tok
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for bot with empty name")
	}
}
