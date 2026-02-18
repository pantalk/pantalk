package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

type MattermostConnector struct {
	serviceName string
	botName     string
	endpoint    string
	token       string
	publish     func(protocol.Event)
	httpClient  *http.Client

	mu       sync.RWMutex
	channels map[string]struct{}
	selfUser string
	nextSeq  int64
}

type mmPost struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id"`
	UserID    string `json:"user_id"`
	CreateAt  int64  `json:"create_at"`
}

type mmCreatePostRequest struct {
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
	RootID    string `json:"root_id,omitempty"`
}

type mmWebSocketEvent struct {
	Event string                 `json:"event"`
	Data  map[string]interface{} `json:"data"`
	Seq   int64                  `json:"seq"`
}

type mmWebSocketClientMessage struct {
	Action string                 `json:"action"`
	Seq    int64                  `json:"seq"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

type mmUser struct {
	ID string `json:"id"`
}

func NewMattermostConnector(bot config.BotConfig, publish func(protocol.Event)) (*MattermostConnector, error) {
	token, err := config.ResolveCredential(bot.BotToken)
	if err != nil {
		return nil, fmt.Errorf("resolve mattermost bot_token for bot %q: %w", bot.Name, err)
	}

	connector := &MattermostConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		endpoint:    strings.TrimRight(strings.TrimSpace(bot.Endpoint), "/"),
		token:       token,
		publish:     publish,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
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

func (m *MattermostConnector) Run(ctx context.Context) {
	if err := m.loadSelfUser(ctx); err != nil {
		log.Printf("[mattermost:%s] auth failed: %v", m.botName, err)
		m.publishStatus("mattermost auth failed: " + err.Error())
		return
	}

	log.Printf("[mattermost:%s] authenticated (user=%s)", m.botName, m.selfUser)

	m.publishStatus("connector online")

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	go m.runWebsocketLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			m.publishStatus("connector offline")
			return
		case <-heartbeatTicker.C:
			m.publishHeartbeat()
		}
	}
}

func (m *MattermostConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	trimmed := strings.TrimSpace(request.Text)
	if trimmed == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	channel := resolveMattermostChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("mattermost send requires channel or target")
	}

	m.rememberChannel(channel)

	bodyPayload := mmCreatePostRequest{ChannelID: channel, Message: trimmed}
	if request.Thread != "" {
		bodyPayload.RootID = request.Thread
	}

	body, err := json.Marshal(bodyPayload)
	if err != nil {
		return protocol.Event{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint+"/api/v4/posts", bytes.NewReader(body))
	if err != nil {
		return protocol.Event{}, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return protocol.Event{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.Event{}, fmt.Errorf("mattermost post failed: status %d", resp.StatusCode)
	}

	var posted mmPost
	if err := json.NewDecoder(resp.Body).Decode(&posted); err != nil {
		return protocol.Event{}, err
	}

	target := request.Target
	if target == "" {
		target = "channel:" + posted.ChannelID
	}

	event := protocol.Event{
		Timestamp: time.UnixMilli(posted.CreateAt).UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "message",
		Direction: "out",
		User:      m.Identity(),
		Target:    target,
		Channel:   posted.ChannelID,
		Thread:    posted.RootID,
		Text:      posted.Message,
	}
	m.publish(event)

	return event, nil
}

func (m *MattermostConnector) runWebsocketLoop(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := m.openWebsocket(ctx)
		if err != nil {
			m.publishStatus("mattermost websocket connect failed: " + err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		log.Printf("[mattermost:%s] websocket connected", m.botName)
		m.publishStatus("mattermost websocket connected")

		if err := m.authenticateWebsocket(conn); err != nil {
			_ = conn.Close()
			m.publishStatus("mattermost websocket auth failed: " + err.Error())
			continue
		}

		m.readWebsocketLoop(ctx, conn)
		_ = conn.Close()
	}
}

func (m *MattermostConnector) openWebsocket(_ context.Context) (*websocket.Conn, error) {
	endpointURL, err := url.Parse(m.endpoint)
	if err != nil {
		return nil, err
	}

	scheme := "ws"
	if endpointURL.Scheme == "https" {
		scheme = "wss"
	}

	wsURL := url.URL{
		Scheme: scheme,
		Host:   endpointURL.Host,
		Path:   "/api/v4/websocket",
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+m.token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), header)
	if err != nil {
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func (m *MattermostConnector) authenticateWebsocket(conn *websocket.Conn) error {
	msg := mmWebSocketClientMessage{
		Action: "authentication_challenge",
		Seq:    m.nextSequence(),
		Data:   map[string]interface{}{"token": m.token},
	}
	return conn.WriteJSON(msg)
}

func (m *MattermostConnector) readWebsocketLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var wsEvent mmWebSocketEvent
		if err := conn.ReadJSON(&wsEvent); err != nil {
			m.publishStatus("mattermost websocket disconnected: " + err.Error())
			return
		}

		if wsEvent.Event != "posted" {
			continue
		}

		postRaw, ok := wsEvent.Data["post"].(string)
		if !ok || strings.TrimSpace(postRaw) == "" {
			continue
		}

		var post mmPost
		if err := json.Unmarshal([]byte(postRaw), &post); err != nil {
			continue
		}

		if m.isSelfUser(post.UserID) {
			continue
		}

		if !m.acceptsChannel(post.ChannelID) {
			continue
		}

		protocolEvent := protocol.Event{
			Timestamp: time.UnixMilli(post.CreateAt).UTC(),
			Service:   m.serviceName,
			Bot:       m.botName,
			Kind:      "message",
			Direction: "in",
			User:      post.UserID,
			Target:    "channel:" + post.ChannelID,
			Channel:   post.ChannelID,
			Thread:    post.RootID,
			Text:      post.Message,
		}

		m.publish(protocolEvent)
	}
}

func (m *MattermostConnector) loadSelfUser(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint+"/api/v4/users/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("users/me failed: status %d", resp.StatusCode)
	}

	var user mmUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return err
	}

	m.mu.Lock()
	m.selfUser = user.ID
	m.mu.Unlock()

	return nil
}

func (m *MattermostConnector) publishStatus(text string) {
	m.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (m *MattermostConnector) publishHeartbeat() {
	m.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (m *MattermostConnector) nextSequence() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSeq++
	return m.nextSeq
}

func (m *MattermostConnector) rememberChannel(channel string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[channel] = struct{}{}
}

func (m *MattermostConnector) acceptsChannel(channel string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.channels) == 0 {
		return true
	}

	_, ok := m.channels[channel]
	return ok
}

func (m *MattermostConnector) Identity() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selfUser
}

func (m *MattermostConnector) isSelfUser(userID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selfUser != "" && userID == m.selfUser
}

func resolveMattermostChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"channel:", "mattermost:channel:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}
