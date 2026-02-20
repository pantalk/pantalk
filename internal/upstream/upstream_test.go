package upstream

import (
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/pantalk/pantalk/internal/protocol"
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
	var mu sync.Mutex
	var published []protocol.Event
	mock := NewMockConnector("test", "bot", func(ev protocol.Event) {
		mu.Lock()
		published = append(published, ev)
		mu.Unlock()
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

	// Wait for the async echo event from the mock goroutine.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	count := len(published)
	mu.Unlock()
	if count < 1 {
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

// --- WhatsApp tests ---

func TestResolveWhatsAppJID(t *testing.T) {
	tests := []struct {
		name       string
		request    protocol.Request
		wantUser   string
		wantServer string
		wantErr    bool
	}{
		{
			name:       "direct channel phone number",
			request:    protocol.Request{Channel: "1234567890"},
			wantUser:   "1234567890",
			wantServer: types.DefaultUserServer,
		},
		{
			name:       "group channel with dash",
			request:    protocol.Request{Channel: "12345678-9876543"},
			wantUser:   "12345678-9876543",
			wantServer: types.GroupServer,
		},
		{
			name:       "full JID with @",
			request:    protocol.Request{Channel: "1234567890@s.whatsapp.net"},
			wantUser:   "1234567890",
			wantServer: types.DefaultUserServer,
		},
		{
			name:       "full group JID with @",
			request:    protocol.Request{Channel: "12345678-9876543@g.us"},
			wantUser:   "12345678-9876543",
			wantServer: types.GroupServer,
		},
		{
			name:       "target with chat prefix",
			request:    protocol.Request{Target: "chat:1234567890"},
			wantUser:   "1234567890",
			wantServer: types.DefaultUserServer,
		},
		{
			name:       "target with whatsapp:chat prefix",
			request:    protocol.Request{Target: "whatsapp:chat:1234567890"},
			wantUser:   "1234567890",
			wantServer: types.DefaultUserServer,
		},
		{
			name:       "target with whatsapp prefix",
			request:    protocol.Request{Target: "whatsapp:1234567890"},
			wantUser:   "1234567890",
			wantServer: types.DefaultUserServer,
		},
		{
			name:       "channel takes precedence over target",
			request:    protocol.Request{Channel: "111", Target: "222"},
			wantUser:   "111",
			wantServer: types.DefaultUserServer,
		},
		{
			name:    "empty channel and target",
			request: protocol.Request{},
			wantErr: true,
		},
		{
			name:    "whitespace target",
			request: protocol.Request{Target: "   "},
			wantErr: true,
		},
		{
			name:       "lid JID",
			request:    protocol.Request{Channel: "171674909585581@lid"},
			wantUser:   "171674909585581",
			wantServer: "lid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jid, err := resolveWhatsAppJID(tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if jid.User != tt.wantUser {
				t.Errorf("user = %q, want %q", jid.User, tt.wantUser)
			}
			if jid.Server != tt.wantServer {
				t.Errorf("server = %q, want %q", jid.Server, tt.wantServer)
			}
		})
	}
}

func TestExtractWhatsAppText(t *testing.T) {
	tests := []struct {
		name string
		msg  *events.Message
		want string
	}{
		{
			name: "plain conversation",
			msg: &events.Message{
				Message: &waE2E.Message{
					Conversation: proto.String("hello world"),
				},
			},
			want: "hello world",
		},
		{
			name: "conversation with whitespace",
			msg: &events.Message{
				Message: &waE2E.Message{
					Conversation: proto.String("  padded  "),
				},
			},
			want: "padded",
		},
		{
			name: "extended text message",
			msg: &events.Message{
				Message: &waE2E.Message{
					ExtendedTextMessage: &waE2E.ExtendedTextMessage{
						Text: proto.String("quoted reply"),
					},
				},
			},
			want: "quoted reply",
		},
		{
			name: "image caption",
			msg: &events.Message{
				Message: &waE2E.Message{
					ImageMessage: &waE2E.ImageMessage{
						Caption: proto.String("photo caption"),
					},
				},
			},
			want: "photo caption",
		},
		{
			name: "video caption",
			msg: &events.Message{
				Message: &waE2E.Message{
					VideoMessage: &waE2E.VideoMessage{
						Caption: proto.String("video caption"),
					},
				},
			},
			want: "video caption",
		},
		{
			name: "document caption",
			msg: &events.Message{
				Message: &waE2E.Message{
					DocumentMessage: &waE2E.DocumentMessage{
						Caption: proto.String("doc caption"),
					},
				},
			},
			want: "doc caption",
		},
		{
			name: "conversation takes precedence over extended",
			msg: &events.Message{
				Message: &waE2E.Message{
					Conversation: proto.String("plain"),
					ExtendedTextMessage: &waE2E.ExtendedTextMessage{
						Text: proto.String("extended"),
					},
				},
			},
			want: "plain",
		},
		{
			name: "empty message",
			msg: &events.Message{
				Message: &waE2E.Message{},
			},
			want: "",
		},
		{
			name: "whitespace-only conversation",
			msg: &events.Message{
				Message: &waE2E.Message{
					Conversation: proto.String("   "),
				},
			},
			want: "",
		},
		{
			name: "image with empty caption falls through to empty",
			msg: &events.Message{
				Message: &waE2E.Message{
					ImageMessage: &waE2E.ImageMessage{},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractWhatsAppText(tt.msg)
			if got != tt.want {
				t.Errorf("extractWhatsAppText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- IRC tests ---

func TestResolveIRCChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel with hash", protocol.Request{Channel: "#general"}, "#general"},
		{"direct channel without hash", protocol.Request{Channel: "general"}, "#general"},
		{"ampersand channel", protocol.Request{Channel: "&local"}, "&local"},
		{"target with channel prefix", protocol.Request{Target: "channel:#test"}, "#test"},
		{"target with irc:channel prefix", protocol.Request{Target: "irc:channel:test"}, "#test"},
		{"target with irc prefix", protocol.Request{Target: "irc:#chat"}, "#chat"},
		{"target dm prefix", protocol.Request{Target: "dm:user1"}, "user1"},
		{"target irc:dm prefix", protocol.Request{Target: "irc:dm:user1"}, "user1"},
		{"bare target", protocol.Request{Target: "#mychan"}, "#mychan"},
		{"channel takes precedence", protocol.Request{Channel: "#a", Target: "#b"}, "#a"},
		{"empty", protocol.Request{}, ""},
		{"whitespace target", protocol.Request{Target: "  "}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveIRCChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveIRCChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseIRCMessage(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantPrefix string
		wantCmd    string
		wantParams []string
	}{
		{
			"PING",
			"PING :server.example.com",
			"",
			"PING",
			[]string{"server.example.com"},
		},
		{
			"PRIVMSG",
			":nick!user@host PRIVMSG #channel :hello world",
			"nick!user@host",
			"PRIVMSG",
			[]string{"#channel", "hello world"},
		},
		{
			"welcome",
			":server 001 bot :Welcome to the IRC Network",
			"server",
			"001",
			[]string{"bot", "Welcome to the IRC Network"},
		},
		{
			"JOIN",
			":bot!bot@host JOIN :#channel",
			"bot!bot@host",
			"JOIN",
			[]string{"#channel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, cmd, params := parseIRCMessage(tt.line)
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
			if cmd != tt.wantCmd {
				t.Errorf("command = %q, want %q", cmd, tt.wantCmd)
			}
			if len(params) != len(tt.wantParams) {
				t.Errorf("params = %v, want %v", params, tt.wantParams)
			} else {
				for i := range params {
					if params[i] != tt.wantParams[i] {
						t.Errorf("params[%d] = %q, want %q", i, params[i], tt.wantParams[i])
					}
				}
			}
		})
	}
}

func TestExtractNick(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{"nick!user@host", "nick"},
		{"nick", "nick"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractNick(tt.prefix)
		if got != tt.want {
			t.Errorf("extractNick(%q) = %q, want %q", tt.prefix, got, tt.want)
		}
	}
}

func TestIRCAcceptsChannel(t *testing.T) {
	t.Run("empty allowlist accepts all", func(t *testing.T) {
		c := &IRCConnector{channels: map[string]struct{}{}}
		if !c.acceptsChannel("#anything") {
			t.Error("expected empty allowlist to accept any channel")
		}
	})

	t.Run("allowlist filters", func(t *testing.T) {
		c := &IRCConnector{channels: map[string]struct{}{
			"#general": {},
		}}
		if !c.acceptsChannel("#general") {
			t.Error("expected allowed channel to be accepted")
		}
		if c.acceptsChannel("#random") {
			t.Error("expected unlisted channel to be rejected")
		}
	})

	t.Run("rememberChannel adds to allowlist", func(t *testing.T) {
		c := &IRCConnector{channels: map[string]struct{}{
			"#general": {},
		}}
		c.rememberChannel("#random")
		if !c.acceptsChannel("#random") {
			t.Error("expected remembered channel to be accepted")
		}
	})
}

func TestWhatsAppAcceptsChannel(t *testing.T) {
	t.Run("empty allowlist accepts all", func(t *testing.T) {
		c := &WhatsAppConnector{channels: map[string]struct{}{}}
		if !c.acceptsChannel("anything") {
			t.Error("expected empty allowlist to accept any channel")
		}
	})

	t.Run("allowlist filters", func(t *testing.T) {
		c := &WhatsAppConnector{channels: map[string]struct{}{
			"1234@s.whatsapp.net": {},
		}}
		if !c.acceptsChannel("1234@s.whatsapp.net") {
			t.Error("expected allowed channel to be accepted")
		}
		if c.acceptsChannel("9999@s.whatsapp.net") {
			t.Error("expected unlisted channel to be rejected")
		}
	})

	t.Run("rememberChannel adds to allowlist", func(t *testing.T) {
		c := &WhatsAppConnector{channels: map[string]struct{}{
			"1234@s.whatsapp.net": {},
		}}
		c.rememberChannel("5678@s.whatsapp.net")
		if !c.acceptsChannel("5678@s.whatsapp.net") {
			t.Error("expected remembered channel to be accepted")
		}
	})
}
