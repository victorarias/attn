package store

import (
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// FileActivitySourceOpened marks a file that was opened as a reader tile, by
// any route (⌘+click on a link, `attn open`, or the file opener itself).
const FileActivitySourceOpened = "opened"

// FileActivitySourceEdited marks a file an agent wrote, reported by the
// tool-use hook. An edit is a weaker signal of intent than an open — the agent
// chose the file, the user did not — so it carries less weight in the ranking,
// but it still introduces a file the user has never opened. That is the case
// the signal exists for: the plan the agent just wrote is one ⌘P away.
const FileActivitySourceEdited = "edited"

// Ranking weights. An open is the baseline; an edit is worth less than an open
// of the same age and frequency, and a file inside the caller's workspace beats
// an equally-scored file from another one without hiding it.
const (
	editedWeight     = 0.6
	inWorkspaceBonus = 1.5
)

func sourceWeight(source string) float64 {
	if source == FileActivitySourceEdited {
		return editedWeight
	}
	return 1
}

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

// GetRecentFiles returns one entry per file, ranked by frecency — the same
// frequency-weighted-by-recency scoring the location picker uses, so a file
// opened often keeps its slot after a burst of one-off opens.
//
// A file can carry several sources (opened and edited); their scores add, with
// each source weighted by sourceWeight, and the returned entry merges them:
// the newest timestamp, the total count, and the source that was most recent.
// Files under root — the caller's workspace — get inWorkspaceBonus, so the
// project in front of you sorts first without other projects disappearing.
//
// Rows are not stat'd here: a summon of the opener must not touch the disk
// once per remembered file. A file that has since disappeared is pruned when
// opening it fails.
func (s *Store) GetRecentFiles(limit int, root string) []protocol.FileActivity {
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

	now := time.Now()
	prefix := workspacePrefix(root)
	merged := map[string]*protocol.FileActivity{}
	scores := map[string]float64{}
	var order []string

	for rows.Next() {
		var entry protocol.FileActivity
		var session *string
		if err := rows.Scan(&entry.Path, &entry.Source, &session, &entry.LastAt, &entry.Count); err != nil {
			continue
		}
		entry.SessionID = session

		scores[entry.Path] += frecencyScore(entry.Count, entry.LastAt, now) * sourceWeight(entry.Source)
		existing, ok := merged[entry.Path]
		if !ok {
			copied := entry
			merged[entry.Path] = &copied
			order = append(order, entry.Path)
			continue
		}
		existing.Count += entry.Count
		// The most recent activity names the entry: its timestamp, its source,
		// and the session that produced it. Timestamps are second-granular, so
		// an open and an edit can land on the same one; the user's own action
		// wins that tie.
		sameSecondOpen := entry.LastAt == existing.LastAt && entry.Source == FileActivitySourceOpened
		if entry.LastAt > existing.LastAt || sameSecondOpen {
			existing.LastAt = entry.LastAt
			existing.Source = entry.Source
			existing.SessionID = entry.SessionID
		}
	}

	for path := range scores {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			scores[path] *= inWorkspaceBonus
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		li, lj := merged[order[i]], merged[order[j]]
		if scores[li.Path] != scores[lj.Path] {
			return scores[li.Path] > scores[lj.Path]
		}
		if li.LastAt != lj.LastAt {
			return li.LastAt > lj.LastAt
		}
		return li.Path < lj.Path
	})

	if len(order) > limit {
		order = order[:limit]
	}
	all := make([]protocol.FileActivity, 0, len(order))
	for _, path := range order {
		all = append(all, *merged[path])
	}
	return all
}

// workspacePrefix normalizes a root into a prefix that only matches paths
// inside it, so /repo never claims /repo-other.
func workspacePrefix(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "/" {
		return ""
	}
	return strings.TrimSuffix(root, "/") + "/"
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
