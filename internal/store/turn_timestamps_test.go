package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// turn_opened_at, turn_settled_at, automation_provider_cursors.observed_at and
// the created_at columns that order the delegation, workspace and pane listings
// are TEXT, so every comparison SQL makes on them is a text comparison and the
// stored encoding is the whole definition of "before". Rows a second apart
// cannot tell a working encoding from a broken one; what has to be right is what
// happens inside one second.
//
// Every fixture here therefore uses sub-second offsets whose
// trailing-zero-stripped encodings sort in an order that is not their own. Two
// shapes do it, and both are in raggedOffsets: a whole second, which under
// RFC3339Nano sorts above every stamp inside itself because 'Z' is 0x5A and '.'
// and the digits are below it; and a fraction that another fraction extends
// (".1234" against ".12345"), where the shorter one's 'Z' again lands above the
// longer one's next digit.

// raggedOffsets are ids in chronological order with the sub-second offsets that
// separate them, all inside one second.
var raggedOffsets = []struct {
	id     string
	offset time.Duration
}{
	{"r0", 0},
	{"r1234", 123400 * time.Microsecond},
	{"r12345", 123450 * time.Microsecond},
	{"r5", 500 * time.Millisecond},
}

func turnBase() time.Time { return time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) }

func chronologicalRaggedIDs() []string {
	out := make([]string, 0, len(raggedOffsets))
	for _, r := range raggedOffsets {
		out = append(out, r.id)
	}
	return out
}

// The turn guard, and the reason this change exists. A turn that was settled is
// closed, and the next attention-wanting state has to open a new one — that is
// the loop the whole queue rests on. The guard asks it as
// `turn_opened_at <= turn_settled_at`, in text, so a settle landing in the same
// second as the open it closes read as "a turn is already open" and
// OpenTurnIfClosed returned false. Nothing logged it: the session simply stopped
// appearing in the queue until some later turn happened to open.
//
// The whole-second case is the one that reaches users. A day-named snooze
// ("tomorrow", "Saturday", "Monday") resolves to an exact second — the client
// zeroes the milliseconds — and the woken turn is stamped with that deadline, so
// a whole-second turn_opened_at is what every such wake produces.
func TestATurnSettledInTheSecondItOpenedInCanReopen(t *testing.T) {
	cases := []struct {
		name            string
		opened, settled time.Duration
	}{
		{"opened on a whole second", 0, 500 * time.Millisecond},
		{"settled fraction extends the opened one", 123400 * time.Microsecond, 123450 * time.Microsecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTurnStore(t)
			addTurnSession(t, s, "s1", "working")

			if !s.OpenTurnIfClosed("s1", turnBase().Add(c.opened)) {
				t.Fatalf("the first turn did not open")
			}
			if !s.SettleTurn("s1", turnBase().Add(c.settled)) {
				t.Fatalf("settle failed")
			}
			if stamps := s.TurnStamps("s1"); !stamps.SettledAt.After(stamps.OpenedAt) {
				t.Fatalf("fixture is wrong: settled %s is not after opened %s",
					stamps.SettledAt, stamps.OpenedAt)
			}

			// The load-bearing assert: the turn is settled, so the next
			// attention-wanting state must open a new one.
			if !s.OpenTurnIfClosed("s1", turnBase().Add(c.settled+time.Millisecond)) {
				t.Fatalf("no turn opened after a settle in the same second; the session is silently out of the queue")
			}
		})
	}
}

// turn_snoozed_until is matched for equality (`turn_snoozed_until = ?`), not
// ordered, so changing the encoding cannot misorder it — it can only stop a
// stored deadline from matching the one a fired timer re-formats. That is what
// makes the migration load-bearing here rather than the format: a snooze written
// before it, in the old encoding, has to still be wakeable after it.
//
// A whole second is the case that matters, because every day-named snooze
// resolves to one — the client zeroes the milliseconds — and it is exactly where
// the two encodings disagree ("…:00Z" against "…:00.000000000Z").
func TestASnoozeWrittenInTheOldEncodingIsStillWakeable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()
	addTurnSession(t, s, "s1", "working")

	until := turnBase().Add(time.Hour)
	if _, err := s.db.Exec(`UPDATE sessions SET turn_snoozed_until = ? WHERE id = 's1'`,
		until.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("plant old snooze stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94: %v", err)
	}

	// Sanity: without the rewrite the timer cannot cash the deadline it holds.
	if s.WakeTurnAt("s1", until) {
		t.Fatalf("the planted stamp already matches; this test would pass without the migration")
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	if got := s.SnoozedSessions()["s1"]; !got.Equal(until) {
		t.Fatalf("stored deadline reads back as %s, want %s", got, until)
	}
	if !s.WakeTurnAt("s1", until) {
		t.Fatalf("after migration 94 the fired timer still could not cash its deadline")
	}
}

// The review-request cursor advance is guarded in SQL —
// `DO UPDATE SET observed_at=excluded.observed_at WHERE excluded.observed_at >=
// automation_provider_cursors.observed_at` — and it is the only observed_at
// comparison the store makes. A poll loop produces observations inside one
// second, and a later one that fails to advance the cursor leaves the fence
// standing on a stale instant.
//
// The schedule cursor deliberately is not the subject here: its upsert carries no
// WHERE, so it advances whatever the encoding does and proves nothing.
func TestAReviewRequestCursorAdvancesWithinASecond(t *testing.T) {
	s := newTurnStore(t)
	def, err := s.UpsertAutomationDefinition("reviews", "Reviews", `{"id":"reviews"}`, turnBase())
	if err != nil {
		t.Fatalf("upsert definition: %v", err)
	}

	first := turnBase()
	second := turnBase().Add(500 * time.Millisecond)
	for _, at := range []time.Time{first, second} {
		if _, err := s.ReconcileAutomationReviewRequests(
			def.ID, "github.com", []string{"github.com/owner/repo#1"}, at); err != nil {
			t.Fatalf("reconcile at %s: %v", at, err)
		}
	}

	var raw string
	if err := s.db.QueryRow(
		`SELECT observed_at FROM automation_provider_cursors
		  WHERE definition_id = ? AND provider = 'github_review_requested' AND scope = 'github.com'`,
		def.ID).Scan(&raw); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	got, err := docstore.ParseTime(raw)
	if err != nil {
		t.Fatalf("parse cursor %q: %v", raw, err)
	}
	if !got.Equal(second) {
		t.Fatalf("cursor is at %s, want the later observation %s", got, second)
	}
}

// PendingDelegationOperations feeds the daemon's recovery of delegations it
// still owes work for, in `ORDER BY created_at`. A burst of claims inside one
// second is the ordinary case — a chief fires several delegations at once — and
// they have to come back in the order they were claimed.
func TestPendingDelegationOperationsAreInClaimOrderWithinASecond(t *testing.T) {
	s := newTurnStore(t)

	for _, r := range raggedOffsets {
		if _, _, err := s.ClaimDelegationOperation(
			r.id, "op-"+r.id, "sess-"+r.id, "chief", "", `{}`, turnBase().Add(r.offset)); err != nil {
			t.Fatalf("claim %s: %v", r.id, err)
		}
	}

	got, err := s.PendingDelegationOperations()
	if err != nil {
		t.Fatalf("pending delegation operations: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, rec := range got {
		ids = append(ids, rec.Operation.RequestID)
	}
	if want := chronologicalRaggedIDs(); !sameOrder(ids, want) {
		t.Fatalf("pending delegations came back as %v, want %v", ids, want)
	}
}

// ListWorkflowRuns promises newest-first, in `ORDER BY created_at DESC`. Its
// stamps come from the CLI through protocol.TimestampNow(), so the store
// normalizes them on write — this is the assert that the normalizing happens at
// all, rather than the column inheriting whatever spelling a caller chose.
func TestWorkflowRunsAreNewestFirstWithinASecond(t *testing.T) {
	s := newTurnStore(t)

	for _, r := range raggedOffsets {
		at := turnBase().Add(r.offset).Format(time.RFC3339Nano)
		if err := s.UpsertWorkflowRun(&WorkflowRunRow{
			RunID: r.id, ScriptPath: "s.js", ScriptHash: "h", Status: "running",
			CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("upsert run %s: %v", r.id, err)
		}
	}

	runs, err := s.ListWorkflowRuns("")
	if err != nil {
		t.Fatalf("list workflow runs: %v", err)
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.RunID)
	}
	if want := reversed(chronologicalRaggedIDs()); !sameOrder(ids, want) {
		t.Fatalf("workflow runs came back as %v, want %v", ids, want)
	}
}

// Migration 94 rewrites what earlier versions stored. Its input is the encoding
// they wrote, so the test writes that encoding rather than describing it: the
// stamps go in the way an older store would have written them, the migration
// runs, and the guard and the order that were wrong come back right.
func TestMigration94RewritesTurnCursorAndListingStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	addTurnSession(t, s, "s1", "working")
	for _, r := range raggedOffsets {
		if _, _, err := s.ClaimDelegationOperation(
			r.id, "op-"+r.id, "sess-"+r.id, "chief", "", `{}`, turnBase().Add(r.offset)); err != nil {
			t.Fatalf("claim %s: %v", r.id, err)
		}
	}

	// Roll the stamps back to the encoding migration 94 exists to replace: a turn
	// opened on a whole second and settled half a second later, and delegation
	// rows whose fractions extend one another. One value the migration cannot read
	// is planted too, which it must leave alone rather than turn into year 1, and
	// turn_snoozed_until stays '' — a session that is not snoozed holds a sentinel,
	// not a stamp that failed to parse.
	if _, err := s.db.Exec(
		`UPDATE sessions SET turn_opened_at = ?, turn_settled_at = ? WHERE id = 's1'`,
		turnBase().Format(time.RFC3339Nano),
		turnBase().Add(500*time.Millisecond).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("plant old turn stamps: %v", err)
	}
	for _, r := range raggedOffsets {
		old := turnBase().Add(r.offset).Format(time.RFC3339Nano)
		if _, err := s.db.Exec(
			`UPDATE delegation_operations SET created_at = ? WHERE request_id = ?`, old, r.id); err != nil {
			t.Fatalf("plant old delegation stamp for %s: %v", r.id, err)
		}
	}
	if _, err := s.db.Exec(
		`UPDATE delegation_operations SET updated_at = 'not a timestamp' WHERE request_id = ?`, "r5"); err != nil {
		t.Fatalf("plant unreadable stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94: %v", err)
	}

	// Sanity: the planted state is the broken one, so a pass here would mean the
	// test proves nothing.
	if s.OpenTurnIfClosed("s1", turnBase().Add(time.Second)) {
		t.Fatalf("the planted turn stamps already reopen correctly; this test would pass without the migration")
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	assertMigration94Applied(t, s)

	// Re-running finds nothing left to do and changes nothing: the rewrite is a
	// decode and re-encode, so an already-converted stamp yields itself.
	before := stampDigest(t, s)
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	if after := stampDigest(t, s); after != before {
		t.Fatalf("a second run changed the stamps:\n%s\nto\n%s", before, after)
	}
}

// assertMigration94Applied is the post-migration state: the settled turn reopens,
// the delegation listing is in claim order, the stamp that does not decode is
// untouched, and the unsnoozed sentinel is still the sentinel.
func assertMigration94Applied(t *testing.T, s *Store) {
	t.Helper()

	if !s.OpenTurnIfClosed("s1", turnBase().Add(time.Second)) {
		t.Fatalf("after migration 94 a settled turn still did not reopen")
	}

	got, err := s.PendingDelegationOperations()
	if err != nil {
		t.Fatalf("pending delegation operations: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, rec := range got {
		ids = append(ids, rec.Operation.RequestID)
	}
	if want := chronologicalRaggedIDs(); !sameOrder(ids, want) {
		t.Fatalf("after migration 94 the delegations came back as %v, want %v", ids, want)
	}

	var unreadable string
	if err := s.db.QueryRow(
		`SELECT updated_at FROM delegation_operations WHERE request_id = 'r5'`).Scan(&unreadable); err != nil {
		t.Fatal(err)
	}
	if unreadable != "not a timestamp" {
		t.Fatalf("the unreadable stamp became %q; it must be left as it was", unreadable)
	}

	var snoozed string
	if err := s.db.QueryRow(`SELECT turn_snoozed_until FROM sessions WHERE id = 's1'`).Scan(&snoozed); err != nil {
		t.Fatal(err)
	}
	if snoozed != "" {
		t.Fatalf("turn_snoozed_until became %q; the unsnoozed sentinel must be left as it is", snoozed)
	}
}

// stampDigest is every stamp migration 94 touches, in a stable order, so a
// second run can be compared byte for byte against the first.
func stampDigest(t *testing.T, s *Store) string {
	t.Helper()
	var digest string
	if err := s.db.QueryRow(`
		SELECT (SELECT group_concat(turn_opened_at || '|' || turn_settled_at || '|' || turn_snoozed_until, ';')
		          FROM (SELECT * FROM sessions ORDER BY id))
		    || '#' ||
		       (SELECT group_concat(created_at || '|' || updated_at, ';')
		          FROM (SELECT * FROM delegation_operations ORDER BY request_id))
	`).Scan(&digest); err != nil {
		t.Fatalf("stamp digest: %v", err)
	}
	return digest
}
