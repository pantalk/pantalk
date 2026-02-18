<p align="center">
  <img src="https://pantalk.dev/icon.svg" alt="Pantalk" width="80" height="80" />
</p>

<h3 align="center">pantalkd - Technical Reference</h3>

<p align="center">
  Daemon, CLI clients, and protocol documentation for the Pantalk tool.
</p>

---

# Architecture

Pantalk is a Unix-style client-server communication tool for chat services.

| Component  | Role                                                                                  |
| ---------- | ------------------------------------------------------------------------------------- |
| `pantalkd` | Local daemon - maintains persistent upstream sessions (WebSocket, Gateway, long-poll) |
| `pantalk`  | Unified CLI - messaging, admin, and config management                                 |

All clients connect to `pantalkd` through a **Unix domain socket** using a simple JSON protocol. AI agents and LLM tools can send, receive, and stream chat messages without embedding any service SDK.

### Design principles

- **One daemon, all platforms** - upstream auth/session complexity lives in `pantalkd`
- **Unix-native IPC** - JSON over Unix socket, composable with `grep`, `jq`, `xargs`
- **Multi-bot** - define multiple bots per service via config
- **Local-first** - SQLite persistence, no external dependencies

## Source Layout

```
cmd/
  pantalkd/              # Daemon entry point
  pantalk/               # Unified CLI (messaging + admin)
configs/
  pantalk.example.yaml   # Example configuration
internal/
  client/                # Shared IPC client logic
  config/                # YAML parsing & validation
  protocol/              # JSON protocol types
  server/                # Daemon server + SQLite
  upstream/              # Platform connectors
```

## Quick Start

### 1. Configure

```bash
# Create config interactively (writes to ~/.config/pantalk/config.yaml by default)
go run ./cmd/pantalk setup

# Or copy the example and edit manually
mkdir -p ~/.config/pantalk
cp configs/pantalk.example.yaml ~/.config/pantalk/config.yaml
```

### 2. Start the daemon

```bash
# Uses ~/.config/pantalk/config.yaml by default
go run ./cmd/pantalkd

# Or specify a custom config
go run ./cmd/pantalkd --config /path/to/pantalk.yaml

# Override socket/db paths
go run ./cmd/pantalkd --socket /tmp/pantalk-dev.sock --db /tmp/pantalk-dev.db
```

### Path Defaults

| Resource | Default Location                    | Override                      |
| -------- | ----------------------------------- | ----------------------------- |
| Config   | `~/.config/pantalk/config.yaml`     | `--config`, `$PANTALK_CONFIG` |
| Socket   | `$XDG_RUNTIME_DIR/pantalk.sock`     | `--socket` flag               |
| Database | `~/.local/share/pantalk/pantalk.db` | `--db` flag                   |

All paths follow the [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/latest/).

### 3. Use the CLI

The unified `pantalk` binary works with all platforms. The daemon resolves which service each bot belongs to automatically.

```bash
# List all bots across all services
go run ./cmd/pantalk bots

# Send a message (service is auto-resolved from bot name)
go run ./cmd/pantalk send --bot my-bot --channel C0123456789 --text "hello from cli"
go run ./cmd/pantalk send --bot my-bot --channel C0123456789 --thread 1711234567.000100 --text "reply in thread"

# Read history
go run ./cmd/pantalk history --bot my-bot --channel C0123456789 --limit 20

# Check & clear notifications
go run ./cmd/pantalk notifications --bot my-bot --unseen --limit 50
go run ./cmd/pantalk notifications --bot my-bot --unseen --clear

# Stream events in real-time (auto-disconnects after 60s by default)
go run ./cmd/pantalk stream --bot my-bot --notify

# Stream with custom timeout (0 = no timeout)
go run ./cmd/pantalk stream --bot my-bot --notify --timeout 120
```

> **Tip:** JSON output is automatic when stdout is not a terminal (e.g. when called by an AI agent). Use `--json` to force it in interactive mode.

### 4. Manage config on the fly

```bash
# Validate config (uses default config location)
go run ./cmd/pantalk validate

# Edit non-interactively
go run ./cmd/pantalk config set-server --history 1000

go run ./cmd/pantalk config add-bot \
  --type slack --name my-bot \
  --bot-token '$SLACK_BOT_TOKEN' --app-level-token '$SLACK_APP_LEVEL_TOKEN'

# Hot-reload running daemon
go run ./cmd/pantalk reload
```

---

## Configuration

`pantalkd` initializes entirely from YAML config with strict schema validation:

- ❌ Unknown keys → config load failure
- ❌ Missing required provider fields → fast failure
- ✅ `transport` and `endpoint` optional for built-in providers (Slack, Discord, Telegram)
- ⚠️ Mattermost requires `endpoint` on the bot entry

### Multi-bot support

```yaml
bots:
  - name: ops-bot # --bot ops-bot
    type: slack
  - name: eng-bot # --bot eng-bot
    type: slack
```

### Daemon flags

| Flag        | Description                   |
| ----------- | ----------------------------- |
| `--config`  | Path to YAML config file      |
| `--socket`  | Override `server.socket_path` |
| `--db`      | Override `server.db_path`     |
| `--debug`   | Enable verbose debug logging  |
| `--version` | Print version and exit        |

### Hot reload

```bash
pantalk reload
```

- Reloads config from the daemon's `--config` path
- Restarts service connectors in-process
- Supports bot/service changes
- Does **not** switch `socket_path` or `db_path` at runtime (restart `pantalkd` for those)

---

## Implementation Notes

### IPC Protocol

JSON over Unix domain socket. Every request is a single JSON object with an `action` field:

```json
{"action": "bots"}
{"action": "send", "bot": "my-bot", "channel": "C0123", "text": "hello"}
{"action": "history", "bot": "my-bot", "channel": "C0123", "limit": 20}
{"action": "history", "bot": "my-bot", "search": "deploy", "limit": 50}
{"action": "notifications", "bot": "my-bot", "unseen": true}
{"action": "subscribe", "bot": "my-bot", "notify": true}
```

### Platform Connectors

| Platform   | Event Streaming   | Message Send  |
| ---------- | ----------------- | ------------- |
| Slack      | Socket Mode       | Web API       |
| Discord    | Gateway           | REST API      |
| Mattermost | WebSocket         | REST API      |
| Telegram   | Bot API long-poll | `sendMessage` |

### Persistence

All events are persisted locally in **SQLite**. `history` always reads from local state.

### Server Capabilities

| Action                | Description                                       |
| --------------------- | ------------------------------------------------- |
| `ping`                | Health check                                      |
| `bots`                | Bot discovery across all services                 |
| `send`                | Route-aware send with `target`/`channel`/`thread` |
| `history`             | Filtered message/event history                    |
| `notifications`       | Agent-relevant inbound events                     |
| `clear_history`       | Delete matching history events                    |
| `clear_notifications` | Delete matching notifications                     |
| `subscribe`           | Filtered real-time streaming                      |
| `reload`              | Hot-reload config and restart connectors          |

---

## Agent Notifications

Pantalk surfaces events relevant to the agent via `notifications`. This is designed for AI agents that need to know when they're being talked to.

### Notification behavior

| Behavior                  | Detail                                           |
| ------------------------- | ------------------------------------------------ |
| **Listing doesn't clear** | Reading notifications is non-destructive         |
| **Persistent**            | Stored in SQLite, survives daemon restarts       |
| **Explicit clearing**     | Use `notifications --clear` or `history --clear` |

### Clearing scopes

```bash
notifications --bot my-bot --clear                      # All for a bot
notifications --bot my-bot --channel C0 --clear          # Scoped by channel
notifications --clear --all                              # Everything
history --bot my-bot --clear                             # Clear history for a bot
history --clear --all                                    # Clear all history
```

### What triggers a notification

An inbound event becomes a notification when any of these are true:

- **Direct message** - `target` matches `dm:*`, `direct:*`, `user:*`, or DM-like channel IDs
- **Mention** - message contains `@bot-name` or `<@platform-user-id>` (auto-discovered at runtime)
- **Active thread** - event is on a route where the agent previously sent a message

---

## Platform Setup

### Slack (Socket Mode)

Pantalk connects to Slack using **Socket Mode** (WebSocket), which means no public URL or webhook endpoint is needed.

#### 1. Create a Slack App

Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app (or select an existing one).

#### 2. Enable Socket Mode

**Settings → Socket Mode** → toggle **ON**.

This lets pantalk receive events over a WebSocket connection instead of requiring a public HTTP endpoint.

#### 3. Generate an App-Level Token

**Settings → Basic Information → App-Level Tokens** → **Generate Token**:

- Give it a name (e.g. `pantalk`)
- Add the **`connections:write`** scope
- Copy the token (`xapp-...`) - this is your `app_level_token`

#### 4. Add Bot Token Scopes

**Features → OAuth & Permissions → Scopes → Bot Token Scopes** - add:

| Scope               | Purpose                                         |
| ------------------- | ----------------------------------------------- |
| `chat:write`        | Send messages                                   |
| `channels:history`  | Receive messages in public channels             |
| `groups:history`    | Receive messages in private channels (optional) |
| `im:history`        | Receive direct messages (optional)              |
| `app_mentions:read` | Receive @mention events                         |

#### 5. Subscribe to Bot Events

**Features → Event Subscriptions** → toggle **ON**, then under **Subscribe to bot events** add:

| Event              | Purpose                                             |
| ------------------ | --------------------------------------------------- |
| `app_mention`      | Fires when someone @mentions your bot               |
| `message.channels` | Fires for all messages in public channels bot is in |
| `message.groups`   | Fires for messages in private channels (optional)   |
| `message.im`       | Fires for direct messages to the bot (optional)     |

Click **Save Changes** at the bottom.

#### 6. Install the App

**Features → OAuth & Permissions** → **Install to Workspace** (or **Reinstall** if you changed scopes).

Copy the **Bot User OAuth Token** (`xoxb-...`) - this is your `bot_token`.

> **Important:** You must reinstall the app every time you change scopes or event subscriptions.

#### 7. Invite the Bot

The bot must be **in a channel** to receive events from it:

```
/invite @YourBotName
```

#### 8. Configure Pantalk

```yaml
bots:
  - name: my-slack-bot
    type: slack
    bot_token: $SLACK_BOT_TOKEN # xoxb-...
    app_level_token: $SLACK_APP_LEVEL_TOKEN # xapp-...
```

Token fields accept either a literal string or an environment variable reference (`$VAR` or `${VAR}`).

#### Troubleshooting

| Symptom                            | Cause                                                                       |
| ---------------------------------- | --------------------------------------------------------------------------- |
| `auth failed` on startup           | Invalid `bot_token` - check the `xoxb-` token is correct                    |
| No `socket mode connected` log     | Socket Mode is not enabled, or `app_level_token` is wrong                   |
| Connected but no events arrive     | Missing event subscriptions (step 5) or bot not invited to channel (step 7) |
| Events arrive but no notifications | Message doesn't @mention the bot and isn't a DM                             |
| `warning: no events received` log  | Likely missing event subscriptions - see step 5                             |

---

## Roadmap

- Richer provider event support (edits, reactions, thread metadata)
- Provider-specific message normalization
- Additional platform connectors

---

<p align="center">
  <a href="https://pantalk.dev">pantalk.dev</a></sub>
</p>
