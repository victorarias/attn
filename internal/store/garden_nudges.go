package store

import (
	"database/sql"
	"fmt"
	"time"
)

// GardenSeedWatch is one session's explicit standing interest in a seed.
type GardenSeedWatch struct {
	WatcherSessionID string
	SeedID           string
}

// SetGardenSeedWatch makes the explicit watch exactly as requested and reports
// whether the stored set changed.
func (s *Store) SetGardenSeedWatch(watcherSessionID, seedID string, watching bool, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		res sql.Result
		err error
	)
	if watching {
		res, err = s.db.Exec(`
			INSERT OR IGNORE INTO garden_seed_watches(watcher_session_id, seed_id, created_at)
			VALUES (?, ?, ?)
		`, watcherSessionID, seedID, now.UTC().Format(sortableTimeFormat))
	} else {
		res, err = s.db.Exec(`DELETE FROM garden_seed_watches WHERE watcher_session_id = ? AND seed_id = ?`,
			watcherSessionID, seedID)
	}
	if err != nil {
		return false, fmt.Errorf("set garden seed watch: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// GardenSeedWatching reports whether this session explicitly watches the seed.
func (s *Store) GardenSeedWatching(watcherSessionID, seedID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var watching bool
	if err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM garden_seed_watches WHERE watcher_session_id = ? AND seed_id = ?)
	`, watcherSessionID, seedID).Scan(&watching); err != nil {
		return false, fmt.Errorf("read garden seed watch: %w", err)
	}
	return watching, nil
}

// GardenSeedWatches lists the explicit subscriptions. Ringing is event-driven,
// so this scan costs nothing while the garden is quiet.
func (s *Store) GardenSeedWatches() ([]GardenSeedWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT watcher_session_id, seed_id FROM garden_seed_watches`)
	if err != nil {
		return nil, fmt.Errorf("list garden seed watches: %w", err)
	}
	defer rows.Close()
	var watches []GardenSeedWatch
	for rows.Next() {
		var watch GardenSeedWatch
		if err := rows.Scan(&watch.WatcherSessionID, &watch.SeedID); err != nil {
			return nil, fmt.Errorf("scan garden seed watch: %w", err)
		}
		watches = append(watches, watch)
	}
	return watches, rows.Err()
}

// ClaimGardenSeedBell atomically claims the unread fence and queues the
// senderless message that delivers it. An existing fence coalesces the event.
func (s *Store) ClaimGardenSeedBell(watcherSessionID, seedID, eventKind string, message AgentMessage, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO garden_seed_bells(watcher_session_id, seed_id, event_kind, message_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, watcherSessionID, seedID, eventKind, message.ID, now.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, fmt.Errorf("claim garden seed bell: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`
		INSERT INTO agent_messages (id, sender_session_id, target_session_id, content, created_at)
		VALUES (?, '', ?, ?, ?)
	`, message.ID, watcherSessionID, message.Content, message.CreatedAt); err != nil {
		return false, fmt.Errorf("queue garden seed bell: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ConsumeGardenSeedBell marks the seed read for this watcher. If its doorbell
// is still queued, the same transaction removes it so a read cannot be followed
// by stale advice to read again.
func (s *Store) ConsumeGardenSeedBell(watcherSessionID, seedID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var messageID string
	if err := tx.QueryRow(`
		SELECT message_id FROM garden_seed_bells WHERE watcher_session_id = ? AND seed_id = ?
	`, watcherSessionID, seedID).Scan(&messageID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("read garden seed bell: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM agent_messages WHERE id = ? AND delivered_at = ''`, messageID); err != nil {
		return false, fmt.Errorf("cancel queued garden seed bell: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM garden_seed_bells WHERE watcher_session_id = ? AND seed_id = ?`,
		watcherSessionID, seedID); err != nil {
		return false, fmt.Errorf("consume garden seed bell: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
