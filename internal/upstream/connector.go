package upstream

import (
	"context"
	"fmt"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/media"
	"github.com/pantalk/pantalk/internal/protocol"
)

type Connector interface {
	Run(ctx context.Context)
	Send(ctx context.Context, request protocol.Request) (protocol.Event, error)
	React(ctx context.Context, request protocol.Request) error
	Identity() string
}

// ChannelResolver canonicalizes a configured channel selector before Pantalk
// creates an initiated or scheduled conversation. Connectors that support
// friendly names return both the stable provider ID and display name.
type ChannelResolver interface {
	ResolveChannel(ctx context.Context, channel string) (id string, name string, err error)
}

// AttachmentSender is implemented by connectors that deliver the local files
// listed in Request.Attach. The daemon checks for it before dispatching a send
// that carries attachments, so a connector without support fails the request
// loudly instead of sending the caption and silently dropping the files.
type AttachmentSender interface {
	SupportsAttachments() bool
}

// TypingIndicator is implemented by connectors that can show a "bot is
// typing..." status in the destination channel. A single call produces one
// pulse; platforms let the status decay after a few seconds, so the daemon's
// typing lease re-pulses on a cadence until the reply is sent.
//
// Implemented by: telegram. Remaining connectors with a native equivalent
// that should grow this: slack, discord, mattermost, matrix, whatsapp, irc
// (via /me fallback is questionable - likely never), twilio (no equivalent),
// zulip, imessage.
type TypingIndicator interface {
	Typing(ctx context.Context, request protocol.Request) error
}

// NewConnector builds the connector for a bot. The media store is handed to
// connectors that can carry attachments; those that cannot simply ignore it.
func NewConnector(bot config.BotConfig, publish func(protocol.Event), attachments media.Store) (Connector, error) {
	if attachments == nil {
		attachments = media.NoopStore{}
	}

	switch bot.Type {
	case "local":
		return NewLocalConnector(bot.Name, publish), nil
	case "slack":
		return NewSlackConnector(bot, publish)
	case "discord":
		return NewDiscordConnector(bot, publish)
	case "mattermost":
		return NewMattermostConnector(bot, publish)
	case "telegram":
		return NewTelegramConnector(bot, publish, attachments)
	case "whatsapp":
		return NewWhatsAppConnector(bot, publish)
	case "irc":
		return NewIRCConnector(bot, publish)
	case "matrix":
		return NewMatrixConnector(bot, publish)
	case "twilio":
		return NewTwilioConnector(bot, publish)
	case "zulip":
		return NewZulipConnector(bot, publish)
	case "imessage":
		return NewIMessageConnector(bot, publish)
	default:
		if bot.Transport == "" {
			return nil, fmt.Errorf("bot %q requires either supported type or transport", bot.Name)
		}
		return NewMockConnector(bot.Type, bot.Name, publish), nil
	}
}
