package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/chatbotkit/pantalk/internal/protocol"
)

type NotificationFilter struct {
	Service string
	Bot     string
	Target  string
	Channel string
	Thread  string
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
	Limit      int
	SinceID    int64
	NotifyOnly bool
}

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
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
	target TEXT,
	channel TEXT,
	thread TEXT,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 0,
	text TEXT NOT NULL
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
	target TEXT,
	channel TEXT,
	thread TEXT,
	text TEXT NOT NULL,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 1,
	seen INTEGER NOT NULL DEFAULT 0,
	seen_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_notifications_scope ON notifications(service, bot, id);
CREATE INDEX IF NOT EXISTS idx_notifications_seen ON notifications(service, bot, seen, id);
`)
	if err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}

	return nil
}

func (s *Store) InsertEvent(event protocol.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
INSERT INTO events (
	timestamp_utc, service, bot, kind, direction,
	target, channel, thread,
	mentions_agent, direct_to_agent, notify, text
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
		event.Target,
		event.Channel,
		event.Thread,
		boolToInt(event.Mentions),
		boolToInt(event.Direct),
		boolToInt(event.Notify),
		event.Text,
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
	target,
	channel,
	thread,
	mentions_agent,
	direct_to_agent,
	notify,
	text
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
	event_id, timestamp_utc, service, bot, kind, direction,
	target, channel, thread, text,
	mentions_agent, direct_to_agent, notify, seen
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
`,
		event.ID,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
		event.Target,
		event.Channel,
		event.Thread,
		event.Text,
		boolToInt(event.Mentions),
		boolToInt(event.Direct),
		boolToInt(event.Notify),
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
	target,
	channel,
	thread,
	text,
	mentions_agent,
	direct_to_agent,
	notify,
	seen,
	seen_at
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

func scanEvent(rows *sql.Rows) (protocol.Event, error) {
	var (
		eventID        int64
		notificationID int64
		timestampRaw   string
		service        string
		bot            string
		kind           string
		direction      string
		target         sql.NullString
		channel        sql.NullString
		thread         sql.NullString
		text           string
		mentions       int
		direct         int
		notify         int
		seen           int
		seenAtRaw      sql.NullString
	)

	if err := rows.Scan(
		&notificationID,
		&eventID,
		&timestampRaw,
		&service,
		&bot,
		&kind,
		&direction,
		&target,
		&channel,
		&thread,
		&text,
		&mentions,
		&direct,
		&notify,
		&seen,
		&seenAtRaw,
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
		Target:         target.String,
		Channel:        channel.String,
		Thread:         thread.String,
		NotificationID: notificationID,
		Seen:           seen == 1,
		SeenAt:         seenAt,
		Mentions:       mentions == 1,
		Direct:         direct == 1,
		Notify:         notify == 1,
		Text:           text,
	}, nil
}

func scanStoredEvent(rows *sql.Rows) (protocol.Event, error) {
	var (
		eventID      int64
		timestampRaw string
		service      string
		bot          string
		kind         string
		direction    string
		target       sql.NullString
		channel      sql.NullString
		thread       sql.NullString
		mentions     int
		direct       int
		notify       int
		text         string
	)

	if err := rows.Scan(
		&eventID,
		&timestampRaw,
		&service,
		&bot,
		&kind,
		&direction,
		&target,
		&channel,
		&thread,
		&mentions,
		&direct,
		&notify,
		&text,
	); err != nil {
		return protocol.Event{}, fmt.Errorf("scan event row: %w", err)
	}

	timestamp, err := time.Parse(time.RFC3339Nano, timestampRaw)
	if err != nil {
		return protocol.Event{}, fmt.Errorf("parse event timestamp: %w", err)
	}

	return protocol.Event{
		ID:        eventID,
		Timestamp: timestamp,
		Service:   service,
		Bot:       bot,
		Kind:      kind,
		Direction: direction,
		Target:    target.String,
		Channel:   channel.String,
		Thread:    thread.String,
		Mentions:  mentions == 1,
		Direct:    direct == 1,
		Notify:    notify == 1,
		Text:      text,
	}, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
