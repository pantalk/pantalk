package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pantalk/pantalk/internal/protocol"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func makeEvent(service, bot, text, direction string) protocol.Event {
	return protocol.Event{
		Timestamp: time.Now().UTC(),
		Service:   service,
		Bot:       bot,
		Kind:      "message",
		Direction: direction,
		Target:    "channel:C1",
		Channel:   "C1",
		Text:      text,
	}
}

func TestInsertAndListEvents(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot-a", "hello world", "in")
	id, err := s.InsertEvent(ev)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive event id, got %d", id)
	}

	events, err := s.ListEvents(EventFilter{Service: "slack", Bot: "bot-a", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Text != "hello world" {
		t.Fatalf("unexpected text: %q", events[0].Text)
	}
	if events[0].ID != id {
		t.Fatalf("expected id %d, got %d", id, events[0].ID)
	}
}

func TestListEvents_DefaultLimit(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 60; i++ {
		_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg", "in"))
	}

	events, err := s.ListEvents(EventFilter{Service: "slack"})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	// default limit is 50
	if len(events) != 50 {
		t.Fatalf("expected 50 events (default limit), got %d", len(events))
	}
}

func TestListEvents_Chronological(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg", "in"))
	}

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	// should be in ascending ID order (chronological)
	for i := 1; i < len(events); i++ {
		if events[i].ID <= events[i-1].ID {
			t.Fatalf("events not in chronological order: id %d <= %d", events[i].ID, events[i-1].ID)
		}
	}
}

func TestListEvents_SinceID(t *testing.T) {
	s := openTestStore(t)

	ids := make([]int64, 5)
	for i := 0; i < 5; i++ {
		id, _ := s.InsertEvent(makeEvent("slack", "bot", "msg", "in"))
		ids[i] = id
	}

	events, err := s.ListEvents(EventFilter{SinceID: ids[2], Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events after id %d, got %d", ids[2], len(events))
	}
	if events[0].ID != ids[3] {
		t.Fatalf("expected first event id %d, got %d", ids[3], events[0].ID)
	}
}

func TestListEvents_FilterByChannel(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "in c1", "in")
	ev1.Channel = "C1"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "in c2", "in")
	ev2.Channel = "C2"
	_, _ = s.InsertEvent(ev2)

	events, err := s.ListEvents(EventFilter{Channel: "C2", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for C2, got %d", len(events))
	}
	if events[0].Text != "in c2" {
		t.Fatalf("unexpected text: %q", events[0].Text)
	}
}

func TestListEvents_NotifyOnly(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "normal", "in")
	ev1.Notify = false
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "notify me", "in")
	ev2.Notify = true
	_, _ = s.InsertEvent(ev2)

	events, err := s.ListEvents(EventFilter{NotifyOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 notify event, got %d", len(events))
	}
	if events[0].Text != "notify me" {
		t.Fatalf("unexpected text: %q", events[0].Text)
	}
}

func TestInsertAndListNotifications(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot", "ping", "in")
	ev.Notify = true
	evID, _ := s.InsertEvent(ev)
	ev.ID = evID

	nID, err := s.InsertNotification(ev)
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	if nID <= 0 {
		t.Fatalf("expected positive notification id, got %d", nID)
	}

	notifications, err := s.ListNotifications(NotificationFilter{Service: "slack", Limit: 10})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Text != "ping" {
		t.Fatalf("unexpected text: %q", notifications[0].Text)
	}
	if notifications[0].Seen {
		t.Fatal("expected notification to be unseen")
	}
}

func TestListNotifications_UnseenFilter(t *testing.T) {
	s := openTestStore(t)

	// insert two notifications
	for _, text := range []string{"first", "second"} {
		ev := makeEvent("slack", "bot", text, "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	// mark the first one as seen
	_, _ = s.MarkSeenByID(1)

	unseen, err := s.ListNotifications(NotificationFilter{Unseen: true, Limit: 10})
	if err != nil {
		t.Fatalf("list unseen: %v", err)
	}
	if len(unseen) != 1 {
		t.Fatalf("expected 1 unseen notification, got %d", len(unseen))
	}
	if unseen[0].Text != "second" {
		t.Fatalf("unexpected unseen text: %q", unseen[0].Text)
	}
}

func TestMarkSeenByID(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot", "msg", "in")
	ev.Notify = true
	evID, _ := s.InsertEvent(ev)
	ev.ID = evID
	nID, _ := s.InsertNotification(ev)

	count, err := s.MarkSeenByID(nID)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row affected, got %d", count)
	}

	// marking again should affect 0
	count, err = s.MarkSeenByID(nID)
	if err != nil {
		t.Fatalf("mark seen again: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows on re-mark, got %d", count)
	}

	// verify seen state
	notifications, _ := s.ListNotifications(NotificationFilter{Limit: 10})
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if !notifications[0].Seen {
		t.Fatal("expected notification to be seen")
	}
	if notifications[0].SeenAt == nil {
		t.Fatal("expected seen_at timestamp")
	}
}

func TestMarkSeenByID_Zero(t *testing.T) {
	s := openTestStore(t)
	count, err := s.MarkSeenByID(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for id=0, got %d", count)
	}
}

func TestMarkSeen_ByBot(t *testing.T) {
	s := openTestStore(t)

	// two bots, one notification each
	for _, bot := range []string{"bot-a", "bot-b"} {
		ev := makeEvent("slack", bot, "msg from "+bot, "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{Bot: "bot-a", Unseen: true}, false)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 marked, got %d", count)
	}

	// bot-b should still be unseen
	unseen, _ := s.ListNotifications(NotificationFilter{Unseen: true, Limit: 10})
	if len(unseen) != 1 {
		t.Fatalf("expected 1 unseen, got %d", len(unseen))
	}
	if unseen[0].Text != "msg from bot-b" {
		t.Fatalf("wrong unseen notification: %q", unseen[0].Text)
	}
}

func TestMarkSeen_All(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{}, true)
	if err != nil {
		t.Fatalf("mark all seen: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 marked, got %d", count)
	}

	unseen, _ := s.ListNotifications(NotificationFilter{Unseen: true, Limit: 10})
	if len(unseen) != 0 {
		t.Fatalf("expected 0 unseen, got %d", len(unseen))
	}
}

func TestMarkSeen_NoFiltersNoAll(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot", "msg", "in")
	ev.Notify = true
	evID, _ := s.InsertEvent(ev)
	ev.ID = evID
	_, _ = s.InsertNotification(ev)

	// without all=true and no filters, should be a no-op
	count, err := s.MarkSeen(NotificationFilter{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 (safety guard), got %d", count)
	}
}

func TestCloseNilStore(t *testing.T) {
	var s *Store
	if err := s.Close(); err != nil {
		t.Fatalf("unexpected error closing nil store: %v", err)
	}
}

func TestListEvents_EmptyStore(t *testing.T) {
	s := openTestStore(t)
	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestListNotifications_EmptyStore(t *testing.T) {
	s := openTestStore(t)
	notifications, err := s.ListNotifications(NotificationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("expected 0 notifications, got %d", len(notifications))
	}
}
