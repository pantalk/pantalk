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
}

func NewConnector(service config.ServiceConfig, bot config.BotConfig, publish func(protocol.Event)) (Connector, error) {
	switch service.Name {
	case "slack":
		return NewSlackConnector(service, bot, publish)
	case "discord":
		return NewDiscordConnector(service, bot, publish)
	case "mattermost":
		return NewMattermostConnector(service, bot, publish)
	case "telegram":
		return NewTelegramConnector(service, bot, publish)
	default:
		if service.Transport == "" {
			return nil, fmt.Errorf("service %q requires either supported name or transport", service.Name)
		}
		return NewMockConnector(service.Name, bot.Name, publish), nil
	}
}
