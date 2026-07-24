package upstream

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/ping"
	"mellium.im/xmpp/stanza"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/formatting"
	"github.com/pantalk/pantalk/internal/protocol"
)

const (
	defaultXMPPPort    = "5222"
	xmppChatStatesNS   = "http://jabber.org/protocol/chatstates"
	xmppReconnectLimit = 30 * time.Second
)

// XMPPConnector bridges an authenticated XMPP client account to PanTalk.
// Configured channels are XEP-0045 MUC room JIDs; direct chat stanzas are
// accepted independently of the room allowlist.
type XMPPConnector struct {
	serviceName string
	botName     string
	account     jid.JID
	password    string
	endpoint    string
	nickname    string
	publish     func(protocol.Event)

	mu       sync.RWMutex
	session  *xmpp.Session
	identity jid.JID
	rooms    map[string]jid.JID
}

func NewXMPPConnector(bot config.BotConfig, publish func(protocol.Event)) (*XMPPConnector, error) {
	accountText := strings.TrimSpace(bot.JID)
	if accountText == "" {
		return nil, fmt.Errorf("xmpp bot %q requires jid", bot.Name)
	}

	account, err := jid.Parse(accountText)
	if err != nil || account.Localpart() == "" {
		if err == nil {
			err = errors.New("account JID must include a localpart")
		}
		return nil, fmt.Errorf("parse xmpp account JID for bot %q: %w", bot.Name, err)
	}

	password, err := config.ResolveCredential(bot.Password)
	if err != nil {
		return nil, fmt.Errorf("resolve xmpp password for bot %q: %w", bot.Name, err)
	}

	endpoint := strings.TrimSpace(bot.Endpoint)
	if endpoint != "" {
		endpoint, err = normalizeXMPPEndpoint(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse xmpp endpoint for bot %q: %w", bot.Name, err)
		}
	}

	nickname := strings.TrimSpace(bot.DisplayName)
	if nickname == "" {
		nickname = strings.TrimSpace(bot.Name)
	}
	if nickname == "" {
		nickname = account.Localpart()
	}
	nicknameJID, err := account.Bare().WithResource(nickname)
	if err != nil {
		return nil, fmt.Errorf("parse xmpp MUC nickname for bot %q: %w", bot.Name, err)
	}
	nickname = nicknameJID.Resourcepart()

	connector := &XMPPConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		account:     account.Bare(),
		password:    password,
		endpoint:    endpoint,
		nickname:    nickname,
		publish:     publish,
		identity:    account.Bare(),
		rooms:       make(map[string]jid.JID),
	}

	for _, configuredRoom := range bot.Channels {
		roomText := strings.TrimSpace(configuredRoom)
		if roomText == "" {
			continue
		}
		room, parseErr := jid.Parse(roomText)
		if parseErr != nil || room.Localpart() == "" {
			if parseErr == nil {
				parseErr = errors.New("room JID must include a localpart")
			}
			return nil, fmt.Errorf("parse xmpp room %q for bot %q: %w", roomText, bot.Name, parseErr)
		}
		room = room.Bare()
		connector.rooms[room.String()] = room
	}

	return connector, nil
}

func normalizeXMPPEndpoint(endpoint string) (string, error) {
	if strings.Contains(endpoint, "://") {
		return "", errors.New("endpoint must be host:port, without a URL scheme")
	}

	if host, port, err := net.SplitHostPort(endpoint); err == nil {
		portNumber, portErr := strconv.Atoi(port)
		if strings.TrimSpace(host) == "" || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("endpoint must contain a valid host and numeric port")
		}
		return net.JoinHostPort(host, port), nil
	}

	// A bare hostname is the common override. IPv6 literals must be bracketed
	// before a default port can be applied.
	if strings.Count(endpoint, ":") > 1 && !strings.HasPrefix(endpoint, "[") {
		return "", errors.New("IPv6 endpoint must be enclosed in brackets")
	}
	if strings.HasPrefix(endpoint, "[") && strings.HasSuffix(endpoint, "]") {
		return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(endpoint, "["), "]"), defaultXMPPPort), nil
	}
	if strings.Contains(endpoint, ":") {
		return "", errors.New("endpoint port is invalid")
	}
	return net.JoinHostPort(endpoint, defaultXMPPPort), nil
}

func (c *XMPPConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			c.publishStatus("connector offline")
			return
		}

		established := false
		err := c.connectAndRun(ctx, func() { established = true })
		if ctx.Err() != nil {
			c.publishStatus("connector offline")
			return
		}
		if err != nil {
			log.Printf("[xmpp:%s] session ended: %v", c.botName, err)
			c.publishStatus("xmpp session ended: " + err.Error())
		}

		// A session that actually came up clears the penalty from earlier
		// outages. Without this a bot that reconnected a few times on Monday
		// still waits the full ceiling to return after a blip on Friday.
		if established {
			backoff = time.Second
		}

		c.publishStatus("xmpp reconnecting...")
		if !sleepXMPPOrDone(ctx, backoff) {
			c.publishStatus("connector offline")
			return
		}
		if backoff < xmppReconnectLimit {
			backoff *= 2
			if backoff > xmppReconnectLimit {
				backoff = xmppReconnectLimit
			}
		}
	}
}

func (c *XMPPConnector) connectAndRun(ctx context.Context, onEstablished func()) error {
	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	session, err := c.dialSession(connectCtx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	c.mu.Lock()
	c.session = session
	c.identity = session.LocalAddr()
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if c.session == session {
			c.session = nil
		}
		c.mu.Unlock()
		_ = session.Close()
		if conn := session.Conn(); conn != nil {
			_ = conn.Close()
		}
	}()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
			if conn := session.Conn(); conn != nil {
				_ = conn.Close()
			}
		case <-done:
		}
	}()
	defer close(done)

	if err := session.Send(ctx, stanza.Presence{
		Type: stanza.AvailablePresence,
	}.Wrap(nil)); err != nil {
		return fmt.Errorf("send initial presence: %w", err)
	}

	if err := c.joinConfiguredRooms(ctx, session); err != nil {
		return err
	}

	log.Printf("[xmpp:%s] authenticated as %s", c.botName, c.Identity())
	c.publishStatus("connector online")
	if onEstablished != nil {
		onEstablished()
	}

	err = session.Serve(c.handler())
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve: %w", err)
	}
	return ctx.Err()
}

func (c *XMPPConnector) handler() xmpp.Handler {
	return &xmppConnectorHandler{
		connector: c,
		iq:        mux.New(stanza.NSClient, ping.Handle()),
	}
}

type xmppConnectorHandler struct {
	connector *XMPPConnector
	iq        *mux.ServeMux
}

func (h *xmppConnectorHandler) HandleXMPP(
	t xmlstream.TokenReadEncoder,
	start *xml.StartElement,
) error {
	if stanza.Is(start.Name, stanza.NSClient) && start.Name.Local == "iq" {
		return h.iq.HandleXMPP(t, start)
	}
	return h.connector.handleStanza(t, start)
}

func (c *XMPPConnector) dialSession(ctx context.Context) (*xmpp.Session, error) {
	features := []xmpp.StreamFeature{
		xmpp.BindResource(),
		xmpp.StartTLS(&tls.Config{
			ServerName: c.account.Domainpart(),
			MinVersion: tls.VersionTLS12,
		}),
		xmpp.SASL(
			"",
			c.password,
			sasl.ScramSha256Plus,
			sasl.ScramSha256,
			sasl.ScramSha1Plus,
			sasl.ScramSha1,
			sasl.Plain,
		),
	}

	if c.endpoint == "" {
		return xmpp.DialClientSession(ctx, c.account, features...)
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.endpoint, err)
	}

	session, err := xmpp.NewClientSession(ctx, c.account, conn, features...)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return session, nil
}

func (c *XMPPConnector) joinConfiguredRooms(ctx context.Context, session *xmpp.Session) error {
	c.mu.RLock()
	rooms := make([]jid.JID, 0, len(c.rooms))
	for _, room := range c.rooms {
		rooms = append(rooms, room)
	}
	nickname := c.nickname
	c.mu.RUnlock()

	for _, room := range rooms {
		roomWithNick, err := room.WithResource(nickname)
		if err != nil {
			return fmt.Errorf("set nickname for xmpp room %s: %w", room, err)
		}

		join := xmppMUCJoinPresence{
			Presence: stanza.Presence{
				To:   roomWithNick,
				Type: stanza.AvailablePresence,
			},
			MUC: xmppMUCJoin{
				History: xmppMUCHistory{MaxStanzas: 0},
			},
		}
		if err := session.Encode(ctx, join); err != nil {
			return fmt.Errorf("join xmpp room %s: %w", room, err)
		}
		log.Printf("[xmpp:%s] joining %s as %s", c.botName, room, nickname)
	}
	return nil
}

type xmppMUCJoinPresence struct {
	stanza.Presence
	MUC xmppMUCJoin `xml:"http://jabber.org/protocol/muc x"`
}

type xmppMUCJoin struct {
	History xmppMUCHistory `xml:"history"`
}

type xmppMUCHistory struct {
	MaxStanzas int `xml:"maxstanzas,attr"`
}

type xmppInboundMessage struct {
	stanza.Message
	Body      string    `xml:"body"`
	Thread    string    `xml:"thread"`
	Active    *struct{} `xml:"http://jabber.org/protocol/chatstates active"`
	Composing *struct{} `xml:"http://jabber.org/protocol/chatstates composing"`
	Paused    *struct{} `xml:"http://jabber.org/protocol/chatstates paused"`
	Inactive  *struct{} `xml:"http://jabber.org/protocol/chatstates inactive"`
	Gone      *struct{} `xml:"http://jabber.org/protocol/chatstates gone"`
	Delay     struct {
		Stamp string `xml:"stamp,attr"`
	} `xml:"urn:xmpp:delay delay"`
}

type xmppInboundPresence struct {
	stanza.Presence
	Show   string        `xml:"show"`
	Status string        `xml:"status"`
	Error  *stanza.Error `xml:"error"`
	MUC    struct {
		Statuses []struct {
			Code int `xml:"code,attr"`
		} `xml:"status"`
	} `xml:"http://jabber.org/protocol/muc#user x"`
}

// hasMUCStatus reports whether the room annotated this presence with a given
// XEP-0045 status code. Code 110 marks the occupant as the recipient itself,
// which is how a room confirms that a join actually completed.
func (p xmppInboundPresence) hasMUCStatus(code int) bool {
	for _, status := range p.MUC.Statuses {
		if status.Code == code {
			return true
		}
	}
	return false
}

func (c *XMPPConnector) handleStanza(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	switch start.Name.Local {
	case "message":
		var message xmppInboundMessage
		if err := xml.NewTokenDecoder(t).DecodeElement(&message, start); err != nil && err != io.EOF {
			return err
		}
		c.handleMessage(message)
	case "presence":
		var presence xmppInboundPresence
		if err := xml.NewTokenDecoder(t).DecodeElement(&presence, start); err != nil && err != io.EOF {
			return err
		}
		c.handlePresence(presence)
	}
	return nil
}

func (c *XMPPConnector) handleMessage(message xmppInboundMessage) {
	if message.Type != stanza.ChatMessage &&
		message.Type != stanza.GroupChatMessage &&
		message.Type != stanza.NormalMessage {
		return
	}

	from := message.From
	if from.Equal(jid.JID{}) {
		return
	}

	body := strings.TrimSpace(message.Body)
	state := message.chatState()
	if body == "" && state == "" {
		return
	}

	event, ok := c.routeInbound(message)
	if !ok {
		return
	}

	event.Timestamp = time.Now().UTC()
	if stamp := strings.TrimSpace(message.Delay.Stamp); stamp != "" {
		if delayedAt, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			event.Timestamp = delayedAt.UTC()
		}
	}
	event.Service = c.serviceName
	event.Bot = c.botName
	event.Direction = "in"
	event.Thread = strings.TrimSpace(message.Thread)

	if body != "" {
		event.Kind = "message"
		event.Text = body
		c.publish(event)
		return
	}

	event.Kind = "typing"
	event.Text = state
	c.publish(event)
}

func (m xmppInboundMessage) chatState() string {
	switch {
	case m.Composing != nil:
		return "composing"
	case m.Paused != nil:
		return "paused"
	case m.Inactive != nil:
		return "inactive"
	case m.Gone != nil:
		return "gone"
	case m.Active != nil:
		return "active"
	default:
		return ""
	}
}

func (c *XMPPConnector) routeInbound(message xmppInboundMessage) (protocol.Event, bool) {
	from := message.From
	if message.Type == stanza.GroupChatMessage {
		room := from.Bare()
		if !c.acceptsRoom(room) || from.Resourcepart() == "" || from.Resourcepart() == c.nickname {
			return protocol.Event{}, false
		}
		return protocol.Event{
			User:    from.Resourcepart(),
			Target:  "room:" + room.String(),
			Channel: room.String(),
		}, true
	}

	sender := from.Bare()
	if sender.Equal(c.account.Bare()) {
		return protocol.Event{}, false
	}
	return protocol.Event{
		User:    sender.String(),
		Target:  "dm:" + sender.String(),
		Channel: "dm:" + sender.String(),
	}, true
}

func (c *XMPPConnector) handlePresence(presence xmppInboundPresence) {
	if presence.From.Equal(jid.JID{}) {
		return
	}

	from := presence.From
	room := from.Bare()
	isRoom := c.acceptsRoom(room)

	// A MUC refuses a join by answering the join presence with an error -
	// members-only, banned, nickname already taken, room missing. Without this
	// the failure is invisible: the connector logs that it is joining, the
	// stanza arrives addressed to our own nickname and would be dropped as
	// self-presence below, and the room simply never delivers a message.
	if isRoom && presence.Type == stanza.ErrorPresence {
		reason := describeXMPPPresenceError(presence.Error)
		log.Printf("[xmpp:%s] cannot join %s: %s", c.botName, room, reason)
		c.publishStatus("xmpp room " + room.String() + " unavailable: " + reason)
		return
	}

	if isRoom && from.Resourcepart() == c.nickname {
		// Status code 110 marks the room's confirmation that this occupant is
		// us, so it is the point at which the join is known to have worked.
		if presence.Type == stanza.AvailablePresence && presence.hasMUCStatus(110) {
			log.Printf("[xmpp:%s] joined %s", c.botName, room)
			c.publishStatus("xmpp joined room " + room.String())
		}
		return
	}
	if !isRoom && room.Equal(c.account.Bare()) {
		return
	}

	state := string(presence.Type)
	if state == "" {
		state = strings.TrimSpace(presence.Show)
	}
	if state == "" {
		state = "available"
	}
	if status := strings.TrimSpace(presence.Status); status != "" {
		state += ": " + status
	}

	event := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "presence",
		Direction: "in",
		Text:      state,
	}
	if isRoom {
		event.User = from.Resourcepart()
		event.Target = "room:" + room.String()
		event.Channel = room.String()
	} else {
		event.User = room.String()
		event.Target = "dm:" + room.String()
		event.Channel = "dm:" + room.String()
	}
	c.publish(event)
}

type xmppOutboundMessage struct {
	stanza.Message
	Body   string         `xml:"body,omitempty"`
	Thread string         `xml:"thread,omitempty"`
	State  *xmppChatState `xml:",omitempty"`
}

type xmppChatState struct {
	XMLName xml.Name
}

func (c *XMPPConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	segments, err := prepareXMPPSegments(request.Format, request.Text)
	if err != nil {
		return protocol.Event{}, err
	}
	if len(segments) == 0 {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	destination, group, channel, target, err := c.resolveDestination(request)
	if err != nil {
		return protocol.Event{}, err
	}

	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return protocol.Event{}, fmt.Errorf("xmpp client not connected")
	}

	messageType := stanza.ChatMessage
	if group {
		messageType = stanza.GroupChatMessage
	}

	var lastEvent protocol.Event
	for _, segment := range segments {
		outbound := xmppOutboundMessage{
			Message: stanza.Message{
				To:   destination,
				Type: messageType,
			},
			Body:   segment,
			Thread: strings.TrimSpace(request.Thread),
			State: &xmppChatState{
				XMLName: xml.Name{Space: xmppChatStatesNS, Local: "active"},
			},
		}
		if err := session.Encode(ctx, outbound); err != nil {
			return protocol.Event{}, fmt.Errorf("xmpp send: %w", err)
		}

		event := protocol.Event{
			Timestamp: time.Now().UTC(),
			Service:   c.serviceName,
			Bot:       c.botName,
			Kind:      "message",
			Direction: "out",
			User:      c.Identity(),
			Target:    target,
			Channel:   channel,
			Thread:    strings.TrimSpace(request.Thread),
			Text:      segment,
		}
		c.publish(event)
		lastEvent = event
	}
	return lastEvent, nil
}

func (c *XMPPConnector) Typing(ctx context.Context, request protocol.Request) error {
	destination, group, _, _, err := c.resolveDestination(request)
	if err != nil {
		return err
	}

	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("xmpp client not connected")
	}

	state := "composing"
	if request.Stop {
		state = "paused"
	}
	messageType := stanza.ChatMessage
	if group {
		messageType = stanza.GroupChatMessage
	}
	return session.Encode(ctx, xmppOutboundMessage{
		Message: stanza.Message{
			To:   destination,
			Type: messageType,
		},
		State: &xmppChatState{
			XMLName: xml.Name{Space: xmppChatStatesNS, Local: state},
		},
	})
}

func (c *XMPPConnector) resolveDestination(request protocol.Request) (destination jid.JID, group bool, channel string, target string, err error) {
	raw := strings.TrimSpace(request.Channel)
	if raw != "" {
		group = true
	} else {
		raw = strings.TrimSpace(request.Target)
	}
	if raw == "" {
		return jid.JID{}, false, "", "", fmt.Errorf("xmpp send requires channel or target")
	}

	switch {
	case strings.HasPrefix(raw, "xmpp:dm:"):
		raw = strings.TrimPrefix(raw, "xmpp:dm:")
		group = false
	case strings.HasPrefix(raw, "dm:"):
		raw = strings.TrimPrefix(raw, "dm:")
		group = false
	case strings.HasPrefix(raw, "xmpp:room:"):
		raw = strings.TrimPrefix(raw, "xmpp:room:")
		group = true
	case strings.HasPrefix(raw, "room:"):
		raw = strings.TrimPrefix(raw, "room:")
		group = true
	case strings.HasPrefix(raw, "channel:"):
		raw = strings.TrimPrefix(raw, "channel:")
		group = true
	case strings.HasPrefix(raw, "xmpp:"):
		raw = strings.TrimPrefix(raw, "xmpp:")
	}

	destination, err = jid.Parse(strings.TrimSpace(raw))
	if err != nil || destination.Localpart() == "" {
		if err == nil {
			err = errors.New("destination JID must include a localpart")
		}
		return jid.JID{}, false, "", "", fmt.Errorf("parse xmpp destination %q: %w", raw, err)
	}
	destination = destination.Bare()

	// A raw configured room JID is unambiguously a groupchat destination.
	if !group && c.acceptsRoom(destination) {
		group = true
	}

	if group {
		channel = destination.String()
		target = "room:" + channel
	} else {
		channel = "dm:" + destination.String()
		target = channel
	}
	return destination, group, channel, target, nil
}

func (c *XMPPConnector) Identity() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.identity.String()
}

func (c *XMPPConnector) acceptsRoom(room jid.JID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.rooms[room.Bare().String()]
	return ok
}

func (c *XMPPConnector) publishStatus(text string) {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

// describeXMPPPresenceError renders a stanza error for an operator. Servers are
// not obliged to send a condition or text, so an unhelpful stanza still yields
// something better than an empty reason.
func describeXMPPPresenceError(stanzaError *stanza.Error) string {
	if stanzaError == nil {
		return "the server rejected the join without giving a reason"
	}
	if described := strings.TrimSpace(stanzaError.Error()); described != "" {
		return described
	}
	if errorType := strings.TrimSpace(string(stanzaError.Type)); errorType != "" {
		return errorType
	}
	return "the server rejected the join without giving a reason"
}

func sleepXMPPOrDone(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func prepareXMPPSegments(format string, text string) ([]string, error) {
	normalizedFormat, err := formatting.NormalizeFormat(format)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	switch normalizedFormat {
	case formatting.FormatMarkdown:
		trimmed = formatting.MarkdownToPlain(trimmed)
	case formatting.FormatHTML:
		trimmed = formatting.StripHTML(trimmed)
	}

	// XMPP itself does not impose a small message limit. A conservative chunk
	// keeps stanzas manageable for servers and bridges with downstream limits.
	return formatting.SplitText(trimmed, 8000), nil
}

// React is not supported by the XMPP connector.
func (c *XMPPConnector) React(_ context.Context, _ protocol.Request) error {
	return fmt.Errorf("reactions are not supported by the xmpp connector")
}
