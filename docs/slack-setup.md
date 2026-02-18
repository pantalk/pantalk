# Slack Setup

Pantalk connects to Slack using **Socket Mode** (WebSocket), which means no public URL or webhook endpoint is needed.

## Prerequisites

- A Slack workspace where you have admin or app management permissions
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 — Create a Slack App

Go to [api.slack.com/apps](https://api.slack.com/apps) and click **Create New App** → **From scratch**.

Give it a name (e.g. `Pantalk Agent`) and select your workspace.

## Step 2 — Enable Socket Mode

**Settings → Socket Mode** → toggle **ON**.

This lets Pantalk receive events over a WebSocket connection instead of requiring a public HTTP endpoint.

## Step 3 — Generate an App-Level Token

**Settings → Basic Information → App-Level Tokens** → **Generate Token**:

- Give it a name (e.g. `pantalk`)
- Add the **`connections:write`** scope
- Click **Generate**
- Copy the token (`xapp-...`) — this is your `app_level_token`

## Step 4 — Add Bot Token Scopes

**Features → OAuth & Permissions → Scopes → Bot Token Scopes** — add:

| Scope               | Purpose                                         |
| -------------------- | ----------------------------------------------- |
| `chat:write`         | Send messages                                   |
| `channels:history`   | Receive messages in public channels             |
| `app_mentions:read`  | Receive @mention events                         |
| `groups:history`     | Receive messages in private channels (optional) |
| `im:history`         | Receive direct messages (optional)              |

## Step 5 — Subscribe to Bot Events

**Features → Event Subscriptions** → toggle **ON**, then under **Subscribe to bot events** add:

| Event              | Purpose                                             |
| ------------------ | --------------------------------------------------- |
| `app_mention`      | Fires when someone @mentions your bot               |
| `message.channels` | Fires for all messages in public channels bot is in |
| `message.groups`   | Fires for messages in private channels (optional)   |
| `message.im`       | Fires for direct messages to the bot (optional)     |

Click **Save Changes** at the bottom.

## Step 6 — Install the App

**Features → OAuth & Permissions** → **Install to Workspace** (or **Reinstall** if you changed scopes).

Copy the **Bot User OAuth Token** (`xoxb-...`) — this is your `bot_token`.

> **Important:** You must reinstall the app every time you change scopes or event subscriptions.

## Step 7 — Invite the Bot to a Channel

The bot must be **in a channel** to receive events from it. In Slack, type:

```
/invite @YourBotName
```

Note the channel ID — you can find it in the channel details (it starts with `C`).

## Step 8 — Configure Pantalk

Set your environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-..."        # from step 6
export SLACK_APP_LEVEL_TOKEN="xapp-..."   # from step 3
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-slack-bot
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN
    channels:
      - C0123456789    # replace with your channel ID
```

Token fields accept either a literal string or an environment variable reference (`$VAR` or `${VAR}`).

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Slack bot listed. Send a test message:

```bash
pantalk send --bot my-slack-bot --channel C0123456789 --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                            | Cause                                                                       |
| ---------------------------------- | --------------------------------------------------------------------------- |
| `auth failed` on startup           | Invalid `bot_token` — check the `xoxb-` token is correct                    |
| No `socket mode connected` log     | Socket Mode is not enabled, or `app_level_token` is wrong                   |
| Connected but no events arrive     | Missing event subscriptions (step 5) or bot not invited to channel (step 7) |
| Events arrive but no notifications | Message doesn't @mention the bot and isn't a DM                             |
| `warning: no events received` log  | Likely missing event subscriptions — see step 5                             |
