package store

import (
	"database/sql"
	"log"
	"time"
)

// TurnStamps decides whether a session owes the user a turn: it does iff
// OpenedAt is after SettledAt; both zero means no turn ever opened.
// SnoozedUntil decides nothing — a snooze settles the open turn as it is
// written, so the deadline only says when the session comes back.
type TurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
}

// OpenTurnIfClosed stamps the start of a turn only when no turn is already
// open, returning true when one was opened; an open turn keeps its age.
// Trap: the guard is a TEXT comparison, so the stored encoding must sort in
// time order within a second — sortableTimeFormat, not RFC3339Nano, whose
// stripped fractions sorted a settle below its same-second open and silently
// kept the turn open. Migration 95 rewrote the stored stamps.
func (s *Store) OpenTurnIfClosed(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := now.UTC().Format(sortableTimeFormat)

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

// SettleTurn closes whatever turn is open, unconditionally; settling a session
// that owes nothing is still recorded.
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
		now.UTC().Format(sortableTimeFormat), id)
	if err != nil {
		log.Printf("[store] SettleTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// SnoozeTurn defers a session until `until`, settling the open turn and
// recording the deadline in ONE statement — split, a broadcast could observe a
// turn both open and suppressed. A past deadline is stored as given; the wake
// path fires immediately on it.
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
		now.UTC().Format(sortableTimeFormat), until.UTC().Format(sortableTimeFormat), id)
	if err != nil {
		log.Printf("[store] SnoozeTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// WakeTurn clears a session's snooze without opening anything — whether a turn
// then opens is the daemon's call. Reports whether a snooze was cleared.
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

// WakeTurnAt clears a snooze only if the stored deadline is exactly the one
// given — a fired timer uses this instead of WakeTurn, so a snooze re-written
// while the timer was waking is not clobbered. Reports whether the snooze it
// was told to end was still the live one.
func (s *Store) WakeTurnAt(id string, deadline time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := deadline.UTC().Format(sortableTimeFormat)

	if s.db == nil {
		current, ok := s.turnStamps[id]
		if !ok || current.SnoozedUntil.IsZero() {
			return false
		}
		if current.SnoozedUntil.UTC().Format(sortableTimeFormat) != stamp {
			return false
		}
		current.SnoozedUntil = time.Time{}
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(
		`UPDATE sessions SET turn_snoozed_until = '' WHERE id = ? AND turn_snoozed_until = ?`, id, stamp)
	if err != nil {
		log.Printf("[store] WakeTurnAt: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// SnoozedSessions is every live snooze, by session id; the daemon reads it at
// start-up to rebuild its in-memory wake timers.
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

// TurnStamps reads one session's stamps; zero values mean no turn ever opened.
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

// parseTurnStamp decodes any RFC3339 spelling (pre-migration-95 stamps
// included), yielding zero time for ” and anything unreadable.
func parseTurnStamp(value string) time.Time { return parseStoreTime(value) }
