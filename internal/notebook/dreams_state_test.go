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
		ScheduledFrom: "2026-06-14T03:00:00Z",
		LastRunAt:     "2026-06-14T03:00:00Z",
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
	if got.ScheduledFrom != "2026-06-14T03:00:00Z" || got.LastRunAt != "2026-06-14T03:00:00Z" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
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
