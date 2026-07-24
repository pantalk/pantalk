package upstream

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

const (
	testNostrPrivateKey = "1111111111111111111111111111111111111111111111111111111111111111"
	testNostrChannelID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testNostrThreadID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testNostrPeer       = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testNostrRelay      = "wss://relay.example.com"
)

func TestNewNostrConnector(t *testing.T) {
	t.Setenv("PANTALK_TEST_NOSTR_KEY", testNostrPrivateKey)

	connector, err := NewNostrConnector(config.BotConfig{
		Name:       "nostr-helper",
		Type:       "nostr",
		PrivateKey: "$PANTALK_TEST_NOSTR_KEY",
		Relays:     []string{testNostrRelay, testNostrRelay + "/", " "},
		Channels: []string{
			"nip28:" + testNostrChannelID,
			"nip29:" + testNostrRelay + "'engineering",
		},
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatalf("NewNostrConnector() error = %v", err)
	}

	if connector.Identity() == "" || connector.Identity() == testNostrPrivateKey {
		t.Fatalf("Identity() = %q, want derived public key", connector.Identity())
	}
	if len(connector.relays) != 1 {
		t.Fatalf("relays = %#v, want one normalized relay", connector.relays)
	}
	if !connector.acceptsNIP28(testNostrChannelID) {
		t.Fatal("configured NIP-28 channel was not accepted")
	}
	if _, ok := connector.configuredNIP29Group(testNostrRelay, "engineering"); !ok {
		t.Fatal("configured relay-scoped NIP-29 group was not accepted")
	}
}

func TestNewNostrConnectorValidation(t *testing.T) {
	tests := []struct {
		name string
		bot  config.BotConfig
		want string
	}{
		{
			name: "private key",
			bot: config.BotConfig{
				Name:   "nostr-helper",
				Type:   "nostr",
				Relays: []string{testNostrRelay},
			},
			want: "private_key",
		},
		{
			name: "invalid private key",
			bot: config.BotConfig{
				Name:       "nostr-helper",
				Type:       "nostr",
				PrivateKey: "not-a-key",
				Relays:     []string{testNostrRelay},
			},
			want: "64-character hex",
		},
		{
			name: "relay",
			bot: config.BotConfig{
				Name:       "nostr-helper",
				Type:       "nostr",
				PrivateKey: testNostrPrivateKey,
			},
			want: "at least one relay",
		},
		{
			name: "relay scheme",
			bot: config.BotConfig{
				Name:       "nostr-helper",
				Type:       "nostr",
				PrivateKey: testNostrPrivateKey,
				Relays:     []string{"https://relay.example.com"},
			},
			want: "ws:// or wss://",
		},
		{
			name: "unscoped NIP-29 group",
			bot: config.BotConfig{
				Name:       "nostr-helper",
				Type:       "nostr",
				PrivateKey: testNostrPrivateKey,
				Relays:     []string{testNostrRelay},
				Channels:   []string{"nip29:engineering"},
			},
			want: "relay-scoped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewNostrConnector(tt.bot, func(protocol.Event) {})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveNostrDestination(t *testing.T) {
	tests := []struct {
		name      string
		request   protocol.Request
		wantKind  nostrDestinationKind
		wantID    string
		wantRelay string
		wantErr   string
	}{
		{
			name:     "DM",
			request:  protocol.Request{Target: "nostr:dm:" + testNostrPeer},
			wantKind: nostrDestinationDM,
			wantID:   testNostrPeer,
		},
		{
			name:     "NIP-28 channel",
			request:  protocol.Request{Channel: "nip28:" + testNostrChannelID},
			wantKind: nostrDestinationNIP28,
			wantID:   testNostrChannelID,
		},
		{
			name:      "NIP-29 group",
			request:   protocol.Request{Channel: "nip29:" + testNostrRelay + "'engineering"},
			wantKind:  nostrDestinationNIP29,
			wantID:    "engineering",
			wantRelay: nostr.NormalizeURL(testNostrRelay),
		},
		{
			name:    "missing relay scope",
			request: protocol.Request{Channel: "nip29:engineering"},
			wantErr: "relay-scoped",
		},
		{
			name:    "empty",
			request: protocol.Request{},
			wantErr: "requires channel or target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveNostrDestination(tt.request)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNostrDestination() error = %v", err)
			}
			if got.kind != tt.wantKind || got.id != tt.wantID || got.relay != tt.wantRelay {
				t.Fatalf("destination = %#v", got)
			}
		})
	}
}

func TestNostrHandleNIP28Message(t *testing.T) {
	connector, published := newTestNostrConnector(t,
		[]string{"nip28:" + testNostrChannelID})

	connector.handleNostrEvent(testNostrRelay, &nostr.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    testNostrPeer,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindChannelMessage,
		Tags: nostr.Tags{
			nostr.Tag{"e", testNostrChannelID, testNostrRelay, "root"},
			nostr.Tag{"e", testNostrThreadID, testNostrRelay, "reply"},
		},
		Content: "hello channel",
	})

	if len(*published) != 1 {
		t.Fatalf("published %d events, want 1", len(*published))
	}
	event := (*published)[0]
	if event.Channel != "nip28:"+testNostrChannelID ||
		event.Thread != testNostrThreadID ||
		event.User != testNostrPeer ||
		event.Text != "hello channel" {
		t.Fatalf("event = %#v", event)
	}
}

func TestNostrNIP29GroupsAreRelayScoped(t *testing.T) {
	connector, published := newTestNostrConnector(t,
		[]string{"nip29:" + testNostrRelay + "'engineering"})
	event := &nostr.Event{
		PubKey:    testNostrPeer,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindSimpleGroupChatMessage,
		Tags:      nostr.Tags{nostr.Tag{"h", "engineering"}},
		Content:   "hello group",
	}

	connector.handleNostrEvent("wss://other.example.com", event)
	if len(*published) != 0 {
		t.Fatalf("accepted group event from wrong relay: %#v", *published)
	}

	connector.handleNostrEvent(testNostrRelay, event)
	if len(*published) != 1 {
		t.Fatalf("published %d events, want 1", len(*published))
	}
	if got := (*published)[0].Channel; got != "nip29:"+nostr.NormalizeURL(testNostrRelay)+"'engineering" {
		t.Fatalf("channel = %q", got)
	}
}

func TestNostrHandleDirectMessage(t *testing.T) {
	connector, published := newTestNostrConnector(t, nil)
	connector.handleDirectMessage(&nostr.Event{
		PubKey:    testNostrPeer,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindDirectMessage,
		Tags: nostr.Tags{
			nostr.Tag{"p", connector.publicKey},
			nostr.Tag{"e", testNostrThreadID},
		},
		Content: "secret hello",
	})

	if len(*published) != 1 {
		t.Fatalf("published %d events, want 1", len(*published))
	}
	event := (*published)[0]
	if !event.Direct || event.Target != "dm:"+testNostrPeer ||
		event.Thread != testNostrThreadID || event.Text != "secret hello" {
		t.Fatalf("event = %#v", event)
	}
}

func TestNostrSendNIP28Message(t *testing.T) {
	connector, published := newTestNostrConnector(t,
		[]string{"nip28:" + testNostrChannelID})

	var sent nostr.Event
	connector.publishEvent = func(_ context.Context, relays []string, event nostr.Event) error {
		if len(relays) != 1 || relays[0] != nostr.NormalizeURL(testNostrRelay) {
			t.Fatalf("relays = %#v", relays)
		}
		sent = event
		return nil
	}

	event, err := connector.Send(context.Background(), protocol.Request{
		Channel: "nip28:" + testNostrChannelID,
		Thread:  testNostrThreadID,
		Text:    "**hello**",
		Format:  "markdown",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.Kind != nostr.KindChannelMessage || sent.Content != "hello" {
		t.Fatalf("sent event = %#v", sent)
	}
	if ok, err := sent.CheckSignature(); err != nil || !ok {
		t.Fatalf("signature valid = %v, error = %v", ok, err)
	}
	root, reply := nip28References(sent.Tags)
	if root != testNostrChannelID || reply != testNostrThreadID {
		t.Fatalf("root/reply = %q/%q", root, reply)
	}
	if event.Direction != "out" || event.Thread != testNostrThreadID ||
		len(*published) != 1 {
		t.Fatalf("event = %#v, published = %#v", event, *published)
	}
}

func TestNostrSendDirectMessage(t *testing.T) {
	connector, published := newTestNostrConnector(t, nil)
	connector.sendDirect = func(
		_ context.Context,
		content string,
		tags nostr.Tags,
		recipient string,
	) (string, error) {
		if content != "hello" || recipient != testNostrPeer ||
			firstNostrTagValue(tags, "e") != testNostrThreadID {
			t.Fatalf("direct send content=%q recipient=%q tags=%#v", content, recipient, tags)
		}
		return strings.Repeat("e", 64), nil
	}

	event, err := connector.Send(context.Background(), protocol.Request{
		Target: "dm:" + testNostrPeer,
		Thread: testNostrThreadID,
		Text:   "hello",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !event.Direct || event.Channel != "dm:"+testNostrPeer || len(*published) != 1 {
		t.Fatalf("event = %#v, published = %#v", event, *published)
	}
}

func TestNostrPublishesDMRelayList(t *testing.T) {
	connector, _ := newTestNostrConnector(t, nil)

	var sent nostr.Event
	connector.publishEvent = func(_ context.Context, relays []string, event nostr.Event) error {
		if len(relays) != 1 || relays[0] != nostr.NormalizeURL(testNostrRelay) {
			t.Fatalf("relays = %#v", relays)
		}
		sent = event
		return nil
	}

	if err := connector.publishDMRelayList(context.Background()); err != nil {
		t.Fatalf("publishDMRelayList() error = %v", err)
	}
	if sent.Kind != nostr.KindDMRelayList ||
		firstNostrTagValue(sent.Tags, "relay") != nostr.NormalizeURL(testNostrRelay) {
		t.Fatalf("relay list event = %#v", sent)
	}
	if ok, err := sent.CheckSignature(); err != nil || !ok {
		t.Fatalf("signature valid = %v, error = %v", ok, err)
	}
}

func TestNostrPublishDMRelayListPropagatesFailure(t *testing.T) {
	connector, _ := newTestNostrConnector(t, nil)
	connector.publishEvent = func(context.Context, []string, nostr.Event) error {
		return errors.New("relay unavailable")
	}

	err := connector.publishDMRelayList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "relay unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func newTestNostrConnector(
	t *testing.T,
	channels []string,
) (*NostrConnector, *[]protocol.Event) {
	t.Helper()

	published := make([]protocol.Event, 0)
	connector, err := NewNostrConnector(config.BotConfig{
		Name:       "nostr-helper",
		Type:       "nostr",
		PrivateKey: testNostrPrivateKey,
		Relays:     []string{testNostrRelay},
		Channels:   channels,
	}, func(event protocol.Event) {
		published = append(published, event)
	})
	if err != nil {
		t.Fatalf("NewNostrConnector() error = %v", err)
	}
	return connector, &published
}
