package store

import (
	"testing"
	"time"
)

func TestWorktreeStore(t *testing.T) {
	store := New()
	defer store.Close()

	wt := &Worktree{
		Path:      "/projects/repo--feature",
		Branch:    "feature/auth",
		MainRepo:  "/projects/repo",
		CreatedAt: time.Now(),
	}

	store.AddWorktree(wt)

	got := store.GetWorktree(wt.Path)
	if got == nil {
		t.Fatal("expected worktree, got nil")
	}
	if got.Branch != wt.Branch {
		t.Errorf("expected branch %s, got %s", wt.Branch, got.Branch)
	}

	list := store.ListWorktreesByRepo(wt.MainRepo)
	if len(list) != 1 {
		t.Errorf("expected 1 worktree, got %d", len(list))
	}

	store.RemoveWorktree(wt.Path)
	got = store.GetWorktree(wt.Path)
	if got != nil {
		t.Error("expected nil after remove")
	}
}

// Adoption is a refresh: a second AddWorktree for a path the registry already has
// updates it in place instead of losing the pin and the observed state with it.
func TestAddWorktreeIsAnUpsertThatKeepsThePinAndTheObservation(t *testing.T) {
	store := New()
	defer store.Close()
	now := time.Now()
	path := "/projects/repo--feature"

	store.AddWorktree(&Worktree{
		Path: path, Branch: "feature/auth", MainRepo: "/projects/repo",
		CreatedAt: now.Add(-time.Hour), Origin: WorktreeOriginAttn,
	})
	store.UpdateWorktreeObservation(path, WorktreeObservation{
		Branch: "feature/auth", HeadSHA: "abc123", Dirty: true, DirtyFiles: 2,
		MergedSignal: MergedSignalTree, LastActivityAt: now.Add(-24 * time.Hour),
	}, now)
	if !store.SetWorktreePin(path, true, now) {
		t.Fatal("pinning found no row")
	}

	store.AddWorktree(&Worktree{
		Path: path, Branch: "feature/renamed", MainRepo: "/projects/repo",
		CreatedAt: now, Origin: WorktreeOriginGit,
	})

	got := store.GetWorktree(path)
	if got == nil {
		t.Fatal("the upsert dropped the row")
	}
	if got.Branch != "feature/renamed" {
		t.Errorf("branch = %q, want the freshly listed one", got.Branch)
	}
	if !got.Pinned() {
		t.Error("the upsert dropped the keep pin")
	}
	if got.Origin != WorktreeOriginAttn {
		t.Errorf("origin = %q, want the one recorded at creation", got.Origin)
	}
	if got.MergedSignal != MergedSignalTree || got.DirtyFiles != 2 {
		t.Errorf("the upsert dropped the observation: %+v", got)
	}
}

// The log outlives the rows, so it is the only place a removal can be inspected;
// it pages newest first and says how much it did not show.
func TestWorktreeSweepLogPagesNewestFirst(t *testing.T) {
	store := New()
	defer store.Close()
	now := time.Now()

	for i := range 5 {
		store.AppendWorktreeSweepLog(WorktreeSweepLogEntry{
			Path: "/projects/repo--gone", MainRepo: "/projects/repo",
			Branch: "feat/gone", Action: "removed", Reason: "merged and clean",
		}, now.Add(time.Duration(i)*time.Minute))
	}
	store.AppendWorktreeSweepLog(WorktreeSweepLogEntry{
		Path: "/other--gone", MainRepo: "/other", Action: "removed",
	}, now)

	entries, omitted := store.WorktreeSweepLog("/projects/repo", 3)
	if len(entries) != 3 || omitted != 2 {
		t.Fatalf("page = %d entries, %d omitted; want 3 and 2", len(entries), omitted)
	}
	if entries[0].At < entries[1].At {
		t.Errorf("entries are not newest first: %s then %s", entries[0].At, entries[1].At)
	}
	for _, entry := range entries {
		if entry.MainRepo != "/projects/repo" {
			t.Errorf("the repository filter let %s through", entry.MainRepo)
		}
	}

	all, omitted := store.WorktreeSweepLog("", 100)
	if len(all) != 6 || omitted != 0 {
		t.Fatalf("unfiltered = %d entries, %d omitted; want 6 and 0", len(all), omitted)
	}
}

// The merged record is repository-scoped on purpose: it has to outlive every
// session that opened the pull request.
func TestRepoMergedBranchesReplaceTheRecordedSet(t *testing.T) {
	store := New()
	defer store.Close()
	now := time.Now()

	store.RecordRepoMergedBranches("/projects/repo", []MergedBranch{
		{Branch: "feat/one", Number: 1, HeadSHA: "aaa"},
		{Branch: "feat/two", Number: 2, HeadSHA: "bbb"},
	}, now)
	store.RecordRepoMergedBranches("/projects/repo", []MergedBranch{
		{Branch: "feat/two", Number: 2, HeadSHA: "bbb"},
		{Branch: "feat/three", Number: 3, HeadSHA: "ccc"},
	}, now.Add(time.Minute))

	merged := store.RepoMergedBranches("/projects/repo")
	if len(merged) != 3 {
		t.Fatalf("recorded %d branches, want all three kept: %+v", len(merged), merged)
	}
	if merged["feat/three"].HeadSHA != "ccc" {
		t.Errorf("feat/three = %+v", merged["feat/three"])
	}
}

func TestRepoIntegrationBranchRoundTrips(t *testing.T) {
	store := New()
	defer store.Close()
	now := time.Now()

	if record := store.RepoIntegrationBranch("/projects/repo"); record != nil {
		t.Fatalf("an unresolved repository answered %+v", record)
	}
	store.SetRepoIntegrationBranch("/projects/repo", "next", "pull_requests", now)
	record := store.RepoIntegrationBranch("/projects/repo")
	if record == nil || record.Branch != "next" || record.Source != "pull_requests" {
		t.Fatalf("record = %+v", record)
	}

	store.SetRepoIntegrationBranch("/projects/repo", "main", "origin_head", now.Add(time.Hour))
	if record := store.RepoIntegrationBranch("/projects/repo"); record.Branch != "main" {
		t.Fatalf("re-resolution did not replace the record: %+v", record)
	}
}
