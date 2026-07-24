package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/acp"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultACPQueueSize = 64

// ACPClient is the ACP surface used by ACPRuntime. Keeping this small makes
// the conversation/session behavior independently testable.
type ACPClient interface {
	NewSession(context.Context, string) (acp.Session, error)
	LoadSession(context.Context, string, string) error
	RunTurn(context.Context, string, string) (acp.TurnResult, error)
	Close() error
}

// ACPRuntimeConfig configures one persistent ACP agent.
type ACPRuntimeConfig struct {
	Name         string
	Workdir      string
	Instructions string
	Timeout      time.Duration
	QueueSize    int
}

// ACPRuntime maps Pantalk conversations to durable ACP agent sessions.
// Messages in one conversation are processed in order, while separate
// conversations may run concurrently through the same managed ACP process.
type ACPRuntime struct {
	cfg      ACPRuntimeConfig
	client   ACPClient
	sessions SessionStore
	reply    ReplyFunc

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	stopped  bool
	workers  map[string]chan protocol.Event
	acquired map[string]string
	wg       sync.WaitGroup
}

var _ Runtime = (*ACPRuntime)(nil)

// NewACPRuntime creates an initialized persistent runtime. The caller owns
// the supplied client only until this succeeds; afterwards Stop closes it.
func NewACPRuntime(
	parent context.Context,
	cfg ACPRuntimeConfig,
	client ACPClient,
	sessions SessionStore,
	reply ReplyFunc,
) (*ACPRuntime, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, errors.New("acp agent name is required")
	}
	if client == nil {
		return nil, fmt.Errorf("agent %q: acp client is required", cfg.Name)
	}
	if sessions == nil {
		return nil, fmt.Errorf("agent %q: session store is required", cfg.Name)
	}
	if reply == nil {
		return nil, fmt.Errorf("agent %q: reply callback is required", cfg.Name)
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultACPQueueSize
	}

	ctx, cancel := context.WithCancel(parent)
	return &ACPRuntime{
		cfg:      cfg,
		client:   client,
		sessions: sessions,
		reply:    reply,
		ctx:      ctx,
		cancel:   cancel,
		workers:  make(map[string]chan protocol.Event),
		acquired: make(map[string]string),
	}, nil
}

func (r *ACPRuntime) Name() string { return r.cfg.Name }

// Handle queues a matching event without blocking the provider receive loop.
func (r *ACPRuntime) Handle(event protocol.Event) {
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

func (r *ACPRuntime) runConversation(conversationKey string, queue <-chan protocol.Event) {
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

func (r *ACPRuntime) processEvent(conversationKey string, event protocol.Event) {
	sessionID, fresh, err := r.sessionForConversation(conversationKey)
	if err != nil {
		log.Printf("[agent:%s] prepare conversation: %v", r.cfg.Name, err)
		deliverAgentFailure(r.ctx, r.cfg.Name, r.reply, event, acpFailureReply)
		return
	}

	// ACP has no per-session instruction channel, so persistent developer
	// instructions ride in front of a fresh session's first prompt.
	prompt := event.Text
	if fresh {
		if instructions := strings.TrimSpace(r.cfg.Instructions); instructions != "" {
			prompt = instructions + "\n\n" + prompt
		}
	}

	turnCtx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	result, err := r.client.RunTurn(turnCtx, sessionID, prompt)
	cancel()
	if err != nil {
		log.Printf("[agent:%s] acp turn failed: %v", r.cfg.Name, err)
		deliverAgentFailure(r.ctx, r.cfg.Name, r.reply, event, acpFailureReply)
		return
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		log.Printf(
			"[agent:%s] acp session %s completed without a final response (stop reason %q)",
			r.cfg.Name, sessionID, result.StopReason,
		)
		deliverAgentFailure(r.ctx, r.cfg.Name, r.reply, event, acpFailureReply)
		return
	}

	replyCtx, replyCancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	err = r.reply(replyCtx, event, text)
	replyCancel()
	if err != nil {
		log.Printf("[agent:%s] deliver reply: %v", r.cfg.Name, err)
	}
}

// sessionForConversation returns the ACP session bound to the conversation.
// fresh reports that a brand-new session was created by this call, meaning no
// prior context exists on the agent side.
func (r *ACPRuntime) sessionForConversation(conversationKey string) (sessionID string, fresh bool, err error) {
	r.mu.Lock()
	if sessionID := r.acquired[conversationKey]; sessionID != "" {
		r.mu.Unlock()
		return sessionID, false, nil
	}
	r.mu.Unlock()

	storedID, found, err := r.sessions.AgentSession(r.cfg.Name, conversationKey)
	if err != nil {
		return "", false, fmt.Errorf("load saved ACP session: %w", err)
	}

	if found {
		err := r.client.LoadSession(r.ctx, storedID, r.cfg.Workdir)
		if err == nil {
			r.mu.Lock()
			r.acquired[conversationKey] = storedID
			r.mu.Unlock()
			return storedID, false, nil
		}
		log.Printf("[agent:%s] cannot load ACP session %s; starting a replacement: %v", r.cfg.Name, storedID, err)
	}

	session, err := r.client.NewSession(r.ctx, r.cfg.Workdir)
	if err != nil {
		return "", false, fmt.Errorf("start ACP session: %w", err)
	}
	if strings.TrimSpace(session.ID) == "" {
		return "", false, errors.New("acp agent returned an empty session id")
	}
	if err := r.sessions.SaveAgentSession(r.cfg.Name, conversationKey, session.ID); err != nil {
		return "", false, fmt.Errorf("save ACP session %s: %w", session.ID, err)
	}

	r.mu.Lock()
	r.acquired[conversationKey] = session.ID
	r.mu.Unlock()
	return session.ID, true, nil
}

// Stop cancels queued and active turns, then closes the managed ACP process.
// It is safe to call more than once.
func (r *ACPRuntime) Stop() {
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
		log.Printf("[agent:%s] close acp client: %v", r.cfg.Name, err)
	}
}
