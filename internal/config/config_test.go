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

// --- Agent config tests ---

const minimalBot = `
bots:
  - name: bot
    type: discord
    bot_token: tok
`

func TestLoad_AgentStringCommand(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: code-review
    command: claude -p "Check notifications"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	if cfg.Agents[0].Name != "code-review" {
		t.Errorf("expected agent name 'code-review', got %q", cfg.Agents[0].Name)
	}
	if len(cfg.Agents[0].Command) != 3 {
		t.Errorf("expected 3 command tokens, got %v", cfg.Agents[0].Command)
	}
}

func TestLoad_AgentArrayCommand(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: code-review
    command:
      - claude
      - -p
      - "Check notifications"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	if len(cfg.Agents[0].Command) != 3 {
		t.Errorf("expected 3 command tokens, got %v", cfg.Agents[0].Command)
	}
}

func TestLoad_AgentWithAllFields(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: triage
    when: "direct || mentions"
    command: aider --check
    workdir: /tmp/project
    buffer: 10
    timeout: 300
    cooldown: 120
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Agents[0]
	if a.When != "direct || mentions" {
		t.Errorf("unexpected when: %q", a.When)
	}
	if a.Workdir != "/tmp/project" {
		t.Errorf("unexpected workdir: %q", a.Workdir)
	}
	if a.Buffer != 10 {
		t.Errorf("expected buffer=10, got %d", a.Buffer)
	}
	if a.Timeout != 300 {
		t.Errorf("expected timeout=300, got %d", a.Timeout)
	}
	if a.Cooldown != 120 {
		t.Errorf("expected cooldown=120, got %d", a.Cooldown)
	}
}

func TestLoad_AgentEmptyName(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: ""
    command: claude
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for agent with empty name")
	}
}

func TestLoad_AgentMissingCommand(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: empty-cmd
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for agent with missing command")
	}
}

func TestLoad_AgentDuplicateNames(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: reviewer
    command: claude -p test
  - name: reviewer
    command: aider --check
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate agent names")
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Errorf("error should mention agent name, got: %v", err)
	}
}

func TestLoad_AgentDisallowedCommand(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: malicious
    command: rm -rf /
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for disallowed command")
	}
	if !strings.Contains(err.Error(), "not in the allowed list") {
		t.Errorf("error should mention allowlist, got: %v", err)
	}
}

func TestLoadWithOptions_AllowExecBypassesAllowlist(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: custom
    command: my-custom-script --flag
`)
	// Without allowExec → error
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error without allow-exec")
	}

	// With allowExec → success
	cfg, err := LoadWithOptions(path, true)
	if err != nil {
		t.Fatalf("unexpected error with allow-exec: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
}

func TestLoad_AgentAllAllowedCommands(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: a1
    command: claude
  - name: a2
    command: codex
  - name: a3
    command: copilot
  - name: a4
    command: aider
  - name: a5
    command: goose
  - name: a6
    command: opencode
  - name: a7
    command: gemini
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 7 {
		t.Fatalf("expected 7 agents, got %d", len(cfg.Agents))
	}
}

func TestLoad_AgentCommandWithPath(t *testing.T) {
	// filepath.Base should extract the binary name for allowlist check
	path := writeConfig(t, minimalBot+`
agents:
  - name: pathed
    command: /usr/local/bin/claude -p test
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
}

func TestLoad_NoAgentsIsValid(t *testing.T) {
	path := writeConfig(t, minimalBot)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(cfg.Agents))
	}
}

func TestLoad_MultipleAgents(t *testing.T) {
	path := writeConfig(t, minimalBot+`
agents:
  - name: reviewer
    when: direct
    command: claude -p "review code"
  - name: triage
    when: mentions
    command: aider --check
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(cfg.Agents))
	}
	if cfg.Agents[0].Name != "reviewer" {
		t.Errorf("expected first agent 'reviewer', got %q", cfg.Agents[0].Name)
	}
	if cfg.Agents[1].Name != "triage" {
		t.Errorf("expected second agent 'triage', got %q", cfg.Agents[1].Name)
	}
}

// --- Path function tests ---

func TestDefaultConfigPath_EnvOverride(t *testing.T) {
	t.Setenv("PANTALK_CONFIG", "/custom/pantalk.yaml")
	got := DefaultConfigPath()
	if got != "/custom/pantalk.yaml" {
		t.Errorf("expected env override, got %q", got)
	}
}

func TestDefaultConfigPath_XDGFallback(t *testing.T) {
	t.Setenv("PANTALK_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdgconf")
	got := DefaultConfigPath()
	if got != "/xdgconf/pantalk/config.yaml" {
		t.Errorf("expected XDG path, got %q", got)
	}
}

func TestDefaultSocketPath_XDGRuntime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	got := DefaultSocketPath()
	if got != "/run/user/1000/pantalk.sock" {
		t.Errorf("expected XDG runtime socket, got %q", got)
	}
}

func TestDefaultSocketPath_Fallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := DefaultSocketPath()
	if got == "" {
		t.Error("expected non-empty fallback socket path")
	}
	if !strings.HasPrefix(got, "/tmp/pantalk-") {
		t.Errorf("expected /tmp/pantalk-* fallback, got %q", got)
	}
}

func TestDefaultSkillsCachePath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdgcache")
	got := DefaultSkillsCachePath()
	if got != "/xdgcache/pantalk/skills" {
		t.Errorf("expected XDG cache path, got %q", got)
	}
}

func TestDefaultSkillsCachePath_Fallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/test")
	got := DefaultSkillsCachePath()
	if got != "/home/test/.cache/pantalk/skills" {
		t.Errorf("expected home-based cache path, got %q", got)
	}
}

func TestEnsureDir(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/sub/dir/file.db"
	if err := EnsureDir(filePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the parent directory was created
	info, err := os.Stat(dir + "/sub/dir")
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/file.db"
	// Should succeed even if dir exists
	if err := EnsureDir(filePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHomeDir_Fallback(t *testing.T) {
	t.Setenv("HOME", "")
	got := homeDir()
	if got == "" {
		t.Error("expected non-empty fallback")
	}
	if !strings.HasPrefix(got, "/tmp/pantalk-") {
		t.Errorf("expected /tmp/pantalk-* fallback, got %q", got)
	}
}

func TestHomeDir_WithEnv(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")
	got := homeDir()
	if got != "/home/testuser" {
		t.Errorf("expected HOME value, got %q", got)
	}
}

func TestXdgConfigHome_WithEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got := xdgConfigHome()
	if got != "/custom/config" {
		t.Errorf("expected custom config home, got %q", got)
	}
}

func TestXdgConfigHome_Fallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/test")
	got := xdgConfigHome()
	if got != "/home/test/.config" {
		t.Errorf("expected fallback config home, got %q", got)
	}
}

func TestXdgCacheHome_WithEnv(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	got := xdgCacheHome()
	if got != "/custom/cache" {
		t.Errorf("expected custom cache home, got %q", got)
	}
}

func TestXdgCacheHome_Fallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/test")
	got := xdgCacheHome()
	if got != "/home/test/.cache" {
		t.Errorf("expected fallback cache home, got %q", got)
	}
}

func TestXdgDataHome_WithEnv(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	got := xdgDataHome()
	if got != "/custom/data" {
		t.Errorf("expected custom data home, got %q", got)
	}
}

func TestXdgDataHome_Fallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/test")
	got := xdgDataHome()
	if got != "/home/test/.local/share" {
		t.Errorf("expected fallback data home, got %q", got)
	}
}

// --- Additional validation tests ---

func TestLoad_TelegramMissingBotToken(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: tg-bot
    type: telegram
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for telegram bot missing bot_token")
	}
	if !strings.Contains(err.Error(), "bot_token") {
		t.Errorf("error should mention bot_token, got: %v", err)
	}
}

func TestLoad_DiscordMissingBotToken(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: dc-bot
    type: discord
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for discord bot missing bot_token")
	}
}

func TestLoad_MattermostMissingBotToken(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: mm-bot
    type: mattermost
    endpoint: https://mm.example.com
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for mattermost bot missing bot_token")
	}
}

func TestLoad_CustomTypeWithTransportAndEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: custom-bot
    type: webhook
    transport: http
    endpoint: https://hook.example.com
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(cfg.Bots))
	}
}

func TestLoad_CustomTypeMissingEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: custom-bot
    type: webhook
    transport: http
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for custom type missing endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention endpoint, got: %v", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: [invalid yaml
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// --- IRC config tests ---

func TestLoad_IRCWithEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: irc-bot
    type: irc
    endpoint: irc.libera.chat:6697
    channels:
      - '#general'
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(cfg.Bots))
	}
	if cfg.Bots[0].Type != "irc" {
		t.Fatalf("expected type 'irc', got %q", cfg.Bots[0].Type)
	}
}

func TestLoad_IRCMissingEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: irc-bot
    type: irc
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for irc bot missing endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention endpoint, got: %v", err)
	}
}

func TestLoad_IRCWithOptionalPassword(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: irc-bot
    type: irc
    endpoint: irc.libera.chat:6697
    password: server-password
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Bots[0].Password != "server-password" {
		t.Fatalf("expected password to be preserved, got %q", cfg.Bots[0].Password)
	}
}

// --- Twilio validation tests ---

func TestLoad_TwilioValidConfig(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: sms-bot
    type: twilio
    auth_token: auth-token
    account_sid: AC1234567890
    phone_number: "+15551234567"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(cfg.Bots))
	}
	if cfg.Bots[0].Type != "twilio" {
		t.Fatalf("expected type twilio, got %s", cfg.Bots[0].Type)
	}
}

func TestLoad_TwilioMissingAuthToken(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: sms-bot
    type: twilio
    account_sid: AC1234567890
    phone_number: "+15551234567"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for twilio bot missing auth_token")
	}
	if !strings.Contains(err.Error(), "auth_token") {
		t.Errorf("error should mention auth_token, got: %v", err)
	}
}

func TestLoad_TwilioMissingAccountSID(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: sms-bot
    type: twilio
    auth_token: auth-token
    phone_number: "+15551234567"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for twilio bot missing account_sid")
	}
	if !strings.Contains(err.Error(), "account_sid") {
		t.Errorf("error should mention account_sid, got: %v", err)
	}
}

func TestLoad_TwilioMissingPhoneNumber(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: sms-bot
    type: twilio
    auth_token: auth-token
    account_sid: AC1234567890
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for twilio bot missing phone_number")
	}
	if !strings.Contains(err.Error(), "phone_number") {
		t.Errorf("error should mention phone_number, got: %v", err)
	}
}

// --- Zulip config tests ---

func TestLoad_ZulipValid(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: zulip-bot
    type: zulip
    api_key: api-key-123
    bot_email: bot@example.com
    endpoint: https://chat.example.com
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(cfg.Bots))
	}
	if cfg.Bots[0].Endpoint != "https://chat.example.com" {
		t.Fatalf("unexpected endpoint: %s", cfg.Bots[0].Endpoint)
	}
}

func TestLoad_ZulipMissingEndpoint(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: zulip-bot
    type: zulip
    api_key: api-key-123
    bot_email: bot@example.com
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for zulip bot missing endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention endpoint, got: %v", err)
	}
}

func TestLoad_ZulipMissingAPIKey(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: zulip-bot
    type: zulip
    bot_email: bot@example.com
    endpoint: https://chat.example.com
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for zulip bot missing api_key")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error should mention api_key, got: %v", err)
	}
}

func TestLoad_ZulipMissingBotEmail(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: zulip-bot
    type: zulip
    api_key: api-key-123
    endpoint: https://chat.example.com
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for zulip bot missing bot_email")
	}
	if !strings.Contains(err.Error(), "bot_email") {
		t.Errorf("error should mention bot_email, got: %v", err)
	}
}
