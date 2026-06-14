package notebook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDreamCandidatesRoundTrip(t *testing.T) {
	root := t.TempDir()

	// Missing file reads as an empty set, not an error.
	got, err := LoadDreamCandidates(root)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}

	want := []DreamCandidate{
		{SignalKey: "k1", Source: "journal", Snippet: "a fact", Sources: []string{"/journal/2026-06-13.md"}, Contexts: []string{"journal:2026-06-13"}, Occurrences: 1},
		{SignalKey: "k2", Source: "context", Snippet: "another", Sources: []string{"context:ws-a", "context:ws-b"}, Contexts: []string{"workspace:ws-a", "workspace:ws-b"}, Occurrences: 2},
	}
	if err := SaveDreamCandidates(root, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = LoadDreamCandidates(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].SignalKey != "k1" || got[1].Occurrences != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// The state lives under the .attn dreams dir, never as a surfaced note.
	if _, err := os.Stat(filepath.Join(DreamsStateDir(root), dreamsCandidatesFile)); err != nil {
		t.Fatalf("candidates file not written: %v", err)
	}
}

func TestDreamRunStateRoundTrip(t *testing.T) {
	root := t.TempDir()

	zero, err := LoadDreamRunState(root)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if zero.ScheduledFrom != "" || zero.LastRunAt != "" {
		t.Fatalf("expected zero state, got %+v", zero)
	}

	if err := SaveDreamRunState(root, DreamRunState{
		ScheduledFrom:         "2026-06-14T03:00:00Z",
		LastRunAt:             "2026-06-14T03:00:00Z",
		LastRunNewCandidates:  3,
		LastRunCandidateCount: 10,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadDreamRunState(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Version != dreamStateVersion {
		t.Fatalf("expected version stamped to %d, got %d", dreamStateVersion, got.Version)
	}
	if got.LastRunCandidateCount != 10 || got.LastRunNewCandidates != 3 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestDreamLockSingleFlightAndRecovery(t *testing.T) {
	root := t.TempDir()

	release, err := AcquireDreamLock(root)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// A second acquire while held must fail (single writer).
	if _, err := AcquireDreamLock(root); err == nil {
		t.Fatal("expected second acquire to fail while lock is held")
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Release is idempotent.
	if err := release(); err != nil {
		t.Fatalf("second release should be a no-op: %v", err)
	}

	// After release the lock is free again.
	release2, err := AcquireDreamLock(root)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	_ = release2()
}

func TestClearOrphanDreamLocks(t *testing.T) {
	root := t.TempDir()

	// No locks dir yet → nothing to clear, no error.
	if n, err := ClearOrphanDreamLocks(root); err != nil || n != 0 {
		t.Fatalf("clear empty: n=%d err=%v", n, err)
	}

	// A lock left behind by a crashed run is orphaned: clear it, then the lock is
	// re-acquirable (proving recovery unblocks the next scheduled run).
	if _, err := AcquireDreamLock(root); err != nil {
		t.Fatalf("acquire (simulating crash that never released): %v", err)
	}
	if _, err := AcquireDreamLock(root); err == nil {
		t.Fatal("sanity: lock should still be held before recovery")
	}
	n, err := ClearOrphanDreamLocks(root)
	if err != nil || n != 1 {
		t.Fatalf("clear orphan: n=%d err=%v", n, err)
	}
	release, err := AcquireDreamLock(root)
	if err != nil {
		t.Fatalf("acquire after recovery: %v", err)
	}
	_ = release()
}

func TestLoadDreamCandidateSetMergesIdempotently(t *testing.T) {
	persisted := []DreamCandidate{
		{SignalKey: signalKey("a durable fact worth keeping"), Source: "journal",
			Snippet: "a durable fact worth keeping", Sources: []string{"/journal/2026-06-13.md"},
			Contexts: []string{"journal:2026-06-13"}, Occurrences: 1},
	}
	set := LoadDreamCandidateSet(persisted)
	if set.Len() != 1 {
		t.Fatalf("expected 1 rehydrated candidate, got %d", set.Len())
	}

	// Re-adding the SAME source ref must not inflate occurrences (idempotent
	// re-harvest of an already-persisted source).
	set.Add(DreamSignal{Source: "journal", Text: "a durable fact worth keeping",
		SourceRef: "/journal/2026-06-13.md", Context: "journal:2026-06-13", Seen: "2026-06-13"})
	if got := set.Candidates()[0].Occurrences; got != 1 {
		t.Fatalf("idempotent re-add inflated occurrences to %d", got)
	}

	// A NEW distinct source for the same fact recurs → occurrences and contexts grow.
	set.Add(DreamSignal{Source: "journal", Text: "a durable fact worth keeping",
		SourceRef: "/journal/2026-06-20.md", Context: "journal:2026-06-20", Seen: "2026-06-20"})
	c := set.Candidates()[0]
	if c.Occurrences != 2 || c.DistinctContexts() != 2 {
		t.Fatalf("expected occurrences=2 contexts=2, got occ=%d ctx=%d", c.Occurrences, c.DistinctContexts())
	}

	// Corrupt entries (empty / duplicate keys) are skipped on load.
	dup := LoadDreamCandidateSet([]DreamCandidate{
		{SignalKey: "x", Snippet: "1"}, {SignalKey: "x", Snippet: "2"}, {SignalKey: "", Snippet: "3"},
	})
	if dup.Len() != 1 {
		t.Fatalf("expected dedup/skip to yield 1, got %d", dup.Len())
	}
}
