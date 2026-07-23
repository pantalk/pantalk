package upstream

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pantalk/pantalk/internal/protocol"
)

// InboundInjector is implemented by local/test connectors that accept
// synthetic inbound messages through pantalkd's Unix socket. Network-backed
// connectors deliberately do not implement it.
type InboundInjector interface {
	Inject(ctx context.Context, request protocol.Request) (protocol.Event, error)
}

// LocalConnector is an offline messaging surface for development and tests.
// Unlike MockConnector, outbound messages are never echoed back as inbound
// messages, which makes it safe to attach an autonomous agent.
type LocalConnector struct {
	bot      string
	identity string
	publish  func(protocol.Event)
}

func NewLocalConnector(bot string, publish func(protocol.Event)) *LocalConnector {
	return &LocalConnector{
		bot:      bot,
		identity: "local:" + bot,
		publish:  publish,
	}
}

func (l *LocalConnector) Run(ctx context.Context) {
	l.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   "local",
		Bot:       l.bot,
		Kind:      "status",
		Direction: "system",
		Text:      "connector online",
	})

	<-ctx.Done()

	l.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   "local",
		Bot:       l.bot,
		Kind:      "status",
		Direction: "system",
		Text:      "connector offline",
	})
}

func (l *LocalConnector) Identity() string {
	return l.identity
}

func (l *LocalConnector) Send(_ context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}
	if err := requireLocalDestination(request); err != nil {
		return protocol.Event{}, err
	}

	event := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   "local",
		Bot:       l.bot,
		Kind:      "message",
		Direction: "out",
		User:      l.identity,
		Target:    strings.TrimSpace(request.Target),
		Channel:   strings.TrimSpace(request.Channel),
		Thread:    strings.TrimSpace(request.Thread),
		Text:      text,
	}
	l.publish(event)

	return event, nil
}

// Inject publishes one explicit inbound event. It does not manufacture a
// response and therefore cannot form an echo loop with an attached agent.
func (l *LocalConnector) Inject(_ context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}
	if err := requireLocalDestination(request); err != nil {
		return protocol.Event{}, err
	}

	user := strings.TrimSpace(request.User)
	if request.Self {
		user = l.identity
	}
	if user == "" {
		return protocol.Event{}, fmt.Errorf("user cannot be empty")
	}

	event := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   "local",
		Bot:       l.bot,
		Kind:      "message",
		Direction: "in",
		User:      user,
		Target:    strings.TrimSpace(request.Target),
		Channel:   strings.TrimSpace(request.Channel),
		Thread:    strings.TrimSpace(request.Thread),
		Text:      text,
	}
	l.publish(event)

	return event, nil
}

func (l *LocalConnector) React(_ context.Context, _ protocol.Request) error {
	return fmt.Errorf("reactions are not supported by the local connector")
}

func requireLocalDestination(request protocol.Request) error {
	if strings.TrimSpace(request.Target) == "" &&
		strings.TrimSpace(request.Channel) == "" &&
		strings.TrimSpace(request.Thread) == "" {
		return fmt.Errorf("at least one of target, channel, or thread is required")
	}
	return nil
}
