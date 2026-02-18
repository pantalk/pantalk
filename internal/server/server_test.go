package server

import (
	"strings"
	"testing"

	"github.com/pantalk/pantalk/internal/protocol"
)

func TestBotKey(t *testing.T) {
	tests := []struct {
		service string
		bot     string
		want    string
	}{
		{"slack", "ops-bot", "slack:ops-bot"},
		{"discord", "ops", "discord:ops"},
		{"", "", ":"},
	}

	for _, tt := range tests {
		got := botKey(tt.service, tt.bot)
		if got != tt.want {
			t.Errorf("botKey(%q, %q) = %q, want %q", tt.service, tt.bot, got, tt.want)
		}
	}
}

func TestRouteKey(t *testing.T) {
	tests := []struct {
		target  string
		channel string
		thread  string
		want    string
	}{
		{"", "", "", ""},
		{"t1", "c1", "th1", "t=t1|c=c1|th=th1"},
		{"", "c1", "", "t=|c=c1|th="},
		{"t1", "", "", "t=t1|c=|th="},
	}

	for _, tt := range tests {
		got := routeKey(tt.target, tt.channel, tt.thread)
		if got != tt.want {
			t.Errorf("routeKey(%q, %q, %q) = %q, want %q", tt.target, tt.channel, tt.thread, got, tt.want)
		}
	}
}

func TestMatchEventFilters(t *testing.T) {
	event := protocol.Event{
		Target:  "channel:C1",
		Channel: "C1",
		Thread:  "T100",
		Text:    "deploy to production",
	}

	tests := []struct {
		name    string
		target  string
		channel string
		thread  string
		search  string
		want    bool
	}{
		{"no filters", "", "", "", "", true},
		{"matching target", "channel:C1", "", "", "", true},
		{"wrong target", "channel:C2", "", "", "", false},
		{"matching channel", "", "C1", "", "", true},
		{"wrong channel", "", "C2", "", "", false},
		{"matching thread", "", "", "T100", "", true},
		{"wrong thread", "", "", "T200", "", false},
		{"all match", "channel:C1", "C1", "T100", "", true},
		{"one mismatch", "channel:C1", "C1", "T200", "", false},
		{"search match", "", "", "", "deploy", true},
		{"search match case-insensitive", "", "", "", "DEPLOY", true},
		{"search no match", "", "", "", "rollback", false},
		{"search with channel match", "", "C1", "", "production", true},
		{"search with channel mismatch", "", "C2", "", "deploy", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchEventFilters(event, tt.target, tt.channel, tt.thread, tt.search)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMentionsAgent(t *testing.T) {
	bot := protocol.BotRef{
		Name:  "helper-bot",
		BotID: "U123ABC",
	}

	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty text", "", false},
		{"no mention", "hello world", false},
		{"name mention", "hey @helper-bot how are you", true},
		{"name mention case-insensitive", "HEY @HELPER-BOT please", true},
		{"id mention slack format", "hello <@U123ABC> please help", true},
		{"id mention case-insensitive", "hello <@u123abc> please help", true},
		{"partial name no at", "helper-bot", false},
		{"partial id no brackets", "@U123ABC", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := protocol.Event{Text: tt.text}
			got := mentionsAgent(event, bot)
			if got != tt.want {
				t.Errorf("mentionsAgent(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestMentionsAgent_EmptyBot(t *testing.T) {
	bot := protocol.BotRef{}
	event := protocol.Event{Text: "@something <@other>"}
	if mentionsAgent(event, bot) {
		t.Error("expected false for empty bot ref")
	}
}

func TestIsDirectToAgent(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		channel string
		kind    string
		want    bool
	}{
		{"dm prefix", "dm:user123", "", "", true},
		{"direct prefix", "direct:user123", "", "", true},
		{"user prefix", "user:someone", "", "", true},
		{"channel prefix", "channel:C1", "", "", false},
		{"slack DM channel", "", "D0123456", "", true},
		{"slack DM channel lower", "", "d0123456", "", true},
		{"normal channel", "", "C0123456", "", false},
		{"dm kind", "", "", "dm", true},
		{"message kind", "", "", "message", false},
		{"no indicators", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := protocol.Event{
				Target:  tt.target,
				Channel: tt.channel,
				Kind:    tt.kind,
			}
			got := isDirectToAgent(event)
			if got != tt.want {
				t.Errorf("isDirectToAgent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParticipation(t *testing.T) {
	s := &Server{
		routesByBot: make(map[string]map[string]struct{}),
	}

	key := "slack:bot-a"

	// no participation yet
	if s.hasParticipation(key, "", "C1", "T1") {
		t.Fatal("expected no participation initially")
	}

	// mark participation
	s.markParticipation(key, "", "C1", "T1")

	if !s.hasParticipation(key, "", "C1", "T1") {
		t.Fatal("expected participation after marking")
	}

	// different thread = no participation
	if s.hasParticipation(key, "", "C1", "T2") {
		t.Fatal("expected no participation for different thread")
	}

	// empty route = no-op
	s.markParticipation(key, "", "", "")
	if s.hasParticipation(key, "", "", "") {
		t.Fatal("expected no participation for empty route")
	}
}

func TestResolveSelector(t *testing.T) {
	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot": {Service: "slack", Name: "ops-bot"},
			"slack:eng-bot": {Service: "slack", Name: "eng-bot"},
			"discord:ops":   {Service: "discord", Name: "ops"},
		},
	}

	// specific bot
	keys, err := s.resolveSelector("slack", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != "slack:ops-bot" {
		t.Fatalf("unexpected keys: %v", keys)
	}

	// all bots for a service
	keys, err = s.resolveSelector("slack", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 slack bots, got %d", len(keys))
	}

	// all bots
	keys, err = s.resolveSelector("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 total bots, got %d", len(keys))
	}

	// unknown bot
	_, err = s.resolveSelector("slack", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown bot")
	}

	// unknown service
	_, err = s.resolveSelector("matrix", "")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}

	// bot name without service - should find across all services
	keys, err = s.resolveSelector("", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != "slack:ops-bot" {
		t.Fatalf("expected [slack:ops-bot], got %v", keys)
	}

	// unknown bot without service
	_, err = s.resolveSelector("", "ghost")
	if err == nil {
		t.Fatal("expected error for unknown bot without service")
	}
}

func TestResolveBotService(t *testing.T) {
	s := &Server{
		bots: map[string]protocol.BotRef{
			"slack:ops-bot":  {Service: "slack", Name: "ops-bot"},
			"slack:eng-bot":  {Service: "slack", Name: "eng-bot"},
			"discord:ops":    {Service: "discord", Name: "ops"},
			"telegram:alert": {Service: "telegram", Name: "alert"},
		},
	}

	// explicit service passthrough
	svc, bot, err := s.resolveBotService("slack", "ops-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != "slack" || bot != "ops-bot" {
		t.Fatalf("expected slack/ops-bot, got %s/%s", svc, bot)
	}

	// resolve bot without service - unique bot name
	svc, bot, err = s.resolveBotService("", "ops")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != "discord" || bot != "ops" {
		t.Fatalf("expected discord/ops, got %s/%s", svc, bot)
	}

	// unknown bot
	_, _, err = s.resolveBotService("", "ghost")
	if err == nil {
		t.Fatal("expected error for unknown bot")
	}

	// empty bot name
	_, _, err = s.resolveBotService("", "")
	if err == nil {
		t.Fatal("expected error for empty bot")
	}

	// ambiguous bot - add a duplicate name across services
	s.bots["telegram:ops"] = protocol.BotRef{Service: "telegram", Name: "ops"}
	_, _, err = s.resolveBotService("", "ops")
	if err == nil {
		t.Fatal("expected error for ambiguous bot")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got: %v", err)
	}
}
