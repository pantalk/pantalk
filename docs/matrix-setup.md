# Matrix Setup

Pantalk connects to Matrix using the **Client-Server API** (via [mautrix-go](https://github.com/mautrix/go)). It authenticates with an access token and receives messages through the `/sync` long-poll endpoint — no webhooks or public URLs are needed.

## Prerequisites

- A Matrix account on any homeserver (e.g. matrix.org, Element, self-hosted Synapse/Conduit)
- An access token for the account
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 — Create a Matrix Account

Use any Matrix client (e.g. [Element](https://element.io)) to create a dedicated bot account, or use an existing one.

For a self-hosted homeserver you can register via the admin API or `register_new_matrix_user` command.

## Step 2 — Obtain an Access Token

There are several ways to get an access token:

### Option A — From Element

1. Log in to [Element Web](https://app.element.io)
2. Go to **Settings → Help & About → Advanced**
3. Your access token is displayed under **Access Token**

> **Warning:** Logging out of Element invalidates the token. Use a dedicated session for bot accounts.

### Option B — Via the API

```bash
curl -X POST "https://matrix.example.com/_matrix/client/v3/login" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "m.login.password",
    "identifier": { "type": "m.id.user", "user": "my-bot" },
    "password": "your-password",
    "initial_device_display_name": "Pantalk"
  }'
```

The response contains `access_token` — save this value.

## Step 3 — Invite the Bot to Rooms

In your Matrix client, invite the bot user to the rooms where it should listen and respond.

The bot will receive messages from all joined rooms unless you configure a `channels` allowlist.

## Step 4 — Get Room IDs

Room IDs look like `!abc123:matrix.org`. You can find them in Element:

1. Open the room
2. Go to **Room Settings → Advanced**
3. The **Internal room ID** is displayed (e.g. `!opaque_id:matrix.org`)

## Step 5 — Configure Pantalk

Set your environment variable:

```bash
export MATRIX_ACCESS_TOKEN="your-access-token-here"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-matrix-bot
    type: matrix
    access_token: $MATRIX_ACCESS_TOKEN
    endpoint: https://matrix.example.com    # your homeserver URL
    channels:
      - '!abc123:matrix.org'                # replace with your room ID
```

### Configuration Reference

| Field      | Purpose                                                    | Required |
| ---------- | ---------------------------------------------------------- | -------- |
| `type`     | Must be `matrix`                                           | Yes      |
| `access_token`| Access token (supports `$ENV_VAR` syntax)                  | Yes      |
| `endpoint` | Homeserver URL (e.g. `https://matrix.org`)                 | Yes      |
| `channels` | Allowlist of room IDs to listen to (empty = all rooms)     | No       |

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
```

You should see:

```
[matrix:my-matrix-bot] authenticated (user=@my-bot:matrix.org)
```

Send a test message from another account in one of the configured rooms and confirm it appears in `pantalk history`.

## Troubleshooting

| Symptom                          | Likely cause                                      |
| -------------------------------- | ------------------------------------------------- |
| `matrix whoami: M_UNKNOWN_TOKEN` | Access token is invalid or expired                |
| `matrix send: M_FORBIDDEN`       | Bot not invited to the room or lacks permission   |
| No messages received              | Bot not joined to any rooms, or room not in `channels` allowlist |
| `endpoint` error                  | Homeserver URL is incorrect or unreachable        |
