# XMPP / Jabber Setup

Pantalk connects as a normal authenticated XMPP client using
[Mellium](https://mellium.im/xmpp/). It supports direct messages, configured
XEP-0045 multi-user chat (MUC) rooms, message threads, presence, and XEP-0085
typing indicators. No webhook or public URL is required.

## Prerequisites

- An XMPP account on a hosted or self-hosted service such as Prosody,
  ejabberd, or Snikket
- The account password
- The JIDs of any MUC rooms Pantalk should join

Using a dedicated bot account is recommended. The account must already be
allowed to join each configured room.

## Configure Pantalk

Store the account password in an environment variable:

```bash
export XMPP_PASSWORD="your-account-password"
```

Add the account to the Pantalk configuration:

```yaml
bots:
  - name: helper
    type: xmpp
    display_name: PanTalk Helper
    jid: helper@example.com
    password: $XMPP_PASSWORD
    channels:
      - agents@conference.example.com
      - support@conference.example.com
```

`jid` must be a complete account JID such as `helper@example.com`.

By default Mellium discovers the client endpoint through the account domain's
XMPP SRV records. For a development server or a deployment without SRV records,
set an explicit STARTTLS endpoint:

```yaml
    endpoint: xmpp.internal.example.com:5222
```

A bare hostname is accepted and gets port `5222` automatically. The endpoint
is a TCP `host:port`, not an HTTP URL. TLS certificate validation still uses
the domain from `jid`, so the server certificate must cover that domain.

### Configuration reference

| Field | Purpose | Required |
| --- | --- | --- |
| `type` | Must be `xmpp` | Yes |
| `jid` | Account JID, for example `helper@example.com` | Yes |
| `password` | Account password; supports `$ENV_VAR` syntax | Yes |
| `display_name` | Nickname used when joining MUC rooms; defaults to `name` | No |
| `endpoint` | Optional STARTTLS `host:port` override; blank uses SRV discovery | No |
| `channels` | MUC room JIDs to join and accept messages from | No |

Direct messages are accepted even when `channels` is empty. Groupchat messages
are accepted only from configured rooms.

## Sending messages

Use a configured room JID as the channel:

```bash
pantalk send \
  --bot helper \
  --channel agents@conference.example.com \
  --text "Hello from Pantalk"
```

Targets can explicitly select a room or direct message:

```text
room:agents@conference.example.com
dm:ada@example.com
xmpp:room:agents@conference.example.com
xmpp:dm:ada@example.com
```

A raw target JID is treated as a direct message unless it matches a configured
room. A raw value in the request's `channel` field is always treated as a MUC
room.

Pantalk preserves the XMPP `<thread>` value in inbound and outbound event
threads. Outbound Markdown and HTML are converted to plain text for broad
client compatibility.

## Presence and typing

Pantalk sends initial available presence after authentication and joins each
configured MUC with `display_name` as its nickname. Incoming contact and room
presence is emitted as `presence` events.

The connector implements XEP-0085 chat states. Pantalk typing leases send
`<composing/>`, completed messages send `<active/>`, and incoming chat states
are emitted as `typing` events. It also responds to XEP-0199 server pings to
keep long-running sessions healthy.

`presence` and `typing` events are observational: they reach `pantalk stream`
subscribers but never start an agent turn, so a contact going idle or typing in
a DM does not wake your harness. Only messages do.

## Verify

Start the daemon:

```bash
pantalkd
```

The log should contain entries similar to:

```text
[xmpp:helper] authenticated as helper@example.com/<resource>
[xmpp:helper] joining agents@conference.example.com as PanTalk Helper
[xmpp:helper] joined agents@conference.example.com
```

The second line means the join was sent; the third means the room accepted it.
If the third never appears, the room reports why:

```text
[xmpp:helper] cannot join agents@conference.example.com: registration-required
```

Send a direct message or a room message from another account and confirm it
appears in Pantalk history.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `requires jid` | The account JID is missing |
| `SASL` or authentication failure | The password is wrong or the server does not permit password authentication |
| Certificate validation failure | The TLS certificate does not cover the account JID's domain |
| Connection timeout with no `endpoint` | XMPP SRV records are missing or incorrect; configure `host:5222` explicitly |
| Room messages are absent | Add the bare MUC room JID to `channels`, then check the log for `cannot join <room>` - the room states its reason there |
| `cannot join <room>: registration-required` | The room is members-only; grant the bot account membership |
| `cannot join <room>: conflict` | Another session already holds that nickname; change `display_name` |
| Messages appear twice | Another resource may be forwarding carbons; use a dedicated bot account |
