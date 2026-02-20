package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// --- Matrix tests ---

func TestResolveMatrixRoom(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "!abc:matrix.org"}, "!abc:matrix.org"},
		{"target with room prefix", protocol.Request{Target: "room:!def:example.com"}, "!def:example.com"},
		{"target with matrix:room prefix", protocol.Request{Target: "matrix:room:!ghi:host"}, "!ghi:host"},
		{"target with matrix prefix", protocol.Request{Target: "matrix:!jkl:host"}, "!jkl:host"},
		{"bare target", protocol.Request{Target: "!mno:host"}, "!mno:host"},
		{"channel takes precedence", protocol.Request{Channel: "!aaa:host", Target: "!bbb:host"}, "!aaa:host"},
		{"empty", protocol.Request{}, ""},
		{"whitespace target", protocol.Request{Target: "  "}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMatrixRoom(tt.request)
			if got != tt.want {
				t.Errorf("resolveMatrixRoom() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatrixAcceptsChannel(t *testing.T) {
	t.Run("empty allowlist accepts all", func(t *testing.T) {
		c := &MatrixConnector{channels: map[string]struct{}{}}
		if !c.acceptsChannel("!any:host") {
			t.Error("expected empty allowlist to accept any channel")
		}
	})

	t.Run("allowlist filters", func(t *testing.T) {
		c := &MatrixConnector{channels: map[string]struct{}{
			"!abc:matrix.org": {},
		}}
		if !c.acceptsChannel("!abc:matrix.org") {
			t.Error("expected allowed channel to be accepted")
		}
		if c.acceptsChannel("!xyz:matrix.org") {
			t.Error("expected unlisted channel to be rejected")
		}
	})

	t.Run("rememberChannel adds to allowlist", func(t *testing.T) {
		c := &MatrixConnector{channels: map[string]struct{}{
			"!abc:matrix.org": {},
		}}
		c.rememberChannel("!def:matrix.org")
		if !c.acceptsChannel("!def:matrix.org") {
			t.Error("expected remembered channel to be accepted")
		}
	})
}

// --- Twilio tests ---

func TestResolveTwilioChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "+15551234567"}, "+15551234567"},
		{"target with phone prefix", protocol.Request{Target: "phone:+15559876543"}, "+15559876543"},
		{"target with twilio:phone prefix", protocol.Request{Target: "twilio:phone:+15551111111"}, "+15551111111"},
		{"target with twilio prefix", protocol.Request{Target: "twilio:+15552222222"}, "+15552222222"},
		{"bare target", protocol.Request{Target: "+15553333333"}, "+15553333333"},
		{"channel takes precedence", protocol.Request{Channel: "+1111", Target: "+2222"}, "+1111"},
		{"empty", protocol.Request{}, ""},
		{"whitespace target", protocol.Request{Target: "  "}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTwilioChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveTwilioChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTwilioDate(t *testing.T) {
	t.Run("RFC1123Z format", func(t *testing.T) {
		ts := parseTwilioDate("Thu, 01 Feb 2024 12:30:00 +0000")
		if ts.Year() != 2024 || ts.Month() != 2 || ts.Day() != 1 {
			t.Errorf("unexpected date: %v", ts)
		}
	})

	t.Run("invalid format returns current time", func(t *testing.T) {
		ts := parseTwilioDate("not-a-date")
		if ts.IsZero() {
			t.Error("expected fallback timestamp, got zero")
		}
	})
}

func TestTwilioAcceptsChannel(t *testing.T) {
	t.Run("empty allowlist accepts all", func(t *testing.T) {
		c := &TwilioConnector{channels: map[string]struct{}{}}
		if !c.acceptsChannel("+15551234567") {
			t.Error("expected empty allowlist to accept any channel")
		}
	})

	t.Run("allowlist filters", func(t *testing.T) {
		c := &TwilioConnector{channels: map[string]struct{}{
			"+15551234567": {},
		}}
		if !c.acceptsChannel("+15551234567") {
			t.Error("expected allowed channel to be accepted")
		}
		if c.acceptsChannel("+15559999999") {
			t.Error("expected unlisted channel to be rejected")
		}
	})

	t.Run("rememberChannel adds to allowlist", func(t *testing.T) {
		c := &TwilioConnector{channels: map[string]struct{}{
			"+15551234567": {},
		}}
		c.rememberChannel("+15559876543")
		if !c.acceptsChannel("+15559876543") {
			t.Error("expected remembered channel to be accepted")
		}
	})
}

// --- Zulip tests ---

func TestResolveZulipChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"direct channel", protocol.Request{Channel: "12345"}, "12345"},
		{"target with channel prefix", protocol.Request{Target: "channel:67890"}, "67890"},
		{"target with zulip:channel prefix", protocol.Request{Target: "zulip:channel:111"}, "111"},
		{"target with stream prefix", protocol.Request{Target: "stream:222"}, "222"},
		{"target with zulip:stream prefix", protocol.Request{Target: "zulip:stream:333"}, "333"},
		{"bare target", protocol.Request{Target: "444"}, "444"},
		{"channel takes precedence", protocol.Request{Channel: "aaa", Target: "bbb"}, "aaa"},
		{"empty", protocol.Request{}, ""},
		{"whitespace target", protocol.Request{Target: "  "}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveZulipChannel(tt.request)
			if got != tt.want {
				t.Errorf("resolveZulipChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestZulipAcceptsChannel(t *testing.T) {
	t.Run("empty allowlist accepts all", func(t *testing.T) {
		c := &ZulipConnector{channels: map[string]struct{}{}}
		if !c.acceptsChannel("12345") {
			t.Error("expected empty allowlist to accept any channel")
		}
	})

	t.Run("allowlist filters", func(t *testing.T) {
		c := &ZulipConnector{channels: map[string]struct{}{
			"12345": {},
		}}
		if !c.acceptsChannel("12345") {
			t.Error("expected allowed channel to be accepted")
		}
		if c.acceptsChannel("99999") {
			t.Error("expected unlisted channel to be rejected")
		}
	})

	t.Run("rememberChannel adds to allowlist", func(t *testing.T) {
		c := &ZulipConnector{channels: map[string]struct{}{
			"12345": {},
		}}
		c.rememberChannel("67890")
		if !c.acceptsChannel("67890") {
			t.Error("expected remembered channel to be accepted")
		}
	})
}

// ---------------------------------------------------------------------------
// isSlackChannelID tests
// ---------------------------------------------------------------------------

func TestIsSlackChannelID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid Slack IDs
		{"C0123456789", true},
		{"C0123ABCDEF", true},
		{"G01AB2CD3EF", true},
		{"D04EXAMPLE0", true},
		{"C012345678901234", true}, // longer IDs are valid

		// Friendly names (should NOT match)
		{"#general", false},
		{"general", false},
		{"engineering", false},
		{"#ops-alerts", false},
		{"my-channel", false},

		// Edge cases
		{"", false},
		{"C", false},
		{"C01234", false},       // too short
		{"c0123456789", false},  // lowercase prefix
		{"X0123456789", false},  // wrong prefix letter
		{"C0123456 89", false},  // space inside
		{"C012345678a", false},  // lowercase letter
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isSlackChannelID(tt.input)
			if got != tt.want {
				t.Errorf("isSlackChannelID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isDiscordChannelID tests
// ---------------------------------------------------------------------------

func TestIsDiscordChannelID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid Discord snowflakes
		{"12345678901234567", true},   // 17 digits
		{"123456789012345678", true},  // 18 digits
		{"1234567890123456789", true}, // 19 digits
		{"12345678901234567890", true}, // 20 digits

		// Friendly names
		{"#general", false},
		{"general", false},
		{"announcements", false},
		{"voice-chat", false},

		// Edge cases
		{"", false},
		{"1234567890123456", false},    // 16 digits - too short
		{"123456789012345678901", false}, // 21 digits - too long
		{"1234567890123456a", false},    // letter in digits
		{"12345678901234567 ", false},   // trailing space
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isDiscordChannelID(tt.input)
			if got != tt.want {
				t.Errorf("isDiscordChannelID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isMattermostChannelID tests
// ---------------------------------------------------------------------------

func TestIsMattermostChannelID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid Mattermost IDs (26 lowercase alphanumeric)
		{"a1b2c3d4e5f6g7h8i9j0klmnop", true},
		{"abcdefghijklmnopqrstuvwxyz", true},
		{"01234567890123456789012345", true},

		// Friendly names
		{"town-square", false},
		{"off-topic", false},
		{"general", false},
		{"engineering-team", false},

		// Edge cases
		{"", false},
		{"a1b2c3d4e5f6g7h8i9j0klmno", false},  // 25 chars - too short
		{"a1b2c3d4e5f6g7h8i9j0klmnopq", false}, // 27 chars - too long
		{"A1B2C3D4E5F6G7H8I9J0KLMNOP", false},  // uppercase
		{"a1b2c3d4e5f6g7h8i9j0klmno!", false},   // special char
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isMattermostChannelID(tt.input)
			if got != tt.want {
				t.Errorf("isMattermostChannelID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isTelegramChatID tests
// ---------------------------------------------------------------------------

func TestIsTelegramChatID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid Telegram chat IDs (positive and negative integers)
		{"-1001234567890", true},
		{"1234567890", true},
		{"-100", true},
		{"0", true},

		// Friendly names
		{"@mychannel", false},
		{"@my_alerts_channel", false},
		{"mygroup", false},

		// Edge cases
		{"", false},
		{"12.34", false},
		{"abc", false},
		{"-", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isTelegramChatID(tt.input)
			if got != tt.want {
				t.Errorf("isTelegramChatID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isZulipStreamID tests
// ---------------------------------------------------------------------------

func TestIsZulipStreamID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid Zulip stream IDs (positive integers)
		{"123", true},
		{"1", true},
		{"999999", true},
		{"0", true},

		// Friendly names
		{"general", false},
		{"engineering", false},
		{"design-team", false},
		{"#general", false},

		// Edge cases
		{"", false},
		{"12.5", false},
		{"abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isZulipStreamID(tt.input)
			if got != tt.want {
				t.Errorf("isZulipStreamID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mattermost resolveChannelNames integration test (with httptest)
// ---------------------------------------------------------------------------

func TestMattermostResolveChannelNames(t *testing.T) {
	mux := http.NewServeMux()

	// /api/v4/users/me/teams → returns one team
	mux.HandleFunc("/api/v4/users/me/teams", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"id": "team1"}})
	})

	// /api/v4/teams/team1/channels/name/town-square → returns resolved ID
	mux.HandleFunc("/api/v4/teams/team1/channels/name/town-square", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "resolved_channel_id_1"})
	})

	// /api/v4/teams/team1/channels/name/unknown → 404
	mux.HandleFunc("/api/v4/teams/team1/channels/name/unknown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("resolves friendly name to ID", func(t *testing.T) {
		c := &MattermostConnector{
			botName:    "test",
			endpoint:   srv.URL,
			token:      "test-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"town-square": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["resolved_channel_id_1"]; !ok {
			t.Error("expected 'town-square' to be resolved to 'resolved_channel_id_1'")
		}
		if _, ok := c.channels["town-square"]; ok {
			t.Error("expected 'town-square' to be removed after resolution")
		}
	})

	t.Run("keeps raw ID unchanged", func(t *testing.T) {
		rawID := "a1b2c3d4e5f6g7h8i9j0klmnop"
		c := &MattermostConnector{
			botName:    "test",
			endpoint:   srv.URL,
			token:      "test-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{rawID: {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels[rawID]; !ok {
			t.Error("expected raw ID to remain unchanged")
		}
	})

	t.Run("keeps unresolvable name as-is", func(t *testing.T) {
		c := &MattermostConnector{
			botName:    "test",
			endpoint:   srv.URL,
			token:      "test-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"unknown": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["unknown"]; !ok {
			t.Error("expected unresolvable name to remain as-is")
		}
	})

	t.Run("mixed raw IDs and friendly names", func(t *testing.T) {
		rawID := "a1b2c3d4e5f6g7h8i9j0klmnop"
		c := &MattermostConnector{
			botName:    "test",
			endpoint:   srv.URL,
			token:      "test-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{rawID: {}, "town-square": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels[rawID]; !ok {
			t.Error("expected raw ID to remain")
		}
		if _, ok := c.channels["resolved_channel_id_1"]; !ok {
			t.Error("expected 'town-square' to be resolved")
		}
		if len(c.channels) != 2 {
			t.Errorf("expected 2 channels, got %d", len(c.channels))
		}
	})
}

// ---------------------------------------------------------------------------
// Telegram resolveChannelNames integration test (with httptest)
// ---------------------------------------------------------------------------

func TestTelegramResolveChannelNames(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/bottest-token/getChat", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		chatID := body["chat_id"]

		if chatID == "@mychannel" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":     true,
				"result": map[string]interface{}{"id": -1001234567890},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": false,
			})
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("resolves @username to chat ID", func(t *testing.T) {
		c := &TelegramConnector{
			botName:    "test",
			baseURL:    srv.URL + "/bottest-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"@mychannel": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["-1001234567890"]; !ok {
			t.Error("expected '@mychannel' to be resolved to '-1001234567890'")
		}
		if _, ok := c.channels["@mychannel"]; ok {
			t.Error("expected '@mychannel' to be removed after resolution")
		}
	})

	t.Run("keeps numeric chat ID unchanged", func(t *testing.T) {
		c := &TelegramConnector{
			botName:    "test",
			baseURL:    srv.URL + "/bottest-token",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"-1001234567890": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["-1001234567890"]; !ok {
			t.Error("expected numeric ID to remain unchanged")
		}
	})
}

// ---------------------------------------------------------------------------
// Zulip resolveChannelNames integration test (with httptest)
// ---------------------------------------------------------------------------

func TestZulipResolveChannelNames(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/get_stream_id", func(w http.ResponseWriter, r *http.Request) {
		stream := r.URL.Query().Get("stream")
		switch stream {
		case "general":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result":    "success",
				"stream_id": 42,
			})
		case "engineering":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result":    "success",
				"stream_id": 99,
			})
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": "error",
				"msg":    fmt.Sprintf("Invalid stream name '%s'", stream),
			})
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("resolves stream name to ID", func(t *testing.T) {
		c := &ZulipConnector{
			botName:    "test",
			endpoint:   srv.URL,
			email:      "bot@example.com",
			apiKey:     "test-key",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"general": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["42"]; !ok {
			t.Error("expected 'general' to be resolved to '42'")
		}
		if _, ok := c.channels["general"]; ok {
			t.Error("expected 'general' to be removed after resolution")
		}
	})

	t.Run("keeps numeric ID unchanged", func(t *testing.T) {
		c := &ZulipConnector{
			botName:    "test",
			endpoint:   srv.URL,
			email:      "bot@example.com",
			apiKey:     "test-key",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"42": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["42"]; !ok {
			t.Error("expected numeric ID to remain unchanged")
		}
	})

	t.Run("resolves multiple stream names", func(t *testing.T) {
		c := &ZulipConnector{
			botName:    "test",
			endpoint:   srv.URL,
			email:      "bot@example.com",
			apiKey:     "test-key",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"general": {}, "engineering": {}, "42": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["42"]; !ok {
			t.Error("expected existing '42' to remain")
		}
		if _, ok := c.channels["99"]; !ok {
			t.Error("expected 'engineering' to be resolved to '99'")
		}
		// 'general' resolves to '42' which already exists — both should merge
		if len(c.channels) > 3 {
			t.Errorf("expected at most 3 channels, got %d", len(c.channels))
		}
	})

	t.Run("keeps unresolvable name as-is", func(t *testing.T) {
		c := &ZulipConnector{
			botName:    "test",
			endpoint:   srv.URL,
			email:      "bot@example.com",
			apiKey:     "test-key",
			httpClient: srv.Client(),
			channels:   map[string]struct{}{"nonexistent": {}},
		}
		c.resolveChannelNames(context.Background())
		if _, ok := c.channels["nonexistent"]; !ok {
			t.Error("expected unresolvable name to remain as-is")
		}
	})
}
