package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newMarkdownAnnotationTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const testMDPath = "/tmp/plan.md"

func TestMarkdownAnnotationDraftSaveGetRoundtrip(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	annotations := `[{"id":"a1","type":"comment","text":"hi","created_at":1752494400000}]`
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, annotations, 1, now); err != nil {
		t.Fatalf("save gen 1: %v", err)
	}

	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != annotations {
		t.Errorf("annotations = %q, want %q", draft.Annotations, annotations)
	}
	if draft.Generation != 1 {
		t.Errorf("generation = %d, want 1", draft.Generation)
	}
	if draft.UpdatedAt != "2026-07-14T12:00:00Z" {
		t.Errorf("updated_at = %q, want RFC3339 of save time", draft.UpdatedAt)
	}
}

func TestMarkdownAnnotationDraftMissingRowIsEmptyGenZero(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)

	draft, err := s.GetMarkdownAnnotationDraft("/nowhere.md")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 0 {
		t.Errorf("missing row draft = %+v, want empty list gen 0", draft)
	}
}

func TestMarkdownAnnotationDraftGenerationMonotonic(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["v2"]`, 2, now); err != nil {
		t.Fatalf("save gen 2: %v", err)
	}
	// Same generation again -> stale.
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["dup"]`, 2, now); !errors.Is(err, ErrStaleMarkdownAnnotationSave) {
		t.Fatalf("save gen 2 twice = %v, want ErrStaleMarkdownAnnotationSave", err)
	}
	// Lower generation -> stale.
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["v1"]`, 1, now); !errors.Is(err, ErrStaleMarkdownAnnotationSave) {
		t.Fatalf("save gen 1 after 2 = %v, want ErrStaleMarkdownAnnotationSave", err)
	}
	// Stale saves must not have overwritten the list.
	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != `["v2"]` || draft.Generation != 2 {
		t.Errorf("draft after stale saves = %+v, want gen-2 list intact", draft)
	}
	// Higher generation proceeds.
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["v3"]`, 3, now); err != nil {
		t.Fatalf("save gen 3: %v", err)
	}
}

func TestMarkdownAnnotationDraftTombstoneRejectsStaleSave(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 5, now); err != nil {
		t.Fatalf("clear gen 5: %v", err)
	}
	// Save at the tombstone generation -> rejected (the debounced-save-after-
	// clear race).
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["ghost"]`, 5, now); !errors.Is(err, ErrStaleMarkdownAnnotationSave) {
		t.Fatalf("save gen 5 after clear(5) = %v, want ErrStaleMarkdownAnnotationSave", err)
	}
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["ghost"]`, 4, now); !errors.Is(err, ErrStaleMarkdownAnnotationSave) {
		t.Fatalf("save gen 4 after clear(5) = %v, want ErrStaleMarkdownAnnotationSave", err)
	}
	// A generation past the tombstone is accepted.
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["fresh"]`, 6, now); err != nil {
		t.Fatalf("save gen 6 after clear(5): %v", err)
	}
	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != `["fresh"]` || draft.Generation != 6 {
		t.Errorf("draft = %+v, want gen-6 fresh list", draft)
	}
}

func TestMarkdownAnnotationDraftClearSetsTombstoneAndEmptiesList(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["a"]`, 3, now); err != nil {
		t.Fatalf("save gen 3: %v", err)
	}
	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 4, now); err != nil {
		t.Fatalf("clear gen 4: %v", err)
	}
	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if draft.Annotations != "[]" {
		t.Errorf("annotations after clear = %q, want []", draft.Annotations)
	}
	// Floor must be >= tombstone so a re-mounting client seeds past it.
	if draft.Generation != 4 {
		t.Errorf("generation floor after clear = %d, want 4", draft.Generation)
	}
}

func TestMarkdownAnnotationDraftClearKeepsHighestTombstone(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	// Stored generation higher than the clear's generation param: tombstone
	// must take the max of both so the stored list can never resurrect.
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["a"]`, 7, now); err != nil {
		t.Fatalf("save gen 7: %v", err)
	}
	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 2, now); err != nil {
		t.Fatalf("clear gen 2: %v", err)
	}
	if err := s.SaveMarkdownAnnotationDraft(testMDPath, `["ghost"]`, 7, now); !errors.Is(err, ErrStaleMarkdownAnnotationSave) {
		t.Fatalf("save gen 7 after clear = %v, want ErrStaleMarkdownAnnotationSave", err)
	}
	// An earlier, higher tombstone survives a later, lower clear.
	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 1, now); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Generation != 7 {
		t.Errorf("floor = %d, want 7 (max of stored gen and clears)", draft.Generation)
	}
}

func TestMarkdownAnnotationDraftClearIdempotentAndOnMissingRow(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	// Clear on a missing row creates the tombstone row.
	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 3, now); err != nil {
		t.Fatalf("clear missing row: %v", err)
	}
	draft, err := s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 3 {
		t.Errorf("draft after clear-on-missing = %+v, want empty list floor 3", draft)
	}
	// Repeating the clear is a no-op, not an error.
	if err := s.ClearMarkdownAnnotationDraft(testMDPath, 3, now); err != nil {
		t.Fatalf("repeat clear: %v", err)
	}
	draft, err = s.GetMarkdownAnnotationDraft(testMDPath)
	if err != nil {
		t.Fatalf("get after repeat clear: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 3 {
		t.Errorf("draft after repeat clear = %+v, want unchanged", draft)
	}
}

func TestMarkdownAnnotationDraftPathsAreIndependent(t *testing.T) {
	s := newMarkdownAnnotationTestStore(t)
	now := time.Now()

	if err := s.SaveMarkdownAnnotationDraft("/a.md", `["a"]`, 1, now); err != nil {
		t.Fatalf("save /a.md: %v", err)
	}
	if err := s.ClearMarkdownAnnotationDraft("/b.md", 9, now); err != nil {
		t.Fatalf("clear /b.md: %v", err)
	}
	draft, err := s.GetMarkdownAnnotationDraft("/a.md")
	if err != nil {
		t.Fatalf("get /a.md: %v", err)
	}
	if draft.Annotations != `["a"]` || draft.Generation != 1 {
		t.Errorf("/a.md draft = %+v, want unaffected by /b.md tombstone", draft)
	}
}
