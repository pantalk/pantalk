package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr/vm"
	"github.com/pantalk/pantalk/internal/protocol"
)

// Matcher applies an agent's bot selection and optional advanced `when`
// expression. Persistent drivers use the same routing semantics as the legacy
// command runner without inheriting its process-launch lifecycle.
type Matcher struct {
	name    string
	when    string
	bots    map[string]struct{}
	program *vm.Program
}

func NewMatcher(name string, when string, bots []string) (*Matcher, error) {
	when = strings.TrimSpace(when)
	if when == "" {
		when = "notify"
	}

	program, err := compileWhen(when)
	if err != nil {
		return nil, fmt.Errorf("agent %q: invalid when expression: %w", name, err)
	}

	selectedBots := make(map[string]struct{}, len(bots))
	for _, bot := range bots {
		if bot = strings.TrimSpace(bot); bot != "" {
			selectedBots[bot] = struct{}{}
		}
	}

	return &Matcher{
		name:    name,
		when:    when,
		bots:    selectedBots,
		program: program,
	}, nil
}

func (m *Matcher) Matches(event protocol.Event) bool {
	return m.MatchesAt(event, time.Now())
}

func (m *Matcher) MatchesAt(event protocol.Event, now time.Time) bool {
	if len(m.bots) > 0 {
		if _, selected := m.bots[event.Bot]; !selected {
			return false
		}
	}
	return matchesAt(m.name, m.program, event, now)
}

func (m *Matcher) NeedsTick() bool {
	return needsTick(m.when)
}

func (m *Matcher) When() string {
	return m.when
}
