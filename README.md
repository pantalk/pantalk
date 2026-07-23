<p align="center">
  <img src="https://pantalk.dev/icon.svg" alt="Pantalk" width="80" height="80" />
</p>

<h1 align="center">Pantalk</h1>

<p align="center">
  <strong>Give your AI agent a voice on every chat platform.</strong><br/>
  A lightweight daemon that lets AI agents send, receive, and stream messages across local test conversations, Slack, Discord, Mattermost, Telegram, WhatsApp, IRC, Matrix, Twilio, and Zulip through a single interface.
</p>

<p align="center">
  <a href="https://pantalk.dev">Website</a> · <a href="https://pantalk.dev/about">About</a> · <a href="#quick-start">Quick Start</a> · <a href="#platform-setup">Platform Setup</a>
</p>

---

## The Problem

AI agents need to communicate with humans where they already are - Slack, Discord, Mattermost, Telegram, WhatsApp, IRC, Matrix, Twilio, Zulip. But every platform speaks a different protocol. Building an agent that can participate in conversations across all of them means writing and maintaining separate integrations before your agent can even say "hello."

## The Solution

Pantalk gives your AI agent a single, consistent interface to all chat platforms. One daemon (`pantalkd`) handles the upstream complexity - auth, sessions, reconnects, rate limits - while your agent talks through simple CLI commands or a Unix domain socket with a JSON protocol.

```mermaid
graph TD
    Agent["Your AI Agent<br/><em>(any language, any framework)</em>"]
    Agent -->|send| Socket
    Agent -->|history| Socket
    Agent -->|notify| Socket
    Agent -->|stream| Socket
    Socket["Unix Domain Socket<br/><em>(JSON protocol)</em>"]
    Socket --> Daemon["pantalkd<br/><em>(daemon)</em>"]
    Daemon --> Slack
    Daemon --> Discord
    Daemon --> Mattermost
    Daemon --> Telegram
    Daemon --> WhatsApp
    Daemon --> IRC
    Daemon --> Matrix
    Daemon --> Twilio
    Daemon --> Zulip
    Daemon --> More["..."]
```

## Why Pantalk

|                        | Without Pantalk            | With Pantalk                  |
| ---------------------- | -------------------------- | ----------------------------- |
| **Integration effort** | One SDK per platform       | One CLI, all platforms        |
| **Auth & sessions**    | You manage everything      | Daemon handles it             |
| **Message history**    | Query each API differently | `history --limit 20`          |
| **Notifications**      | Build your own routing     | `notifications --unseen`      |
| **Real-time events**   | WebSocket/Gateway/polling  | `stream --bot name`           |
| **Composability**      | Library lock-in            | Pipe to `grep`, `jq`, `xargs` |

## Supported Platforms

| Platform       | Transport                       | Status          |
| -------------- | ------------------------------- | --------------- |
| **Local**      | Unix socket                     | ✅ Dev/test     |
| **Slack**      | Socket Mode + Web API           | ✅ Full support |
| **Discord**    | Gateway + REST API              | ✅ Full support |
| **Mattermost** | WebSocket + REST API            | ✅ Full support |
| **Telegram**   | Bot API long-poll + sendMessage | ✅ Full support |
| **WhatsApp**   | Web multi-device (whatsmeow)    | ✅ Full support |
| **IRC**        | TCP/TLS + IRC protocol          | ✅ Full support |
| **Matrix**     | Client-Server API (mautrix-go)  | ✅ Full support |
| **Twilio**     | REST API (polling + send)       | ✅ Full support |
| **Zulip**      | REST API + Event Queue          | ✅ Full support |

---

## Architecture

| Component  | Role                                                                                  |
| ---------- | ------------------------------------------------------------------------------------- |
| `pantalkd` | Local daemon - maintains persistent upstream sessions (WebSocket, Gateway, long-poll) |
| `pantalk`  | Unified CLI plus an embedded one-command local testing mode                           |

Normal client commands connect to `pantalkd` through a **Unix domain socket**
using a simple JSON protocol. `pantalk local` embeds the same server and still
uses that socket protocol internally. AI agents and LLM tools can send,
receive, and stream chat messages without embedding a provider SDK.

### Design Principles

- **Agent-first** - structured output, skill definitions, and notification routing designed for AI agents
- **One daemon, all platforms** - upstream auth/session complexity lives in `pantalkd`
- **Composable CLI** - JSON over Unix socket, works with `grep`, `jq`, `xargs`, and any language
- **Multi-bot** - define multiple bots per service via config
- **Local-first** - SQLite persistence, no external dependencies

## Source Layout

```
cmd/
  pantalkd/              # Daemon entry point
  pantalk/               # Unified CLI (messaging + admin)
configs/
  pantalk.example.yaml   # Example configuration
docs/
  agents.md              # Reactive agent configuration guide
  codex-agent.md         # Persistent native Codex app-server driver
  claude-agent.md        # Durable local Claude Code conversational driver
  local-connector.md     # Offline injection and interactive chat
  slack-setup.md         # Slack platform setup guide
  discord-setup.md       # Discord platform setup guide
  mattermost-setup.md    # Mattermost platform setup guide
  telegram-setup.md      # Telegram platform setup guide
  whatsapp-setup.md      # WhatsApp platform setup guide
  irc-setup.md           # IRC platform setup guide
  matrix-setup.md        # Matrix platform setup guide
  twilio-setup.md        # Twilio platform setup guide
  zulip-setup.md         # Zulip platform setup guide
  claude-code-hooks.md   # Claude Code hooks integration guide
internal/
  client/                # Shared IPC client logic
  config/                # YAML parsing & validation
  protocol/              # JSON protocol types
  server/                # Daemon server + SQLite
  upstream/              # Platform connectors
```

## Quick Start

### 1. Configure

Create a config file with your bot credentials:

```bash
mkdir -p ~/.config/pantalk
cat > ~/.config/pantalk/config.yaml << 'EOF'
server:
  notification_history_size: 1000

bots:
  - name: my-bot
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN
    channels:
      - C0123456789
EOF
```

See `configs/pantalk.example.yaml` for a full example with all platforms.

For a zero-config local Codex conversation:

```bash
pantalk local --workdir .
```

This starts the real daemon, local connector, native Codex app-server driver,
and interactive chat inside one process. It defaults to a read-only sandbox and
persists conversation state in `~/.local/share/pantalk/local.db`. Use
`--ephemeral` to discard the session on exit or
`--sandbox workspace-write` when you intentionally want Codex to edit files.

Use the same local flow with Claude Code:

```bash
pantalk local --driver claude --workdir .
```

This reuses the installed Claude Code authentication and configuration,
defaults to `plan` permissions, and resumes the Claude session on later turns.

To test the connector separately or attach it to a larger configuration,
configure a credential-free local bot:

```yaml
bots:
  - name: local-test
    type: local
```

Then inject a direct inbound message or open an interactive terminal:

```bash
pantalk inject --bot local-test --user alice --text "hello"
pantalk chat --bot local-test --user alice
```

The local connector uses the same routing, notification, history, and streaming
pipeline as provider connectors, but never makes network calls and never echoes
outbound messages back as inbound messages. See
[`docs/local-connector.md`](docs/local-connector.md).

### 2. Start the daemon

```bash
# Uses ~/.config/pantalk/config.yaml by default
pantalkd &

# Or specify a custom config
pantalkd --config /path/to/pantalk.yaml

# Override socket/db paths
pantalkd --socket /tmp/pantalk-dev.sock --db /tmp/pantalk-dev.db
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
pantalk bots

# Send a message (service is auto-resolved from bot name)
pantalk send --bot my-bot --channel C0123456789 --text "hello from cli"
pantalk send --bot my-bot --channel C0123456789 --thread 1711234567.000100 --text "reply in thread"
pantalk send --bot my-bot --channel C0123456789 --text "see attached" --attach ./report.pdf --attach ./chart.png
# (requires the file's directory to be listed in server.media.attach_roots)

# Read history
pantalk history --bot my-bot --channel C0123456789 --limit 20

# Show "typing..." while the agent thinks (auto-stops when you send)
pantalk typing --bot my-bot --channel C0123456789

# Check & clear notifications
pantalk notifications --bot my-bot --unseen --limit 50
pantalk notifications --bot my-bot --unseen --clear

# Stream events in real-time (auto-disconnects after 60s by default)
pantalk stream --bot my-bot --notify

# Stream with custom timeout (0 = no timeout)
pantalk stream --bot my-bot --notify --timeout 120
```

> **Tip:** JSON output is automatic when stdout is not a terminal (e.g. when called by an AI agent). Use `--json` to force it in interactive mode.

### 4. Manage config on the fly

```bash
# Validate config (uses default config location)
pantalk validate

# List configured bots without exposing credentials
pantalk config list-bots --json

# Edit non-interactively
pantalk config set-server --history 1000

pantalk config add-bot \
  --type slack --name my-bot \
  --bot-token '$SLACK_BOT_TOKEN' --app-level-token '$SLACK_APP_LEVEL_TOKEN'

# Hot-reload running daemon
pantalk reload
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

| Flag           | Description                                        |
| -------------- | -------------------------------------------------- |
| `--config`     | Path to YAML config file                           |
| `--socket`     | Override `server.socket_path`                      |
| `--db`         | Override `server.db_path`                          |
| `--allow-exec` | Allow agent commands outside the default allowlist |
| `--debug`      | Enable verbose debug logging                       |
| `--version`    | Print version and exit                             |

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
{"action": "inject", "bot": "local-test", "user": "alice", "target": "user:alice", "text": "hello"}
{"action": "send", "bot": "my-bot", "channel": "C0123", "text": "see attached", "attach": ["/abs/path/report.pdf"]}
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
| WhatsApp   | Web multi-device  | `SendMessage` |
| IRC        | TCP/TLS           | `PRIVMSG`     |
| Matrix     | Client-Server API | REST API      |
| Twilio     | REST API poll     | REST API      |
| Zulip      | Event Queue       | REST API      |

### Persistence

All events are persisted locally in **SQLite**. `history` always reads from local state.

Attachment bytes live outside the database in a content-addressed media store (default `~/.local/share/pantalk/media`, configurable via `server.media`). Events record a durable storage key; local paths are derived from it at read time, so moving the storage root never strands history. Files no longer referenced by any event or notification are garbage-collected on startup and after `--clear`. Attachment support is currently implemented for **Telegram** (send + receive); other connectors refuse `--attach` rather than dropping files silently.

Outbound attachments are allowlist-gated: `send --attach` only reads files under the directories listed in `server.media.attach_roots`, and is disabled entirely when the list is empty - the same allowlist-by-default posture as agent commands. Symlinks are resolved before the check, and the list can be changed with a live `reload`.

A message that arrives with files but no text is stored with empty text; `history` and `notifications` queries render a synthetic placeholder (`[attachment: photo.jpg]`) so agents and list output can see something arrived. The placeholder is derived at query time and never persisted.

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
notifications --bot my-bot --clear                       # All for a bot
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

Each platform requires its own app/bot setup before Pantalk can connect. See the detailed guides:

| Platform   | Guide                                        | Connection Method       |
| ---------- | -------------------------------------------- | ----------------------- |
| Slack      | [Slack Setup](docs/slack-setup.md)           | Socket Mode (WebSocket) |
| Discord    | [Discord Setup](docs/discord-setup.md)       | Gateway (WebSocket)     |
| Mattermost | [Mattermost Setup](docs/mattermost-setup.md) | WebSocket + REST API    |
| Telegram   | [Telegram Setup](docs/telegram-setup.md)     | Bot API (long-poll)     |
| WhatsApp   | [WhatsApp Setup](docs/whatsapp-setup.md)     | Web multi-device        |
| IRC        | [IRC Setup](docs/irc-setup.md)               | TCP/TLS                 |
| Matrix     | [Matrix Setup](docs/matrix-setup.md)         | Client-Server API       |
| Twilio     | [Twilio Setup](docs/twilio-setup.md)         | REST API (polling)      |
| Zulip      | [Zulip Setup](docs/zulip-setup.md)           | REST API + Event Queue  |

---

## Integrations

| Integration | Guide                                          | Description                                                                                           |
| ----------- | ---------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Agents      | [Agents](docs/agents.md)                       | Launch AI agents automatically when matching notifications arrive                                     |
| Claude Code | [Claude Code Hooks](docs/claude-code-hooks.md) | Use pantalk as a hook to forward notifications, check chat on stop, and load context on session start |

---

## Comparisons

| Compared to                           | Guide                                      | In short                                                                                                                                                |
| ------------------------------------- | ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [Buzz](https://github.com/block/buzz) | [Pantalk vs Buzz](docs/pantalk-vs-buzz.md) | Both give agents a unified, addressable event stream. Buzz consolidates your team into one workspace; Pantalk federates the platforms they already use. |

---

## Roadmap

- Richer provider event support (edits, reactions, thread metadata)
- Typing indicators for the remaining connectors (Slack, Discord, Mattermost, Matrix, WhatsApp, Zulip, iMessage - Telegram shipped)
- Inbound attachment support for the remaining connectors (Telegram shipped)
- Provider-specific message normalization
- Additional platform connectors

---

## See Also

**[MCPShim](https://github.com/mcpshim/mcpshim)** - Use any MCP server as a standard CLI command. Pantalk gives your agent a voice; MCPShim gives it tools. Together they form a complete agent infrastructure stack.

---

<p align="center">
  <a href="https://pantalk.dev">pantalk.dev</a></sub>
</p>
