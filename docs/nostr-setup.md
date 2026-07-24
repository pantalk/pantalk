# Nostr Setup

Pantalk connects directly to Nostr relays using
[`go-nostr`](https://github.com/nbd-wtf/go-nostr). It signs all outbound events
with the configured bot key and supports NIP-17 encrypted direct messages,
NIP-28 public channels, and ordinary NIP-29 group chat messages.

## Prerequisites

- A dedicated Nostr private key for the bot
- One or more WebSocket relay URLs
- Any NIP-28 channel event IDs or relay-scoped NIP-29 group IDs the bot should
  join

Keep the private key in an environment variable:

```bash
export NOSTR_PRIVATE_KEY="nsec1..."
```

Both `nsec` and 64-character hexadecimal private keys are accepted. Pantalk
derives the public key; never put a private key in `channels` or a send target.

## Configure Pantalk

```yaml
bots:
  - name: nostr-helper
    type: nostr
    private_key: $NOSTR_PRIVATE_KEY
    relays:
      - wss://relay.example.com
      - wss://relay2.example.com
    channels:
      - nip28:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      - nip29:wss://relay.example.com'engineering
```

### Configuration reference

| Field | Purpose | Required |
| --- | --- | --- |
| `type` | Must be `nostr` | Yes |
| `private_key` | Bot signing key; supports `$ENV_VAR` syntax | Yes |
| `relays` | `ws://` or `wss://` relay URLs used for subscriptions and publishing | Yes |
| `channels` | NIP-28 channel event IDs and relay-scoped NIP-29 group IDs | No |

NIP-29 group IDs are meaningful only together with their relay. Pantalk
therefore requires the form `nip29:<relay-url>'<group-id>` and rejects an
unscoped group ID.

## Direct messages (NIP-17)

Pantalk listens for NIP-17 gift wraps on the configured relays and publishes a
signed kind-10050 DM relay list when it connects, allowing other NIP-17 clients
to discover the bot's inboxes. A recipient must likewise publish a kind-10050
relay list before Pantalk can send them a DM.

DM targets accept hexadecimal public keys, `npub`, or `nprofile` values:

```bash
pantalk send \
  --bot nostr-helper \
  --target dm:npub1... \
  --text "Private hello"
```

`nostr:dm:<key>` is also accepted. Pantalk creates both recipient and sender
copies as required by NIP-17 and ignores the sender copy when it returns
through the bot's subscription.

## NIP-28 channels

The channel identifier is the 64-character event ID of the kind-40 channel
creation event:

```bash
pantalk send \
  --bot nostr-helper \
  --channel nip28:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  --text "Hello channel"
```

A raw 64-character channel event ID and the prefixes `channel:` and
`nostr:nip28:` are accepted as well. Pantalk subscribes only to configured
NIP-28 channels. A `thread` value becomes a NIP-10-style reply event reference.

## NIP-29 groups

Use the same relay-scoped identifier from the configuration:

```bash
pantalk send \
  --bot nostr-helper \
  --channel "nip29:wss://relay.example.com'engineering" \
  --text "Hello group"
```

Pantalk handles ordinary kind-9 group chat messages carrying the required `h`
tag. Group creation, invitations, moderation events, and kind-10/11/12 thread
objects remain the responsibility of a NIP-29 client or relay administrator.
Threaded NIP-29 sends are rejected rather than silently flattened.

## Verify

Start the daemon and inspect its status:

```bash
pantalkd &
pantalk bots
```

The daemon emits `connector online` after it has connected to at least one
relay and successfully advertised the bot's DM relay list. Send a message from
another key and confirm it appears in `pantalk history`.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `private key must be an nsec or 64-character hex key` | The secret is malformed or the environment variable did not resolve |
| `relay must use ws:// or wss://` | An HTTP URL was supplied instead of a relay WebSocket URL |
| `recipient has no discoverable ... DM relay list` | The recipient has not published a NIP-17 kind-10050 inbox list |
| NIP-28 messages are absent | The kind-40 channel event ID is not listed in `channels` |
| NIP-29 messages are absent | The group ID is paired with the wrong relay, or the bot is not a group member |
| Relay rejects signed events | The relay requires authentication, membership, payment, or write permission |
