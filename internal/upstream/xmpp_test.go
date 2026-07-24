package upstream

import (
	"encoding/xml"
	"strings"
	"testing"

	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/ping"
	"mellium.im/xmpp/stanza"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

func TestNewXMPPConnector(t *testing.T) {
	t.Setenv("PANTALK_XMPP_PASSWORD", "secret")

	connector, err := NewXMPPConnector(config.BotConfig{
		Name:        "helper",
		Type:        "xmpp",
		DisplayName: "PanTalk Helper",
		JID:         "Helper@Example.COM/pantalk",
		Password:    "$PANTALK_XMPP_PASSWORD",
		Endpoint:    "chat.example.com",
		Channels: []string{
			"agents@conference.example.com/ignored-resource",
			" ",
		},
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatalf("NewXMPPConnector: %v", err)
	}

	if got, want := connector.account.String(), "helper@example.com"; got != want {
		t.Fatalf("account = %q, want %q", got, want)
	}
	if got, want := connector.Identity(), "helper@example.com"; got != want {
		t.Fatalf("Identity() = %q, want %q", got, want)
	}
	if got, want := connector.password, "secret"; got != want {
		t.Fatalf("password = %q, want resolved credential", got)
	}
	if got, want := connector.endpoint, "chat.example.com:5222"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := connector.nickname, "PanTalk Helper"; got != want {
		t.Fatalf("nickname = %q, want %q", got, want)
	}
	if !connector.acceptsRoom(jid.MustParse("agents@conference.example.com")) {
		t.Fatal("configured room was not retained")
	}
}

func TestNewXMPPConnectorValidation(t *testing.T) {
	tests := []struct {
		name string
		bot  config.BotConfig
		want string
	}{
		{
			name: "missing account",
			bot:  config.BotConfig{Name: "helper", Type: "xmpp", Password: "secret"},
			want: "requires jid",
		},
		{
			name: "domain only account",
			bot: config.BotConfig{
				Name: "helper", Type: "xmpp", JID: "example.com", Password: "secret",
			},
			want: "must include a localpart",
		},
		{
			name: "missing password",
			bot: config.BotConfig{
				Name: "helper", Type: "xmpp", JID: "helper@example.com",
			},
			want: "credential value cannot be empty",
		},
		{
			name: "URL endpoint",
			bot: config.BotConfig{
				Name: "helper", Type: "xmpp", JID: "helper@example.com",
				Password: "secret", Endpoint: "https://example.com",
			},
			want: "without a URL scheme",
		},
		{
			name: "invalid endpoint port",
			bot: config.BotConfig{
				Name: "helper", Type: "xmpp", JID: "helper@example.com",
				Password: "secret", Endpoint: "example.com:not-a-port",
			},
			want: "valid host and numeric port",
		},
		{
			name: "invalid room",
			bot: config.BotConfig{
				Name: "helper", Type: "xmpp", JID: "helper@example.com",
				Password: "secret", Channels: []string{"conference.example.com"},
			},
			want: "room JID must include a localpart",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewXMPPConnector(tt.bot, func(protocol.Event) {})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestXMPPResolveDestination(t *testing.T) {
	connector := mustTestXMPPConnector(t, nil)

	tests := []struct {
		name        string
		request     protocol.Request
		wantJID     string
		wantGroup   bool
		wantChannel string
		wantTarget  string
	}{
		{
			name: "channel field",
			request: protocol.Request{
				Channel: "agents@conference.example.com",
			},
			wantJID:     "agents@conference.example.com",
			wantGroup:   true,
			wantChannel: "agents@conference.example.com",
			wantTarget:  "room:agents@conference.example.com",
		},
		{
			name: "prefixed room target",
			request: protocol.Request{
				Target: "xmpp:room:other@conference.example.com",
			},
			wantJID:     "other@conference.example.com",
			wantGroup:   true,
			wantChannel: "other@conference.example.com",
			wantTarget:  "room:other@conference.example.com",
		},
		{
			name: "prefixed DM target",
			request: protocol.Request{
				Target: "xmpp:dm:Ada@Example.COM/mobile",
			},
			wantJID:     "ada@example.com",
			wantGroup:   false,
			wantChannel: "dm:ada@example.com",
			wantTarget:  "dm:ada@example.com",
		},
		{
			name: "configured room as raw target",
			request: protocol.Request{
				Target: "agents@conference.example.com",
			},
			wantJID:     "agents@conference.example.com",
			wantGroup:   true,
			wantChannel: "agents@conference.example.com",
			wantTarget:  "room:agents@conference.example.com",
		},
		{
			name: "raw DM target",
			request: protocol.Request{
				Target: "ada@example.com",
			},
			wantJID:     "ada@example.com",
			wantGroup:   false,
			wantChannel: "dm:ada@example.com",
			wantTarget:  "dm:ada@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotJID, gotGroup, gotChannel, gotTarget, err := connector.resolveDestination(tt.request)
			if err != nil {
				t.Fatalf("resolveDestination: %v", err)
			}
			if gotJID.String() != tt.wantJID ||
				gotGroup != tt.wantGroup ||
				gotChannel != tt.wantChannel ||
				gotTarget != tt.wantTarget {
				t.Fatalf(
					"got (%q, %t, %q, %q), want (%q, %t, %q, %q)",
					gotJID, gotGroup, gotChannel, gotTarget,
					tt.wantJID, tt.wantGroup, tt.wantChannel, tt.wantTarget,
				)
			}
		})
	}
}

func TestXMPPHandleMessage(t *testing.T) {
	var events []protocol.Event
	connector := mustTestXMPPConnector(t, func(event protocol.Event) {
		events = append(events, event)
	})

	groupMessage := decodeTestXMPPMessage(t, `
		<message xmlns='jabber:client'
		         from='agents@conference.example.com/Ada'
		         to='helper@example.com/pantalk'
		         type='groupchat'>
		  <body>Hello from the room</body>
		  <thread>topic-42</thread>
		  <delay xmlns='urn:xmpp:delay' stamp='2026-07-24T10:11:12Z'/>
		</message>`)
	connector.handleMessage(groupMessage)

	directMessage := decodeTestXMPPMessage(t, `
		<message xmlns='jabber:client'
		         from='Grace@Example.COM/phone'
		         type='chat'>
		  <body>Hello directly</body>
		</message>`)
	connector.handleMessage(directMessage)

	typing := decodeTestXMPPMessage(t, `
		<message xmlns='jabber:client'
		         from='grace@example.com/phone'
		         type='chat'>
		  <composing xmlns='http://jabber.org/protocol/chatstates'/>
		</message>`)
	connector.handleMessage(typing)

	if got, want := len(events), 3; got != want {
		t.Fatalf("published %d events, want %d", got, want)
	}

	groupEvent := events[0]
	if groupEvent.Kind != "message" ||
		groupEvent.User != "Ada" ||
		groupEvent.Channel != "agents@conference.example.com" ||
		groupEvent.Target != "room:agents@conference.example.com" ||
		groupEvent.Thread != "topic-42" ||
		groupEvent.Text != "Hello from the room" {
		t.Fatalf("unexpected group event: %+v", groupEvent)
	}
	if got, want := groupEvent.Timestamp.Format("2006-01-02T15:04:05Z"), "2026-07-24T10:11:12Z"; got != want {
		t.Fatalf("timestamp = %q, want %q", got, want)
	}

	directEvent := events[1]
	if directEvent.Kind != "message" ||
		directEvent.User != "grace@example.com" ||
		directEvent.Channel != "dm:grace@example.com" ||
		directEvent.Target != "dm:grace@example.com" ||
		directEvent.Text != "Hello directly" {
		t.Fatalf("unexpected direct event: %+v", directEvent)
	}

	typingEvent := events[2]
	if typingEvent.Kind != "typing" ||
		typingEvent.User != "grace@example.com" ||
		typingEvent.Text != "composing" {
		t.Fatalf("unexpected typing event: %+v", typingEvent)
	}
}

func TestXMPPHandleMessageFiltersSelfAndUnconfiguredRooms(t *testing.T) {
	var events []protocol.Event
	connector := mustTestXMPPConnector(t, func(event protocol.Event) {
		events = append(events, event)
	})

	for _, raw := range []string{
		`<message xmlns='jabber:client' from='helper@example.com/other' type='chat'><body>self</body></message>`,
		`<message xmlns='jabber:client' from='agents@conference.example.com/PanTalk Helper' type='groupchat'><body>self MUC</body></message>`,
		`<message xmlns='jabber:client' from='other@conference.example.com/Ada' type='groupchat'><body>not configured</body></message>`,
		`<message xmlns='jabber:client' from='grace@example.com' type='chat'><active xmlns='http://jabber.org/protocol/chatstates'/></message>`,
	} {
		connector.handleMessage(decodeTestXMPPMessage(t, raw))
	}

	// XEP-0085 "active" is useful protocol state but not a typing indicator,
	// and the other stanzas are self/unconfigured messages.
	if len(events) != 1 || events[0].Kind != "typing" || events[0].Text != "active" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPrepareXMPPSegments(t *testing.T) {
	segments, err := prepareXMPPSegments("markdown", "**hello** [world](https://example.com)")
	if err != nil {
		t.Fatalf("prepareXMPPSegments: %v", err)
	}
	if got, want := strings.Join(segments, ""), "hello world"; got != want {
		t.Fatalf("segments = %q, want %q", got, want)
	}
}

func TestXMPPOutboundMessageIncludesThreadAndChatState(t *testing.T) {
	encoded, err := xml.Marshal(xmppOutboundMessage{
		Message: stanza.Message{
			To:   jid.MustParse("ada@example.com"),
			Type: stanza.ChatMessage,
		},
		Body:   "hello",
		Thread: "topic-42",
		State: &xmppChatState{
			XMLName: xml.Name{Space: xmppChatStatesNS, Local: "active"},
		},
	})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	got := string(encoded)
	for _, want := range []string{
		`to="ada@example.com"`,
		`type="chat"`,
		`<body>hello</body>`,
		`<thread>topic-42</thread>`,
		`<active xmlns="http://jabber.org/protocol/chatstates"></active>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("encoded message %q does not contain %q", got, want)
		}
	}
}

func TestXMPPMUCJoinDisablesHistory(t *testing.T) {
	encoded, err := xml.Marshal(xmppMUCJoinPresence{
		Presence: stanza.Presence{
			To: jid.MustParse("agents@conference.example.com/helper"),
		},
		MUC: xmppMUCJoin{
			History: xmppMUCHistory{MaxStanzas: 0},
		},
	})
	if err != nil {
		t.Fatalf("marshal MUC join: %v", err)
	}

	got := string(encoded)
	for _, want := range []string{
		`to="agents@conference.example.com/helper"`,
		`<x xmlns="http://jabber.org/protocol/muc">`,
		`<history maxstanzas="0"></history>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("encoded presence %q does not contain %q", got, want)
		}
	}
}

func TestXMPPHandlerSupportsServerPings(t *testing.T) {
	connector := mustTestXMPPConnector(t, nil)
	handler, ok := connector.handler().(*xmppConnectorHandler)
	if !ok {
		t.Fatalf("handler type = %T, want *xmppConnectorHandler", connector.handler())
	}

	if _, ok := handler.iq.IQHandler(
		stanza.GetIQ,
		xml.Name{Space: ping.NS, Local: "ping"},
	); !ok {
		t.Fatal("XEP-0199 ping handler is not registered")
	}
}

func mustTestXMPPConnector(t *testing.T, publish func(protocol.Event)) *XMPPConnector {
	t.Helper()
	if publish == nil {
		publish = func(protocol.Event) {}
	}
	connector, err := NewXMPPConnector(config.BotConfig{
		Name:        "helper",
		Type:        "xmpp",
		DisplayName: "PanTalk Helper",
		JID:         "helper@example.com",
		Password:    "secret",
		Channels:    []string{"agents@conference.example.com"},
	}, publish)
	if err != nil {
		t.Fatalf("NewXMPPConnector: %v", err)
	}
	return connector
}

func decodeTestXMPPMessage(t *testing.T, raw string) xmppInboundMessage {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var start xml.StartElement
	for {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read message start token: %v", err)
		}
		var ok bool
		start, ok = token.(xml.StartElement)
		if ok {
			break
		}
	}

	var message xmppInboundMessage
	if err := decoder.DecodeElement(&message, &start); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if message.Type == "" {
		message.Type = stanza.NormalMessage
	}
	return message
}

func decodeTestXMPPPresence(t *testing.T, raw string) xmppInboundPresence {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var start xml.StartElement
	for {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read presence start token: %v", err)
		}
		var ok bool
		start, ok = token.(xml.StartElement)
		if ok {
			break
		}
	}

	var presence xmppInboundPresence
	if err := decoder.DecodeElement(&presence, &start); err != nil {
		t.Fatalf("decode presence: %v", err)
	}
	return presence
}

// A room that refuses the join must say so. The stanza is addressed to our own
// nickname, so without explicit handling it would be dropped as self-presence
// and the room would silently never deliver a message.
func TestXMPPRejectedRoomJoinIsReported(t *testing.T) {
	var events []protocol.Event
	connector := mustTestXMPPConnector(t, func(event protocol.Event) {
		events = append(events, event)
	})

	connector.handlePresence(decodeTestXMPPPresence(t, `
		<presence xmlns='jabber:client'
		          from='agents@conference.example.com/PanTalk Helper'
		          to='helper@example.com/pantalk'
		          type='error'>
		  <error type='auth'>
		    <registration-required xmlns='urn:ietf:params:xml:ns:xmpp-stanzas'/>
		  </error>
		</presence>`))

	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	event := events[0]
	if event.Kind != "status" {
		t.Fatalf("Kind = %q, want status so the failure reaches the daemon log", event.Kind)
	}
	if !strings.Contains(event.Text, "agents@conference.example.com") ||
		!strings.Contains(event.Text, "registration-required") {
		t.Fatalf("status text = %q, want the room and the reason", event.Text)
	}
}

func TestXMPPSuccessfulRoomJoinIsReported(t *testing.T) {
	var events []protocol.Event
	connector := mustTestXMPPConnector(t, func(event protocol.Event) {
		events = append(events, event)
	})

	connector.handlePresence(decodeTestXMPPPresence(t, `
		<presence xmlns='jabber:client'
		          from='agents@conference.example.com/PanTalk Helper'
		          to='helper@example.com/pantalk'>
		  <x xmlns='http://jabber.org/protocol/muc#user'>
		    <item affiliation='member' role='participant'/>
		    <status code='110'/>
		  </x>
		</presence>`))

	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	if got := events[0]; got.Kind != "status" || !strings.Contains(got.Text, "joined room agents@conference.example.com") {
		t.Fatalf("unexpected join event: %+v", got)
	}
}

// Another occupant's presence is ambient room traffic, not a join confirmation.
func TestXMPPOtherOccupantPresenceStaysAmbient(t *testing.T) {
	var events []protocol.Event
	connector := mustTestXMPPConnector(t, func(event protocol.Event) {
		events = append(events, event)
	})

	connector.handlePresence(decodeTestXMPPPresence(t, `
		<presence xmlns='jabber:client'
		          from='agents@conference.example.com/Ada'
		          to='helper@example.com/pantalk'>
		  <show>away</show>
		  <x xmlns='http://jabber.org/protocol/muc#user'>
		    <item affiliation='member' role='participant'/>
		  </x>
		</presence>`))

	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	event := events[0]
	if event.Kind != "presence" || event.User != "Ada" || event.Text != "away" {
		t.Fatalf("unexpected occupant presence: %+v", event)
	}
}
