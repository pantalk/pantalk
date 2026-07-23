package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
	"github.com/pantalk/pantalk/internal/store"
	"github.com/pantalk/pantalk/internal/upstream"
)

func TestBotKey(t *testing.T) {
	tests := []struct {
		service string
		bot     string
		want    string
	}{
		{"slack", "ops-bot", "slack:ops-bot"},
		{"discord", "ops", "discord:ops"},
		{"", "", ":"},
	}

	for _, tt := range tests {
		got := botKey(tt.service, tt.bot)
		if got != tt.want {
			t.Errorf("botKey(%q, %q) = %q, want %q", tt.service, tt.bot, got, tt.want)
		}
	}
}

func TestRouteKey(t *testing.T) {
	tests := []struct {
		target  string
		channel string
		thread  string
		want    string
	}{
		{"", "", "", ""},
		{"t1", "c1", "th1", "t=t1|c=c1|th=th1"},
		{"", "c1", "", "t=|c=c1|th="},
		{"t1", "", "", "t=t1|c=|th="},
	}

	for _, tt := range tests {
		got := routeKey(tt.target, tt.channel, tt.thread)
		if got != tt.want {
			t.Errorf("routeKey(%q, %q, %q) = %q, want %q", tt.target, tt.channel, tt.thread, got, tt.want)
		}
	}
}

func TestMatchEventFilters(t *testing.T) {
	event := protocol.Event{
		Target:  "channel:C1",
		Channel: "C1",
		Thread:  "T100",
		Text:    "deploy to production",
	}

	tests := []struct {
		name    string
		target  string
		channel string
		thread  string
		search  string
		want    bool
	}{
		{"no filters", "", "", "", "", true},
		{"matching target", "channel:C1", "", "", "", true},
		{"wrong target", "channel:C2", "", "", "", false},
		{"matching channel", "", "C1", "", "", true},
		{"wrong channel", "", "C2", "", "", false},
		{"matching thread", "", "", "T100", "", true},
		{"wrong thread", "", "", "T200", "", false},
		{"all match", "channel:C1", "C1", "T100", "", true},
		{"one mismatch", "channel:C1", "C1", "T200", "", false},
		{"search match", "", "", "", "deploy", true},
		{"search match case-insensitive", "", "", "", "DEPLOY", true},
		{"search no match", "", "", "", "rollback", false},
		{"search with channel match", "", "C1", "", "production", true},
		{"search with channel mismatch", "", "C2", "", "deploy", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchEventFilters(event, tt.target, tt.channel, tt.thread, tt.search)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMentionsAgent(t *testing.T) {
	bot := protocol.BotRef{
		Name:  "helper-bot",
		BotID: "U123ABC",
	}

	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty text", "", false},
		{"no mention", "hello world", false},
		{"name mention", "hey @helper-bot how are you", true},
		{"name mention case-insensitive", "HEY @HELPER-BOT please", true},
		{"id mention slack format", "hello <@U123ABC> please help", true},
		{"id mention case-insensitive", "hello <@u123abc> please help", true},
		{"partial name no at", "helper-bot", false},
		{"partial id no brackets", "@U123ABC", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := protocol.Event{Text: tt.text}
			got := mentionsAgent(event, bot)
			if got != tt.want {
				t.Errorf("mentionsAgent(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestMentionsAgent_EmptyBot(t *testing.T) {
	bot := protocol.BotRef{}
	event := protocol.Event{Text: "@something <@other>"}
	if mentionsAgent(event, bot) {
		t.Error("expected false for empty bot ref")
	}
}

func TestIsDirectToAgent(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		channel string
		kind    string
		want    bool
	}{
		{"dm prefix", "dm:user123", "", "", true},
		{"direct prefix", "direct:user123", "", "", true},
		{"user prefix", "user:someone", "", "", true},
		{"channel prefix", "channel:C1", "", "", false},
		{"slack DM channel", "", "D0123456", "", true},
		{"slack DM channel lower", "", "d0123456", "", true},
		{"normal channel", "", "C0123456", "", false},
		{"dm kind", "", "", "dm", true},
		{"message kind", "", "", "message", false},
		{"no indicators", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := protocol.Event{
				Target:  tt.target,
				Channel: tt.channel,
				Kind:    tt.kind,
			}
			got := isDirectToAgent(event)
			if got != tt.want {
				t.Errorf("isDirectToAgent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParticipation(t *testing.T) {
	s := &Server{
		routesByBot: make(map[string]map[string]struct{}),
	}

	key := "slack:bot-a"

	// no participation yet
	if s.hasParticipation(key, "", "C1", "T1") {
		t.Fatal("expected no participation initially")
	}

	// mark participation
	s.markParticipation(key, "", "C1", "T1")

	if !s.hasParticipation(key, "", "C1", "T1") {
		t.Fatal("expected participation after marking")
	}

	// different thread = no participation
	if s.hasParticipation(key, "", "C1", "T2") {
		t.Fatal("expected no participation for different thread")
	}

	// empty route = no-op
	s.markParticipation(key, "", "", "")
	if s.hasParticipation(key, "", "", "") {
		t.Fatal("expected no participation for empty route")
	}
}

func TestResolveSelector(t *testing.T) {
	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot": {Service: "slack", Name: "ops-bot"},
			"slack:eng-bot": {Service: "slack", Name: "eng-bot"},
			"discord:ops":   {Service: "discord", Name: "ops"},
		},
	}

	// specific bot
	keys, err := s.resolveSelector("slack", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != "slack:ops-bot" {
		t.Fatalf("unexpected keys: %v", keys)
	}

	// all bots for a service
	keys, err = s.resolveSelector("slack", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 slack bots, got %d", len(keys))
	}

	// all bots
	keys, err = s.resolveSelector("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 total bots, got %d", len(keys))
	}

	// unknown bot
	_, err = s.resolveSelector("slack", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown bot")
	}

	// unknown service
	_, err = s.resolveSelector("matrix", "")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}

	// bot name without service - should find across all services
	keys, err = s.resolveSelector("", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != "slack:ops-bot" {
		t.Fatalf("expected [slack:ops-bot], got %v", keys)
	}

	// unknown bot without service
	_, err = s.resolveSelector("", "ghost")
	if err == nil {
		t.Fatal("expected error for unknown bot without service")
	}
}

func TestResolveBotService(t *testing.T) {
	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot":  {Service: "slack", Name: "ops-bot"},
			"slack:eng-bot":  {Service: "slack", Name: "eng-bot"},
			"discord:ops":    {Service: "discord", Name: "ops"},
			"telegram:alert": {Service: "telegram", Name: "alert"},
		},
	}

	// explicit service passthrough
	svc, bot, err := s.resolveBotService("slack", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != "slack" || bot != "ops-bot" {
		t.Fatalf("expected slack/ops-bot, got %s/%s", svc, bot)
	}

	// resolve bot without service - unique bot name
	svc, bot, err = s.resolveBotService("", "ops")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != "discord" || bot != "ops" {
		t.Fatalf("expected discord/ops, got %s/%s", svc, bot)
	}

	// unknown bot
	_, _, err = s.resolveBotService("", "ghost")
	if err == nil {
		t.Fatal("expected error for unknown bot")
	}

	// empty bot name
	_, _, err = s.resolveBotService("", "")
	if err == nil {
		t.Fatal("expected error for empty bot")
	}

	// ambiguous bot - add a duplicate name across services
	s.bots["telegram:ops"] = protocol.BotRef{Service: "telegram", Name: "ops"}
	_, _, err = s.resolveBotService("", "ops")
	if err == nil {
		t.Fatal("expected error for ambiguous bot")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got: %v", err)
	}
}

func TestHandleRequest_React_MissingEmoji(t *testing.T) {
	s := &Server{
		bots:       make(map[string]protocol.BotRef),
		connectors: make(map[string]upstream.Connector),
	}

	resp := s.handleRequest(nil, protocol.Request{
		Action: protocol.ActionReact,
		Bot:    "ops-bot",
		Emoji:  "",
	})

	if resp.OK {
		t.Fatal("expected error response for missing emoji")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestHandleRequest_React_UnknownBot(t *testing.T) {
	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot": {Service: "slack", Name: "ops-bot"},
		},
		connectors: make(map[string]upstream.Connector),
	}

	resp := s.handleRequest(nil, protocol.Request{
		Action:  protocol.ActionReact,
		Service: "slack",
		Bot:     "ops-bot",
		Channel: "C0123",
		Thread:  "1700000000.123456",
		Emoji:   "white_check_mark",
	})

	if resp.OK {
		t.Fatal("expected error response for unknown connector")
	}
}

func TestDaemonStatus_IncludesNotificationBacklog(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pantalk-status.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ev := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   "slack",
		Bot:       "ops-bot",
		Kind:      "message",
		Direction: "in",
		Notify:    true,
		Channel:   "C1",
		Text:      "first",
	}
	evID, err := st.InsertEvent(ev)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	ev.ID = evID
	firstNotificationID, err := st.InsertNotification(ev)
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	ev.Text = "second"
	ev.Timestamp = time.Now().UTC()
	evID, err = st.InsertEvent(ev)
	if err != nil {
		t.Fatalf("insert event #2: %v", err)
	}
	ev.ID = evID
	if _, err := st.InsertNotification(ev); err != nil {
		t.Fatalf("insert notification #2: %v", err)
	}

	if _, err := st.MarkSeenByID(firstNotificationID); err != nil {
		t.Fatalf("mark seen: %v", err)
	}

	s := &Server{
		startedAt:     time.Now().Add(-time.Minute),
		notifications: st,
		bots:          make(map[string]protocol.BotRef),
		connectors:    make(map[string]upstream.Connector),
		routesByBot:   make(map[string]map[string]struct{}),
		subsByBot:     make(map[string]map[chan protocol.Event]struct{}),
	}

	status := s.daemonStatus()
	if status.Notifications == nil {
		t.Fatal("expected notifications backlog in status")
	}
	if status.Notifications.Total != 2 {
		t.Fatalf("expected total=2, got %d", status.Notifications.Total)
	}
	if status.Notifications.Unseen != 1 {
		t.Fatalf("expected unseen=1, got %d", status.Notifications.Unseen)
	}
}

// A send carrying attachments must be refused for connectors that would
// silently drop them, and the error must name the bot.
func TestHandleRequest_Send_AttachmentsRejectedForUnsupportedConnector(t *testing.T) {
	mock := upstream.NewMockConnector("slack", "ops-bot", func(protocol.Event) {})

	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot": {Service: "slack", Name: "ops-bot"},
		},
		connectors: map[string]upstream.Connector{
			"slack:ops-bot": mock,
		},
		routesByBot: make(map[string]map[string]struct{}),
	}

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action:  protocol.ActionSend,
		Service: "slack",
		Bot:     "ops-bot",
		Channel: "C0123",
		Text:    "caption",
		Attach:  []string{"/tmp/file.png"},
	})

	if resp.OK {
		t.Fatal("expected attachment send to be rejected for a connector without support")
	}
	if !strings.Contains(resp.Error, "does not support attachments") {
		t.Fatalf("error = %q, want an unsupported-attachments message", resp.Error)
	}
}

// An attachment-only send must pass the daemon's text guard; it should fail
// on capability (mock connector), not on "text is required".
func TestHandleRequest_Send_AttachmentOnlyPassesTextGuard(t *testing.T) {
	mock := upstream.NewMockConnector("slack", "ops-bot", func(protocol.Event) {})

	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot": {Service: "slack", Name: "ops-bot"},
		},
		connectors: map[string]upstream.Connector{
			"slack:ops-bot": mock,
		},
		routesByBot: make(map[string]map[string]struct{}),
	}

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action:  protocol.ActionSend,
		Service: "slack",
		Bot:     "ops-bot",
		Channel: "C0123",
		Attach:  []string{"/tmp/file.png"},
	})

	if resp.OK {
		t.Fatal("expected capability rejection")
	}
	if strings.Contains(resp.Error, "text is required") {
		t.Fatalf("attachment-only send hit the text guard: %q", resp.Error)
	}
}

// A send with neither text nor attachments keeps failing fast.
func TestHandleRequest_Send_EmptyRequestStillRejected(t *testing.T) {
	s := &Server{
		bots:       make(map[string]protocol.BotRef),
		connectors: make(map[string]upstream.Connector),
	}

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action:  protocol.ActionSend,
		Bot:     "ops-bot",
		Channel: "C0123",
	})

	if resp.OK {
		t.Fatal("expected rejection for empty send")
	}
}

func TestValidateAttachPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	inside := filepath.Join(root, "report.pdf")
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write inside: %v", err)
	}
	stray := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(stray, []byte("no"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	// Allowed: file inside a configured root.
	if err := validateAttachPaths([]string{root}, []string{inside}); err != nil {
		t.Fatalf("inside root rejected: %v", err)
	}

	// Rejected: file outside every root, error names the policy.
	err := validateAttachPaths([]string{root}, []string{stray})
	if err == nil || !strings.Contains(err.Error(), "attach_roots") {
		t.Fatalf("outside root: err = %v, want attach_roots rejection", err)
	}

	// Rejected: one bad path poisons the whole request.
	if err := validateAttachPaths([]string{root}, []string{inside, stray}); err == nil {
		t.Fatal("mixed request was accepted")
	}

	// Rejected: no roots configured, error tells the user which key to set.
	err = validateAttachPaths(nil, []string{inside})
	if err == nil || !strings.Contains(err.Error(), "server.media.attach_roots") {
		t.Fatalf("no roots: err = %v, want config guidance", err)
	}

	// No attachments: nothing to check even with no roots.
	if err := validateAttachPaths(nil, nil); err != nil {
		t.Fatalf("empty request rejected: %v", err)
	}

	// Rejected: directories cannot be attached.
	if err := validateAttachPaths([]string{root}, []string{root}); err == nil {
		t.Fatal("directory was accepted")
	}

	// Rejected: nonexistent file.
	if err := validateAttachPaths([]string{root}, []string{filepath.Join(root, "missing.txt")}); err == nil {
		t.Fatal("missing file was accepted")
	}

	// A configured root that does not exist is skipped; with no usable roots
	// left the request is refused rather than silently allowed.
	err = validateAttachPaths([]string{filepath.Join(root, "gone")}, []string{inside})
	if err == nil || !strings.Contains(err.Error(), "usable") {
		t.Fatalf("unusable roots: err = %v", err)
	}
}

// A symlink inside an allowed root must not smuggle in a file from outside it.
func TestValidateAttachPathsResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "id_ed25519")
	if err := os.WriteFile(secret, []byte("key material"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	link := filepath.Join(root, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := validateAttachPaths([]string{root}, []string{link}); err == nil {
		t.Fatal("symlink escaping the root was accepted")
	}
}

func TestMediaStoreConfigChanged(t *testing.T) {
	base := config.MediaConfig{Backend: "fs", Path: "/data/media", MaxBytes: 1024}

	same := base
	same.AttachRoots = []string{"/home/user/exports"}
	if mediaStoreConfigChanged(base, same) {
		t.Fatal("attach_roots change flagged as a store change - reload would be refused for pure policy")
	}

	for name, changed := range map[string]config.MediaConfig{
		"backend":   {Backend: "none", Path: base.Path, MaxBytes: base.MaxBytes},
		"path":      {Backend: base.Backend, Path: "/elsewhere", MaxBytes: base.MaxBytes},
		"max_bytes": {Backend: base.Backend, Path: base.Path, MaxBytes: 2048},
	} {
		if !mediaStoreConfigChanged(base, changed) {
			t.Fatalf("%s change not flagged", name)
		}
	}
}

// Attachment-only messages get a synthetic placeholder at query time; stored
// text and captioned messages are untouched.
func TestHydrateAttachmentsSynthesizesPlaceholder(t *testing.T) {
	s := &Server{} // no media store: synthesis must not depend on one

	events := []protocol.Event{
		{Text: "", Attachments: []protocol.Attachment{{Name: "photo.jpg"}}},
		{Text: "", Attachments: []protocol.Attachment{{MIME: "audio/ogg"}}},
		{Text: "", Attachments: []protocol.Attachment{{}}},
		{Text: "already has words", Attachments: []protocol.Attachment{{Name: "photo.jpg"}}},
		{Text: "", Attachments: []protocol.Attachment{{Name: "a.png"}, {Name: "b.pdf"}}},
		{Text: ""},
	}

	s.hydrateAttachments(events)

	if events[0].Text != "[attachment: photo.jpg]" {
		t.Fatalf("name placeholder = %q", events[0].Text)
	}
	if events[1].Text != "[attachment: audio/ogg]" {
		t.Fatalf("mime fallback = %q", events[1].Text)
	}
	if events[2].Text != "[attachment: file]" {
		t.Fatalf("bare fallback = %q", events[2].Text)
	}
	if events[3].Text != "already has words" {
		t.Fatalf("captioned message rewritten: %q", events[3].Text)
	}
	if events[4].Text != "[attachment: a.png] [attachment: b.pdf]" {
		t.Fatalf("multi placeholder = %q", events[4].Text)
	}
	if events[5].Text != "" {
		t.Fatalf("attachment-less event gained text: %q", events[5].Text)
	}
}

// typingFake is a Connector + TypingIndicator that counts pulses.
type typingFake struct {
	mu     sync.Mutex
	pulses int
	fail   bool
}

func (f *typingFake) Run(context.Context) {}
func (f *typingFake) Send(_ context.Context, req protocol.Request) (protocol.Event, error) {
	return protocol.Event{Service: "fake", Bot: "bot", Kind: "message", Direction: "out", Text: req.Text}, nil
}
func (f *typingFake) React(context.Context, protocol.Request) error { return nil }
func (f *typingFake) Identity() string                              { return "fake-bot" }
func (f *typingFake) Typing(context.Context, protocol.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("simulated failure")
	}
	f.pulses++
	return nil
}
func (f *typingFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pulses
}

func newTypingTestServer(fake *typingFake, pulse time.Duration, ttl time.Duration) *Server {
	return &Server{
		bots: map[string]protocol.BotRef{
			"fake:bot": {Service: "fake", Name: "bot"},
		},
		connectors: map[string]upstream.Connector{
			"fake:bot": fake,
		},
		routesByBot: make(map[string]map[string]struct{}),
		typingPulse: pulse,
		typingTTL:   ttl,
	}
}

func TestTypingRejectedForUnsupportedConnector(t *testing.T) {
	mock := upstream.NewMockConnector("slack", "ops-bot", func(protocol.Event) {})
	s := &Server{
		bots:       map[string]protocol.BotRef{"slack:ops-bot": {Service: "slack", Name: "ops-bot"}},
		connectors: map[string]upstream.Connector{"slack:ops-bot": mock},
	}

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action:  protocol.ActionTyping,
		Service: "slack",
		Bot:     "ops-bot",
		Channel: "C0123",
	})

	if resp.OK {
		t.Fatal("typing accepted for a connector without support")
	}
	if !strings.Contains(resp.Error, "does not support typing") {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestTypingLeasePulsesUntilTTLExpires(t *testing.T) {
	fake := &typingFake{}
	s := newTypingTestServer(fake, 10*time.Millisecond, 60*time.Millisecond)

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1",
	})
	if !resp.OK {
		t.Fatalf("typing start failed: %s", resp.Error)
	}

	// Wait past the TTL; the lease must have pulsed several times and then
	// removed itself.
	time.Sleep(200 * time.Millisecond)

	if got := fake.count(); got < 3 {
		t.Fatalf("pulses = %d, want >= 3 (immediate + cadence)", got)
	}

	s.mu.RLock()
	remaining := len(s.typing)
	s.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("%d lease(s) survived the TTL", remaining)
	}

	settled := fake.count()
	time.Sleep(50 * time.Millisecond)
	if fake.count() != settled {
		t.Fatal("lease kept pulsing after expiry")
	}
}

func TestTypingLeaseStoppedBySend(t *testing.T) {
	fake := &typingFake{}
	s := newTypingTestServer(fake, 10*time.Millisecond, time.Minute)

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1",
	})
	if !resp.OK {
		t.Fatalf("typing start failed: %s", resp.Error)
	}

	// A send to the same destination ends the lease.
	resp = s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionSend, Service: "fake", Bot: "bot", Channel: "C1", Text: "reply",
	})
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}

	// Give the lease goroutine a beat to observe the stop.
	time.Sleep(30 * time.Millisecond)
	settled := fake.count()
	time.Sleep(50 * time.Millisecond)
	if fake.count() != settled {
		t.Fatal("lease kept pulsing after the reply was sent")
	}

	s.mu.RLock()
	remaining := len(s.typing)
	s.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("%d lease(s) survived the send", remaining)
	}
}

func TestTypingLeaseExplicitStop(t *testing.T) {
	fake := &typingFake{}
	s := newTypingTestServer(fake, 10*time.Millisecond, time.Minute)

	if resp := s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1",
	}); !resp.OK {
		t.Fatalf("typing start failed: %s", resp.Error)
	}

	if resp := s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1", Stop: true,
	}); !resp.OK {
		t.Fatalf("typing stop failed: %s", resp.Error)
	}

	time.Sleep(30 * time.Millisecond)
	settled := fake.count()
	time.Sleep(50 * time.Millisecond)
	if fake.count() != settled {
		t.Fatal("lease kept pulsing after explicit stop")
	}
}

// A failed first pulse must fail the request and leave no lease behind.
func TestTypingLeaseFailedFirstPulse(t *testing.T) {
	fake := &typingFake{fail: true}
	s := newTypingTestServer(fake, 10*time.Millisecond, time.Minute)

	resp := s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1",
	})
	if resp.OK {
		t.Fatal("typing start succeeded despite pulse failure")
	}

	s.mu.RLock()
	remaining := len(s.typing)
	s.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("%d lease(s) left after failed start", remaining)
	}
}

// Renewing an existing lease must not stack a second pulser.
func TestTypingLeaseRenewalDoesNotStack(t *testing.T) {
	fake := &typingFake{}
	s := newTypingTestServer(fake, 10*time.Millisecond, time.Minute)

	for i := 0; i < 3; i++ {
		if resp := s.handleRequest(context.Background(), protocol.Request{
			Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1",
		}); !resp.OK {
			t.Fatalf("typing call %d failed: %s", i, resp.Error)
		}
	}

	s.mu.RLock()
	leases := len(s.typing)
	s.mu.RUnlock()
	if leases != 1 {
		t.Fatalf("leases = %d, want 1", leases)
	}

	// Three calls produce three immediate pulses only for the first; renewals
	// must not add immediate pulses of their own beyond the single cadence.
	time.Sleep(35 * time.Millisecond)
	if got := fake.count(); got > 6 {
		t.Fatalf("pulses = %d - renewals appear to have stacked pulsers", got)
	}

	// Cleanup: stop the lease so it does not outlive the test.
	s.handleRequest(context.Background(), protocol.Request{
		Action: protocol.ActionTyping, Service: "fake", Bot: "bot", Channel: "C1", Stop: true,
	})
}
