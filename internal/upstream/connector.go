package upstream

import (
	"context"
	"fmt"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/protocol"
)

type Connector interface {
	Run(ctx context.Context)
	Send(ctx context.Context, request protocol.Request) (protocol.Event, error)
	Identity() string
}

func NewConnector(bot config.BotConfig, publish func(protocol.Event)) (Connector, error) {
	switch bot.Type {
	case "slack":
		return NewSlackConnector(bot, publish)
	case "discord":
		return NewDiscordConnector(bot, publish)
	case "mattermost":
		return NewMattermostConnector(bot, publish)
	case "telegram":
		return NewTelegramConnector(bot, publish)
	default:
		if bot.Transport == "" {
			return nil, fmt.Errorf("bot %q requires either supported type or transport", bot.Name)
		}
		return NewMockConnector(bot.Type, bot.Name, publish), nil
	}
}
