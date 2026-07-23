# Agents

Pantalk separates reusable agent definitions from bot-specific routing.

- Top-level `agents` describe how Codex, Claude Code, or a command starts.
- Each bot contains an ordered `agents` list describing when it uses those
  definitions.
- One agent runtime can serve multiple bots and conversations.
- Persistent sessions remain isolated by agent, service, bot, DM, channel, and
  thread.

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
chats, Zulip private messages, SMS, iMessage DMs, and IRC private messages are
normalized to the same expression field.

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
