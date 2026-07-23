package agent

import "github.com/pantalk/pantalk/internal/protocol"

// Runtime is the common lifecycle used by command and persistent agent
// drivers. Implementations decide whether they launch per trigger or maintain a
// long-running agent process, while the server retains one routing path.
type Runtime interface {
	Name() string
	Handle(protocol.Event)
	Stop()
}

var _ Runtime = (*Runner)(nil)
