package upstream

import (
	"bufio"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

func TestNewTwitchConnector(t *testing.T) {
	t.Setenv("PANTALK_TEST_TWITCH_TOKEN", "oauth:secret-token")

	connector, err := NewTwitchConnector(config.BotConfig{
		Name:        "stream-helper",
		Type:        "twitch",
		Username:    "My_Bot",
		AccessToken: "$PANTALK_TEST_TWITCH_TOKEN",
		Channels:    []string{"Pantalk", "#OpenAI", " "},
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatalf("NewTwitchConnector() error = %v", err)
	}

	if connector.login != "my_bot" {
		t.Fatalf("login = %q, want my_bot", connector.login)
	}
	if connector.token != "secret-token" {
		t.Fatalf("token = %q, want OAuth prefix removed", connector.token)
	}
	if connector.endpoint != defaultTwitchEndpoint {
		t.Fatalf("endpoint = %q, want %q", connector.endpoint, defaultTwitchEndpoint)
	}
	if !connector.acceptsChannel("#pantalk") || !connector.acceptsChannel("#openai") {
		t.Fatalf("normalized channels = %#v", connector.channels)
	}
}

func TestNewTwitchConnectorRequiresToken(t *testing.T) {
	_, err := NewTwitchConnector(config.BotConfig{
		Name:     "stream-helper",
		Type:     "twitch",
		Username: "my_bot",
		Channels: []string{"pantalk"},
	}, func(protocol.Event) {})
	if err == nil || !strings.Contains(err.Error(), "requires access_token") {
		t.Fatalf("error = %v, want missing access_token error", err)
	}
}

func TestNewTwitchConnectorRequiresChannel(t *testing.T) {
	_, err := NewTwitchConnector(config.BotConfig{
		Name:        "stream-helper",
		Type:        "twitch",
		Username:    "my_bot",
		AccessToken: "test-token",
		Channels:    []string{" ", "#"},
	}, func(protocol.Event) {})
	if err == nil || !strings.Contains(err.Error(), "at least one channel") {
		t.Fatalf("error = %v, want missing channel error", err)
	}
}

func TestTwitchRegistrationOrder(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	server, client := net.Pipe()
	connector.conn = client
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- connector.writeRegistration()
	}()

	scanner := bufio.NewScanner(server)
	var lines []string
	for range 3 {
		if !scanner.Scan() {
			t.Fatalf("registration line missing: %v", scanner.Err())
		}
		lines = append(lines, scanner.Text())
	}
	if err := <-errCh; err != nil {
		t.Fatalf("writeRegistration() error = %v", err)
	}

	want := []string{
		"CAP REQ :twitch.tv/membership twitch.tv/tags twitch.tv/commands",
		"PASS oauth:test-token",
		"NICK pantalkbot",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("registration = %#v, want %#v", lines, want)
	}
}

func TestTwitchHandlePingAndJoin(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	server, client := net.Pipe()
	connector.conn = client
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	errCh := make(chan error, 1)
	go func() {
		if err := connector.handleLine("PING :tmi.twitch.tv"); err != nil {
			errCh <- err
			return
		}
		errCh <- connector.handleLine(":tmi.twitch.tv 001 pantalkbot :Welcome")
	}()

	scanner := bufio.NewScanner(server)
	var lines []string
	for range 3 {
		if !scanner.Scan() {
			t.Fatalf("IRC response missing: %v", scanner.Err())
		}
		lines = append(lines, scanner.Text())
	}
	if err := <-errCh; err != nil {
		t.Fatalf("handleLine() error = %v", err)
	}

	want := []string{"PONG :tmi.twitch.tv", "JOIN #openai", "JOIN #pantalk"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("responses = %#v, want %#v", lines, want)
	}
}

func TestTwitchInboundPrivmsgUsesTagsAndMessageID(t *testing.T) {
	var published []protocol.Event
	connector := newTestTwitchConnector(t, func(event protocol.Event) {
		published = append(published, event)
	})

	line := "@display-name=Space\\sCadet;id=message-123;room-id=42;tmi-sent-ts=1710000000123;user-id=7 " +
		":spacecadet!spacecadet@spacecadet.tmi.twitch.tv PRIVMSG #Pantalk :hello chat"
	if err := connector.handleLine(line); err != nil {
		t.Fatalf("handleLine() error = %v", err)
	}

	if len(published) != 1 {
		t.Fatalf("published %d events, want 1", len(published))
	}
	event := published[0]
	if event.User != "7" {
		t.Errorf("User = %q, want stable Twitch user ID", event.User)
	}
	// A top-level message carries no thread, so every line of chat in the
	// channel shares one conversation instead of starting a new session.
	if event.Thread != "" {
		t.Errorf("Thread = %q, want empty for a top-level message", event.Thread)
	}
	if event.Channel != "#pantalk" || event.Target != "channel:#pantalk" {
		t.Errorf("Channel/Target = %q/%q", event.Channel, event.Target)
	}
	if event.Text != "hello chat" {
		t.Errorf("Text = %q, want hello chat", event.Text)
	}
	if got := event.Timestamp.UnixMilli(); got != 1710000000123 {
		t.Errorf("Timestamp = %d, want 1710000000123", got)
	}
}

func TestTwitchInboundReplyUsesRootThreadID(t *testing.T) {
	var published []protocol.Event
	connector := newTestTwitchConnector(t, func(event protocol.Event) {
		published = append(published, event)
	})

	line := "@display-name=Viewer;id=reply-message;reply-parent-msg-id=direct-parent;" +
		"reply-thread-parent-msg-id=root-message;user-id=7 " +
		":viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #pantalk :nested reply"
	if err := connector.handleLine(line); err != nil {
		t.Fatalf("handleLine() error = %v", err)
	}

	if len(published) != 1 {
		t.Fatalf("published %d events, want 1", len(published))
	}
	if got := published[0].Thread; got != "root-message" {
		t.Fatalf("Thread = %q, want root-message", got)
	}
}

func TestTwitchFiltersOwnMessagesByLoginAndUserID(t *testing.T) {
	var published []protocol.Event
	connector := newTestTwitchConnector(t, func(event protocol.Event) {
		published = append(published, event)
	})

	if err := connector.handleLine("@user-id=99 :tmi.twitch.tv GLOBALUSERSTATE"); err != nil {
		t.Fatalf("GLOBALUSERSTATE error = %v", err)
	}
	for _, line := range []string{
		"@display-name=PantalkBot;id=one;user-id=99 :pantalkbot!u@h PRIVMSG #pantalk :by login",
		"@display-name=Renamed;id=two;user-id=99 :renamed!u@h PRIVMSG #pantalk :by id",
	} {
		if err := connector.handleLine(line); err != nil {
			t.Fatalf("PRIVMSG error = %v", err)
		}
	}

	if len(published) != 0 {
		t.Fatalf("published own messages: %#v", published)
	}
}

func TestTwitchSendReply(t *testing.T) {
	var mu sync.Mutex
	var published []protocol.Event
	connector := newTestTwitchConnector(t, func(event protocol.Event) {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
	})
	server, client := net.Pipe()
	connector.conn = client
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	type sendResult struct {
		event protocol.Event
		err   error
	}
	resultCh := make(chan sendResult, 1)
	go func() {
		event, err := connector.Send(context.Background(), protocol.Request{
			Channel: "#Pantalk",
			Thread:  "message-123",
			Text:    "**hello**",
			Format:  "markdown",
		})
		resultCh <- sendResult{event: event, err: err}
	}()

	scanner := bufio.NewScanner(server)
	if !scanner.Scan() {
		t.Fatalf("outbound message missing: %v", scanner.Err())
	}
	if got, want := scanner.Text(), "@reply-parent-msg-id=message-123 PRIVMSG #pantalk :hello"; got != want {
		t.Fatalf("outbound line = %q, want %q", got, want)
	}

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Send() error = %v", result.err)
	}
	if result.event.Direction != "out" || result.event.User != "pantalkbot" ||
		result.event.Channel != "#pantalk" || result.event.Thread != "message-123" {
		t.Fatalf("outbound event = %#v", result.event)
	}
	if len(published) != 1 {
		t.Fatalf("published %d events, want 1", len(published))
	}
}

func TestTwitchSendJoinsNewChannel(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	server, client := net.Pipe()
	connector.conn = client
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := connector.Send(context.Background(), protocol.Request{
			Channel: "newchannel",
			Text:    "hello",
		})
		errCh <- err
	}()

	scanner := bufio.NewScanner(server)
	var lines []string
	for range 2 {
		if !scanner.Scan() {
			t.Fatalf("outbound line missing: %v", scanner.Err())
		}
		lines = append(lines, scanner.Text())
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	want := []string{"JOIN #newchannel", "PRIVMSG #newchannel :hello"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("outbound lines = %#v, want %#v", lines, want)
	}
}

func TestTwitchReconnectCommand(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	err := connector.handleLine(":tmi.twitch.tv RECONNECT")
	if !errors.Is(err, errTwitchReconnect) {
		t.Fatalf("error = %v, want errTwitchReconnect", err)
	}
}

func TestParseTwitchIRCMessage(t *testing.T) {
	message := parseTwitchIRCMessage(
		"@display-name=Space\\sCadet;system-msg=semi\\:colon\\\\ok;empty=;flag " +
			":nick!user@host PRIVMSG #room :hello there",
	)

	if message.Prefix != "nick!user@host" || message.Command != "PRIVMSG" {
		t.Fatalf("parsed message = %#v", message)
	}
	if !reflect.DeepEqual(message.Params, []string{"#room", "hello there"}) {
		t.Fatalf("params = %#v", message.Params)
	}
	wantTags := map[string]string{
		"display-name": "Space Cadet",
		"system-msg":   "semi;colon\\ok",
		"empty":        "",
		"flag":         "",
	}
	if !reflect.DeepEqual(message.Tags, wantTags) {
		t.Fatalf("tags = %#v, want %#v", message.Tags, wantTags)
	}
}

func TestResolveTwitchChannel(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.Request
		want    string
	}{
		{"channel", protocol.Request{Channel: "Pantalk"}, "#pantalk"},
		{"hash channel", protocol.Request{Channel: "#Pantalk"}, "#pantalk"},
		{"target", protocol.Request{Target: "channel:Pantalk"}, "#pantalk"},
		{"twitch target", protocol.Request{Target: "twitch:channel:Pantalk"}, "#pantalk"},
		{"channel wins", protocol.Request{Channel: "one", Target: "two"}, "#one"},
		{"empty", protocol.Request{}, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveTwitchChannel(test.request); got != test.want {
				t.Fatalf("resolveTwitchChannel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTwitchIdentityAndUnsupportedReaction(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	if got := connector.Identity(); got != "pantalkbot" {
		t.Fatalf("Identity() = %q, want pantalkbot", got)
	}
	if err := connector.React(context.Background(), protocol.Request{}); err == nil {
		t.Fatal("React() error = nil, want unsupported error")
	}
}

func TestTwitchSendRequiresConnection(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	_, err := connector.Send(context.Background(), protocol.Request{
		Channel: "pantalk",
		Text:    "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Send() error = %v, want disconnected error", err)
	}
}

func TestTwitchRejectsDirectMessages(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	for _, request := range []protocol.Request{
		{Channel: "dm:viewer", Text: "hello"},
		{Target: "twitch:dm:viewer", Text: "hello"},
	} {
		if _, err := connector.Send(context.Background(), request); err == nil ||
			!strings.Contains(err.Error(), "does not support direct messages") {
			t.Fatalf("Send(%#v) error = %v, want direct-message error", request, err)
		}
	}
}

func TestTwitchMessageTimestampFallback(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got := twitchMessageTimestamp(map[string]string{"tmi-sent-ts": "invalid"})
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Fatalf("fallback timestamp = %v, want current time", got)
	}
}

func newTestTwitchConnector(t *testing.T, publish func(protocol.Event)) *TwitchConnector {
	t.Helper()
	if publish == nil {
		publish = func(protocol.Event) {}
	}
	connector, err := NewTwitchConnector(config.BotConfig{
		Name:        "stream-helper",
		Type:        "twitch",
		Username:    "PantalkBot",
		AccessToken: "test-token",
		Channels:    []string{"pantalk", "openai"},
	}, publish)
	if err != nil {
		t.Fatalf("NewTwitchConnector() error = %v", err)
	}
	return connector
}

func TestTwitchRateLimiterSpreadsSendsAcrossWindows(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newTwitchRateLimiter(3, 30*time.Second)
	limiter.now = func() time.Time { return now }

	// The first window's worth of messages goes out immediately.
	for i := 0; i < 3; i++ {
		if wait := limiter.reserve(); wait != 0 {
			t.Fatalf("message %d waited %s, want immediate", i, wait)
		}
	}

	// The next three must wait for the first three to age out, and the three
	// after that for a further window - rather than all landing at once.
	for i := 0; i < 3; i++ {
		if wait := limiter.reserve(); wait != 30*time.Second {
			t.Fatalf("message %d waited %s, want 30s", i+3, wait)
		}
	}
	for i := 0; i < 3; i++ {
		if wait := limiter.reserve(); wait != 60*time.Second {
			t.Fatalf("message %d waited %s, want 60s", i+6, wait)
		}
	}

	// Once the window has genuinely passed, sending is free again.
	now = now.Add(90 * time.Second)
	if wait := limiter.reserve(); wait != 0 {
		t.Fatalf("waited %s after the window elapsed, want immediate", wait)
	}
}

func TestTwitchAwaitSendSlotHonoursContextCancellation(t *testing.T) {
	connector := newTestTwitchConnector(t, nil)
	connector.limiter = newTwitchRateLimiter(1, time.Hour)

	if err := connector.awaitSendSlot(context.Background()); err != nil {
		t.Fatalf("first slot returned %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connector.awaitSendSlot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("awaitSendSlot() = %v, want context.Canceled", err)
	}
}

func TestSanitizeIRCLineNeutralizesInjectedCommands(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "PRIVMSG #room :hello there", "PRIVMSG #room :hello there"},
		{"crlf in message body", "PRIVMSG #room :hi\r\nQUIT :bye", "PRIVMSG #room :hi  QUIT :bye"},
		{"bare cr in message body", "PRIVMSG #room :hi\rQUIT", "PRIVMSG #room :hi QUIT"},
		{"bare lf in message body", "PRIVMSG #room :hi\nJOIN #other", "PRIVMSG #room :hi JOIN #other"},
		{"crlf in channel name", "JOIN #room\r\nPART #other", "JOIN #room  PART #other"},
		{"nul is dropped", "PRIVMSG #room :hi\x00there", "PRIVMSG #room :hithere"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeIRCLine(tt.in); got != tt.want {
				t.Errorf("sanitizeIRCLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
