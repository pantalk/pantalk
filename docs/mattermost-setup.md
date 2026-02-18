# Mattermost Setup

Pantalk connects to Mattermost using **WebSocket** for real-time events and the **REST API** for sending messages. Unlike Slack and Discord, Mattermost requires an `endpoint` pointing to your Mattermost server.

## Prerequisites

- A Mattermost server (self-hosted or cloud) with admin access
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 - Create a Bot Account

1. Go to your Mattermost instance → **Main Menu → Integrations → Bot Accounts**
2. Click **Add Bot Account**
3. Fill in:
   - **Username** - e.g. `pantalk-bot`
   - **Display Name** - e.g. `Pantalk Agent`
   - **Role** - `Member` (or `System Admin` if you need cross-team access)
4. Click **Create Bot Account**
5. Copy the **Access Token** - this is your `bot_token`

> **Note:** If Bot Accounts are not available, ask your Mattermost admin to enable them in **System Console → Integrations → Bot Accounts → Enable Bot Account Creation**.

## Step 2 - Get the Server Endpoint

Your Mattermost endpoint is the base URL of your server, e.g.:

```
https://mattermost.example.com
```

This must be reachable from the machine running Pantalk.

## Step 3 - Add the Bot to Channels

The bot must be added to each channel it should monitor:

1. Open the channel in Mattermost
2. Click the channel name → **Add Members** → search for your bot username
3. Add the bot to the channel

## Step 4 - Get Channel IDs

To find a channel ID in Mattermost:

1. Open the channel
2. Click the channel name → **View Info**
3. The channel ID is shown at the bottom of the info panel (a 26-character alphanumeric string)

Alternatively, use the Mattermost API:

```bash
curl -H "Authorization: Bearer $MATTERMOST_BOT_TOKEN" \
  https://mattermost.example.com/api/v4/channels/search \
  -d '{"term": "channel-name"}'
```

## Step 5 - Configure Pantalk

Set your environment variable:

```bash
export MATTERMOST_BOT_TOKEN="your-bot-token-here"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-mattermost-bot
    type: mattermost
    bot_token: $MATTERMOST_BOT_TOKEN
    endpoint: https://mattermost.example.com
    channels:
      - a1b2c3d4e5f6g7h8i9j0klmnop    # replace with your channel ID
```

> **Important:** The `endpoint` field is **required** for Mattermost (it's the only platform that needs it).

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Mattermost bot listed. Send a test message:

```bash
pantalk send --bot my-mattermost-bot --channel a1b2c3d4e5f6g7h8i9j0klmnop --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                            | Cause                                                                          |
| ---------------------------------- | ------------------------------------------------------------------------------ |
| `connection refused` or timeout    | `endpoint` is wrong or the server is unreachable from the Pantalk host         |
| `401 Unauthorized`                 | Invalid bot token - regenerate it in Mattermost Integrations                   |
| Connected but no events            | Bot is not added to the channel, or channel ID is wrong                        |
| WebSocket disconnects frequently   | Network instability or proxy/firewall blocking WebSocket upgrades              |
| Bot can't post messages            | Bot lacks permissions in the channel - re-add it or check channel permissions  |
