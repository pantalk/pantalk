package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultTelegramEndpoint = "https://api.telegram.org"

type TelegramConnector struct {
	serviceName string
	botName     string
	baseURL     string
	token       string
	publish     func(protocol.Event)
	httpClient  *http.Client

	mu           sync.RWMutex
	channels     map[string]struct{}
	selfBotID    int64
	nextUpdateID int64
}

type tgGetMeResponse struct {
	OK     bool      `json:"ok"`
	Result tgBotUser `json:"result"`
}

type tgBotUser struct {
	ID int64 `json:"id"`
}

type tgGetUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type tgGetUpdatesResponse struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID          int64      `json:"update_id"`
	Message           *tgMessage `json:"message,omitempty"`
	EditedMessage     *tgMessage `json:"edited_message,omitempty"`
	ChannelPost       *tgMessage `json:"channel_post,omitempty"`
	EditedChannelPost *tgMessage `json:"edited_channel_post,omitempty"`
}

type tgMessage struct {
	MessageID       int64      `json:"message_id"`
	Date            int64      `json:"date"`
	Text            string     `json:"text"`
	Caption         string     `json:"caption"`
	Chat            tgChat     `json:"chat"`
	From            *tgUser    `json:"from,omitempty"`
	MessageThreadID int64      `json:"message_thread_id,omitempty"`
	ReplyToMessage  *tgMessage `json:"reply_to_message,omitempty"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

type tgSendMessageRequest struct {
	ChatID           string `json:"chat_id"`
	Text             string `json:"text"`
	MessageThreadID  int64  `json:"message_thread_id,omitempty"`
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
}

type tgSendMessageResponse struct {
	OK     bool      `json:"ok"`
	Result tgMessage `json:"result"`
}

func NewTelegramConnector(bot config.BotConfig, publish func(protocol.Event)) (*TelegramConnector, error) {
	token, err := config.ResolveCredential(bot.BotToken)
	if err != nil {
		return nil, fmt.Errorf("resolve telegram bot_token for bot %q: %w", bot.Name, err)
	}

	endpoint := strings.TrimSpace(bot.Endpoint)
	if endpoint == "" {
		endpoint = defaultTelegramEndpoint
	}

	connector := &TelegramConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		baseURL:     strings.TrimRight(endpoint, "/") + "/bot" + token,
		token:       token,
		publish:     publish,
		httpClient:  &http.Client{Timeout: 70 * time.Second},
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

func (t *TelegramConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			t.publishStatus("connector offline")
			return
		default:
		}

		if err := t.loadSelf(ctx); err != nil {
			log.Printf("[telegram:%s] auth failed: %v", t.botName, err)
			t.publishStatus("telegram auth failed: " + err.Error())
			t.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		log.Printf("[telegram:%s] authenticated (bot_id=%d)", t.botName, t.selfBotID)
		t.publishStatus("connector online")
		t.pollLoop(ctx)
	}
}

func (t *TelegramConnector) pollLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates(ctx)
		if err != nil {
			t.publishStatus("telegram getUpdates error: " + err.Error())
			t.sleepOrDone(ctx, 2*time.Second)
			continue
		}

		for _, update := range updates {
			t.advanceOffset(update.UpdateID + 1)
			message := selectTelegramMessage(update)
			if message == nil {
				continue
			}

			if t.isSelfMessage(message) {
				continue
			}

			channelID := strconv.FormatInt(message.Chat.ID, 10)
			if !t.acceptsChannel(channelID) {
				continue
			}

			text := strings.TrimSpace(message.Text)
			if text == "" {
				text = strings.TrimSpace(message.Caption)
			}

			thread := ""
			if message.MessageThreadID > 0 {
				thread = strconv.FormatInt(message.MessageThreadID, 10)
			} else if message.ReplyToMessage != nil && message.ReplyToMessage.MessageID > 0 {
				thread = strconv.FormatInt(message.ReplyToMessage.MessageID, 10)
			}

			userID := ""
			if message.From != nil {
				userID = strconv.FormatInt(message.From.ID, 10)
			}

			t.publish(protocol.Event{
				Timestamp: time.Unix(message.Date, 0).UTC(),
				Service:   t.serviceName,
				Bot:       t.botName,
				Kind:      "message",
				Direction: "in",
				User:      userID,
				Target:    "chat:" + channelID,
				Channel:   channelID,
				Thread:    thread,
				Text:      text,
			})
		}
	}
}

func (t *TelegramConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	chatID := resolveTelegramChat(request)
	if chatID == "" {
		return protocol.Event{}, fmt.Errorf("telegram send requires channel or target")
	}
	t.rememberChannel(chatID)

	payload := tgSendMessageRequest{ChatID: chatID, Text: text}
	if request.Thread != "" {
		if threadID, err := strconv.ParseInt(request.Thread, 10, 64); err == nil {
			payload.ReplyToMessageID = threadID
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return protocol.Event{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return protocol.Event{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return protocol.Event{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.Event{}, fmt.Errorf("telegram sendMessage failed: status %d", resp.StatusCode)
	}

	var sendResponse tgSendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResponse); err != nil {
		return protocol.Event{}, err
	}
	if !sendResponse.OK {
		return protocol.Event{}, fmt.Errorf("telegram sendMessage returned not ok")
	}

	channel := strconv.FormatInt(sendResponse.Result.Chat.ID, 10)
	thread := request.Thread
	if thread == "" && sendResponse.Result.MessageThreadID > 0 {
		thread = strconv.FormatInt(sendResponse.Result.MessageThreadID, 10)
	}

	target := request.Target
	if target == "" {
		target = "chat:" + channel
	}

	event := protocol.Event{
		Timestamp: time.Unix(sendResponse.Result.Date, 0).UTC(),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "message",
		Direction: "out",
		User:      t.Identity(),
		Target:    target,
		Channel:   channel,
		Thread:    thread,
		Text:      text,
	}
	t.publish(event)

	return event, nil
}

func (t *TelegramConnector) loadSelf(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/getMe", nil)
	if err != nil {
		return err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("getMe failed: status %d", resp.StatusCode)
	}

	var me tgGetMeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return err
	}
	if !me.OK {
		return fmt.Errorf("getMe returned not ok")
	}

	t.mu.Lock()
	t.selfBotID = me.Result.ID
	t.mu.Unlock()

	return nil
}

func (t *TelegramConnector) getUpdates(ctx context.Context) ([]tgUpdate, error) {
	offset := t.currentOffset()
	payload := tgGetUpdatesRequest{
		Offset:         offset,
		Timeout:        50,
		AllowedUpdates: []string{"message", "edited_message", "channel_post", "edited_channel_post"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("getUpdates failed: status %d", resp.StatusCode)
	}

	var updatesResponse tgGetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&updatesResponse); err != nil {
		return nil, err
	}
	if !updatesResponse.OK {
		return nil, fmt.Errorf("getUpdates returned not ok")
	}

	return updatesResponse.Result, nil
}

func (t *TelegramConnector) currentOffset() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nextUpdateID
}

func (t *TelegramConnector) advanceOffset(next int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if next > t.nextUpdateID {
		t.nextUpdateID = next
	}
}

func (t *TelegramConnector) rememberChannel(channel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channels[channel] = struct{}{}
}

func (t *TelegramConnector) acceptsChannel(channel string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.channels) == 0 {
		return true
	}

	_, ok := t.channels[channel]
	return ok
}

func (t *TelegramConnector) Identity() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.selfBotID > 0 {
		return strconv.FormatInt(t.selfBotID, 10)
	}
	return ""
}

func (t *TelegramConnector) isSelfMessage(message *tgMessage) bool {
	if message == nil || message.From == nil {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.selfBotID > 0 && message.From.ID == t.selfBotID
}

func (t *TelegramConnector) publishStatus(text string) {
	t.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (t *TelegramConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

func selectTelegramMessage(update tgUpdate) *tgMessage {
	if update.Message != nil {
		return update.Message
	}
	if update.EditedMessage != nil {
		return update.EditedMessage
	}
	if update.ChannelPost != nil {
		return update.ChannelPost
	}
	if update.EditedChannelPost != nil {
		return update.EditedChannelPost
	}
	return nil
}

func resolveTelegramChat(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"chat:", "telegram:chat:", "channel:", "telegram:channel:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}
