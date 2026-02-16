package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultSocketPath = "/tmp/pantalk.sock"
	defaultHistory    = 500
	defaultPantalkDB  = "/tmp/pantalk.db"
)

type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Services []ServiceConfig `yaml:"services"`
}

type ServerConfig struct {
	SocketPath  string `yaml:"socket_path"`
	HistorySize int    `yaml:"notification_history_size"`
	DBPath      string `yaml:"db_path"`
}

type ServiceConfig struct {
	Name      string      `yaml:"name"`
	Transport string      `yaml:"transport"`
	Endpoint  string      `yaml:"endpoint"`
	Bots      []BotConfig `yaml:"bots"`
}

type BotConfig struct {
	Name          string   `yaml:"name"`
	BotID         string   `yaml:"bot_id"`
	BotToken      string   `yaml:"bot_token"`
	AppLevelToken string   `yaml:"app_level_token"`
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
		cfg.Server.SocketPath = defaultSocketPath
	}

	if cfg.Server.HistorySize <= 0 {
		cfg.Server.HistorySize = defaultHistory
	}

	if cfg.Server.DBPath == "" {
		cfg.Server.DBPath = defaultPantalkDB
	}
}

func validate(cfg Config) error {
	if len(cfg.Services) == 0 {
		return errors.New("config must include at least one service")
	}

	seenServices := map[string]struct{}{}
	for _, service := range cfg.Services {
		if service.Name == "" {
			return errors.New("service name cannot be empty")
		}

		if service.Name != "slack" && service.Name != "discord" && service.Name != "mattermost" && service.Name != "telegram" {
			if strings.TrimSpace(service.Transport) == "" {
				return fmt.Errorf("service %q transport cannot be empty", service.Name)
			}

			if strings.TrimSpace(service.Endpoint) == "" {
				return fmt.Errorf("service %q endpoint cannot be empty", service.Name)
			}
		}

		if _, exists := seenServices[service.Name]; exists {
			return fmt.Errorf("duplicate service name: %s", service.Name)
		}
		seenServices[service.Name] = struct{}{}

		if len(service.Bots) == 0 {
			return fmt.Errorf("service %q must include at least one bot", service.Name)
		}

		seenBots := map[string]struct{}{}
		for _, bot := range service.Bots {
			if bot.Name == "" {
				return fmt.Errorf("service %q has bot with empty name", service.Name)
			}

			if strings.TrimSpace(bot.BotID) == "" {
				return fmt.Errorf("service %q bot %q requires bot_id", service.Name, bot.Name)
			}

			if _, exists := seenBots[bot.Name]; exists {
				return fmt.Errorf("service %q has duplicate bot name %q", service.Name, bot.Name)
			}
			seenBots[bot.Name] = struct{}{}

			switch service.Name {
			case "slack":
				if strings.TrimSpace(bot.BotToken) == "" {
					return fmt.Errorf("service %q bot %q requires bot_token", service.Name, bot.Name)
				}
				if strings.TrimSpace(bot.AppLevelToken) == "" {
					return fmt.Errorf("service %q bot %q requires app_level_token", service.Name, bot.Name)
				}
			case "discord":
				if strings.TrimSpace(bot.BotToken) == "" {
					return fmt.Errorf("service %q bot %q requires bot_token", service.Name, bot.Name)
				}
			case "mattermost":
				if strings.TrimSpace(service.Endpoint) == "" {
					return fmt.Errorf("service %q requires endpoint", service.Name)
				}
				if strings.TrimSpace(bot.BotToken) == "" {
					return fmt.Errorf("service %q bot %q requires bot_token", service.Name, bot.Name)
				}
			case "telegram":
				if strings.TrimSpace(bot.BotToken) == "" {
					return fmt.Errorf("service %q bot %q requires bot_token", service.Name, bot.Name)
				}
			}
		}
	}

	return nil
}
