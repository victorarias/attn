package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newRecentLocationsStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedRecentLocation inserts a row directly so tests control last_seen and
// can model rows written before upsert-time worktree resolution existed.
func seedRecentLocation(t *testing.T, s *Store, path, lastSeen string, useCount int) {
	t.Helper()
	_, err := s.db.Exec(
		"INSERT INTO recent_locations (path, last_seen, use_count) VALUES (?, ?, ?)",
		path, lastSeen, useCount,
	)
	if err != nil {
		t.Fatalf("failed to seed recent location: %v", err)
	}
}

func TestUpsertRecentLocationAccumulatesUseCount(t *testing.T) {
	s := newRecentLocationsStore(t)
	dir := t.TempDir()

	s.UpsertRecentLocation(dir)
	s.UpsertRecentLocation(dir)

	locs := s.GetRecentLocations(10)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].Path != dir {
		t.Errorf("expected path %s, got %s", dir, locs[0].Path)
	}
	if locs[0].UseCount != 2 {
		t.Errorf("expected use_count 2, got %d", locs[0].UseCount)
	}
}

func TestGetRecentLocationsRanksByFrecency(t *testing.T) {
	s := newRecentLocationsStore(t)
	now := time.Now()
	frequentOld := t.TempDir() // 10 uses 3 days ago: 10 * 0.5 = 5
	recentOnce := t.TempDir()  // 1 use just now:      1 * 4   = 4
	staleOnce := t.TempDir()   // 1 use a month ago:   1 * 0.25

	seedRecentLocation(t, s, frequentOld, now.Add(-72*time.Hour).Format(time.RFC3339), 10)
	seedRecentLocation(t, s, recentOnce, now.Format(time.RFC3339), 1)
	seedRecentLocation(t, s, staleOnce, now.Add(-30*24*time.Hour).Format(time.RFC3339), 1)

	locs := s.GetRecentLocations(10)
	if len(locs) != 3 {
		t.Fatalf("expected 3 locations, got %d", len(locs))
	}
	// Pure last_seen ordering would put recentOnce first; frecency keeps the
	// heavily-used project on top.
	want := []string{frequentOld, recentOnce, staleOnce}
	for i, path := range want {
		if locs[i].Path != path {
			t.Errorf("position %d: expected %s, got %s", i, path, locs[i].Path)
		}
	}
}

func TestRecentLocationsCollapseWorktreesIntoMainRepo(t *testing.T) {
	s := newRecentLocationsStore(t)
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo--feat")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "feat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitFile := "gitdir: " + filepath.Join(mainRepo, ".git", "worktrees", "feat") + "\n"
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(gitFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Upserts record the main repo even when the session lives in the
	// worktree or below it.
	s.UpsertRecentLocation(worktree)
	s.UpsertRecentLocation(filepath.Join(worktree, "sub"))

	locs := s.GetRecentLocations(10)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d: %+v", len(locs), locs)
	}
	if locs[0].Path != mainRepo {
		t.Errorf("expected path %s, got %s", mainRepo, locs[0].Path)
	}
	if locs[0].UseCount != 2 {
		t.Errorf("expected use_count 2, got %d", locs[0].UseCount)
	}

	// Rows recorded before upsert-time resolution merge at read time.
	seedRecentLocation(t, s, worktree, time.Now().Format(time.RFC3339), 3)
	locs = s.GetRecentLocations(10)
	if len(locs) != 1 {
		t.Fatalf("expected legacy worktree row to merge, got %d locations", len(locs))
	}
	if locs[0].Path != mainRepo {
		t.Errorf("expected path %s, got %s", mainRepo, locs[0].Path)
	}
	if locs[0].UseCount != 5 {
		t.Errorf("expected merged use_count 5, got %d", locs[0].UseCount)
	}
}

func TestRecentLocationsKeepMainRepoSubdirectories(t *testing.T) {
	s := newRecentLocationsStore(t)
	mainRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(mainRepo, "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	s.UpsertRecentLocation(sub)

	locs := s.GetRecentLocations(10)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].Path != sub {
		t.Errorf("subdirectory of a main repo should not collapse: expected %s, got %s", sub, locs[0].Path)
	}
}
