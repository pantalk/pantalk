# Zulip Setup

Pantalk connects to Zulip using the **REST API** for sending messages and the **event queue** (long-poll) system for real-time events. Zulip requires an `endpoint` pointing to your Zulip server, a bot email, and an API key.

## Prerequisites

- A Zulip server (self-hosted or Zulip Cloud) with admin access
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 - Create a Bot

1. Go to your Zulip instance → **Settings → Your bots** (or navigate to `/#settings/your-bots`)
2. Click **Add a new bot**
3. Fill in:
   - **Name** - e.g. `Pantalk Agent`
   - **Bot type** - **Generic bot** (recommended) or **Incoming webhook**
4. Click **Create bot**
5. Copy the **API key** - this is your `api_key`
6. Note the **Bot email** - this is your `bot_email` (e.g. `pantalk-bot@your-org.zulipchat.com`)

> **Note:** You can also download the bot's `zuliprc` file which contains the email and API key.

## Step 2 - Get the Server Endpoint

Your Zulip endpoint is the base URL of your server, e.g.:

```
https://your-org.zulipchat.com
```

For self-hosted Zulip:

```
https://zulip.example.com
```

This must be reachable from the machine running Pantalk.

## Step 3 - Subscribe the Bot to Streams

The bot must be subscribed to each stream (channel) it should monitor:

1. Open the stream settings in Zulip
2. Go to **Subscribers** → add your bot's email
3. Alternatively, use the Zulip API:

```bash
curl -u "pantalk-bot@your-org.zulipchat.com:$ZULIP_API_KEY" \
  https://your-org.zulipchat.com/api/v1/users/me/subscriptions \
  -d 'subscriptions=[{"name": "general"}]'
```

## Step 4 - Get Stream IDs

To find a stream ID in Zulip:

1. Go to **Settings → Subscribed streams**
2. Click on the stream name
3. The stream ID is visible in the URL (e.g. `/#streams/123/general` → stream ID is `123`)

Alternatively, use the API:

```bash
curl -u "pantalk-bot@your-org.zulipchat.com:$ZULIP_API_KEY" \
  https://your-org.zulipchat.com/api/v1/streams
```

## Step 5 - Configure Pantalk

Set your environment variables:

```bash
export ZULIP_API_KEY="your-bot-api-key-here"
export ZULIP_BOT_EMAIL="pantalk-bot@your-org.zulipchat.com"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-zulip-bot
    type: zulip
    api_key: $ZULIP_API_KEY
    bot_email: $ZULIP_BOT_EMAIL
    endpoint: https://your-org.zulipchat.com
    channels:
      - general  # stream name (resolved to stream ID at startup)
```

Channels accept either friendly stream names (e.g. `general`, `engineering`) or raw numeric stream IDs (e.g. `123`). Stream names are resolved to IDs automatically when the daemon starts.

> **Important:** All three fields (`endpoint`, `api_key`, `bot_email`) are **required** for Zulip.

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Zulip bot listed. Send a test message:

```bash
pantalk send --bot my-zulip-bot --channel 123 --thread "test topic" --text "Hello from Pantalk!"
```

> **Note:** Zulip uses topics (threads) within streams. Use the `--thread` flag to specify the topic when sending messages.

## Troubleshooting

| Symptom                         | Cause                                                                     |
| ------------------------------- | ------------------------------------------------------------------------- |
| `auth failed` on startup        | Invalid API key or email - regenerate the bot's API key in Zulip settings |
| `connection refused` or timeout | `endpoint` is wrong or the server is unreachable from the Pantalk host    |
| Connected but no events         | Bot is not subscribed to the stream, or stream ID is wrong                |
| Bot can't post messages         | Bot lacks permissions - check organization bot policies                   |
| `register queue failed`         | API key may have expired or bot account was deactivated                   |
