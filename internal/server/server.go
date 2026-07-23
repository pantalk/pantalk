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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pantalk/pantalk/internal/agent"
	"github.com/pantalk/pantalk/internal/claude"
	"github.com/pantalk/pantalk/internal/codex"
	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/media"
	"github.com/pantalk/pantalk/internal/protocol"
	"github.com/pantalk/pantalk/internal/store"
	"github.com/pantalk/pantalk/internal/upstream"
)

type Server struct {
	cfg      config.Config
	listener net.Listener
	cfgPath  string

	socketOverride string
	dbOverride     string
	debug          bool
	allowExec      bool

	startedAt time.Time
	ready     chan struct{}
	readyOnce sync.Once

	rootCtx       context.Context
	runtimeCancel context.CancelFunc

	mu            sync.RWMutex
	bots          map[string]protocol.BotRef
	subsByBot     map[string]map[chan protocol.Event]struct{}
	routesByBot   map[string]map[string]struct{}
	connectors    map[string]upstream.Connector
	notifications *store.Store
	attachments   media.Store
	agents        []agent.Runtime
	tickStop      chan struct{} // closed to stop the clock ticker
	typing        map[string]*typingLease

	// Typing lease timing overrides; zero means the package defaults.
	typingPulse time.Duration
	typingTTL   time.Duration

	startCodexClient  func(context.Context, codex.Config) (agent.CodexClient, error)
	startClaudeClient func(claude.Config) (agent.ClaudeClient, error)
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
		ready:          make(chan struct{}),
		startCodexClient: func(ctx context.Context, cfg codex.Config) (agent.CodexClient, error) {
			return codex.Start(ctx, cfg)
		},
		startClaudeClient: func(cfg claude.Config) (agent.ClaudeClient, error) {
			return claude.New(cfg)
		},
	}
}

// openMediaStore builds the attachment store described by the config. The
// "none" backend yields a store that records attachment metadata without
// fetching bytes, so disabling media never disables messaging.
func openMediaStore(cfg config.MediaConfig) (media.Store, error) {
	if cfg.Backend == config.MediaBackendNone {
		log.Printf("attachment storage disabled (server.media.backend: none)")
		return media.NoopStore{}, nil
	}

	fsStore, err := media.NewFSStore(cfg.Path, cfg.MaxBytes)
	if err != nil {
		return nil, err
	}

	log.Printf("storing attachments at %s (max %d bytes)", fsStore.Root(), cfg.MaxBytes)

	return fsStore, nil
}

// orphanGracePeriod is how long an unreferenced attachment is kept before it
// becomes eligible for collection. Bytes land in the media store before the
// event row that references them is written, so a sweep with no grace period
// could delete an attachment that is still being delivered.
const orphanGracePeriod = time.Hour

// collectOrphanedAttachments deletes stored attachments that no event or
// notification refers to any more. It is best-effort maintenance: a failure is
// logged and never propagated, since reclaiming disk is not worth failing a
// startup or a clear over.
func (s *Server) collectOrphanedAttachments(reason string) {
	if s.notifications == nil || s.attachments == nil {
		return
	}

	collector, ok := s.attachments.(media.Collector)
	if !ok {
		return
	}

	referenced, err := s.notifications.ReferencedAttachmentKeys()
	if err != nil {
		log.Printf("attachment gc (%s) skipped: %v", reason, err)
		return
	}

	result, err := collector.Collect(referenced, time.Now().Add(-orphanGracePeriod))
	if err != nil {
		log.Printf("attachment gc (%s) failed: %v", reason, err)
		return
	}

	if result.Deleted > 0 {
		log.Printf("attachment gc (%s): removed %d orphaned file(s), reclaimed %d bytes", reason, result.Deleted, result.Bytes)
	} else if s.debug {
		log.Printf("debug: attachment gc (%s): scanned %d, retained %d, removed 0", reason, result.Scanned, result.Retained)
	}
}

// mediaStoreConfigChanged reports whether the fields that shape the media
// store itself differ. AttachRoots is intentionally not compared - it is
// request-time policy, not store construction.
func mediaStoreConfigChanged(old config.MediaConfig, new config.MediaConfig) bool {
	return old.Backend != new.Backend ||
		old.Path != new.Path ||
		old.MaxBytes != new.MaxBytes
}

// validateAttachPaths enforces the server.media.attach_roots allowlist against
// every path in a send request. It is the daemon-side security boundary: the
// CLI also validates for friendlier errors, but any process with socket access
// can submit a raw request, so the check that matters lives here.
//
// Symlinks are resolved on both sides before comparison, otherwise a link
// inside an allowed root pointing at ~/.ssh would defeat the allowlist. This
// is check-then-open, so a file swapped for a symlink between this check and
// the connector's os.Open is not caught - the threat model here is a
// misbehaving or prompt-injected agent, not an attacker who can already race
// the local filesystem.
func validateAttachPaths(roots []string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	if len(roots) == 0 {
		return errors.New("outbound attachments are disabled: set server.media.attach_roots to the directories files may be attached from")
	}

	resolvedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		expanded := expandHomePath(root)

		absolute, err := filepath.Abs(expanded)
		if err != nil {
			continue
		}

		// A root that does not exist cannot admit anything; skip it rather
		// than failing every send over one stale config entry.
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			continue
		}

		resolvedRoots = append(resolvedRoots, resolved)
	}

	if len(resolvedRoots) == 0 {
		return errors.New("no usable directory in server.media.attach_roots (do the configured paths exist?)")
	}

	for _, raw := range paths {
		absolute, err := filepath.Abs(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("attachment %q: %w", raw, err)
		}

		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return fmt.Errorf("attachment %q: %w", raw, err)
		}

		info, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("attachment %q: %w", raw, err)
		}
		if info.IsDir() {
			return fmt.Errorf("attachment %q is a directory", raw)
		}

		allowed := false
		for _, root := range resolvedRoots {
			if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("attachment %q is outside the configured attach_roots", raw)
		}
	}

	return nil
}

// expandHomePath resolves a leading ~/ against the daemon's home directory so
// attach_roots entries can be written portably in YAML.
func expandHomePath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

// Typing lease timing defaults. The pulse cadence mirrors the platform
// integrations this design is borrowed from (queue.js pulses every 4s against
// Telegram's ~5s status decay). The TTL bounds a lease whose owner died
// without sending - each new typing request for the same destination renews
// it. Instances carry overrides so tests can shrink the timing without racing
// on shared globals.
const (
	defaultTypingPulseInterval = 4 * time.Second
	defaultTypingLeaseTTL      = 60 * time.Second
)

func (s *Server) typingPulseEvery() time.Duration {
	if s.typingPulse > 0 {
		return s.typingPulse
	}
	return defaultTypingPulseInterval
}

func (s *Server) typingLeaseWindow() time.Duration {
	if s.typingTTL > 0 {
		return s.typingTTL
	}
	return defaultTypingLeaseTTL
}

// typingLease keeps a "bot is typing..." indicator alive for one destination.
// The daemon owns the cadence because lease holders are external agent
// processes that cannot be relied on to re-pulse mid-generation: one request
// means "keep typing until I send, I stop, or the TTL expires".
type typingLease struct {
	stop    chan struct{}
	refresh chan struct{}
	once    sync.Once
}

func (l *typingLease) end() {
	l.once.Do(func() { close(l.stop) })
}

// typingKey scopes a lease to one bot and one destination, using the same
// destination precedence the connectors use to resolve where a send goes.
func typingKey(botKey string, req protocol.Request) string {
	destination := strings.TrimSpace(req.Channel)
	if destination == "" {
		destination = strings.TrimSpace(req.Target)
	}
	if destination == "" {
		destination = strings.TrimSpace(req.Thread)
	}
	return botKey + "\x00" + destination
}

// startTypingLease starts a lease for the request's destination, or renews the
// existing one. The first pulse happens synchronously so the caller learns
// about an unreachable platform immediately rather than from a log line.
func (s *Server) startTypingLease(botKey string, req protocol.Request) error {
	key := typingKey(botKey, req)

	s.mu.Lock()
	if s.typing == nil {
		s.typing = make(map[string]*typingLease)
	}
	if lease, ok := s.typing[key]; ok {
		s.mu.Unlock()
		// Renew rather than stack: the TTL extends, the cadence continues.
		select {
		case lease.refresh <- struct{}{}:
		default:
		}
		return nil
	}

	lease := &typingLease{
		stop:    make(chan struct{}),
		refresh: make(chan struct{}, 1),
	}
	s.typing[key] = lease
	s.mu.Unlock()

	if err := s.pulseTyping(botKey, req); err != nil {
		s.removeTypingLease(key, lease)
		return err
	}

	go s.runTypingLease(key, botKey, req, lease)

	return nil
}

func (s *Server) runTypingLease(key string, botKey string, req protocol.Request, lease *typingLease) {
	defer s.removeTypingLease(key, lease)

	ticker := time.NewTicker(s.typingPulseEvery())
	defer ticker.Stop()

	deadline := time.Now().Add(s.typingLeaseWindow())

	for {
		select {
		case <-lease.stop:
			return
		case <-lease.refresh:
			deadline = time.Now().Add(s.typingLeaseWindow())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return
			}
			// A pulse failure ends the lease: repeating a call that just
			// failed every four seconds only fills the log.
			if err := s.pulseTyping(botKey, req); err != nil {
				if s.debug {
					log.Printf("debug: typing lease %q ended: %v", key, err)
				}
				return
			}
		}
	}
}

// pulseTyping resolves the connector at call time - not capture time - so a
// lease survives config reloads and dies cleanly when its bot is removed.
func (s *Server) pulseTyping(botKey string, req protocol.Request) error {
	s.mu.RLock()
	connector := s.connectors[botKey]
	s.mu.RUnlock()

	if connector == nil {
		return fmt.Errorf("bot for %q is no longer configured", botKey)
	}

	indicator, ok := connector.(upstream.TypingIndicator)
	if !ok {
		return fmt.Errorf("connector for %q does not support typing indicators", botKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return indicator.Typing(ctx, req)
}

func (s *Server) removeTypingLease(key string, lease *typingLease) {
	lease.end()

	s.mu.Lock()
	if current, ok := s.typing[key]; ok && current == lease {
		delete(s.typing, key)
	}
	s.mu.Unlock()
}

// stopTypingLease ends the lease for one destination, if any.
func (s *Server) stopTypingLease(botKey string, req protocol.Request) {
	key := typingKey(botKey, req)

	s.mu.Lock()
	lease, ok := s.typing[key]
	s.mu.Unlock()

	if ok {
		lease.end()
	}
}

// SetDebug enables verbose debug logging.
func (s *Server) SetDebug(enabled bool) {
	s.debug = enabled
}

// SetAllowExec permits agent commands outside the default allowlist.
func (s *Server) SetAllowExec(enabled bool) {
	s.allowExec = enabled
}

func (s *Server) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return s.RunContext(ctx)
}

// Ready is closed after the socket, connectors, and configured agent runtimes
// are ready. It lets embedded callers such as `pantalk local` avoid racing the
// first client connection against server startup.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

// RunContext runs the daemon until ctx is canceled. Run remains the
// signal-aware entry point for pantalkd; embedded modes provide their own
// lifecycle through this method.
func (s *Server) RunContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("server context is required")
	}

	s.rootCtx = ctx
	s.startedAt = time.Now()

	log.Printf("opening database at %s", s.cfg.Server.DBPath)

	notificationStore, err := store.Open(s.cfg.Server.DBPath)
	if err != nil {
		return fmt.Errorf("open notification store: %w", err)
	}
	defer notificationStore.Close()
	s.notifications = notificationStore

	attachmentStore, err := openMediaStore(s.cfg.Server.Media)
	if err != nil {
		return fmt.Errorf("open media store: %w", err)
	}
	s.attachments = attachmentStore

	// Catch anything stranded by an unclean shutdown or an out-of-band edit to
	// the database.
	s.collectOrphanedAttachments("startup")

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
	defer s.stopAgentRuntime()

	log.Printf("pantalkd ready (%d bot(s) configured)", len(s.cfg.Bots))
	s.readyOnce.Do(func() { close(s.ready) })

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
		}, s.attachments)
		if err != nil {
			return fmt.Errorf("create connector for %s: %w", key, err)
		}

		connectors[key] = connector

		log.Printf("bot %s (%s) registered", bot.Name, bot.Type)
	}

	runtimeCtx, runtimeCancel := context.WithCancel(s.rootCtx)

	// Build agent runners from config.
	var runners []agent.Runtime
	for _, acfg := range cfg.Agents {
		driver := strings.TrimSpace(acfg.Driver)
		if driver == "" {
			driver = "command"
		}

		var runtime agent.Runtime
		switch driver {
		case "command":
			r, err := agent.NewRunner(agent.Config{
				Name:     acfg.Name,
				Bots:     acfg.Bots,
				When:     acfg.When,
				Command:  agent.Command(acfg.Command),
				Workdir:  acfg.Workdir,
				Buffer:   acfg.Buffer,
				Timeout:  acfg.Timeout,
				Cooldown: acfg.Cooldown,
			})
			if err != nil {
				stopRuntimes(runners)
				runtimeCancel()
				return fmt.Errorf("create agent %q: %w", acfg.Name, err)
			}
			runtime = r
		case "codex":
			client, err := s.startCodexClient(runtimeCtx, codex.Config{
				Binary: acfg.Codex.Binary,
			})
			if err != nil {
				stopRuntimes(runners)
				runtimeCancel()
				return fmt.Errorf("start codex agent %q: %w", acfg.Name, err)
			}

			r, err := agent.NewCodexRuntime(runtimeCtx, agent.CodexRuntimeConfig{
				Name:         acfg.Name,
				Bots:         acfg.Bots,
				When:         acfg.When,
				Workdir:      acfg.Workdir,
				Instructions: acfg.Instructions,
				Timeout:      time.Duration(acfg.Timeout) * time.Second,
				Model:        acfg.Codex.Model,
				Effort:       acfg.Codex.Effort,
				Sandbox:      acfg.Codex.Sandbox,
				Approval:     acfg.Codex.ApprovalPolicy,
			}, client, s.notifications, s.deliverAgentReply)
			if err != nil {
				_ = client.Close()
				stopRuntimes(runners)
				runtimeCancel()
				return fmt.Errorf("create codex agent %q: %w", acfg.Name, err)
			}
			runtime = r
		case "claude":
			client, err := s.startClaudeClient(claude.Config{
				Binary:          acfg.Claude.Binary,
				Workdir:         acfg.Workdir,
				Model:           acfg.Claude.Model,
				Effort:          acfg.Claude.Effort,
				PermissionMode:  acfg.Claude.PermissionMode,
				Instructions:    acfg.Instructions,
				AllowedTools:    acfg.Claude.AllowedTools,
				DisallowedTools: acfg.Claude.DisallowedTools,
			})
			if err != nil {
				stopRuntimes(runners)
				runtimeCancel()
				return fmt.Errorf("start claude agent %q: %w", acfg.Name, err)
			}

			r, err := agent.NewClaudeRuntime(runtimeCtx, agent.ClaudeRuntimeConfig{
				Name:    acfg.Name,
				Bots:    acfg.Bots,
				When:    acfg.When,
				Timeout: time.Duration(acfg.Timeout) * time.Second,
			}, client, s.notifications, s.deliverAgentReply)
			if err != nil {
				_ = client.Close()
				stopRuntimes(runners)
				runtimeCancel()
				return fmt.Errorf("create claude agent %q: %w", acfg.Name, err)
			}
			runtime = r
		default:
			stopRuntimes(runners)
			runtimeCancel()
			return fmt.Errorf("create agent %q: unsupported driver %q", acfg.Name, driver)
		}
		runners = append(runners, runtime)
		log.Printf("agent %s registered (driver=%s)", acfg.Name, driver)
	}

	s.mu.Lock()
	oldCancel := s.runtimeCancel
	oldAgents := s.agents
	oldTickStop := s.tickStop
	s.cfg = cfg
	s.bots = bots
	s.connectors = connectors
	s.routesByBot = make(map[string]map[string]struct{})
	s.runtimeCancel = runtimeCancel
	s.agents = runners
	s.tickStop = nil
	s.mu.Unlock()

	// Stop old agent timers and clock ticker.
	for _, r := range oldAgents {
		r.Stop()
	}

	if oldTickStop != nil {
		close(oldTickStop)
	}

	if oldCancel != nil {
		oldCancel()
	}

	for key, connector := range connectors {
		log.Printf("starting connector %s", key)
		go connector.Run(runtimeCtx)
	}

	// Start the 1-minute clock ticker if any agent uses time expressions.
	needsTick := false
	for _, r := range runners {
		if r.NeedsTick() {
			needsTick = true
			break
		}
	}
	if needsTick {
		stop := make(chan struct{})
		s.mu.Lock()
		s.tickStop = stop
		s.mu.Unlock()
		go s.runClockTicker(stop)
		log.Printf("clock ticker started (1-minute interval)")
	}

	return nil
}

func stopRuntimes(runtimes []agent.Runtime) {
	for _, runtime := range runtimes {
		runtime.Stop()
	}
}

func (s *Server) stopAgentRuntime() {
	s.mu.Lock()
	cancel := s.runtimeCancel
	runtimes := s.agents
	tickStop := s.tickStop
	s.runtimeCancel = nil
	s.agents = nil
	s.tickStop = nil
	s.mu.Unlock()

	if tickStop != nil {
		close(tickStop)
	}
	if cancel != nil {
		cancel()
	}
	stopRuntimes(runtimes)
}

func (s *Server) deliverAgentReply(ctx context.Context, event protocol.Event, text string) error {
	response := s.handleRequest(ctx, protocol.Request{
		Action:  protocol.ActionSend,
		Service: event.Service,
		Bot:     event.Bot,
		Target:  event.Target,
		Channel: event.Channel,
		Thread:  event.Thread,
		Text:    text,
		Format:  "markdown",
	})
	if !response.OK {
		return errors.New(response.Error)
	}
	return nil
}

// runClockTicker sends a synthetic tick event to all agent runners every
// minute, aligned to the top of each minute. This enables time-based
// expressions like at("9:00") and every("15m").
func (s *Server) runClockTicker(stop chan struct{}) {
	// Align to the next minute boundary so ticks fire at :00 seconds.
	now := time.Now()
	next := now.Truncate(time.Minute).Add(time.Minute)
	alignTimer := time.NewTimer(time.Until(next))

	select {
	case <-alignTimer.C:
	case <-stop:
		alignTimer.Stop()
		return
	}

	// Fire immediately at the first aligned minute.
	s.dispatchTick()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.dispatchTick()
		case <-stop:
			return
		}
	}
}

// dispatchTick generates a synthetic tick event and dispatches it to all
// agent runners that match.
func (s *Server) dispatchTick() {
	tick := agent.TickEvent()

	s.mu.RLock()
	runners := s.agents
	s.mu.RUnlock()

	for _, runner := range runners {
		if runner.Matches(tick) {
			runner.Handle(tick)
		}
	}
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
	case protocol.ActionStatus:
		return protocol.Response{OK: true, Status: s.daemonStatus()}
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
	case protocol.ActionInject:
		if strings.TrimSpace(req.Text) == "" {
			return protocol.Response{OK: false, Error: "text is required"}
		}
		if strings.TrimSpace(req.User) == "" && !req.Self {
			return protocol.Response{OK: false, Error: "user is required (or set self)"}
		}
		if strings.TrimSpace(req.Target) == "" && strings.TrimSpace(req.Channel) == "" && strings.TrimSpace(req.Thread) == "" {
			return protocol.Response{OK: false, Error: "at least one of target, channel, or thread is required"}
		}

		resolvedService, resolvedBot, err := s.resolveBotService(req.Service, req.Bot)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		key := botKey(resolvedService, resolvedBot)
		s.mu.RLock()
		connector, ok := s.connectors[key]
		s.mu.RUnlock()
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("unknown bot %q for service %q", resolvedBot, resolvedService)}
		}

		injector, ok := connector.(upstream.InboundInjector)
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("bot %q (%s) does not accept local message injection", resolvedBot, resolvedService)}
		}

		event, err := injector.Inject(ctx, req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		event.Self = connector.Identity() != "" && event.User == connector.Identity()
		s.mu.RLock()
		botRef := s.bots[key]
		s.mu.RUnlock()
		botRef.BotID = connector.Identity()
		event.Mentions = mentionsAgent(event, botRef)
		event.Direct = isDirectToAgent(event)
		event.Notify = event.Direction == "in" && !event.Self &&
			(event.Mentions || event.Direct || s.hasParticipation(key, event.Target, event.Channel, event.Thread))

		return protocol.Response{OK: true, Ack: "injected inbound message", Event: &event}
	case protocol.ActionSend:
		if strings.TrimSpace(req.Text) == "" && len(req.Attach) == 0 {
			return protocol.Response{OK: false, Error: "text is required (or attach files)"}
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

		// Refuse attachments the connector cannot deliver. Without this gate a
		// connector that ignores req.Attach would send the caption and drop
		// the files while reporting success.
		if len(req.Attach) > 0 {
			sender, supports := connector.(upstream.AttachmentSender)
			if !supports || !sender.SupportsAttachments() {
				return protocol.Response{OK: false, Error: fmt.Sprintf("bot %q (%s) does not support attachments yet", resolvedBot, resolvedService)}
			}

			s.mu.RLock()
			attachRoots := s.cfg.Server.Media.AttachRoots
			s.mu.RUnlock()

			if err := validateAttachPaths(attachRoots, req.Attach); err != nil {
				return protocol.Response{OK: false, Error: err.Error()}
			}
		}

		s.markParticipation(key, req.Target, req.Channel, req.Thread)

		event, err := connector.Send(ctx, req)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		// The reply is out - the "typing..." indicator for this destination
		// has served its purpose.
		s.stopTypingLease(key, req)

		// Annotate self flag on the send response (publish callback works on a copy).
		event.Self = connector.Identity() != "" && event.User == connector.Identity()

		return protocol.Response{OK: true, Ack: fmt.Sprintf("sent event %d", event.ID), Event: &event}
	case protocol.ActionTyping:
		if strings.TrimSpace(req.Target) == "" && strings.TrimSpace(req.Channel) == "" && strings.TrimSpace(req.Thread) == "" {
			return protocol.Response{OK: false, Error: "at least one of target, channel, or thread is required"}
		}

		resolvedService, resolvedBot, err := s.resolveBotService(req.Service, req.Bot)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		key := botKey(resolvedService, resolvedBot)
		s.mu.RLock()
		connector, ok := s.connectors[key]
		s.mu.RUnlock()
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("unknown bot %q for service %q", resolvedBot, resolvedService)}
		}

		if _, supported := connector.(upstream.TypingIndicator); !supported {
			return protocol.Response{OK: false, Error: fmt.Sprintf("bot %q (%s) does not support typing indicators yet", resolvedBot, resolvedService)}
		}

		if req.Stop {
			s.stopTypingLease(key, req)
			return protocol.Response{OK: true, Ack: "typing stopped"}
		}

		if err := s.startTypingLease(key, req); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		return protocol.Response{OK: true, Ack: fmt.Sprintf("typing until send or %s timeout", s.typingLeaseWindow())}
	case protocol.ActionReact:
		emoji := strings.TrimSpace(req.Emoji)
		if emoji == "" {
			return protocol.Response{OK: false, Error: "emoji is required"}
		}

		resolvedService, resolvedBot, err := s.resolveBotService(req.Service, req.Bot)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		key := botKey(resolvedService, resolvedBot)
		s.mu.RLock()
		connector, ok := s.connectors[key]
		s.mu.RUnlock()
		if !ok {
			return protocol.Response{OK: false, Error: fmt.Sprintf("unknown bot %q for service %q", resolvedBot, resolvedService)}
		}

		if err := connector.React(ctx, req); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}

		return protocol.Response{OK: true, Ack: "reacted"}
	case protocol.ActionReload:
		if err := s.reloadConfig(); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true, Ack: "reloaded config and services"}
	default:
		return protocol.Response{OK: false, Error: fmt.Sprintf("unsupported action: %s", req.Action)}
	}
}

// daemonStatus returns a snapshot of the daemon's current runtime state.
func (s *Server) daemonStatus() *protocol.DaemonStatus {
	s.mu.RLock()
	bots := make([]protocol.BotStatus, 0, len(s.bots))
	for _, bot := range s.bots {
		bots = append(bots, protocol.BotStatus{
			Name:        bot.Name,
			Service:     bot.Service,
			DisplayName: bot.DisplayName,
		})
	}
	sort.Slice(bots, func(i, j int) bool {
		if bots[i].Service == bots[j].Service {
			return bots[i].Name < bots[j].Name
		}
		return bots[i].Service < bots[j].Service
	})

	agents := make([]protocol.AgentInfo, 0, len(s.agents))
	for _, r := range s.agents {
		when := r.When()
		if when == "" {
			when = "notify"
		}
		agents = append(agents, protocol.AgentInfo{
			Name: r.Name(),
			When: when,
		})
	}

	now := time.Now()
	uptime := int64(0)
	if !s.startedAt.IsZero() {
		uptime = int64(now.Sub(s.startedAt).Seconds())
	}
	startedAt := s.startedAt
	notifications := s.notifications
	s.mu.RUnlock()

	status := &protocol.DaemonStatus{
		StartedAt: startedAt,
		UptimeSec: uptime,
		Bots:      bots,
		Agents:    agents,
	}

	if notifications != nil {
		stats, err := notifications.NotificationStats()
		if err == nil {
			status.Notifications = &protocol.NotifyBacklog{
				Total:  stats.Total,
				Unseen: stats.Unseen,
			}
		}
	}

	return status
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
	s.hydrateAttachments(events)
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
	event.Notify = event.Direction == "in" && !event.Self && (event.Mentions || event.Direct || s.hasParticipation(key, event.Target, event.Channel, event.Thread))

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

	// Dispatch to agent runners before taking the write lock.
	s.mu.RLock()
	agents := s.agents
	s.mu.RUnlock()

	for _, runner := range agents {
		if runner.Matches(event) {
			runner.Handle(event)
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

	cfg, err := config.LoadWithOptions(s.cfgPath, s.allowExec)
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
	currentMedia := s.cfg.Server.Media
	s.mu.RUnlock()

	if cfg.Server.SocketPath != currentSocket {
		return fmt.Errorf("reload cannot change socket_path at runtime (current=%q new=%q), restart daemon", currentSocket, cfg.Server.SocketPath)
	}
	if cfg.Server.DBPath != currentDB {
		return fmt.Errorf("reload cannot change db_path at runtime (current=%q new=%q), restart daemon", currentDB, cfg.Server.DBPath)
	}
	// The media store is built once at startup and captured by every
	// connector, so the store-shaping fields cannot change on reload.
	// AttachRoots is deliberately exempt: it is pure per-request policy read
	// from the live config, so tightening or widening it is exactly what a
	// reload is for.
	if mediaStoreConfigChanged(currentMedia, cfg.Server.Media) {
		return fmt.Errorf("reload cannot change server.media backend/path/max_bytes at runtime (current=%+v new=%+v), restart daemon", currentMedia, cfg.Server.Media)
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

// hydrateAttachments prepares attachment-bearing events for query responses.
//
// Two derived views are produced, neither of which is ever persisted:
//
//   - Path is resolved from the durable storage key against the media store
//     configured right now, rather than read back from the database, so
//     moving the storage root does not strand history.
//   - An event whose text is empty but which carries attachments gets a
//     synthetic placeholder ("[attachment: photo.jpg]") so list output and
//     text-matching consumers see that something arrived. This runs at query
//     time only; the stored row and the live stream keep the empty text.
func (s *Server) hydrateAttachments(events []protocol.Event) {
	for i := range events {
		for j := range events[i].Attachments {
			attachment := &events[i].Attachments[j]

			attachment.Path = ""
			if attachment.Key == "" || s.attachments == nil {
				continue
			}

			if path, ok := s.attachments.LocalPath(attachment.Key); ok {
				attachment.Path = path
			} else if s.debug {
				log.Printf("debug: attachment key=%q has no local path", attachment.Key)
			}
		}

		if strings.TrimSpace(events[i].Text) == "" && len(events[i].Attachments) > 0 {
			events[i].Text = attachmentPlaceholder(events[i].Attachments)
		}
	}
}

// attachmentPlaceholder renders a synthetic text stand-in for a message that
// arrived with files but no words, e.g. "[attachment: photo.jpg]". The name
// degrades to the MIME type, then to "file", so the placeholder never comes
// out empty.
func attachmentPlaceholder(attachments []protocol.Attachment) string {
	parts := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		label := attachment.Name
		if label == "" {
			label = attachment.MIME
		}
		if label == "" {
			label = "file"
		}
		parts = append(parts, "[attachment: "+label+"]")
	}
	return strings.Join(parts, " ")
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
	s.hydrateAttachments(events)
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

	cleared, err := s.notifications.DeleteNotifications(store.NotificationFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Search:  req.Search,
		Unseen:  req.Unseen,
	}, req.All)
	if err != nil {
		return cleared, err
	}

	if cleared > 0 {
		s.collectOrphanedAttachments("clear notifications")
	}

	return cleared, nil
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

	cleared, err := s.notifications.DeleteEvents(store.EventFilter{
		Service: req.Service,
		Bot:     req.Bot,
		Target:  req.Target,
		Channel: req.Channel,
		Thread:  req.Thread,
		Search:  req.Search,
	}, req.All)
	if err != nil {
		return cleared, err
	}

	if cleared > 0 {
		s.collectOrphanedAttachments("clear history")
	}

	return cleared, nil
}
