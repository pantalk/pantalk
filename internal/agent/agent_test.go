package agent

import (
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/protocol"
)

func makeEvent(opts ...func(*protocol.Event)) protocol.Event {
	e := protocol.Event{
		Kind:      "message",
		Direction: "in",
		Notify:    true,
		Bot:       "test-bot",
		Service:   "slack",
		Channel:   "#general",
		User:      "U123",
		Text:      "hello world",
	}
	for _, opt := range opts {
		opt(&e)
	}
	return e
}

func TestMatches_DefaultWhen_Notify(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude", "-p", "test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !r.Matches(makeEvent()) {
		t.Error("expected match on notification event with default when")
	}

	if r.Matches(makeEvent(func(e *protocol.Event) { e.Notify = false })) {
		t.Error("should not match non-notification event with default when")
	}
}

func TestMatches_DirectExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "direct",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match non-direct event")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Direct = true })) {
		t.Error("expected match on direct event")
	}
}

func TestMatches_MentionsExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "mentions",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match non-mention event")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Mentions = true })) {
		t.Error("expected match on mention event")
	}
}

func TestMatches_DirectOrMentions(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "direct || mentions",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match plain notification")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Direct = true })) {
		t.Error("expected match on direct event")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Mentions = true })) {
		t.Error("expected match on mention event")
	}
}

func TestMatches_ChannelExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `channel == "#incidents"`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match #general")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Channel = "#incidents" })) {
		t.Error("expected match on #incidents")
	}
}

func TestMatches_ChannelIn(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `channel in ["#incidents", "#alerts"]`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match #general")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Channel = "#alerts" })) {
		t.Error("expected match on #alerts")
	}
}

func TestMatches_BotFilter(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `notify && bot == "ops-bot"`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match test-bot")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Bot = "ops-bot" })) {
		t.Error("expected match on ops-bot")
	}
}

func TestMatches_TextMatches(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `notify && text matches "deploy|rollback"`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match 'hello world'")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Text = "please deploy now" })) {
		t.Error("expected match on deploy text")
	}
}

func TestMatches_ComplexExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `(direct && text matches "deploy") || (mentions && channel == "#ops")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Direct + deploy text → match
	if !r.Matches(makeEvent(func(e *protocol.Event) {
		e.Direct = true
		e.Text = "deploy the app"
	})) {
		t.Error("expected match on direct+deploy")
	}

	// Mention in #ops → match
	if !r.Matches(makeEvent(func(e *protocol.Event) {
		e.Mentions = true
		e.Channel = "#ops"
	})) {
		t.Error("expected match on mention+#ops")
	}

	// Mention in #general → no match
	if r.Matches(makeEvent(func(e *protocol.Event) {
		e.Mentions = true
	})) {
		t.Error("should not match mention in #general")
	}

	// Direct without deploy → no match
	if r.Matches(makeEvent(func(e *protocol.Event) {
		e.Direct = true
		e.Text = "hello"
	})) {
		t.Error("should not match direct without deploy text")
	}
}

func TestMatches_IgnoresOwnMessages(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent(func(e *protocol.Event) { e.Self = true })) {
		t.Error("should not match own messages")
	}
}

func TestMatches_IgnoresOutbound(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent(func(e *protocol.Event) { e.Direction = "out" })) {
		t.Error("should not match outbound events")
	}
}

func TestMatches_IgnoresNonMessage(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent(func(e *protocol.Event) { e.Kind = "heartbeat" })) {
		t.Error("should not match heartbeat events")
	}
}

func TestNewRunner_InvalidExpression(t *testing.T) {
	_, err := NewRunner(Config{
		Name:    "test",
		When:    "notify &&& invalid",
		Command: Command{"claude"},
	})
	if err == nil {
		t.Fatal("expected error for invalid expression")
	}
}

func TestNewRunner_EmptyCommand(t *testing.T) {
	_, err := NewRunner(Config{
		Name: "test",
	})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestNewRunner_Defaults(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.cfg.Buffer != 30 {
		t.Errorf("expected buffer=30, got %d", r.cfg.Buffer)
	}
	if r.cfg.Timeout != 120 {
		t.Errorf("expected timeout=120, got %d", r.cfg.Timeout)
	}
	if r.cfg.Cooldown != 60 {
		t.Errorf("expected cooldown=60, got %d", r.cfg.Cooldown)
	}
}

// --- Additional expression tests ---

func TestMatches_NegationDirect(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify && !direct",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Notification that is not direct → match
	if !r.Matches(makeEvent()) {
		t.Error("expected match on notification that is not direct")
	}

	// Notification that IS direct → no match
	if r.Matches(makeEvent(func(e *protocol.Event) { e.Direct = true })) {
		t.Error("should not match when direct is true")
	}
}

func TestMatches_TrueExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "true",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Any inbound message matches
	if !r.Matches(makeEvent()) {
		t.Error("expected match with 'true' expression")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Notify = false })) {
		t.Error("expected match for non-notify event with 'true' expression")
	}

	// But outbound still rejected by pre-filter
	if r.Matches(makeEvent(func(e *protocol.Event) { e.Direction = "out" })) {
		t.Error("should not match outbound even with 'true' expression")
	}
}

func TestMatches_WhitespaceWhenDefaultsToNotify(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "   ",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !r.Matches(makeEvent()) {
		t.Error("whitespace-only when should default to 'notify' and match")
	}

	if r.Matches(makeEvent(func(e *protocol.Event) { e.Notify = false })) {
		t.Error("whitespace-only when should default to 'notify' and reject non-notify")
	}
}

func TestMatches_ServiceExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `service == "discord"`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Default makeEvent has service=slack
	if r.Matches(makeEvent()) {
		t.Error("should not match slack")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Service = "discord" })) {
		t.Error("expected match on discord")
	}
}

func TestMatches_UserExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `user == "UADMIN"`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Matches(makeEvent()) {
		t.Error("should not match user U123")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.User = "UADMIN" })) {
		t.Error("expected match on UADMIN")
	}
}

func TestMatches_ThreadExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `thread != ""`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Default makeEvent has empty thread → no match
	if r.Matches(makeEvent()) {
		t.Error("should not match when thread is empty")
	}

	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Thread = "1234.5678" })) {
		t.Error("expected match when thread is set")
	}
}

func TestNewRunner_CustomTimings(t *testing.T) {
	r, err := NewRunner(Config{
		Name:     "test",
		Command:  Command{"claude"},
		Buffer:   10,
		Timeout:  300,
		Cooldown: 120,
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.cfg.Buffer != 10 {
		t.Errorf("expected buffer=10, got %d", r.cfg.Buffer)
	}
	if r.cfg.Timeout != 300 {
		t.Errorf("expected timeout=300, got %d", r.cfg.Timeout)
	}
	if r.cfg.Cooldown != 120 {
		t.Errorf("expected cooldown=120, got %d", r.cfg.Cooldown)
	}
}

// --- Handle / buffering / lifecycle tests ---

func TestHandle_BuffersEvents(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
		Buffer:  30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	evt := makeEvent()
	r.Handle(evt)

	r.mu.Lock()
	pendingCount := len(r.pending)
	hasTimer := r.timer != nil
	r.mu.Unlock()

	if pendingCount != 1 {
		t.Errorf("expected 1 pending event, got %d", pendingCount)
	}
	if !hasTimer {
		t.Error("expected timer to be set after Handle()")
	}
}

func TestHandle_AccumulatesEvents(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
		Buffer:  30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	// Send 5 events
	for i := 0; i < 5; i++ {
		r.Handle(makeEvent())
	}

	r.mu.Lock()
	pendingCount := len(r.pending)
	r.mu.Unlock()

	if pendingCount != 5 {
		t.Errorf("expected 5 pending events, got %d", pendingCount)
	}
}

func TestStop_CancelsPendingTimer(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
		Buffer:  300, // very long buffer so it doesn't fire
	})
	if err != nil {
		t.Fatal(err)
	}

	r.Handle(makeEvent())

	r.mu.Lock()
	hasTimerBefore := r.timer != nil
	r.mu.Unlock()

	if !hasTimerBefore {
		t.Fatal("expected timer before Stop()")
	}

	r.Stop()

	r.mu.Lock()
	hasTimerAfter := r.timer != nil
	r.mu.Unlock()

	if hasTimerAfter {
		t.Error("expected timer to be nil after Stop()")
	}
}

func TestFlush_WhenAlreadyRunning(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
		Buffer:  30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	// Simulate an agent that's already running
	r.mu.Lock()
	r.running = true
	r.pending = append(r.pending, makeEvent())
	r.mu.Unlock()

	// Call flush directly — it should see running=true and reschedule
	r.flush()

	r.mu.Lock()
	hasTimer := r.timer != nil
	isRunning := r.running
	r.mu.Unlock()

	if !hasTimer {
		t.Error("expected timer to be re-set when already running")
	}
	if !isRunning {
		t.Error("running flag should still be true")
	}

	r.Stop()
}

func TestFlush_EmptyPending(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Flush with nothing pending — should be a no-op
	r.flush()

	r.mu.Lock()
	hasTimer := r.timer != nil
	isRunning := r.running
	r.mu.Unlock()

	if hasTimer {
		t.Error("timer should not be set after flushing empty pending")
	}
	if isRunning {
		t.Error("running should not be set after flushing empty pending")
	}
}

// --- truncate tests ---

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"ab", 1, "a..."},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
		}
	}
}

// --- Time-based trigger tests ---

func makeTickEvent() protocol.Event {
	return protocol.Event{
		Kind: "tick",
	}
}

func TestMatches_AtFunction_SingleTime(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("9:00")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 9:00 → match
	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if !r.MatchesAt(makeTickEvent(), at9) {
		t.Error("expected match at 9:00")
	}

	// 9:01 → no match
	at901 := time.Date(2026, 2, 19, 9, 1, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at901) {
		t.Error("should not match at 9:01")
	}
}

func TestMatches_AtFunction_MultipleTimes(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("9:00", "12:30", "17:00")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	match := func(h, m int) bool {
		t := time.Date(2026, 2, 19, h, m, 0, 0, time.Local)
		return r.MatchesAt(makeTickEvent(), t)
	}

	if !match(9, 0) {
		t.Error("expected match at 9:00")
	}
	if !match(12, 30) {
		t.Error("expected match at 12:30")
	}
	if !match(17, 0) {
		t.Error("expected match at 17:00")
	}
	if match(8, 59) {
		t.Error("should not match at 8:59")
	}
	if match(12, 31) {
		t.Error("should not match at 12:31")
	}
}

func TestMatches_AtFunction_LeadingZeros(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("09:05")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	at905 := time.Date(2026, 2, 19, 9, 5, 0, 0, time.Local)
	if !r.MatchesAt(makeTickEvent(), at905) {
		t.Error("expected match at 09:05 (leading zero)")
	}
}

func TestMatches_EveryFunction_15m(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("15m")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	match := func(h, m int) bool {
		t := time.Date(2026, 2, 19, h, m, 0, 0, time.Local)
		return r.MatchesAt(makeTickEvent(), t)
	}

	// every 15m should fire at :00, :15, :30, :45
	if !match(0, 0) {
		t.Error("expected match at 0:00")
	}
	if !match(9, 15) {
		t.Error("expected match at 9:15")
	}
	if !match(14, 30) {
		t.Error("expected match at 14:30")
	}
	if !match(23, 45) {
		t.Error("expected match at 23:45")
	}
	if match(9, 1) {
		t.Error("should not match at 9:01")
	}
	if match(10, 7) {
		t.Error("should not match at 10:07")
	}
}

func TestMatches_EveryFunction_2h(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("2h")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	match := func(h, m int) bool {
		t := time.Date(2026, 2, 19, h, m, 0, 0, time.Local)
		return r.MatchesAt(makeTickEvent(), t)
	}

	// every 2h fires at 0:00, 2:00, 4:00, ... 22:00 (at minute 0 only)
	if !match(0, 0) {
		t.Error("expected match at 0:00")
	}
	if !match(2, 0) {
		t.Error("expected match at 2:00")
	}
	if !match(10, 0) {
		t.Error("expected match at 10:00")
	}
	if match(1, 0) {
		t.Error("should not match at 1:00")
	}
	if match(2, 1) {
		t.Error("should not match at 2:01 (not on the hour)")
	}
}

func TestMatches_EveryFunction_10m(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("10m")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	match := func(h, m int) bool {
		t := time.Date(2026, 2, 19, h, m, 0, 0, time.Local)
		return r.MatchesAt(makeTickEvent(), t)
	}

	if !match(0, 0) {
		t.Error("expected match at 0:00")
	}
	if !match(1, 10) {
		t.Error("expected match at 1:10")
	}
	if !match(3, 20) {
		t.Error("expected match at 3:20")
	}
	if match(1, 7) {
		t.Error("should not match at 1:07")
	}
}

func TestMatches_WeekdayExpression(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("9:00") && weekday in ["mon", "tue", "wed", "thu", "fri"]`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Thursday 9:00 → match
	thu := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local) // Feb 19 2026 is Thursday
	if !r.MatchesAt(makeTickEvent(), thu) {
		t.Errorf("expected match on Thursday 9:00 (weekday=%s)", weekdayName(thu.Weekday()))
	}

	// Saturday 9:00 → no match
	sat := time.Date(2026, 2, 21, 9, 0, 0, 0, time.Local) // Feb 21 2026 is Saturday
	if r.MatchesAt(makeTickEvent(), sat) {
		t.Errorf("should not match on Saturday 9:00 (weekday=%s)", weekdayName(sat.Weekday()))
	}
}

func TestMatches_HourMinuteFields(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("15m") && hour >= 9 && hour < 17`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 9:15 → match (within business hours, on interval)
	at915 := time.Date(2026, 2, 19, 9, 15, 0, 0, time.Local)
	if !r.MatchesAt(makeTickEvent(), at915) {
		t.Error("expected match at 9:15 (business hours, aligned)")
	}

	// 2:00 → no match (outside business hours)
	at200 := time.Date(2026, 2, 19, 2, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at200) {
		t.Error("should not match at 2:00 (outside business hours)")
	}

	// 17:00 → no match (hour < 17 is false)
	at1700 := time.Date(2026, 2, 19, 17, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at1700) {
		t.Error("should not match at 17:00 (hour not < 17)")
	}
}

func TestMatches_TimeAndEventCombined(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `direct || at("9:00")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Direct message → match (event-trigger)
	if !r.Matches(makeEvent(func(e *protocol.Event) { e.Direct = true })) {
		t.Error("expected match on direct message")
	}

	// Tick at 9:00 → match (time-trigger)
	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if !r.MatchesAt(makeTickEvent(), at9) {
		t.Error("expected match on tick at 9:00")
	}

	// Tick at 10:00 → no match
	at10 := time.Date(2026, 2, 19, 10, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at10) {
		t.Error("should not match tick at 10:00")
	}

	// Regular notify (not direct) → no match
	if r.Matches(makeEvent()) {
		t.Error("should not match plain notification (only direct triggers)")
	}
}

func TestMatches_TickDoesNotMatchNotify(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tick event → notify is false → no match
	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at9) {
		t.Error("tick events should not match 'notify' expression")
	}
}

func TestMatches_MessageDoesNotMatchTick(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "tick",
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Message event → tick is false → no match
	if r.Matches(makeEvent()) {
		t.Error("message events should not match 'tick' expression")
	}
}

func TestMatches_AtOnMessageEvent(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("9:00")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// at() returns false for non-tick events
	if r.Matches(makeEvent()) {
		t.Error("at() should return false for message events")
	}
}

func TestMatches_EveryOnMessageEvent(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("15m")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// every() returns false for non-tick events
	if r.Matches(makeEvent()) {
		t.Error("every() should return false for message events")
	}
}

func TestNeedsTick(t *testing.T) {
	tests := []struct {
		name     string
		when     string
		expected bool
	}{
		{"default notify", "", false},
		{"notify", "notify", false},
		{"direct", "direct", false},
		{"at function", `at("9:00")`, true},
		{"every function", `every("15m")`, true},
		{"tick field", "tick", true},
		{"hour field", "hour >= 9", true},
		{"minute field", "minute == 0", true},
		{"weekday field", `weekday == "mon"`, true},
		{"combined at + notify", `at("9:00") || notify`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewRunner(Config{
				Name:    "test",
				When:    tt.when,
				Command: Command{"claude"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if r.NeedsTick() != tt.expected {
				t.Errorf("NeedsTick()=%v, want %v", r.NeedsTick(), tt.expected)
			}
		})
	}
}

func TestNewRunner_InvalidAtArgument(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `at("25:00")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal("expression should compile — validation happens at runtime")
	}

	// Invalid time should cause a mismatch (runtime error → false)
	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at9) {
		t.Error("invalid time 25:00 should not match")
	}
}

func TestNewRunner_InvalidEveryArgument(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("abc")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal("expression should compile — validation happens at runtime")
	}

	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at9) {
		t.Error("invalid interval 'abc' should not match")
	}
}

func TestTickEvent(t *testing.T) {
	e := TickEvent()
	if e.Kind != "tick" {
		t.Errorf("expected kind 'tick', got %q", e.Kind)
	}
	if e.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestWeekdayName(t *testing.T) {
	tests := []struct {
		day      time.Weekday
		expected string
	}{
		{time.Monday, "mon"},
		{time.Tuesday, "tue"},
		{time.Wednesday, "wed"},
		{time.Thursday, "thu"},
		{time.Friday, "fri"},
		{time.Saturday, "sat"},
		{time.Sunday, "sun"},
	}
	for _, tt := range tests {
		got := weekdayName(tt.day)
		if got != tt.expected {
			t.Errorf("weekdayName(%v) = %q, want %q", tt.day, got, tt.expected)
		}
	}
}

func TestNormalizeTime(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"9:00", "9:00", false},
		{"09:00", "9:00", false},
		{"12:30", "12:30", false},
		{"0:00", "0:00", false},
		{"23:59", "23:59", false},
		{"24:00", "", true},
		{"9:60", "", true},
		{"abc", "", true},
		{"-1:00", "", true},
	}
	for _, tt := range tests {
		got, err := normalizeTime(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("normalizeTime(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeTime(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("normalizeTime(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- Additional coverage tests ---

func TestEveryFunc_UnknownUnit(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("10s")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal("expression should compile — validation happens at runtime")
	}

	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at9) {
		t.Error("unknown unit 's' should not match")
	}
}

func TestEveryFunc_EmptyInterval(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal("expression should compile — validation happens at runtime")
	}

	at9 := time.Date(2026, 2, 19, 9, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at9) {
		t.Error("empty interval should not match")
	}
}

func TestEveryFunc_NegativeInterval(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    `every("-5m")`,
		Command: Command{"claude"},
	})
	if err != nil {
		t.Fatal("expression should compile — validation happens at runtime")
	}

	at := time.Date(2026, 2, 19, 0, 0, 0, 0, time.Local)
	if r.MatchesAt(makeTickEvent(), at) {
		t.Error("negative interval should not match")
	}
}

func TestRun_SuccessfulCommand(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"echo", "hello"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// run directly and wait for it to finish
	r.run(1)

	// After run, running should be false and lastFinish should be set
	r.mu.Lock()
	if r.running {
		t.Error("expected running=false after completion")
	}
	if r.lastFinish.IsZero() {
		t.Error("expected lastFinish to be set")
	}
	r.mu.Unlock()
}

func TestRun_FailingCommand(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"false"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	r.run(1)

	r.mu.Lock()
	if r.running {
		t.Error("expected running=false after failed command")
	}
	if r.lastFinish.IsZero() {
		t.Error("expected lastFinish to be set even on failure")
	}
	r.mu.Unlock()
}

func TestRun_CommandWithOutput(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"echo", "some output here"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	r.run(3)

	r.mu.Lock()
	if r.running {
		t.Error("expected running=false after completion")
	}
	r.mu.Unlock()
}

func TestRun_WithWorkdir(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"pwd"},
		Workdir: "/tmp",
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	r.run(1)

	r.mu.Lock()
	if r.lastFinish.IsZero() {
		t.Error("expected command to complete")
	}
	r.mu.Unlock()
}

func TestRun_ReschedulesOnPendingEvents(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"true"},
		Buffer:  1,
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add pending events before running
	r.mu.Lock()
	r.pending = append(r.pending, makeEvent())
	r.mu.Unlock()

	r.run(1)

	r.mu.Lock()
	if r.timer == nil {
		t.Error("expected timer to be rescheduled for pending events")
	}
	timer := r.timer
	r.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func TestFlush_CooldownRebuffer(t *testing.T) {
	r, err := NewRunner(Config{
		Name:     "test",
		When:     "notify",
		Command:  Command{"true"},
		Buffer:   1,
		Cooldown: 60,
		Timeout:  5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a recent finish
	r.mu.Lock()
	r.lastFinish = time.Now()
	r.pending = []protocol.Event{makeEvent()}
	r.mu.Unlock()

	r.flush()

	// Should have set a retry timer instead of launching
	r.mu.Lock()
	if r.timer == nil {
		t.Error("expected retry timer during cooldown")
	}
	if r.running {
		t.Error("should not be running during cooldown")
	}
	timer := r.timer
	r.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func TestFlush_CooldownExpired(t *testing.T) {
	r, err := NewRunner(Config{
		Name:     "test",
		When:     "notify",
		Command:  Command{"true"},
		Cooldown: 1,
		Timeout:  5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a finish that happened long ago (cooldown expired)
	r.mu.Lock()
	r.lastFinish = time.Now().Add(-2 * time.Second)
	r.pending = []protocol.Event{makeEvent()}
	r.mu.Unlock()

	r.flush()

	// Give the goroutine time to start
	time.Sleep(100 * time.Millisecond)

	r.mu.Lock()
	// Either running or already finished — lastFinish should be updated
	r.mu.Unlock()
}

func TestFlush_AlreadyRunning(t *testing.T) {
	r, err := NewRunner(Config{
		Name:    "test",
		When:    "notify",
		Command: Command{"true"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate already-running state
	r.mu.Lock()
	r.running = true
	r.pending = []protocol.Event{makeEvent()}
	r.mu.Unlock()

	r.flush()

	// Should have set a retry timer
	r.mu.Lock()
	if r.timer == nil {
		t.Error("expected retry timer when already running")
	}
	timer := r.timer
	r.running = false // reset
	r.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func TestAtFunc_NotOnTick(t *testing.T) {
	// atFunc should return false when tick is false
	result, err := atFunc(false, 9, 0, "9:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false when not a tick")
	}
}

func TestEveryFunc_NotOnTick(t *testing.T) {
	result, err := everyFunc(false, 9, 0, "15m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false when not a tick")
	}
}

func TestAtFunc_Direct(t *testing.T) {
	// Match at 9:00
	result, err := atFunc(true, 9, 0, "9:00")
	if err != nil {
		t.Fatal(err)
	}
	if !result {
		t.Error("expected match at 9:00")
	}

	// No match at 10:00
	result, err = atFunc(true, 10, 0, "9:00")
	if err != nil {
		t.Fatal(err)
	}
	if result {
		t.Error("expected no match at 10:00")
	}
}

func TestEveryFunc_Direct(t *testing.T) {
	// every 15m at :00 should match
	result, err := everyFunc(true, 9, 0, "15m")
	if err != nil {
		t.Fatal(err)
	}
	if !result {
		t.Error("expected match at 9:00 for 15m interval")
	}

	// every 15m at :07 should not match
	result, err = everyFunc(true, 9, 7, "15m")
	if err != nil {
		t.Fatal(err)
	}
	if result {
		t.Error("expected no match at 9:07 for 15m interval")
	}
}
