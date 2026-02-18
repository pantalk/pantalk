package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/protocol"
	"github.com/chatbotkit/pantalk/internal/store"
	"github.com/chatbotkit/pantalk/internal/upstream"
)

type Server struct {
	cfg      config.Config
	listener net.Listener
	cfgPath  string

	socketOverride string
	dbOverride     string
	debug          bool

	rootCtx       context.Context
	runtimeCancel context.CancelFunc

	mu            sync.RWMutex
	bots          map[string]protocol.BotRef
	subsByBot     map[string]map[chan protocol.Event]struct{}
	routesByBot   map[string]map[string]struct{}
	connectors    map[string]upstream.Connector
	notifications *store.Store
}

func New(cfg config.Config, cfgPath string, socketOverride string, dbOverride string) *Server {
	return &Server{
		cfg:            cfg,
		cfgPath:        cfgPath,
		socketOverride: socketOverride,
		dbOverride:     dbOverride,
		bots:           make(map[string]protocol.BotRef),
		subsByBot:      make(map[string]map[chan protocol.Event]struct{}),
		routesByBot:    make(map[string]map[string]struct{}),
		connectors:     make(map[string]upstream.Connector),
	}
}

// SetDebug enables verbose debug logging.
func (s *Server) SetDebug(enabled bool) {
	s.debug = enabled
}

func (s *Server) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	s.rootCtx = ctx

	log.Printf("opening database at %s", s.cfg.Server.DBPath)

	notificationStore, err := store.Open(s.cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("open notification store: %w", err)
	}
	defer notificationStore.Close()
	s.notifications = notificationStore

	if err := os.RemoveAll(s.cfg.Server.SocketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	listener, err := net.Listen("unix", s.cfg.Server.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on socket %s: %w", s.cfg.Server.SocketPath, err)
	}
	defer listener.Close()

	if err := os.Chmod(s.cfg.Server.SocketPath, 0600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	s.listener = listener

	log.Printf("listening on %s", s.cfg.Server.SocketPath)

	if err := s.startConnectors(s.cfg); err != nil {
		return err
	}

	log.Printf("pantalkd ready (%d bot(s) configured)", len(s.cfg.Bots))

	go func() {
		<-ctx.Done()
		log.Printf("shutting down")
		_ = s.listener.Close()
	}()

	if s.debug {
		log.Printf("debug mode enabled")
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			continue
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *Server) startConnectors(cfg config.Config) error {
	bots := make(map[string]protocol.BotRef)
	connectors := make(map[string]upstream.Connector)

	for _, bot := range cfg.Bots {
		key := botKey(bot.Type, bot.Name)

		displayName := bot.DisplayName
		if displayName == "" {
			displayName = bot.Name
		}

		botRef := protocol.BotRef{
			Service:     bot.Type,
			Name:        bot.Name,
			DisplayName: displayName,
		}
		bots[key] = botRef

		connector, err := upstream.NewConnector(bot, func(event protocol.Event) {
			event.Service = bot.Type
			event.Bot = bot.Name
			s.publish(event)
		})
		if err != nil {
			return fmt.Errorf("create connector for %s: %w", key, err)
		}

		connectors[key] = connector

		log.Printf("bot %s (%s) registered", bot.Name, bot.Type)
	}

	runtimeCtx, runtimeCancel := context.WithCancel(s.rootCtx)

	s.mu.Lock()
	oldCancel := s.runtimeCancel
	s.cfg = cfg
	s.bots = bots
	s.connectors = connectors
	s.routesByBot = make(map[string]map[string]struct{})
	s.runtimeCancel = runtimeCancel
	s.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}

	for key, connector := range connectors {
		log.Printf("starting connector %s", key)
		go connector.Run(runtimeCtx)
	}

	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req protocol.Request
		if err := decoder.Decode(&req); err != nil {
			return
		}

		if req.Action == protocol.ActionSubscribe {
			s.handleSubscribe(ctx, req, encoder)
			return
		}

		resp := s.handleRequest(ctx, req)
		if err := encoder.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) handleSubscribe(ctx context.Context, req protocol.Request, encoder *json.Encoder) {
	selector, err := s.resolveSelector(req.Service, req.Bot)
	if err != nil {
		_ = encoder.Encode(protocol.Response{OK: false, Error: err.Error()})
		return
	}

	channels := s.subscribe(selector)
	defer s.unsubscribe(selector, channels)

	if err := encoder.Encode(protocol.Response{OK: true, Ack: "subscribed"}); err != nil {
		return
	}

	// Fan-in: merge all per-bot channels into a single channel so we can
	// block cleanly instead of busy-polling.
	merged := make(chan protocol.Event, 64)
	var fanInDone sync.WaitGroup
	fanInDone.Add(len(channels))
	for _, ch := range channels {
		go func(src chan protocol.Event) {
			defer fanInDone.Done()
			for ev := range src {
				select {
				case merged <- ev:
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}
	go func() {
		fanInDone.Wait()
		close(merged)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-merged:
			if !ok {
				return
			}
			if !matchEventFilters(ev, req.Target, req.Channel, req.Thread, req.Search) {
				continue
			}
			if req.Notify && !ev.Notify {
				continue
			}
			if err := encoder.Encode(protocol.Response{OK: true, Event: &ev}); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Action {
	case protocol.ActionPing:
		return protocol.Response{OK: true, Ack: "pong"}
	case protocol.ActionBots:
		if s.debug {
			log.Printf("debug: request action=%s service=%q bot=%q", req.Action, req.Service, req.Bot)
		}
		bots := s.listBots(req.Service)
		return protocol.Response{OK: true, Bots: bots}
	case protocol.ActionNotify:
		events, err := s.listNotifications(req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Events: events}
	case protocol.ActionClearNotify:
		cleared, err := s.clearNotifications(req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Cleared: cleared, Ack: fmt.Sprintf("cleared %d notifications", cleared)}
	case protocol.ActionClearHistory:
		cleared, err := s.clearHistory(req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Cleared: cleared, Ack: fmt.Sprintf("cleared %d events", cleared)}
	case protocol.ActionHistory:
		notifyOnly := req.Notify
		events, err := s.readEvents(req.Service, req.Bot, req.Limit, req.SinceID, req.Target, req.Channel, req.Thread, req.Search, notifyOnly)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Events: events}
	case protocol.ActionSend:
		if strings.TrimSpace(req.Text) == "" {
			return protocol.Response{OK: false, Error: "text is required"}
		}
		if strings.TrimSpace(req.Target) == "" && strings.TrimSpace(req.Channel) == "" && strings.TrimSpace(req.Thread) == "" {
			return protocol.Response{OK: false, Error: "at least one of target, channel, or thread is required"}
		}

		if s.debug {
			log.Printf("debug: send request bot=%q target=%q channel=%q text=%q", req.Bot, req.Target, req.Channel, req.Text)
		}

		resolvedService, resolvedBot, err := s.resolveBotService(req.Service, req.Bot)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		// Auto-resolve channel from thread when only --thread is provided.
		if strings.TrimSpace(req.Channel) == "" && strings.TrimSpace(req.Target) == "" && strings.TrimSpace(req.Thread) != "" {
			if s.notifications != nil {
				if ch, lookupErr := s.notifications.LookupChannelByThread(resolvedService, resolvedBot, req.Thread); lookupErr == nil && ch != "" {
					req.Channel = ch
					if s.debug {
						log.Printf("debug: resolved channel %q from thread %q", ch, req.Thread)
					}
				}
			}
		}

		key := botKey(resolvedService, resolvedBot)
		s.mu.RLock()
		connector, ok := s.connectors[key]
		s.mu.RUnlock()
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("unknown bot %q for service %q", resolvedBot, resolvedService)}
		}

		s.markParticipation(key, req.Target, req.Channel, req.Thread)

		event, err := connector.Send(ctx, req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		// Annotate self flag on the send response (publish callback works on a copy).
		event.Self = connector.Identity() != "" && event.User == connector.Identity()

		return protocol.Response{OK: true, Ack: fmt.Sprintf("sent event %d", event.ID), Event: &event}
	case protocol.ActionReload:
		if err := s.reloadConfig(); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Ack: "reloaded config and services"}
	default:
		return protocol.Response{OK: false, Error: fmt.Sprintf("unsupported action: %s", req.Action)}
	}
}

func (s *Server) listBots(service string) []protocol.BotRef {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]protocol.BotRef, 0, len(s.bots))
	for key, bot := range s.bots {
		if service != "" && bot.Service != service {
			continue
		}
		if connector := s.connectors[key]; connector != nil {
			bot.BotID = connector.Identity()
		}
		result = append(result, bot)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Service == result[j].Service {
			return result[i].Name < result[j].Name
		}
		return result[i].Service < result[j].Service
	})

	return result
}

func (s *Server) readEvents(service string, bot string, limit int, sinceID int64, target string, channel string, thread string, search string, notifyOnly bool) ([]protocol.Event, error) {
	if s.notifications == nil {
		return nil, errors.New("store is not available")
	}

	_, err := s.resolveSelector(service, bot)
	if err != nil {
		return nil, err
	}

	events, err := s.notifications.ListEvents(store.EventFilter{
		Service:    service,
		Bot:        bot,
		Target:     target,
		Channel:    channel,
		Thread:     thread,
		Search:     search,
		Limit:      limit,
		SinceID:    sinceID,
		NotifyOnly: notifyOnly,
	})
	if err != nil {
		return nil, err
	}

	s.annotateSelf(events)
	return events, nil
}

func (s *Server) publish(event protocol.Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	key := botKey(event.Service, event.Bot)
	s.mu.RLock()
	botRef := s.bots[key]
	connector := s.connectors[key]
	s.mu.RUnlock()

	if connector != nil {
		botRef.BotID = connector.Identity()
	}

	event.Self = botRef.BotID != "" && event.User == botRef.BotID
	event.Mentions = mentionsAgent(event, botRef)
	event.Direct = isDirectToAgent(event)
	event.Notify = event.Direction == "in" && (event.Mentions || event.Direct || s.hasParticipation(key, event.Target, event.Channel, event.Thread))

	if event.Kind == "status" {
		log.Printf("[%s] %s", key, event.Text)
	} else if event.Kind == "message" {
		tag := event.Direction
		if event.Notify {
			if event.Direct {
				tag += " (direct)"
			} else if event.Mentions {
				tag += " (mention)"
			} else {
				tag += " (notify)"
			}
		}
		log.Printf("[%s] %s message on %s", key, tag, event.Channel)
		if s.debug {
			log.Printf("[%s] debug: target=%s channel=%s thread=%s text=%q", key, event.Target, event.Channel, event.Thread, event.Text)
		}
	} else if event.Kind == "heartbeat" {
		if s.debug {
			log.Printf("[%s] debug: heartbeat", key)
		}
	}

	if s.notifications != nil && event.Kind == "message" {
		eventID, err := s.notifications.InsertEvent(event)
		if err == nil {
			event.ID = eventID
		}

		if event.Notify {
			notificationID, notifyErr := s.notifications.InsertNotification(event)
			if notifyErr == nil {
				event.NotificationID = notificationID
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.subsByBot[key] {
		select {
		case ch <- event:
		default:
			log.Printf("warning: dropped event %d for subscriber on %s (buffer full)", event.ID, key)
		}
	}
}

func (s *Server) reloadConfig() error {
	if strings.TrimSpace(s.cfgPath) == "" {
		return errors.New("reload requires daemon --config path")
	}

	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	if s.socketOverride != "" {
		cfg.Server.SocketPath = s.socketOverride
	}
	if s.dbOverride != "" {
		cfg.Server.DBPath = s.dbOverride
	}

	s.mu.RLock()
	currentSocket := s.cfg.Server.SocketPath
	currentDB := s.cfg.Server.DBPath
	s.mu.RUnlock()

	if cfg.Server.SocketPath != currentSocket {
		return fmt.Errorf("reload cannot change socket_path at runtime (current=%q new=%q), restart daemon", currentSocket, cfg.Server.SocketPath)
	}
	if cfg.Server.DBPath != currentDB {
		return fmt.Errorf("reload cannot change db_path at runtime (current=%q new=%q), restart daemon", currentDB, cfg.Server.DBPath)
	}

	log.Printf("reloading configuration from %s", s.cfgPath)

	if err := s.startConnectors(cfg); err != nil {
		return fmt.Errorf("reload connectors: %w", err)
	}

	log.Printf("configuration reloaded (%d bot(s))", len(cfg.Bots))

	return nil
}

func (s *Server) resolveSelector(service string, bot string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if service != "" && bot != "" {
		key := botKey(service, bot)
		if _, ok := s.bots[key]; !ok {
			return nil, fmt.Errorf("unknown bot %q for service %q", bot, service)
		}
		return []string{key}, nil
	}

	// When service is empty but bot is specified, find the bot across all services
	if service == "" && bot != "" {
		var matches []string
		for key, botRef := range s.bots {
			if botRef.Name == bot {
				matches = append(matches, key)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("unknown bot %q", bot)
		}
		sort.Strings(matches)
		return matches, nil
	}

	keys := make([]string, 0)
	for key, botRef := range s.bots {
		if service != "" && botRef.Service != service {
			continue
		}
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		if service != "" {
			return nil, fmt.Errorf("unknown service %q", service)
		}
		return nil, errors.New("no bots configured")
	}

	sort.Strings(keys)
	return keys, nil
}

// resolveBotService resolves the service for a given bot name when service is
// empty. If service is already provided, it is returned as-is. Returns an error
// if the bot name is ambiguous across multiple services.
func (s *Server) resolveBotService(service string, bot string) (string, string, error) {
	if service != "" {
		return service, bot, nil
	}

	if strings.TrimSpace(bot) == "" {
		return "", "", errors.New("--bot is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var match protocol.BotRef
	var count int

	for _, botRef := range s.bots {
		if botRef.Name == bot {
			match = botRef
			count++
		}
	}

	if count == 0 {
		return "", "", fmt.Errorf("unknown bot %q", bot)
	}
	if count > 1 {
		return "", "", fmt.Errorf("ambiguous bot %q exists in multiple services, use --service to disambiguate", bot)
	}

	return match.Service, match.Name, nil
}

func (s *Server) subscribe(keys []string) []chan protocol.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	channels := make([]chan protocol.Event, 0, len(keys))
	for _, key := range keys {
		if s.subsByBot[key] == nil {
			s.subsByBot[key] = make(map[chan protocol.Event]struct{})
		}
		ch := make(chan protocol.Event, 64)
		s.subsByBot[key][ch] = struct{}{}
		channels = append(channels, ch)
	}

	return channels
}

func (s *Server) unsubscribe(keys []string, channels []chan protocol.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, key := range keys {
		ch := channels[i]
		if subs := s.subsByBot[key]; subs != nil {
			delete(subs, ch)
		}
		close(ch)
	}
}

func botKey(service string, bot string) string {
	return service + ":" + bot
}

func matchEventFilters(event protocol.Event, target string, channel string, thread string, search string) bool {
	if target != "" && event.Target != target {
		return false
	}
	if channel != "" && event.Channel != channel {
		return false
	}
	if thread != "" && event.Thread != thread {
		return false
	}
	if search != "" && !strings.Contains(strings.ToLower(event.Text), strings.ToLower(search)) {
		return false
	}
	return true
}

func (s *Server) markParticipation(key string, target string, channel string, thread string) {
	route := routeKey(target, channel, thread)
	if route == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.routesByBot[key] == nil {
		s.routesByBot[key] = make(map[string]struct{})
	}
	s.routesByBot[key][route] = struct{}{}
}

func (s *Server) hasParticipation(key string, target string, channel string, thread string) bool {
	route := routeKey(target, channel, thread)
	if route == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.routesByBot[key][route]
	return ok
}

func routeKey(target string, channel string, thread string) string {
	if target == "" && channel == "" && thread == "" {
		return ""
	}
	return "t=" + target + "|c=" + channel + "|th=" + thread
}

func mentionsAgent(event protocol.Event, bot protocol.BotRef) bool {
	text := strings.ToLower(event.Text)
	if text == "" {
		return false
	}

	nameToken := "@" + strings.ToLower(bot.Name)
	if bot.Name != "" && strings.Contains(text, nameToken) {
		return true
	}

	idToken := "<@" + strings.ToLower(bot.BotID) + ">"
	if bot.BotID != "" && strings.Contains(text, idToken) {
		return true
	}

	return false
}

func isDirectToAgent(event protocol.Event) bool {
	target := strings.ToLower(event.Target)
	if strings.HasPrefix(target, "dm:") || strings.HasPrefix(target, "direct:") || strings.HasPrefix(target, "user:") {
		return true
	}

	if strings.HasPrefix(strings.ToUpper(event.Channel), "D") {
		return true
	}

	return event.Kind == "dm"
}

// annotateSelf sets the Self flag on events where User matches the bot's
// runtime identity. This is used when serving stored events from the DB.
func (s *Server) annotateSelf(events []protocol.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range events {
		key := botKey(events[i].Service, events[i].Bot)
		if connector := s.connectors[key]; connector != nil {
			identity := connector.Identity()
			events[i].Self = identity != "" && events[i].User == identity
			if s.debug {
				log.Printf("debug: annotateSelf event=%d user=%q identity=%q self=%t", events[i].ID, events[i].User, identity, events[i].Self)
			}
		} else if s.debug {
			log.Printf("debug: annotateSelf event=%d no connector for key=%q", events[i].ID, key)
		}
	}
}

func (s *Server) listNotifications(req protocol.Request) ([]protocol.Event, error) {
	if s.notifications == nil {
		return nil, errors.New("notification store is not available")
	}

	if _, err := s.resolveSelector(req.Service, req.Bot); err != nil {
		return nil, err
	}

	events, err := s.notifications.ListNotifications(store.NotificationFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Search:  req.Search,
		Limit:   req.Limit,
		SinceID: req.SinceID,
		Unseen:  req.Unseen,
	})
	if err != nil {
		return nil, err
	}

	s.annotateSelf(events)
	return events, nil
}

func (s *Server) clearNotifications(req protocol.Request) (int64, error) {
	if s.notifications == nil {
		return 0, errors.New("notification store is not available")
	}

	if _, err := s.resolveSelector(req.Service, req.Bot); err != nil {
		return 0, err
	}

	if !req.All && req.Bot == "" && req.Target == "" && req.Channel == "" && req.Thread == "" {
		return 0, errors.New("refusing broad clear without --all (or specific filters)")
	}

	return s.notifications.DeleteNotifications(store.NotificationFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Search:  req.Search,
		Unseen:  req.Unseen,
	}, req.All)
}

func (s *Server) clearHistory(req protocol.Request) (int64, error) {
	if s.notifications == nil {
		return 0, errors.New("store is not available")
	}

	if _, err := s.resolveSelector(req.Service, req.Bot); err != nil {
		return 0, err
	}

	if !req.All && req.Bot == "" && req.Target == "" && req.Channel == "" && req.Thread == "" {
		return 0, errors.New("refusing broad clear without --all (or specific filters)")
	}

	return s.notifications.DeleteEvents(store.EventFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Search:  req.Search,
	}, req.All)
}
