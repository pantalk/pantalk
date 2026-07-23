package agent

import "github.com/pantalk/pantalk/internal/protocol"

// Runtime is the common lifecycle used by command and persistent agent
// drivers. Implementations decide whether they launch per trigger or maintain a
// long-running agent process, while the server retains one routing path.
type Runtime interface {
	Name() string
	When() string
	Matches(protocol.Event) bool
	Handle(protocol.Event)
	NeedsTick() bool
	Stop()
}

var _ Runtime = (*Runner)(nil)
