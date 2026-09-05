package daemon

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	worktreeSweepKind    = "worktree_sweep"
	worktreeSweepTimeout = 30 * time.Minute
)

// Sits in a measured gap: nothing worked with passes 10 days idle, and the next
// candidate is at 19. Receipt in docs/worktree-sweep.md.
const defaultWorktreeSweepIdleDays = 14

// A full pass is ~12 s of git for 147 worktrees. Hourly keeps it far off any
// request path and far under the window it watches.
const defaultWorktreeSweepInterval = time.Hour

func worktreeSweepIdle() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS")); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	return defaultWorktreeSweepIdleDays * 24 * time.Hour
}

func worktreeSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_WORKTREE_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultWorktreeSweepInterval
}

// On unless the user turns it off: the gates were verified against real
// repositories at 29 of 29 genuinely merged. See docs/worktree-sweep.md.
func (d *Daemon) worktreeSweepEnabled() bool {
	if d.store == nil {
		return false
	}
	return d.store.GetSetting(settingWorktreeSweepEnabled) != "false"
}

const settingWorktreeSweepEnabled = "worktree_sweep_enabled"

func (d *Daemon) registerWorktreeSweepCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		worktreeSweepKind,
		worktreeSweepInterval(),
		d.worktreeSweepHandler,
		jobs.HandlerConfig{Timeout: worktreeSweepTimeout},
	); err != nil {
		d.logf("worktree sweep: register tick: %v", err)
	}
}

func (d *Daemon) worktreeSweepHandler(_ context.Context, _ *jobs.Job) (any, error) {
	refreshed, removed, kept := d.worktreeSweepPass(time.Now())
	return map[string]any{"refreshed": refreshed, "removed": removed, "kept": kept}, nil
}

// Refresh, then decide from stored state only. The sweep never runs git to decide,
// so a verdict is always explainable from the row the surface is showing.
func (d *Daemon) worktreeSweepPass(now time.Time) (refreshed, removed, kept int) {
	if d.store == nil {
		return 0, 0, 0
	}

	repos := d.trackedRepositories()
	for _, repo := range repos {
		d.refreshRepositoryWorktrees(repo, now)
		refreshed++
	}

	sweeping := d.worktreeSweepEnabled()
	for _, repo := range repos {
		facts := d.sweepContext(repo)
		for _, wt := range d.store.ListWorktreesByRepo(repo) {
			verdict := worktreeSweepVerdict(wt, facts, now, worktreeSweepIdle())
			d.recordSweepVerdict(wt, verdict, now)
			if verdict.Status != store.WorktreeSweepRemoved {
				kept++
				continue
			}
			if !sweeping {
				// Eligible, but the sweep is turned off. Say so on the row rather than
				// pretending it is kept for a reason of its own.
				d.store.SetWorktreeSweep(wt.Path, store.WorktreeSweepScheduled,
					"eligible now; the sweep is off (Settings › Files and locations › Worktree sweep)", now)
				kept++
				continue
			}
			if d.removeSweptWorktree(wt, verdict, now) {
				removed++
			}
		}
	}
	if removed > 0 {
		d.logf("worktree sweep: reclaimed %d worktree(s), kept %d", removed, kept)
	}
	return refreshed, removed, kept
}

// What the gates read, all of it already stored or already in memory. Nothing
// here runs git.
type sweepContext struct {
	liveSessions map[string][]string
	openSeeds    map[string][]string
	integration  string
}

func (d *Daemon) sweepContext(repo string) sweepContext {
	return sweepContext{
		liveSessions: d.liveSessionsByWorktree(repo),
		openSeeds:    d.openSeedsByWorktree(repo),
		integration:  d.storedIntegrationBranch(repo),
	}
}

func (d *Daemon) storedIntegrationBranch(repo string) string {
	if record := d.store.RepoIntegrationBranch(repo); record != nil {
		return record.Branch
	}
	return ""
}

type sweepVerdict struct {
	Status store.WorktreeSweepStatus
	Reason string
	// When the row is scheduled, the date it becomes eligible.
	At time.Time
}

// The spike's rule set, in order. The first gate that holds names the kept reason,
// so a kept row always says which one it was and an agent can act on the text.
func worktreeSweepVerdict(wt *store.Worktree, facts sweepContext, now time.Time, idleFor time.Duration) sweepVerdict {
	if wt.Prunable {
		return sweepVerdict{store.WorktreeSweepKeptStale,
			"git still lists it but the directory is gone; delete the row to drop the record", time.Time{}}
	}
	if wt.Pinned() {
		return sweepVerdict{store.WorktreeSweepPinned, "kept forever by you", time.Time{}}
	}
	if sessions := facts.liveSessions[wt.Path]; len(sessions) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptLiveSession,
			fmt.Sprintf("%s is running in it", strings.Join(sessions, ", ")), time.Time{}}
	}
	if seeds := facts.openSeeds[wt.Path]; len(seeds) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptOpenSeed,
			fmt.Sprintf("open seed %s points at it", strings.Join(seeds, ", ")), time.Time{}}
	}
	if wt.ObservedAt == "" {
		return sweepVerdict{store.WorktreeSweepUnknown, "not refreshed yet", time.Time{}}
	}
	if wt.RefreshError != "" {
		return sweepVerdict{store.WorktreeSweepUnknown,
			"last refresh failed: " + wt.RefreshError, time.Time{}}
	}
	if wt.Dirty {
		return sweepVerdict{store.WorktreeSweepKeptDirty,
			fmt.Sprintf("%d uncommitted or untracked file(s)", wt.DirtyFiles), time.Time{}}
	}
	if wt.Stashes > 0 {
		return sweepVerdict{store.WorktreeSweepKeptDirty,
			fmt.Sprintf("%d stash entr%s on %s", wt.Stashes, plural(wt.Stashes, "y", "ies"), wt.Branch), time.Time{}}
	}
	base := facts.integration
	if base == "" {
		base = "the integration branch"
	}
	// 22 of the 23 detached worktrees in the sample held commits on no branch at
	// all; deleting one destroys them with nothing to recover them from.
	if wt.Detached && wt.MergedSignal != store.MergedSignalAncestor {
		return sweepVerdict{store.WorktreeSweepKeptDetached,
			fmt.Sprintf("detached HEAD at %s is not on %s, so its commits are on no branch", shortSHA(wt.HeadSHA), base), time.Time{}}
	}
	if wt.MergedSignal == store.MergedSignalNone {
		return sweepVerdict{store.WorktreeSweepKeptUnmerged,
			fmt.Sprintf("no merged signal for %s against %s", wt.Branch, base), time.Time{}}
	}
	if wt.Unpushed > 0 {
		return sweepVerdict{store.WorktreeSweepKeptUnpushed,
			fmt.Sprintf("%d commit(s) on %s the merge does not account for", wt.Unpushed, wt.Branch), time.Time{}}
	}

	lastActivity, err := time.Parse(time.RFC3339, wt.LastActivityAt)
	if err != nil {
		return sweepVerdict{store.WorktreeSweepUnknown, "no activity date observed yet", time.Time{}}
	}
	eligibleAt := lastActivity.Add(idleFor)
	if now.Before(eligibleAt) {
		return sweepVerdict{store.WorktreeSweepScheduled,
			fmt.Sprintf("merged and clean; idle %d of %d days",
				int(now.Sub(lastActivity).Hours()/24), int(idleFor.Hours()/24)), eligibleAt}
	}
	return sweepVerdict{store.WorktreeSweepRemoved,
		fmt.Sprintf("merged (%s) and clean, idle %d days", wt.MergedSignal, int(now.Sub(lastActivity).Hours()/24)), now}
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func (d *Daemon) recordSweepVerdict(wt *store.Worktree, verdict sweepVerdict, now time.Time) {
	if verdict.Status == store.WorktreeSweepRemoved {
		return
	}
	if wt.SweepStatus == verdict.Status && wt.SweepReason == verdict.Reason {
		return
	}
	d.store.SetWorktreeSweep(wt.Path, verdict.Status, verdict.Reason, verdict.At)
	if fresh := d.store.GetWorktree(wt.Path); fresh != nil {
		d.publishWorktreeState(fresh)
	}
}

// The delete writes the successful entry and the seed notes; a refusal never
// reaches it, so the sweep's own failure is the only one recorded here.
func (d *Daemon) removeSweptWorktree(wt *store.Worktree, verdict sweepVerdict, now time.Time) bool {
	err := d.doDeleteWorktree(wt.Path, nil, deleteWorktreeOptions{
		RemovalAction: "removed", RemovalReason: verdict.Reason,
	})
	if err != nil {
		d.logf("worktree sweep: removing %s: %v", wt.Path, err)
		entry := store.WorktreeSweepLogEntry{
			Path: wt.Path, MainRepo: wt.MainRepo, Branch: wt.Branch,
			Action: "failed", Reason: err.Error(),
		}
		entry.ID = d.store.AppendWorktreeSweepLog(entry, now)
		d.publishFact(FactWorktreeSwept, wt.Path, protocolSweepEntry(entry))
		return false
	}

	d.logf("worktree sweep: reclaimed %s (%s)", wt.Path, verdict.Reason)
	return true
}

// The registry drops the row, so the log and the seed notes are all that is left
// to say what went and why. Every removal writes both, whoever asked for it.
func (d *Daemon) recordWorktreeRemoval(
	wt *store.Worktree, seeds []string, opts deleteWorktreeOptions, now time.Time,
) {
	action, reason := opts.RemovalAction, opts.RemovalReason
	if action == "" {
		action, reason = "deleted", "at your request"
	}
	entry := store.WorktreeSweepLogEntry{
		Path: wt.Path, MainRepo: wt.MainRepo, Branch: wt.Branch,
		Action: action, Reason: reason,
	}
	entry.ID = d.store.AppendWorktreeSweepLog(entry, now)
	d.publishFact(FactWorktreeSwept, wt.Path, protocolSweepEntry(entry))

	// The path is in the note on purpose: worktrees other tools made go the same
	// way, and the note is where you see which one it was.
	body := fmt.Sprintf("attn %s the worktree %s (branch %s of %s): %s.",
		action, wt.Path, wt.Branch, wt.MainRepo, reason)
	for _, seedID := range seeds {
		if _, err := d.appendSeedNote(seedID, body, "", "", "", nil); err != nil {
			d.logf("worktree removal: noting %s on seed %s: %v", wt.Path, seedID, err)
		}
	}
}

// Every seed whose last execution ran in the worktree, open or closed. A closed
// seed blocks nothing, but its log is still where somebody looks for the branch.
func (d *Daemon) seedsForWorktree(wt *store.Worktree) []string {
	var seeds []string
	d.eachSeedExecution(func(seed garden.Seed, dispatch garden.Dispatch) {
		if worktreeOwnsExecution(wt, dispatch) {
			seeds = append(seeds, seed.ID)
		}
	})
	return seeds
}

// Open seeds only: what the gate blocks on. Planted, growing and dormant seeds
// all count; a harvested or withered one has nothing left to protect.
func (d *Daemon) openSeedsByWorktree(repo string) map[string][]string {
	byPath := make(map[string][]string)
	if d.store == nil {
		return byPath
	}
	rows := d.store.ListWorktreesByRepo(repo)
	if len(rows) == 0 {
		return byPath
	}
	d.eachSeedExecution(func(seed garden.Seed, dispatch garden.Dispatch) {
		if garden.Closed(seed.Status) {
			return
		}
		for _, row := range rows {
			if worktreeOwnsExecution(row, dispatch) {
				byPath[row.Path] = append(byPath[row.Path], seed.ID)
			}
		}
	})
	return byPath
}

func worktreeOwnsExecution(wt *store.Worktree, dispatch garden.Dispatch) bool {
	if pathAtOrBelow(dispatch.Cwd, wt.Path) {
		return true
	}
	return wt.Branch != "" && strings.TrimSpace(dispatch.Branch) == wt.Branch &&
		attngit.CanonicalizePath(dispatch.RepositoryRoot) == attngit.CanonicalizePath(wt.MainRepo)
}

// One scan of the seed collection per call, not one per worktree: the sweep pass
// builds its index once and every gate reads the map.
func (d *Daemon) eachSeedExecution(visit func(garden.Seed, garden.Dispatch)) {
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
			Limit: docstore.MaxLimit, After: after,
		})
		if err != nil {
			d.logf("worktree sweep: reading seeds: %v", err)
			return
		}
		for _, doc := range read.Documents {
			seed, err := garden.Decode(doc.Body)
			if err != nil || strings.TrimSpace(seed.LastExecutionID) == "" {
				continue
			}
			dispatch, ok := d.gardenDispatch(seed.LastExecutionID)
			if !ok {
				continue
			}
			visit(seed, dispatch)
		}
		if len(read.Documents) < docstore.MaxLimit {
			return
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func protocolSweepEntry(entry store.WorktreeSweepLogEntry) protocol.WorktreeSweepEntry {
	return protocol.WorktreeSweepEntry{
		ID: entry.ID, Path: entry.Path, MainRepo: entry.MainRepo,
		Branch: protocol.Ptr(entry.Branch), Action: entry.Action,
		Reason: protocol.Ptr(entry.Reason), At: entry.At,
	}
}
