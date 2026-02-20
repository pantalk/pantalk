package upstream

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

// defaultMessagesDBPath is the standard location of the iMessage database on
// macOS. Full Disk Access is required for the process to read it.
const defaultMessagesDBPath = "~/Library/Messages/chat.db"

// IMessageConnector bridges iMessage to the PanTalk event stream natively on
// macOS. Incoming messages are read directly from the Messages SQLite database
// (~/Library/Messages/chat.db) and outbound messages are sent via osascript
// (AppleScript → Messages.app). No third-party servers or tools are required —
// just a Mac with Messages signed in.
//
// Requirements:
//   - macOS with Messages.app signed in to an iMessage account
//   - Full Disk Access granted to the process running pantalkd (for chat.db)
//   - Automation permission for Messages.app (granted on first send)
type IMessageConnector struct {
	serviceName string
	botName     string
	dbPath      string
	publish     func(protocol.Event)

	mu         sync.RWMutex
	channels   map[string]struct{}
	lastRowID  int64
	selfHandle string

	// osascriptCmd is the command used to run AppleScript. Overridable for
	// testing so we don't actually invoke osascript in unit tests.
	osascriptCmd string
}

// chatDBRow represents a single message row joined from the Messages SQLite
// database. The schema joins the message, handle, chat, and
// chat_message_join tables.
type chatDBRow struct {
	RowID       int64
	GUID        string
	Text        string
	Date        int64 // Apple's CoreData timestamp (nanoseconds since 2001-01-01)
	IsFromMe    int
	HandleID    string // sender phone/email
	ChatID      string // chat identifier (e.g. "+15551234567" or "chat123456")
	ServiceName string // "iMessage" or "SMS"
	RoomName    string // non-empty for group chats
	DisplayName string // group chat display name
}

func NewIMessageConnector(bot config.BotConfig, publish func(protocol.Event)) (*IMessageConnector, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("imessage connector requires macOS (darwin), current OS is %s", runtime.GOOS)
	}

	dbPath := strings.TrimSpace(bot.DBPath)
	if dbPath == "" {
		dbPath = expandHome(defaultMessagesDBPath)
	} else {
		dbPath = expandHome(dbPath)
	}

	connector := &IMessageConnector{
		serviceName:  bot.Type,
		botName:      bot.Name,
		dbPath:       dbPath,
		publish:      publish,
		channels:     make(map[string]struct{}),
		osascriptCmd: "osascript",
	}

	for _, channel := range bot.Channels {
		trimmed := strings.TrimSpace(channel)
		if trimmed == "" {
			continue
		}
		connector.channels[trimmed] = struct{}{}
	}

	return connector, nil
}

func (c *IMessageConnector) Run(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			c.publishStatus("connector offline")
			return
		default:
		}

		db, err := sql.Open("sqlite3", c.dbPath+"?mode=ro&_journal_mode=WAL")
		if err != nil {
			log.Printf("[imessage:%s] cannot open database: %v", c.botName, err)
			c.publishStatus("imessage database open failed: " + err.Error())
			c.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		// Verify we can actually query the database.
		if err := c.verifyDB(db); err != nil {
			db.Close()
			log.Printf("[imessage:%s] database check failed: %v", c.botName, err)
			c.publishStatus("imessage database check failed: " + err.Error())
			c.sleepOrDone(ctx, backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second

		// Seed lastRowID to the current max so we only pick up new messages.
		c.seedLastRowID(db)

		log.Printf("[imessage:%s] reading from %s (last_rowid=%d)", c.botName, c.dbPath, c.lastRowID)
		c.publishStatus("connector online")
		c.pollLoop(ctx, db)
		db.Close()
	}
}

func (c *IMessageConnector) pollLoop(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	heartbeatTicker := time.NewTicker(45 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			c.publishHeartbeat()
		case <-ticker.C:
			rows, err := c.fetchNewMessages(db)
			if err != nil {
				c.publishStatus("imessage poll error: " + err.Error())
				continue
			}
			for _, row := range rows {
				c.handleIncomingMessage(row)
			}
		}
	}
}

func (c *IMessageConnector) Send(ctx context.Context, request protocol.Request) (protocol.Event, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return protocol.Event{}, fmt.Errorf("text cannot be empty")
	}

	recipient := resolveIMessageChannel(request)
	if recipient == "" {
		return protocol.Event{}, fmt.Errorf("imessage send requires channel or target (phone number or email)")
	}

	c.rememberChannel(recipient)

	if err := c.sendViaAppleScript(ctx, recipient, text); err != nil {
		return protocol.Event{}, fmt.Errorf("imessage send failed: %w", err)
	}

	target := request.Target
	if target == "" {
		target = "dm:" + recipient
	}

	event := protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "out",
		User:      c.Identity(),
		Target:    target,
		Channel:   recipient,
		Thread:    request.Thread,
		Text:      text,
	}
	c.publish(event)

	return event, nil
}

func (c *IMessageConnector) Identity() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.selfHandle != "" {
		return c.selfHandle
	}
	return "self"
}

// verifyDB runs a lightweight query to confirm the database is accessible and
// has the expected schema.
func (c *IMessageConnector) verifyDB(db *sql.DB) error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM message LIMIT 1").Scan(&count)
	if err != nil {
		return fmt.Errorf("cannot query message table: %w", err)
	}
	return nil
}

// seedLastRowID sets the initial lastRowID to the current maximum so the
// connector only processes messages that arrive after startup.
func (c *IMessageConnector) seedLastRowID(db *sql.DB) {
	var maxID sql.NullInt64
	if err := db.QueryRow("SELECT MAX(ROWID) FROM message").Scan(&maxID); err != nil {
		log.Printf("[imessage:%s] could not seed lastRowID: %v", c.botName, err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if maxID.Valid {
		c.lastRowID = maxID.Int64
	}
}

// fetchNewMessages queries the Messages database for rows newer than
// lastRowID. It joins the handle, chat, and chat_message_join tables to
// produce complete message metadata.
func (c *IMessageConnector) fetchNewMessages(db *sql.DB) ([]chatDBRow, error) {
	c.mu.RLock()
	since := c.lastRowID
	c.mu.RUnlock()

	query := `
		SELECT
			m.ROWID,
			m.guid,
			COALESCE(m.text, ''),
			m.date,
			m.is_from_me,
			COALESCE(h.id, ''),
			COALESCE(c.chat_identifier, ''),
			COALESCE(c.service_name, ''),
			COALESCE(c.room_name, ''),
			COALESCE(c.display_name, '')
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
		LEFT JOIN chat c ON cmj.chat_id = c.ROWID
		WHERE m.ROWID > ?
		ORDER BY m.ROWID ASC
		LIMIT 200
	`

	sqlRows, err := db.Query(query, since)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer sqlRows.Close()

	var rows []chatDBRow
	var maxRowID int64

	for sqlRows.Next() {
		var row chatDBRow
		if err := sqlRows.Scan(
			&row.RowID,
			&row.GUID,
			&row.Text,
			&row.Date,
			&row.IsFromMe,
			&row.HandleID,
			&row.ChatID,
			&row.ServiceName,
			&row.RoomName,
			&row.DisplayName,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}

		if row.RowID > maxRowID {
			maxRowID = row.RowID
		}

		// Skip self-sent messages.
		if row.IsFromMe == 1 {
			continue
		}

		// Skip empty messages (attachments-only, reactions, etc.).
		if strings.TrimSpace(row.Text) == "" {
			continue
		}

		rows = append(rows, row)
	}

	if err := sqlRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message rows: %w", err)
	}

	// Advance the high-water mark.
	if maxRowID > 0 {
		c.mu.Lock()
		if maxRowID > c.lastRowID {
			c.lastRowID = maxRowID
		}
		c.mu.Unlock()
	}

	return rows, nil
}

func (c *IMessageConnector) handleIncomingMessage(row chatDBRow) {
	sender := row.HandleID
	chatID := row.ChatID
	isGroup := row.RoomName != ""

	if chatID != "" && !c.acceptsChannel(chatID) {
		return
	}

	text := strings.TrimSpace(row.Text)
	if text == "" {
		return
	}

	target := "dm:" + sender
	if isGroup {
		if row.DisplayName != "" {
			target = "group:" + row.DisplayName
		} else {
			target = "group:" + row.RoomName
		}
	}

	channel := chatID
	if channel == "" {
		channel = sender
	}

	c.publish(protocol.Event{
		Timestamp: appleTimestampToTime(row.Date),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "message",
		Direction: "in",
		User:      sender,
		Target:    target,
		Channel:   channel,
		Text:      text,
		Direct:    !isGroup,
	})
}

// sendViaAppleScript sends a message through Messages.app using osascript.
// The script tries the modern AppleScript syntax (account/participant, macOS
// Monterey+) first and falls back to the legacy syntax (service/buddy) if
// the modern form fails. This covers macOS 10.13 through Sequoia+.
func (c *IMessageConnector) sendViaAppleScript(ctx context.Context, recipient, text string) error {
	// Escape backslashes and quotes for AppleScript string literals.
	escapedText := strings.ReplaceAll(text, `\`, `\\`)
	escapedText = strings.ReplaceAll(escapedText, `"`, `\"`)

	escapedRecipient := strings.ReplaceAll(recipient, `\`, `\\`)
	escapedRecipient = strings.ReplaceAll(escapedRecipient, `"`, `\"`)

	// Modern syntax (macOS Monterey / Ventura / Sonoma / Sequoia):
	//   id of 1st account  →  account id  →  participant
	// Legacy fallback (macOS High Sierra – Big Sur):
	//   1st service  →  buddy
	script := fmt.Sprintf(`
		tell application "Messages"
			try
				set iMessageAccount to id of 1st account whose service type = iMessage
				set targetBuddy to participant "%s" of account id iMessageAccount
				send "%s" to targetBuddy
			on error
				set targetService to 1st service whose service type = iMessage
				set targetBuddy to buddy "%s" of targetService
				send "%s" to targetBuddy
			end try
		end tell
	`, escapedRecipient, escapedText, escapedRecipient, escapedText)

	cmd := exec.CommandContext(ctx, c.osascriptCmd, "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func (c *IMessageConnector) rememberChannel(channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels[channel] = struct{}{}
}

func (c *IMessageConnector) acceptsChannel(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.channels) == 0 {
		return true
	}

	_, ok := c.channels[channel]
	return ok
}

func (c *IMessageConnector) publishStatus(text string) {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "status",
		Direction: "system",
		Text:      text,
	})
}

func (c *IMessageConnector) publishHeartbeat() {
	c.publish(protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   c.serviceName,
		Bot:       c.botName,
		Kind:      "heartbeat",
		Direction: "system",
		Text:      "upstream session alive",
	})
}

func (c *IMessageConnector) sleepOrDone(ctx context.Context, wait time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// resolveIMessageChannel extracts a recipient (phone number or email) from
// the request's channel or target field. Supports prefixed forms like
// "dm:+1234567890" and "imessage:+1234567890".
func resolveIMessageChannel(request protocol.Request) string {
	if request.Channel != "" {
		return request.Channel
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"imessage:dm:", "imessage:chat:", "imessage:group:", "imessage:", "dm:", "chat:", "group:"} {
		if strings.HasPrefix(target, prefix) {
			return strings.TrimPrefix(target, prefix)
		}
	}

	return target
}

// appleTimestampToTime converts an Apple CoreData timestamp (nanoseconds
// since 2001-01-01 00:00:00 UTC) to a Go time.Time. macOS stores message
// dates in this format in chat.db.
func appleTimestampToTime(appleNanos int64) time.Time {
	if appleNanos == 0 {
		return time.Now().UTC()
	}
	// Apple epoch: 2001-01-01 00:00:00 UTC
	appleEpoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	// Modern macOS stores timestamps in nanoseconds. Older versions used
	// seconds — detect by magnitude. We convert to seconds + remainder to
	// avoid time.Duration overflow on large nanosecond values.
	//
	// Magnitude ranges (for dates in the 2020s, ~20-25 years after epoch):
	//   nanoseconds:  ~6e17 – 8e17
	//   microseconds: ~6e14 – 8e14
	//   milliseconds: ~6e11 – 8e11
	//   seconds:      ~6e8  – 8e8
	var sec, nsec int64
	if appleNanos > 1e15 {
		// Nanoseconds (modern macOS 10.13+)
		sec = appleNanos / 1e9
		nsec = appleNanos % 1e9
	} else if appleNanos > 1e12 {
		// Microseconds (some transitional macOS versions)
		sec = appleNanos / 1e6
		nsec = (appleNanos % 1e6) * 1e3
	} else if appleNanos > 1e9 {
		// Milliseconds
		sec = appleNanos / 1e3
		nsec = (appleNanos % 1e3) * 1e6
	} else {
		// Plain seconds (legacy)
		sec = appleNanos
	}
	return appleEpoch.Add(time.Duration(sec)*time.Second + time.Duration(nsec)).UTC()
}

// expandHome replaces a leading ~ with the user's home directory using
// os.UserHomeDir (no subprocess needed).
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return home + path[1:]
}

// parseIMessageRowID parses a string row ID to int64 for use in tests.
func parseIMessageRowID(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
