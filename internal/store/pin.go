package store

import (
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// SetSessionPinned pins one session out of the queue, or releases it.
//
// The stamp is the pin: a non-empty pinned_at means pinned, and the instant it
// carries is the pinned band's order, so re-pinning a session moves it to the
// end of the band rather than back to wherever it used to sit.
//
// Pinning filters at read, exactly like a pinned workspace: the turn stamps go
// on accruing underneath, so releasing a pin surfaces whatever was outstanding
// at its true age instead of restarting it from nothing. That is the whole
// reason the pin is a separate column rather than a settle.
//
// It returns true when a session was updated.
func (s *Store) SetSessionPinned(id string, pinned bool, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := ""
	if pinned {
		stamp = now.UTC().Format(time.RFC3339Nano)
	}

	if s.db == nil {
		session, ok := s.sessions[id]
		if !ok {
			return false
		}
		if stamp == "" {
			session.PinnedAt = nil
		} else {
			session.PinnedAt = protocol.Ptr(stamp)
		}
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET pinned_at = ? WHERE id = ?`, stamp, id)
	if err != nil {
		log.Printf("[store] SetSessionPinned: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}
