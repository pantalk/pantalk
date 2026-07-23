# Local connector

The `local` connector is an offline messaging surface for developing and
testing Pantalk routing and agent integrations. It requires no credentials and
makes no network calls.

## One-command agent chat

The shortest path needs no YAML file and no separately running daemon:

```bash
pantalk local --workdir .
```

`pantalk local` starts an embedded Pantalk server, a `local-test` connector, one
native `local-codex` agent, and the interactive chat. The generated agent uses:

```yaml
driver: codex
when: notify
timeout: 900
codex:
  sandbox: read-only
  approval_policy: never
```

Conversation state persists at `~/.local/share/pantalk/local.db`, separate from
the normal daemon database. Useful overrides include:

```bash
# Allow edits in the selected working directory.
pantalk local --workdir . --sandbox workspace-write

# Discard the database, socket, and Codex thread mapping on exit.
pantalk local --workdir . --ephemeral

# Keep state in a chosen location.
pantalk local --workdir . --state /tmp/my-pantalk-local.db
```

Use `/quit`, `/exit`, Ctrl-D, or Ctrl+C to stop the chat and its embedded
server.

To use the locally authenticated Claude Code installation instead:

```bash
pantalk local --driver claude --workdir .
```

This creates a separate `local-claude` agent session and defaults to
`--permission-mode plan`. Claude sessions and Codex threads share the local
Pantalk database but are namespaced by agent name, so switching drivers cannot
mix their conversation identifiers. See
[Claude Code agent](claude-agent.md) for configuration and permission details.

## Configured connector

To run the local connector through the normal long-lived daemon, configure it
explicitly:

```yaml
bots:
  - name: local-test
    type: local

agents:
  - name: engineering-assistant
    driver: codex
    bots:
      - local-test
    workdir: /workspace/cbk-platform
    timeout: 900
    codex:
      sandbox: workspace-write
      approval_policy: never
```

The `agents` section is optional. It is included here to show the complete
offline test setup for the [native Codex driver](codex-agent.md).

Start `pantalkd` normally, then inject an inbound direct message:

```bash
pantalk inject \
  --bot local-test \
  --user alice \
  --text "Explain this repository"
```

When no destination is supplied, `inject` uses `user:<user>` and the message is
treated as a direct message. Destinations can be supplied explicitly to test
channel and thread routing:

```bash
pantalk inject \
  --bot local-test \
  --user alice \
  --channel engineering \
  --thread review-42 \
  --text "@local-test review this change"
```

`user`, `target`, `channel`, and `thread` are preserved on the resulting event.
Normal notification rules apply: direct messages and mentions notify; unrelated
channel messages do not.

For manual conversations, use:

```bash
pantalk chat --bot local-test --user alice
```

The chat client subscribes before accepting input, injects each input line
through the daemon socket, and displays outbound messages on the same route.
Use `/quit`, `/exit`, or Ctrl-D to exit.

## Loop prevention

Outbound sends publish exactly one `direction: out` event. They are never
automatically echoed as inbound messages.

To verify bot-originated inbound events are ignored by notification and agent
routing, inject as the connector identity:

```bash
pantalk inject \
  --bot local-test \
  --self \
  --text "synthetic bot message"
```

The resulting event has `self: true` and `notify: false`. The local bot identity
is deterministic: `local:<bot-name>`.

## Socket protocol

The CLI uses the normal daemon Unix socket. A noninteractive client can send:

```json
{
  "action": "inject",
  "bot": "local-test",
  "user": "alice",
  "target": "user:alice",
  "channel": "",
  "thread": "",
  "text": "hello"
}
```

The daemon accepts `inject` only for connectors that explicitly implement local
inbound injection. Requests targeting Slack, Discord, or any other
network-backed connector are rejected.

The local connector currently supports plain-text inbound and outbound
messages. Attachments, reactions, and typing indicators are intentionally not
implemented.
