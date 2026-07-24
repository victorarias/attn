package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newFileActivityStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedFileActivity writes a row directly so tests control last_at.
func seedFileActivity(t *testing.T, s *Store, path, source, lastAt string, count int) {
	t.Helper()
	_, err := s.db.Exec(
		"INSERT INTO file_activity (path, source, last_at, count) VALUES (?, ?, ?, ?)",
		path, source, lastAt, count,
	)
	if err != nil {
		t.Fatalf("failed to seed file activity: %v", err)
	}
}

func TestRecordFileActivityAccumulatesCount(t *testing.T) {
	s := newFileActivityStore(t)

	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "session-1")
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "session-2")

	files := s.GetRecentFiles(10)
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if files[0].Count != 2 {
		t.Errorf("count = %d, want 2", files[0].Count)
	}
	// The latest opener wins, so "which session was this for" tracks the most
	// recent open rather than the first one.
	if files[0].SessionID == nil || *files[0].SessionID != "session-2" {
		t.Errorf("session_id = %v, want session-2", files[0].SessionID)
	}
}

func TestRecordFileActivityKeepsSourcesSeparate(t *testing.T) {
	s := newFileActivityStore(t)

	// The key is (path, source): a future "edited" source must accumulate
	// beside "opened" instead of overwriting it.
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "")
	s.RecordFileActivity("/docs/plan.md", "edited", "")

	files := s.GetRecentFiles(10)
	if len(files) != 2 {
		t.Fatalf("files = %d, want one row per source", len(files))
	}
	for _, file := range files {
		if file.Count != 1 {
			t.Errorf("%s count = %d, want 1", file.Source, file.Count)
		}
	}
}

func TestGetRecentFilesRanksByFrecency(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()
	frequentOld := "/docs/frequent-old.md" // 10 opens 3 days ago: 10 * 0.5 = 5
	recentOnce := "/docs/recent-once.md"   // 1 open just now:      1 * 4   = 4
	staleOnce := "/docs/stale-once.md"     // 1 open a month ago:   1 * 0.25

	seedFileActivity(t, s, frequentOld, FileActivitySourceOpened, now.Add(-72*time.Hour).Format(time.RFC3339), 10)
	seedFileActivity(t, s, recentOnce, FileActivitySourceOpened, now.Format(time.RFC3339), 1)
	seedFileActivity(t, s, staleOnce, FileActivitySourceOpened, now.Add(-30*24*time.Hour).Format(time.RFC3339), 1)

	files := s.GetRecentFiles(10)
	want := []string{frequentOld, recentOnce, staleOnce}
	if len(files) != len(want) {
		t.Fatalf("files = %d, want %d", len(files), len(want))
	}
	for i, path := range want {
		if files[i].Path != path {
			t.Errorf("position %d = %s, want %s", i, files[i].Path, path)
		}
	}
}

func TestGetRecentFilesRanksBeforeTruncating(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()

	// An old but heavily opened document must beat a burst of fresher one-off
	// opens, so ranking has to run over the whole table rather than a
	// recency-truncated prefix of it.
	seedFileActivity(t, s, "/docs/frequent-old.md", FileActivitySourceOpened,
		now.Add(-30*24*time.Hour).Format(time.RFC3339), 100) // 100 * 0.25 = 25
	for i := range 250 {
		seedFileActivity(t, s, filepath.Join("/docs", "fresh", string(rune('a'+i%26))+string(rune('a'+i/26))+".md"),
			FileActivitySourceOpened, now.Format(time.RFC3339), 1) // 1 * 4 = 4
	}

	files := s.GetRecentFiles(5)
	if len(files) != 5 {
		t.Fatalf("files = %d, want 5", len(files))
	}
	if files[0].Path != "/docs/frequent-old.md" {
		t.Errorf("first = %s, want /docs/frequent-old.md", files[0].Path)
	}
}

func TestGetRecentFilesDoesNotStatMissingFiles(t *testing.T) {
	s := newFileActivityStore(t)

	// Recents are listed without touching the disk; a file that has since
	// disappeared stays in the list until opening it fails.
	s.RecordFileActivity("/nowhere/gone.md", FileActivitySourceOpened, "")
	if files := s.GetRecentFiles(10); len(files) != 1 {
		t.Fatalf("files = %d, want the missing file still listed", len(files))
	}

	s.DeleteFileActivity("/nowhere/gone.md")
	if files := s.GetRecentFiles(10); len(files) != 0 {
		t.Fatalf("files = %d, want the entry forgotten", len(files))
	}
}

func TestDeleteFileActivityForgetsEverySource(t *testing.T) {
	s := newFileActivityStore(t)
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "")
	s.RecordFileActivity("/docs/plan.md", "edited", "")
	s.RecordFileActivity("/docs/keep.md", FileActivitySourceOpened, "")

	s.DeleteFileActivity("/docs/plan.md")

	files := s.GetRecentFiles(10)
	if len(files) != 1 || files[0].Path != "/docs/keep.md" {
		t.Fatalf("files = %+v, want only /docs/keep.md", files)
	}
}
