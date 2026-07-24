package store

import (
	"sort"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// FileActivitySourceOpened marks a file that was opened as a reader tile, by
// any route (⌘+click on a link, `attn open`, or the file opener itself).
const FileActivitySourceOpened = "opened"

// RecordFileActivity records that something happened to a file, incrementing
// the (path, source) counter and stamping the time. sessionID is the session
// the activity belongs to, or "" when there is none; the most recent one wins.
//
// Keying on (path, source) rather than path alone keeps future sources — an
// agent editing a file — accumulating independently, so a ranking change never
// needs a migration.
func (s *Store) RecordFileActivity(path, source, sessionID string) {
	if path == "" || source == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}

	var session any
	if sessionID != "" {
		session = sessionID
	}
	s.execLog(`
		INSERT INTO file_activity (path, source, session_id, last_at, count)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(path, source) DO UPDATE SET
			session_id = COALESCE(excluded.session_id, session_id),
			last_at = excluded.last_at,
			count = count + 1`,
		path, source, session, time.Now().Format(time.RFC3339))
}

// GetRecentFiles returns file activity ranked by frecency — the same
// frequency-weighted-by-recency scoring the location picker uses, so a file
// opened often keeps its slot after a burst of one-off opens.
//
// Rows are not stat'd here: a summon of the opener must not touch the disk
// once per remembered file. A file that has since disappeared is pruned when
// opening it fails.
func (s *Store) GetRecentFiles(limit int) []protocol.FileActivity {
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	// Read every row: ranking happens below, so pre-truncating by last_at
	// would hide old-but-frequent files. The table holds one row per file
	// ever opened, and dead entries are dropped on a failed open, so it
	// stays small.
	rows, err := s.db.Query(`SELECT path, source, session_id, last_at, count FROM file_activity`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var all []protocol.FileActivity
	for rows.Next() {
		var entry protocol.FileActivity
		var session *string
		if err := rows.Scan(&entry.Path, &entry.Source, &session, &entry.LastAt, &entry.Count); err != nil {
			continue
		}
		entry.SessionID = session
		all = append(all, entry)
	}

	now := time.Now()
	sort.Slice(all, func(i, j int) bool {
		si := frecencyScore(all[i].Count, all[i].LastAt, now)
		sj := frecencyScore(all[j].Count, all[j].LastAt, now)
		if si != sj {
			return si > sj
		}
		if all[i].LastAt != all[j].LastAt {
			return all[i].LastAt > all[j].LastAt
		}
		return all[i].Path < all[j].Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// DeleteFileActivity forgets every source for a path. Called when opening a
// remembered file fails because it no longer exists, so a dead entry costs one
// failed open rather than a slot forever.
func (s *Store) DeleteFileActivity(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	s.execLog("DELETE FROM file_activity WHERE path = ?", path)
}
