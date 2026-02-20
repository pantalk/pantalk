# Telegram Setup

Pantalk connects to Telegram using the **Bot API** with long-polling (`getUpdates`), which means no public URL or webhook endpoint is needed.

## Prerequisites

- A Telegram account
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 - Create a Bot via BotFather

1. Open Telegram and search for **@BotFather**
2. Send `/newbot`
3. Follow the prompts:
   - Choose a **display name** (e.g. `Pantalk Agent`)
   - Choose a **username** (must end in `bot`, e.g. `pantalk_agent_bot`)
4. BotFather will reply with your **bot token** - copy it

The token looks like: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`

## Step 2 - Configure Bot Settings (Optional)

Send these commands to **@BotFather** to customize your bot:

| Command           | Purpose                                      |
| ----------------- | -------------------------------------------- |
| `/setdescription` | Set the bot's description shown in its profile |
| `/setabouttext`   | Set the "About" text                         |
| `/setuserpic`     | Set the bot's profile picture                |
| `/setjoingroups`  | Allow the bot to be added to groups          |
| `/setprivacy`     | Set group privacy mode (see below)           |

### Group Privacy Mode

By default, bots in groups only see:
- Messages that @mention the bot
- Commands (messages starting with `/`)
- Replies to the bot's messages

To receive **all messages** in a group, disable privacy mode:
1. Send `/setprivacy` to @BotFather
2. Select your bot
3. Choose **Disable**

> **Note:** If the bot was already in a group before disabling privacy, you must remove and re-add it for the change to take effect.

## Step 3 - Add the Bot to a Group (Optional)

For group messaging:

1. Open the group in Telegram
2. Tap the group name → **Add Members** → search for your bot's username
3. Add it to the group

For direct messaging, users can simply start a conversation with the bot by searching for its username.

## Step 4 - Get Chat IDs

Telegram uses numeric chat IDs. To find a chat ID:

**Option A - Use the bot API directly:**

1. Send a message to the bot (or in a group the bot is in)
2. Call the API:

```bash
curl "https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates" | jq '.result[-1].message.chat.id'
```

**Option B - Use @userinfobot:**

1. Forward a message from the target chat to **@userinfobot**
2. It will reply with the chat ID

> **Note:** Group chat IDs are negative numbers (e.g. `-1001234567890`). They must be quoted in YAML.

## Step 5 - Configure Pantalk

Set your environment variable:

```bash
export TELEGRAM_BOT_TOKEN="123456789:ABCdefGHIjklMNOpqrsTUVwxyz"
```

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-telegram-bot
    type: telegram
    bot_token: $TELEGRAM_BOT_TOKEN
    channels:
      - '@my_alerts_channel' # public channel username (resolved at startup)
```

Channels accept either public channel usernames (e.g. `@mychannel`) or raw numeric chat IDs (e.g. `-1001234567890`). Public usernames are resolved to numeric IDs automatically when the daemon starts. Private group chats still require numeric IDs.

> **Note:** The `endpoint` field is optional and defaults to `https://api.telegram.org`. Only set it if you're using a custom Bot API server.

## Verify

Start the daemon and check that the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your Telegram bot listed. Send a test message:

```bash
pantalk send --bot my-telegram-bot --channel -1001234567890 --text "Hello from Pantalk!"
```

## Troubleshooting

| Symptom                          | Cause                                                                        |
| -------------------------------- | ---------------------------------------------------------------------------- |
| `401 Unauthorized`               | Invalid bot token - regenerate via @BotFather with `/revoke` then `/newbot`  |
| Connected but no group messages  | Privacy mode is enabled - disable it via @BotFather `/setprivacy`            |
| Bot doesn't see DMs              | User hasn't started a conversation with the bot yet (must send `/start`)     |
| Wrong chat ID                    | Use `getUpdates` to confirm the correct chat ID - groups use negative IDs    |
| Messages delayed                 | Normal for long-polling - Telegram batches updates with a ~1s cycle          |
