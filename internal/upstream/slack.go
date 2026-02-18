package upstream

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

type SlackConnector struct {
	serviceName string
	botName     string
	publish     func(protocol.Event)
	api         *slack.Client
	socket      *socketmode.Client

	mu            sync.RWMutex
	channels      map[string]struct{}
	selfUser      string
	selfBotID     string
	receivedEvent bool
}

func NewSlackConnector(bot config.BotConfig, publish func(protocol.Event)) (*SlackConnector, error) {
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
		serviceName: bot.Type,
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
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			s.publishStatus("connector offline")
			return
		default:
		}

		if err := s.connectAndRun(ctx); err != nil {
			log.Printf("[slack:%s] session ended: %v", s.botName, err)
			s.publishStatus("slack session ended: " + err.Error())
		}

		select {
		case <-ctx.Done():
			s.publishStatus("connector offline")
			return
		case <-time.After(backoff):
		}

		if backoff < 30*time.Second {
			backoff *= 2
		}

		s.publishStatus("slack reconnecting...")
		log.Printf("[slack:%s] reconnecting", s.botName)

		// Re-create the socket-mode client for a fresh connection
		s.mu.Lock()
		s.socket = socketmode.New(s.api)
		s.mu.Unlock()
	}
}

func (s *SlackConnector) connectAndRun(ctx context.Context) error {
	auth, err := s.api.AuthTestContext(ctx)
	if err != nil {
		log.Printf("[slack:%s] auth failed: %v", s.botName, err)
		return fmt.Errorf("auth failed: %w", err)
	}

	s.mu.Lock()
	s.selfUser = auth.UserID
	s.selfBotID = auth.BotID
	s.mu.Unlock()

	log.Printf("[slack:%s] authenticated (user=%s)", s.botName, auth.UserID)

	go s.socket.RunContext(ctx)

	s.publishStatus("connector online")

	// Start a timer to detect missing event subscriptions. If no events arrive
	// within 30 seconds of connecting, it likely means the Slack app is missing
	// event subscriptions (app_mention, message.channels, etc.).
	eventCheckTimer := time.NewTimer(30 * time.Second)
	defer eventCheckTimer.Stop()

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-eventCheckTimer.C:
			s.mu.RLock()
			gotEvent := s.receivedEvent
			s.mu.RUnlock()
			if !gotEvent {
				log.Printf("[slack:%s] warning: no events received after 30s - check that your Slack app has event subscriptions enabled (app_mention, message.channels) and the bot is invited to a channel", s.botName)
				s.publishStatus("warning: no events received - check Slack app event subscriptions")
			}
		case <-heartbeatTicker.C:
			s.publishHeartbeat()
		case event, ok := <-s.socket.Events:
			if !ok {
				return fmt.Errorf("socket mode event channel closed")
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

	target := request.Target
	if target == "" {
		target = "channel:" + postedChannel
	}

	event := protocol.Event{
		Timestamp: parseSlackTimestamp(postedTS),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "message",
		Direction: "out",
		User:      s.Identity(),
		Target:    target,
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
		s.mu.Lock()
		s.receivedEvent = true
		s.mu.Unlock()

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
	switch ev := inner.Data.(type) {
	case *slackevents.MessageEvent:
		s.handleMessageEvent(ev)
	case *slackevents.AppMentionEvent:
		s.handleAppMentionEvent(ev)
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
		User:      message.User,
		Target:    "channel:" + message.Channel,
		Channel:   message.Channel,
		Thread:    message.ThreadTimeStamp,
		Text:      message.Text,
	}

	s.publish(event)
}

func (s *SlackConnector) handleAppMentionEvent(mention *slackevents.AppMentionEvent) {
	if mention == nil {
		return
	}

	if mention.TimeStamp == "" {
		return
	}

	if mention.BotID != "" {
		s.mu.RLock()
		isSelf := s.selfBotID != "" && mention.BotID == s.selfBotID
		s.mu.RUnlock()
		if isSelf {
			return
		}
	}

	if !s.acceptsChannel(mention.Channel) {
		return
	}

	event := protocol.Event{
		Timestamp: parseSlackTimestamp(mention.TimeStamp),
		Service:   s.serviceName,
		Bot:       s.botName,
		Kind:      "message",
		Direction: "in",
		User:      mention.User,
		Target:    "channel:" + mention.Channel,
		Channel:   mention.Channel,
		Thread:    mention.ThreadTimeStamp,
		Text:      mention.Text,
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

func (s *SlackConnector) Identity() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selfUser
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
