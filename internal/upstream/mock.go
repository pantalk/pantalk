package upstream

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pantalk/pantalk/internal/protocol"
)

type MockConnector struct {
	service string
	bot     string
	publish func(protocol.Event)
}

func NewMockConnector(service string, bot string, publish func(protocol.Event)) *MockConnector {
	return &MockConnector{
		service: service,
		bot:     bot,
		publish: publish,
	}
}

func (m *MockConnector) Run(ctx context.Context) {
	connected := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.service,
		Bot:       m.bot,
		Kind:      "status",
		Direction: "system",
		Text:      "connector online",
	}
	m.publish(connected)

	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			disconnected := protocol.Event{
				Timestamp: time.Now().UTC(),
				Service:   m.service,
				Bot:       m.bot,
				Kind:      "status",
				Direction: "system",
				Text:      "connector offline",
			}
			m.publish(disconnected)
			return
		case <-ticker.C:
			heartbeat := protocol.Event{
				Timestamp: time.Now().UTC(),
				Service:   m.service,
				Bot:       m.bot,
				Kind:      "heartbeat",
				Direction: "system",
				Text:      "upstream session alive",
			}
			m.publish(heartbeat)
		}
	}
}

func (m *MockConnector) Identity() string {
	return ""
}

func (m *MockConnector) Send(_ context.Context, request protocol.Request) (protocol.Event, error) {
	trimmed := strings.TrimSpace(request.Text)
	if trimmed == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	target := request.Target
	if target == "" && request.Channel != "" {
		target = "channel:" + request.Channel
	}

	outbound := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   m.service,
		Bot:       m.bot,
		Kind:      "message",
		Direction: "out",
		Target:    target,
		Channel:   request.Channel,
		Thread:    request.Thread,
		Text:      trimmed,
	}
	m.publish(outbound)

	go func() {
		time.Sleep(300 * time.Millisecond)
		echo := protocol.Event{
			Timestamp: time.Now().UTC(),
			Service:   m.service,
			Bot:       m.bot,
			Kind:      "message",
			Direction: "in",
			Target:    target,
			Channel:   request.Channel,
			Thread:    request.Thread,
			Text:      "echo: " + trimmed,
		}
		m.publish(echo)
	}()

	return outbound, nil
}
