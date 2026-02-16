package upstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/protocol"
)

type DiscordConnector struct {
	serviceName string
	botName     string
	publish     func(protocol.Event)
	session     *discordgo.Session

	mu        sync.RWMutex
	channels  map[string]struct{}
	selfUser  string
	selfBotID string
}

func NewDiscordConnector(service config.ServiceConfig, bot config.BotConfig, publish func(protocol.Event)) (*DiscordConnector, error) {
	token, err := config.ResolveCredential(bot.BotToken)
	if err != nil {
		return nil, fmt.Errorf("resolve discord bot_token for bot %q: %w", bot.Name, err)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent

	connector := &DiscordConnector{
		serviceName: service.Name,
		botName:     bot.Name,
		publish:     publish,
		session:     session,
		channels:    make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		connector.channels[trimmed] = struct{}{}
	}

	session.AddHandler(connector.onMessageCreate)

	return connector, nil
}

func (d *DiscordConnector) Run(ctx context.Context) {
	if err := d.session.Open(); err != nil {
		d.publishStatus("discord connect failed: " + err.Error())
		return
	}

	if stateUser := d.session.State.User; stateUser != nil {
		d.mu.Lock()
		d.selfUser = stateUser.ID
		d.selfBotID = stateUser.ID
		d.mu.Unlock()
	}

	d.publishStatus("connector online")

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = d.session.Close()
			d.publishStatus("connector offline")
			return
		case <-heartbeatTicker.C:
			d.publishHeartbeat()
		}
	}
}

func (d *DiscordConnector) Send(_ context.Context, request protocol.Request) (protocol.Event, error) {
	trimmed := strings.TrimSpace(request.Text)
	if trimmed == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	channel := resolveDiscordChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("discord send requires channel or target")
	}

	d.rememberChannel(channel)

	message := &discordgo.MessageSend{Content: trimmed}

	if request.Thread != "" {
		message.Reference = &discordgo.MessageReference{MessageID: request.Thread, ChannelID: channel}
	}

	posted, err := d.session.ChannelMessageSendComplex(channel, message)
	if err != nil {
		return protocol.Event{}, err
	}

	event := protocol.Event{
		Timestamp: posted.Timestamp,
		Service:   d.serviceName,
		Bot:       d.botName,
		Kind:      "message",
		Direction: "out",
		Target:    request.Target,
		Channel:   posted.ChannelID,
		Thread:    request.Thread,
		Text:      trimmed,
	}

	d.publish(event)

	return event, nil
}

func (d *DiscordConnector) onMessageCreate(_ *discordgo.Session, message *discordgo.MessageCreate) {
	if message == nil || message.Message == nil {
		return
	}

	if d.isSelfMessage(message) {
		return
	}

	if !d.acceptsChannel(message.ChannelID) {
		return
	}

	thread := ""
	if message.MessageReference != nil {
		thread = message.MessageReference.MessageID
	}

	event := protocol.Event{
		Timestamp: message.Timestamp,
		Service:   d.serviceName,
		Bot:       d.botName,
		Kind:      "message",
		Direction: "in",
		Target:    "channel:" + message.ChannelID,
		Channel:   message.ChannelID,
		Thread:    thread,
		Text:      message.Content,
	}

	d.publish(event)
}

func (d *DiscordConnector) publishStatus(text string) {
	d.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   d.serviceName,
		Bot:       d.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (d *DiscordConnector) publishHeartbeat() {
	d.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   d.serviceName,
		Bot:       d.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (d *DiscordConnector) rememberChannel(channel string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.channels[channel] = struct{}{}
}

func (d *DiscordConnector) acceptsChannel(channel string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.channels) == 0 {
		return true
	}

	_, ok := d.channels[channel]
	return ok
}

func (d *DiscordConnector) isSelfMessage(message *discordgo.MessageCreate) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if message.Author == nil {
		return false
	}

	if d.selfUser != "" && message.Author.ID == d.selfUser {
		return true
	}

	if d.selfBotID != "" && message.Author.ID == d.selfBotID {
		return true
	}

	return false
}

func resolveDiscordChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"channel:", "discord:channel:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}
