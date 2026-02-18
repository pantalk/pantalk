# Using Pantalk as a Claude Code Hook

This guide shows how to connect Claude Code's [hooks system](https://code.claude.com/docs/en/hooks) to Pantalk, so Claude Code can send and receive messages on Slack, Discord, Mattermost, and Telegram automatically.

## What This Enables

Claude Code hooks are shell commands that run at specific points in Claude's lifecycle. Since Pantalk is a CLI tool, it slots in directly — no glue code or SDKs needed.

| Hook Event       | What Pantalk Does                                                    |
| ---------------- | -------------------------------------------------------------------- |
| `Notification`   | Forward Claude Code alerts (permission prompts, idle) to a chat channel |
| `Stop`           | Check for new chat messages and tell Claude to keep working if needed |
| `SessionStart`   | Load recent chat context into Claude's session                       |
| `PostToolUse`    | Report progress to a chat channel after file edits                   |
| `SessionEnd`     | Post a summary when the coding session ends                          |

## Prerequisites

- Pantalk installed and `pantalkd` running with at least one bot configured
- Claude Code installed ([code.claude.com](https://code.claude.com))
- `jq` available on your system

Verify pantalk is working:

```bash
pantalk bots
```

## Setup

### Hook scripts location

Create a directory for your hook scripts:

```bash
mkdir -p .claude/hooks
```

Claude Code reads hooks from `.claude/settings.json` (project-level) or `~/.claude/settings.json` (global).

### Configure the bot name

All scripts below use the `PANTALK_BOT` and `PANTALK_CHANNEL` environment variables. Set them in a `SessionStart` hook or hardcode them in each script.

---

## Hook 1: Forward Notifications to Chat

When Claude Code needs your attention — a permission prompt, an idle timeout — this hook sends the notification to your chat channel so you don't have to watch the terminal.

### Script

```bash
#!/bin/bash
# .claude/hooks/notify-chat.sh
# Forwards Claude Code notifications to a chat channel via pantalk.

INPUT=$(cat)
MESSAGE=$(echo "$INPUT" | jq -r '.message // empty')
TITLE=$(echo "$INPUT" | jq -r '.title // empty')
TYPE=$(echo "$INPUT" | jq -r '.notification_type // empty')

BOT="${PANTALK_BOT:-my-bot}"
CHANNEL="${PANTALK_CHANNEL:-C0123456789}"

# Build a formatted message
if [ -n "$TITLE" ]; then
  TEXT="🔔 *${TITLE}*: ${MESSAGE}"
else
  TEXT="🔔 ${MESSAGE}"
fi

# Add the notification type as context
if [ -n "$TYPE" ]; then
  TEXT="${TEXT} [${TYPE}]"
fi

pantalk send --bot "$BOT" --channel "$CHANNEL" --text "$TEXT" 2>/dev/null

exit 0
```

```bash
chmod +x .claude/hooks/notify-chat.sh
```

### Configuration

```json
{
  "hooks": {
    "Notification": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/notify-chat.sh",
            "async": true,
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

The `async: true` flag is important here — notifications should not block Claude's work.

---

## Hook 2: Check Chat Messages on Stop

This is the most powerful integration. When Claude finishes a response, this hook checks pantalk for new messages. If someone has sent a message on Slack/Discord, Claude is told to continue working and respond.

This turns Claude Code into a chat-responsive agent.

### Script

```bash
#!/bin/bash
# .claude/hooks/check-chat.sh
# On Stop, check pantalk for unseen notifications. If there are new messages,
# tell Claude to continue and respond to them.

INPUT=$(cat)
STOP_HOOK_ACTIVE=$(echo "$INPUT" | jq -r '.stop_hook_active // false')

# Prevent infinite loops: if a stop hook already triggered continuation,
# don't check again
if [ "$STOP_HOOK_ACTIVE" = "true" ]; then
  exit 0
fi

BOT="${PANTALK_BOT:-my-bot}"

# Check for unseen notifications
NOTIFICATIONS=$(pantalk notifications --bot "$BOT" --unseen --json 2>/dev/null)
COUNT=$(echo "$NOTIFICATIONS" | jq -r 'length // 0' 2>/dev/null)

if [ "$COUNT" -gt 0 ] && [ "$COUNT" != "0" ]; then
  # Format the messages for Claude
  MESSAGES=$(echo "$NOTIFICATIONS" | jq -r '.[] | "[\(.user // "unknown")]: \(.text // "")"' 2>/dev/null)

  # Tell Claude to continue and respond to these messages
  jq -n \
    --arg reason "New chat messages received via pantalk. Read and respond to them using the pantalk-send-message and pantalk-read-notifications skills." \
    --arg context "Unseen chat messages (${COUNT} total):\n${MESSAGES}" \
    '{
      "decision": "block",
      "reason": $reason,
      "hookSpecificOutput": {
        "hookEventName": "Stop",
        "additionalContext": $context
      }
    }'
else
  exit 0
fi
```

```bash
chmod +x .claude/hooks/check-chat.sh
```

### Configuration

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/check-chat.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

> **Important:** The `stop_hook_active` check prevents infinite loops. Without it, Claude would stop → check chat → continue → stop → check chat → forever. The guard ensures the hook only fires on "natural" stops, not hook-triggered continuations.

---

## Hook 3: Load Chat Context on Session Start

Pull recent conversation history into Claude's session so it has context about what's been discussed.

### Script

```bash
#!/bin/bash
# .claude/hooks/load-chat-context.sh
# On SessionStart, load recent chat history as context for Claude.

BOT="${PANTALK_BOT:-my-bot}"
CHANNEL="${PANTALK_CHANNEL:-C0123456789}"

# Get recent history
HISTORY=$(pantalk history --bot "$BOT" --channel "$CHANNEL" --limit 10 --json 2>/dev/null)
COUNT=$(echo "$HISTORY" | jq -r 'length // 0' 2>/dev/null)

if [ "$COUNT" -gt 0 ] && [ "$COUNT" != "0" ]; then
  MESSAGES=$(echo "$HISTORY" | jq -r '.[] | "[\(.user // "unknown")]: \(.text // "")"' 2>/dev/null)

  jq -n \
    --arg ctx "Recent chat messages from ${BOT} in channel ${CHANNEL}:\n${MESSAGES}" \
    '{
      "hookSpecificOutput": {
        "hookEventName": "SessionStart",
        "additionalContext": $ctx
      }
    }'
else
  exit 0
fi
```

```bash
chmod +x .claude/hooks/load-chat-context.sh
```

### Configuration

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/load-chat-context.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

---

## Hook 4: Report Progress to Chat

After Claude edits or writes a file, post an update to your chat channel.

### Script

```bash
#!/bin/bash
# .claude/hooks/report-edit.sh
# After Write or Edit, send a progress update to chat.

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name // empty')
FILE=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')

BOT="${PANTALK_BOT:-my-bot}"
CHANNEL="${PANTALK_CHANNEL:-C0123456789}"

# Only report if we have a file path
if [ -z "$FILE" ]; then
  exit 0
fi

# Strip common prefixes for readability
SHORT_FILE=$(echo "$FILE" | sed "s|$HOME/||; s|$(pwd)/||")

if [ "$TOOL" = "Write" ]; then
  TEXT="📝 Created \`${SHORT_FILE}\`"
elif [ "$TOOL" = "Edit" ]; then
  TEXT="✏️ Edited \`${SHORT_FILE}\`"
else
  TEXT="🔧 ${TOOL} on \`${SHORT_FILE}\`"
fi

pantalk send --bot "$BOT" --channel "$CHANNEL" --text "$TEXT" 2>/dev/null

exit 0
```

```bash
chmod +x .claude/hooks/report-edit.sh
```

### Configuration

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/report-edit.sh",
            "async": true,
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

---

## Combined Configuration

Here's a complete `.claude/settings.json` that combines all four hooks:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/load-chat-context.sh",
            "timeout": 10
          }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/notify-chat.sh",
            "async": true,
            "timeout": 10
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/report-edit.sh",
            "async": true,
            "timeout": 10
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/check-chat.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

### Environment setup

Set your bot name and channel. You can either hardcode them in each script or set them via a `SessionStart` environment hook:

```bash
#!/bin/bash
# .claude/hooks/set-pantalk-env.sh
if [ -n "$CLAUDE_ENV_FILE" ]; then
  echo 'export PANTALK_BOT=my-bot' >> "$CLAUDE_ENV_FILE"
  echo 'export PANTALK_CHANNEL=C0123456789' >> "$CLAUDE_ENV_FILE"
fi
exit 0
```

Add this as a separate `SessionStart` hook to make the variables available to all subsequent hooks and Bash commands:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/set-pantalk-env.sh"
          }
        ]
      },
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/load-chat-context.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

---

## Debugging

Run Claude Code with `--debug` to see hook execution:

```bash
claude --debug
```

Test individual hook scripts manually:

```bash
# Test notification forwarding
echo '{"message":"Claude needs permission","title":"Permission needed","notification_type":"permission_prompt"}' | .claude/hooks/notify-chat.sh

# Test chat check (simulates a Stop event)
echo '{"stop_hook_active":false}' | .claude/hooks/check-chat.sh

# Test session context loading
echo '{"source":"startup"}' | .claude/hooks/load-chat-context.sh
```

---

## Design Considerations

### Sync vs. Async

| Hook              | Sync/Async | Why                                                             |
| ----------------- | ---------- | --------------------------------------------------------------- |
| `Notification`    | Async      | Sending to chat shouldn't block Claude's work                   |
| `Stop`            | Sync       | Must be sync to return a decision that blocks Claude from stopping |
| `SessionStart`    | Sync       | Context must be loaded before Claude starts processing          |
| `PostToolUse`     | Async      | Progress reports shouldn't slow down editing                    |

### Loop Prevention

The `Stop` hook checks `stop_hook_active` to prevent infinite loops. When Claude stops naturally, the hook checks for messages. If it finds messages and tells Claude to continue, the next stop will have `stop_hook_active: true`, so the hook exits cleanly.

### Timeout

All hooks use a 10-second timeout. Pantalk commands are fast (local Unix socket), but the timeout prevents hangs if `pantalkd` is down.

### Error Handling

All `pantalk` commands redirect stderr to `/dev/null` and the scripts exit 0 on failure. A failed chat send should never block Claude's work. If you want visibility into failures, log stderr to a file instead:

```bash
pantalk send --bot "$BOT" --channel "$CHANNEL" --text "$TEXT" 2>>/tmp/pantalk-hook-errors.log
```
