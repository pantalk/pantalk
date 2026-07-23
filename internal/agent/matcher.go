package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr/vm"
	"github.com/pantalk/pantalk/internal/protocol"
)

// Matcher evaluates one bot-to-agent binding's `when` expression. Bot
// selection and binding order are owned by the server router.
type Matcher struct {
	name    string
	when    string
	program *vm.Program
}

func NewMatcher(name string, when string) (*Matcher, error) {
	when = strings.TrimSpace(when)
	if when == "" {
		when = "notify"
	}

	program, err := compileWhen(when)
	if err != nil {
		return nil, fmt.Errorf("agent %q: invalid when expression: %w", name, err)
	}

	return &Matcher{
		name:    name,
		when:    when,
		program: program,
	}, nil
}

func (m *Matcher) Matches(event protocol.Event) bool {
	return m.MatchesAt(event, time.Now())
}

func (m *Matcher) MatchesAt(event protocol.Event, now time.Time) bool {
	return matchesAt(m.name, m.program, event, now)
}

func (m *Matcher) NeedsTick() bool {
	return needsTick(m.when)
}

func (m *Matcher) When() string {
	return m.when
}

// ValidateWhen compiles a routing expression without retaining a matcher.
func ValidateWhen(when string) error {
	_, err := NewMatcher("config", when)
	return err
}

// IsTimeExpression reports whether a binding must be evaluated on clock ticks.
func IsTimeExpression(when string) bool {
	return needsTick(when)
}
