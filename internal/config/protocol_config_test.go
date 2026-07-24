package config

import (
	"strings"
	"testing"
)

func TestLoadXMPPConfig(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: jabber-bot
    type: xmpp
    jid: agent@example.com
    password: $PANTALK_XMPP_PASSWORD
    endpoint: xmpp.example.com:5222
    channels:
      - engineering@conference.example.com
`)
	t.Setenv("PANTALK_XMPP_PASSWORD", "secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load XMPP config: %v", err)
	}
	if got := cfg.Bots[0].JID; got != "agent@example.com" {
		t.Fatalf("jid = %q", got)
	}
}

func TestLoadXMPPRequiresJIDAndPassword(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		missing string
	}{
		{
			name: "jid",
			config: `
bots:
  - name: jabber-bot
    type: xmpp
    password: secret
`,
			missing: "jid",
		},
		{
			name: "password",
			config: `
bots:
  - name: jabber-bot
    type: xmpp
    jid: agent@example.com
`,
			missing: "password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.config))
			if err == nil || !strings.Contains(err.Error(), tt.missing) {
				t.Fatalf("error = %v, want missing %s", err, tt.missing)
			}
		})
	}
}

func TestLoadTwitchConfig(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: stream-bot
    type: twitch
    username: pantalkbot
    access_token: $PANTALK_TWITCH_TOKEN
    channels: [pantalkdev]
`)
	t.Setenv("PANTALK_TWITCH_TOKEN", "oauth:token")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load Twitch config: %v", err)
	}
	if got := cfg.Bots[0].Username; got != "pantalkbot" {
		t.Fatalf("username = %q", got)
	}
}

func TestLoadTwitchRequiresCredentialsAndChannel(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		missing string
	}{
		{
			name: "username",
			config: `
bots:
  - name: stream-bot
    type: twitch
    access_token: token
    channels: [pantalkdev]
`,
			missing: "username",
		},
		{
			name: "access token",
			config: `
bots:
  - name: stream-bot
    type: twitch
    username: pantalkbot
    channels: [pantalkdev]
`,
			missing: "access_token",
		},
		{
			name: "channel",
			config: `
bots:
  - name: stream-bot
    type: twitch
    username: pantalkbot
    access_token: token
`,
			missing: "channel",
		},
		{
			name: "blank channel",
			config: `
bots:
  - name: stream-bot
    type: twitch
    username: pantalkbot
    access_token: token
    channels: [" "]
`,
			missing: "channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.config))
			if err == nil || !strings.Contains(err.Error(), tt.missing) {
				t.Fatalf("error = %v, want missing %s", err, tt.missing)
			}
		})
	}
}

func TestLoadNostrConfig(t *testing.T) {
	path := writeConfig(t, `
bots:
  - name: nostr-bot
    type: nostr
    private_key: $PANTALK_NOSTR_PRIVATE_KEY
    relays:
      - wss://relay.example.com
    channels:
      - nip28:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`)
	t.Setenv("PANTALK_NOSTR_PRIVATE_KEY", strings.Repeat("a", 64))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load Nostr config: %v", err)
	}
	if got := cfg.Bots[0].Relays; len(got) != 1 || got[0] != "wss://relay.example.com" {
		t.Fatalf("relays = %#v", got)
	}
}

func TestLoadNostrRequiresPrivateKeyAndRelay(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		missing string
	}{
		{
			name: "private key",
			config: `
bots:
  - name: nostr-bot
    type: nostr
    relays: [wss://relay.example.com]
`,
			missing: "private_key",
		},
		{
			name: "relay",
			config: `
bots:
  - name: nostr-bot
    type: nostr
    private_key: secret
`,
			missing: "relay",
		},
		{
			name: "blank relay",
			config: `
bots:
  - name: nostr-bot
    type: nostr
    private_key: secret
    relays: [" "]
`,
			missing: "relay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.config))
			if err == nil || !strings.Contains(err.Error(), tt.missing) {
				t.Fatalf("error = %v, want missing %s", err, tt.missing)
			}
		})
	}
}
