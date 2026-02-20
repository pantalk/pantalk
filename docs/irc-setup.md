# IRC Setup

Pantalk connects to IRC servers using a standard TCP connection (with optional TLS) and the IRC protocol. No special API keys or app registrations are needed — just a server address and a nickname.

## Prerequisites

- An IRC server to connect to (e.g. [Libera.Chat](https://libera.chat), [OFTC](https://www.oftc.net), or your own)
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 - Choose an IRC Server

Popular public IRC networks:

| Network      | Address                   | TLS Port | Plain Port |
| ------------ | ------------------------- | -------- | ---------- |
| Libera.Chat  | `irc.libera.chat`         | 6697     | 6667       |
| OFTC         | `irc.oftc.net`            | 6697     | 6667       |
| EFnet        | `irc.efnet.org`           | 6697     | 6667       |
| Self-hosted  | your server address       | varies   | varies     |

TLS (port 6697) is recommended and used by default when no port is specified.

## Step 2 - Pick a Nickname

Your bot's `name` in the Pantalk config becomes the IRC nickname. IRC nicknames:

- Must be 1–30 characters (varies by server)
- Can contain letters, numbers, hyphens, and underscores
- Cannot start with a number or hyphen
- Must be unique on the network

If the nickname is already taken, Pantalk will automatically append `_` and retry.

## Step 3 - Register the Nickname (Optional)

On networks that support NickServ, you can register your bot's nickname to prevent others from using it:

1. Connect manually or via Pantalk
2. Send to NickServ:

```
/msg NickServ REGISTER <password> <email>
```

3. Use the registered password as `password` in Pantalk config (NickServ identification is not yet automatic, but the server password field can be used for servers that support PASS-based auth)

## Step 4 - Get Channel Names

IRC channels start with `#` (network-wide) or `&` (server-local). Find channels to join:

- Ask the network: `/list` (may be rate-limited on large networks)
- Check the network's website for channel directories
- Create your own: just join a non-existent channel name

> **Note:** Pantalk automatically prepends `#` to channel names that don't start with `#` or `&`.

## Step 5 - Configure Pantalk

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-irc-bot
    type: irc
    endpoint: irc.libera.chat:6697
    channels:
      - '#mychannel'
      - '#anotherchannel'
```

### Optional: Server Password

If the IRC server requires a password (or for PASS-based authentication):

```bash
export IRC_SERVER_PASSWORD="your-password"
```

```yaml
bots:
  - name: my-irc-bot
    type: irc
    endpoint: irc.libera.chat:6697
    password: $IRC_SERVER_PASSWORD
    channels:
      - '#mychannel'
```

### Optional: Display Name

Set a custom "real name" shown in WHOIS responses:

```yaml
bots:
  - name: my-irc-bot
    type: irc
    endpoint: irc.libera.chat:6697
    display_name: My AI Agent
    channels:
      - '#mychannel'
```

### TLS vs Plain Text

TLS is used automatically when connecting to port 6697. For plain text connections, use port 6667:

```yaml
bots:
  - name: my-irc-bot
    type: irc
    endpoint: irc.example.com:6667    # plain text
    channels:
      - '#mychannel'
```

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your IRC bot listed. Send a test message:

```bash
pantalk send --bot my-irc-bot --channel '#mychannel' --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                          | Cause                                                                          |
| -------------------------------- | ------------------------------------------------------------------------------ |
| Connection refused               | Wrong server address or port — verify the endpoint                             |
| TLS handshake error              | Server doesn't support TLS on that port — try port 6667 for plain text         |
| Nickname in use                  | Another user has the nick — Pantalk retries with `_` suffix automatically      |
| Not receiving messages           | Bot may not have joined the channel — check daemon logs for JOIN confirmation  |
| Kicked from channel              | Bot was kicked — Pantalk auto-rejoins, but check channel permissions           |
| No messages from channel         | Channels must be listed in config, or use an empty channels list for all       |
