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
	SocketPath  string `yaml:"socket_path"`
	HistorySize int    `yaml:"notification_history_size"`
	DBPath      string `yaml:"db_path"`
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

// AgentConfig describes a preconfigured command that pantalkd can launch when
// matching notifications arrive. Commands are exec'd directly (no shell) so
// only explicitly listed programs can run unless --allow-exec is set.
type AgentConfig struct {
	Name     string        `yaml:"name"`
	When     string        `yaml:"when"`     // expr expression evaluated against each event (default: "notify")
	Command  agent.Command `yaml:"command"`  // string or []string — exec'd directly, never via shell
	Workdir  string        `yaml:"workdir"`  // working directory (optional)
	Buffer   int           `yaml:"buffer"`   // seconds to batch events before launching (default 30)
	Timeout  int           `yaml:"timeout"`  // max runtime in seconds (default 120)
	Cooldown int           `yaml:"cooldown"` // min seconds between consecutive runs (default 60)
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
}

func validate(cfg Config, allowExec bool) error {
	if len(cfg.Bots) == 0 {
		return errors.New("config must include at least one bot")
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
		case "whatsapp":
			// No credentials required — authentication is handled via QR code
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

		if len(a.Command) == 0 {
			return fmt.Errorf("agent %q requires command", a.Name)
		}

		// Restrict command binaries to the known allowlist unless --allow-exec.
		binary := filepath.Base(a.Command[0])
		if !allowExec && !agent.AllowedCommands[binary] {
			return fmt.Errorf("agent %q: command %q is not in the allowed list (claude, codex, copilot, aider, goose, opencode, gemini); start pantalkd with --allow-exec to permit arbitrary commands", a.Name, a.Command[0])
		}
	}

	return nil
}
