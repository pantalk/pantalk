package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pantalk/pantalk/internal/protocol"
)

type NotificationFilter struct {
	Service string
	Bot     string
	Target  string
	Channel string
	Thread  string
	Search  string
	Limit   int
	SinceID int64
	Unseen  bool
}

type EventFilter struct {
	Service    string
	Bot        string
	Target     string
	Channel    string
	Thread     string
	Search     string
	Limit      int
	SinceID    int64
	NotifyOnly bool
}

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

type NotificationStats struct {
	Total  int64
	Unseen int64
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp_utc TEXT NOT NULL,
	service TEXT NOT NULL,
	bot TEXT NOT NULL,
	kind TEXT NOT NULL,
	direction TEXT NOT NULL,
	user TEXT NOT NULL DEFAULT '',
	target TEXT,
	channel TEXT,
	channel_name TEXT,
	thread TEXT,
	schedule TEXT,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 0,
	text TEXT NOT NULL,
	attachments TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_events_scope ON events(service, bot, id);
CREATE INDEX IF NOT EXISTS idx_events_notify ON events(service, bot, notify, id);

CREATE TABLE IF NOT EXISTS notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id INTEGER NOT NULL,
	timestamp_utc TEXT NOT NULL,
	service TEXT NOT NULL,
	bot TEXT NOT NULL,
	kind TEXT NOT NULL,
	direction TEXT NOT NULL,
	user TEXT NOT NULL DEFAULT '',
	target TEXT,
	channel TEXT,
	channel_name TEXT,
	thread TEXT,
	schedule TEXT,
	text TEXT NOT NULL,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 1,
	seen INTEGER NOT NULL DEFAULT 0,
	seen_at TEXT,
	attachments TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_notifications_scope ON notifications(service, bot, id);
CREATE INDEX IF NOT EXISTS idx_notifications_seen ON notifications(service, bot, seen, id);

CREATE TABLE IF NOT EXISTS agent_sessions (
	agent TEXT NOT NULL,
	conversation_key TEXT NOT NULL,
	thread_id TEXT NOT NULL,
	updated_at_utc TEXT NOT NULL,
	PRIMARY KEY (agent, conversation_key)
);
`)
	if err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}

	return s.migrateSchema()
}

// AgentSession returns the persisted opaque runtime session for an agent
// conversation. The boolean is false before that conversation has started.
func (s *Store) AgentSession(agent string, conversationKey string) (string, bool, error) {
	var sessionID string
	err := s.db.QueryRow(
		`SELECT thread_id FROM agent_sessions WHERE agent = ? AND conversation_key = ?`,
		agent,
		conversationKey,
	).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read agent session: %w", err)
	}

	return sessionID, true, nil
}

// SaveAgentSession creates or replaces the persisted runtime session for an
// agent conversation. The historical thread_id column remains unchanged for
// database compatibility.
func (s *Store) SaveAgentSession(agent string, conversationKey string, sessionID string) error {
	agent = strings.TrimSpace(agent)
	conversationKey = strings.TrimSpace(conversationKey)
	sessionID = strings.TrimSpace(sessionID)
	if agent == "" || conversationKey == "" || sessionID == "" {
		return fmt.Errorf("agent, conversation key, and session id are required")
	}

	_, err := s.db.Exec(`
INSERT INTO agent_sessions (agent, conversation_key, thread_id, updated_at_utc)
VALUES (?, ?, ?, ?)
ON CONFLICT(agent, conversation_key) DO UPDATE SET
	thread_id = excluded.thread_id,
	updated_at_utc = excluded.updated_at_utc
`, agent, conversationKey, sessionID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save agent session: %w", err)
	}

	return nil
}

// migrateSchema applies additive column migrations to databases created by
// earlier versions. CREATE TABLE IF NOT EXISTS leaves an existing table
// untouched, so a column added to the DDL above never reaches a database that
// already exists - it has to be added explicitly here.
func (s *Store) migrateSchema() error {
	migrations := []struct {
		table  string
		column string
		ddl    string
	}{
		{"events", "attachments", `ALTER TABLE events ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]'`},
		{"notifications", "attachments", `ALTER TABLE notifications ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]'`},
		{"events", "channel_name", `ALTER TABLE events ADD COLUMN channel_name TEXT`},
		{"notifications", "channel_name", `ALTER TABLE notifications ADD COLUMN channel_name TEXT`},
		{"events", "schedule", `ALTER TABLE events ADD COLUMN schedule TEXT`},
		{"notifications", "schedule", `ALTER TABLE notifications ADD COLUMN schedule TEXT`},
	}

	for _, migration := range migrations {
		exists, err := s.hasColumn(migration.table, migration.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		if _, err := s.db.Exec(migration.ddl); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", migration.table, migration.column, err)
		}
	}

	return nil
}

func (s *Store) hasColumn(table string, column string) (bool, error) {
	rows, err := s.db.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}

	return found, nil
}

// encodeAttachments serializes attachments for storage. It always produces
// valid JSON so the column can be decoded unconditionally on read.
//
// Path is stripped before writing: it is a rendering of Key under whichever
// backend and storage root were configured at the time, so persisting it would
// leave every historical row pointing at a location that a later config change
// invalidates. Key is the durable locator; Path is recomputed on read.
func encodeAttachments(attachments []protocol.Attachment) string {
	if len(attachments) == 0 {
		return "[]"
	}

	persisted := make([]protocol.Attachment, len(attachments))
	for i, attachment := range attachments {
		attachment.Path = ""
		persisted[i] = attachment
	}

	encoded, err := json.Marshal(persisted)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}

// decodeAttachments parses the attachments column. Malformed or legacy values
// degrade to no attachments rather than failing the whole query - a damaged
// metadata blob should not make message history unreadable.
func decodeAttachments(raw sql.NullString) []protocol.Attachment {
	value := strings.TrimSpace(raw.String)
	if !raw.Valid || value == "" || value == "[]" {
		return nil
	}

	var attachments []protocol.Attachment
	if err := json.Unmarshal([]byte(value), &attachments); err != nil {
		return nil
	}

	return attachments
}

// ReferencedAttachmentKeys returns every attachment storage key still reachable
// from stored history, across both events and notifications.
//
// Content addressing means one key can back many rows, so this is a set union
// rather than a per-row list: a key survives as long as any single row still
// mentions it. Rows with no attachments are skipped in SQL so the scan stays
// proportional to attachment-bearing history rather than all history.
func (s *Store) ReferencedAttachmentKeys() (map[string]struct{}, error) {
	referenced := make(map[string]struct{})

	for _, table := range []string{"events", "notifications"} {
		// #nosec G202 - table is from a fixed local list, never user input.
		rows, err := s.db.Query(`SELECT attachments FROM ` + table + ` WHERE attachments IS NOT NULL AND attachments != '[]' AND attachments != ''`)
		if err != nil {
			return nil, fmt.Errorf("scan %s attachments: %w", table, err)
		}

		for rows.Next() {
			var raw sql.NullString
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan %s attachment row: %w", table, err)
			}

			for _, attachment := range decodeAttachments(raw) {
				if key := strings.TrimSpace(attachment.Key); key != "" {
					referenced[key] = struct{}{}
				}
			}
		}

		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan %s attachments: %w", table, err)
		}
		rows.Close()
	}

	return referenced, nil
}

// LookupChannelByThread returns the channel associated with a thread timestamp.
// It searches the events table for any event matching the given thread value
// and returns the first channel found.
func (s *Store) LookupChannelByThread(service string, bot string, thread string) (string, error) {
	query := `SELECT channel FROM events WHERE thread = ? AND channel != ''`
	args := []any{thread}

	if service != "" {
		query += " AND service = ?"
		args = append(args, service)
	}
	if bot != "" {
		query += " AND bot = ?"
		args = append(args, bot)
	}

	query += " LIMIT 1"

	var channel string
	err := s.db.QueryRow(query, args...).Scan(&channel)
	if err != nil {
		return "", err
	}
	return channel, nil
}

func (s *Store) InsertEvent(event protocol.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
INSERT INTO events (
	timestamp_utc, service, bot, kind, direction, user,
	target, channel, channel_name, thread, schedule,
	mentions_agent, direct_to_agent, notify, text, attachments
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
		event.User,
		event.Target,
		event.Channel,
		event.ChannelName,
		event.Thread,
		event.Schedule,
		boolToInt(event.Mentions),
		boolToInt(event.Direct),
		boolToInt(event.Notify),
		event.Text,
		encodeAttachments(event.Attachments),
	)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted event id: %w", err)
	}

	return id, nil
}

func (s *Store) ListEvents(filter EventFilter) ([]protocol.Event, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}

	query := `
SELECT
	id,
	timestamp_utc,
	service,
	bot,
	kind,
	direction,
	user,
	target,
	channel,
	channel_name,
	thread,
	schedule,
	mentions_agent,
	direct_to_agent,
	notify,
	text,
	attachments
FROM events`

	where := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if filter.Service != "" {
		where = append(where, "service = ?")
		args = append(args, filter.Service)
	}
	if filter.Bot != "" {
		where = append(where, "bot = ?")
		args = append(args, filter.Bot)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, filter.Channel)
	}
	if filter.Thread != "" {
		where = append(where, "thread = ?")
		args = append(args, filter.Thread)
	}
	if filter.SinceID > 0 {
		where = append(where, "id > ?")
		args = append(args, filter.SinceID)
	}
	if filter.NotifyOnly {
		where = append(where, "notify = 1")
	}
	if filter.Search != "" {
		where = append(where, "text LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	events := make([]protocol.Event, 0, filter.Limit)
	for rows.Next() {
		event, err := scanStoredEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}

	return events, nil
}

func (s *Store) InsertNotification(event protocol.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
INSERT INTO notifications (
	event_id, timestamp_utc, service, bot, kind, direction, user,
	target, channel, channel_name, thread, schedule, text,
	mentions_agent, direct_to_agent, notify, seen, attachments
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
`,
		event.ID,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
		event.User,
		event.Target,
		event.Channel,
		event.ChannelName,
		event.Thread,
		event.Schedule,
		event.Text,
		boolToInt(event.Mentions),
		boolToInt(event.Direct),
		boolToInt(event.Notify),
		encodeAttachments(event.Attachments),
	)
	if err != nil {
		return 0, fmt.Errorf("insert notification: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted notification id: %w", err)
	}

	return id, nil
}

func (s *Store) ListNotifications(filter NotificationFilter) ([]protocol.Event, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}

	query := `
SELECT
	id,
	event_id,
	timestamp_utc,
	service,
	bot,
	kind,
	direction,
	user,
	target,
	channel,
	channel_name,
	thread,
	schedule,
	text,
	mentions_agent,
	direct_to_agent,
	notify,
	seen,
	seen_at,
	attachments
FROM notifications`

	where := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if filter.Service != "" {
		where = append(where, "service = ?")
		args = append(args, filter.Service)
	}
	if filter.Bot != "" {
		where = append(where, "bot = ?")
		args = append(args, filter.Bot)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, filter.Channel)
	}
	if filter.Thread != "" {
		where = append(where, "thread = ?")
		args = append(args, filter.Thread)
	}
	if filter.SinceID > 0 {
		where = append(where, "id > ?")
		args = append(args, filter.SinceID)
	}
	if filter.Unseen {
		where = append(where, "seen = 0")
	}
	if filter.Search != "" {
		where = append(where, "text LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	events := make([]protocol.Event, 0, filter.Limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}

	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}

	return events, nil
}

func (s *Store) MarkSeenByID(id int64) (int64, error) {
	if id <= 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
UPDATE notifications
SET seen = 1, seen_at = ?
WHERE id = ? AND seen = 0
`, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return 0, fmt.Errorf("mark notification seen by id: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read affected rows: %w", err)
	}

	return count, nil
}

func (s *Store) MarkSeen(filter NotificationFilter, all bool) (int64, error) {
	where := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if filter.Service != "" {
		where = append(where, "service = ?")
		args = append(args, filter.Service)
	}
	if filter.Bot != "" {
		where = append(where, "bot = ?")
		args = append(args, filter.Bot)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, filter.Channel)
	}
	if filter.Thread != "" {
		where = append(where, "thread = ?")
		args = append(args, filter.Thread)
	}
	if filter.Unseen {
		where = append(where, "seen = 0")
	}

	if !all && len(where) == 0 {
		return 0, nil
	}

	query := "UPDATE notifications SET seen = 1, seen_at = ?"
	args = append([]any{time.Now().UTC().Format(time.RFC3339Nano)}, args...)

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("mark notifications seen: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read affected rows: %w", err)
	}

	return count, nil
}

func (s *Store) DeleteEvents(filter EventFilter, all bool) (int64, error) {
	where := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if filter.Service != "" {
		where = append(where, "service = ?")
		args = append(args, filter.Service)
	}
	if filter.Bot != "" {
		where = append(where, "bot = ?")
		args = append(args, filter.Bot)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, filter.Channel)
	}
	if filter.Thread != "" {
		where = append(where, "thread = ?")
		args = append(args, filter.Thread)
	}
	if filter.Search != "" {
		where = append(where, "text LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}

	if !all && len(where) == 0 {
		return 0, nil
	}

	query := "DELETE FROM events"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete events: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read affected rows: %w", err)
	}

	return count, nil
}

func (s *Store) DeleteNotifications(filter NotificationFilter, all bool) (int64, error) {
	where := make([]string, 0, 8)
	args := make([]any, 0, 8)

	if filter.Service != "" {
		where = append(where, "service = ?")
		args = append(args, filter.Service)
	}
	if filter.Bot != "" {
		where = append(where, "bot = ?")
		args = append(args, filter.Bot)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, filter.Channel)
	}
	if filter.Thread != "" {
		where = append(where, "thread = ?")
		args = append(args, filter.Thread)
	}
	if filter.Unseen {
		where = append(where, "seen = 0")
	}
	if filter.Search != "" {
		where = append(where, "text LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}

	if !all && len(where) == 0 {
		return 0, nil
	}

	query := "DELETE FROM notifications"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete notifications: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read affected rows: %w", err)
	}

	return count, nil
}

func (s *Store) NotificationStats() (NotificationStats, error) {
	row := s.db.QueryRow(`
SELECT
	COUNT(*) AS total,
	SUM(CASE WHEN seen = 0 THEN 1 ELSE 0 END) AS unseen
FROM notifications
`)

	var stats NotificationStats
	var unseen sql.NullInt64
	if err := row.Scan(&stats.Total, &unseen); err != nil {
		return NotificationStats{}, fmt.Errorf("notification stats: %w", err)
	}
	if unseen.Valid {
		stats.Unseen = unseen.Int64
	}
	return stats, nil
}

func scanEvent(rows *sql.Rows) (protocol.Event, error) {
	var (
		eventID        int64
		notificationID int64
		timestampRaw   string
		service        string
		bot            string
		kind           string
		direction      string
		user           string
		target         sql.NullString
		channel        sql.NullString
		channelName    sql.NullString
		thread         sql.NullString
		schedule       sql.NullString
		text           string
		mentions       int
		direct         int
		notify         int
		seen           int
		seenAtRaw      sql.NullString
		attachmentsRaw sql.NullString
	)

	if err := rows.Scan(
		&notificationID,
		&eventID,
		&timestampRaw,
		&service,
		&bot,
		&kind,
		&direction,
		&user,
		&target,
		&channel,
		&channelName,
		&thread,
		&schedule,
		&text,
		&mentions,
		&direct,
		&notify,
		&seen,
		&seenAtRaw,
		&attachmentsRaw,
	); err != nil {
		return protocol.Event{}, fmt.Errorf("scan notification row: %w", err)
	}

	timestamp, err := time.Parse(time.RFC3339Nano, timestampRaw)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("parse notification timestamp: %w", err)
	}

	var seenAt *time.Time
	if seenAtRaw.Valid {
		parsedSeenAt, parseErr := time.Parse(time.RFC3339Nano, seenAtRaw.String)
		if parseErr == nil {
			seenAt = &parsedSeenAt
		}
	}

	return protocol.Event{
		ID:             eventID,
		Timestamp:      timestamp,
		Service:        service,
		Bot:            bot,
		Kind:           kind,
		Direction:      direction,
		User:           user,
		Target:         target.String,
		Channel:        channel.String,
		ChannelName:    channelName.String,
		Thread:         thread.String,
		Schedule:       schedule.String,
		NotificationID: notificationID,
		Seen:           seen == 1,
		SeenAt:         seenAt,
		Mentions:       mentions == 1,
		Direct:         direct == 1,
		Notify:         notify == 1,
		Text:           text,
		Attachments:    decodeAttachments(attachmentsRaw),
	}, nil
}

func scanStoredEvent(rows *sql.Rows) (protocol.Event, error) {
	var (
		eventID        int64
		timestampRaw   string
		service        string
		bot            string
		kind           string
		direction      string
		user           string
		target         sql.NullString
		channel        sql.NullString
		channelName    sql.NullString
		thread         sql.NullString
		schedule       sql.NullString
		mentions       int
		direct         int
		notify         int
		text           string
		attachmentsRaw sql.NullString
	)

	if err := rows.Scan(
		&eventID,
		&timestampRaw,
		&service,
		&bot,
		&kind,
		&direction,
		&user,
		&target,
		&channel,
		&channelName,
		&thread,
		&schedule,
		&mentions,
		&direct,
		&notify,
		&text,
		&attachmentsRaw,
	); err != nil {
		return protocol.Event{}, fmt.Errorf("scan event row: %w", err)
	}

	timestamp, err := time.Parse(time.RFC3339Nano, timestampRaw)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("parse event timestamp: %w", err)
	}

	return protocol.Event{
		ID:          eventID,
		Timestamp:   timestamp,
		Service:     service,
		Bot:         bot,
		Kind:        kind,
		Direction:   direction,
		User:        user,
		Target:      target.String,
		Channel:     channel.String,
		ChannelName: channelName.String,
		Thread:      thread.String,
		Schedule:    schedule.String,
		Mentions:    mentions == 1,
		Direct:      direct == 1,
		Notify:      notify == 1,
		Text:        text,
		Attachments: decodeAttachments(attachmentsRaw),
	}, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
