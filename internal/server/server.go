package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *Server) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	s.rootCtx = ctx

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

	if err := os.Chmod(s.cfg.Server.SocketPath, 0660); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	s.listener = listener

	if err := s.startConnectors(s.cfg); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

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

	for _, service := range cfg.Services {
		for _, bot := range service.Bots {
			key := botKey(service.Name, bot.Name)
			botRef := protocol.BotRef{
				Service: service.Name,
				Name:    bot.Name,
				BotID:   bot.BotID,
			}
			bots[key] = botRef

			connector, err := upstream.NewConnector(service, bot, func(event protocol.Event) {
				event.Service = service.Name
				event.Bot = bot.Name
				s.publish(event)
			})
			if err != nil {
				return fmt.Errorf("create connector for %s: %w", key, err)
			}

			connectors[key] = connector
		}
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

	for _, connector := range connectors {
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

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		for _, ch := range channels {
			select {
			case ev := <-ch:
				if !matchEventFilters(ev, req.Target, req.Channel, req.Thread) {
					continue
				}
				if req.Notify && !ev.Notify {
					continue
				}
				if err := encoder.Encode(protocol.Response{OK: true, Event: &ev}); err != nil {
					return
				}
			default:
			}
		}

		time.Sleep(25 * time.Millisecond)
	}
}

func (s *Server) handleRequest(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Action {
	case protocol.ActionPing:
		return protocol.Response{OK: true, Ack: "pong"}
	case protocol.ActionBots:
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
	case protocol.ActionHistory:
		notifyOnly := req.Notify
		events, err := s.readEvents(req.Service, req.Bot, req.Limit, req.SinceID, req.Target, req.Channel, req.Thread, notifyOnly)
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

		key := botKey(req.Service, req.Bot)
		s.mu.RLock()
		connector, ok := s.connectors[key]
		s.mu.RUnlock()
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("unknown bot %q for service %q", req.Bot, req.Service)}
		}

		s.markParticipation(key, req.Target, req.Channel, req.Thread)

		event, err := connector.Send(ctx, req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

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
	for _, bot := range s.bots {
		if service != "" && bot.Service != service {
			continue
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

func (s *Server) readEvents(service string, bot string, limit int, sinceID int64, target string, channel string, thread string, notifyOnly bool) ([]protocol.Event, error) {
	if s.notifications == nil {
		return nil, errors.New("store is not available")
	}

	_, err := s.resolveSelector(service, bot)
	if err != nil {
		return nil, err
	}

	return s.notifications.ListEvents(store.EventFilter{
		Service:    service,
		Bot:        bot,
		Target:     target,
		Channel:    channel,
		Thread:     thread,
		Limit:      limit,
		SinceID:    sinceID,
		NotifyOnly: notifyOnly,
	})
}

func (s *Server) publish(event protocol.Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	key := botKey(event.Service, event.Bot)
	s.mu.RLock()
	botRef := s.bots[key]
	s.mu.RUnlock()

	event.Mentions = mentionsAgent(event, botRef)
	event.Direct = isDirectToAgent(event)
	event.Notify = event.Direction == "in" && (event.Mentions || event.Direct || s.hasParticipation(key, event.Target, event.Channel, event.Thread))

	if s.notifications != nil {
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

	if err := s.startConnectors(cfg); err != nil {
		return fmt.Errorf("reload connectors: %w", err)
	}

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

func matchEventFilters(event protocol.Event, target string, channel string, thread string) bool {
	if target != "" && event.Target != target {
		return false
	}
	if channel != "" && event.Channel != channel {
		return false
	}
	if thread != "" && event.Thread != thread {
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

func (s *Server) listNotifications(req protocol.Request) ([]protocol.Event, error) {
	if s.notifications == nil {
		return nil, errors.New("notification store is not available")
	}

	if _, err := s.resolveSelector(req.Service, req.Bot); err != nil {
		return nil, err
	}

	return s.notifications.ListNotifications(store.NotificationFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Limit:   req.Limit,
		SinceID: req.SinceID,
		Unseen:  req.Unseen,
	})
}

func (s *Server) clearNotifications(req protocol.Request) (int64, error) {
	if s.notifications == nil {
		return 0, errors.New("notification store is not available")
	}

	if _, err := s.resolveSelector(req.Service, req.Bot); err != nil {
		return 0, err
	}

	if req.NotificationID > 0 {
		return s.notifications.MarkSeenByID(req.NotificationID)
	}

	if !req.All && req.Bot == "" && req.Target == "" && req.Channel == "" && req.Thread == "" {
		return 0, errors.New("refusing broad clear without --all (or specific filters/id)")
	}

	unseenOnly := req.Unseen
	if !req.All {
		unseenOnly = true
	}

	return s.notifications.MarkSeen(store.NotificationFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Unseen:  unseenOnly,
	}, req.All)
}
