package upstream

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/protocol"
)

type SlackConnector struct {
	serviceName string
	botName     string
	publish     func(protocol.Event)
	api         *slack.Client
	socket      *socketmode.Client

	mu        sync.RWMutex
	channels  map[string]struct{}
	selfUser  string
	selfBotID string
}

func NewSlackConnector(service config.ServiceConfig, bot config.BotConfig, publish func(protocol.Event)) (*SlackConnector, error) {
	token, err := config.ResolveCredential(bot.BotToken)
	if err != nil {
		return nil, fmt.Errorf("resolve slack bot_token for bot %q: %w", bot.Name, err)
	}

	appToken, err := config.ResolveCredential(bot.AppLevelToken)
	if err != nil {
		return nil, fmt.Errorf("resolve slack app_level_token for bot %q: %w", bot.Name, err)
	}

	apiClient := slack.New(token, slack.OptionAppLevelToken(appToken))

	connector := &SlackConnector{
		serviceName: service.Name,
		botName:     bot.Name,
		publish:     publish,
		api:         apiClient,
		socket:      socketmode.New(apiClient),
		channels:    make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		connector.channels[trimmed] = struct{}{}
	}

	return connector, nil
}

func (s *SlackConnector) Run(ctx context.Context) {
	auth, err := s.api.AuthTestContext(ctx)
	if err != nil {
		s.publishStatus("slack auth failed: " + err.Error())
		return
	}

	s.mu.Lock()
	s.selfUser = auth.UserID
	s.selfBotID = auth.BotID
	s.mu.Unlock()

	go s.socket.RunContext(ctx)

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.publishStatus("connector offline")
			return
		case <-heartbeatTicker.C:
			s.publishHeartbeat()
		case event, ok := <-s.socket.Events:
			if !ok {
				s.publishStatus("socket mode event channel closed")
				return
			}
			s.handleSocketEvent(event)
		}
	}
}

func (s *SlackConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	trimmed := strings.TrimSpace(request.Text)
	if trimmed == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	channel := resolveSlackChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("slack send requires channel or target")
	}

	s.rememberChannel(channel)

	parameters := slack.PostMessageParameters{}
	if request.Thread != "" {
		parameters.ThreadTimestamp = request.Thread
	}

	messageOptions := []slack.MsgOption{
		slack.MsgOptionText(trimmed, false),
		slack.MsgOptionPostMessageParameters(parameters),
	}

	postedChannel, postedTS, err := s.api.PostMessageContext(ctx, channel, messageOptions...)
	if err != nil {
		return protocol.Event{}, err
	}

	event := protocol.Event{
		Timestamp: parseSlackTimestamp(postedTS),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "message",
		Direction: "out",
		Target:    request.Target,
		Channel:   postedChannel,
		Thread:    request.Thread,
		Text:      trimmed,
	}

	s.publish(event)

	return event, nil
}

func (s *SlackConnector) handleSocketEvent(event socketmode.Event) {
	switch event.Type {
	case socketmode.EventTypeConnected:
		s.publishStatus("socket mode connected")
	case socketmode.EventTypeConnectionError:
		if err, ok := event.Data.(error); ok {
			s.publishStatus("socket mode error: " + err.Error())
		} else {
			s.publishStatus("socket mode connection error")
		}
	case socketmode.EventTypeEventsAPI:
		if event.Request != nil {
			s.socket.Ack(*event.Request)
		}

		eventsAPIEvent, ok := event.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}

		if eventsAPIEvent.Type != slackevents.CallbackEvent {
			return
		}

		s.handleInnerEvent(eventsAPIEvent.InnerEvent)
	}
}

func (s *SlackConnector) handleInnerEvent(inner slackevents.EventsAPIInnerEvent) {
	switch message := inner.Data.(type) {
	case *slackevents.MessageEvent:
		s.handleMessageEvent(message)
	}
}

func (s *SlackConnector) handleMessageEvent(message *slackevents.MessageEvent) {
	if message == nil {
		return
	}

	if message.SubType == "message_deleted" {
		return
	}

	if message.TimeStamp == "" {
		return
	}

	if s.isSelfMessage(message) {
		return
	}

	if !s.acceptsChannel(message.Channel) {
		return
	}

	event := protocol.Event{
		Timestamp: parseSlackTimestamp(message.TimeStamp),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "message",
		Direction: "in",
		Target:    "channel:" + message.Channel,
		Channel:   message.Channel,
		Thread:    message.ThreadTimeStamp,
		Text:      message.Text,
	}

	s.publish(event)
}

func (s *SlackConnector) publishStatus(text string) {
	s.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (s *SlackConnector) publishHeartbeat() {
	s.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (s *SlackConnector) rememberChannel(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[channel] = struct{}{}
}

func (s *SlackConnector) acceptsChannel(channel string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.channels) == 0 {
		return true
	}

	_, ok := s.channels[channel]
	return ok
}

func (s *SlackConnector) channelList() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	channels := make([]string, 0, len(s.channels))
	for channel := range s.channels {
		channels = append(channels, channel)
	}

	sort.Strings(channels)
	return channels
}

func (s *SlackConnector) isSelfMessage(message *slackevents.MessageEvent) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.selfUser != "" && message.User == s.selfUser {
		return true
	}

	if s.selfBotID != "" && message.BotID == s.selfBotID {
		return true
	}

	return false
}

func resolveSlackChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"channel:", "slack:channel:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}

func parseSlackTimestamp(ts string) time.Time {
	value, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return time.Now().UTC()
	}

	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}
