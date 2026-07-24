# Claude Code agent

Pantalk can run Claude Code as a conversational agent while reusing the
installation, authentication, project settings, `CLAUDE.md`, skills, and MCP
servers already configured for the user running `pantalkd`.

The `claude` driver invokes Claude Code noninteractively with streaming JSON
output. The first completed turn returns a Claude session ID, which Pantalk
stores against the originating conversation. Later turns use
`claude --resume <session-id>`, including after Pantalk restarts.

This differs from the `command` driver: the inbound message is passed directly
to Claude, and Pantalk automatically delivers Claude's final response through
the originating bot.

Nothing below is platform-specific. The same agent definition works whether the
conversation arrives from Slack, WhatsApp, SMS, IRC, or a local test connector,
and replacing `driver: claude` with `driver: codex` leaves every one of those
connections untouched.

## Zero-config local test

From the repository Claude should use:

```bash
claude auth status
pantalk local --driver claude --workdir .
```

Local mode defaults to Claude's `plan` permission mode. It persists sessions in
`~/.local/share/pantalk/local.db`; use `--ephemeral` to discard the local
conversation on exit:

```bash
pantalk local --driver claude --workdir . --ephemeral
```

Select another permission mode deliberately:

```bash
pantalk local \
  --driver claude \
  --workdir . \
  --permission-mode acceptEdits
```

Pantalk does not currently transport Claude's interactive approval questions
through messaging connectors. Avoid `bypassPermissions` unless the surrounding
environment provides an appropriate sandbox.

If a turn cannot start or complete, Pantalk replies in the originating
conversation with an installation/authentication hint; detailed errors remain
in the daemon logs.

## Complete configuration

```yaml
bots:
  - name: local-test
    type: local
    agents:
      - agent: claude-engineering
        when: notify

agents:
  - name: claude-engineering
    driver: claude
    workdir: /workspace/project
    timeout: 900
    instructions: |
      You are the engineering assistant for this repository.
      Answer the user directly and keep changes scoped to their request.
    claude:
      # All fields are optional and otherwise inherit local Claude Code config.
      # binary: /usr/local/bin/claude
      # model: sonnet
      # effort: high
      permission_mode: plan
      # allowed_tools: [Read, Grep, Glob]
      # disallowed_tools: [Edit, Write]
```

## Field reference

| Field | Required | Meaning |
| --- | --- | --- |
| `name` | yes | Unique Pantalk agent name; namespaces persisted Claude sessions |
| `driver` | yes | `claude` selects the Claude Code integration |
| `workdir` | no | Repository/directory presented to Claude Code |
| `timeout` | no | Maximum seconds for each turn; defaults to 120 |
| `instructions` | no | Instructions appended to Claude Code's system prompt |
| `claude.binary` | no | Claude executable; defaults to `claude` on `PATH` |
| `claude.model` | no | Model override; otherwise inherit local Claude config |
| `claude.effort` | no | Effort override sent on each turn |
| `claude.permission_mode` | no | `plan`, `dontAsk`, `acceptEdits`, `auto`, `manual`, or `bypassPermissions` |
| `claude.allowed_tools` | no | Optional Claude tool allowlist |
| `claude.disallowed_tools` | no | Optional Claude tool denylist |

Bot selection and `when` expressions belong to entries in each bot's
`agents` list. See [Bot bindings](agents.md#bot-bindings).

There is no `command`, `respond_to`, or `session_scope` field for this driver.
The driver knows how to invoke Claude Code, replies follow the inbound route,
and conversation identity is derived consistently from the connector event.

## Process and session lifecycle

Pantalk launches one Claude Code print-mode process per turn:

```text
claude --print --output-format stream-json --verbose
```

After the first turn, Pantalk adds `--resume <session-id>`. Separate Pantalk
conversations receive separate Claude sessions and can run concurrently;
messages within one conversation are queued in order. Stopping Pantalk cancels
active CLI processes through their turn contexts.

The session transcript is owned by Claude Code on the local machine. The
Pantalk database stores only the opaque session ID needed to resume it.

## Current limitations

- Pantalk delivers the final `result` text, not partial streaming deltas.
- Interactive tool approval prompts are not forwarded to the chat.
- Attachments are not yet translated into Claude Code prompt inputs.
- If a locally stored Claude session is deleted, clear or replace the
  corresponding Pantalk state before continuing that conversation.
