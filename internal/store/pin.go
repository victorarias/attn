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

// SetSessionContextWindowCap pins a per-session context-window cap in tokens,
// or clears the pin with cap 0. Like the queue pin, the column is owned by this
// setter alone — it is absent from the session upsert, so a respawn or state
// re-add cannot disturb it. The daemon's launch resolver reads the pin ahead of
// the chief and per-agent default settings.
//
// It returns true when a session was updated.
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

	result, err := s.db.Exec(`UPDATE sessions SET context_window_cap = ? WHERE id = ?`, cap, id)
	if err != nil {
		log.Printf("[store] SetSessionContextWindowCap: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}
