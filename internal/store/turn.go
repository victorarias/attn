package store

import (
	"database/sql"
	"log"
	"time"
)

// TurnStamps is the pair that decides whether a session owes the user a turn:
// it does iff OpenedAt is after SettledAt. Both are zero for a session that has
// never opened a turn.
//
// SnoozedUntil rides along rather than deciding anything, because a snooze
// always settles the open turn as it is written — so a snoozed session already
// reads as owing nothing, and the deadline is only there to say when it comes
// back and to keep the next turn from opening before then.
type TurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
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
		current.SettledAt = now.UTC()
		s.setTurnStampsLocked(id, current)
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

// SnoozeTurn defers a session until `until`: it closes whatever turn is open and
// records the deadline in the same write.
//
// The two halves are one statement on purpose. A snooze that stamped the
// deadline without settling would leave a turn open *and* suppressed — a row in
// the queue that no state change can ever move — and the window in which that is
// true is exactly the window a broadcast can land in.
//
// A deadline already in the past is stored as given rather than rejected. The
// daemon's wake path fires immediately on it, which is what makes a snooze that
// lapsed while the daemon was down behave like one that lapsed while it was up.
func (s *Store) SnoozeTurn(id string, until, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		current.SettledAt = now.UTC()
		current.SnoozedUntil = until.UTC()
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(
		`UPDATE sessions SET turn_settled_at = ?, turn_snoozed_until = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		log.Printf("[store] SnoozeTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// WakeTurn clears a session's snooze without opening anything. Whether a turn
// then opens is the daemon's call — it depends on the state the agent is in at
// that instant — so this stays the narrow write.
//
// It reports whether a snooze was actually cleared, which is what lets the
// caller stay quiet about a wake for a session that was not snoozed.
func (s *Store) WakeTurn(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		current, ok := s.turnStamps[id]
		if !ok || current.SnoozedUntil.IsZero() {
			return false
		}
		current.SnoozedUntil = time.Time{}
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(
		`UPDATE sessions SET turn_snoozed_until = '' WHERE id = ? AND turn_snoozed_until != ''`, id)
	if err != nil {
		log.Printf("[store] WakeTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// SnoozedSessions is every live snooze, by session id. The daemon reads it once
// at start-up to rebuild its wake timers, which is the only thing that makes a
// snooze survive a restart — the timers themselves are in memory.
func (s *Store) SnoozedSessions() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snoozed := make(map[string]time.Time)

	if s.db == nil {
		for id, stamps := range s.turnStamps {
			if !stamps.SnoozedUntil.IsZero() {
				snoozed[id] = stamps.SnoozedUntil
			}
		}
		return snoozed
	}

	rows, err := s.db.Query(`SELECT id, turn_snoozed_until FROM sessions WHERE turn_snoozed_until != ''`)
	if err != nil {
		log.Printf("[store] SnoozedSessions: failed: %v", err)
		return snoozed
	}
	defer rows.Close()
	for rows.Next() {
		var id, until string
		if err := rows.Scan(&id, &until); err != nil {
			log.Printf("[store] SnoozedSessions: scan failed: %v", err)
			continue
		}
		if parsed := parseTurnStamp(until); !parsed.IsZero() {
			snoozed[id] = parsed
		}
	}
	return snoozed
}

// TurnStamps reads one session's stamps. Zero values mean no turn has ever
// opened, which reads as owing nothing.
func (s *Store) TurnStamps(id string) TurnStamps {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return s.turnStamps[id]
	}

	var opened, settled, snoozed string
	err := s.db.QueryRow(
		`SELECT turn_opened_at, turn_settled_at, turn_snoozed_until FROM sessions WHERE id = ?`, id).
		Scan(&opened, &settled, &snoozed)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] TurnStamps: failed for session %s: %v", id, err)
		}
		return TurnStamps{}
	}
	return TurnStamps{
		OpenedAt:     parseTurnStamp(opened),
		SettledAt:    parseTurnStamp(settled),
		SnoozedUntil: parseTurnStamp(snoozed),
	}
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
