package store

import (
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// The stamp is the pin: its instant orders the pinned band, so re-pinning moves a session to
// the end of it. Pinning filters at read, so turn stamps keep accruing underneath.
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

	result, err := s.db.Exec(`UPDATE sessions SET pinned_at = ? WHERE id = ? AND closed_at = ''`, stamp, id)
	if err != nil {
		log.Printf("[store] SetSessionPinned: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// The column is owned by this setter alone — absent from the session upsert, so
// a respawn or state re-add cannot disturb it.
func (s *Store) SetSessionContextWindowCap(id string, cap int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cap < 0 {
		cap = 0
	}

	if s.db == nil {
		session, ok := s.sessions[id]
		if !ok {
			return false
		}
		if cap == 0 {
			session.ContextWindowCap = nil
		} else {
			session.ContextWindowCap = protocol.Ptr(cap)
		}
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET context_window_cap = ? WHERE id = ? AND closed_at = ''`, cap, id)
	if err != nil {
		log.Printf("[store] SetSessionContextWindowCap: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}
