package store

import (
	"testing"
	"time"
)

// seedLegacyTask writes a row directly into the retired task runner's table,
// which nothing in the codebase writes anymore — the drain is the only reader.
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

// The drain hands over everything still owed and empties the table, so the
// import that follows it cannot run twice on the same rows.
func TestDrainLegacyTasksReturnsOwedWorkAndEmptiesTheTable(t *testing.T) {
	s := New()
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedLegacyTask(t, s, "summarize_session:s-1", "summarize_session", "s-1", "queued",
		`{"transcript":"/tmp/turn.jsonl"}`, at)
	seedLegacyTask(t, s, "reconcile:t-1", "reconcile", "t-1", "dead", "", at)

	drained, err := s.DrainLegacyTasks()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(drained) != 2 {
		t.Fatalf("drained %d rows, want 2: %+v", len(drained), drained)
	}
	byID := map[string]LegacyTaskRecord{}
	for _, rec := range drained {
		byID[rec.ID] = rec
	}
	summarize, ok := byID["summarize_session:s-1"]
	if !ok {
		t.Fatalf("drained rows missing the summarize task: %+v", drained)
	}
	if summarize.Subject != "s-1" || summarize.State != "queued" || summarize.Attempts != 2 ||
		summarize.LastError != "boom" || !summarize.Requeued ||
		summarize.MetaJSON != `{"transcript":"/tmp/turn.jsonl"}` {
		t.Fatalf("summarize row lost fields in the drain: %+v", summarize)
	}
	if !summarize.NextAttemptAt.Equal(at) || !summarize.CreatedAt.Equal(at) {
		t.Fatalf("summarize row lost timestamps: %+v", summarize)
	}

	if n := legacyTaskCount(t, s); n != 0 {
		t.Fatalf("%d legacy rows survived the drain", n)
	}
	again, err := s.DrainLegacyTasks()
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("a second drain returned %d rows, want none", len(again))
	}
}

// Work that already happened is dropped rather than handed over: nothing re-runs
// a done task, and importing it would only replay old rows into the new table.
func TestDrainLegacyTasksDropsCompletedRows(t *testing.T) {
	s := New()
	at := time.Now().UTC().Truncate(time.Millisecond)
	seedLegacyTask(t, s, "compact_context:ws-done", "compact_context", "ws-done", "done", "", at)
	seedLegacyTask(t, s, "compact_context:ws-owed", "compact_context", "ws-owed", "failed", "", at)

	drained, err := s.DrainLegacyTasks()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(drained) != 1 || drained[0].Subject != "ws-owed" {
		t.Fatalf("drained = %+v, want only the owed row", drained)
	}
	if n := legacyTaskCount(t, s); n != 0 {
		t.Fatalf("%d legacy rows survived the drain", n)
	}
}
