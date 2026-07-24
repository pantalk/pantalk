package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultTwitchEndpoint = "irc.chat.twitch.tv:6697"

// Twitch allows an account with no elevated role 20 chat messages per 30
// seconds and answers a breach with a 30-minute chat ban. prepareIRCSegments
// emits one PRIVMSG per line, so an ordinary multi-line agent reply reaches
// that ceiling easily and must be paced rather than written at once.
const (
	twitchSendLimit  = 20
	twitchSendWindow = 30 * time.Second
)

var errTwitchReconnect = errors.New("Twitch requested reconnect")

// twitchRateLimiter spreads outbound messages across a sliding window. The
// budget is shared across channels rather than tracked per channel, because
// Twitch also enforces account-wide ceilings and the cost of being slightly
// conservative is far below the cost of a ban.
type twitchRateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	sent   []time.Time
	now    func() time.Time
}

func newTwitchRateLimiter(limit int, window time.Duration) *twitchRateLimiter {
	return &twitchRateLimiter{limit: limit, window: window, now: time.Now}
}

// reserve claims the next send slot and reports how long the caller must wait
// before using it. Slots are handed out in call order, so concurrent sends
// queue behind each other instead of racing to the same instant.
func (l *twitchRateLimiter) reserve() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	cutoff := now.Add(-l.window)
	kept := l.sent[:0]
	for _, at := range l.sent {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	l.sent = kept

	at := now
	if len(l.sent) >= l.limit {
		// This send may go only once the limit-th most recent one has aged out
		// of the window.
		if ready := l.sent[len(l.sent)-l.limit].Add(l.window); ready.After(at) {
			at = ready
		}
	}
	l.sent = append(l.sent, at)

	return at.Sub(now)
}

// TwitchConnector connects Pantalk to Twitch chat's IRC-over-TLS interface.
// Twitch login names are case-insensitive, so the configured username and all
// configured channels are kept in their canonical lower-case form.
type TwitchConnector struct {
	serviceName string
	botName     string
	login       string
	endpoint    string
	token       string
	publish     func(protocol.Event)

	limiter *twitchRateLimiter

	mu        sync.RWMutex
	writeMu   sync.Mutex
	channels  map[string]struct{}
	conn      net.Conn
	botUserID string
}

type twitchIRCMessage struct {
	Tags    map[string]string
	Prefix  string
	Command string
	Params  []string
}

func NewTwitchConnector(bot config.BotConfig, publish func(protocol.Event)) (*TwitchConnector, error) {
	login := strings.ToLower(strings.TrimSpace(bot.Username))
	if login == "" {
		return nil, fmt.Errorf("bot %q requires username for twitch", bot.Name)
	}

	credential := strings.TrimSpace(bot.AccessToken)
	if credential == "" {
		return nil, fmt.Errorf("bot %q requires access_token for twitch", bot.Name)
	}

	token, err := config.ResolveCredential(credential)
	if err != nil {
		return nil, fmt.Errorf("resolve twitch token for bot %q: %w", bot.Name, err)
	}
	token = strings.TrimSpace(token)
	if strings.HasPrefix(strings.ToLower(token), "oauth:") {
		token = token[len("oauth:"):]
	}
	if token == "" {
		return nil, fmt.Errorf("bot %q requires a non-empty twitch access token", bot.Name)
	}

	endpoint := strings.TrimSpace(bot.Endpoint)
	if endpoint == "" {
		endpoint = defaultTwitchEndpoint
	} else if _, _, splitErr := net.SplitHostPort(endpoint); splitErr != nil {
		endpoint = net.JoinHostPort(endpoint, "6697")
	}

	connector := &TwitchConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		login:       login,
		endpoint:    endpoint,
		token:       token,
		publish:     publish,
		limiter:     newTwitchRateLimiter(twitchSendLimit, twitchSendWindow),
		channels:    make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		if channel = normalizeTwitchChannel(channel); channel != "" {
			connector.channels[channel] = struct{}{}
		}
	}
	if len(connector.channels) == 0 {
		return nil, fmt.Errorf("bot %q requires at least one channel for twitch", bot.Name)
	}

	return connector, nil
}

func (c *TwitchConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			c.publishStatus("connector offline")
			return
		default:
		}

		established := false
		err := c.connectAndRun(ctx, func() { established = true })
		if ctx.Err() != nil {
			c.publishStatus("connector offline")
			return
		}
		if err != nil {
			log.Printf("[twitch:%s] connection error: %v", c.botName, err)
			c.publishStatus("twitch connection error: " + err.Error())
		}

		// A connection that reached the chat server clears the penalty from
		// earlier outages, so a long-lived bot reconnects promptly.
		if established {
			backoff = time.Second
		}

		c.sleepOrDone(ctx, backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *TwitchConnector) connectAndRun(ctx context.Context, onEstablished func()) error {
	host, _, err := net.SplitHostPort(c.endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", c.endpoint, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.botUserID = ""
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	if err := c.writeRegistration(); err != nil {
		return err
	}

	log.Printf("[twitch:%s] connected to %s", c.botName, c.endpoint)
	c.publishStatus("connector online")
	if onEstablished != nil {
		onEstablished()
	}

	return c.readLoop(ctx, conn)
}

func (c *TwitchConnector) writeRegistration() error {
	// Twitch requires PASS before NICK. Capabilities provide tags (including
	// message IDs), Twitch commands, and membership events.
	for _, line := range []string{
		"CAP REQ :twitch.tv/membership twitch.tv/tags twitch.tv/commands",
		"PASS oauth:" + c.token,
		"NICK " + c.login,
	} {
		if err := c.sendRaw(line); err != nil {
			return fmt.Errorf("register twitch connection: %w", err)
		}
	}
	return nil
}

func (c *TwitchConnector) readLoop(ctx context.Context, conn net.Conn) error {
	scanner := bufio.NewScanner(conn)
	// Twitch chat messages are normally far below this, but a larger bound
	// avoids disconnecting on a future tag expansion.
	scanner.Buffer(make([]byte, 4096), 64*1024)

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for scanner.Scan() {
		if err := c.handleLine(scanner.Text()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.EOF
}

func (c *TwitchConnector) handleLine(line string) error {
	message := parseTwitchIRCMessage(line)

	switch message.Command {
	case "PING":
		payload := ""
		if len(message.Params) > 0 {
			payload = message.Params[0]
		}
		if payload == "" {
			return c.sendRaw("PONG")
		}
		return c.sendRaw("PONG :" + strings.TrimPrefix(payload, ":"))

	case "001":
		return c.joinChannels()

	case "GLOBALUSERSTATE":
		if userID := strings.TrimSpace(message.Tags["user-id"]); userID != "" {
			c.mu.Lock()
			c.botUserID = userID
			c.mu.Unlock()
		}

	case "PRIVMSG":
		c.handlePrivmsg(message)

	case "RECONNECT":
		return errTwitchReconnect

	case "CAP":
		if len(message.Params) >= 2 && strings.EqualFold(message.Params[1], "NAK") {
			return fmt.Errorf("twitch rejected requested IRC capabilities")
		}

	case "NOTICE":
		text := ""
		if len(message.Params) > 1 {
			text = message.Params[len(message.Params)-1]
		}
		if strings.EqualFold(text, "Login authentication failed") ||
			strings.EqualFold(text, "Improperly formatted auth") {
			return fmt.Errorf("twitch authentication failed: %s", text)
		}

	case "PART":
		if len(message.Params) > 0 &&
			strings.EqualFold(extractNick(message.Prefix), c.login) &&
			c.acceptsChannel(message.Params[0]) {
			return c.sendRaw("JOIN " + normalizeTwitchChannel(message.Params[0]))
		}
	}

	return nil
}

func (c *TwitchConnector) handlePrivmsg(message twitchIRCMessage) {
	if len(message.Params) < 2 {
		return
	}

	login := strings.ToLower(strings.TrimSpace(extractNick(message.Prefix)))
	userID := strings.TrimSpace(message.Tags["user-id"])

	c.mu.RLock()
	botUserID := c.botUserID
	c.mu.RUnlock()

	// Twitch does not normally echo a bot's own PRIVMSG, but checking both
	// login and user ID prevents loops with proxies and test IRC servers.
	if strings.EqualFold(login, c.login) ||
		(botUserID != "" && userID != "" && botUserID == userID) {
		return
	}

	channel := normalizeTwitchChannel(message.Params[0])
	if channel == "" || !c.acceptsChannel(channel) {
		return
	}

	text := strings.TrimSpace(message.Params[1])
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		text = "/me " + strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
	}

	// Prefer Twitch's immutable user ID for session isolation. Display names
	// and login names can change, while Pantalk's Event.User is also part of
	// the durable conversation key.
	user := userID
	if user == "" {
		user = login
	}

	c.publish(protocol.Event{
		Timestamp: twitchMessageTimestamp(message.Tags),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "in",
		User:      user,
		Target:    "channel:" + channel,
		Channel:   channel,
		// Pantalk's event schema has no provider message-ID field. Thread is
		// the existing reply-routing handle: top-level messages use their own
		// ID, while replies retain Twitch's root thread ID.
		Thread: twitchThreadID(message.Tags),
		Text:   text,
	})
}

func (c *TwitchConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	segments, err := prepareIRCSegments(request.Format, request.Text)
	if err != nil {
		return protocol.Event{}, err
	}
	if len(segments) == 0 {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	if isTwitchDirectTarget(request.Channel) || isTwitchDirectTarget(request.Target) {
		return protocol.Event{}, fmt.Errorf("twitch chat does not support direct messages")
	}

	channel := resolveTwitchChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("twitch send requires channel or target")
	}

	if c.rememberChannel(channel) {
		if err := c.sendRaw("JOIN " + channel); err != nil {
			return protocol.Event{}, err
		}
	}

	var lastEvent protocol.Event
	for _, segment := range segments {
		if err := c.awaitSendSlot(ctx); err != nil {
			return protocol.Event{}, err
		}

		command := "PRIVMSG " + channel + " :" + segment
		if thread := strings.TrimSpace(request.Thread); thread != "" {
			command = "@reply-parent-msg-id=" + escapeTwitchTag(thread) + " " + command
		}
		if err := c.sendRaw(command); err != nil {
			return protocol.Event{}, fmt.Errorf("send twitch message: %w", err)
		}

		target := strings.TrimSpace(request.Target)
		if target == "" {
			target = "channel:" + channel
		}
		lastEvent = protocol.Event{
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
		c.publish(lastEvent)
	}

	return lastEvent, nil
}

func (c *TwitchConnector) Identity() string {
	return c.login
}

// React is not supported by Twitch's IRC chat interface.
func (c *TwitchConnector) React(_ context.Context, _ protocol.Request) error {
	return fmt.Errorf("reactions are not supported by the twitch connector")
}

// awaitSendSlot blocks until the rate limiter releases the next message. A
// long agent reply therefore trickles out over several windows instead of
// tripping Twitch's chat ban.
func (c *TwitchConnector) awaitSendSlot(ctx context.Context) error {
	wait := c.limiter.reserve()
	if wait <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	log.Printf("[twitch:%s] rate limit reached, holding next message for %s", c.botName, wait.Round(time.Second))

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *TwitchConnector) sendRaw(line string) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("twitch connector is not connected")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(conn, "%s\r\n", sanitizeIRCLine(line)); err != nil {
		return err
	}
	return nil
}

func (c *TwitchConnector) joinChannels() error {
	c.mu.RLock()
	channels := make([]string, 0, len(c.channels))
	for channel := range c.channels {
		channels = append(channels, channel)
	}
	c.mu.RUnlock()
	sort.Strings(channels)

	for _, channel := range channels {
		if err := c.sendRaw("JOIN " + channel); err != nil {
			return err
		}
	}
	return nil
}

func (c *TwitchConnector) rememberChannel(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.channels[channel]; exists {
		return false
	}
	c.channels[channel] = struct{}{}
	return true
}

// acceptsChannel reports whether inbound traffic on a channel should be
// forwarded. The constructor rejects a bot with no channels, so an empty set is
// unreachable and is treated as accepting nothing rather than everything.
func (c *TwitchConnector) acceptsChannel(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.channels[normalizeTwitchChannel(channel)]
	return ok
}

func (c *TwitchConnector) publishStatus(text string) {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (c *TwitchConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func parseTwitchIRCMessage(line string) twitchIRCMessage {
	line = strings.TrimRight(line, "\r\n")
	message := twitchIRCMessage{Tags: make(map[string]string)}

	if strings.HasPrefix(line, "@") {
		if separator := strings.IndexByte(line, ' '); separator >= 0 {
			message.Tags = parseTwitchTags(line[1:separator])
			line = strings.TrimLeft(line[separator+1:], " ")
		}
	}

	message.Prefix, message.Command, message.Params = parseIRCMessage(line)
	message.Command = strings.ToUpper(message.Command)
	return message
}

func parseTwitchTags(raw string) map[string]string {
	tags := make(map[string]string)
	for _, pair := range strings.Split(raw, ";") {
		key, value, found := strings.Cut(pair, "=")
		if key == "" {
			continue
		}
		if !found {
			tags[key] = ""
			continue
		}
		tags[key] = unescapeTwitchTag(value)
	}
	return tags
}

func unescapeTwitchTag(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	escaped := false
	for _, char := range value {
		if !escaped {
			if char == '\\' {
				escaped = true
			} else {
				result.WriteRune(char)
			}
			continue
		}

		switch char {
		case 's':
			result.WriteByte(' ')
		case ':':
			result.WriteByte(';')
		case 'r':
			result.WriteByte('\r')
		case 'n':
			result.WriteByte('\n')
		case '\\':
			result.WriteByte('\\')
		default:
			result.WriteRune(char)
		}
		escaped = false
	}
	if escaped {
		result.WriteByte('\\')
	}
	return result.String()
}

func escapeTwitchTag(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\:",
		" ", "\\s",
		"\r", "\\r",
		"\n", "\\n",
	)
	return replacer.Replace(value)
}

func twitchMessageTimestamp(tags map[string]string) time.Time {
	if millis, err := strconv.ParseInt(tags["tmi-sent-ts"], 10, 64); err == nil && millis > 0 {
		return time.UnixMilli(millis).UTC()
	}
	return time.Now().UTC()
}

// twitchThreadID returns the root of a reply chain, or empty for an ordinary
// top-level message.
//
// Filling this in for every message would look harmless - it is the handle
// `reply-parent-msg-id` wants - but agent.ConversationKey scopes a conversation
// by thread whenever Thread is set, so a unique per-message value would start a
// fresh harness session for every line of chat. Discord and Slack set Thread
// only for genuine replies for the same reason.
func twitchThreadID(tags map[string]string) string {
	if rootID := strings.TrimSpace(tags["reply-thread-parent-msg-id"]); rootID != "" {
		return rootID
	}
	return strings.TrimSpace(tags["reply-parent-msg-id"])
}

func normalizeTwitchChannel(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	channel = strings.TrimPrefix(channel, "twitch:channel:")
	channel = strings.TrimPrefix(channel, "channel:")
	channel = strings.TrimPrefix(channel, "twitch:")
	channel = strings.TrimPrefix(channel, "#")
	if channel == "" {
		return ""
	}
	return "#" + channel
}

func resolveTwitchChannel(request protocol.Request) string {
	if channel := normalizeTwitchChannel(request.Channel); channel != "" {
		return channel
	}
	return normalizeTwitchChannel(request.Target)
}

func isTwitchDirectTarget(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "dm:") ||
		strings.HasPrefix(value, "twitch:dm:")
}
