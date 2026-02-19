# WhatsApp Setup

Pantalk connects to WhatsApp using the **Web multi-device protocol** (via [whatsmeow](https://github.com/tulir/whatsmeow)). No WhatsApp Business account, API keys, or public server are required — you pair by scanning a QR code printed directly in your terminal, just like linking WhatsApp Web.

## Prerequisites

- A phone with WhatsApp installed and an active account
- Your Pantalk binaries installed (`pantalk` and `pantalkd`)

## Step 1 — Add the Bot to Your Config

Add a WhatsApp bot entry. No tokens or credentials are needed upfront:

```yaml
bots:
  - name: my-whatsapp
    type: whatsapp
```

That's it. Authentication is handled separately via `pantalk pair` before starting the daemon.

### Optional Fields

| Field      | Purpose                                                       | Default                                           |
| ---------- | ------------------------------------------------------------- | ------------------------------------------------- |
| `db_path`  | Path to the whatsmeow SQLite database (encryption keys, etc.) | `~/.local/share/pantalk/whatsapp-<name>.db`       |
| `channels` | Allowlist of chat JIDs to listen to (empty = all chats)       | All chats                                         |

```yaml
bots:
  - name: my-whatsapp
    type: whatsapp
    db_path: /custom/path/whatsapp.db
    channels:
      - '1234567890@s.whatsapp.net'       # personal chat (phone number)
      - '12345678-9876543@g.us'           # group chat
```

## Step 2 — Pair via QR Code

Pair your WhatsApp account by scanning a QR code. This can be done before or while the daemon is running:

```bash
pantalk pair --bot my-whatsapp
```

A QR code will be printed directly in the terminal:

```
scan this QR code with WhatsApp on your phone:
(Settings → Linked Devices → Link a Device)

█████████████████████████████
█ ▄▄▄▄▄ █ ▀ █▀▄▀█ ▄▄▄▄▄ █
█ █   █ █▀▄▀▀ ▀██ █   █ █
█ █▄▄▄█ █ █ ▀▀▄ █ █▄▄▄█ █
...

waiting for scan...
paired successfully! credentials saved to ~/.local/share/pantalk/whatsapp-my-whatsapp.db
```

Open WhatsApp on your phone:

1. Go to **Settings → Linked Devices** (or tap the three-dot menu → **Linked Devices**)
2. Tap **Link a Device**
3. Point your phone camera at the terminal QR code

> **Note:** The QR code expires after about 60 seconds. If it times out, run the command again.

## Step 3 — Connect the Daemon

If the daemon is already running, `pantalk pair` automatically reloads it after a successful pair — no extra step needed.

If the daemon isn't running yet, start it:

```bash
pantalkd &
```

Either way, you'll see:

```
[whatsapp:my-whatsapp] connected (jid=1234567890@s.whatsapp.net)
```

The session stays valid as long as:

- You don't unlink the device from your phone (**Linked Devices → tap the device → Log Out**)
- You don't delete the whatsmeow database file

## Step 4 — Find Chat IDs (JIDs)

WhatsApp uses JIDs (Jabber IDs) to identify chats:

| Chat type | JID format                           | Example                          |
| --------- | ------------------------------------ | -------------------------------- |
| Personal  | `<phone>@s.whatsapp.net`             | `1234567890@s.whatsapp.net`      |
| Group     | `<id>@g.us`                          | `12345678-9876543@g.us`          |

To discover JIDs, start the daemon with no channel filter and check the incoming events:

```bash
pantalkd &
pantalk stream --bot my-whatsapp
```

Send a message in the target chat. The stream output will include the `channel` field with the JID.

## Verify

```bash
pantalk bots
```

You should see your WhatsApp bot listed. Send a test message:

```bash
# To a personal chat (use phone number without + prefix)
pantalk send --bot my-whatsapp --channel 1234567890@s.whatsapp.net --text "Hello from Pantalk!"

# To a group
pantalk send --bot my-whatsapp --channel 12345678-9876543@g.us --text "Hello group!"
```

You can also use shorthand channel IDs — the connector auto-detects whether it's a group or personal chat:

```bash
# Personal (plain phone number → @s.whatsapp.net)
pantalk send --bot my-whatsapp --channel 1234567890 --text "Hi"

# Group (contains a dash → @g.us)
pantalk send --bot my-whatsapp --channel 12345678-9876543 --text "Hi group"
```

## Re-pairing

If your session becomes invalid (e.g. you logged out from your phone), delete the database and pair again:

```bash
rm ~/.local/share/pantalk/whatsapp-my-whatsapp.db
pantalk pair --bot my-whatsapp   # scan QR again
```

## Troubleshooting

| Symptom                           | Cause                                                                                   |
| --------------------------------- | --------------------------------------------------------------------------------------- |
| QR code looks garbled             | Terminal font may not support Unicode block characters — try a different terminal        |
| QR code timed out                 | Expired after ~60s — run `pantalk pair --bot <name>` again                  |
| `logged out — restart to re-pair` | Session was revoked from phone — delete the db file and restart                         |
| Connected but no messages         | Channel filter is active — remove `channels` to receive all, or check JIDs              |
| Messages from self are ignored    | By design — the connector skips messages sent by the linked account                     |
| Only text messages appear         | Media files are not forwarded; only text, captions, and quoted-text are extracted        |
