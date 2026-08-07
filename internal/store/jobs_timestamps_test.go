package store

import (
	"path/filepath"
	"testing"
	"time"
)

// scheduled_at, created_at and updated_at are TEXT columns, so every comparison
// the queue makes on them — claiming (`scheduled_at <= now`), listing
// (`ORDER BY updated_at DESC`), retention (`updated_at < cutoff`) — is a text
// comparison, and the stored encoding is the whole definition of "before". Rows
// a second apart cannot tell a working encoding from a broken one: what has to
// be right is what happens inside one second, which is where a burst of enqueues
// and a whole-second schedule both land.
//
// Every fixture here therefore uses sub-second offsets whose
// trailing-zero-stripped encodings sort in an order that is not their own,
// including the whole second itself — under RFC3339Nano "…:00Z" sorts above
// every stamp in its own second, because 'Z' is 0x5A and '.' and the digits are
// below it.

// raggedJobOffsets are ids in chronological order with the sub-second offsets
// that separate them, all inside one second.
var raggedJobOffsets = []struct {
	id     string
	offset time.Duration
}{
	{"j0", 0},
	{"j1234", 123400 * time.Microsecond},
	{"j12345", 123450 * time.Microsecond},
	{"j5", 500 * time.Millisecond},
}

func jobBase() time.Time { return time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) }

func jobIDs(recs []JobRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func chronologicalJobIDs() []string {
	out := make([]string, 0, len(raggedJobOffsets))
	for _, r := range raggedJobOffsets {
		out = append(out, r.id)
	}
	return out
}

// storeWithRaggedJobs writes the four jobs, each scheduled, created and updated
// at its own instant inside one second.
func storeWithRaggedJobs(t *testing.T) *Store {
	t.Helper()
	s := New()
	for _, r := range raggedJobOffsets {
		if err := s.UpsertJob(newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
	}
	return s
}

// The claim boundary. A job scheduled on a whole second is the ordinary case —
// it is what a whole-second clock and a hand-written schedule both produce — and
// under the old encoding it sorted above every stamp inside that second, so
// `scheduled_at <= now` refused it until the next second ticked over. A second
// of wobble on every such job, reported nowhere.
func TestAJobScheduledOnAWholeSecondIsClaimableAtThatSecond(t *testing.T) {
	s := New()
	at := jobBase()
	if err := s.UpsertJob(newJobRecord("whole", "compact_context", at)); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, now := range []time.Time{at, at.Add(time.Millisecond), at.Add(500 * time.Millisecond)} {
		got, err := s.EligibleJobs(now, 10)
		if err != nil {
			t.Fatalf("eligible jobs at %v: %v", now, err)
		}
		if len(got) != 1 {
			t.Fatalf("a job scheduled at %s was not claimable at %s: got %v",
				at.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), jobIDs(got))
		}
	}
}

// Claiming order is what decides which job runs first when more are eligible
// than the sweep will take, and the tie-break within one priority is
// scheduled_at. Four jobs enqueued inside one second must come back in the order
// they were scheduled for, not in the order their text happens to sort.
func TestEligibleJobsComeBackInScheduledOrderWithinASecond(t *testing.T) {
	s := storeWithRaggedJobs(t)

	got, err := s.EligibleJobs(jobBase().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("eligible jobs: %v", err)
	}
	if want := chronologicalJobIDs(); !sameOrder(jobIDs(got), want) {
		t.Fatalf("eligible jobs came back as %v, want %v", jobIDs(got), want)
	}
}

// ListJobs is the queue's inspection surface (`attn jobs`), and it promises
// newest-updated first.
func TestListJobsIsNewestUpdatedFirstWithinASecond(t *testing.T) {
	s := storeWithRaggedJobs(t)

	got, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(jobIDs(got), want) {
		t.Fatalf("list jobs came back as %v, want %v", jobIDs(got), want)
	}
}

// Retention deletes done rows updated before a cutoff. With the cutoff inside a
// second, the rows updated before it must go and the ones updated after it must
// stay — under the old encoding the row updated exactly on the second survived a
// cutoff later than it, because its text sorted above the cutoff's.
func TestTrimDoneJobsDeletesByTimeWithinASecond(t *testing.T) {
	s := New()
	for _, r := range raggedJobOffsets {
		rec := newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))
		rec.State = "done"
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
	}

	n, err := s.TrimDoneJobs(jobBase().Add(200 * time.Millisecond))
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if n != 3 {
		t.Fatalf("trimmed %d jobs, want the 3 updated before the cutoff", n)
	}
	left, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if want := []string{"j5"}; !sameOrder(jobIDs(left), want) {
		t.Fatalf("trim left %v, want %v", jobIDs(left), want)
	}
}

// The notifications feed stores its stamps in the same encoding and lists by
// created_at, so a burst of failures inside one second is where its order breaks.
func TestNotificationsListNewestFirstWithinASecond(t *testing.T) {
	s := New()
	for _, r := range raggedJobOffsets {
		if _, err := s.AddNotification(
			NotificationRecord{Kind: "task_failed", Title: r.id}, jobBase().Add(r.offset)); err != nil {
			t.Fatalf("add notification %s: %v", r.id, err)
		}
	}

	got, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	titles := make([]string, 0, len(got))
	for _, n := range got {
		titles = append(titles, n.Title)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(titles, want) {
		t.Fatalf("notifications came back as %v, want %v", titles, want)
	}
}

// Migration 94 rewrites what earlier versions stored. Its input is the encoding
// they wrote, so the test writes that encoding rather than describing it: the
// stamps go in the way an older store would have written them, the migration
// runs, and the claim and the order that were wrong come back right.
func TestMigration94RewritesJobAndNotificationStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, r := range raggedJobOffsets {
		if err := s.UpsertJob(newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
		if _, err := s.AddNotification(
			NotificationRecord{Kind: "task_failed", Title: r.id}, jobBase().Add(r.offset)); err != nil {
			t.Fatalf("add notification %s: %v", r.id, err)
		}
	}

	// Roll the stamps back to the encoding migration 94 exists to replace, and
	// plant one value it cannot read, which it must leave alone rather than turn
	// into year 1. read_at stays '' on every notification: an unread row's
	// sentinel is not a stamp that failed to parse, and the migration must leave
	// it as it is too.
	for _, r := range raggedJobOffsets {
		old := jobBase().Add(r.offset).Format(time.RFC3339Nano)
		if _, err := s.db.Exec(
			`UPDATE jobs SET scheduled_at = ?, created_at = ?, updated_at = ? WHERE id = ?`,
			old, old, old, r.id); err != nil {
			t.Fatalf("plant old job stamp for %s: %v", r.id, err)
		}
		if _, err := s.db.Exec(
			`UPDATE notifications SET created_at = ? WHERE title = ?`, old, r.id); err != nil {
			t.Fatalf("plant old notification stamp for %s: %v", r.id, err)
		}
	}
	if _, err := s.db.Exec(
		`UPDATE jobs SET created_at = 'not a timestamp' WHERE id = ?`, "j5"); err != nil {
		t.Fatalf("plant unreadable stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94: %v", err)
	}

	// Sanity: the planted state is the broken one, so a pass here would mean the
	// test proves nothing.
	if got, err := s.EligibleJobs(jobBase(), 10); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatalf("the planted stamps already claim correctly (%v); this test would pass without the migration", jobIDs(got))
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	assertMigration94Applied(t, s)

	// Re-running finds nothing left to do and changes nothing: the rewrite is a
	// decode and re-encode, so an already-converted stamp yields itself.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	assertMigration94Applied(t, s)
}

// assertMigration94Applied is the post-migration state: the whole-second job is
// claimable at its own second, both feeds order by time, the stamp that does not
// decode is untouched, and an unread notification is still unread.
func assertMigration94Applied(t *testing.T, s *Store) {
	t.Helper()
	got, err := s.EligibleJobs(jobBase(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"j0"}; !sameOrder(jobIDs(got), want) {
		t.Fatalf("after migration 94 the jobs claimable at the whole second are %v, want %v", jobIDs(got), want)
	}

	notes, err := s.ListNotifications()
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, 0, len(notes))
	for _, n := range notes {
		titles = append(titles, n.Title)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(titles, want) {
		t.Fatalf("after migration 94 the notifications list as %v, want %v", titles, want)
	}
	for _, n := range notes {
		if !n.ReadAt.IsZero() {
			t.Fatalf("notification %q came back read; read_at must stay the unread sentinel", n.Title)
		}
	}
	var unreadable string
	if err := s.db.QueryRow(`SELECT created_at FROM jobs WHERE id = ?`, "j5").Scan(&unreadable); err != nil {
		t.Fatal(err)
	}
	if unreadable != "not a timestamp" {
		t.Fatalf("the unreadable stamp became %q; it must be left as it was", unreadable)
	}
}
