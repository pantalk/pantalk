package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultIRCPort = "6667"
const defaultIRCSPort = "6697"

type IRCConnector struct {
	serviceName string
	botName     string
	nick        string
	realname    string
	endpoint    string
	password    string
	useTLS      bool
	publish     func(protocol.Event)

	mu       sync.RWMutex
	channels map[string]struct{}
	conn     net.Conn
}

func NewIRCConnector(bot config.BotConfig, publish func(protocol.Event)) (*IRCConnector, error) {
	endpoint := strings.TrimSpace(bot.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("bot %q requires endpoint for irc", bot.Name)
	}

	nick := bot.Name
	realname := bot.DisplayName
	if realname == "" {
		realname = nick
	}

	// Resolve optional server password.
	var password string
	if strings.TrimSpace(bot.Password) != "" {
		resolved, err := config.ResolveCredential(bot.Password)
		if err != nil {
			return nil, fmt.Errorf("resolve irc password for bot %q: %w", bot.Name, err)
		}
		password = resolved
	}

	// Determine TLS usage from port. Default to TLS on port 6697.
	useTLS := false
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		// No port specified — add default TLS port.
		endpoint = endpoint + ":" + defaultIRCSPort
		useTLS = true
	} else if port == defaultIRCSPort {
		useTLS = true
	}

	connector := &IRCConnector{
		serviceName: bot.Type,
		botName:     bot.Name,
		nick:        nick,
		realname:    realname,
		endpoint:    endpoint,
		password:    password,
		useTLS:      useTLS,
		publish:     publish,
		channels:    make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "&") {
			trimmed = "#" + trimmed
		}
		connector.channels[trimmed] = struct{}{}
	}

	return connector, nil
}

func (c *IRCConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			c.publishStatus("connector offline")
			return
		default:
		}

		if err := c.connectAndRun(ctx); err != nil {
			log.Printf("[irc:%s] connection error: %v", c.botName, err)
			c.publishStatus("irc connection error: " + err.Error())
			c.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
	}
}

func (c *IRCConnector) connectAndRun(ctx context.Context) error {
	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: 15 * time.Second}

	if c.useTLS {
		host, _, _ := net.SplitHostPort(c.endpoint)
		conn, err = tls.DialWithDialer(dialer, "tcp", c.endpoint, &tls.Config{
			ServerName: host,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", c.endpoint)
	}
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	// Register with the server.
	if c.password != "" {
		c.sendRaw("PASS " + c.password)
	}
	c.sendRaw("NICK " + c.nick)
	c.sendRaw("USER " + c.nick + " 0 * :" + c.realname)

	log.Printf("[irc:%s] connected to %s", c.botName, c.endpoint)
	c.publishStatus("connector online")

	return c.readLoop(ctx)
}

func (c *IRCConnector) readLoop(ctx context.Context) error {
	scanner := bufio.NewScanner(c.conn)

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.sendRaw("QUIT :shutting down")
			c.conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for scanner.Scan() {
		line := scanner.Text()
		c.handleLine(line)
	}

	return scanner.Err()
}

func (c *IRCConnector) handleLine(line string) {
	// Handle PING/PONG keepalive.
	if strings.HasPrefix(line, "PING") {
		payload := strings.TrimPrefix(line, "PING ")
		c.sendRaw("PONG " + payload)
		return
	}

	// Parse IRC message: :<prefix> <command> <params>
	prefix, command, params := parseIRCMessage(line)

	switch command {
	case "001": // RPL_WELCOME — registration complete, join channels.
		c.joinChannels()

	case "PRIVMSG":
		c.handlePrivmsg(prefix, params)

	case "NOTICE":
		// Notices are logged but not published as messages.

	case "JOIN":
		if nick := extractNick(prefix); nick == c.nick {
			channel := ""
			if len(params) > 0 {
				channel = strings.TrimPrefix(params[0], ":")
			}
			log.Printf("[irc:%s] joined %s", c.botName, channel)
		}

	case "KICK":
		if len(params) >= 2 && params[1] == c.nick {
			channel := params[0]
			log.Printf("[irc:%s] kicked from %s, rejoining", c.botName, channel)
			c.sendRaw("JOIN " + channel)
		}

	case "433": // ERR_NICKNAMEINUSE
		log.Printf("[irc:%s] nick %q in use, trying %s_", c.botName, c.nick, c.nick)
		c.nick = c.nick + "_"
		c.sendRaw("NICK " + c.nick)
	}
}

func (c *IRCConnector) handlePrivmsg(prefix string, params []string) {
	if len(params) < 2 {
		return
	}

	sender := extractNick(prefix)
	if sender == c.nick {
		return
	}

	target := params[0]
	text := strings.TrimSpace(strings.TrimPrefix(params[1], ":"))

	// Determine if this is a channel message or DM.
	channel := target
	isDirect := false
	if !strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "&") {
		// Direct message — target is our nick.
		channel = "dm:" + sender
		isDirect = true
	}

	if !isDirect && !c.acceptsChannel(channel) {
		return
	}

	eventTarget := channel
	if isDirect {
		eventTarget = "dm:" + sender
	} else {
		eventTarget = "channel:" + channel
	}

	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "in",
		User:      sender,
		Target:    eventTarget,
		Channel:   channel,
		Text:      text,
	})
}

func (c *IRCConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	channel := resolveIRCChannel(request)
	if channel == "" {
		return protocol.Event{}, fmt.Errorf("irc send requires channel or target")
	}
	c.rememberChannel(channel)

	c.sendRaw("PRIVMSG " + channel + " :" + text)

	target := request.Target
	if target == "" {
		target = "channel:" + channel
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
		Text:      text,
	}
	c.publish(event)

	return event, nil
}

func (c *IRCConnector) Identity() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nick
}

func (c *IRCConnector) sendRaw(line string) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return
	}

	if _, err := fmt.Fprintf(conn, "%s\r\n", line); err != nil {
		log.Printf("[irc:%s] send error: %v", c.botName, err)
	}
}

func (c *IRCConnector) joinChannels() {
	c.mu.RLock()
	channels := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		channels = append(channels, ch)
	}
	c.mu.RUnlock()

	for _, ch := range channels {
		c.sendRaw("JOIN " + ch)
	}
}

func (c *IRCConnector) rememberChannel(channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels[channel] = struct{}{}
}

func (c *IRCConnector) acceptsChannel(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.channels) == 0 {
		return true
	}
	_, ok := c.channels[channel]
	return ok
}

func (c *IRCConnector) publishStatus(text string) {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (c *IRCConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// parseIRCMessage splits a raw IRC line into prefix, command, and params.
func parseIRCMessage(line string) (prefix, command string, params []string) {
	line = strings.TrimRight(line, "\r\n")

	if strings.HasPrefix(line, ":") {
		idx := strings.Index(line, " ")
		if idx < 0 {
			return line[1:], "", nil
		}
		prefix = line[1:idx]
		line = line[idx+1:]
	}

	// Split remaining into command and params.
	if idx := strings.Index(line, " :"); idx >= 0 {
		trailing := line[idx+2:]
		parts := strings.Fields(line[:idx])
		if len(parts) > 0 {
			command = parts[0]
			params = append(parts[1:], trailing)
		}
	} else {
		parts := strings.Fields(line)
		if len(parts) > 0 {
			command = parts[0]
			params = parts[1:]
		}
	}

	return prefix, command, params
}

// extractNick returns the nickname from an IRC prefix (nick!user@host).
func extractNick(prefix string) string {
	if idx := strings.Index(prefix, "!"); idx >= 0 {
		return prefix[:idx]
	}
	return prefix
}

func resolveIRCChannel(request protocol.Request) string {
	if request.Channel != "" {
		ch := request.Channel
		if !strings.HasPrefix(ch, "#") && !strings.HasPrefix(ch, "&") && !strings.HasPrefix(ch, "dm:") {
			ch = "#" + ch
		}
		return ch
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	// Check DM prefixes first (before generic irc: prefix).
	for _, prefix := range []string{"irc:dm:", "dm:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	for _, prefix := range []string{"irc:channel:", "channel:", "irc:"} {
		if strings.HasPrefix(target, prefix) {
			ch := strings.TrimPrefix(target, prefix)
			if !strings.HasPrefix(ch, "#") && !strings.HasPrefix(ch, "&") {
				ch = "#" + ch
			}
			return ch
		}
	}

	return target
}
