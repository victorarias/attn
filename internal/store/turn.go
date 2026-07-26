package store

import (
	"database/sql"
	"log"
	"time"
)

// TurnStamps is the pair that decides whether a session owes the user a turn:
// it does iff OpenedAt is after SettledAt. Both are zero for a session that has
// never opened a turn.
type TurnStamps struct {
	OpenedAt  time.Time
	SettledAt time.Time
}

// OpenTurnIfClosed stamps the start of a turn, but only when no turn is already
// open. Leaving an open turn alone is the whole state machine: a turn keeps the
// age it was opened at, so a row never moves in the queue while the user works
// with the agent, and a re-reported state cannot disturb it.
//
// It returns true when a turn was opened.
func (s *Store) OpenTurnIfClosed(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := now.UTC().Format(time.RFC3339Nano)

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		if current.OpenedAt.After(current.SettledAt) {
			return false
		}
		s.setTurnStampsLocked(id, TurnStamps{OpenedAt: now.UTC(), SettledAt: current.SettledAt})
		return true
	}

	// The condition lives in SQL so opening is atomic rather than
	// read-modify-write.
	result, err := s.db.Exec(`
		UPDATE sessions SET turn_opened_at = ?
		 WHERE id = ? AND (turn_opened_at = '' OR turn_opened_at <= turn_settled_at)`,
		stamp, id)
	if err != nil {
		log.Printf("[store] OpenTurnIfClosed: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// SettleTurn closes whatever turn is open, unconditionally. Settling a session
// that owes nothing is a no-op in effect but still recorded, so a later turn
// opens against a stamp that is at least this recent.
func (s *Store) SettleTurn(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		s.setTurnStampsLocked(id, TurnStamps{OpenedAt: current.OpenedAt, SettledAt: now.UTC()})
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET turn_settled_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		log.Printf("[store] SettleTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// TurnStamps reads one session's stamps. Zero values mean no turn has ever
// opened, which reads as owing nothing.
func (s *Store) TurnStamps(id string) TurnStamps {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return s.turnStamps[id]
	}

	var opened, settled string
	err := s.db.QueryRow(`SELECT turn_opened_at, turn_settled_at FROM sessions WHERE id = ?`, id).
		Scan(&opened, &settled)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] TurnStamps: failed for session %s: %v", id, err)
		}
		return TurnStamps{}
	}
	return TurnStamps{OpenedAt: parseTurnStamp(opened), SettledAt: parseTurnStamp(settled)}
}

func (s *Store) setTurnStampsLocked(id string, stamps TurnStamps) {
	if s.turnStamps == nil {
		s.turnStamps = make(map[string]TurnStamps)
	}
	s.turnStamps[id] = stamps
}

func parseTurnStamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
