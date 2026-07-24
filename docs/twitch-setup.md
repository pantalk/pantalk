# Twitch Setup

Pantalk connects to Twitch chat over Twitch's IRC-over-TLS endpoint. The
connector requests Twitch's IRCv3 tags, commands, and membership capabilities,
so inbound messages include stable user IDs, timestamps, and message IDs.

## Prerequisites

- A Twitch account dedicated to the bot
- A Twitch user access token for that same account with the `chat:read` and
  `chat:edit` scopes
- The channel login names whose chats the bot should join

Twitch chat authentication requires a **user** access token. An app access
token from the client-credentials flow cannot authenticate an IRC chat user.

## Step 1 - Create a User Access Token

Register an application in the
[Twitch developer console](https://dev.twitch.tv/console/apps), then use one of
Twitch's user authorization flows to obtain a user access token with:

- `chat:read`
- `chat:edit`

The token must belong to the account named by the Pantalk bot's `username`.

Store the token in an environment variable rather than writing it directly in
the configuration:

```bash
export TWITCH_BOT_TOKEN="your-user-access-token"
```

Both the raw access token and Twitch's `oauth:<token>` IRC form are accepted.

## Step 2 - Configure Pantalk

Add the bot and the channels it should join:

```yaml
bots:
  - name: stream-helper
    type: twitch
    username: mytwitchbot
    access_token: $TWITCH_BOT_TOKEN
    channels:
      - twitch
      - some_streamer
```

The `name` identifies the bot inside Pantalk, while `username` is the Twitch
account's login name. Channel names are case-insensitive; a leading `#` is
optional.

Pantalk connects to `irc.chat.twitch.tv:6697` by default. An alternate
IRC-over-TLS endpoint can be set for a compatible proxy or test server:

```yaml
bots:
  - name: stream-helper
    type: twitch
    username: mytwitchbot
    access_token: $TWITCH_BOT_TOKEN
    endpoint: irc.chat.twitch.tv:6697
    channels:
      - '#twitch'
```

## Verify

Start the daemon and check that the bot is listed:

```bash
pantalkd &
pantalk bots
```

Send a message:

```bash
pantalk send \
  --bot stream-helper \
  --channel twitch \
  --text "Hello from Pantalk!"
```

Inbound replies carry Twitch's root thread ID as the Pantalk thread ID; an
ordinary top-level chat message has no thread, so all of a channel's chat shares
one conversation. Sending with a thread ID creates a native Twitch reply:

```bash
pantalk send \
  --bot stream-helper \
  --channel twitch \
  --thread 885196de-cb67-427a-baa8-82f9b0fcd05f \
  --text "Thanks for the question!"
```

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `Login authentication failed` | The token is invalid, expired, or belongs to a different account than the configured Twitch username |
| Bot connects but receives no messages | Ensure the channel is listed and the token has `chat:read` |
| Messages are not sent | Ensure the token has `chat:edit`; Twitch may also require the bot account to have a verified phone number |
| A channel cannot be joined | Use the channel owner's Twitch login name, not the channel display name |
| A long reply arrives in pieces over a minute or two | Expected - see rate limiting below |

## Rate limiting

Twitch allows an account with no elevated role 20 chat messages per 30 seconds
and answers a breach with a 30-minute chat ban. Pantalk sends one message per
line of a reply, so the connector paces its own sending to stay inside that
budget: once 20 messages have gone out, the rest of a long reply trickles into
the channel as the window rolls forward. The budget is shared across all
channels a bot is in.

Granting the bot account moderator status in a channel raises Twitch's own
ceiling, but Pantalk still paces to the conservative limit.

The connector reconnects with exponential backoff when Twitch closes the
connection or sends its `RECONNECT` command, resetting the delay once a session
is established. It also answers server `PING` messages automatically.
