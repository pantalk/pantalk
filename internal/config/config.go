package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/pantalk/pantalk/internal/agent"
	"gopkg.in/yaml.v3"
)

const defaultHistory = 500

type Config struct {
	Server ServerConfig  `yaml:"server"`
	Bots   []BotConfig   `yaml:"bots"`
	Agents []AgentConfig `yaml:"agents"`
}

type ServerConfig struct {
	SocketPath  string      `yaml:"socket_path"`
	HistorySize int         `yaml:"notification_history_size"`
	DBPath      string      `yaml:"db_path"`
	Media       MediaConfig `yaml:"media"`
}

// Media backend identifiers.
const (
	// MediaBackendFS keeps attachments on the local filesystem.
	MediaBackendFS = "fs"
	// MediaBackendNone disables attachment storage entirely. Inbound
	// attachments are still reported as metadata, but no bytes are fetched.
	MediaBackendNone = "none"
)

// defaultMediaMaxBytes caps a single stored attachment at 20 MiB, which sits
// just above Telegram's 20 MB Bot API download limit.
const defaultMediaMaxBytes int64 = 20 << 20

// MediaConfig controls where inbound attachments are stored and how large they
// may be. The backend is deliberately pluggable - `fs` is the only
// implementation today, but the indirection means object storage can be added
// without changing the config surface users already wrote.
type MediaConfig struct {
	Backend  string `yaml:"backend"`   // "fs" (default) or "none" to disable downloads
	Path     string `yaml:"path"`      // storage root, fs backend only
	MaxBytes int64  `yaml:"max_bytes"` // per-file cap in bytes (0 = default 20 MiB)

	// AttachRoots lists the directories outbound attachments may be read
	// from. Send requests referencing files outside every root are refused.
	// Empty means outbound attachments are disabled entirely - the daemon
	// auto-launches agents, and an allowlist-by-default posture (mirroring
	// the agent command allowlist) keeps a prompt-injected agent from
	// attaching arbitrary readable files like ~/.ssh/id_ed25519.
	AttachRoots []string `yaml:"attach_roots"`
}

type BotConfig struct {
	Name          string            `yaml:"name"`
	Type          string            `yaml:"type"`
	DisplayName   string            `yaml:"display_name"`
	About         string            `yaml:"about"`   // profile bio, where the platform has one (nostr kind:0)
	Picture       string            `yaml:"picture"` // profile avatar URL, where the platform has one (nostr kind:0)
	Username      string            `yaml:"username"`
	JID           string            `yaml:"jid"`
	BotToken      string            `yaml:"bot_token"`
	AppLevelToken string            `yaml:"app_level_token"`
	Transport     string            `yaml:"transport"`
	Endpoint      string            `yaml:"endpoint"`
	Password      string            `yaml:"password"`
	AuthToken     string            `yaml:"auth_token"`
	AccountSID    string            `yaml:"account_sid"`
	PhoneNumber   string            `yaml:"phone_number"`
	APIKey        string            `yaml:"api_key"`
	BotEmail      string            `yaml:"bot_email"`
	AccessToken   string            `yaml:"access_token"`
	PrivateKey    string            `yaml:"private_key"`
	DBPath        string            `yaml:"db_path"`
	Relays        []string          `yaml:"relays"`
	Channels      []string          `yaml:"channels"`
	Agents        []BotAgentBinding `yaml:"agents"`
}

// BotAgentBinding routes events from one bot to a reusable agent definition.
// Message bindings are evaluated in order and the first match wins. Bindings
// containing time expressions are also evaluated by the clock scheduler.
type BotAgentBinding struct {
	Name     string `yaml:"name"`     // stable rule identifier; required for time-based bindings
	Agent    string `yaml:"agent"`    // name from the top-level agents section
	When     string `yaml:"when"`     // expression evaluated against bot events (default: notify)
	Prompt   string `yaml:"prompt"`   // scheduled turn text; required for time-based bindings
	Timezone string `yaml:"timezone"` // IANA timezone for time expressions (default: UTC)
	Channel  string `yaml:"channel"`  // optional scheduled response channel
	Target   string `yaml:"target"`   // optional scheduled response target
}

// AgentConfig describes how to run one reusable agent. Event selection belongs
// to BotAgentBinding so one definition can be reused without duplicating
// driver, workdir, and instruction settings.
type AgentConfig struct {
	Name         string            `yaml:"name"`
	Driver       string            `yaml:"driver"`       // command (default when command is set), codex, claude, or acp
	Command      agent.Command     `yaml:"command"`      // command and acp drivers; exec'd directly, never via shell
	Workdir      string            `yaml:"workdir"`      // working directory (optional)
	Instructions string            `yaml:"instructions"` // persistent-agent developer instructions
	Env          map[string]string `yaml:"env"`          // the agent process environment; $ENV_VAR values are resolved
	EnvInherit   []string          `yaml:"env_inherit"`  // daemon variables copied into the agent process; nothing is inherited otherwise
	Buffer       int               `yaml:"buffer"`       // command driver: batch window in seconds (default 30)
	Timeout      int               `yaml:"timeout"`      // max command/turn runtime in seconds
	Cooldown     int               `yaml:"cooldown"`     // command driver: min seconds between runs (default 60)
	Isolation    agent.Isolation   `yaml:"isolation"`    // where the harness runs; `container` or a block. Default: same host as the daemon
	Codex        CodexAgentConfig  `yaml:"codex"`        // codex driver overrides; omitted values inherit local Codex config
	Claude       ClaudeAgentConfig `yaml:"claude"`       // claude driver overrides; omitted values inherit local Claude Code config
	ACP          ACPAgentConfig    `yaml:"acp"`          // acp driver overrides for the agent named by command
}

// CodexAgentConfig contains optional native app-server overrides. Authentication
// and all omitted settings are inherited from the user's local Codex install.
type CodexAgentConfig struct {
	Binary         string `yaml:"binary"`
	Model          string `yaml:"model"`
	Effort         string `yaml:"effort"`
	Sandbox        string `yaml:"sandbox"`
	ApprovalPolicy string `yaml:"approval_policy"`
}

// ClaudeAgentConfig contains optional Claude Code CLI overrides.
// Authentication and all omitted settings are inherited from the user's local
// Claude Code installation.
type ClaudeAgentConfig struct {
	Binary          string   `yaml:"binary"`
	Model           string   `yaml:"model"`
	Effort          string   `yaml:"effort"`
	PermissionMode  string   `yaml:"permission_mode"`
	AllowedTools    []string `yaml:"allowed_tools"`
	DisallowedTools []string `yaml:"disallowed_tools"`
}

// ACPAgentConfig contains optional overrides for an agent driven over the
// Agent Client Protocol. The agent itself is named by the definition's
// command (for example `kimi acp`); authentication and all omitted settings
// are inherited from that agent's local installation.
type ACPAgentConfig struct {
	Model    string `yaml:"model"`    // optional; otherwise the agent's local default
	Approval string `yaml:"approval"` // tool permission requests: reject (default), approve, or approve-for-session
}

// effectiveHarness is the command an agent runs once an explicit command has
// overridden the driver's own binary. It mirrors what the server resolves at
// start, so validation reports the same invocation that will actually run.
func effectiveHarness(a AgentConfig, driver string) agent.Command {
	if len(a.Command) > 0 {
		return agent.Command(a.Command)
	}
	switch driver {
	case "codex":
		if binary := strings.TrimSpace(a.Codex.Binary); binary != "" {
			return agent.Command{binary}
		}
		return agent.Command{"codex"}
	case "claude":
		if binary := strings.TrimSpace(a.Claude.Binary); binary != "" {
			return agent.Command{binary}
		}
		return agent.Command{"claude"}
	default:
		return nil
	}
}

func ResolveCredential(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("credential value cannot be empty")
	}

	if strings.HasPrefix(trimmed, "$") {
		envName := strings.TrimPrefix(trimmed, "$")
		envName = strings.TrimPrefix(envName, "{")
		envName = strings.TrimSuffix(envName, "}")
		envName = strings.TrimSpace(envName)
		if envName == "" {
			return "", errors.New("credential env reference is invalid")
		}

		resolved := strings.TrimSpace(os.Getenv(envName))
		if resolved == "" {
			return "", fmt.Errorf("environment variable %q is not set", envName)
		}

		return resolved, nil
	}

	return trimmed, nil
}

func Load(path string) (Config, error) {
	return LoadWithOptions(path, false)
}

// LoadWithOptions loads and validates the config. When allowExec is false,
// agent commands are restricted to the known allowlist.
func LoadWithOptions(path string, allowExec bool) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse yaml: %w", err)
	}

	applyDefaults(&cfg)
	if err := validate(cfg, allowExec); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.SocketPath == "" {
		cfg.Server.SocketPath = DefaultSocketPath()
	}

	if cfg.Server.HistorySize <= 0 {
		cfg.Server.HistorySize = defaultHistory
	}

	if cfg.Server.DBPath == "" {
		cfg.Server.DBPath = DefaultDBPath()
	}

	if strings.TrimSpace(cfg.Server.Media.Backend) == "" {
		cfg.Server.Media.Backend = MediaBackendFS
	}

	if cfg.Server.Media.Path == "" {
		cfg.Server.Media.Path = DefaultMediaPath()
	}

	if cfg.Server.Media.MaxBytes <= 0 {
		cfg.Server.Media.MaxBytes = defaultMediaMaxBytes
	}

	// Drop blank attach roots so downstream policy checks can treat every
	// entry as meaningful.
	if len(cfg.Server.Media.AttachRoots) > 0 {
		roots := cfg.Server.Media.AttachRoots[:0]
		for _, root := range cfg.Server.Media.AttachRoots {
			if trimmed := strings.TrimSpace(root); trimmed != "" {
				roots = append(roots, trimmed)
			}
		}
		cfg.Server.Media.AttachRoots = roots
	}

	for botIndex := range cfg.Bots {
		for bindingIndex := range cfg.Bots[botIndex].Agents {
			binding := &cfg.Bots[botIndex].Agents[bindingIndex]
			if strings.TrimSpace(binding.When) == "" {
				binding.When = "notify"
			}
			if agent.IsTimeExpression(binding.When) && strings.TrimSpace(binding.Timezone) == "" {
				binding.Timezone = "UTC"
			}
		}
	}
}

func validate(cfg Config, allowExec bool) error {
	if len(cfg.Bots) == 0 {
		return errors.New("config must include at least one bot")
	}

	switch cfg.Server.Media.Backend {
	case MediaBackendFS:
		if strings.TrimSpace(cfg.Server.Media.Path) == "" {
			return errors.New("server.media.path cannot be empty for the fs backend")
		}
	case MediaBackendNone:
	default:
		return fmt.Errorf("server.media.backend %q is not supported (use %q or %q)", cfg.Server.Media.Backend, MediaBackendFS, MediaBackendNone)
	}

	seenBots := map[string]struct{}{}
	for _, bot := range cfg.Bots {
		if bot.Name == "" {
			return errors.New("bot name cannot be empty")
		}

		if strings.TrimSpace(bot.Type) == "" {
			return fmt.Errorf("bot %q requires type", bot.Name)
		}

		if _, exists := seenBots[bot.Name]; exists {
			return fmt.Errorf("duplicate bot name: %s", bot.Name)
		}
		seenBots[bot.Name] = struct{}{}

		switch bot.Type {
		case "local":
			// Offline connector for local development and deterministic tests.
			// It receives inbound messages only through the daemon socket.
		case "slack":
			if strings.TrimSpace(bot.BotToken) == "" {
				return fmt.Errorf("bot %q requires bot_token", bot.Name)
			}
			if strings.TrimSpace(bot.AppLevelToken) == "" {
				return fmt.Errorf("bot %q requires app_level_token", bot.Name)
			}
		case "discord":
			if strings.TrimSpace(bot.BotToken) == "" {
				return fmt.Errorf("bot %q requires bot_token", bot.Name)
			}
		case "mattermost":
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q requires endpoint", bot.Name)
			}
			if strings.TrimSpace(bot.BotToken) == "" {
				return fmt.Errorf("bot %q requires bot_token", bot.Name)
			}
		case "telegram":
			if strings.TrimSpace(bot.BotToken) == "" {
				return fmt.Errorf("bot %q requires bot_token", bot.Name)
			}
		case "matrix":
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q requires endpoint (Matrix homeserver URL)", bot.Name)
			}
			if strings.TrimSpace(bot.AccessToken) == "" {
				return fmt.Errorf("bot %q requires access_token (Matrix access token)", bot.Name)
			}
		case "whatsapp":
			// No credentials required - authentication is handled via QR code
			// pairing at first startup. The optional endpoint field overrides
			// the default whatsmeow database path.
		case "irc":
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q requires endpoint for irc (e.g. irc.libera.chat:6697)", bot.Name)
			}
		case "xmpp":
			if strings.TrimSpace(bot.JID) == "" {
				return fmt.Errorf("bot %q requires jid for xmpp (e.g. agent@example.com)", bot.Name)
			}
			if strings.TrimSpace(bot.Password) == "" {
				return fmt.Errorf("bot %q requires password for xmpp", bot.Name)
			}
		case "twitch":
			if strings.TrimSpace(bot.Username) == "" {
				return fmt.Errorf("bot %q requires username for twitch", bot.Name)
			}
			if strings.TrimSpace(bot.AccessToken) == "" {
				return fmt.Errorf("bot %q requires access_token for twitch", bot.Name)
			}
			if !hasNonBlankString(bot.Channels) {
				return fmt.Errorf("bot %q requires at least one channel for twitch", bot.Name)
			}
		case "nostr":
			if strings.TrimSpace(bot.PrivateKey) == "" {
				return fmt.Errorf("bot %q requires private_key for nostr", bot.Name)
			}
			if !hasNonBlankString(bot.Relays) {
				return fmt.Errorf("bot %q requires at least one relay for nostr", bot.Name)
			}
		case "twilio":
			if strings.TrimSpace(bot.AuthToken) == "" {
				return fmt.Errorf("bot %q requires auth_token (Twilio Auth Token)", bot.Name)
			}
			if strings.TrimSpace(bot.AccountSID) == "" {
				return fmt.Errorf("bot %q requires account_sid (Twilio Account SID)", bot.Name)
			}
			if strings.TrimSpace(bot.PhoneNumber) == "" {
				return fmt.Errorf("bot %q requires phone_number (Twilio phone number)", bot.Name)
			}
		case "zulip":
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q requires endpoint (Zulip server URL)", bot.Name)
			}
			if strings.TrimSpace(bot.APIKey) == "" {
				return fmt.Errorf("bot %q requires api_key (Zulip API key)", bot.Name)
			}
			if strings.TrimSpace(bot.BotEmail) == "" {
				return fmt.Errorf("bot %q requires bot_email (Zulip bot email)", bot.Name)
			}
		case "imessage":
			// Native macOS integration - no credentials required. The
			// connector reads ~/Library/Messages/chat.db directly and
			// sends via AppleScript. db_path is optional (defaults to
			// ~/Library/Messages/chat.db).
		default:
			if strings.TrimSpace(bot.Transport) == "" {
				return fmt.Errorf("bot %q transport cannot be empty for custom type %q", bot.Name, bot.Type)
			}
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q endpoint cannot be empty for custom type %q", bot.Name, bot.Type)
			}
		}
	}

	// Validate agents.
	seenAgents := map[string]struct{}{}
	for _, a := range cfg.Agents {
		if strings.TrimSpace(a.Name) == "" {
			return errors.New("agent name cannot be empty")
		}
		if _, exists := seenAgents[a.Name]; exists {
			return fmt.Errorf("duplicate agent name: %s", a.Name)
		}
		seenAgents[a.Name] = struct{}{}

		// Environment overrides apply to every driver's agent process.
		for key, value := range a.Env {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("agent %q: env contains an empty variable name", a.Name)
			}
			if strings.ContainsAny(key, "=\x00") {
				return fmt.Errorf("agent %q: env variable name %q contains an invalid character", a.Name, key)
			}
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("agent %q: env %s has an empty value", a.Name, key)
			}
		}

		if err := a.Isolation.Validate(); err != nil {
			return fmt.Errorf("agent %q: %w", a.Name, err)
		}

		driver := strings.TrimSpace(a.Driver)
		if driver == "" && len(a.Command) > 0 {
			driver = "command"
		}

		// An explicit command is executed directly whatever the driver, so the
		// allowlist applies to it exactly as it does for the command and acp
		// drivers.
		if len(a.Command) > 0 && (driver == "codex" || driver == "claude") {
			binary := filepath.Base(a.Command[0])
			if !allowExec && !agent.AllowedCommands[binary] {
				return fmt.Errorf("agent %q: command %q is not in the allowed list (%s); start pantalkd with --allow-exec to permit arbitrary commands", a.Name, a.Command[0], strings.Join(agent.AllowedCommandNames(), ", "))
			}
		}

		// Resolve the container invocation now so an unknown image is reported
		// while loading the config rather than when the agent first starts.
		if a.Isolation.Enabled() {
			if _, err := a.Isolation.Wrap(a.Name, effectiveHarness(a, driver), a.Env); err != nil {
				return err
			}
		}

		switch driver {
		case "command":
			if len(a.Command) == 0 {
				return fmt.Errorf("agent %q requires command", a.Name)
			}

			// Restrict command binaries to the known allowlist unless
			// --allow-exec. Commands are executed directly, never by a shell.
			binary := filepath.Base(a.Command[0])
			if !allowExec && !agent.AllowedCommands[binary] {
				return fmt.Errorf("agent %q: command %q is not in the allowed list (%s); start pantalkd with --allow-exec to permit arbitrary commands", a.Name, a.Command[0], strings.Join(agent.AllowedCommandNames(), ", "))
			}
		case "codex":
			if len(a.Command) > 0 && strings.TrimSpace(a.Codex.Binary) != "" {
				return fmt.Errorf("agent %q: set either command or codex.binary, not both", a.Name)
			}
			switch a.Codex.Sandbox {
			case "", "read-only", "workspace-write", "danger-full-access":
			default:
				return fmt.Errorf("agent %q: unsupported codex sandbox %q", a.Name, a.Codex.Sandbox)
			}
			switch a.Codex.ApprovalPolicy {
			case "", "untrusted", "on-request", "never":
			default:
				return fmt.Errorf("agent %q: unsupported codex approval_policy %q", a.Name, a.Codex.ApprovalPolicy)
			}
		case "claude":
			if len(a.Command) > 0 && strings.TrimSpace(a.Claude.Binary) != "" {
				return fmt.Errorf("agent %q: set either command or claude.binary, not both", a.Name)
			}
			switch a.Claude.PermissionMode {
			case "", "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan":
			default:
				return fmt.Errorf("agent %q: unsupported claude permission_mode %q", a.Name, a.Claude.PermissionMode)
			}
		case "acp":
			if len(a.Command) == 0 {
				return fmt.Errorf("agent %q: acp driver requires command naming the agent, e.g. \"kimi acp\"", a.Name)
			}

			// The acp driver executes the configured agent directly, so the
			// command-driver allowlist applies unless --allow-exec.
			binary := filepath.Base(a.Command[0])
			if !allowExec && !agent.AllowedCommands[binary] {
				return fmt.Errorf("agent %q: command %q is not in the allowed list (%s); start pantalkd with --allow-exec to permit arbitrary commands", a.Name, a.Command[0], strings.Join(agent.AllowedCommandNames(), ", "))
			}
			switch a.ACP.Approval {
			case "", "reject", "approve", "approve-for-session":
			default:
				return fmt.Errorf("agent %q: unsupported acp approval %q (use reject, approve, or approve-for-session)", a.Name, a.ACP.Approval)
			}
		case "":
			return fmt.Errorf("agent %q requires driver or command", a.Name)
		default:
			return fmt.Errorf("agent %q: unsupported driver %q (use command, codex, claude, or acp)", a.Name, driver)
		}
	}

	for _, bot := range cfg.Bots {
		seenBindings := make(map[string]struct{}, len(bot.Agents))
		for index, binding := range bot.Agents {
			ruleLabel := fmt.Sprintf("bot %q agent binding %d", bot.Name, index+1)
			agentName := strings.TrimSpace(binding.Agent)
			if agentName == "" {
				return fmt.Errorf("%s requires agent", ruleLabel)
			}
			if _, exists := seenAgents[agentName]; !exists {
				return fmt.Errorf("%s references unknown agent %q", ruleLabel, agentName)
			}

			bindingName := strings.TrimSpace(binding.Name)
			if bindingName != "" {
				if _, exists := seenBindings[bindingName]; exists {
					return fmt.Errorf("bot %q contains duplicate agent binding name %q", bot.Name, bindingName)
				}
				seenBindings[bindingName] = struct{}{}
			}

			if err := agent.ValidateWhen(binding.When); err != nil {
				return fmt.Errorf("%s: %w", ruleLabel, err)
			}

			timeBased := agent.IsTimeExpression(binding.When)
			if !timeBased {
				if strings.TrimSpace(binding.Prompt) != "" {
					return fmt.Errorf("%s: prompt requires a time-based when expression", ruleLabel)
				}
				if strings.TrimSpace(binding.Timezone) != "" {
					return fmt.Errorf("%s: timezone requires a time-based when expression", ruleLabel)
				}
				if strings.TrimSpace(binding.Channel) != "" || strings.TrimSpace(binding.Target) != "" {
					return fmt.Errorf("%s: channel and target are only valid for time-based bindings", ruleLabel)
				}
				continue
			}

			if bindingName == "" {
				return fmt.Errorf("%s: time-based binding requires name", ruleLabel)
			}
			if strings.TrimSpace(binding.Prompt) == "" {
				return fmt.Errorf("%s: time-based binding requires prompt", ruleLabel)
			}
			if _, err := time.LoadLocation(binding.Timezone); err != nil {
				return fmt.Errorf("%s: invalid timezone %q: %w", ruleLabel, binding.Timezone, err)
			}
			if strings.TrimSpace(binding.Channel) != "" && strings.TrimSpace(binding.Target) != "" {
				return fmt.Errorf("%s: channel and target cannot both be set", ruleLabel)
			}
			if bot.Type != "local" &&
				strings.TrimSpace(binding.Channel) == "" &&
				strings.TrimSpace(binding.Target) == "" {
				return fmt.Errorf("%s: scheduled binding on %s requires channel or target", ruleLabel, bot.Type)
			}
		}
	}

	return nil
}

func hasNonBlankString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
