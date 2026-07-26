# Agents

Pantalk separates reusable agent definitions from bot-specific routing. This
separation is the mechanism behind Pantalk's central claim: harnesses and
platforms are declared in different blocks that never reference each other, so
either can be replaced without touching the other.

- Top-level `agents` describe how a harness starts - Codex, Claude Code, any
  Agent Client Protocol server through the `acp` driver, or any other agent
  CLI through the `command` driver. Nothing here names a platform.
- Each bot contains an ordered `agents` list describing when it uses those
  definitions. Nothing in a bot names a harness beyond the binding itself.
- One agent runtime can serve multiple bots and conversations.
- Persistent sessions remain isolated by agent, service, bot, DM, channel, and
  thread.

Changing which harness answers a conversation is a one-line edit followed by
`pantalk reload`. [Pantalk Ghost](https://github.com/pantalk/ghost) ships
Codex, Claude Code, and Kimi Code preinstalled so you can try that swap
immediately.

## Complete example

```yaml
agents:
  - name: codex
    driver: codex
    workdir: /home/me/project
    timeout: 900
    instructions: |
      You are the primary engineering assistant.
    codex:
      sandbox: workspace-write
      approval_policy: never

  - name: claude
    driver: claude
    workdir: /home/me/project
    timeout: 900
    instructions: |
      You are the code-review assistant.
    claude:
      permission_mode: plan

bots:
  - name: company-slack
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN

    agents:
      - agent: claude
        when: 'channel == "#code-review"'

      - agent: codex
        when: direct

      - agent: codex
        when: true

  - name: local-automation
    type: local

    agents:
      - name: morning-review
        agent: codex
        when: 'at("09:00") && weekday in ["mon", "tue", "wed", "thu", "fri"]'
        timezone: Europe/London
        prompt: |
          Review the repository and summarize today's priorities.
```

## Agent definitions

### Codex

```yaml
agents:
  - name: engineering
    driver: codex
    workdir: /home/me/project
    timeout: 900
    instructions: |
      Answer engineering questions and work in this repository.
    codex:
      binary: /usr/local/bin/codex # optional; defaults to codex on PATH
      model: gpt-5.4              # optional; otherwise local default
      effort: high                # optional
      sandbox: workspace-write
      approval_policy: never
```

Pantalk owns one `codex app-server --stdio` process for this definition. It
creates or resumes a durable Codex thread for every Pantalk conversation.

### Claude Code

```yaml
agents:
  - name: reviewer
    driver: claude
    workdir: /home/me/project
    timeout: 900
    instructions: |
      Review code and explain findings.
    claude:
      binary: /usr/local/bin/claude # optional; defaults to claude on PATH
      model: sonnet                  # optional
      effort: high                   # optional
      permission_mode: plan
      allowed_tools: [Read, Grep, Glob]
      disallowed_tools: [Edit, Write]
```

Claude Code authentication and omitted settings come from the local CLI
installation. Pantalk persists Claude session IDs for conversation continuity.

### ACP

The `acp` driver speaks the [Agent Client Protocol](https://agentclientprotocol.com)
and works with any agent that can run as an ACP server over stdio. The agent
is not baked into the driver: `command` names it, exactly like the `command`
driver. Kimi Code (`kimi acp`) is the canonical example:

```yaml
agents:
  - name: kimi-engineering
    driver: acp
    command: kimi acp             # any ACP-server command works here
    workdir: /home/me/project
    timeout: 900
    instructions: |
      You are the primary engineering assistant.
    acp:
      model: kimi-k3              # optional; otherwise the agent's local default
      approval: reject            # reject (default), approve, or approve-for-session
```

Pantalk owns one ACP server process for this definition and creates or loads a
durable ACP session for every Pantalk conversation. Sessions persist across
daemon restarts when the agent supports session loading (Kimi Code and Goose
both do) and are typically looked up per working directory, so keep `workdir`
stable.

Other agents work the same way. [Goose](https://github.com/aaif-goose/goose)
serves ACP natively, so it needs no adapter:

```yaml
agents:
  - name: goose-engineering
    driver: acp
    command: goose acp
    workdir: /home/me/project
```

[zot](https://github.com/chatbotkit/zot) does too, via `command: zot acp`.
Codex and Claude Code are the exception: they reach ACP through the separate
`codex-acp` and `claude-agent-acp` adapters, so the native `codex` and `claude`
drivers are usually the better choice for them.

Authentication comes from the agent's local installation (for Kimi Code:
`kimi login`; for Goose: `goose configure`). An agent may advertise ACP
`authMethods` during initialization — Goose offers one — but Pantalk does not
implement the protocol's `authenticate` call, so an unconfigured agent fails
when its first session starts rather than reporting that it needs credentials.

`approval` answers the agent's tool-permission requests:
`reject` denies anything the agent cannot do on its own, `approve` allows each
request once, and `approve-for-session` grants a standing approval per
session. `model` selection uses ACP's model surface where the agent offers
one. ACP has no dedicated instruction channel, so `instructions` are prepended
to the first prompt of each new session.

The same allowlist as the `command` driver applies: only known agent binaries
may be named unless `pantalkd` starts with `--allow-exec`.

## Isolation

An agent can run its harness in a container instead of alongside the daemon:

```yaml
agents:
  - name: reviewer
    driver: acp
    command: zot acp
    isolation: container
```

The shorthand expands to a block when something needs overriding:

```yaml
    isolation:
      mode: container            # container | none (default)
      image: ghcr.io/acme/zot:v3 # required unless the harness has a known image
      workspace: /workspace      # in-container working directory
      runtime: docker            # docker | podman
```

Pantalk compiles the container invocation itself. The agent keeps naming its
harness, so the allowlist still checks `zot` rather than the container runtime
and `--allow-exec` is not needed. Each isolated agent gets its own workspace
volume, `pantalk-<agent>-workspace`, which is what keeps one agent's work out
of reach of the others.

Two consequences worth knowing:

- `command` and `binary` become paths **inside the image**, not on this host. A
  `claude.binary: /opt/homebrew/bin/claude` that works uncontained will not
  resolve in a container.
- The Codex driver's own sandbox is disabled (`danger-full-access`) for
  isolated agents, because the container is already the boundary and Codex's
  bubblewrap needs user namespaces an unprivileged container usually denies. An
  explicit `codex.sandbox` always wins.

## Overriding the harness binary

Every driver accepts `command`, which replaces the binary it would otherwise
run while the driver still appends its own protocol arguments:

```yaml
agents:
  - name: pinned
    driver: claude
    command: /opt/claude-2.1/bin/claude
```

Use it for wrappers, pinned versions, or a path that only exists inside an
image. It cannot be combined with `codex.binary` or `claude.binary`, and an
overriding command faces the same allowlist as the `command` driver.

### Command

```yaml
agents:
  - name: notification-checker
    driver: command
    command: claude -p "Check Pantalk notifications and respond"
    workdir: /home/me/project
    buffer: 30
    timeout: 120
    cooldown: 60
```

Command agents retain the fire-and-forget CLI workflow. Commands are executed
directly without a shell. Only known agent binaries are allowed unless
`pantalkd` starts with `--allow-exec`.

An unbound agent definition is valid but its runtime is not started.

### Environment

Every driver runs its agent as a child process, and every driver accepts the
same agent-level `env` map and `env_inherit` list. **An agent process inherits
nothing from the daemon.** Its environment is exactly what its own definition
names - nothing else, including `PATH` and `HOME`.

This is deliberate. The daemon's environment holds every bot credential a
config resolved through `$NAME`, so an inheriting child would hand a single
agent the tokens for every service Pantalk serves. Since agents answer messages
from people who are not you, one prompt injection would be enough. Default-deny
also makes per-agent secrets real: naming `$API_KEY` in one definition no
longer exposes it to the others.

A value written as `$NAME` is resolved from the daemon's environment at
startup, which lets a config name a secret without containing one. Startup
fails with a clear error if the variable is unset, rather than launching an
agent with an empty credential.

`env_inherit` copies variables through by name, for the process settings an
agent needs but that are not worth writing into a config. A name ending in `*`
copies every variable with that prefix, and an unset name is skipped rather
than being an error. An explicit `env` entry wins over an inherited one.

```yaml
agents:
  - name: engineering
    driver: claude
    env_inherit: [PATH, HOME, USER, SHELL, TMPDIR, TZ, LANG, 'LC_*']
```

Most definitions want something close to that list. `HOME` matters most: local
CLI authentication, settings, `CLAUDE.md`/`AGENTS.md` files, skills, and MCP
configuration are all found through it, so a driver that should inherit your
local login must inherit `HOME`. Command agents that call the `pantalk` CLI
also need `XDG_RUNTIME_DIR` when the daemon uses a non-default socket path.

Everything else stays opt-in:

```yaml
agents:
  - name: proxied
    driver: codex          # or claude, acp, command
    env:
      HTTPS_PROXY: http://proxy.internal:8080
```

Because Claude Code selects its backend through the environment, this is also
how the `claude` driver is pointed at any endpoint speaking the Anthropic
Messages protocol:

```yaml
agents:
  - name: reviewer
    driver: claude
    claude:
      model: some-model
    env:
      ANTHROPIC_BASE_URL: https://api.example.com/anthropic
      ANTHROPIC_AUTH_TOKEN: $API_KEY
```

## Bot bindings

```yaml
bots:
  - name: engineering-slack
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN

    agents:
      - agent: reviewer
        when: 'channel == "#reviews"'

      - agent: engineering
        when: 'direct || mentions'

      - agent: engineering
        when: true
```

For an inbound message, bindings are evaluated in their written order and the
first matching binding wins. Consequently, fallback rules belong last.

If `when` is omitted, it defaults to `notify`. A notification is an inbound
message that is a DM, mentions the bot, or continues a conversation in which
the bot has participated.

Useful expression fields:

| Field          | Meaning                                      |
| -------------- | -------------------------------------------- |
| `notify`       | Normal Pantalk notification                  |
| `direct`       | Direct message                               |
| `mentions`     | Message explicitly mentions the bot          |
| `channel`      | Alias-aware channel reference                |
| `channel_id`   | Raw provider channel identifier              |
| `channel_name` | Friendly channel name when available         |
| `thread`       | Provider thread identifier                    |
| `bot`          | Configured bot name                           |
| `service`      | Integration type                              |
| `user`         | Provider user identifier                      |
| `text`         | Message text                                  |

Examples:

```yaml
when: direct
when: 'direct || mentions'
when: 'channel == "#incidents"'
when: 'channel == "C0123456789"'
when: 'channel in ["#incidents", "#alerts"]'
when: 'text matches "deploy|rollback|hotfix"'
when: 'thread != ""'
when: true
```

The `channel` value compares against either the stable provider ID or the
friendly name. Slack names may be written with or without `#`.

## Scheduled bindings

Time expressions live in the same bot-to-agent binding:

```yaml
bots:
  - name: local-automation
    type: local
    agents:
      - name: morning-review
        agent: engineering
        when: 'at("09:00") && weekday in ["mon", "tue", "wed", "thu", "fri"]'
        timezone: Europe/London
        prompt: |
          Review the repository and summarize today's priorities.

      - name: periodic-check
        agent: engineering
        when: 'every("30m")'
        timezone: UTC
        prompt: |
          Check for important outstanding work.
```

Time-based bindings require:

- A unique `name` within the bot.
- A `prompt` used as the scheduled turn text.
- An optional IANA `timezone`; it defaults to `UTC`.

Local schedules derive a stable channel named `schedule:<binding-name>`.
This gives persistent agents a durable conversation and makes replies visible
through Pantalk's local history and chat surfaces.

A scheduled binding on a network bot must provide a `channel` or `target`:

```yaml
bots:
  - name: company-slack
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN
    agents:
      - name: morning-report
        agent: engineering
        when: 'at("09:00")'
        timezone: Europe/London
        channel: "#engineering"
        prompt: Prepare the morning engineering report.
```

Every due time-based binding runs independently. Ordinary message fallbacks
such as `when: true` are not evaluated on clock ticks. Pantalk suppresses
duplicate execution of the same scheduled minute during config reloads.
Schedules do not catch up occurrences missed while the daemon was offline.

## Direct messages and channel allowlists

`when: direct` is provider-neutral. Slack `D…` conversations, Discord private
channels, Telegram private chats, Mattermost direct channels, WhatsApp direct
chats, XMPP direct chats, Nostr NIP-17 DMs, Zulip private messages, SMS,
iMessage DMs, and IRC private messages are normalized to the same expression
field. Twitch supports channel chat but not direct messages.

Provider `channels` lists remain ingress restrictions rather than agent
routing. Direct messages are admitted independently where the provider clearly
distinguishes them; use the bot bindings to decide whether an agent responds.

## Breaking configuration change

Routing no longer belongs in top-level agent definitions. The following old
shape is rejected:

```yaml
agents:
  - name: engineering
    driver: codex
    bots: [company-slack]
    when: direct
```

Move the routing fields into the relevant bot:

```yaml
agents:
  - name: engineering
    driver: codex

bots:
  - name: company-slack
    type: slack
    agents:
      - agent: engineering
        when: direct
```
