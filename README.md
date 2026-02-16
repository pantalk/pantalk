# Pantalk

Pantalk is a Unix-style client-server communication tool for chat services.

- `pantalkd` runs as a local daemon and keeps persistent upstream sessions (websocket/gateway/etc).
- Service clients (`pantalk-slack`, `pantalk-discord`, `pantalk-mattermost`, `pantalk-telegram`, and future tools) connect to `pantalkd` through a Unix domain socket.
- AI agents and other LLM tools can use simple shell commands to send/receive/stream chat messages without embedding service SDK logic.

## Why this shape

- Keeps upstream auth/session complexity in one process (`pantalkd`)
- Exposes a simple local IPC protocol that feels Unix-native
- Supports multiple bots per service through config-defined bot names
- Enables composable CLI workflows (`grep`, `jq`, `xargs`, etc.)

## Project layout

```
incubator/pantalk/
  cmd/
    pantalkd/
    pantalkctl/
    pantalk-slack/
    pantalk-discord/
    pantalk-mattermost/
    pantalk-telegram/
  configs/
    pantalk.example.yaml
  internal/
    client/
    config/
    protocol/
    server/
    upstream/
```

## Quick start

```bash
cd incubator/pantalk
cp configs/pantalk.example.yaml configs/pantalk.yaml

# or create a strict config interactively
go run ./cmd/pantalkctl setup --output ./configs/pantalk.yaml

# validate config anytime
go run ./cmd/pantalkctl validate --config ./configs/pantalk.yaml

# edit config non-interactively
go run ./cmd/pantalkctl config set-server --config ./configs/pantalk.yaml --socket /tmp/pantalk.sock --db /tmp/pantalk.db --history 1000
go run ./cmd/pantalkctl config add-service --config ./configs/pantalk.yaml --name slack
go run ./cmd/pantalkctl config add-bot --config ./configs/pantalk.yaml --service slack --name team-a --bot-id U123456 --bot-token '$SLACK_BOT_TOKEN' --app-level-token '$SLACK_APP_LEVEL_TOKEN'

# reload bots/services in a running daemon after config updates
go run ./cmd/pantalkctl reload --socket /tmp/pantalk.sock

go run ./cmd/pantalkd --config ./configs/pantalk.yaml

# optional overrides
go run ./cmd/pantalkd --config ./configs/pantalk.yaml --socket /tmp/pantalk-dev.sock --db /tmp/pantalk-dev.db
```

In another shell:

```bash
cd incubator/pantalk

go run ./cmd/pantalk-slack bots
go run ./cmd/pantalk-slack send --bot team-a --channel C0123456789 --text "hello from cli"
go run ./cmd/pantalk-slack send --bot team-a --channel C0123456789 --thread 1711234567.000100 --text "reply in thread"
go run ./cmd/pantalk-slack history --bot team-a --channel C0123456789 --limit 20
go run ./cmd/pantalk-slack notifications --bot team-a --limit 50
go run ./cmd/pantalk-slack notifications --bot team-a --unseen --limit 50
go run ./cmd/pantalk-slack clear-notifications --bot team-a --unseen
go run ./cmd/pantalk-slack clear-notifications --id 42
go run ./cmd/pantalk-slack clear-notifications --all
go run ./cmd/pantalk-slack stream --bot team-a --notify
go run ./cmd/pantalk-slack stream --bot team-a --channel C0123456789

go run ./cmd/pantalk-discord bots
go run ./cmd/pantalk-discord send --bot ops-bot --target 123456789012345678 --text "hello guild"

go run ./cmd/pantalk-mattermost bots
go run ./cmd/pantalk-mattermost send --bot support-bot --channel a1b2c3d4e5f6g7h8i9j0k --text "hello mattermost"

go run ./cmd/pantalk-telegram bots
go run ./cmd/pantalk-telegram send --bot alerts-bot --channel -1001234567890 --text "hello telegram"
```

## Config model

`pantalkd` initializes entirely from YAML config.

Schema behavior is strict:

- Unknown keys fail config loading
- Missing required provider fields fail fast (for Slack: `bot_id`, `bot_token`, `app_level_token`)
- `transport` and `endpoint` are optional for built-in providers (`slack`, `discord`, `telegram`)
- Mattermost requires `endpoint` at service level and `bot_token` at bot level

- Define any number of services
- Define multiple bots per service
- Clients pick a bot with `--bot <name>`

Daemon runtime flags:

- `--config`: path to YAML config
- `--socket`: override `server.socket_path`
- `--db`: override `server.db_path`

Daemon reload behavior:

- `pantalkctl reload` reloads config from daemon `--config` path and restarts service connectors in-process.
- Reload supports bot/service changes but does not switch `socket_path` or `db_path` at runtime.
- If `socket_path` or `db_path` changed in config, restart `pantalkd`.

Example:

```yaml
services:
  - name: slack
    bots:
      - name: team-a
      - name: team-b
```

## Current implementation notes

- IPC protocol is JSON over Unix socket.
- Slack connector is implemented with Slack Socket Mode event streaming + Web API send.
- Discord connector is implemented with Discord Gateway event streaming + REST send.
- Mattermost connector is implemented with Mattermost WebSocket event streaming + REST send.
- Telegram connector is implemented with Bot API long-poll event streaming + sendMessage.
- Pantalk persists all events locally in SQLite and `history` always reads from that local state.
- Server already supports:
  - Bot discovery (`bots`)
  - Route-aware send (`send` with `target/channel/thread`)
  - Filtered message/event history (`history`)
  - Notification reads (`notifications`) for agent-relevant inbound events
  - Filtered streaming subscription (`stream`/`subscribe`)

## Agent notifications

Pantalk can surface events that are relevant to the agent via `notifications` (or `history --notify`).

Important behavior:

- Listing notifications does **not** clear them.
- Notifications are persisted in the Pantalk SQLite database and survive daemon restarts/crashes.
- Clearing is explicit with `clear-notifications`.
- You can clear one notification (`--id`) or clear by scope (`--bot`, `--channel`, `--thread`, `--target`) or use `--all`.

An event is considered notification-relevant when it is inbound and one of these is true:

- It looks like a direct message to the agent (`target` such as `dm:*` / `direct:*` / `user:*`, or DM-like channel IDs)
- It mentions the agent (`@bot-name` or `<@bot-id>`)
- It is part of a route where the agent previously participated (`target/channel/thread` where the agent sent before)

## Slack socket mode setup

For each Slack bot config you need:

- `bot_token` for bot token (`xoxb-...`)
- `app_level_token` for app-level socket mode token (`xapp-...`)

Token fields accept either:

- a literal token string
- an environment variable reference like `$SLACK_BOT_TOKEN_TEAM_A`

## Next implementation step

Add richer provider event support (edits/reactions/thread metadata) and provider-specific message normalization.
