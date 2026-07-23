package store

import (
	"database/sql"
	"path/filepath"
	"strings"
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

// --- LookupChannelByThread tests ---

func TestLookupChannelByThread(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot", "in thread", "in")
	ev.Thread = "T123"
	ev.Channel = "C-general"
	_, _ = s.InsertEvent(ev)

	channel, err := s.LookupChannelByThread("slack", "bot", "T123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channel != "C-general" {
		t.Fatalf("expected channel C-general, got %q", channel)
	}
}

func TestLookupChannelByThread_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.LookupChannelByThread("slack", "bot", "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing thread")
	}
}

func TestLookupChannelByThread_NoServiceFilter(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("discord", "bot", "msg", "in")
	ev.Thread = "T999"
	ev.Channel = "C-discord"
	_, _ = s.InsertEvent(ev)

	// Empty service/bot should still find it
	channel, err := s.LookupChannelByThread("", "", "T999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channel != "C-discord" {
		t.Fatalf("expected C-discord, got %q", channel)
	}
}

func TestLookupChannelByThread_ServiceFilter(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "from slack", "in")
	ev1.Thread = "T100"
	ev1.Channel = "C-slack"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("discord", "bot", "from discord", "in")
	ev2.Thread = "T100"
	ev2.Channel = "C-discord"
	_, _ = s.InsertEvent(ev2)

	// Filter by service=slack
	channel, err := s.LookupChannelByThread("slack", "", "T100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channel != "C-slack" {
		t.Fatalf("expected C-slack, got %q", channel)
	}
}

// --- DeleteEvents tests ---

func TestDeleteEvents_ByService(t *testing.T) {
	s := openTestStore(t)

	_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg1", "in"))
	_, _ = s.InsertEvent(makeEvent("discord", "bot", "msg2", "in"))
	_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg3", "in"))

	count, err := s.DeleteEvents(EventFilter{Service: "slack"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}

	remaining, _ := s.ListEvents(EventFilter{Limit: 10})
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Service != "discord" {
		t.Fatalf("expected discord event, got %q", remaining[0].Service)
	}
}

func TestDeleteEvents_All(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg", "in"))
	}

	count, err := s.DeleteEvents(EventFilter{}, true)
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 deleted, got %d", count)
	}
}

func TestDeleteEvents_NoFiltersNoAll(t *testing.T) {
	s := openTestStore(t)
	_, _ = s.InsertEvent(makeEvent("slack", "bot", "msg", "in"))

	count, err := s.DeleteEvents(EventFilter{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 (safety guard), got %d", count)
	}
}

func TestDeleteEvents_ByBot(t *testing.T) {
	s := openTestStore(t)

	_, _ = s.InsertEvent(makeEvent("slack", "bot-a", "msg1", "in"))
	_, _ = s.InsertEvent(makeEvent("slack", "bot-b", "msg2", "in"))

	count, err := s.DeleteEvents(EventFilter{Bot: "bot-a"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteEvents_ByChannel(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "msg1", "in")
	ev1.Channel = "C1"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "msg2", "in")
	ev2.Channel = "C2"
	_, _ = s.InsertEvent(ev2)

	count, err := s.DeleteEvents(EventFilter{Channel: "C1"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteEvents_ByThread(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "msg", "in")
	ev1.Thread = "T1"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "msg", "in")
	ev2.Thread = "T2"
	_, _ = s.InsertEvent(ev2)

	count, err := s.DeleteEvents(EventFilter{Thread: "T1"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteEvents_BySearch(t *testing.T) {
	s := openTestStore(t)

	_, _ = s.InsertEvent(makeEvent("slack", "bot", "hello world", "in"))
	_, _ = s.InsertEvent(makeEvent("slack", "bot", "goodbye world", "in"))
	_, _ = s.InsertEvent(makeEvent("slack", "bot", "nothing here", "in"))

	count, err := s.DeleteEvents(EventFilter{Search: "world"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}
}

func TestDeleteEvents_ByTarget(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "msg", "in")
	ev1.Target = "target-a"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "msg", "in")
	ev2.Target = "target-b"
	_, _ = s.InsertEvent(ev2)

	count, err := s.DeleteEvents(EventFilter{Target: "target-a"}, false)
	if err != nil {
		t.Fatalf("delete events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

// --- DeleteNotifications tests ---

func TestDeleteNotifications_ByService(t *testing.T) {
	s := openTestStore(t)

	for _, svc := range []string{"slack", "discord", "slack"} {
		ev := makeEvent(svc, "bot", "msg from "+svc, "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Service: "slack"}, false)
	if err != nil {
		t.Fatalf("delete notifications: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}
}

func TestDeleteNotifications_All(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 3; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{}, true)
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 deleted, got %d", count)
	}
}

func TestDeleteNotifications_NoFiltersNoAll(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("slack", "bot", "msg", "in")
	ev.Notify = true
	evID, _ := s.InsertEvent(ev)
	ev.ID = evID
	_, _ = s.InsertNotification(ev)

	count, err := s.DeleteNotifications(NotificationFilter{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 (safety guard), got %d", count)
	}
}

func TestDeleteNotifications_ByBot(t *testing.T) {
	s := openTestStore(t)

	for _, bot := range []string{"bot-a", "bot-b"} {
		ev := makeEvent("slack", bot, "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Bot: "bot-a"}, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteNotifications_ByChannel(t *testing.T) {
	s := openTestStore(t)

	for _, ch := range []string{"C1", "C2"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Channel = ch
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Channel: "C1"}, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteNotifications_ByThread(t *testing.T) {
	s := openTestStore(t)

	for _, th := range []string{"T1", "T2"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Thread = th
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Thread: "T1"}, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

func TestDeleteNotifications_UnseenOnly(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 3; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	// Mark first as seen
	_, _ = s.MarkSeenByID(1)

	count, err := s.DeleteNotifications(NotificationFilter{Unseen: true}, false)
	if err != nil {
		t.Fatalf("delete unseen: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}
}

func TestDeleteNotifications_BySearch(t *testing.T) {
	s := openTestStore(t)

	for _, text := range []string{"hello world", "goodbye world", "nothing"} {
		ev := makeEvent("slack", "bot", text, "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Search: "world"}, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}
}

func TestDeleteNotifications_ByTarget(t *testing.T) {
	s := openTestStore(t)

	for _, tgt := range []string{"target-a", "target-b"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Target = tgt
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.DeleteNotifications(NotificationFilter{Target: "target-a"}, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 deleted, got %d", count)
	}
}

// --- Additional ListEvents filter tests ---

func TestListEvents_SearchFilter(t *testing.T) {
	s := openTestStore(t)

	_, _ = s.InsertEvent(makeEvent("slack", "bot", "hello world", "in"))
	_, _ = s.InsertEvent(makeEvent("slack", "bot", "goodbye universe", "in"))

	events, err := s.ListEvents(EventFilter{Search: "hello", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Text != "hello world" {
		t.Fatalf("unexpected text: %q", events[0].Text)
	}
}

func TestListEvents_TargetFilter(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "msg1", "in")
	ev1.Target = "target-a"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "msg2", "in")
	ev2.Target = "target-b"
	_, _ = s.InsertEvent(ev2)

	events, err := s.ListEvents(EventFilter{Target: "target-b", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1, got %d", len(events))
	}
}

func TestListEvents_ThreadFilter(t *testing.T) {
	s := openTestStore(t)

	ev1 := makeEvent("slack", "bot", "msg", "in")
	ev1.Thread = "T100"
	_, _ = s.InsertEvent(ev1)

	ev2 := makeEvent("slack", "bot", "msg", "in")
	ev2.Thread = "T200"
	_, _ = s.InsertEvent(ev2)

	events, err := s.ListEvents(EventFilter{Thread: "T100", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1, got %d", len(events))
	}
}

// --- Additional ListNotifications filter tests ---

func TestListNotifications_SearchFilter(t *testing.T) {
	s := openTestStore(t)

	for _, text := range []string{"deploy done", "test passed", "deploy failed"} {
		ev := makeEvent("slack", "bot", text, "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{Search: "deploy", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 2 {
		t.Fatalf("expected 2, got %d", len(notifs))
	}
}

func TestListNotifications_TargetFilter(t *testing.T) {
	s := openTestStore(t)

	for _, tgt := range []string{"target-x", "target-y"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Target = tgt
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{Target: "target-x", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
}

func TestListNotifications_ThreadFilter(t *testing.T) {
	s := openTestStore(t)

	for _, th := range []string{"T1", "T2"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Thread = th
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{Thread: "T1", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
}

func TestListNotifications_ChannelFilter(t *testing.T) {
	s := openTestStore(t)

	for _, ch := range []string{"C1", "C2", "C1"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Channel = ch
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{Channel: "C1", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 2 {
		t.Fatalf("expected 2, got %d", len(notifs))
	}
}

func TestListNotifications_SinceID(t *testing.T) {
	s := openTestStore(t)

	nIDs := make([]int64, 3)
	for i := 0; i < 3; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		nID, _ := s.InsertNotification(ev)
		nIDs[i] = nID
	}

	notifs, err := s.ListNotifications(NotificationFilter{SinceID: nIDs[1], Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification after id %d, got %d", nIDs[1], len(notifs))
	}
}

func TestListNotifications_Chronological(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for i := 1; i < len(notifs); i++ {
		if notifs[i].NotificationID <= notifs[i-1].NotificationID {
			t.Fatalf("not in chronological order: %d <= %d", notifs[i].NotificationID, notifs[i-1].NotificationID)
		}
	}
}

func TestListNotifications_DefaultLimit(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 60; i++ {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	notifs, err := s.ListNotifications(NotificationFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(notifs) != 50 {
		t.Fatalf("expected 50 (default limit), got %d", len(notifs))
	}
}

// --- Additional MarkSeen filter tests ---

func TestMarkSeen_ByChannel(t *testing.T) {
	s := openTestStore(t)

	for _, ch := range []string{"C1", "C2"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Channel = ch
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{Channel: "C1", Unseen: true}, false)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 marked, got %d", count)
	}
}

func TestMarkSeen_ByTarget(t *testing.T) {
	s := openTestStore(t)

	for _, tgt := range []string{"A", "B"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Target = tgt
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{Target: "A"}, false)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 marked, got %d", count)
	}
}

func TestMarkSeen_ByThread(t *testing.T) {
	s := openTestStore(t)

	for _, th := range []string{"T1", "T2"} {
		ev := makeEvent("slack", "bot", "msg", "in")
		ev.Thread = th
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{Thread: "T1"}, false)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 marked, got %d", count)
	}
}

func TestMarkSeen_ByService(t *testing.T) {
	s := openTestStore(t)

	for _, svc := range []string{"slack", "discord"} {
		ev := makeEvent(svc, "bot", "msg", "in")
		ev.Notify = true
		evID, _ := s.InsertEvent(ev)
		ev.ID = evID
		_, _ = s.InsertNotification(ev)
	}

	count, err := s.MarkSeen(NotificationFilter{Service: "slack"}, false)
	if err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 marked, got %d", count)
	}
}

// --- Event field preservation tests ---

func TestInsertEvent_AllFieldsPreserved(t *testing.T) {
	s := openTestStore(t)

	ev := protocol.Event{
		Timestamp: time.Date(2026, 2, 19, 9, 0, 0, 0, time.UTC),
		Service:   "slack",
		Bot:       "ops-bot",
		Kind:      "message",
		Direction: "in",
		User:      "U123",
		Target:    "channel:C1",
		Channel:   "C1",
		Thread:    "T456",
		Mentions:  true,
		Direct:    true,
		Notify:    true,
		Text:      "hello @bot",
	}

	id, err := s.InsertEvent(ev)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	events, _ := s.ListEvents(EventFilter{Limit: 1})
	if len(events) != 1 {
		t.Fatalf("expected 1 event")
	}

	got := events[0]
	if got.ID != id {
		t.Errorf("ID: %d != %d", got.ID, id)
	}
	if got.Service != "slack" {
		t.Errorf("Service: %q", got.Service)
	}
	if got.Bot != "ops-bot" {
		t.Errorf("Bot: %q", got.Bot)
	}
	if got.User != "U123" {
		t.Errorf("User: %q", got.User)
	}
	if got.Target != "channel:C1" {
		t.Errorf("Target: %q", got.Target)
	}
	if got.Thread != "T456" {
		t.Errorf("Thread: %q", got.Thread)
	}
	if !got.Mentions {
		t.Error("Mentions should be true")
	}
	if !got.Direct {
		t.Error("Direct should be true")
	}
	if !got.Notify {
		t.Error("Notify should be true")
	}
}

// --- Open/Close edge cases ---

func TestOpen_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
}

func TestOpen_InMemory(t *testing.T) {
	// ":memory:" has dir "." - should work
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	defer s.Close()

	_, err = s.InsertEvent(makeEvent("slack", "bot", "test", "in"))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestNotificationStats(t *testing.T) {
	s := openTestStore(t)

	inbound := makeEvent("slack", "bot-a", "first", "in")
	inbound.Notify = true
	inboundID, err := s.InsertEvent(inbound)
	if err != nil {
		t.Fatalf("insert inbound event: %v", err)
	}
	inbound.ID = inboundID
	firstNotificationID, err := s.InsertNotification(inbound)
	if err != nil {
		t.Fatalf("insert inbound notification: %v", err)
	}

	outbound := makeEvent("slack", "bot-a", "second", "out")
	outbound.Notify = true
	outboundID, err := s.InsertEvent(outbound)
	if err != nil {
		t.Fatalf("insert outbound event: %v", err)
	}
	outbound.ID = outboundID
	if _, err := s.InsertNotification(outbound); err != nil {
		t.Fatalf("insert outbound notification: %v", err)
	}

	if _, err := s.MarkSeenByID(firstNotificationID); err != nil {
		t.Fatalf("mark seen by id: %v", err)
	}

	stats, err := s.NotificationStats()
	if err != nil {
		t.Fatalf("notification stats: %v", err)
	}
	if stats.Total != 2 {
		t.Fatalf("expected total=2, got %d", stats.Total)
	}
	if stats.Unseen != 1 {
		t.Fatalf("expected unseen=1, got %d", stats.Unseen)
	}
}

func TestAttachmentsRoundTrip(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("telegram", "bot-a", "see attached", "in")
	ev.Attachments = []protocol.Attachment{
		{Name: "photo.jpg", MIME: "image/jpeg", Size: 2048, Digest: "abc123", Path: "/tmp/ab/abc123.jpg", RemoteID: "AgACAgQ"},
		{Name: "notes.pdf", MIME: "application/pdf", Size: 90},
	}

	if _, err := s.InsertEvent(ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	got := events[0].Attachments
	if len(got) != 2 {
		t.Fatalf("got %d attachments, want 2", len(got))
	}
	if got[0].Name != "photo.jpg" || got[0].Digest != "abc123" || got[0].Size != 2048 {
		t.Fatalf("first attachment round-tripped as %+v", got[0])
	}
	if got[0].RemoteID != "AgACAgQ" {
		t.Fatalf("remote id = %q, want %q", got[0].RemoteID, "AgACAgQ")
	}
	if got[1].Name != "notes.pdf" || got[1].MIME != "application/pdf" {
		t.Fatalf("second attachment round-tripped as %+v", got[1])
	}
}

func TestAttachmentsRoundTripThroughNotifications(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("telegram", "bot-a", "ping with file", "in")
	ev.Notify = true
	ev.Attachments = []protocol.Attachment{{Name: "voice.ogg", MIME: "audio/ogg", Size: 12}}

	if _, err := s.InsertNotification(ev); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	notifications, err := s.ListNotifications(NotificationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("got %d notifications, want 1", len(notifications))
	}
	if len(notifications[0].Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(notifications[0].Attachments))
	}
	if notifications[0].Attachments[0].Name != "voice.ogg" {
		t.Fatalf("attachment = %+v", notifications[0].Attachments[0])
	}
}

func TestEventWithoutAttachmentsStaysNil(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.InsertEvent(makeEvent("slack", "bot-a", "plain text", "in")); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events[0].Attachments) != 0 {
		t.Fatalf("expected no attachments, got %+v", events[0].Attachments)
	}
}

// A database created before attachments existed must gain the column on open,
// keep its existing rows readable, and accept new attachment-bearing writes.
func TestMigrationAddsAttachmentsToLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	if _, err := legacy.Exec(`
CREATE TABLE events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp_utc TEXT NOT NULL,
	service TEXT NOT NULL,
	bot TEXT NOT NULL,
	kind TEXT NOT NULL,
	direction TEXT NOT NULL,
	user TEXT NOT NULL DEFAULT '',
	target TEXT,
	channel TEXT,
	thread TEXT,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 0,
	text TEXT NOT NULL
);

CREATE TABLE notifications (
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
	thread TEXT,
	text TEXT NOT NULL,
	mentions_agent INTEGER NOT NULL DEFAULT 0,
	direct_to_agent INTEGER NOT NULL DEFAULT 0,
	notify INTEGER NOT NULL DEFAULT 1,
	seen INTEGER NOT NULL DEFAULT 0,
	seen_at TEXT
);
`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	if _, err := legacy.Exec(`
INSERT INTO events (timestamp_utc, service, bot, kind, direction, user, target, channel, thread, text)
VALUES (?, 'slack', 'bot-a', 'message', 'in', 'U1', 'channel:C1', 'C1', '', 'legacy row')
`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// Opening through the store must migrate rather than fail.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events after migration: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Text != "legacy row" {
		t.Fatalf("legacy text = %q, want %q", events[0].Text, "legacy row")
	}
	if len(events[0].Attachments) != 0 {
		t.Fatalf("legacy row gained attachments: %+v", events[0].Attachments)
	}

	// New writes with attachments must work against the migrated schema.
	fresh := makeEvent("telegram", "bot-b", "after migration", "in")
	fresh.Attachments = []protocol.Attachment{{Name: "new.png", MIME: "image/png", Size: 5}}
	if _, err := s.InsertEvent(fresh); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}

	fresh.Notify = true
	if _, err := s.InsertNotification(fresh); err != nil {
		t.Fatalf("insert notification after migration: %v", err)
	}

	events, err = s.ListEvents(EventFilter{Bot: "bot-b", Limit: 10})
	if err != nil {
		t.Fatalf("list migrated events: %v", err)
	}
	if len(events) != 1 || len(events[0].Attachments) != 1 {
		t.Fatalf("post-migration attachments = %+v", events)
	}
}

// Opening the same database twice must not attempt the migration a second time.
func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repeat.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := first.InsertEvent(makeEvent("slack", "bot-a", "hello", "in")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	events, err := second.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list after reopen: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
}

// A corrupt attachments blob should degrade to no attachments rather than
// making the whole row unreadable.
func TestMalformedAttachmentsColumnDegradesGracefully(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	id, err := s.InsertEvent(makeEvent("slack", "bot-a", "row with bad blob", "in"))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE events SET attachments = ? WHERE id = ?`, "{not json", id); err != nil {
		t.Fatalf("corrupt column: %v", err)
	}

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Text != "row with bad blob" {
		t.Fatalf("text = %q", events[0].Text)
	}
	if len(events[0].Attachments) != 0 {
		t.Fatalf("expected no attachments from corrupt blob, got %+v", events[0].Attachments)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// Path is a rendering of Key under the currently configured backend, so it
// must never reach the database - persisting it would strand history the
// moment the storage root moves.
func TestAttachmentPathIsNeverPersisted(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("telegram", "bot-a", "with file", "in")
	ev.Attachments = []protocol.Attachment{{
		Name:   "photo.jpg",
		Key:    "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Digest: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Path:   "/home/someone/.local/share/pantalk/media/9f/9f86d0.jpg",
	}}

	id, err := s.InsertEvent(ev)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	var raw string
	if err := s.db.QueryRow(`SELECT attachments FROM events WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw column: %v", err)
	}

	if strings.Contains(raw, "/home/someone") || strings.Contains(raw, `"path"`) {
		t.Fatalf("path leaked into storage: %s", raw)
	}
	if !strings.Contains(raw, `"key"`) {
		t.Fatalf("key was not persisted: %s", raw)
	}

	events, err := s.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if events[0].Attachments[0].Path != "" {
		t.Fatalf("path came back from storage: %q", events[0].Attachments[0].Path)
	}
	if events[0].Attachments[0].Key == "" {
		t.Fatalf("key did not round-trip")
	}
}

func TestAttachmentPathIsNotPersistedForNotifications(t *testing.T) {
	s := openTestStore(t)

	ev := makeEvent("telegram", "bot-a", "with file", "in")
	ev.Notify = true
	ev.Attachments = []protocol.Attachment{{Name: "a.png", Key: "abc", Path: "/tmp/should-not-persist.png"}}

	if _, err := s.InsertNotification(ev); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	var raw string
	if err := s.db.QueryRow(`SELECT attachments FROM notifications LIMIT 1`).Scan(&raw); err != nil {
		t.Fatalf("read raw column: %v", err)
	}

	if strings.Contains(raw, "should-not-persist") {
		t.Fatalf("path leaked into notification storage: %s", raw)
	}
}

func TestReferencedAttachmentKeysSpansBothTables(t *testing.T) {
	s := openTestStore(t)

	eventOnly := makeEvent("telegram", "bot-a", "in history", "in")
	eventOnly.Attachments = []protocol.Attachment{{Name: "a.png", Key: "key-event"}}
	if _, err := s.InsertEvent(eventOnly); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	notificationOnly := makeEvent("telegram", "bot-a", "in notifications", "in")
	notificationOnly.Notify = true
	notificationOnly.Attachments = []protocol.Attachment{{Name: "b.png", Key: "key-notification"}}
	if _, err := s.InsertNotification(notificationOnly); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	keys, err := s.ReferencedAttachmentKeys()
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}

	for _, want := range []string{"key-event", "key-notification"} {
		if _, ok := keys[want]; !ok {
			t.Fatalf("missing key %q in %v", want, keys)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2: %v", len(keys), keys)
	}
}

// The same file referenced by many rows must appear once, and must keep being
// reported until every referencing row is gone.
func TestReferencedAttachmentKeysDeduplicatesSharedKey(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 3; i++ {
		ev := makeEvent("telegram", "bot-a", "same file again", "in")
		ev.Attachments = []protocol.Attachment{{Name: "shared.png", Key: "shared-key"}}
		if _, err := s.InsertEvent(ev); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	keys, err := s.ReferencedAttachmentKeys()
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1: %v", len(keys), keys)
	}

	// Removing some referencing rows must not drop the key.
	if _, err := s.DeleteEvents(EventFilter{Search: "same file again"}, false); err != nil {
		t.Fatalf("delete events: %v", err)
	}

	keys, err = s.ReferencedAttachmentKeys()
	if err != nil {
		t.Fatalf("referenced keys after delete: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected no keys after deleting every referencing row, got %v", keys)
	}
}

func TestReferencedAttachmentKeysIgnoresAttachmentlessRows(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		if _, err := s.InsertEvent(makeEvent("slack", "bot-a", "plain", "in")); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	keys, err := s.ReferencedAttachmentKeys()
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected no keys, got %v", keys)
	}
}
