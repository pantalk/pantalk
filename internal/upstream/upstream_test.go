package upstream

import (
	"testing"

	"github.com/chatbotkit/pantalk/internal/protocol"
)

func TestResolveSlackChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "C1234"}, "C1234"},
		{"target with channel prefix", protocol.Request{Target: "channel:C5678"}, "C5678"},
		{"target with slack prefix", protocol.Request{Target: "slack:channel:C9999"}, "C9999"},
		{"bare target", protocol.Request{Target: "C1111"}, "C1111"},
		{"channel takes precedence", protocol.Request{Channel: "C1", Target: "C2"}, "C1"},
		{"empty", protocol.Request{}, ""},
		{"whitespace target", protocol.Request{Target: "  "}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSlackChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveSlackChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveDiscordChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "123456"}, "123456"},
		{"target with channel prefix", protocol.Request{Target: "channel:789"}, "789"},
		{"target with discord prefix", protocol.Request{Target: "discord:channel:999"}, "999"},
		{"bare target", protocol.Request{Target: "555"}, "555"},
		{"channel takes precedence", protocol.Request{Channel: "111", Target: "222"}, "111"},
		{"empty", protocol.Request{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDiscordChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveDiscordChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveMattermostChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "abc123"}, "abc123"},
		{"target with channel prefix", protocol.Request{Target: "channel:def456"}, "def456"},
		{"target with mm prefix", protocol.Request{Target: "mattermost:channel:ghi"}, "ghi"},
		{"bare target", protocol.Request{Target: "xyz"}, "xyz"},
		{"channel takes precedence", protocol.Request{Channel: "aaa", Target: "bbb"}, "aaa"},
		{"empty", protocol.Request{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMattermostChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveMattermostChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTelegramChat(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "-1001234"}, "-1001234"},
		{"target with chat prefix", protocol.Request{Target: "chat:999"}, "999"},
		{"target with telegram chat prefix", protocol.Request{Target: "telegram:chat:888"}, "888"},
		{"target with channel prefix", protocol.Request{Target: "channel:777"}, "777"},
		{"target with telegram channel prefix", protocol.Request{Target: "telegram:channel:666"}, "666"},
		{"bare target", protocol.Request{Target: "555"}, "555"},
		{"channel takes precedence", protocol.Request{Channel: "111", Target: "222"}, "111"},
		{"empty", protocol.Request{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTelegramChat(tt.request)
			if got != tt.want {
				t.Errorf("resolveTelegramChat() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSlackTimestamp(t *testing.T) {
	ts := parseSlackTimestamp("1711234567.000100")
	if ts.Unix() != 1711234567 {
		t.Errorf("expected unix 1711234567, got %d", ts.Unix())
	}
	if ts.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestParseSlackTimestamp_Invalid(t *testing.T) {
	ts := parseSlackTimestamp("not-a-number")
	// should return current time (non-zero) rather than error
	if ts.IsZero() {
		t.Error("expected fallback timestamp, got zero")
	}
}

func TestMockConnector_Send(t *testing.T) {
	var published []protocol.Event
	mock := NewMockConnector("test", "bot", func(ev protocol.Event) {
		published = append(published, ev)
	})

	event, err := mock.Send(nil, protocol.Request{
		Channel: "C1",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Text != "hello" {
		t.Fatalf("unexpected text: %q", event.Text)
	}
	if event.Direction != "out" {
		t.Fatalf("expected direction 'out', got %q", event.Direction)
	}
	if len(published) < 1 {
		t.Fatal("expected at least 1 published event")
	}
}

func TestMockConnector_SendEmpty(t *testing.T) {
	mock := NewMockConnector("test", "bot", func(ev protocol.Event) {})
	_, err := mock.Send(nil, protocol.Request{Channel: "C1", Text: "  "})
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}
