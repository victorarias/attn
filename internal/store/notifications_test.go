package store

import (
	"path/filepath"
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

func TestNotifications_SeverityRoundTrip(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, severity := range []NotificationSeverity{NotificationInfo, NotificationWarning, NotificationCritical} {
		rec, err := s.AddNotification(NotificationRecord{
			Kind:     "task_failed",
			Severity: severity,
			Title:    string(severity),
		}, now)
		if err != nil {
			t.Fatalf("add %s: %v", severity, err)
		}
		if rec.Severity != severity {
			t.Fatalf("returned severity = %q, want %q", rec.Severity, severity)
		}
	}

	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(all))
	}
	for _, got := range all {
		if string(got.Severity) != got.Title {
			t.Fatalf("read back severity %q for row titled %q", got.Severity, got.Title)
		}
	}
}

func TestNotifications_SeverityNormalizes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want NotificationSeverity
	}{
		{"", NotificationInfo},
		{"info", NotificationInfo},
		{"warning", NotificationWarning},
		{"critical", NotificationCritical},
		{"  Critical ", NotificationCritical},
		{"WARNING", NotificationWarning},
		{"criticial", NotificationInfo}, // a typo is not critical
		{"error", NotificationInfo},
		{"0", NotificationInfo},
	} {
		if got := NormalizeNotificationSeverity(tc.raw); got != tc.want {
			t.Fatalf("NormalizeNotificationSeverity(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// The same rule has to hold through the database, not just in the helper:
	// an unrecognized value written to the row must read back as info.
	s := New()
	now := time.Now().UTC()
	if _, err := s.AddNotification(NotificationRecord{
		Kind: "task_failed", Severity: NotificationSeverity("CRITICIAL"), Title: "typo",
	}, now); err != nil {
		t.Fatalf("add: %v", err)
	}
	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if all[0].Severity != NotificationInfo {
		t.Fatalf("typo severity read back as %q, want info", all[0].Severity)
	}
}

// A notification feed that predates the severity column must survive the
// migration with its rows intact and every one of them reading as info: the
// column is added, never recreated, and its DEFAULT is what carries them.
func TestMigration100CarriesPreSeverityNotifications(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	// Put the table back the way a store before migration 100 had it, with two
	// notifications already in it — one read, one not.
	if _, err := s.db.Exec(`ALTER TABLE notifications DROP COLUMN severity`); err != nil {
		t.Fatalf("drop severity column: %v", err)
	}
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	for i, row := range []struct{ id, title, readAt string }{
		{"n1", "Older failure", base.Format(sortableTimeFormat)},
		{"n2", "Newer failure", ""},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO notifications (id, kind, title, body, detail, source_kind, source_id, created_at, read_at)
			 VALUES (?, 'task_failed', ?, 'body', 'detail', 'task', 't1', ?, ?)`,
			row.id, row.title, base.Add(time.Duration(i)*time.Second).Format(sortableTimeFormat), row.readAt,
		); err != nil {
			t.Fatalf("plant pre-severity row %s: %v", row.id, err)
		}
	}

	// Sanity: the planted schema really is the old one, so a pass here would
	// mean the test proves nothing.
	if _, err := s.ListNotifications(); err == nil {
		t.Fatal("the planted schema already has severity; this test would pass without the migration")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list after migration: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("carried %d rows, want 2", len(all))
	}
	for _, got := range all {
		if got.Severity != NotificationInfo {
			t.Fatalf("carried row %q has severity %q, want info", got.Title, got.Severity)
		}
		if got.Body != "body" || got.Detail != "detail" || got.SourceID != "t1" {
			t.Fatalf("carried row %q lost fields: %+v", got.Title, got)
		}
	}
	// The read/unread split survives too — a migration that reset read_at would
	// re-raise every notification the user had already dealt with.
	unread, err := s.UnreadNotificationCount()
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}

	// The guarded ALTER makes a re-run a no-op rather than an error.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	if again, err := s.ListNotifications(); err != nil || len(again) != 2 {
		t.Fatalf("re-run changed the feed: %v (err %v)", again, err)
	}
}

func TestNotifications_UnreadCritical(t *testing.T) {
	s := New()
	base := time.Now().UTC().Truncate(time.Millisecond)

	// Nothing critical yet: a warning and an info do not count, and the title is
	// empty rather than borrowed from them.
	if _, err := s.AddNotification(NotificationRecord{
		Kind: "task_failed", Severity: NotificationWarning, Title: "Dead job",
	}, base); err != nil {
		t.Fatalf("add warning: %v", err)
	}
	if _, err := s.AddNotification(NotificationRecord{
		Kind: "note", Severity: NotificationInfo, Title: "Mundane",
	}, base.Add(time.Second)); err != nil {
		t.Fatalf("add info: %v", err)
	}
	n, title, err := s.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 0 || title != "" {
		t.Fatalf("with no critical rows got (%d, %q), want (0, \"\")", n, title)
	}

	older, err := s.AddNotification(NotificationRecord{
		Kind: "plugin_parked", Severity: NotificationCritical, Title: "Older critical",
	}, base.Add(2*time.Second))
	if err != nil {
		t.Fatalf("add older critical: %v", err)
	}
	newer, err := s.AddNotification(NotificationRecord{
		Kind: "plugin_parked", Severity: NotificationCritical, Title: "Newer critical",
	}, base.Add(3*time.Second))
	if err != nil {
		t.Fatalf("add newer critical: %v", err)
	}

	// Two unread, and the title is the NEWEST one's — not whichever the storage
	// engine happened to return first.
	n, title, err = s.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 2 || title != "Newer critical" {
		t.Fatalf("got (%d, %q), want (2, \"Newer critical\")", n, title)
	}

	// Reading the newest falls back to the older one rather than emptying the
	// title while a critical notification is still unread.
	if err := s.MarkNotificationRead(newer.ID, base.Add(4*time.Second)); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	n, title, err = s.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 1 || title != "Older critical" {
		t.Fatalf("after reading the newest got (%d, %q), want (1, \"Older critical\")", n, title)
	}

	// Reading the last one clears the surface entirely.
	if err := s.MarkNotificationRead(older.ID, base.Add(5*time.Second)); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	n, title, err = s.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 0 || title != "" {
		t.Fatalf("after reading every critical got (%d, %q), want (0, \"\")", n, title)
	}
}
