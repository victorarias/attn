package store

import (
	"time"
)

// SessionAnnotationDraft is the persisted set of terminal annotations for one
// session: the marks a user has made on that session's assistant messages and
// not yet sent. Keyed by session, because that is the thing the annotations are
// about — they outlive the pane that drew them and the app that was running.
type SessionAnnotationDraft struct {
	SessionID   string
	Annotations string // raw JSON array of protocol.SessionAnnotation
	// What the user wants to say about the turn as a whole, beside the marks
	// on its parts. Sent in front of them and cleared with them.
	Note       string
	Generation int // current generation floor: max(generation, tombstone)
	UpdatedAt  string
}

// GetSessionAnnotationDraft returns the draft for a session. A session that was
// never annotated yields an empty draft at generation 0. Generation is the
// floor including any tombstone, so a client that re-mounts after sending its
// annotations seeds its counter past the clear.
func (s *Store) GetSessionAnnotationDraft(sessionID string) (*SessionAnnotationDraft, error) {
	draft, err := sessionDraftTable.get(s, sessionID)
	if err != nil {
		return nil, err
	}
	return &SessionAnnotationDraft{
		SessionID:   sessionID,
		Annotations: draft.Annotations,
		Note:        draft.Note,
		Generation:  draft.Generation,
		UpdatedAt:   draft.UpdatedAt,
	}, nil
}

// SaveSessionAnnotationDraft upserts the full annotation list and note for a
// session, rejecting a generation that does not clear the stored one and the
// tombstone with ErrStaleAnnotationSave.
func (s *Store) SaveSessionAnnotationDraft(sessionID, annotationsJSON, note string, generation int, now time.Time) error {
	return sessionDraftTable.save(s, sessionID, annotationsJSON, note, generation, now)
}

// ClearSessionAnnotationDraft tombstones a session's annotations — what
// sending them to the agent does. Idempotent, and works on a session that
// never had a row.
func (s *Store) ClearSessionAnnotationDraft(sessionID string, generation int, now time.Time) error {
	return sessionDraftTable.clear(s, sessionID, generation, now)
}

// DeleteSessionAnnotationDraft drops a session's draft outright. Called when
// the session itself is removed: a tombstone would leave a row keyed by an id
// that can never come back.
func (s *Store) DeleteSessionAnnotationDraft(sessionID string) error {
	return sessionDraftTable.delete(s, sessionID)
}
