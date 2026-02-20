package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

type ZulipConnector struct {
	serviceName string
	botName     string
	endpoint    string
	email       string
	apiKey      string
	publish     func(protocol.Event)
	httpClient  *http.Client

	mu       sync.RWMutex
	channels map[string]struct{}
	selfUser string
	selfID   int64
}

type zulipUser struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
}

type zulipGetProfileResponse struct {
	Result string `json:"result"`
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
}

type zulipRegisterResponse struct {
	Result      string `json:"result"`
	Msg         string `json:"msg"`
	QueueID     string `json:"queue_id"`
	LastEventID int64  `json:"last_event_id"`
}

type zulipEventsResponse struct {
	Result string       `json:"result"`
	Msg    string       `json:"msg"`
	Events []zulipEvent `json:"events"`
}

type zulipEvent struct {
	Type    string        `json:"type"`
	ID      int64         `json:"id"`
	Message *zulipMessage `json:"message,omitempty"`
}

type zulipMessage struct {
	ID              int64    `json:"id"`
	SenderID        int64    `json:"sender_id"`
	SenderEmail     string   `json:"sender_email"`
	Content         string   `json:"content"`
	Subject         string   `json:"subject"`
	Timestamp       int64    `json:"timestamp"`
	Type            string   `json:"type"`
	StreamID        int64    `json:"stream_id"`
	DisplayRecipient json.RawMessage `json:"display_recipient"`
}

type zulipSendMessageResponse struct {
	Result string `json:"result"`
	Msg    string `json:"msg"`
	ID     int64  `json:"id"`
}

func NewZulipConnector(bot config.BotConfig, publish func(protocol.Event)) (*ZulipConnector, error) {
	apiKey, err := config.ResolveCredential(bot.APIKey)
	if err != nil {
		return nil, fmt.Errorf("resolve zulip api_key for bot %q: %w", bot.Name, err)
	}

	email, err := config.ResolveCredential(bot.BotEmail)
	if err != nil {
		return nil, fmt.Errorf("resolve zulip bot_email for bot %q: %w", bot.Name, err)
	}

	connector := &ZulipConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		endpoint:    strings.TrimRight(strings.TrimSpace(bot.Endpoint), "/"),
		email:       email,
		apiKey:      apiKey,
		publish:     publish,
		httpClient:  &http.Client{Timeout: 90 * time.Second},
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

func (z *ZulipConnector) Run(ctx context.Context) {
	if err := z.loadSelfUser(ctx); err != nil {
		log.Printf("[zulip:%s] auth failed: %v", z.botName, err)
		z.publishStatus("zulip auth failed: " + err.Error())
		return
	}

	log.Printf("[zulip:%s] authenticated (user_id=%d, email=%s)", z.botName, z.selfID, z.email)

	z.resolveChannelNames(ctx)

	z.publishStatus("connector online")

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	go z.eventLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			z.publishStatus("connector offline")
			return
		case <-heartbeatTicker.C:
			z.publishHeartbeat()
		}
	}
}

func (z *ZulipConnector) eventLoop(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		queueID, lastEventID, err := z.registerQueue(ctx)
		if err != nil {
			z.publishStatus("zulip register queue failed: " + err.Error())
			z.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		log.Printf("[zulip:%s] event queue registered: %s", z.botName, queueID)
		z.publishStatus("zulip event queue connected")

		if err := z.pollEvents(ctx, queueID, lastEventID); err != nil {
			log.Printf("[zulip:%s] event poll ended: %v", z.botName, err)
			z.publishStatus("zulip event poll ended: " + err.Error())
		}
	}
}

func (z *ZulipConnector) registerQueue(ctx context.Context) (string, int64, error) {
	form := url.Values{}
	form.Set("event_types", `["message"]`)
	form.Set("narrow", `[]`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.endpoint+"/api/v1/register", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.SetBasicAuth(z.email, z.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := z.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("register queue failed: status %d", resp.StatusCode)
	}

	var result zulipRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}

	if result.Result != "success" {
		return "", 0, fmt.Errorf("register queue: %s", result.Msg)
	}

	return result.QueueID, result.LastEventID, nil
}

func (z *ZulipConnector) pollEvents(ctx context.Context, queueID string, lastEventID int64) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		events, newLastID, err := z.getEvents(ctx, queueID, lastEventID)
		if err != nil {
			return err
		}

		lastEventID = newLastID

		for _, evt := range events {
			if evt.Type != "message" || evt.Message == nil {
				continue
			}

			msg := evt.Message

			if z.isSelfMessage(msg.SenderID) {
				continue
			}

			channelID := z.extractChannel(msg)
			if !z.acceptsChannel(channelID) {
				continue
			}

			text := strings.TrimSpace(msg.Content)
			if text == "" {
				continue
			}

			z.publish(protocol.Event{
				Timestamp: time.Unix(msg.Timestamp, 0).UTC(),
				Service:   z.serviceName,
				Bot:       z.botName,
				Kind:      "message",
				Direction: "in",
				User:      msg.SenderEmail,
				Target:    "channel:" + channelID,
				Channel:   channelID,
				Thread:    msg.Subject,
				Text:      text,
			})
		}
	}
}

func (z *ZulipConnector) getEvents(ctx context.Context, queueID string, lastEventID int64) ([]zulipEvent, int64, error) {
	params := url.Values{}
	params.Set("queue_id", queueID)
	params.Set("last_event_id", strconv.FormatInt(lastEventID, 10))

	reqURL := z.endpoint + "/api/v1/events?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, lastEventID, err
	}
	req.SetBasicAuth(z.email, z.apiKey)

	resp, err := z.httpClient.Do(req)
	if err != nil {
		return nil, lastEventID, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, lastEventID, fmt.Errorf("get events failed: status %d", resp.StatusCode)
	}

	var result zulipEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, lastEventID, err
	}

	if result.Result != "success" {
		return nil, lastEventID, fmt.Errorf("get events: %s", result.Msg)
	}

	newLastID := lastEventID
	for _, evt := range result.Events {
		if evt.ID > newLastID {
			newLastID = evt.ID
		}
	}

	return result.Events, newLastID, nil
}

func (z *ZulipConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	trimmed := strings.TrimSpace(request.Text)
	if trimmed == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	channel := resolveZulipChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("zulip send requires channel or target")
	}

	z.rememberChannel(channel)

	form := url.Values{}
	form.Set("content", trimmed)

	if request.Thread != "" {
		form.Set("topic", request.Thread)
	}

	// Determine message type based on channel format
	if strings.Contains(channel, "@") {
		// Direct message
		form.Set("type", "direct")
		form.Set("to", channel)
	} else {
		// Stream message
		form.Set("type", "stream")
		form.Set("to", channel)
		if request.Thread == "" {
			form.Set("topic", "(no topic)")
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.endpoint+"/api/v1/messages", strings.NewReader(form.Encode()))
	if err != nil {
		return protocol.Event{}, err
	}
	req.SetBasicAuth(z.email, z.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := z.httpClient.Do(req)
	if err != nil {
		return protocol.Event{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.Event{}, fmt.Errorf("zulip send failed: status %d", resp.StatusCode)
	}

	var sendResp zulipSendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		return protocol.Event{}, err
	}

	if sendResp.Result != "success" {
		return protocol.Event{}, fmt.Errorf("zulip send: %s", sendResp.Msg)
	}

	target := request.Target
	if target == "" {
		target = "channel:" + channel
	}

	event := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   z.serviceName,
		Bot:       z.botName,
		Kind:      "message",
		Direction: "out",
		User:      z.Identity(),
		Target:    target,
		Channel:   channel,
		Thread:    request.Thread,
		Text:      trimmed,
	}
	z.publish(event)

	return event, nil
}

func (z *ZulipConnector) loadSelfUser(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, z.endpoint+"/api/v1/users/me", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(z.email, z.apiKey)

	resp, err := z.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("users/me failed: status %d", resp.StatusCode)
	}

	var profile zulipGetProfileResponse
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return err
	}

	if profile.Result != "success" {
		return fmt.Errorf("users/me returned: %s", profile.Result)
	}

	z.mu.Lock()
	z.selfUser = profile.Email
	z.selfID = profile.UserID
	z.mu.Unlock()

	return nil
}

func (z *ZulipConnector) Identity() string {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.selfUser
}

func (z *ZulipConnector) extractChannel(msg *zulipMessage) string {
	if msg.Type == "stream" {
		return strconv.FormatInt(msg.StreamID, 10)
	}
	return msg.SenderEmail
}

func (z *ZulipConnector) isSelfMessage(senderID int64) bool {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.selfID > 0 && senderID == z.selfID
}

func (z *ZulipConnector) rememberChannel(channel string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.channels[channel] = struct{}{}
}

func (z *ZulipConnector) acceptsChannel(channel string) bool {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if len(z.channels) == 0 {
		return true
	}
	_, ok := z.channels[channel]
	return ok
}

func (z *ZulipConnector) publishStatus(text string) {
	z.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   z.serviceName,
		Bot:       z.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (z *ZulipConnector) publishHeartbeat() {
	z.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   z.serviceName,
		Bot:       z.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (z *ZulipConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

func resolveZulipChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"channel:", "zulip:channel:", "stream:", "zulip:stream:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}

// resolveChannelNames resolves any friendly stream names (e.g. "general",
// "engineering") to Zulip numeric stream IDs by querying the get_stream_id
// API. Entries that already look like numeric IDs are left unchanged.
func (z *ZulipConnector) resolveChannelNames(ctx context.Context) {
	z.mu.RLock()
	var toResolve []string
	for ch := range z.channels {
		if !isZulipStreamID(ch) {
			toResolve = append(toResolve, ch)
		}
	}
	z.mu.RUnlock()

	if len(toResolve) == 0 {
		return
	}

	z.mu.Lock()
	defer z.mu.Unlock()
	for _, name := range toResolve {
		streamID, err := z.getStreamID(ctx, name)
		if err != nil {
			log.Printf("[zulip:%s] could not resolve stream %q: %v – keeping as-is", z.botName, name, err)
			continue
		}
		delete(z.channels, name)
		resolved := strconv.FormatInt(streamID, 10)
		z.channels[resolved] = struct{}{}
		log.Printf("[zulip:%s] resolved stream %q → %s", z.botName, name, resolved)
	}
}

func (z *ZulipConnector) getStreamID(ctx context.Context, name string) (int64, error) {
	reqURL := fmt.Sprintf("%s/api/v1/get_stream_id?stream=%s", z.endpoint, url.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(z.email, z.apiKey)

	resp, err := z.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Result   string `json:"result"`
		StreamID int64  `json:"stream_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if result.Result != "success" {
		return 0, fmt.Errorf("get_stream_id failed for %q", name)
	}
	return result.StreamID, nil
}

// isZulipStreamID returns true when s looks like a Zulip numeric stream ID.
func isZulipStreamID(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}