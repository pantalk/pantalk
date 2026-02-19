# Agents

Pantalk can automatically launch AI agents when matching notifications arrive. Instead of polling for new messages, you define agents in your config and `pantalkd` triggers them reactively — buffering events, enforcing cooldowns, and restricting which binaries can run.

Agents are **fire-and-forget**. When triggered, the command runs and reads notifications itself via `pantalk notifications`. No events are piped to stdin.

## Quick Example

```yaml
bots:
  - name: ops-bot
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN

agents:
  - name: responder
    when: "direct || mentions"
    command: claude -p "Check pantalk notifications and respond"
    workdir: /home/user/project
```

When someone DMs the bot or @mentions it, `pantalkd` waits 30 seconds (batching), then launches `claude` with the given prompt. The agent calls `pantalk notifications --unseen` to read what happened and acts on it.

## Configuration

Each agent is defined under the `agents` key in your config:

```yaml
agents:
  - name: responder              # required — unique identifier
    when: "direct || mentions"   # expression (default: "notify")
    command: claude -p "Check notifications"  # required
    workdir: /home/user/project  # optional — inherits daemon's cwd
    buffer: 30                   # seconds to batch events (default: 30)
    timeout: 120                 # kill after N seconds (default: 120)
    cooldown: 60                 # min gap between runs (default: 60)
```

### Fields

| Field      | Required | Default    | Description                                               |
| ---------- | -------- | ---------- | --------------------------------------------------------- |
| `name`     | yes      | —          | Unique identifier, used in log messages                   |
| `when`     | no       | `"notify"` | Boolean expression evaluated against each event           |
| `command`  | yes      | —          | Binary + args to exec (string or array)                   |
| `workdir`  | no       | daemon cwd | Working directory for the command                         |
| `buffer`   | no       | `30`       | Seconds to wait and batch events before launching         |
| `timeout`  | no       | `120`      | Maximum runtime in seconds before the process is killed   |
| `cooldown` | no       | `60`       | Minimum seconds between consecutive runs of this agent    |

### Command Format

The `command` field accepts both a string and an array. It is **exec'd directly** — never passed through a shell.

```yaml
# String form — tokenized with shell-like quoting (no variable expansion)
command: claude -p "Check pantalk notifications and respond"

# Array form — each element is a separate argv entry
command:
  - claude
  - -p
  - "Check pantalk notifications and respond"
```

Both forms produce the same argv: `["claude", "-p", "Check pantalk notifications and respond"]`.

## When Expressions

The `when` field uses the [expr](https://github.com/expr-lang/expr) expression language. Expressions are boolean and evaluated against each inbound message event.

### Available Fields

**Event fields** — populated on message events, zero on tick events:

| Field      | Type   | Description                                      |
| ---------- | ------ | ------------------------------------------------ |
| `notify`   | bool   | Event is a notification (DM, mention, or thread) |
| `direct`   | bool   | Event is a direct message to the bot             |
| `mentions` | bool   | Event mentions the bot                           |
| `channel`  | string | Channel name or ID (e.g. `"#general"`)           |
| `thread`   | string | Thread ID (empty if not in a thread)             |
| `bot`      | string | Bot name from config                             |
| `service`  | string | Platform type (`"slack"`, `"discord"`, etc.)     |
| `user`     | string | User ID of the message author                    |
| `text`     | string | Message text content                             |

**Time fields** — populated on tick events (1-minute internal clock), zero on message events:

| Field      | Type   | Description                                      |
| ---------- | ------ | ------------------------------------------------ |
| `tick`     | bool   | True on clock tick events                        |
| `hour`     | int    | Current hour (0–23)                              |
| `minute`   | int    | Current minute (0–59)                            |
| `weekday`  | string | Day name: `"mon"`, `"tue"`, ..., `"sun"`         |

### Time Functions

| Function             | Description                                         |
| -------------------- | --------------------------------------------------- |
| `at("HH:MM", ...)`  | True when current time matches any listed time      |
| `every("Nm")`        | True on aligned minute intervals (e.g. :00, :15, :30, :45 for `"15m"`) |
| `every("Nh")`        | True on aligned hour intervals at minute :00        |

### Operators

| Operator  | Example                                  |
| --------- | ---------------------------------------- |
| `&&`      | `notify && bot == "ops-bot"`             |
| `\|\|`    | `direct \|\| mentions`                   |
| `!`       | `notify && !direct`                      |
| `==`      | `channel == "#incidents"`                |
| `!=`      | `service != "telegram"`                  |
| `in`      | `channel in ["#incidents", "#alerts"]`   |
| `matches` | `text matches "deploy\|rollback"`        |
| `()`      | `(direct && text matches "deploy") \|\| mentions` |

### Examples

```yaml
# Default — trigger on any notification
when: "notify"

# Only direct messages
when: "direct"

# DMs or @mentions
when: "direct || mentions"

# Notifications in a specific channel
when: 'notify && channel == "#incidents"'

# Multiple channels
when: 'notify && channel in ["#incidents", "#alerts"]'

# Only from a specific bot
when: 'notify && bot == "ops-bot"'

# Only from a specific platform
when: 'service == "slack" && notify'

# Text content matching (regex)
when: 'notify && text matches "deploy|rollback|hotfix"'

# Threaded messages only
when: 'notify && thread != ""'

# Complex: DMs about deploys OR any mention in #ops
when: '(direct && text matches "deploy") || (mentions && channel == "#ops")'

# Match all inbound messages (not just notifications)
when: "true"

# Everything except DMs
when: "notify && !direct"
```

### Time-Based Examples

```yaml
# Run at specific times
when: 'at("9:00")'
when: 'at("9:00", "12:30", "17:00")'    # variadic — multiple times

# Run on intervals
when: 'every("15m")'                     # :00, :15, :30, :45
when: 'every("2h")'                      # 0:00, 2:00, 4:00, ...
when: 'every("10m")'                     # :00, :10, :20, ...

# Weekday mornings only
when: 'at("9:00") && weekday in ["mon", "tue", "wed", "thu", "fri"]'

# Business hours only
when: 'every("15m") && hour >= 9 && hour < 17'

# Mix time + events — wake on schedule OR when someone DMs
when: 'at("9:00", "17:00") || direct'
when: 'every("30m") || mentions'
```

Time expressions only fire on the daemon's internal 1-minute clock. The default `when: "notify"` does **not** match clock ticks — you must explicitly use `at()`, `every()`, or the `tick` field.

## Security

Agent commands are restricted to a set of known AI agent binaries by default:

| Binary     |
| ---------- |
| `claude`   |
| `codex`    |
| `copilot`  |
| `aider`    |
| `goose`    |
| `opencode` |
| `gemini`   |

If the first token of `command` is not in this list, config validation fails:

```
agent "deploy-hook": command "bash" is not in the allowed list
(claude, codex, copilot, aider, goose, opencode, gemini);
start pantalkd with --allow-exec to permit arbitrary commands
```

### `--allow-exec`

To run arbitrary commands, start the daemon with the `--allow-exec` flag:

```bash
pantalkd --allow-exec
```

This bypasses the binary allowlist entirely. Use with caution — the command has the same privileges as the `pantalkd` process.

### Path-qualified binaries

Full paths are supported. The binary name is extracted for allowlist checking:

```yaml
# Allowed — filepath.Base extracts "claude"
command: /usr/local/bin/claude -p "Check notifications"
```

### No shell interpretation

Commands are **never** passed through a shell. There is no variable expansion, globbing, or piping. The command string is tokenized with simple quoting rules:

- Single quotes preserve literal content: `'hello world'` → `hello world`
- Double quotes allow backslash escapes: `"say \"hi\""` → `say "hi"`
- No `$VAR` expansion, no `~`, no `*`

## Lifecycle

```
Event arrives               Clock tick (every 1 min)
    │                              │
    ▼                              ▼
Matches(event)          ← expression evaluated
    │ yes
    ▼
Handle(event)           ← event buffered
    │
    ▼
  ┌─────────────┐
  │ Buffer timer │  (default 30s — batches rapid events)
  └──────┬──────┘
         ▼
  ┌──────────────┐
  │ Cooldown OK? │  ← last run finished > cooldown ago?
  └──────┬───────┘
     no  │  yes
     ▼   ▼
  retry  │
  later  │
         ▼
  ┌──────────────┐
  │ Already      │  ← only one instance per agent at a time
  │ running?     │
  └──────┬───────┘
     yes │  no
     ▼   ▼
  retry  │
  later  │
         ▼
   exec command        ← direct exec, no shell
         │
         ▼
   log output + status
```

### Buffering

When the first matching event arrives, a timer starts (default 30 seconds). Additional events during this window accumulate silently. When the timer fires, the agent launches with the total count logged. This prevents an agent from launching on every single message in a busy channel.

### Cooldown

After a run completes, the agent enters a cooldown period (default 60 seconds). Events arriving during cooldown are re-buffered and the timer is rescheduled.

### Concurrency

Only one instance of each agent can run at a time. If the agent is still running when new events arrive and the buffer fires, the launch is deferred and retried after 5 seconds.

### Timeout

If the agent process exceeds its timeout (default 120 seconds), it is killed via `context.WithTimeout`.

## Full Example

```yaml
server:
  notification_history_size: 1000

bots:
  - name: ops-bot
    type: slack
    bot_token: $SLACK_BOT_TOKEN
    app_level_token: $SLACK_APP_LEVEL_TOKEN
    channels:
      - C0123456789

agents:
  # Respond to DMs and mentions
  - name: responder
    when: "direct || mentions"
    command: claude -p "Check pantalk notifications --unseen and respond to each"
    workdir: /home/user/project
    buffer: 15
    timeout: 180
    cooldown: 30

  # Watch for incidents
  - name: incident-triage
    when: 'notify && channel == "#incidents"'
    command: claude -p "Triage the latest incident from pantalk notifications"
    workdir: /home/user/ops
    buffer: 10
    timeout: 300
    cooldown: 120

  # Code review requests
  - name: reviewer
    when: 'notify && text matches "review|PR|pull request"'
    command:
      - aider
      - --check
    workdir: /home/user/repos

  # Morning digest — weekdays at 9 AM
  - name: morning-digest
    when: 'at("9:00") && weekday in ["mon", "tue", "wed", "thu", "fri"]'
    command: claude -p "Summarize overnight pantalk notifications"
    workdir: /home/user/project
    timeout: 300

  # Periodic check + DM trigger — every 30 min OR direct message
  - name: periodic
    when: 'every("30m") || direct'
    command: claude -p "Check pantalk notifications --unseen and respond"
    workdir: /home/user/project
```

## How Agents Read Notifications

Agents are not given events on stdin. When launched, they use the standard `pantalk` CLI to read what triggered them:

```bash
# Inside the agent's prompt or script
pantalk notifications --unseen --limit 20
pantalk notifications --unseen --clear
```

This keeps the interface consistent — agents use the same CLI as interactive users.
