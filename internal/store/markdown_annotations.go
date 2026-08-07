package store

import (
	"time"
)

// ErrStaleMarkdownAnnotationSave is returned by SaveMarkdownAnnotationDraft
// when the incoming generation is not strictly greater than both the stored
// generation and the tombstone generation. It marks the save as benignly
// stale (the client should drop its pending list and re-hydrate), not an
// operational failure.
//
// The same value as ErrStaleAnnotationSave: both draft kinds share one
// ordering rule, so `errors.Is` against either name matches.
var ErrStaleMarkdownAnnotationSave = ErrStaleAnnotationSave

// MarkdownAnnotationDraft is the persisted annotation draft for one markdown
// file, keyed by absolute path (drafts are a property of the document, not of
// any workspace or tile — the same file open anywhere shows the same drafts).
type MarkdownAnnotationDraft struct {
	Path        string
	Annotations string // raw JSON array of protocol.MarkdownAnnotation
	Generation  int    // current generation floor: max(generation, tombstone)
	UpdatedAt   string
}

// GetMarkdownAnnotationDraft returns the draft for path. Annotations may be
// "[]" (after a clear). Generation is the current generation floor —
// max(stored generation, tombstone_generation) — so a re-mounting client
// seeds its counter past a tombstone even when the draft is empty. A missing
// row yields an empty draft with generation 0.
func (s *Store) GetMarkdownAnnotationDraft(path string) (*MarkdownAnnotationDraft, error) {
	draft, err := markdownDraftTable.get(s, path)
	if err != nil {
		return nil, err
	}
	return &MarkdownAnnotationDraft{
		Path:        path,
		Annotations: draft.Annotations,
		Generation:  draft.Generation,
		UpdatedAt:   draft.UpdatedAt,
	}, nil
}

// SaveMarkdownAnnotationDraft upserts the full annotation list for path.
// The save is rejected with ErrStaleMarkdownAnnotationSave unless generation
// is strictly greater than both the stored generation (monotonicity) and the
// tombstone generation (a debounced save that fires after a clear must not
// resurrect ghost drafts).
func (s *Store) SaveMarkdownAnnotationDraft(path, annotationsJSON string, generation int, now time.Time) error {
	return markdownDraftTable.save(s, path, annotationsJSON, "", generation, now)
}

// ClearMarkdownAnnotationDraft tombstones the draft for path: the list is
// emptied and tombstone_generation becomes max(existing tombstone, stored
// generation, generation). Any later save carrying generation <= tombstone is
// rejected. Idempotent, and works on a missing row (the tombstone IS the
// row). This is the primitive PR6's clear-on-send calls; the sidebar
// "clear all" uses it today.
func (s *Store) ClearMarkdownAnnotationDraft(path string, generation int, now time.Time) error {
	return markdownDraftTable.clear(s, path, generation, now)
}
