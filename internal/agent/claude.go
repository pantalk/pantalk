package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/claude"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultClaudeQueueSize = 64

// ClaudeClient is the per-turn Claude Code surface used by ClaudeRuntime.
type ClaudeClient interface {
	RunTurn(context.Context, string, string) (claude.TurnResult, error)
	Close() error
}

// ClaudeRuntimeConfig configures one Claude Code conversational agent.
type ClaudeRuntimeConfig struct {
	Name      string
	When      string
	Bots      []string
	Timeout   time.Duration
	QueueSize int
}

// ClaudeRuntime maps Pantalk conversations to durable Claude Code sessions.
// Messages in one conversation are processed in order, while separate
// conversations may run concurrently as independent Claude CLI processes.
type ClaudeRuntime struct {
	cfg      ClaudeRuntimeConfig
	matcher  *Matcher
	client   ClaudeClient
	sessions SessionStore
	reply    ReplyFunc

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	workers map[string]chan protocol.Event
	session map[string]string
	wg      sync.WaitGroup
}

var _ Runtime = (*ClaudeRuntime)(nil)

// NewClaudeRuntime creates an initialized Claude Code runtime.
func NewClaudeRuntime(
	parent context.Context,
	cfg ClaudeRuntimeConfig,
	client ClaudeClient,
	sessions SessionStore,
	reply ReplyFunc,
) (*ClaudeRuntime, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, errors.New("claude agent name is required")
	}
	if client == nil {
		return nil, fmt.Errorf("agent %q: claude client is required", cfg.Name)
	}
	if sessions == nil {
		return nil, fmt.Errorf("agent %q: session store is required", cfg.Name)
	}
	if reply == nil {
		return nil, fmt.Errorf("agent %q: reply callback is required", cfg.Name)
	}

	matcher, err := NewMatcher(cfg.Name, cfg.When, cfg.Bots)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultClaudeQueueSize
	}

	ctx, cancel := context.WithCancel(parent)
	return &ClaudeRuntime{
		cfg:      cfg,
		matcher:  matcher,
		client:   client,
		sessions: sessions,
		reply:    reply,
		ctx:      ctx,
		cancel:   cancel,
		workers:  make(map[string]chan protocol.Event),
		session:  make(map[string]string),
	}, nil
}

func (r *ClaudeRuntime) Name() string { return r.cfg.Name }

func (r *ClaudeRuntime) When() string { return r.matcher.When() }

func (r *ClaudeRuntime) Matches(event protocol.Event) bool {
	return r.matcher.Matches(event)
}

func (r *ClaudeRuntime) NeedsTick() bool {
	return r.matcher.NeedsTick()
}

// Handle queues a matching event without blocking the provider receive loop.
func (r *ClaudeRuntime) Handle(event protocol.Event) {
	conversationKey, err := ConversationKey(event)
	if err != nil {
		log.Printf("[agent:%s] cannot route event: %v", r.cfg.Name, err)
		return
	}

	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	queue := r.workers[conversationKey]
	if queue == nil {
		queue = make(chan protocol.Event, r.cfg.QueueSize)
		r.workers[conversationKey] = queue
		r.wg.Add(1)
		go r.runConversation(conversationKey, queue)
	}
	select {
	case queue <- event:
	default:
		log.Printf("[agent:%s] dropped message for busy conversation %s (queue full)", r.cfg.Name, conversationKey)
	}
	r.mu.Unlock()
}

func (r *ClaudeRuntime) runConversation(conversationKey string, queue <-chan protocol.Event) {
	defer r.wg.Done()

	for {
		select {
		case <-r.ctx.Done():
			return
		case event := <-queue:
			r.processEvent(conversationKey, event)
		}
	}
}

func (r *ClaudeRuntime) processEvent(conversationKey string, event protocol.Event) {
	sessionID, err := r.sessionForConversation(conversationKey)
	if err != nil {
		log.Printf("[agent:%s] prepare conversation: %v", r.cfg.Name, err)
		return
	}

	turnCtx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	result, err := r.client.RunTurn(turnCtx, sessionID, event.Text)
	cancel()
	if err != nil {
		log.Printf("[agent:%s] claude turn failed: %v", r.cfg.Name, err)
		return
	}
	if strings.TrimSpace(result.SessionID) == "" {
		log.Printf("[agent:%s] claude turn completed without a session id", r.cfg.Name)
		return
	}
	if result.SessionID != sessionID {
		if err := r.sessions.SaveAgentSession(r.cfg.Name, conversationKey, result.SessionID); err != nil {
			log.Printf("[agent:%s] save Claude session %s: %v", r.cfg.Name, result.SessionID, err)
			return
		}
		r.mu.Lock()
		r.session[conversationKey] = result.SessionID
		r.mu.Unlock()
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		log.Printf("[agent:%s] claude session %s completed without a final response", r.cfg.Name, result.SessionID)
		return
	}

	replyCtx, replyCancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	err = r.reply(replyCtx, event, text)
	replyCancel()
	if err != nil {
		log.Printf("[agent:%s] deliver reply: %v", r.cfg.Name, err)
	}
}

func (r *ClaudeRuntime) sessionForConversation(conversationKey string) (string, error) {
	r.mu.Lock()
	if sessionID := r.session[conversationKey]; sessionID != "" {
		r.mu.Unlock()
		return sessionID, nil
	}
	r.mu.Unlock()

	storedID, found, err := r.sessions.AgentSession(r.cfg.Name, conversationKey)
	if err != nil {
		return "", fmt.Errorf("load saved Claude session: %w", err)
	}
	if !found {
		return "", nil
	}

	r.mu.Lock()
	r.session[conversationKey] = storedID
	r.mu.Unlock()
	return storedID, nil
}

// Stop cancels queued and active turns. It is safe to call more than once.
func (r *ClaudeRuntime) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	r.cancel()
	r.mu.Unlock()

	r.wg.Wait()
	if err := r.client.Close(); err != nil {
		log.Printf("[agent:%s] close claude client: %v", r.cfg.Name, err)
	}
}
