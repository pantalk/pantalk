<p align="center">
  <img src="https://pantalk.dev/icon.svg" alt="Pantalk" width="80" height="80" />
</p>

<h3 align="center">pantalkd — Technical Reference</h3>

<p align="center">
  Daemon, CLI clients, and protocol documentation for the Pantalk tool.
</p>

---

# Architecture

Pantalk is a Unix-style client-server communication tool for chat services.

| Component            | Role                                                                                  |
| -------------------- | ------------------------------------------------------------------------------------- |
| `pantalkd`           | Local daemon — maintains persistent upstream sessions (WebSocket, Gateway, long-poll) |
| `pantalk-slack`      | Slack CLI client                                                                      |
| `pantalk-discord`    | Discord CLI client                                                                    |
| `pantalk-mattermost` | Mattermost CLI client                                                                 |
| `pantalk-telegram`   | Telegram CLI client                                                                   |
| `pantalkctl`         | Config management & daemon control                                                    |

All clients connect to `pantalkd` through a **Unix domain socket** using a simple JSON protocol. AI agents and LLM tools can send, receive, and stream chat messages without embedding any service SDK.

### Design principles

- **One daemon, all platforms** — upstream auth/session complexity lives in `pantalkd`
- **Unix-native IPC** — JSON over Unix socket, composable with `grep`, `jq`, `xargs`
- **Multi-bot** — define multiple bots per service via config
- **Local-first** — SQLite persistence, no external dependencies

## Source Layout

```
cmd/
  pantalkd/              # Daemon entry point
  pantalkctl/            # Config & control CLI
  pantalk-slack/         # Slack client
  pantalk-discord/       # Discord client
  pantalk-mattermost/    # Mattermost client
  pantalk-telegram/      # Telegram client
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
# Copy example config
cp configs/pantalk.example.yaml configs/pantalk.yaml

# Or create config interactively
go run ./cmd/pantalkctl setup --output ./configs/pantalk.yaml
```

### 2. Start the daemon

```bash
go run ./cmd/pantalkd --config ./configs/pantalk.yaml

# Optional: override socket/db paths
go run ./cmd/pantalkd --config ./configs/pantalk.yaml \
  --socket /tmp/pantalk-dev.sock \
  --db /tmp/pantalk-dev.db
```

### 3. Use the CLI clients

**Slack**

```bash
go run ./cmd/pantalk-slack bots
go run ./cmd/pantalk-slack send --bot team-a --channel C0123456789 --text "hello from cli"
go run ./cmd/pantalk-slack send --bot team-a --channel C0123456789 --thread 1711234567.000100 --text "reply in thread"
go run ./cmd/pantalk-slack history --bot team-a --channel C0123456789 --limit 20
go run ./cmd/pantalk-slack notifications --bot team-a --unseen --limit 50
go run ./cmd/pantalk-slack clear-notifications --bot team-a --unseen
go run ./cmd/pantalk-slack stream --bot team-a --notify
```

**Discord**

```bash
go run ./cmd/pantalk-discord bots
go run ./cmd/pantalk-discord send --bot ops-bot --target 123456789012345678 --text "hello guild"
```

**Mattermost**

```bash
go run ./cmd/pantalk-mattermost bots
go run ./cmd/pantalk-mattermost send --bot support-bot --channel a1b2c3d4e5f6g7h8i9j0k --text "hello mattermost"
```

**Telegram**

```bash
go run ./cmd/pantalk-telegram bots
go run ./cmd/pantalk-telegram send --bot alerts-bot --channel -1001234567890 --text "hello telegram"
```

### 4. Manage config on the fly

```bash
# Validate config
go run ./cmd/pantalkctl validate --config ./configs/pantalk.yaml

# Edit non-interactively
go run ./cmd/pantalkctl config set-server --config ./configs/pantalk.yaml \
  --socket /tmp/pantalk.sock --db /tmp/pantalk.db --history 1000

go run ./cmd/pantalkctl config add-service --config ./configs/pantalk.yaml --name slack

go run ./cmd/pantalkctl config add-bot --config ./configs/pantalk.yaml \
  --service slack --name team-a --bot-id U123456 \
  --bot-token '$SLACK_BOT_TOKEN' --app-level-token '$SLACK_APP_LEVEL_TOKEN'

# Hot-reload running daemon
go run ./cmd/pantalkctl reload --socket /tmp/pantalk.sock
```

---

## Configuration

`pantalkd` initializes entirely from YAML config with strict schema validation:

- ❌ Unknown keys → config load failure
- ❌ Missing required provider fields → fast failure
- ✅ `transport` and `endpoint` optional for built-in providers (Slack, Discord, Telegram)
- ⚠️ Mattermost requires `endpoint` at service level

### Multi-bot support

```yaml
services:
  - name: slack
    bots:
      - name: team-a # --bot team-a
      - name: team-b # --bot team-b
```

### Daemon flags

| Flag       | Description                   |
| ---------- | ----------------------------- |
| `--config` | Path to YAML config file      |
| `--socket` | Override `server.socket_path` |
| `--db`     | Override `server.db_path`     |

### Hot reload

```bash
pantalkctl reload --socket /tmp/pantalk.sock
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
{"action": "send", "bot": "team-a", "channel": "C0123", "text": "hello"}
{"action": "history", "bot": "team-a", "channel": "C0123", "limit": 20}
{"action": "notifications", "bot": "team-a", "unseen": true}
{"action": "subscribe", "bot": "team-a", "notify": true}
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
| `bots`                | Bot discovery across all services                 |
| `send`                | Route-aware send with `target`/`channel`/`thread` |
| `history`             | Filtered message/event history                    |
| `notifications`       | Agent-relevant inbound events                     |
| `subscribe`           | Filtered real-time streaming                      |
| `clear_notifications` | Explicit notification clearing                    |
| `reload`              | Hot-reload config and restart connectors          |

---

## Agent Notifications

Pantalk surfaces events relevant to the agent via `notifications`. This is designed for AI agents that need to know when they're being talked to.

### Notification behavior

| Behavior                  | Detail                                     |
| ------------------------- | ------------------------------------------ |
| **Listing doesn't clear** | Reading notifications is non-destructive   |
| **Persistent**            | Stored in SQLite, survives daemon restarts |
| **Explicit clearing**     | Use `clear-notifications` with scope       |

### Clearing scopes

```bash
clear-notifications --id 42                    # Single notification
clear-notifications --bot team-a               # All for a bot
clear-notifications --bot team-a --channel C0  # Scoped by channel
clear-notifications --all                      # Everything
```

### What triggers a notification

An inbound event becomes a notification when any of these are true:

- **Direct message** — `target` matches `dm:*`, `direct:*`, `user:*`, or DM-like channel IDs
- **Mention** — message contains `@bot-name` or `<@bot-id>`
- **Active thread** — event is on a route where the agent previously sent a message

---

## Platform Setup

### Slack (Socket Mode)

Each Slack bot requires:

| Field             | Value                                    |
| ----------------- | ---------------------------------------- |
| `bot_token`       | Bot token (`xoxb-...`)                   |
| `app_level_token` | App-level socket mode token (`xapp-...`) |

Token fields accept either a literal string or an environment variable reference:

```yaml
bot_token: $SLACK_BOT_TOKEN_TEAM_A
# or
bot_token: xoxb-your-actual-token
```

---

## Roadmap

- Richer provider event support (edits, reactions, thread metadata)
- Provider-specific message normalization
- Additional platform connectors

---

<p align="center">
  <a href="https://pantalk.dev">pantalk.dev</a></sub>
</p>
