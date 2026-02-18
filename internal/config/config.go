package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultHistory = 500

type Config struct {
	Server ServerConfig `yaml:"server"`
	Bots   []BotConfig  `yaml:"bots"`
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
	Channels      []string `yaml:"channels"`
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
	if err := validate(cfg); err != nil {
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

func validate(cfg Config) error {
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
		default:
			if strings.TrimSpace(bot.Transport) == "" {
				return fmt.Errorf("bot %q transport cannot be empty for custom type %q", bot.Name, bot.Type)
			}
			if strings.TrimSpace(bot.Endpoint) == "" {
				return fmt.Errorf("bot %q endpoint cannot be empty for custom type %q", bot.Name, bot.Type)
			}
		}
	}

	return nil
}
