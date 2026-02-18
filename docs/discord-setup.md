# Discord Setup

Pantalk connects to Discord using the **Gateway** (WebSocket), which means no public URL or webhook endpoint is needed.

## Prerequisites

- A Discord account with permission to create applications
- A Discord server (guild) where you have the **Manage Server** permission
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 — Create a Discord Application

Go to [discord.com/developers/applications](https://discord.com/developers/applications) and click **New Application**.

Give it a name (e.g. `Pantalk Agent`) and click **Create**.

## Step 2 — Create a Bot

In your application settings, go to **Bot** → click **Add Bot** (if not already created).

Under the **Bot** section:

- Optionally set a username and avatar
- Under **Privileged Gateway Intents**, enable:
  - **Message Content Intent** — required for Pantalk to read message text

> **Important:** The Message Content Intent is required. Without it, Pantalk will receive events but message text will be empty.

## Step 3 — Copy the Bot Token

In the **Bot** section, click **Reset Token** (or **Copy** if visible).

Copy the token — this is your `bot_token`.

> **Warning:** The token is only shown once. Store it securely.

## Step 4 — Generate an Invite URL

Go to **OAuth2 → URL Generator**:

1. Under **Scopes**, select: `bot`
2. Under **Bot Permissions**, select:
   - `Send Messages`
   - `Read Message History`
   - `View Channels`

Copy the generated URL at the bottom.

## Step 5 — Invite the Bot to Your Server

Open the URL from step 4 in your browser. Select your server and click **Authorize**.

The bot should now appear in your server's member list (it will be offline until Pantalk connects).

## Step 6 — Get Channel IDs

Enable **Developer Mode** in Discord:
- **User Settings → Advanced → Developer Mode** → toggle ON

Right-click any channel → **Copy Channel ID**.

Discord channel IDs are numeric strings (e.g. `123456789012345678`).

## Step 7 — Configure Pantalk

Set your environment variable:

```bash
export DISCORD_BOT_TOKEN="your-bot-token-here"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-discord-bot
    type: discord
    bot_token: $DISCORD_BOT_TOKEN
    channels:
      - '123456789012345678'    # replace with your channel ID
```

> **Note:** Channel IDs should be quoted strings in YAML since they are numeric.

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Discord bot listed. Send a test message:

```bash
pantalk send --bot my-discord-bot --channel 123456789012345678 --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                            | Cause                                                                    |
| ---------------------------------- | ------------------------------------------------------------------------ |
| Bot connects but no messages       | **Message Content Intent** not enabled (step 2)                          |
| `authentication failed`            | Invalid bot token — regenerate in the Developer Portal                   |
| Bot not in channel list            | Bot wasn't invited to the server, or lacks **View Channels** permission  |
| Events arrive but text is empty    | Message Content Intent is disabled — enable it in the Developer Portal   |
| Bot appears offline in Discord     | `pantalkd` is not running, or the bot is not configured in the config    |
