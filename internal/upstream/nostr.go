package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/keyer"
	"github.com/nbd-wtf/go-nostr/nip17"
	"github.com/nbd-wtf/go-nostr/nip19"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/formatting"
	"github.com/pantalk/pantalk/internal/protocol"
)

const nostrOperationTimeout = 20 * time.Second

// Ephemeral presence and typing kinds. These are not part of NIP-29 itself;
// they are the convention NIP-29 relays that support liveness have settled on
// (Buzz among them). Relays that do not recognise them reject the event, which
// is why both are published best-effort.
const (
	nostrKindPresence = 20001
	nostrKindTyping   = 20002

	nostrPresenceOnline = "online"
)

type nostrDestinationKind int

const (
	nostrDestinationUnknown nostrDestinationKind = iota
	nostrDestinationDM
	nostrDestinationNIP28
	nostrDestinationNIP29
)

type nostrDestination struct {
	kind  nostrDestinationKind
	id    string
	relay string
}

func (d nostrDestination) channel() string {
	switch d.kind {
	case nostrDestinationDM:
		return "dm:" + d.id
	case nostrDestinationNIP28:
		return "nip28:" + d.id
	case nostrDestinationNIP29:
		return "nip29:" + d.relay + "'" + d.id
	default:
		return ""
	}
}

type nostrDirectSender func(
	ctx context.Context,
	content string,
	tags nostr.Tags,
	recipient string,
) (eventID string, err error)

// NostrConnector bridges NIP-17 one-to-one DMs, NIP-28 public channels, and
// NIP-29 kind-9 group chat messages. NIP-29 group IDs are always paired with
// their authoritative relay because the same ID can identify different forks
// on different relays.
type NostrConnector struct {
	serviceName string
	botName     string
	publish     func(protocol.Event)
	signer      keyer.KeySigner
	publicKey   string
	relays      []string
	displayName string
	about       string
	picture     string

	mu            sync.RWMutex
	pool          *nostr.SimplePool
	nip28Channels map[string]struct{}
	nip29Groups   map[string]nostrDestination

	publishEvent func(context.Context, []string, nostr.Event) error
	sendDirect   nostrDirectSender
}

func NewNostrConnector(bot config.BotConfig, publish func(protocol.Event)) (*NostrConnector, error) {
	privateKey, err := config.ResolveCredential(bot.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("resolve nostr private_key for bot %q: %w", bot.Name, err)
	}

	privateKey, err = decodeNostrPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("decode nostr private_key for bot %q: %w", bot.Name, err)
	}

	signer, err := keyer.NewPlainKeySigner(privateKey)
	if err != nil {
		return nil, fmt.Errorf("create nostr signer for bot %q: %w", bot.Name, err)
	}

	publicKey, err := signer.GetPublicKey(context.Background())
	if err != nil {
		return nil, fmt.Errorf("derive nostr public key for bot %q: %w", bot.Name, err)
	}

	relays, err := normalizeNostrRelays(bot.Relays)
	if err != nil {
		return nil, fmt.Errorf("configure nostr bot %q: %w", bot.Name, err)
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("nostr bot %q requires at least one relay", bot.Name)
	}

	connector := &NostrConnector{
		serviceName:   bot.Type,
		botName:       bot.Name,
		publish:       publish,
		signer:        signer,
		publicKey:     publicKey,
		relays:        relays,
		displayName:   strings.TrimSpace(bot.DisplayName),
		about:         strings.TrimSpace(bot.About),
		picture:       strings.TrimSpace(bot.Picture),
		nip28Channels: make(map[string]struct{}),
		nip29Groups:   make(map[string]nostrDestination),
	}

	for _, configured := range bot.Channels {
		trimmed := strings.TrimSpace(configured)
		if trimmed == "" {
			continue
		}

		destination, parseErr := parseNostrChannel(trimmed)
		if parseErr != nil {
			return nil, fmt.Errorf("configure nostr bot %q channel %q: %w", bot.Name, configured, parseErr)
		}

		switch destination.kind {
		case nostrDestinationNIP28:
			connector.nip28Channels[destination.id] = struct{}{}
		case nostrDestinationNIP29:
			connector.nip29Groups[destination.channel()] = destination
		default:
			return nil, fmt.Errorf("configure nostr bot %q channel %q: DMs are not channel subscriptions", bot.Name, configured)
		}
	}

	connector.publishEvent = connector.publishNostrEvent
	connector.sendDirect = connector.publishDirectMessage

	return connector, nil
}

func (c *NostrConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			c.publishStatus("connector offline")
			return
		}

		established := false
		if err := c.connectAndRun(ctx, func() { established = true }); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[nostr:%s] session ended: %v", c.botName, err)
			c.publishStatus("nostr session ended: " + err.Error())
		}

		// Subscriptions that came up clear the penalty from earlier outages,
		// so a long-lived bot reconnects promptly rather than inheriting the
		// ceiling reached during an unrelated relay outage days earlier.
		if established {
			backoff = time.Second
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			c.publishStatus("connector offline")
			return
		case <-timer.C:
		}

		if backoff < 30*time.Second {
			backoff *= 2
		}

		c.publishStatus("nostr reconnecting...")
	}
}

func (c *NostrConnector) connectAndRun(ctx context.Context, onEstablished func()) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pool := nostr.NewSimplePool(sessionCtx, nostr.WithAuthHandler(func(
		authCtx context.Context,
		authEvent nostr.RelayEvent,
	) error {
		return c.signer.SignEvent(authCtx, authEvent.Event)
	}))
	defer pool.Close("pantalk nostr session ended")

	allRelays := c.subscriptionRelays()
	var connectErrors []error
	connected := 0
	for _, relayURL := range allRelays {
		if _, err := pool.EnsureRelay(relayURL); err != nil {
			connectErrors = append(connectErrors, fmt.Errorf("%s: %w", relayURL, err))
			continue
		}
		connected++
	}
	if connected == 0 {
		return fmt.Errorf("connect to configured relays: %w", errors.Join(connectErrors...))
	}

	c.mu.Lock()
	c.pool = pool
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.pool == pool {
			c.pool = nil
		}
		c.mu.Unlock()
	}()

	// Relays that only accept a fixed kind set (Buzz, for one) reject the DM
	// relay list. That costs NIP-17 discovery, not the session: group and
	// channel subscriptions below are unaffected, so this stays best-effort.
	if err := c.publishDMRelayList(sessionCtx); err != nil {
		log.Printf("[nostr:%s] DM relay list rejected, continuing without NIP-17 discovery: %v", c.botName, err)
	}

	// A bot with no kind:0 shows up as a bare pubkey with no name or avatar.
	if err := c.publishProfile(sessionCtx); err != nil {
		log.Printf("[nostr:%s] profile publish failed, bot may appear unnamed: %v", c.botName, err)
	}

	since := nostr.Now()
	ended := make(chan error, 2+len(c.nip29Groups))

	if len(c.nip28Channels) > 0 {
		channelIDs := make([]string, 0, len(c.nip28Channels))
		for channelID := range c.nip28Channels {
			channelIDs = append(channelIDs, channelID)
		}
		events := pool.SubscribeMany(sessionCtx, c.relays, nostr.Filter{
			Kinds: []int{nostr.KindChannelMessage},
			Tags:  nostr.TagMap{"e": channelIDs},
			Since: &since,
		})
		go c.forwardPublicEvents(sessionCtx, events, ended)
	}

	for _, group := range c.nip29Groups {
		group := group
		events := pool.SubscribeMany(sessionCtx, []string{group.relay}, nostr.Filter{
			Kinds: []int{nostr.KindSimpleGroupChatMessage},
			Tags:  nostr.TagMap{"h": []string{group.id}},
			Since: &since,
		})
		go c.forwardPublicEvents(sessionCtx, events, ended)
	}

	directMessages := nip17.ListenForMessages(sessionCtx, pool, c.signer, c.relays, since)
	go c.forwardDirectMessages(sessionCtx, directMessages, ended)

	c.publishPresence(sessionCtx, nostrPresenceOnline)

	c.publishStatus("connector online")
	if onEstablished != nil {
		onEstablished()
	}

	heartbeat := time.NewTicker(45 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ended:
			return err
		case <-heartbeat.C:
			// Presence is ephemeral and decays, so the heartbeat doubles as
			// the keepalive that holds the bot "online" between messages.
			c.publishPresence(sessionCtx, nostrPresenceOnline)
			c.publishHeartbeat()
		}
	}
}

func (c *NostrConnector) forwardPublicEvents(
	ctx context.Context,
	events <-chan nostr.RelayEvent,
	ended chan<- error,
) {
	for relayEvent := range events {
		if relayEvent.Event == nil || relayEvent.Relay == nil {
			continue
		}
		c.handleNostrEvent(relayEvent.Relay.URL, relayEvent.Event)
	}

	select {
	case ended <- errors.New("nostr relay subscription ended"):
	case <-ctx.Done():
	}
}

func (c *NostrConnector) forwardDirectMessages(
	ctx context.Context,
	events <-chan nostr.Event,
	ended chan<- error,
) {
	for event := range events {
		c.handleDirectMessage(&event)
	}

	select {
	case ended <- errors.New("nostr direct-message subscription ended"):
	case <-ctx.Done():
	}
}

func (c *NostrConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text, err := prepareNostrText(request.Format, request.Text)
	if err != nil {
		return protocol.Event{}, err
	}

	destination, err := resolveNostrDestination(request)
	if err != nil {
		return protocol.Event{}, err
	}

	switch destination.kind {
	case nostrDestinationDM:
		return c.sendDirectMessage(ctx, request, destination, text)
	case nostrDestinationNIP28:
		return c.sendNIP28Message(ctx, request, destination, text)
	case nostrDestinationNIP29:
		return c.sendNIP29Message(ctx, request, destination, text)
	default:
		return protocol.Event{}, errors.New("nostr send requires a dm:, nip28:, or relay-scoped nip29: destination")
	}
}

func (c *NostrConnector) sendDirectMessage(
	ctx context.Context,
	request protocol.Request,
	destination nostrDestination,
	text string,
) (protocol.Event, error) {
	tags := make(nostr.Tags, 0, 1)
	if thread := strings.TrimSpace(request.Thread); thread != "" {
		if !nostr.IsValid32ByteHex(strings.ToLower(thread)) {
			return protocol.Event{}, fmt.Errorf("nostr DM thread must be a 64-character event ID")
		}
		tags = append(tags, nostr.Tag{"e", strings.ToLower(thread)})
	}

	_, err := c.sendDirect(ctx, text, tags, destination.id)
	if err != nil {
		return protocol.Event{}, err
	}

	event := c.outboundEvent(request, destination, text)
	c.publish(event)
	return event, nil
}

func (c *NostrConnector) sendNIP28Message(
	ctx context.Context,
	request protocol.Request,
	destination nostrDestination,
	text string,
) (protocol.Event, error) {
	relayHint := c.relays[0]
	tags := nostr.Tags{
		nostr.Tag{"e", destination.id, relayHint, "root"},
	}

	if thread := strings.TrimSpace(request.Thread); thread != "" {
		thread = strings.ToLower(thread)
		if !nostr.IsValid32ByteHex(thread) {
			return protocol.Event{}, fmt.Errorf("NIP-28 thread must be a 64-character event ID")
		}
		tags = append(tags, nostr.Tag{"e", thread, relayHint, "reply"})
	}

	event := nostr.Event{
		Kind:      nostr.KindChannelMessage,
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   text,
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		return protocol.Event{}, fmt.Errorf("sign NIP-28 message: %w", err)
	}
	if err := c.publishEvent(ctx, c.relays, event); err != nil {
		return protocol.Event{}, fmt.Errorf("publish NIP-28 message: %w", err)
	}

	outbound := c.outboundEvent(request, destination, text)
	c.publish(outbound)
	return outbound, nil
}

func (c *NostrConnector) sendNIP29Message(
	ctx context.Context,
	request protocol.Request,
	destination nostrDestination,
	text string,
) (protocol.Event, error) {
	if strings.TrimSpace(request.Thread) != "" {
		return protocol.Event{}, errors.New("threaded NIP-29 sends are not supported; use an ordinary kind-9 group message")
	}

	if _, configured := c.nip29Groups[destination.channel()]; !configured {
		return protocol.Event{}, fmt.Errorf("NIP-29 group %q is not configured for this bot", destination.channel())
	}

	event := nostr.Event{
		Kind:      nostr.KindSimpleGroupChatMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"h", destination.id}},
		Content:   text,
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		return protocol.Event{}, fmt.Errorf("sign NIP-29 message: %w", err)
	}
	if err := c.publishEvent(ctx, []string{destination.relay}, event); err != nil {
		return protocol.Event{}, fmt.Errorf("publish NIP-29 message: %w", err)
	}

	outbound := c.outboundEvent(request, destination, text)
	outbound.ChannelName = destination.id
	c.publish(outbound)
	return outbound, nil
}

func (c *NostrConnector) outboundEvent(
	request protocol.Request,
	destination nostrDestination,
	text string,
) protocol.Event {
	target := strings.TrimSpace(request.Target)
	if target == "" {
		target = destination.channel()
	}

	return protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "out",
		User:      c.Identity(),
		Target:    target,
		Channel:   destination.channel(),
		Thread:    strings.TrimSpace(request.Thread),
		Direct:    destination.kind == nostrDestinationDM,
		Text:      text,
	}
}

func (c *NostrConnector) handleNostrEvent(relayURL string, event *nostr.Event) {
	if event == nil || event.PubKey == c.publicKey {
		return
	}

	text := strings.TrimSpace(event.Content)
	if text == "" {
		return
	}

	switch event.Kind {
	case nostr.KindChannelMessage:
		channelID, thread := nip28References(event.Tags)
		if channelID == "" || !c.acceptsNIP28(channelID) {
			return
		}

		channel := nostrDestination{kind: nostrDestinationNIP28, id: channelID}.channel()
		c.publish(protocol.Event{
			Timestamp: event.CreatedAt.Time().UTC(),
			Service:   c.serviceName,
			Bot:       c.botName,
			Kind:      "message",
			Direction: "in",
			User:      event.PubKey,
			Target:    channel,
			Channel:   channel,
			Thread:    thread,
			Text:      text,
		})

	case nostr.KindSimpleGroupChatMessage:
		groupID := firstNostrTagValue(event.Tags, "h")
		group, ok := c.configuredNIP29Group(relayURL, groupID)
		if !ok {
			return
		}

		channel := group.channel()
		c.publish(protocol.Event{
			Timestamp:   event.CreatedAt.Time().UTC(),
			Service:     c.serviceName,
			Bot:         c.botName,
			Kind:        "message",
			Direction:   "in",
			User:        event.PubKey,
			Target:      channel,
			Channel:     channel,
			ChannelName: group.id,
			Text:        text,
		})
	}
}

func (c *NostrConnector) handleDirectMessage(event *nostr.Event) {
	if event == nil || event.Kind != nostr.KindDirectMessage || event.PubKey == c.publicKey {
		return
	}

	text := strings.TrimSpace(event.Content)
	if text == "" {
		return
	}

	receivers := nostrTagValues(event.Tags, "p")
	if len(receivers) != 1 || receivers[0] != c.publicKey {
		// Pantalk's target model currently represents one peer per DM. Accepting
		// a multi-recipient rumor would make an automatic reply silently create
		// a different room.
		return
	}

	thread := firstNostrTagValue(event.Tags, "e")
	channel := nostrDestination{kind: nostrDestinationDM, id: event.PubKey}.channel()
	c.publish(protocol.Event{
		Timestamp: event.CreatedAt.Time().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "in",
		User:      event.PubKey,
		Target:    channel,
		Channel:   channel,
		Thread:    thread,
		Direct:    true,
		Text:      text,
	})
}

func (c *NostrConnector) publishDirectMessage(
	ctx context.Context,
	content string,
	tags nostr.Tags,
	recipient string,
) (string, error) {
	pool, err := c.currentPool()
	if err != nil {
		return "", err
	}

	operationCtx, cancel := nostrContext(ctx)
	defer cancel()

	recipientRelays := nip17.GetDMRelays(operationCtx, recipient, pool, c.relays)
	recipientRelays, err = normalizeNostrRelays(recipientRelays)
	if err != nil {
		return "", fmt.Errorf("recipient advertised invalid DM relay: %w", err)
	}
	if len(recipientRelays) == 0 {
		return "", errors.New("recipient has no discoverable NIP-17 kind-10050 DM relay list")
	}

	toUs, toThem, err := nip17.PrepareMessage(
		operationCtx,
		content,
		tags,
		c.signer,
		recipient,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("prepare NIP-17 message: %w", err)
	}

	if err := c.publishNostrEvent(operationCtx, c.relays, toUs); err != nil {
		return "", fmt.Errorf("publish NIP-17 sender copy: %w", err)
	}
	if err := c.publishNostrEvent(operationCtx, recipientRelays, toThem); err != nil {
		return "", fmt.Errorf("publish NIP-17 recipient copy: %w", err)
	}

	return toThem.ID, nil
}

// publishDMRelayList advertises the inboxes this bot actively monitors. NIP-17
// senders discover a recipient's gift-wrap destinations through this
// replaceable kind-10050 event, so listening without publishing the list would
// leave a fresh bot unreachable to standards-compliant clients.
func (c *NostrConnector) publishDMRelayList(ctx context.Context) error {
	tags := make(nostr.Tags, 0, len(c.relays))
	for _, relayURL := range c.relays {
		tags = append(tags, nostr.Tag{"relay", relayURL})
	}

	event := nostr.Event{
		Kind:      nostr.KindDMRelayList,
		CreatedAt: nostr.Now(),
		Tags:      tags,
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		return fmt.Errorf("sign relay list: %w", err)
	}
	if err := c.publishEvent(ctx, c.relays, event); err != nil {
		return fmt.Errorf("publish relay list: %w", err)
	}
	return nil
}

// publishProfile announces the bot's kind:0 metadata so it appears as a named
// participant rather than a bare pubkey. Fields left unset in config fall back
// to the bot name, which is always present.
func (c *NostrConnector) publishProfile(ctx context.Context) error {
	metadata := map[string]string{"name": c.botName}
	if c.displayName != "" {
		metadata["display_name"] = c.displayName
		metadata["name"] = c.displayName
	}
	if c.about != "" {
		metadata["about"] = c.about
	}
	if c.picture != "" {
		metadata["picture"] = c.picture
	}

	content, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode profile metadata: %w", err)
	}

	event := nostr.Event{
		Kind:      nostr.KindProfileMetadata,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
		Content:   string(content),
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		return fmt.Errorf("sign profile: %w", err)
	}
	if err := c.publishEvent(ctx, c.relays, event); err != nil {
		return fmt.Errorf("publish profile: %w", err)
	}
	return nil
}

// publishPresence emits an ephemeral presence event. The kind is outside the
// range every relay implements, so rejection is logged and ignored rather than
// treated as a session failure.
func (c *NostrConnector) publishPresence(ctx context.Context, status string) {
	event := nostr.Event{
		Kind:      nostrKindPresence,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
		Content:   status,
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		log.Printf("[nostr:%s] sign presence: %v", c.botName, err)
		return
	}
	if err := c.publishEvent(ctx, c.relays, event); err != nil {
		log.Printf("[nostr:%s] presence not accepted by relay: %v", c.botName, err)
	}
}

// Typing implements TypingIndicator with an ephemeral typing event scoped to a
// group. Only NIP-29 destinations carry one: NIP-28 channels and NIP-17 DMs
// have no equivalent, and reporting an error for them would make the daemon's
// typing lease retry something that can never succeed.
func (c *NostrConnector) Typing(ctx context.Context, request protocol.Request) error {
	destination, err := resolveNostrDestination(request)
	if err != nil {
		return err
	}
	if destination.kind != nostrDestinationNIP29 {
		return nil
	}

	event := nostr.Event{
		Kind:      nostrKindTyping,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"h", destination.id}},
	}
	if err := c.signer.SignEvent(ctx, &event); err != nil {
		return fmt.Errorf("sign typing indicator: %w", err)
	}
	if err := c.publishEvent(ctx, []string{destination.relay}, event); err != nil {
		return fmt.Errorf("publish typing indicator: %w", err)
	}
	return nil
}

func (c *NostrConnector) publishNostrEvent(
	ctx context.Context,
	relayURLs []string,
	event nostr.Event,
) error {
	pool, err := c.currentPool()
	if err != nil {
		return err
	}

	operationCtx, cancel := nostrContext(ctx)
	defer cancel()

	var publishErrors []error
	succeeded := false
	for result := range pool.PublishMany(operationCtx, relayURLs, event) {
		if result.Error == nil {
			succeeded = true
			continue
		}
		publishErrors = append(publishErrors, fmt.Errorf("%s: %w", result.RelayURL, result.Error))
	}

	if succeeded {
		return nil
	}
	if len(publishErrors) == 0 {
		return errors.New("no relays were provided")
	}
	return errors.Join(publishErrors...)
}

func (c *NostrConnector) currentPool() (*nostr.SimplePool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.pool == nil {
		return nil, errors.New("nostr connector is not connected")
	}
	return c.pool, nil
}

func (c *NostrConnector) acceptsNIP28(channelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.nip28Channels) == 0 {
		return false
	}
	_, ok := c.nip28Channels[channelID]
	return ok
}

func (c *NostrConnector) configuredNIP29Group(relayURL string, groupID string) (nostrDestination, bool) {
	normalizedRelay := nostr.NormalizeURL(strings.TrimSpace(relayURL))
	key := nostrDestination{
		kind:  nostrDestinationNIP29,
		id:    groupID,
		relay: normalizedRelay,
	}.channel()

	c.mu.RLock()
	defer c.mu.RUnlock()
	group, ok := c.nip29Groups[key]
	return group, ok
}

func (c *NostrConnector) subscriptionRelays() []string {
	relays := slices.Clone(c.relays)
	for _, group := range c.nip29Groups {
		if !slices.Contains(relays, group.relay) {
			relays = append(relays, group.relay)
		}
	}
	return relays
}

func (c *NostrConnector) Identity() string {
	return c.publicKey
}

func (c *NostrConnector) React(_ context.Context, _ protocol.Request) error {
	return errors.New("reactions are not supported by the nostr connector")
}

func (c *NostrConnector) publishStatus(text string) {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (c *NostrConnector) publishHeartbeat() {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream relay subscriptions alive",
	})
}

func decodeNostrPrivateKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(trimmed), "nsec1") {
		prefix, decoded, err := nip19.Decode(strings.ToLower(trimmed))
		if err != nil {
			return "", err
		}
		secret, ok := decoded.(string)
		if prefix != "nsec" || !ok {
			return "", errors.New("private key must be an nsec or 64-character hex key")
		}
		return secret, nil
	}

	if !nostr.IsValid32ByteHex(strings.ToLower(trimmed)) {
		return "", errors.New("private key must be an nsec or 64-character hex key")
	}
	return strings.ToLower(trimmed), nil
}

func normalizeNostrRelays(relays []string) ([]string, error) {
	normalized := make([]string, 0, len(relays))
	for _, relayURL := range relays {
		relayURL = strings.TrimSpace(relayURL)
		if relayURL == "" {
			continue
		}
		if !nostr.IsValidRelayURL(relayURL) {
			return nil, fmt.Errorf("relay %q must use ws:// or wss://", relayURL)
		}
		relayURL = nostr.NormalizeURL(relayURL)
		if !slices.Contains(normalized, relayURL) {
			normalized = append(normalized, relayURL)
		}
	}
	return normalized, nil
}

func resolveNostrDestination(request protocol.Request) (nostrDestination, error) {
	raw := strings.TrimSpace(request.Channel)
	if raw == "" {
		raw = strings.TrimSpace(request.Target)
	}
	if raw == "" {
		return nostrDestination{}, errors.New("nostr send requires channel or target")
	}
	return parseNostrDestination(raw)
}

func parseNostrChannel(raw string) (nostrDestination, error) {
	destination, err := parseNostrDestination(raw)
	if err != nil {
		return nostrDestination{}, err
	}
	if destination.kind == nostrDestinationDM {
		return nostrDestination{}, errors.New("DM targets cannot be configured as channels")
	}
	return destination, nil
}

func parseNostrDestination(raw string) (nostrDestination, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nostrDestination{}, errors.New("nostr destination cannot be empty")
	}

	lower := strings.ToLower(raw)
	for _, prefix := range []string{"nostr:dm:", "dm:"} {
		if strings.HasPrefix(lower, prefix) {
			publicKey, err := decodeNostrPublicKey(raw[len(prefix):])
			if err != nil {
				return nostrDestination{}, fmt.Errorf("invalid Nostr DM recipient: %w", err)
			}
			return nostrDestination{kind: nostrDestinationDM, id: publicKey}, nil
		}
	}

	for _, prefix := range []string{"nostr:nip28:", "nip28:", "nostr:channel:", "channel:"} {
		if strings.HasPrefix(lower, prefix) {
			channelID := strings.ToLower(strings.TrimSpace(raw[len(prefix):]))
			if !nostr.IsValid32ByteHex(channelID) {
				return nostrDestination{}, errors.New("NIP-28 channel must be a 64-character kind-40 event ID")
			}
			return nostrDestination{kind: nostrDestinationNIP28, id: channelID}, nil
		}
	}

	for _, prefix := range []string{"nostr:nip29:", "nip29:"} {
		if strings.HasPrefix(lower, prefix) {
			return parseNIP29Destination(raw[len(prefix):])
		}
	}

	if nostr.IsValid32ByteHex(strings.ToLower(raw)) {
		return nostrDestination{
			kind: nostrDestinationNIP28,
			id:   strings.ToLower(raw),
		}, nil
	}

	return nostrDestination{}, errors.New("destination must use dm:, nip28:, or relay-scoped nip29:<relay>'<group>")
}

func parseNIP29Destination(raw string) (nostrDestination, error) {
	relayURL, groupID, found := strings.Cut(strings.TrimSpace(raw), "'")
	if !found {
		return nostrDestination{}, errors.New("NIP-29 destination must be relay-scoped as nip29:<relay-url>'<group-id>")
	}

	relayURL = strings.TrimSpace(relayURL)
	groupID = strings.TrimSpace(groupID)
	if !nostr.IsValidRelayURL(relayURL) {
		return nostrDestination{}, fmt.Errorf("NIP-29 relay %q must use ws:// or wss://", relayURL)
	}
	if groupID == "" {
		return nostrDestination{}, errors.New("NIP-29 group ID cannot be empty")
	}

	return nostrDestination{
		kind:  nostrDestinationNIP29,
		id:    groupID,
		relay: nostr.NormalizeURL(relayURL),
	}, nil
}

func decodeNostrPublicKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if nostr.IsValid32ByteHex(lower) {
		return lower, nil
	}

	prefix, decoded, err := nip19.Decode(lower)
	if err != nil {
		return "", errors.New("recipient must be a hex pubkey, npub, or nprofile")
	}

	switch prefix {
	case "npub":
		publicKey, ok := decoded.(string)
		if !ok || !nostr.IsValid32ByteHex(publicKey) {
			return "", errors.New("npub did not contain a valid public key")
		}
		return publicKey, nil
	case "nprofile":
		profile, ok := decoded.(nostr.ProfilePointer)
		if !ok || !nostr.IsValid32ByteHex(profile.PublicKey) {
			return "", errors.New("nprofile did not contain a valid public key")
		}
		return profile.PublicKey, nil
	default:
		return "", errors.New("recipient must be a hex pubkey, npub, or nprofile")
	}
}

func prepareNostrText(format string, text string) (string, error) {
	normalizedFormat, err := formatting.NormalizeFormat(format)
	if err != nil {
		return "", err
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text cannot be empty")
	}

	switch normalizedFormat {
	case formatting.FormatMarkdown:
		text = formatting.MarkdownToPlain(text)
	case formatting.FormatHTML:
		text = formatting.StripHTML(text)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text cannot be empty")
	}
	return text, nil
}

func nip28References(tags nostr.Tags) (root string, reply string) {
	var unmarked []string
	for _, tag := range tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		switch {
		case len(tag) >= 4 && tag[3] == "root":
			root = tag[1]
		case len(tag) >= 4 && tag[3] == "reply":
			reply = tag[1]
		default:
			unmarked = append(unmarked, tag[1])
		}
	}

	if root == "" && len(unmarked) > 0 {
		root = unmarked[0]
	}
	if reply == "" && len(unmarked) > 1 {
		reply = unmarked[len(unmarked)-1]
	}
	return root, reply
}

func firstNostrTagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}

func nostrTagValues(tags nostr.Tags, key string) []string {
	values := make([]string, 0)
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			values = append(values, tag[1])
		}
	}
	return values
}

func nostrContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, nostrOperationTimeout)
}
