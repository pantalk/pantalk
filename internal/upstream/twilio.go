package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

const defaultTwilioEndpoint = "https://api.twilio.com"

// TwilioConnector bridges a Twilio account to the PanTalk event stream using
// the Twilio REST API. It polls the Messages resource for incoming SMS/MMS and
// sends outbound messages via POST to the Messages endpoint.
type TwilioConnector struct {
	serviceName string
	botName     string
	baseURL     string
	accountSID  string
	authToken   string
	phoneNumber string
	publish     func(protocol.Event)
	httpClient  *http.Client

	mu            sync.RWMutex
	channels      map[string]struct{}
	lastPollTime  time.Time
	seenMessages  map[string]struct{}
}

type twilioMessageList struct {
	Messages []twilioMessage `json:"messages"`
}

type twilioMessage struct {
	SID         string  `json:"sid"`
	Body        string  `json:"body"`
	From        string  `json:"from"`
	To          string  `json:"to"`
	Status      string  `json:"status"`
	Direction   string  `json:"direction"`
	DateCreated string  `json:"date_created"`
}

type twilioSendResponse struct {
	SID         string `json:"sid"`
	Body        string `json:"body"`
	From        string `json:"from"`
	To          string `json:"to"`
	Status      string `json:"status"`
	DateCreated string `json:"date_created"`
}

func NewTwilioConnector(bot config.BotConfig, publish func(protocol.Event)) (*TwilioConnector, error) {
	authToken, err := config.ResolveCredential(bot.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("resolve twilio auth_token for bot %q: %w", bot.Name, err)
	}

	accountSID, err := config.ResolveCredential(bot.AccountSID)
	if err != nil {
		return nil, fmt.Errorf("resolve twilio account_sid for bot %q: %w", bot.Name, err)
	}

	phoneNumber := strings.TrimSpace(bot.PhoneNumber)
	if phoneNumber == "" {
		return nil, fmt.Errorf("twilio bot %q requires phone_number (Twilio phone number in E.164 format)", bot.Name)
	}

	connector := &TwilioConnector{
		serviceName:  bot.Type,
		botName:      bot.Name,
		baseURL:      defaultTwilioEndpoint,
		accountSID:   accountSID,
		authToken:    authToken,
		phoneNumber:  phoneNumber,
		publish:      publish,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		channels:     make(map[string]struct{}),
		seenMessages: make(map[string]struct{}),
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		connector.channels[trimmed] = struct{}{}
	}

	return connector, nil
}

func (t *TwilioConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			t.publishStatus("connector offline")
			return
		default:
		}

		if err := t.verifyAccount(ctx); err != nil {
			log.Printf("[twilio:%s] auth failed: %v", t.botName, err)
			t.publishStatus("twilio auth failed: " + err.Error())
			t.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		log.Printf("[twilio:%s] authenticated (phone=%s)", t.botName, t.phoneNumber)
		t.publishStatus("connector online")

		t.mu.Lock()
		t.lastPollTime = time.Now().UTC().Add(-1 * time.Minute)
		t.mu.Unlock()

		t.pollLoop(ctx)
	}
}

func (t *TwilioConnector) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			t.publishHeartbeat()
		case <-ticker.C:
			messages, err := t.fetchNewMessages(ctx)
			if err != nil {
				t.publishStatus("twilio poll error: " + err.Error())
				continue
			}

			for _, msg := range messages {
				t.handleIncomingMessage(msg)
			}
		}
	}
}

func (t *TwilioConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	toNumber := resolveTwilioChannel(request)
	if toNumber == "" {
		return protocol.Event{}, fmt.Errorf("twilio send requires channel or target")
	}

	t.rememberChannel(toNumber)

	data := url.Values{}
	data.Set("To", toNumber)
	data.Set("From", t.phoneNumber)
	data.Set("Body", text)

	apiURL := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", t.baseURL, t.accountSID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return protocol.Event{}, err
	}
	httpReq.SetBasicAuth(t.accountSID, t.authToken)
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return protocol.Event{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.Event{}, fmt.Errorf("twilio send failed: status %d", resp.StatusCode)
	}

	var sendResp twilioSendResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		return protocol.Event{}, err
	}

	target := request.Target
	if target == "" {
		target = "phone:" + toNumber
	}

	event := protocol.Event{
		Timestamp: parseTwilioDate(sendResp.DateCreated),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "message",
		Direction: "out",
		User:      t.Identity(),
		Target:    target,
		Channel:   toNumber,
		Thread:    sendResp.SID,
		Text:      text,
	}
	t.publish(event)

	return event, nil
}

func (t *TwilioConnector) Identity() string {
	return t.phoneNumber
}

func (t *TwilioConnector) verifyAccount(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/2010-04-01/Accounts/%s.json", t.baseURL, t.accountSID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	httpReq.SetBasicAuth(t.accountSID, t.authToken)

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("account verification failed: status %d", resp.StatusCode)
	}

	return nil
}

func (t *TwilioConnector) fetchNewMessages(ctx context.Context) ([]twilioMessage, error) {
	t.mu.RLock()
	since := t.lastPollTime
	t.mu.RUnlock()

	apiURL := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", t.baseURL, t.accountSID)

	params := url.Values{}
	params.Set("To", t.phoneNumber)
	params.Set("DateSent>", since.Format("2006-01-02T15:04:05Z"))
	params.Set("PageSize", "50")

	fullURL := apiURL + "?" + params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.SetBasicAuth(t.accountSID, t.authToken)

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("twilio list messages failed: status %d", resp.StatusCode)
	}

	var msgList twilioMessageList
	if err := json.NewDecoder(resp.Body).Decode(&msgList); err != nil {
		return nil, err
	}

	// Filter out already-seen messages.
	var newMessages []twilioMessage

	t.mu.Lock()
	t.lastPollTime = time.Now().UTC()
	for _, msg := range msgList.Messages {
		if msg.Direction != "inbound" {
			continue
		}
		if _, seen := t.seenMessages[msg.SID]; seen {
			continue
		}
		t.seenMessages[msg.SID] = struct{}{}
		newMessages = append(newMessages, msg)
	}
	t.mu.Unlock()

	return newMessages, nil
}

func (t *TwilioConnector) handleIncomingMessage(msg twilioMessage) {
	from := msg.From
	if !t.acceptsChannel(from) {
		return
	}

	text := strings.TrimSpace(msg.Body)
	if text == "" {
		return
	}

	t.publish(protocol.Event{
		Timestamp: parseTwilioDate(msg.DateCreated),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "message",
		Direction: "in",
		User:      from,
		Target:    "phone:" + from,
		Channel:   from,
		Thread:    msg.SID,
		Text:      text,
	})
}

func (t *TwilioConnector) rememberChannel(channel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channels[channel] = struct{}{}
}

func (t *TwilioConnector) acceptsChannel(channel string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.channels) == 0 {
		return true
	}

	_, ok := t.channels[channel]
	return ok
}

func (t *TwilioConnector) publishStatus(text string) {
	t.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (t *TwilioConnector) publishHeartbeat() {
	t.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   t.serviceName,
		Bot:       t.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (t *TwilioConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// resolveTwilioChannel extracts a phone number from the request's channel or
// target field. It strips common prefixes so callers can pass raw phone numbers
// or prefixed forms (e.g. "phone:+1234567890").
func resolveTwilioChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"phone:", "twilio:phone:", "twilio:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}

// parseTwilioDate parses a Twilio date string (RFC 2822 format) into a time.Time.
func parseTwilioDate(dateStr string) time.Time {
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		time.RFC3339,
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return t.UTC()
		}
	}

	return time.Now().UTC()
}
