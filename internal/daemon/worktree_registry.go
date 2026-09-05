package daemon

import (
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Resolved from where a repository's pull requests actually merge, not origin/HEAD.
// Receipt in docs/worktree-sweep.md.
const integrationBranchTTL = 24 * time.Hour

// What one repository pass learned once and every worktree of it then reads. Built
// per pass so 141 worktrees cost one repository-wide git call each, not 141.
type repositoryFacts struct {
	repo string
	// The ref ancestry and tree equality are measured against, e.g. "origin/next".
	integrationBranch string
	treeHashes        map[string]bool
	stashes           map[string]int
	mergedBranches    map[string]store.MergedBranch
	// Live sessions and open seeds by canonical worktree path, so a gate never
	// rescans the session list or the seed collection per worktree.
	liveSessions map[string][]string
	openSeeds    map[string][]string
	// The newest activity any live session reported in each worktree.
	sessionActivity map[string]time.Time
}

// The registry's repositories plus the ones live sessions sit in, so a repository
// with no worktree yet still shows up on the surface.
func (d *Daemon) trackedRepositories() []string {
	seen := make(map[string]bool)
	var repos []string
	add := func(repo string) {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return
		}
		repo = git.CanonicalizePath(repo)
		if seen[repo] {
			return
		}
		seen[repo] = true
		repos = append(repos, repo)
	}
	if d.store == nil {
		return nil
	}
	for _, repo := range d.store.ListWorktreeRepos() {
		add(repo)
	}
	// ResolveMainRepoPath, always: a session in a worktree resolves to its own root,
	// and treating that as a repository would register the main worktree itself.
	for _, session := range d.store.List("") {
		if repo := strings.TrimSpace(protocol.Deref(session.MainRepo)); repo != "" {
			add(git.ResolveMainRepoPath(repo))
			continue
		}
		if root, err := git.GetRepoRoot(session.Directory); err == nil {
			add(git.ResolveMainRepoPath(root))
		}
	}
	return repos
}

// Reconciles the registry against git and writes observed state. Every git call is
// a tracked GitOperation, so the surface can show a slow repository per row.
func (d *Daemon) refreshRepositoryWorktrees(repo string, now time.Time) {
	if d.store == nil {
		return
	}

	facts, err := d.repositoryFacts(repo, now)
	if err != nil {
		d.logf("worktree refresh: %s: %v", repo, err)
		return
	}

	states, err := d.reconcileWorktreeRegistry(repo, now)
	if err != nil {
		d.logf("worktree refresh: %s: listing worktrees: %v", repo, err)
		return
	}

	d.coalesceSnapshots(func() {
		for _, state := range states {
			d.refreshWorktreeRow(facts, state, now)
		}
	})
}

// git is the truth: what it reports becomes a row, what it no longer reports is
// dropped. The main worktree is never a row, so no gate has to exclude it.
func (d *Daemon) reconcileWorktreeRegistry(repo string, now time.Time) ([]git.WorktreeState, error) {
	finish := d.beginGitOperation(protocol.GitOperationKindRefreshRepository, repo, nil)
	states, err := git.ListWorktreeStates(repo)
	finish(err)
	if err != nil {
		return nil, err
	}

	listed := make(map[string]bool, len(states))
	var rows []git.WorktreeState
	for _, state := range states {
		if state.Path == repo {
			continue
		}
		listed[state.Path] = true
		rows = append(rows, state)
		existing := d.store.GetWorktree(state.Path)
		if existing != nil {
			continue
		}
		d.store.AddWorktree(&store.Worktree{
			Path: state.Path, Branch: state.Branch, MainRepo: repo,
			CreatedAt: now, Origin: store.WorktreeOriginGit,
		})
	}

	for _, row := range d.store.ListWorktreesByRepo(repo) {
		if !listed[row.Path] {
			d.store.RemoveWorktree(row.Path)
			d.publishFact(FactWorktreeDeleted, row.Path, nil)
		}
	}
	return rows, nil
}

func (d *Daemon) repositoryFacts(repo string, now time.Time) (*repositoryFacts, error) {
	facts := &repositoryFacts{repo: repo}

	d.refreshMergedPullRequests(repo, now)
	facts.integrationBranch = d.integrationBranch(repo, now)

	finish := d.beginGitOperation(protocol.GitOperationKindRefreshRepository, repo, nil)
	treeHashes, err := git.TreeHashesOnHistory(repo, facts.integrationBranch)
	if err != nil {
		// A repository with no integration branch locally is not an error: the pull
		// request record still answers rung 1 and the tree rung simply stays silent.
		d.logf("worktree refresh: %s: tree hashes for %s: %v", repo, facts.integrationBranch, err)
		treeHashes = nil
	}
	stashes, stashErr := git.StashCountsByBranch(repo)
	finish(stashErr)
	facts.treeHashes = treeHashes
	facts.stashes = stashes

	facts.mergedBranches = d.store.RepoMergedBranches(repo)
	if facts.mergedBranches == nil {
		facts.mergedBranches = make(map[string]store.MergedBranch)
	}
	// A pull request a live session opened counts before the repository-wide
	// refresh has ever run, which is the whole point of keeping both records.
	for branch, record := range d.store.MergedSessionPullRequestBranches() {
		if _, known := facts.mergedBranches[branch]; !known {
			facts.mergedBranches[branch] = record
		}
	}

	facts.liveSessions = d.liveSessionsByWorktree(repo)
	facts.openSeeds = d.openSeedsByWorktree(repo)
	facts.sessionActivity = d.sessionActivityByWorktree(facts.liveSessions)
	return facts, nil
}

func (d *Daemon) sessionActivityByWorktree(liveSessions map[string][]string) map[string]time.Time {
	activity := make(map[string]time.Time, len(liveSessions))
	for path, sessionIDs := range liveSessions {
		for _, sessionID := range sessionIDs {
			session := d.store.Get(sessionID)
			if session == nil {
				continue
			}
			seen, err := time.Parse(time.RFC3339, session.LastSeen)
			if err == nil && seen.After(activity[path]) {
				activity[path] = seen
			}
		}
	}
	return activity
}

// One `gh pr list`-equivalent call per repository, measured at 899 ms; the sweep
// itself reads stored state only and never touches the network.
func (d *Daemon) refreshMergedPullRequests(repo string, now time.Time) {
	host, ownerRepo := git.OriginHostOwnerRepo(repo)
	if host == "" || ownerRepo == "" {
		return
	}
	if d.ghRegistry == nil {
		return
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return
	}
	if limited, resetAt := client.IsRateLimited("core"); limited {
		d.logf("worktree refresh: %s rate limited until %s, merged branches stay as recorded",
			host, resetAt.Format(time.RFC3339))
		return
	}

	merged, err := client.ListMergedPullRequests(ownerRepo)
	if err != nil {
		d.logf("worktree refresh: %s: listing merged pull requests: %v", repo, err)
		return
	}

	branches := make([]store.MergedBranch, 0, len(merged))
	baseCounts := make(map[string]int)
	for _, pr := range merged {
		branches = append(branches, store.MergedBranch{
			Branch: pr.HeadRef, MergedAt: pr.MergedAt, Number: pr.Number,
			URL: pr.URL, HeadSHA: pr.HeadSHA,
		})
		if pr.BaseRef != "" {
			baseCounts[pr.BaseRef]++
		}
	}
	d.store.RecordRepoMergedBranches(repo, branches, now)

	if base, ok := modalBaseBranch(baseCounts); ok {
		d.store.SetRepoIntegrationBranch(repo, base, "pull_requests", now)
	}
}

func modalBaseBranch(counts map[string]int) (string, bool) {
	best, bestCount := "", 0
	for branch, count := range counts {
		// Ties break on the name so the resolved branch does not flip between passes.
		if count > bestCount || (count == bestCount && branch < best) {
			best, bestCount = branch, count
		}
	}
	return best, best != ""
}

// The stored resolution wins while it is fresh; origin/HEAD is the fallback, not
// the source. The returned ref is one git can resolve in this repository.
func (d *Daemon) integrationBranch(repo string, now time.Time) string {
	if record := d.store.RepoIntegrationBranch(repo); record != nil && record.Branch != "" {
		resolvedAt, err := time.Parse(time.RFC3339, record.ResolvedAt)
		if err == nil && now.Sub(resolvedAt) < integrationBranchTTL {
			return resolveIntegrationRef(repo, record.Branch)
		}
		if record.Source == "pull_requests" {
			return resolveIntegrationRef(repo, record.Branch)
		}
	}
	branch, err := git.GetDefaultBranch(repo)
	if err != nil || branch == "" {
		branch = "main"
	}
	d.store.SetRepoIntegrationBranch(repo, branch, "origin_head", now)
	return resolveIntegrationRef(repo, branch)
}

func resolveIntegrationRef(repo, branch string) string {
	if strings.HasPrefix(branch, "origin/") {
		return branch
	}
	if git.RefExists(repo, "origin/"+branch) {
		return "origin/" + branch
	}
	return branch
}

func (d *Daemon) refreshWorktreeRow(facts *repositoryFacts, state git.WorktreeState, now time.Time) {
	before := d.store.GetWorktree(state.Path)
	if before == nil {
		return
	}

	finish := d.beginGitOperation(protocol.GitOperationKindRefreshWorktree, state.Path, nil)
	observation, err := observeWorktree(facts, state, now)
	finish(err)

	d.store.UpdateWorktreeObservation(state.Path, observation, now)
	after := d.store.GetWorktree(state.Path)
	if after == nil || sameObservation(before, after) {
		return
	}
	d.publishWorktreeState(after)
}

func sameObservation(before, after *store.Worktree) bool {
	return before.Branch == after.Branch &&
		before.HeadSHA == after.HeadSHA &&
		before.Detached == after.Detached &&
		before.Dirty == after.Dirty &&
		before.DirtyFiles == after.DirtyFiles &&
		before.Stashes == after.Stashes &&
		before.Unpushed == after.Unpushed &&
		before.MergedSignal == after.MergedSignal &&
		before.Prunable == after.Prunable &&
		before.LastActivityAt == after.LastActivityAt &&
		before.RefreshError == after.RefreshError
}

func observeWorktree(facts *repositoryFacts, state git.WorktreeState, now time.Time) (store.WorktreeObservation, error) {
	observation := store.WorktreeObservation{
		Branch:   state.Branch,
		HeadSHA:  state.HeadSHA,
		Detached: state.Detached,
		Stashes:  facts.stashes[state.Branch],
	}

	if _, err := os.Stat(state.Path); err != nil {
		// git lists it, the directory is gone. Stale, never openable, never swept.
		observation.Prunable = true
		return observation, nil
	}
	observation.Prunable = state.Prunable

	dirtyFiles, err := git.WorktreeDirtyCount(state.Path)
	if err != nil {
		observation.Error = err.Error()
		return observation, err
	}
	observation.DirtyFiles = dirtyFiles
	observation.Dirty = dirtyFiles > 0

	observation.MergedSignal = mergedSignal(facts, state)
	observation.Unpushed = commitsBeyondTheMerge(facts, state, observation.MergedSignal)
	observation.LastActivityAt = worktreeLastActivity(facts, state, now)
	return observation, nil
}

// The ladder, first hit wins. No patch-id probe: it writes a loose object per
// branch into the user's repository. See docs/worktree-sweep.md.
func mergedSignal(facts *repositoryFacts, state git.WorktreeState) store.MergedSignal {
	if _, merged := facts.mergedBranches[state.Branch]; merged && state.Branch != "" {
		return store.MergedSignalPullRequest
	}
	ref := state.Branch
	if ref == "" {
		ref = state.HeadSHA
	}
	if ref == "" || facts.integrationBranch == "" {
		return store.MergedSignalNone
	}
	if git.IsAncestor(facts.repo, ref, facts.integrationBranch) {
		return store.MergedSignalAncestor
	}
	if len(facts.treeHashes) > 0 {
		if hash, err := git.TreeHash(facts.repo, ref); err == nil && facts.treeHashes[hash] {
			return store.MergedSignalTree
		}
	}
	return store.MergedSignalNone
}

// Commits the merge does not account for, not commits ahead: a squash merge is
// accounted for only up to the tip GitHub recorded as merged.
func commitsBeyondTheMerge(facts *repositoryFacts, state git.WorktreeState, signal store.MergedSignal) int {
	if facts.integrationBranch == "" || state.Branch == "" {
		return 0
	}
	switch signal {
	case store.MergedSignalAncestor, store.MergedSignalTree:
		return 0
	}
	ahead, err := git.CommitsAhead(facts.repo, facts.integrationBranch, state.Branch)
	if err != nil || ahead == 0 {
		return 0
	}
	if signal != store.MergedSignalPullRequest {
		return ahead
	}
	record := facts.mergedBranches[state.Branch]
	if record.HeadSHA != "" && record.HeadSHA == state.HeadSHA {
		return 0
	}
	if record.HeadSHA == "" {
		// No recorded tip to compare against: the merge accounts for the branch as
		// the record found it, so the commits ahead are the merged ones.
		return 0
	}
	beyond, err := git.CommitsAhead(facts.repo, record.HeadSHA, state.Branch)
	if err != nil {
		return ahead
	}
	return beyond
}

// max(newest tree mtime excluding .git and build dirs, last commit, last session
// activity). The mtime is what makes idle honest where attn ran no session.
func worktreeLastActivity(facts *repositoryFacts, state git.WorktreeState, now time.Time) time.Time {
	newest := time.Time{}
	consider := func(candidate time.Time) {
		if candidate.After(newest) && !candidate.After(now) {
			newest = candidate
		}
	}
	if mtime, err := git.NewestTreeModTime(state.Path); err == nil {
		consider(mtime)
	}
	if committed, err := git.LastCommitTime(state.Path); err == nil {
		consider(committed)
	}
	consider(facts.sessionActivity[state.Path])
	return newest
}

func (d *Daemon) liveSessionsByWorktree(repo string) map[string][]string {
	byPath := make(map[string][]string)
	if d.store == nil {
		return byPath
	}
	rows := d.store.ListWorktreesByRepo(repo)
	for _, session := range d.store.List("") {
		for _, row := range rows {
			if pathAtOrBelow(session.Directory, row.Path) {
				byPath[row.Path] = append(byPath[row.Path], session.ID)
			}
		}
	}
	return byPath
}

func (d *Daemon) publishWorktreeState(wt *store.Worktree) {
	d.publishFact(FactWorktreeStateChanged, wt.Path, protocolWorktree(wt))
}

func protocolWorktree(wt *store.Worktree) protocol.Worktree {
	out := protocol.Worktree{
		Path:     wt.Path,
		Branch:   wt.Branch,
		MainRepo: wt.MainRepo,
		Origin:   protocol.Ptr(string(wt.Origin)),
		Detached: protocol.Ptr(wt.Detached),
		Dirty:    protocol.Ptr(wt.Dirty),
		Prunable: protocol.Ptr(wt.Prunable),
		Pinned:   protocol.Ptr(wt.Pinned()),
	}
	if !wt.CreatedAt.IsZero() {
		out.CreatedAt = protocol.Ptr(wt.CreatedAt.Format(time.RFC3339))
	}
	if wt.HeadSHA != "" {
		out.HeadSHA = protocol.Ptr(wt.HeadSHA)
	}
	if wt.DirtyFiles > 0 {
		out.DirtyFiles = protocol.Ptr(wt.DirtyFiles)
	}
	if wt.Stashes > 0 {
		out.Stashes = protocol.Ptr(wt.Stashes)
	}
	if wt.Unpushed > 0 {
		out.Unpushed = protocol.Ptr(wt.Unpushed)
	}
	if wt.MergedSignal != store.MergedSignalNone {
		out.MergedSignal = protocol.Ptr(string(wt.MergedSignal))
	}
	if wt.ObservedAt != "" {
		out.ObservedAt = protocol.Ptr(wt.ObservedAt)
	}
	if wt.LastActivityAt != "" {
		out.LastActivityAt = protocol.Ptr(wt.LastActivityAt)
	}
	if wt.PinnedAt != "" {
		out.PinnedAt = protocol.Ptr(wt.PinnedAt)
	}
	if wt.SweepStatus != store.WorktreeSweepUnknown {
		out.SweepStatus = protocol.Ptr(string(wt.SweepStatus))
	}
	if wt.SweepReason != "" {
		out.SweepReason = protocol.Ptr(wt.SweepReason)
	}
	if wt.SweepAt != "" {
		out.SweepAt = protocol.Ptr(wt.SweepAt)
	}
	if wt.RefreshError != "" {
		out.RefreshError = protocol.Ptr(wt.RefreshError)
	}
	return out
}
