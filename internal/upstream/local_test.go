package upstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

func TestLocalConnectorSendPublishesOutboundWithoutEcho(t *testing.T) {
	var mu sync.Mutex
	var published []protocol.Event
	connector := NewLocalConnector("test-bot", func(event protocol.Event) {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
	})

	event, err := connector.Send(context.Background(), protocol.Request{
		Target:  "channel:dev",
		Channel: "dev",
		Thread:  "thread-1",
		Text:    " hello ",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if event.Direction != "out" || event.User != "local:test-bot" {
		t.Fatalf("unexpected outbound event: %+v", event)
	}
	if event.Target != "channel:dev" || event.Channel != "dev" || event.Thread != "thread-1" {
		t.Fatalf("destination fields were not preserved: %+v", event)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected exactly one outbound event and no echo, got %d: %+v", len(published), published)
	}
}

func TestLocalConnectorInjectPreservesInboundFields(t *testing.T) {
	var published protocol.Event
	connector := NewLocalConnector("test-bot", func(event protocol.Event) {
		published = event
	})

	event, err := connector.Inject(context.Background(), protocol.Request{
		User:    "alice",
		Target:  "channel:dev",
		Channel: "dev",
		Thread:  "thread-1",
		Text:    " question ",
	})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if event.Direction != "in" || event.User != "alice" || event.Text != "question" {
		t.Fatalf("unexpected inbound event: %+v", event)
	}
	if event.User != published.User || event.Target != published.Target ||
		event.Channel != published.Channel || event.Thread != published.Thread ||
		event.Text != published.Text || event.Direction != published.Direction {
		t.Fatalf("returned and published events differ:\nreturned:  %+v\npublished: %+v", event, published)
	}
}

func TestLocalConnectorInjectAsSelfUsesConnectorIdentity(t *testing.T) {
	connector := NewLocalConnector("test-bot", func(protocol.Event) {})

	event, err := connector.Inject(context.Background(), protocol.Request{
		Self:   true,
		Target: "user:alice",
		Text:   "ignore me",
	})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if event.User != connector.Identity() {
		t.Fatalf("user = %q, want identity %q", event.User, connector.Identity())
	}
}

func TestLocalConnectorRejectsInvalidInjection(t *testing.T) {
	connector := NewLocalConnector("test-bot", func(protocol.Event) {})

	if _, err := connector.Inject(context.Background(), protocol.Request{
		User:   "alice",
		Target: "user:alice",
		Text:   " ",
	}); err == nil {
		t.Fatal("expected empty text error")
	}
	if _, err := connector.Inject(context.Background(), protocol.Request{
		User: "alice",
		Text: "hello",
	}); err == nil {
		t.Fatal("expected missing destination error")
	}
	if _, err := connector.Inject(context.Background(), protocol.Request{
		Target: "user:alice",
		Text:   "hello",
	}); err == nil {
		t.Fatal("expected missing user error")
	}
}

func TestNewConnectorBuildsLocalConnector(t *testing.T) {
	connector, err := NewConnector(config.BotConfig{
		Name: "test-bot",
		Type: "local",
	}, func(protocol.Event) {}, nil)
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}

	local, ok := connector.(*LocalConnector)
	if !ok {
		t.Fatalf("connector type = %T, want *LocalConnector", connector)
	}
	if local.Identity() != "local:test-bot" {
		t.Fatalf("identity = %q", local.Identity())
	}
}
