package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Name          string   `yaml:"name"`
	Type          string   `yaml:"type"`
	DisplayName   string   `yaml:"display_name"`
	BotToken      string   `yaml:"bot_token"`
	AppLevelToken string   `yaml:"app_level_token"`
	Transport     string   `yaml:"transport"`
	Endpoint      string   `yaml:"endpoint"`
	Password      string   `yaml:"password"`
	AuthToken     string   `yaml:"auth_token"`
	AccountSID    string   `yaml:"account_sid"`
	PhoneNumber   string   `yaml:"phone_number"`
	APIKey        string   `yaml:"api_key"`
	BotEmail      string   `yaml:"bot_email"`
	AccessToken   string   `yaml:"access_token"`
	DBPath        string   `yaml:"db_path"`
	Channels      []string `yaml:"channels"`
}

// AgentConfig describes either a command launched for matching notifications
// or a persistent native agent connection. Bots select the messaging
// connections an agent may consume; leaving it empty retains the legacy
// command-runner behavior of matching every bot.
type AgentConfig struct {
	Name         string            `yaml:"name"`
	Driver       string            `yaml:"driver"`       // command (default for legacy configs), codex, or claude
	Bots         []string          `yaml:"bots"`         // configured bot names this agent handles
	When         string            `yaml:"when"`         // expr expression evaluated against each event (default: "notify")
	Command      agent.Command     `yaml:"command"`      // command driver only; exec'd directly, never via shell
	Workdir      string            `yaml:"workdir"`      // working directory (optional)
	Instructions string            `yaml:"instructions"` // persistent-agent developer instructions
	Buffer       int               `yaml:"buffer"`       // command driver: batch window in seconds (default 30)
	Timeout      int               `yaml:"timeout"`      // max command/turn runtime in seconds
	Cooldown     int               `yaml:"cooldown"`     // command driver: min seconds between runs (default 60)
	Codex        CodexAgentConfig  `yaml:"codex"`        // codex driver overrides; omitted values inherit local Codex config
	Claude       ClaudeAgentConfig `yaml:"claude"`       // claude driver overrides; omitted values inherit local Claude Code config
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

		seenAgentBots := map[string]struct{}{}
		for _, botName := range a.Bots {
			botName = strings.TrimSpace(botName)
			if botName == "" {
				return fmt.Errorf("agent %q contains an empty bot name", a.Name)
			}
			if _, exists := seenBots[botName]; !exists {
				return fmt.Errorf("agent %q references unknown bot %q", a.Name, botName)
			}
			if _, exists := seenAgentBots[botName]; exists {
				return fmt.Errorf("agent %q references bot %q more than once", a.Name, botName)
			}
			seenAgentBots[botName] = struct{}{}
		}

		driver := strings.TrimSpace(a.Driver)
		if driver == "" && len(a.Command) > 0 {
			driver = "command"
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
				return fmt.Errorf("agent %q: command %q is not in the allowed list (claude, codex, copilot, aider, goose, opencode, gemini); start pantalkd with --allow-exec to permit arbitrary commands", a.Name, a.Command[0])
			}
		case "codex":
			if len(a.Command) > 0 {
				return fmt.Errorf("agent %q: command cannot be used with driver %q", a.Name, driver)
			}
			if len(a.Bots) == 0 {
				return fmt.Errorf("agent %q: codex driver requires at least one bot", a.Name)
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
			if len(a.Command) > 0 {
				return fmt.Errorf("agent %q: command cannot be used with driver %q", a.Name, driver)
			}
			if len(a.Bots) == 0 {
				return fmt.Errorf("agent %q: claude driver requires at least one bot", a.Name)
			}
			switch a.Claude.PermissionMode {
			case "", "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan":
			default:
				return fmt.Errorf("agent %q: unsupported claude permission_mode %q", a.Name, a.Claude.PermissionMode)
			}
		case "":
			return fmt.Errorf("agent %q requires driver or command", a.Name)
		default:
			return fmt.Errorf("agent %q: unsupported driver %q (use command, codex, or claude)", a.Name, driver)
		}
	}

	return nil
}
