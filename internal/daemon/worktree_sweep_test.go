package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type sweepRepo struct {
	t    *testing.T
	root string
	main string
}

func newSweepRepo(t *testing.T) *sweepRepo {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, root, "init", "--bare", "origin.git")
	runGitDaemon(t, main, "init", "-b", "main")
	runGitDaemon(t, main, "remote", "add", "origin", origin)

	repo := &sweepRepo{t: t, root: root, main: git.CanonicalizePath(main)}
	repo.commitOnMain("seed", "seed\n", "seed")
	runGitDaemon(t, main, "push", "-u", "origin", "main")
	return repo
}

// The message is a parameter because two commits with the same tree, parent,
// author and second hash identically, and some cases need them distinct.
func (r *sweepRepo) commitIn(dir, file, content, message string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
	runGitDaemon(r.t, dir, "add", file)
	runGitDaemon(r.t, dir, "commit", "-m", message)
	return strings.TrimSpace(gitOutput(r.t, dir, "rev-parse", "HEAD"))
}

func (r *sweepRepo) commitOnMain(file, content, message string) string {
	return r.commitIn(filepath.Join(r.root, "main"), file, content, message)
}

func (r *sweepRepo) worktree(name, branch, from string) string {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	runGitDaemon(r.t, filepath.Join(r.root, "main"), "worktree", "add", "-b", branch, path, from)
	return git.CanonicalizePath(path)
}

func (r *sweepRepo) pushMain() {
	r.t.Helper()
	runGitDaemon(r.t, filepath.Join(r.root, "main"), "push", "origin", "main")
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}

func sweepDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
}

func rowFor(t *testing.T, d *Daemon, path string) *store.Worktree {
	t.Helper()
	wt := d.store.GetWorktree(path)
	if wt == nil {
		t.Fatalf("no registry row for %s", path)
	}
	return wt
}

func verdictFor(t *testing.T, d *Daemon, repo, path string, idle time.Duration, now time.Time) sweepVerdict {
	t.Helper()
	return worktreeSweepVerdict(rowFor(t, d, path), d.sweepContext(repo), now, idle)
}

func TestWorktreeRefreshObservesEachMergedSignal(t *testing.T) {
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()

	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))
	ancestor := repo.worktree("ancestor", "feat/ancestor", base)

	// What a squash or rebase merge leaves behind.
	tree := repo.worktree("tree", "feat/tree", base)
	repo.commitIn(tree, "shared.txt", "shared\n", "on the branch")
	repo.commitOnMain("shared.txt", "shared\n", "on main")
	repo.pushMain()

	pr := repo.worktree("pr", "feat/pr", base)
	prHead := repo.commitIn(pr, "pr.txt", "pr\n", "pr work")
	d.store.RecordRepoMergedBranches(repo.main, []store.MergedBranch{{
		Branch: "feat/pr", Number: 7, URL: "https://example.test/7", HeadSHA: prHead,
		MergedAt: now.Add(-time.Hour).Format(time.RFC3339),
	}}, now)
	d.store.SetRepoIntegrationBranch(repo.main, "main", "pull_requests", now)

	unmerged := repo.worktree("unmerged", "feat/unmerged", base)
	repo.commitIn(unmerged, "only-here.txt", "only here\n", "unmerged work")

	d.refreshRepositoryWorktrees(repo.main, now)

	for path, want := range map[string]store.MergedSignal{
		ancestor: store.MergedSignalAncestor,
		tree:     store.MergedSignalTree,
		pr:       store.MergedSignalPullRequest,
		unmerged: store.MergedSignalNone,
	} {
		if got := rowFor(t, d, path).MergedSignal; got != want {
			t.Errorf("%s merged signal = %q, want %q", filepath.Base(path), got, want)
		}
	}
}

func TestWorktreeSweepKeepsForEachReason(t *testing.T) {
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))

	dirty := repo.worktree("dirty", "feat/dirty", base)
	if err := os.WriteFile(filepath.Join(dirty, "wip.txt"), []byte("wip\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stashed := repo.worktree("stashed", "feat/stashed", base)
	if err := os.WriteFile(filepath.Join(stashed, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, stashed, "add", "tracked.txt")
	runGitDaemon(t, stashed, "commit", "-m", "tracked")
	if err := os.WriteFile(filepath.Join(stashed, "tracked.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, stashed, "stash", "push", "-m", "keep me")

	detached := repo.worktree("detached", "feat/detached", base)
	detachedHead := repo.commitIn(detached, "detached.txt", "detached\n", "detached work")
	runGitDaemon(t, detached, "checkout", "--detach", detachedHead)

	unmerged := repo.worktree("unmerged", "feat/unmerged", base)
	repo.commitIn(unmerged, "only-here.txt", "only here\n", "unmerged work")

	// One commit past the recorded merged tip.
	unpushed := repo.worktree("unpushed", "feat/unpushed", base)
	mergedHead := repo.commitIn(unpushed, "merged.txt", "merged\n", "the merged tip")
	repo.commitIn(unpushed, "after.txt", "after\n", "after the merge")

	pinned := repo.worktree("pinned", "feat/pinned", base)
	live := repo.worktree("live", "feat/live", base)
	openSeed := repo.worktree("open-seed", "feat/open-seed", base)

	d.store.RecordRepoMergedBranches(repo.main, []store.MergedBranch{
		{Branch: "feat/unpushed", Number: 8, HeadSHA: mergedHead},
		{Branch: "feat/stashed", Number: 9},
	}, now)
	d.store.SetRepoIntegrationBranch(repo.main, "main", "pull_requests", now)

	d.refreshRepositoryWorktrees(repo.main, now)

	if !d.store.SetWorktreePin(pinned, true, now) {
		t.Fatal("pinning the worktree found no row")
	}
	d.store.Add(&protocol.Session{ID: "session-live", Directory: live})

	cases := []struct {
		path       string
		wantStatus store.WorktreeSweepStatus
		wantReason string
	}{
		{pinned, store.WorktreeSweepPinned, "kept forever"},
		{live, store.WorktreeSweepKeptLiveSession, "session-live"},
		{dirty, store.WorktreeSweepKeptDirty, "uncommitted"},
		{stashed, store.WorktreeSweepKeptDirty, "stash"},
		{detached, store.WorktreeSweepKeptDetached, "detached HEAD"},
		{unmerged, store.WorktreeSweepKeptUnmerged, "no merged signal"},
		{unpushed, store.WorktreeSweepKeptUnpushed, "does not account for"},
	}
	for _, tc := range cases {
		verdict := verdictFor(t, d, repo.main, tc.path, 0, now)
		if verdict.Status != tc.wantStatus {
			t.Errorf("%s status = %q, want %q (reason %q)",
				filepath.Base(tc.path), verdict.Status, tc.wantStatus, verdict.Reason)
			continue
		}
		if !strings.Contains(verdict.Reason, tc.wantReason) {
			t.Errorf("%s reason = %q, want it to mention %q",
				filepath.Base(tc.path), verdict.Reason, tc.wantReason)
		}
	}

	facts := d.sweepContext(repo.main)
	facts.openSeeds = map[string][]string{openSeed: {"s-abc123"}}
	verdict := worktreeSweepVerdict(rowFor(t, d, openSeed), facts, now, 0)
	if verdict.Status != store.WorktreeSweepKeptOpenSeed || !strings.Contains(verdict.Reason, "s-abc123") {
		t.Errorf("open seed verdict = %q / %q", verdict.Status, verdict.Reason)
	}
}

func TestWorktreeKeepPinSurvivesRefreshAndReleases(t *testing.T) {
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))
	path := repo.worktree("merged", "feat/merged", base)

	d.refreshRepositoryWorktrees(repo.main, now)
	if _, err := d.setWorktreeKeep(path, true); err != nil {
		t.Fatalf("pinning: %v", err)
	}
	if got := verdictFor(t, d, repo.main, path, 0, now); got.Status != store.WorktreeSweepPinned {
		t.Fatalf("pinned verdict = %q", got.Status)
	}

	d.refreshRepositoryWorktrees(repo.main, now)
	if !rowFor(t, d, path).Pinned() {
		t.Fatal("the refresh dropped the pin")
	}

	if _, err := d.setWorktreeKeep(path, false); err != nil {
		t.Fatalf("unpinning: %v", err)
	}
	if got := verdictFor(t, d, repo.main, path, 0, now); got.Status == store.WorktreeSweepPinned {
		t.Fatal("unpinning left the row pinned")
	}
}

func TestWorktreeSweepPassKeepsALiveSessionAndReclaimsTheRest(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))

	reclaimed := repo.worktree("reclaimed", "feat/reclaimed", base)
	held := repo.worktree("held", "feat/held", base)
	d.store.Add(&protocol.Session{ID: "session-held", Directory: held})

	_, removed, _ := d.worktreeSweepPass(now)
	if removed != 1 {
		t.Fatalf("removed %d worktrees, want 1", removed)
	}
	if _, err := os.Stat(reclaimed); !os.IsNotExist(err) {
		t.Errorf("reclaimed worktree still on disk: %v", err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("the worktree holding a live session was removed: %v", err)
	}
	if d.store.GetWorktree(reclaimed) != nil {
		t.Error("the reclaimed row is still in the registry")
	}

	heldRow := rowFor(t, d, held)
	if heldRow.SweepStatus != store.WorktreeSweepKeptLiveSession ||
		!strings.Contains(heldRow.SweepReason, "session-held") {
		t.Errorf("held row = %q / %q", heldRow.SweepStatus, heldRow.SweepReason)
	}

	entries, _ := d.store.WorktreeSweepLog(repo.main, 10)
	if len(entries) != 1 {
		t.Fatalf("sweep log has %d entries, want 1", len(entries))
	}
	if entries[0].Path != reclaimed || entries[0].Action != "removed" {
		t.Errorf("sweep log entry = %+v", entries[0])
	}
	if !strings.Contains(entries[0].Reason, "merged") {
		t.Errorf("sweep log reason = %q, want it to name the merged signal", entries[0].Reason)
	}
}

func TestWorktreeSweepDisabledKeepsEligibleWorktreesAndSaysWhy(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	d.store.SetSetting(settingWorktreeSweepEnabled, "false")
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))
	path := repo.worktree("eligible", "feat/eligible", base)
	d.store.Add(&protocol.Session{ID: "session-main", Directory: repo.main})

	if _, removed, _ := d.worktreeSweepPass(time.Now()); removed != 0 {
		t.Fatalf("removed %d worktrees with the sweep off", removed)
	}
	row := rowFor(t, d, path)
	if row.SweepStatus != store.WorktreeSweepScheduled || !strings.Contains(row.SweepReason, "the sweep is off") {
		t.Errorf("row = %q / %q", row.SweepStatus, row.SweepReason)
	}
}

func TestWorktreeRefreshReconcilesAgainstGit(t *testing.T) {
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))

	nested := filepath.Join(repo.root, "main", ".claude", "worktrees", "agent-1")
	runGitDaemon(t, filepath.Join(repo.root, "main"), "worktree", "add", "-b", "agent/one", nested, base)
	adopted := git.CanonicalizePath(nested)

	d.refreshRepositoryWorktrees(repo.main, now)
	if row := d.store.GetWorktree(adopted); row == nil {
		t.Fatalf("the worktree under .claude/worktrees was not adopted")
	} else if row.Branch != "agent/one" {
		t.Errorf("adopted branch = %q", row.Branch)
	}

	runGitDaemon(t, filepath.Join(repo.root, "main"), "worktree", "remove", nested)
	d.refreshRepositoryWorktrees(repo.main, now)
	if d.store.GetWorktree(adopted) != nil {
		t.Error("a worktree git no longer lists is still in the registry")
	}
}

func TestWorktreeSweepIdleRuleWaitsTheFullWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lastActivity := time.Now()
		row := &store.Worktree{
			Path: "/repo/wt", Branch: "feat/done", MainRepo: "/repo",
			ObservedAt: lastActivity.Format(time.RFC3339), MergedSignal: store.MergedSignalAncestor,
			LastActivityAt: lastActivity.Format(time.RFC3339),
		}
		facts := sweepContext{integration: "origin/main"}
		idleFor := defaultWorktreeSweepIdleDays * 24 * time.Hour

		for day := 1; day < defaultWorktreeSweepIdleDays; day++ {
			time.Sleep(24 * time.Hour)
			verdict := worktreeSweepVerdict(row, facts, time.Now(), idleFor)
			if verdict.Status != store.WorktreeSweepScheduled {
				t.Fatalf("day %d: status = %q, want scheduled (%q)", day, verdict.Status, verdict.Reason)
			}
			if !verdict.At.Equal(lastActivity.Add(idleFor)) {
				t.Fatalf("day %d: eligible at %s, want %s", day, verdict.At, lastActivity.Add(idleFor))
			}
		}

		time.Sleep(24 * time.Hour)
		verdict := worktreeSweepVerdict(row, facts, time.Now(), idleFor)
		if verdict.Status != store.WorktreeSweepRemoved {
			t.Fatalf("at the threshold: status = %q, want removed (%q)", verdict.Status, verdict.Reason)
		}
	})
}

func TestWorktreeSweepIdleReadsTheEnvironmentOverride(t *testing.T) {
	if got := worktreeSweepIdle(); got != defaultWorktreeSweepIdleDays*24*time.Hour {
		t.Fatalf("default idle = %s", got)
	}
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "3")
	if got := worktreeSweepIdle(); got != 3*24*time.Hour {
		t.Fatalf("overridden idle = %s, want 72h", got)
	}
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "not a number")
	if got := worktreeSweepIdle(); got != defaultWorktreeSweepIdleDays*24*time.Hour {
		t.Fatalf("garbage override = %s, want the default", got)
	}
}

func TestWorktreeSweepNotesTheRemovalOnItsSeed(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	repo := newSweepRepo(t)
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))
	path := repo.worktree("worked", "feat/worked", base)

	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	if _, err := d.plantSeed(*schema, garden.Seed{
		ID: "s-abc123", Title: "the work that lived there",
		Status: garden.StatusHarvested, LastExecutionID: "session-worked",
	}); err != nil {
		t.Fatalf("planting: %v", err)
	}
	d.store.Add(&protocol.Session{ID: "session-worked", Directory: path})
	if err := d.recordGardenDispatch("session-worked", "s-abc123", "", path, "claude", false); err != nil {
		t.Fatalf("recording the dispatch: %v", err)
	}
	d.store.Remove("session-worked")
	d.store.Add(&protocol.Session{ID: "session-main", Directory: repo.main})

	if _, removed, _ := d.worktreeSweepPass(time.Now()); removed != 1 {
		t.Fatalf("removed %d worktrees, want 1", removed)
	}

	notes, _, err := d.readNotes("s-abc123", 10)
	if err != nil {
		t.Fatalf("reading notes: %v", err)
	}
	var found string
	for _, note := range notes {
		if strings.Contains(note.Body, "attn removed the worktree") {
			found = note.Body
		}
	}
	if found == "" {
		t.Fatalf("no sweep note on the seed; notes = %+v", notes)
	}
	for _, want := range []string{path, "feat/worked", repo.main} {
		if !strings.Contains(found, want) {
			t.Errorf("sweep note %q does not mention %q", found, want)
		}
	}
}

func TestDeletingAWorktreeByHandLeavesTheSameTrailAsTheSweep(t *testing.T) {
	repo := newSweepRepo(t)
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))
	path := repo.worktree("by-hand", "feat/by-hand", base)

	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	if _, err := d.plantSeed(*schema, garden.Seed{
		ID: "s-byhand", Title: "still open work", LastExecutionID: "session-by-hand",
	}); err != nil {
		t.Fatalf("planting: %v", err)
	}
	d.store.Add(&protocol.Session{ID: "session-by-hand", Directory: path})
	if err := d.recordGardenDispatch("session-by-hand", "s-byhand", "", path, "claude", false); err != nil {
		t.Fatalf("recording the dispatch: %v", err)
	}
	d.store.Remove("session-by-hand")
	d.store.Add(&protocol.Session{ID: "session-main", Directory: repo.main})

	if _, removed, _ := d.worktreeSweepPass(time.Now()); removed != 0 {
		t.Fatalf("the sweep removed %d worktrees, want 0", removed)
	}
	if d.store.GetWorktree(path) == nil {
		t.Fatalf("no registry row for %s", path)
	}

	if err := d.doDeleteWorktree(path, nil, deleteWorktreeOptions{}); err != nil {
		t.Fatalf("deleting the worktree: %v", err)
	}

	entries, _ := d.store.WorktreeSweepLog(repo.main, 10)
	if len(entries) != 1 {
		t.Fatalf("log has %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Action != "deleted" || entries[0].Path != path {
		t.Errorf("log entry = %+v, want a deleted entry for %s", entries[0], path)
	}

	notes, _, err := d.readNotes("s-byhand", 10)
	if err != nil {
		t.Fatalf("reading notes: %v", err)
	}
	var found string
	for _, note := range notes {
		if strings.Contains(note.Body, "attn deleted the worktree") {
			found = note.Body
		}
	}
	if found == "" {
		t.Fatalf("no removal note on the seed; notes = %+v", notes)
	}
	for _, want := range []string{path, "feat/by-hand", repo.main} {
		if !strings.Contains(found, want) {
			t.Errorf("removal note %q does not mention %q", found, want)
		}
	}
}

// Iterating a null takes the whole app down through its error boundary.
func TestTheWorktreeSurfaceNeverPutsNullWhereTheAppExpectsAnArray(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)

	list, err := json.Marshal(d.worktreeListResult("", 0))
	if err != nil {
		t.Fatalf("marshalling the empty list: %v", err)
	}
	for _, field := range []string{`"worktrees":[]`, `"repositories":[]`} {
		if !strings.Contains(string(list), field) {
			t.Fatalf("the empty worktree list is %s, want %s in it", list, field)
		}
	}

	log, err := json.Marshal(d.worktreeSweepLogResult("", 0))
	if err != nil {
		t.Fatalf("marshalling the empty sweep log: %v", err)
	}
	if !strings.Contains(string(log), `"entries":[]`) {
		t.Fatalf("the empty sweep log is %s, want \"entries\":[] in it", log)
	}
}

func TestASweepNeverActsOnARepositoryItCouldNotRefresh(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))

	stale := repo.worktree("stale", "feat/stale", base)
	d.refreshRepositoryWorktrees(repo.main, now)
	if row := rowFor(t, d, stale); row.MergedSignal == store.MergedSignalNone {
		t.Fatalf("the row is not eligible before the refresh breaks: %q", row.MergedSignal)
	}

	repo.commitIn(stale, "new-work.txt", "not on any branch yet\n", "work after the last refresh")

	broken := filepath.Join(repo.root, "main", ".git")
	if err := os.Rename(broken, broken+"-gone"); err != nil {
		t.Fatal(err)
	}

	if _, removed, _ := d.worktreeSweepPass(time.Now()); removed != 0 {
		t.Fatalf("the sweep removed %d worktrees of a repository it could not refresh, want 0", removed)
	}
	if _, err := os.Stat(filepath.Join(stale, "new-work.txt")); err != nil {
		t.Fatalf("the work committed since the last refresh is gone: %v", err)
	}
	// A removal git happened to refuse still writes its failure here.
	if entries, _ := d.store.WorktreeSweepLog(repo.main, 10); len(entries) != 0 {
		t.Fatalf("the sweep acted on a repository it could not refresh: %+v", entries)
	}

	row := rowFor(t, d, stale)
	if row.SweepStatus != store.WorktreeSweepUnknown ||
		!strings.Contains(row.SweepReason, "could not be refreshed") {
		t.Errorf("row after a failed refresh = %q / %q, want it to say nothing is decided",
			row.SweepStatus, row.SweepReason)
	}
}

func TestAFailedStashListingStopsTheSweepRatherThanReadingAsNoStash(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	repo := newSweepRepo(t)
	d := sweepDaemon(t)
	now := time.Now()
	base := strings.TrimSpace(gitOutput(t, repo.main, "rev-parse", "HEAD"))

	stashed := repo.worktree("stashed", "feat/stashed", base)
	if err := os.WriteFile(filepath.Join(stashed, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, stashed, "add", "tracked.txt")
	runGitDaemon(t, stashed, "commit", "-m", "tracked")
	if err := os.WriteFile(filepath.Join(stashed, "tracked.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, stashed, "stash", "push", "-m", "keep me")
	d.store.RecordRepoMergedBranches(repo.main, []store.MergedBranch{{Branch: "feat/stashed", Number: 9}}, now)
	d.store.SetRepoIntegrationBranch(repo.main, "main", "pull_requests", now)

	d.refreshRepositoryWorktrees(repo.main, now)
	if verdict := verdictFor(t, d, repo.main, stashed, 0, now); verdict.Status != store.WorktreeSweepKeptDirty {
		t.Fatalf("the stash gate does not hold before the listing breaks: %q / %q", verdict.Status, verdict.Reason)
	}

	// A stash ref pointing at an object that is gone: git refuses the listing.
	stashRef := filepath.Join(repo.root, "main", ".git", "refs", "stash")
	if err := os.WriteFile(stashRef, []byte(strings.Repeat("0", 39)+"1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, removed, _ := d.worktreeSweepPass(time.Now()); removed != 0 {
		t.Fatalf("the sweep removed %d worktrees while it could not see the stashes, want 0", removed)
	}
	if _, err := os.Stat(stashed); err != nil {
		t.Fatalf("the worktree holding the stash is gone: %v", err)
	}
	if entries, _ := d.store.WorktreeSweepLog(repo.main, 10); len(entries) != 0 {
		t.Fatalf("the sweep acted while it could not see the stashes: %+v", entries)
	}
	row := rowFor(t, d, stashed)
	if row.SweepStatus != store.WorktreeSweepUnknown ||
		!strings.Contains(row.SweepReason, "could not be refreshed") {
		t.Errorf("row after a failed stash listing = %q / %q, want it to say nothing is decided",
			row.SweepStatus, row.SweepReason)
	}
}
