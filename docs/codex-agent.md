# Native Codex agent

Pantalk can keep Codex connected through its native app-server protocol. The
daemon starts and owns `codex app-server --stdio`, sends matching inbound
messages as turns, and sends Codex's final answer back through the bot that
received the message.

This is different from the `command` driver: it does not launch a fresh CLI
command for every notification and does not require Codex to poll Pantalk.

## Zero-config local test

From the repository you want Codex to use:

```bash
pantalk local --workdir .
```

This starts the real Pantalk server, local connector, native Codex driver, and
interactive chat in one process. The safe default is `sandbox: read-only`;
enable edits explicitly:

```bash
pantalk local --workdir . --sandbox workspace-write
```

Use `--ephemeral` for a disposable conversation. Without it, the local Codex
thread mapping persists in a dedicated `local.db` and resumes the next time the
same user starts local mode.

## Complete local configuration

```yaml
server:
  socket_path: /tmp/pantalk.sock
  db_path: /tmp/pantalk.db

bots:
  # A bot is a messaging connection. Local is credential-free and is useful
  # for testing this flow before adding Slack or another provider.
  - name: local-test
    type: local
    agents:
      - agent: engineering-assistant
        when: notify

agents:
  # The top-level definition controls how the runtime starts. Bots reference
  # it from their ordered agents lists.
  - name: engineering-assistant
    driver: codex

    # Codex operates on this repository. If omitted, it inherits pantalkd's
    # current directory.
    workdir: /workspace/cbk-platform

    # Maximum duration of one Codex turn, not the lifetime of app-server.
    timeout: 900

    # Supplied as developer instructions whenever Pantalk starts or resumes a
    # Codex thread for this agent.
    instructions: |
      You are the engineering assistant for this repository.
      Answer the user directly and keep changes scoped to their request.

    # Every field below is optional. Omitted values inherit the user's local
    # Codex configuration and authentication.
    codex:
      # binary: /usr/local/bin/codex
      # model: gpt-5.4
      # effort: high
      sandbox: workspace-write
      approval_policy: never
```

Start the daemon, then open a test conversation:

```bash
pantalkd --config /path/to/pantalk.yaml
pantalk chat --bot local-test --user alice
```

Or inject one message noninteractively:

```bash
pantalk inject \
  --bot local-test \
  --user alice \
  --text "Explain the repository structure"
```

Codex must already be installed and authenticated for the user running
`pantalkd`. Pantalk inherits that user's environment and Codex configuration.

## Field reference

| Field | Required | Meaning |
| --- | --- | --- |
| `name` | yes | Unique Pantalk agent name; also namespaces persisted Codex sessions |
| `driver` | yes | `codex` selects the native app-server integration |
| `bots` | yes | Bot connection names this agent may handle |
| `when` | no | Event filter; defaults to `notify` |
| `workdir` | no | Repository/directory presented to Codex |
| `timeout` | no | Maximum seconds for each turn; defaults to 120 |
| `instructions` | no | Developer instructions for each new or resumed thread |
| `codex.binary` | no | Codex executable; defaults to `codex` on `PATH` |
| `codex.model` | no | Model override; otherwise inherit local Codex config |
| `codex.effort` | no | Reasoning-effort override sent on each turn |
| `codex.sandbox` | no | `read-only`, `workspace-write`, or `danger-full-access` |
| `codex.approval_policy` | no | `untrusted`, `on-request`, or `never` |

There is no `command`, `respond_to`, or `session_scope` field for the Codex
driver:

- `command` is unnecessary because the driver knows how to start app-server.
- Replies automatically use the bot, target, channel, and thread of the
  inbound event.
- Conversation scope is derived from the integration route, not configured.

## Conversations and persistence

Pantalk derives a stable conversation key from:

1. provider/service and configured bot name;
2. upstream thread when one exists;
3. otherwise the direct-message participant or channel/target.

Each key gets its own Codex thread. Messages in one conversation are processed
in order; separate conversations can run concurrently through the persistent
app-server process. The Pantalk database stores the mapping from agent and
conversation key to Codex thread ID. After a daemon restart, Pantalk resumes
that thread. If Codex can no longer resume it, Pantalk creates and persists a
replacement.

This is why conversation scope should not be a global config switch: the
correct boundary comes from each messaging integration's own route metadata.

## Process and safety behavior

Pantalk starts one app-server process per `driver: codex` agent definition.
That process stays alive across messages and hosts multiple conversation
threads.

Empty Codex overrides inherit the local Codex setup. Be deliberate when
setting `sandbox` and `approval_policy`: the daemon can answer messages without
an interactive terminal. Pantalk currently declines app-server approval and
elicitation requests rather than leaving a turn hanging. Interactive approval
forwarding through chat is not implemented yet.

The initial integration is text-only and sends the completed final response.
It does not expose streaming deltas or attachments to Codex yet, and it does
not automatically restart app-server after an unexpected process failure.
