package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/codex"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultCodexQueueSize = 64

// CodexClient is the app-server surface used by CodexRuntime. Keeping this
// small makes the conversation/session behavior independently testable.
type CodexClient interface {
	StartThread(context.Context, codex.ThreadOptions) (*codex.Thread, error)
	ResumeThread(context.Context, string, codex.ThreadOptions) (*codex.Thread, error)
	RunTurnWithOptions(context.Context, string, string, codex.TurnOptions) (codex.TurnResult, error)
	Close() error
}

// SessionStore persists the opaque agent session assigned to a Pantalk
// conversation.
type SessionStore interface {
	AgentSession(agent, conversationKey string) (sessionID string, found bool, err error)
	SaveAgentSession(agent, conversationKey, sessionID string) error
}

// ReplyFunc delivers one completed agent response through the originating bot.
type ReplyFunc func(context.Context, protocol.Event, string) error

// CodexRuntimeConfig configures one persistent Codex app-server agent.
type CodexRuntimeConfig struct {
	Name         string
	When         string
	Bots         []string
	Workdir      string
	Instructions string
	Timeout      time.Duration
	Model        string
	Effort       string
	Sandbox      string
	Approval     string
	QueueSize    int
}

// CodexRuntime maps Pantalk conversations to durable Codex threads. Messages
// in one conversation are processed in order, while separate conversations
// may run concurrently through the same managed app-server process.
type CodexRuntime struct {
	cfg      CodexRuntimeConfig
	matcher  *Matcher
	client   CodexClient
	sessions SessionStore
	reply    ReplyFunc

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	workers map[string]chan protocol.Event
	threads map[string]string
	wg      sync.WaitGroup
}

var _ Runtime = (*CodexRuntime)(nil)

// NewCodexRuntime creates an initialized persistent runtime. The caller owns
// the supplied client only until this succeeds; afterwards Stop closes it.
func NewCodexRuntime(
	parent context.Context,
	cfg CodexRuntimeConfig,
	client CodexClient,
	sessions SessionStore,
	reply ReplyFunc,
) (*CodexRuntime, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, errors.New("codex agent name is required")
	}
	if client == nil {
		return nil, fmt.Errorf("agent %q: codex client is required", cfg.Name)
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
		cfg.QueueSize = defaultCodexQueueSize
	}

	ctx, cancel := context.WithCancel(parent)
	return &CodexRuntime{
		cfg:      cfg,
		matcher:  matcher,
		client:   client,
		sessions: sessions,
		reply:    reply,
		ctx:      ctx,
		cancel:   cancel,
		workers:  make(map[string]chan protocol.Event),
		threads:  make(map[string]string),
	}, nil
}

func (r *CodexRuntime) Name() string { return r.cfg.Name }

func (r *CodexRuntime) When() string { return r.matcher.When() }

func (r *CodexRuntime) Matches(event protocol.Event) bool {
	return r.matcher.Matches(event)
}

func (r *CodexRuntime) NeedsTick() bool {
	return r.matcher.NeedsTick()
}

// Handle queues a matching event without blocking the provider receive loop.
func (r *CodexRuntime) Handle(event protocol.Event) {
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

func (r *CodexRuntime) runConversation(conversationKey string, queue <-chan protocol.Event) {
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

func (r *CodexRuntime) processEvent(conversationKey string, event protocol.Event) {
	threadID, err := r.threadForConversation(conversationKey)
	if err != nil {
		log.Printf("[agent:%s] prepare conversation: %v", r.cfg.Name, err)
		return
	}

	turnCtx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	result, err := r.client.RunTurnWithOptions(
		turnCtx,
		threadID,
		event.Text,
		codex.TurnOptions{Effort: r.cfg.Effort},
	)
	cancel()
	if err != nil {
		log.Printf("[agent:%s] codex turn failed: %v", r.cfg.Name, err)
		return
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		log.Printf("[agent:%s] codex turn %s completed without a final response", r.cfg.Name, result.TurnID)
		return
	}

	replyCtx, replyCancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	err = r.reply(replyCtx, event, text)
	replyCancel()
	if err != nil {
		log.Printf("[agent:%s] deliver reply: %v", r.cfg.Name, err)
	}
}

func (r *CodexRuntime) threadForConversation(conversationKey string) (string, error) {
	r.mu.Lock()
	if threadID := r.threads[conversationKey]; threadID != "" {
		r.mu.Unlock()
		return threadID, nil
	}
	r.mu.Unlock()

	opts := codex.ThreadOptions{
		CWD:                   r.cfg.Workdir,
		Model:                 r.cfg.Model,
		Sandbox:               r.cfg.Sandbox,
		ApprovalPolicy:        r.cfg.Approval,
		DeveloperInstructions: r.cfg.Instructions,
	}

	storedID, found, err := r.sessions.AgentSession(r.cfg.Name, conversationKey)
	if err != nil {
		return "", fmt.Errorf("load saved Codex thread: %w", err)
	}

	var thread *codex.Thread
	if found {
		thread, err = r.client.ResumeThread(r.ctx, storedID, opts)
		if err != nil {
			log.Printf("[agent:%s] cannot resume Codex thread %s; starting a replacement: %v", r.cfg.Name, storedID, err)
		}
	}
	if thread == nil {
		thread, err = r.client.StartThread(r.ctx, opts)
		if err != nil {
			return "", fmt.Errorf("start Codex thread: %w", err)
		}
	}
	if strings.TrimSpace(thread.ID) == "" {
		return "", errors.New("Codex returned an empty thread id")
	}
	if err := r.sessions.SaveAgentSession(r.cfg.Name, conversationKey, thread.ID); err != nil {
		return "", fmt.Errorf("save Codex thread %s: %w", thread.ID, err)
	}

	r.mu.Lock()
	r.threads[conversationKey] = thread.ID
	r.mu.Unlock()
	return thread.ID, nil
}

// Stop cancels queued and active turns, then closes the managed app-server
// process. It is safe to call more than once.
func (r *CodexRuntime) Stop() {
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
		log.Printf("[agent:%s] close codex app-server: %v", r.cfg.Name, err)
	}
}
