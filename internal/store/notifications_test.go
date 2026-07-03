package store

import (
	"testing"
	"time"
)

func TestNotifications_AddListUnread(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	rec, err := s.AddNotification(NotificationRecord{
		Kind:       "task_failed",
		Title:      "Compaction failed",
		Body:       "compact_context for ws-1 gave up after 3 attempts",
		Detail:     "boom: context deadline exceeded",
		SourceKind: "task",
		SourceID:   "compact_context:ws-1",
	}, now)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if rec.ID == "" {
		t.Fatalf("expected generated id")
	}
	if !rec.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v, want %v", rec.CreatedAt, now)
	}
	if !rec.ReadAt.IsZero() {
		t.Fatalf("new notification should be unread, read_at=%v", rec.ReadAt)
	}

	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(all))
	}
	got := all[0]
	if got.Kind != "task_failed" || got.Title != "Compaction failed" ||
		got.Detail != "boom: context deadline exceeded" || got.SourceID != "compact_context:ws-1" {
		t.Fatalf("fields mismatch on read-back: %+v", got)
	}
	if !got.ReadAt.IsZero() {
		t.Fatalf("expected unread on read-back, read_at=%v", got.ReadAt)
	}

	n, err := s.UnreadNotificationCount()
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if n != 1 {
		t.Fatalf("unread = %d, want 1", n)
	}
}

func TestNotifications_ListNewestFirst(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i, ts := range []time.Time{base.Add(1 * time.Second), base.Add(3 * time.Second), base.Add(2 * time.Second)} {
		if _, err := s.AddNotification(NotificationRecord{Kind: "task_failed", Title: string(rune('a' + i))}, ts); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
	// Titles were a,b,c at t+1,t+3,t+2 → newest-first is b,c,a.
	order := []string{all[0].Title, all[1].Title, all[2].Title}
	want := []string{"b", "c", "a"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestNotifications_MarkRead(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	r1, _ := s.AddNotification(NotificationRecord{Kind: "task_failed", Title: "one"}, base)
	_, _ = s.AddNotification(NotificationRecord{Kind: "task_failed", Title: "two"}, base.Add(time.Second))

	readAt := base.Add(time.Minute)
	if err := s.MarkNotificationRead(r1.ID, readAt); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	n, _ := s.UnreadNotificationCount()
	if n != 1 {
		t.Fatalf("unread after one read = %d, want 1", n)
	}

	// Read timestamp is preserved on a second mark-read (idempotent).
	later := readAt.Add(time.Hour)
	if err := s.MarkNotificationRead(r1.ID, later); err != nil {
		t.Fatalf("mark read again: %v", err)
	}
	all, _ := s.ListNotifications()
	var got NotificationRecord
	for _, r := range all {
		if r.ID == r1.ID {
			got = r
		}
	}
	if got.ReadAt.IsZero() {
		t.Fatalf("expected read after mark, got unread")
	}
	if !got.ReadAt.Equal(readAt) {
		t.Fatalf("read_at moved on re-mark: got %v want %v", got.ReadAt, readAt)
	}

	// Marking a missing id is not an error.
	if err := s.MarkNotificationRead("absent", readAt); err != nil {
		t.Fatalf("mark missing: %v", err)
	}
}

func TestNotifications_MarkAllRead(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := range 3 {
		if _, err := s.AddNotification(NotificationRecord{Kind: "task_failed"}, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	flipped, err := s.MarkAllNotificationsRead(base.Add(time.Minute))
	if err != nil {
		t.Fatalf("mark all: %v", err)
	}
	if flipped != 3 {
		t.Fatalf("flipped %d, want 3", flipped)
	}
	if n, _ := s.UnreadNotificationCount(); n != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", n)
	}
	// A second mark-all flips nothing.
	flipped, err = s.MarkAllNotificationsRead(base.Add(2 * time.Minute))
	if err != nil {
		t.Fatalf("mark all again: %v", err)
	}
	if flipped != 0 {
		t.Fatalf("second mark-all flipped %d, want 0", flipped)
	}
}
