package store

import (
	"testing"
	"time"
)

// seedLegacyTask writes a row directly into the retired task runner's table,
// which nothing in the codebase writes anymore — the handover is the only reader.
func seedLegacyTask(t *testing.T, s *Store, id, kind, subject, state, meta string, at time.Time) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, kind, subject, state, 2, at.Format(jobTimeFormat), "boom", meta, 1,
		at.Format(jobTimeFormat), at.Format(jobTimeFormat))
	if err != nil {
		t.Fatalf("seed legacy task %s: %v", id, err)
	}
}

func legacyTaskCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatalf("count legacy tasks: %v", err)
	}
	return n
}

func jobCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// oneForOne is the translation a caller with nothing to interpret would write:
// every legacy row becomes the job that replaces it, keeping its id.
func oneForOne(rec LegacyTaskRecord) JobRecord {
	return JobRecord{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.Subject,
		Payload:     rec.MetaJSON,
		State:       rec.State,
		Attempts:    rec.Attempts,
		ScheduledAt: rec.NextAttemptAt,
		LastError:   rec.LastError,
		Requeued:    rec.Requeued,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
}

// The handover moves everything still owed onto the jobs table and empties the
// old one, so it cannot run twice on the same rows.
func TestMigrateLegacyTasksMovesOwedWorkAndEmptiesTheTable(t *testing.T) {
	s := New()
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedLegacyTask(t, s, "summarize_session:s-1", "summarize_session", "s-1", "queued",
		`{"transcript":"/tmp/turn.jsonl"}`, at)
	seedLegacyTask(t, s, "reconcile:t-1", "reconcile", "t-1", "dead", "", at)

	var seen []LegacyTaskRecord
	moved, err := s.MigrateLegacyTasks(func(rec LegacyTaskRecord) JobRecord {
		seen = append(seen, rec)
		return oneForOne(rec)
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if moved != 2 {
		t.Fatalf("migrated %d rows, want 2", moved)
	}

	byID := map[string]LegacyTaskRecord{}
	for _, rec := range seen {
		byID[rec.ID] = rec
	}
	summarize, ok := byID["summarize_session:s-1"]
	if !ok {
		t.Fatalf("the summarize task was never handed to the translation: %+v", seen)
	}
	if summarize.Subject != "s-1" || summarize.State != "queued" || summarize.Attempts != 2 ||
		summarize.LastError != "boom" || !summarize.Requeued ||
		summarize.MetaJSON != `{"transcript":"/tmp/turn.jsonl"}` {
		t.Fatalf("summarize row lost fields in the handover: %+v", summarize)
	}
	if !summarize.NextAttemptAt.Equal(at) || !summarize.CreatedAt.Equal(at) {
		t.Fatalf("summarize row lost timestamps: %+v", summarize)
	}

	written, ok, err := s.GetJob("summarize_session:s-1")
	if err != nil || !ok {
		t.Fatalf("the summarize job was not written: %v (ok=%v)", err, ok)
	}
	if written.State != "queued" || written.UniqueKey != "s-1" {
		t.Fatalf("the summarize job lost fields: %+v", written)
	}

	if n := legacyTaskCount(t, s); n != 0 {
		t.Fatalf("%d legacy rows survived the handover", n)
	}
	again, err := s.MigrateLegacyTasks(oneForOne)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if again != 0 {
		t.Fatalf("a second handover moved %d rows, want none", again)
	}
}

// Work that already happened is dropped rather than moved: nothing re-runs a
// done task, and moving it would only replay old rows into the new table.
func TestMigrateLegacyTasksDropsCompletedRows(t *testing.T) {
	s := New()
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedLegacyTask(t, s, "compact_context:ws-done", "compact_context", "ws-done", "done", "", at)
	seedLegacyTask(t, s, "compact_context:ws-owed", "compact_context", "ws-owed", "failed", "", at)

	var seen []string
	moved, err := s.MigrateLegacyTasks(func(rec LegacyTaskRecord) JobRecord {
		seen = append(seen, rec.Subject)
		return oneForOne(rec)
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if moved != 1 || len(seen) != 1 || seen[0] != "ws-owed" {
		t.Fatalf("migrated %d rows %v, want only the owed one", moved, seen)
	}
	if _, ok, _ := s.GetJob("compact_context:ws-done"); ok {
		t.Fatal("a completed task was replayed into the jobs table")
	}
	if n := legacyTaskCount(t, s); n != 0 {
		t.Fatalf("%d legacy rows survived the handover", n)
	}
}

// The whole point of doing this in one transaction. If any job write fails, the
// old rows must still be there: this path runs once per installation and the
// work it carries has no other copy, so a partial handover is lost work.
func TestAFailedJobWriteLeavesEveryLegacyRowIntact(t *testing.T) {
	s := New()
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedLegacyTask(t, s, "compact_context:ws-1", "compact_context", "ws-1", "queued", "", at)
	seedLegacyTask(t, s, "compact_context:ws-2", "compact_context", "ws-2", "queued", "", at)
	seedLegacyTask(t, s, "compact_context:ws-3", "compact_context", "ws-3", "queued", "", at)

	// Two distinct ids claiming one coalescing key: the partial unique index on
	// (kind, unique_key) refuses the second write, mid-handover.
	collide := func(rec LegacyTaskRecord) JobRecord {
		job := oneForOne(rec)
		job.UniqueKey = "same"
		return job
	}
	if _, err := s.MigrateLegacyTasks(collide); err == nil {
		t.Fatal("a colliding job write was accepted; this test no longer proves anything")
	}

	if n := legacyTaskCount(t, s); n != 3 {
		t.Fatalf("%d legacy rows left after a failed handover, want all 3 — the rest is lost work", n)
	}
	if n := jobCount(t, s); n != 0 {
		t.Fatalf("%d job rows survived a rolled-back handover, want 0", n)
	}

	// The failure is recoverable: nothing was consumed, so a later start that can
	// translate the rows moves all of them.
	moved, err := s.MigrateLegacyTasks(oneForOne)
	if err != nil {
		t.Fatalf("migrate after a failed attempt: %v", err)
	}
	if moved != 3 {
		t.Fatalf("the retry moved %d rows, want all 3", moved)
	}
	if n := legacyTaskCount(t, s); n != 0 {
		t.Fatalf("%d legacy rows survived the retry", n)
	}
}

// A store with no database reports it rather than quietly reporting a handover
// of zero rows, which reads identically to "there was nothing owed".
func TestMigrateLegacyTasksWithoutADatabaseIsAnError(t *testing.T) {
	var s Store
	if _, err := s.MigrateLegacyTasks(oneForOne); err == nil {
		t.Fatal("migrating without a database reported success")
	}
}
