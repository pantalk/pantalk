# iMessage Setup

Pantalk connects to iMessage natively on macOS. It reads incoming messages directly from the Messages database (`~/Library/Messages/chat.db`) and sends outbound messages via AppleScript through Messages.app. No third-party servers or tools are required — just a Mac with Messages signed in.

## Prerequisites

- A Mac with **macOS Monterey or later** and **Messages** signed in to an iMessage account
- **Full Disk Access** granted to the process running `pantalkd` (required to read `chat.db`)
- **Automation** permission for Messages.app (granted automatically on first send)
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 — Grant Permissions

### Full Disk Access

Pantalk reads `~/Library/Messages/chat.db` directly. macOS requires Full Disk Access for any process that touches this file.

1. Open **System Settings → Privacy & Security → Full Disk Access**
2. Click the **+** button and add your terminal application (e.g. Terminal.app, iTerm2, or the `pantalkd` binary itself)
3. Restart the terminal or relaunch `pantalkd`

### Automation Permission

The first time Pantalk sends a message, macOS will prompt you to allow it to control Messages.app. Click **OK** to grant the permission. You can also pre-grant this in **System Settings → Privacy & Security → Automation**.

## Step 2 — Configure Pantalk

Add the bot to your Pantalk config:

```yaml
bots:
  - name: my-imessage
    type: imessage
```

That's it — no credentials needed. The connector defaults to reading from `~/Library/Messages/chat.db`.

### Config Fields

| Field     | Required | Description                                                      |
| --------- | -------- | ---------------------------------------------------------------- |
| `name`    | Yes      | Bot name (used in events and CLI commands)                       |
| `type`    | Yes      | Must be `imessage`                                               |
| `db_path` | No       | Path to Messages database (defaults to `~/Library/Messages/chat.db`) |
| `channels`| No       | Limit to specific chat identifiers (empty = all conversations)   |

### Optional: Custom Database Path

If the Messages database is in a non-standard location (e.g. a backup or test database):

```yaml
bots:
  - name: my-imessage
    type: imessage
    db_path: /path/to/chat.db
```

### Optional: Channel Filtering

Limit which conversations the bot listens to:

```yaml
bots:
  - name: my-imessage
    type: imessage
    channels:
      - '+15551234567'             # DM by phone number
      - 'user@example.com'        # DM by email address
      - 'chat123456789'           # group chat ID
```

## Step 3 — Verify

Start the daemon and confirm the bot connects:

```bash
pantalkd &
pantalk bots
```

You should see your iMessage bot listed with status "connector online". Send a test message:

```bash
pantalk send --bot my-imessage --channel "+15551234567" --text "Hello from Pantalk!"
```

## How It Works

### Incoming Messages

Pantalk opens `chat.db` in **read-only** mode and polls for new rows every 2 seconds using a ROWID high-water mark. It joins the `message`, `handle`, `chat`, and `chat_message_join` tables to build complete message metadata. Self-sent messages and empty messages (attachments-only, reactions) are automatically filtered out.

### Outgoing Messages

Pantalk sends messages via AppleScript (`osascript`) using the `tell application "Messages"` command. This is the same mechanism that Shortcuts and Automator use.

## Troubleshooting

| Symptom                                   | Cause                                                                              |
| ----------------------------------------- | ---------------------------------------------------------------------------------- |
| `cannot open database` or `not permitted` | Full Disk Access not granted — add your terminal/binary in System Settings         |
| `database check failed`                   | Messages.app not signed in or `chat.db` does not exist                             |
| Connected but no messages                 | Messages arrive with a 2s polling delay; also check channel filters                |
| `imessage send failed`                    | Automation permission not granted for Messages.app, or Messages is not running     |
| Group messages missing                    | Ensure the group chat identifier is in your `channels` list (or remove the filter) |
| `not a database` error                    | The `db_path` points to an invalid or corrupted file                               |
