package upstream

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

// WhatsAppConnector bridges a WhatsApp account to the PanTalk event stream
// using the whatsmeow library (WhatsApp Web multi-device protocol). Pairing
// is done separately via `pantalk pair --bot <name>` which stores credentials
// in a per-bot SQLite database. The daemon connector expects credentials to
// already exist and will wait with periodic retries if they are missing.
type WhatsAppConnector struct {
	serviceName string
	botName     string
	container   *sqlstore.Container
	publish     func(protocol.Event)

	mu       sync.RWMutex
	client   *whatsmeow.Client
	channels map[string]struct{}
	selfJID  types.JID
}

func NewWhatsAppConnector(bot config.BotConfig, publish func(protocol.Event)) (*WhatsAppConnector, error) {
	// The db_path field specifies where whatsmeow stores encryption keys and
	// session state. When omitted the database is placed next to the main
	// PanTalk database.
	dbPath := strings.TrimSpace(bot.DBPath)
	if dbPath == "" {
		dataDir := filepath.Dir(config.DefaultDBPath())
		dbPath = filepath.Join(dataDir, fmt.Sprintf("whatsapp-%s.db", bot.Name))
	}

	if err := config.EnsureDir(dbPath); err != nil {
		return nil, fmt.Errorf("create whatsapp data dir for bot %q: %w", bot.Name, err)
	}

	logger := waLog.Stdout("WhatsApp", "ERROR", true)
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)
	container, err := sqlstore.New(context.Background(), "sqlite3", dsn, logger)
	if err != nil {
		return nil, fmt.Errorf("open whatsapp store for bot %q: %w", bot.Name, err)
	}

	connector := &WhatsAppConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		container:   container,
		publish:     publish,
		channels:    make(map[string]struct{}),
	}

	for _, ch := range bot.Channels {
		if trimmed := strings.TrimSpace(ch); trimmed != "" {
			connector.channels[trimmed] = struct{}{}
		}
	}

	return connector, nil
}

func (w *WhatsAppConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			w.publishStatus("connector offline")
			return
		default:
		}

		if err := w.connect(ctx); err != nil {
			log.Printf("[whatsapp:%s] connection failed: %v", w.botName, err)
			w.publishStatus("whatsapp connection failed: " + err.Error())
			w.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second

		// Block until context is cancelled. whatsmeow handles WebSocket
		// reconnection internally so we only restart the full connect cycle
		// on context cancellation (daemon shutdown / config reload).
		<-ctx.Done()

		w.mu.Lock()
		if w.client != nil {
			w.client.Disconnect()
			w.client = nil
		}
		w.mu.Unlock()

		w.publishStatus("connector offline")
		return
	}
}

func (w *WhatsAppConnector) connect(ctx context.Context) error {
	device, err := w.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get whatsapp device: %w", err)
	}

	logger := waLog.Stdout("WhatsApp", "ERROR", true)
	client := whatsmeow.NewClient(device, logger)
	client.AddEventHandler(w.handleEvent)

	if client.Store.ID == nil {
		// Not paired yet — tell the user how to pair and return an error
		// so the Run loop retries after backoff.
		msg := fmt.Sprintf("not paired — run: pantalk pair --bot %s", w.botName)
		log.Printf("[whatsapp:%s] %s", w.botName, msg)
		w.publishStatus(msg)
		return fmt.Errorf("%s", msg)
	}

	// Already paired — connect with saved credentials.
	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsapp connect: %w", err)
	}

	w.mu.Lock()
	w.client = client
	w.selfJID = *client.Store.ID
	w.mu.Unlock()

	log.Printf("[whatsapp:%s] connected (jid=%s)", w.botName, w.selfJID.String())
	w.publishStatus("connector online")

	return nil
}

func (w *WhatsAppConnector) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		w.handleMessage(v)
	case *events.Connected:
		log.Printf("[whatsapp:%s] connected event", w.botName)
		w.publishStatus("connector online")
	case *events.Disconnected:
		log.Printf("[whatsapp:%s] disconnected", w.botName)
		w.publishStatus("connector disconnected")
	case *events.LoggedOut:
		log.Printf("[whatsapp:%s] logged out — re-pair required on next restart", w.botName)
		w.publishStatus("logged out — restart pantalkd to re-pair")
	}
}

func (w *WhatsAppConnector) handleMessage(msg *events.Message) {
	if msg.Info.IsFromMe {
		return
	}

	chatJID := msg.Info.Chat.String()
	if !w.acceptsChannel(chatJID) {
		return
	}

	text := extractWhatsAppText(msg)
	if text == "" {
		return
	}

	thread := ""
	if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
		if ci := ext.GetContextInfo(); ci != nil {
			if stanzaID := ci.GetStanzaID(); stanzaID != "" {
				thread = stanzaID
			}
		}
	}

	w.publish(protocol.Event{
		Timestamp: msg.Info.Timestamp,
		Service:   w.serviceName,
		Bot:       w.botName,
		Kind:      "message",
		Direction: "in",
		User:      msg.Info.Sender.String(),
		Target:    "chat:" + chatJID,
		Channel:   chatJID,
		Thread:    thread,
		Text:      text,
	})
}

func (w *WhatsAppConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	chatJID, err := resolveWhatsAppJID(request)
	if err != nil {
		return protocol.Event{}, err
	}
	w.rememberChannel(chatJID.String())

	w.mu.RLock()
	client := w.client
	w.mu.RUnlock()

	if client == nil {
		return protocol.Event{}, fmt.Errorf("whatsapp client not connected")
	}

	resp, err := client.SendMessage(ctx, chatJID, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		return protocol.Event{}, fmt.Errorf("whatsapp send: %w", err)
	}

	channel := chatJID.String()
	target := request.Target
	if target == "" {
		target = "chat:" + channel
	}

	event := protocol.Event{
		Timestamp: resp.Timestamp,
		Service:   w.serviceName,
		Bot:       w.botName,
		Kind:      "message",
		Direction: "out",
		User:      w.Identity(),
		Target:    target,
		Channel:   channel,
		Thread:    request.Thread,
		Text:      text,
	}
	w.publish(event)

	return event, nil
}

func (w *WhatsAppConnector) Identity() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.selfJID.User != "" {
		return w.selfJID.String()
	}
	return ""
}

func (w *WhatsAppConnector) acceptsChannel(channel string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.channels) == 0 {
		return true
	}
	_, ok := w.channels[channel]
	return ok
}

func (w *WhatsAppConnector) rememberChannel(channel string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.channels[channel] = struct{}{}
}

func (w *WhatsAppConnector) publishStatus(text string) {
	w.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   w.serviceName,
		Bot:       w.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (w *WhatsAppConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// extractWhatsAppText returns the text content from a WhatsApp message,
// checking plain conversation text, extended (quoted) text, and media captions.
func extractWhatsAppText(msg *events.Message) string {
	if text := msg.Message.GetConversation(); text != "" {
		return strings.TrimSpace(text)
	}
	if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
		if text := ext.GetText(); text != "" {
			return strings.TrimSpace(text)
		}
	}
	if img := msg.Message.GetImageMessage(); img != nil {
		return strings.TrimSpace(img.GetCaption())
	}
	if vid := msg.Message.GetVideoMessage(); vid != nil {
		return strings.TrimSpace(vid.GetCaption())
	}
	if doc := msg.Message.GetDocumentMessage(); doc != nil {
		return strings.TrimSpace(doc.GetCaption())
	}
	return ""
}

// resolveWhatsAppJID parses a WhatsApp JID from the request's channel or
// target field. Group JIDs (containing a dash) use the group server; plain
// phone numbers use the default user server.
func resolveWhatsAppJID(request protocol.Request) (types.JID, error) {
	raw := request.Channel
	if raw == "" {
		raw = strings.TrimSpace(request.Target)
	}
	if raw == "" {
		return types.JID{}, fmt.Errorf("whatsapp send requires channel or target")
	}

	for _, prefix := range []string{"chat:", "whatsapp:chat:", "whatsapp:"} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimPrefix(raw, prefix)
			break
		}
	}

	if strings.Contains(raw, "@") {
		jid, err := types.ParseJID(raw)
		if err != nil {
			return types.JID{}, fmt.Errorf("invalid whatsapp JID %q: %w", raw, err)
		}
		return jid, nil
	}

	// Group JIDs contain a dash (e.g. "12345678-9876543"); personal JIDs are
	// plain phone numbers.
	if strings.Contains(raw, "-") {
		return types.NewJID(raw, types.GroupServer), nil
	}

	return types.NewJID(raw, types.DefaultUserServer), nil
}
