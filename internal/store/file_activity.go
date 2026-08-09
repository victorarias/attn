package store

import (
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// FileActivitySourceOpened marks a file opened as a reader tile, by any route.
const FileActivitySourceOpened = "opened"

// FileActivitySourceEdited marks a file an agent wrote, reported by the
// tool-use hook; a weaker intent signal than an open, so it weighs less.
const FileActivitySourceEdited = "edited"

// Ranking weights: an open is the baseline, an edit weighs less, an
// in-workspace file beats an equally-scored one without hiding it.
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

// RecordFileActivity increments the (path, source) counter and stamps the
// time; sessionID is "" when there is none, and the most recent one wins.
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

// GetRecentFiles returns one entry per file, ranked by frecency; a file's
// sources add (weighted by sourceWeight) and the merged entry keeps the newest
// timestamp, total count, and most recent source. Rows are never stat'd here —
// dead files are pruned when opening them fails.
func (s *Store) GetRecentFiles(limit int, root string) []protocol.FileActivity {
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	// Read every row: pre-truncating by last_at would hide old-but-frequent
	// files.
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
		// Most recent activity names the entry; timestamps are second-granular,
		// so an open wins a same-second tie with an edit.
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

// DeleteFileActivity forgets every source for a path; called when opening a
// remembered file fails because it no longer exists.
func (s *Store) DeleteFileActivity(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	s.execLog("DELETE FROM file_activity WHERE path = ?", path)
}
