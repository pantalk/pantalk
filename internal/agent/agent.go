// Package agent implements notification-triggered agent runners for pantalkd.
//
// When notifications arrive for a configured bot, the runner evaluates a
// "when" expression against the event. Matching events are buffered for a
// configurable window, then the runner exec's a preconfigured command. The
// command is never interpreted by a shell — it is exec'd directly from an
// argv slice. Unless the daemon is started with --allow-exec, only known
// agent binaries (claude, codex, aider, goose) are permitted.
//
// Time-based triggers are supported via at() and every() functions in the
// when expression. The server generates synthetic "tick" events every minute
// which flow through the same matching pipeline.
package agent

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/pantalk/pantalk/internal/protocol"
)

// AllowedCommands is the set of binaries that can be used without --allow-exec.
var AllowedCommands = map[string]bool{
	"claude":   true,
	"codex":    true,
	"copilot":  true,
	"aider":    true,
	"goose":    true,
	"opencode": true,
	"gemini":   true,
}

// Config describes a single agent definition from the YAML config.
type Config struct {
	Name     string  `yaml:"name"`
	When     string  `yaml:"when"`     // expr expression evaluated against each event
	Command  Command `yaml:"command"`  // argv — string or []string, exec'd directly
	Workdir  string  `yaml:"workdir"`  // optional working directory
	Buffer   int     `yaml:"buffer"`   // seconds to batch notifications (default 30)
	Timeout  int     `yaml:"timeout"`  // max runtime in seconds (default 120)
	Cooldown int     `yaml:"cooldown"` // min seconds between runs (default 60)
}

// exprEnv is the environment exposed to "when" expressions. Field names are
// lowercased automatically by expr-lang so they match the YAML examples
// (e.g. notify, direct, channel).
type exprEnv struct {
	// Event fields
	Notify   bool   `expr:"notify"`
	Direct   bool   `expr:"direct"`
	Mentions bool   `expr:"mentions"`
	Channel  string `expr:"channel"`
	Thread   string `expr:"thread"`
	Bot      string `expr:"bot"`
	Service  string `expr:"service"`
	User     string `expr:"user"`
	Text     string `expr:"text"`

	// Time fields — populated on tick events, zero on message events.
	Tick    bool   `expr:"tick"`
	Hour    int    `expr:"hour"`
	Minute  int    `expr:"minute"`
	Weekday string `expr:"weekday"` // "mon", "tue", "wed", "thu", "fri", "sat", "sun"

	// Time functions — set to closures that capture the env's time fields.
	// Exposed as at() and every() in expressions via expr tags.
	AtFn    func(times ...string) (bool, error) `expr:"at"`
	EveryFn func(interval string) (bool, error)  `expr:"every"`
}

// weekdayName converts a time.Weekday to a short lowercase name.
func weekdayName(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	case time.Sunday:
		return "sun"
	default:
		return ""
	}
}

// atFunc implements the at("HH:MM", ...) expression function. Returns true
// when the current hour:minute matches any of the given times. Only
// meaningful on tick events (returns false otherwise).
func atFunc(tick bool, hour, minute int, times ...string) (bool, error) {
	if !tick {
		return false, nil
	}
	current := fmt.Sprintf("%d:%02d", hour, minute)
	for _, t := range times {
		normalized, err := normalizeTime(t)
		if err != nil {
			return false, err
		}
		if current == normalized {
			return true, nil
		}
	}
	return false, nil
}

// normalizeTime normalizes a "HH:MM" or "H:MM" string to "H:MM" for
// consistent comparison.
func normalizeTime(s string) (string, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("at(): invalid time format %q, expected HH:MM", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return "", fmt.Errorf("at(): invalid hour in %q", s)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return "", fmt.Errorf("at(): invalid minute in %q", s)
	}
	return fmt.Sprintf("%d:%02d", h, m), nil
}

// everyFunc implements the every("Nm"/"Nh") expression function. Returns
// true on aligned intervals, e.g. every("15m") fires at :00, :15, :30, :45.
// Only meaningful on tick events (returns false otherwise).
func everyFunc(tick bool, hour, minute int, interval string) (bool, error) {
	if !tick {
		return false, nil
	}
	interval = strings.TrimSpace(interval)
	if interval == "" {
		return false, fmt.Errorf("every(): interval cannot be empty")
	}

	unit := interval[len(interval)-1]
	numStr := interval[:len(interval)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return false, fmt.Errorf("every(): invalid interval %q", interval)
	}

	switch unit {
	case 'm':
		// Aligned to start of day in minutes.
		minuteOfDay := hour*60 + minute
		return minuteOfDay%n == 0, nil
	case 'h':
		// Fires at the top of each aligned hour.
		return minute == 0 && hour%n == 0, nil
	default:
		return false, fmt.Errorf("every(): unknown unit %q, expected 'm' or 'h'", string(unit))
	}
}

// Runner manages the lifecycle of a single agent: matching, buffering, and
// launching. It is safe for concurrent use.
type Runner struct {
	cfg     Config
	program *vm.Program

	mu         sync.Mutex
	running    bool
	lastFinish time.Time
	pending    []protocol.Event
	timer      *time.Timer
}

// NewRunner creates a runner for the given agent config. Returns an error if
// the when expression is invalid or the command is empty.
func NewRunner(cfg Config) (*Runner, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("agent %q: command is required", cfg.Name)
	}

	if cfg.Buffer <= 0 {
		cfg.Buffer = 30
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 60
	}

	// Compile the when expression. Default to "notify" if omitted.
	whenExpr := cfg.When
	if strings.TrimSpace(whenExpr) == "" {
		whenExpr = "notify"
	}

	program, err := expr.Compile(whenExpr,
		expr.Env(exprEnv{}),
		expr.AsBool(),
	)
	if err != nil {
		return nil, fmt.Errorf("agent %q: invalid when expression: %w", cfg.Name, err)
	}

	return &Runner{
		cfg:     cfg,
		program: program,
	}, nil
}

// Matches evaluates the when expression against the event using the current
// time for tick events. See MatchesAt for testing with a specific time.
func (r *Runner) Matches(event protocol.Event) bool {
	return r.MatchesAt(event, time.Now())
}

// MatchesAt evaluates the when expression against the event using the given
// time for tick fields (hour, minute, weekday). This allows deterministic
// testing of time-based expressions.
func (r *Runner) MatchesAt(event protocol.Event, now time.Time) bool {
	isTick := event.Kind == "tick"
	isMessage := event.Kind == "message" && event.Direction == "in"

	// Accept inbound messages and tick events only.
	if !isTick && !isMessage {
		return false
	}

	// Don't react to our own messages (not applicable to ticks).
	if isMessage && event.Self {
		return false
	}

	env := exprEnv{
		Notify:   event.Notify,
		Direct:   event.Direct,
		Mentions: event.Mentions,
		Channel:  event.Channel,
		Thread:   event.Thread,
		Bot:      event.Bot,
		Service:  event.Service,
		User:     event.User,
		Text:     event.Text,
	}

	if isTick {
		env.Tick = true
		env.Hour = now.Hour()
		env.Minute = now.Minute()
		env.Weekday = weekdayName(now.Weekday())
	}

	// Set time function closures that capture the env's time fields.
	env.AtFn = func(times ...string) (bool, error) {
		return atFunc(env.Tick, env.Hour, env.Minute, times...)
	}
	env.EveryFn = func(interval string) (bool, error) {
		return everyFunc(env.Tick, env.Hour, env.Minute, interval)
	}

	result, err := expr.Run(r.program, env)
	if err != nil {
		log.Printf("[agent:%s] when expression error: %v", r.cfg.Name, err)
		return false
	}

	match, ok := result.(bool)
	return ok && match
}

// Handle accepts a matching event. Events are buffered for the configured
// window before the agent command is launched. If the agent is already running
// or in cooldown, events accumulate until the next eligible launch.
func (r *Runner) Handle(event protocol.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pending = append(r.pending, event)

	// If a timer is already ticking, let it fire — additional events just
	// accumulate in the pending buffer.
	if r.timer != nil {
		return
	}

	r.timer = time.AfterFunc(time.Duration(r.cfg.Buffer)*time.Second, r.flush)
}

// flush is called when the buffer timer fires. It drains the pending events
// and launches the agent if eligible.
func (r *Runner) flush() {
	r.mu.Lock()

	count := len(r.pending)
	r.pending = nil
	r.timer = nil

	if count == 0 {
		r.mu.Unlock()
		return
	}

	// Cooldown check: if the last run finished too recently, re-buffer.
	if !r.lastFinish.IsZero() {
		elapsed := time.Since(r.lastFinish)
		remaining := time.Duration(r.cfg.Cooldown)*time.Second - elapsed
		if remaining > 0 {
			r.timer = time.AfterFunc(remaining, r.flush)
			r.mu.Unlock()
			log.Printf("[agent:%s] in cooldown, retrying in %s", r.cfg.Name, remaining.Round(time.Second))
			return
		}
	}

	// Concurrency check: only one instance at a time.
	if r.running {
		r.timer = time.AfterFunc(5*time.Second, r.flush)
		r.mu.Unlock()
		log.Printf("[agent:%s] already running, will retry", r.cfg.Name)
		return
	}

	r.running = true
	r.mu.Unlock()

	go r.run(count)
}

// run executes the agent command. The command is responsible for reading
// notifications via the pantalk CLI — no events are passed on stdin.
func (r *Runner) run(triggerCount int) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.lastFinish = time.Now()

		// If more events arrived while we were running, schedule a flush.
		if len(r.pending) > 0 && r.timer == nil {
			r.timer = time.AfterFunc(time.Duration(r.cfg.Buffer)*time.Second, r.flush)
		}
		r.mu.Unlock()
	}()

	log.Printf("[agent:%s] launching (%d notification(s) triggered)", r.cfg.Name, triggerCount)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.cfg.Timeout)*time.Second)
	defer cancel()

	// Direct exec — no shell interpretation.
	cmd := exec.CommandContext(ctx, r.cfg.Command[0], r.cfg.Command[1:]...)

	if r.cfg.Workdir != "" {
		cmd.Dir = r.cfg.Workdir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[agent:%s] command failed: %v", r.cfg.Name, err)
		if len(output) > 0 {
			log.Printf("[agent:%s] output: %s", r.cfg.Name, truncate(string(output), 500))
		}
		return
	}

	log.Printf("[agent:%s] completed successfully", r.cfg.Name)
	if len(output) > 0 {
		log.Printf("[agent:%s] output: %s", r.cfg.Name, truncate(strings.TrimSpace(string(output)), 500))
	}
}

// NeedsTick reports whether this runner's when expression uses time-based
// functions (at, every, tick, hour, minute, weekday). If no runners need
// ticks, the server can skip the 1-minute ticker entirely.
func (r *Runner) NeedsTick() bool {
	w := strings.TrimSpace(r.cfg.When)
	if w == "" {
		w = "notify"
	}
	return strings.Contains(w, "at(") ||
		strings.Contains(w, "every(") ||
		strings.Contains(w, "tick") ||
		strings.Contains(w, "hour") ||
		strings.Contains(w, "minute") ||
		strings.Contains(w, "weekday")
}

// TickEvent returns a synthetic event that represents a clock tick.
func TickEvent() protocol.Event {
	return protocol.Event{
		Kind:      "tick",
		Timestamp: time.Now().UTC(),
	}
}

// Stop cancels any pending buffer timer. Should be called during shutdown.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
