package upstream

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

// MatrixConnector bridges a Matrix homeserver account to the PanTalk event
// stream using the mautrix-go library. It authenticates with an access token
// and uses the /sync long-poll loop to receive room events. Messages are sent
// via the client-server REST API.
type MatrixConnector struct {
	serviceName  string
	botName      string
	homeserverURL string
	accessToken  string
	publish      func(protocol.Event)

	mu       sync.RWMutex
	client   *mautrix.Client
	channels map[string]struct{}
	selfUser string
}

func NewMatrixConnector(bot config.BotConfig, publish func(protocol.Event)) (*MatrixConnector, error) {
	token, err := config.ResolveCredential(bot.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("resolve matrix access_token for bot %q: %w", bot.Name, err)
	}

	homeserver := strings.TrimSpace(bot.Endpoint)
	if homeserver == "" {
		return nil, fmt.Errorf("matrix bot %q requires endpoint (homeserver URL)", bot.Name)
	}

	connector := &MatrixConnector{
		serviceName:  bot.Type,
		botName:      bot.Name,
		homeserverURL: homeserver,
		accessToken:  token,
		publish:      publish,
		channels:     make(map[string]struct{}),
	}

	for _, ch := range bot.Channels {
		if trimmed := strings.TrimSpace(ch); trimmed != "" {
			connector.channels[trimmed] = struct{}{}
		}
	}

	return connector, nil
}

func (m *MatrixConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			m.publishStatus("connector offline")
			return
		default:
		}

		if err := m.connectAndRun(ctx); err != nil {
			log.Printf("[matrix:%s] session ended: %v", m.botName, err)
			m.publishStatus("matrix session ended: " + err.Error())
		}

		select {
		case <-ctx.Done():
			m.publishStatus("connector offline")
			return
		case <-time.After(backoff):
		}

		if backoff < 30*time.Second {
			backoff *= 2
		}

		m.publishStatus("matrix reconnecting...")
		log.Printf("[matrix:%s] reconnecting", m.botName)
	}
}

func (m *MatrixConnector) connectAndRun(ctx context.Context) error {
	client, err := mautrix.NewClient(m.homeserverURL, "", m.accessToken)
	if err != nil {
		return fmt.Errorf("create matrix client: %w", err)
	}

	// Verify credentials and discover our own user ID.
	resp, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("matrix whoami: %w", err)
	}

	m.mu.Lock()
	m.client = client
	m.selfUser = string(resp.UserID)
	m.mu.Unlock()

	log.Printf("[matrix:%s] authenticated (user=%s)", m.botName, resp.UserID)
	m.publishStatus("connector online")

	// Register the sync event handler for incoming room messages.
	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(_ context.Context, evt *event.Event) {
		m.handleMessage(evt)
	})

	// Run the sync loop; blocks until context cancellation or a fatal error.
	syncCtx, syncCancel := context.WithCancel(ctx)
	defer syncCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.SyncWithContext(syncCtx)
	}()

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			syncCancel()
			client.StopSync()
			return ctx.Err()
		case syncErr := <-errCh:
			return fmt.Errorf("sync loop: %w", syncErr)
		case <-heartbeatTicker.C:
			m.publishHeartbeat()
		}
	}
}

func (m *MatrixConnector) handleMessage(evt *event.Event) {
	// Ignore messages from ourselves.
	m.mu.RLock()
	self := m.selfUser
	m.mu.RUnlock()
	if string(evt.Sender) == self {
		return
	}

	roomID := string(evt.RoomID)
	if !m.acceptsChannel(roomID) {
		return
	}

	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok || content == nil {
		return
	}
	text := strings.TrimSpace(content.Body)
	if text == "" {
		return
	}

	// Use the relation (reply) as the thread ID if present.
	thread := ""
	if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
		thread = string(content.RelatesTo.InReplyTo.EventID)
	}

	m.publish(protocol.Event{
		Timestamp: time.UnixMilli(evt.Timestamp),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "message",
		Direction: "in",
		User:      string(evt.Sender),
		Target:    "room:" + roomID,
		Channel:   roomID,
		Thread:    thread,
		Text:      text,
	})
}

func (m *MatrixConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	roomID := resolveMatrixRoom(request)
	if roomID == "" {
		return protocol.Event{}, fmt.Errorf("matrix send requires channel or target")
	}

	m.rememberChannel(roomID)

	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()

	if client == nil {
		return protocol.Event{}, fmt.Errorf("matrix client not connected")
	}

	resp, err := client.SendText(ctx, id.RoomID(roomID), text)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("matrix send: %w", err)
	}

	target := request.Target
	if target == "" {
		target = "room:" + roomID
	}

	evt := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "message",
		Direction: "out",
		User:      m.Identity(),
		Target:    target,
		Channel:   roomID,
		Thread:    string(resp.EventID),
		Text:      text,
	}
	m.publish(evt)

	return evt, nil
}

func (m *MatrixConnector) Identity() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selfUser
}

func (m *MatrixConnector) acceptsChannel(channel string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.channels) == 0 {
		return true
	}
	_, ok := m.channels[channel]
	return ok
}

func (m *MatrixConnector) rememberChannel(channel string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[channel] = struct{}{}
}

func (m *MatrixConnector) publishStatus(text string) {
	m.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (m *MatrixConnector) publishHeartbeat() {
	m.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.serviceName,
		Bot:       m.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

// resolveMatrixRoom extracts a Matrix room ID from the request's channel or
// target field. It strips common prefixes so callers can pass raw room IDs
// (e.g. "!abc:matrix.org") or prefixed forms (e.g. "room:!abc:matrix.org").
func resolveMatrixRoom(request protocol.Request) string {
	raw := request.Channel
	if raw == "" {
		raw = strings.TrimSpace(request.Target)
	}
	if raw == "" {
		return ""
	}

	for _, prefix := range []string{"room:", "matrix:room:", "matrix:"} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimPrefix(raw, prefix)
			break
		}
	}

	return strings.TrimSpace(raw)
}
