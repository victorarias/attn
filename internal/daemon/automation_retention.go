package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/store"
)

// A3's fixed retention policy: per definition, keep the newest N runs
// unconditionally; a run is prunable only once it is outside that window,
// terminal (delivered/failed), and older than the age floor. All three are
// env-overridable so tests can shrink them without touching real time.Sleep
// or minting hundreds of runs — mirrors ticketReconcileSweepInterval's idiom
// in ticket_reconcile.go.
const (
	defaultAutomationRetentionKeep          = 200
	defaultAutomationRetentionMinAge        = 14 * 24 * time.Hour
	defaultAutomationRetentionSweepInterval = time.Hour
)

func automationRetentionKeep() int {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_KEEP")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultAutomationRetentionKeep
}

func automationRetentionMinAge() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_MIN_AGE")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur >= 0 {
			return dur
		}
	}
	return defaultAutomationRetentionMinAge
}

func automationRetentionSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultAutomationRetentionSweepInterval
}

// runAutomationRetentionSweep is the dedicated periodic backstop for A3: it
// does not piggyback the schedule-observation tick, since that tick's
// interval is driven by cron granularity, not retention policy. Mirrors
// runTicketReconcileSweep's shape exactly (ticket_reconcile.go) — no initial
// pass at boot (retention is not urgent enough to compete with startup
// churn), select on d.done to stop cleanly at shutdown.
func (d *Daemon) runAutomationRetentionSweep() {
	ticker := time.NewTicker(automationRetentionSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.automationRetentionSweepPass(time.Now())
		}
	}
}

// automationRetentionSweepPass prunes every definition's (including
// soft-deleted definitions', per ListAutomationDefinitionIDsIncludingDeleted's
// doc comment) prunable runs: for each candidate, a shared safety predicate
// (automationRunCleanupSafety, factored so A4's automationCleanup reuses it)
// decides whether it's safe to touch disk; only then are the worktree,
// occurrence artifact, and run+occurrence rows removed.
func (d *Daemon) automationRetentionSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	ids, err := d.store.ListAutomationDefinitionIDsIncludingDeleted()
	if err != nil {
		d.logf("automation retention sweep: list definitions: %v", err)
		return
	}
	keep := automationRetentionKeep()
	cutoff := now.Add(-automationRetentionMinAge())
	// boundThread is counted separately from the (deliberately unlogged)
	// live-session skip: a bound thread outliving one of its own runs is
	// still routine, but without this counter a sweep that examined
	// candidates and skipped every one of them for that reason logs nothing
	// at all, which reads identically to "nothing to do".
	pruned, keptDirty, boundThread := 0, 0, 0
	for _, defID := range ids {
		candidates, err := d.store.ListPrunableAutomationRuns(defID, keep, cutoff)
		if err != nil {
			d.logf("automation retention sweep: list prunable runs for %s: %v", defID, err)
			continue
		}
		for _, run := range candidates {
			block, err := d.automationRunCleanupSafety(run)
			if err != nil {
				d.logf("automation retention sweep: run %s: %v", run.ID, err)
				continue
			}
			switch block {
			case automationRunCleanupLiveSession:
				continue
			case automationRunCleanupBoundThread:
				boundThread++
				continue
			case automationRunCleanupDirtyWorktree:
				keptDirty++
				d.logf("automation retention sweep: keeping run %s (dirty worktree)", run.ID)
				continue
			}
			if err := d.removeAutomationRunWorktree(run); err != nil {
				d.logf("automation retention sweep: remove worktree for run %s: %v", run.ID, err)
				continue
			}
			if err := d.removeAutomationOccurrenceArtifact(run.ID); err != nil {
				d.logf("automation retention sweep: remove occurrence artifact for run %s: %v", run.ID, err)
				continue
			}
			if err := d.store.DeleteAutomationRun(run.ID); err != nil {
				d.logf("automation retention sweep: delete run %s: %v", run.ID, err)
				continue
			}
			pruned++
		}
	}
	if pruned > 0 || keptDirty > 0 || boundThread > 0 {
		d.logf("automation retention sweep: pruned %d run(s), kept %d dirty, %d bound to a live thread", pruned, keptDirty, boundThread)
	}
}

// automationRunCleanupBlock reports why a terminal run's on-disk footprint
// (if any) is not safe to remove yet.
type automationRunCleanupBlock int

const (
	// automationRunCleanupOK: safe to remove (or nothing to remove).
	automationRunCleanupOK automationRunCleanupBlock = iota
	// automationRunCleanupLiveSession: the run's session still exists —
	// permanently skip until that changes. Not logged per-run; a live
	// session is the routine, expected state for most terminal runs.
	automationRunCleanupLiveSession
	// automationRunCleanupBoundThread: the run's session id is still
	// referenced by a continuity binding, even though its own session row is
	// gone. A continuity thread reuses one session id and one shared
	// worktree (worktrees/<sessionID>/<repo>) across every occurrence, so an
	// old terminal run and the thread's live current run resolve to the same
	// on-disk directory — removing it here would brick the thread the next
	// time it's asked to continue. Not logged per-run, same reasoning as
	// automationRunCleanupLiveSession: a bound thread outliving one of its
	// own runs is the routine case this exists to protect, not an anomaly.
	// The sweep still counts it in aggregate (see automationRetentionSweepPass's
	// boundThread counter) and A4's cleanup reports it per-run-id in
	// kept_active — both callers must surface it as "examined and kept", not
	// silently skip it, which is what made this block invisible in the first
	// place.
	automationRunCleanupBoundThread
	// automationRunCleanupDirtyWorktree: the worktree has uncommitted
	// changes. Dirty evidence is never deleted — logged so it's visible.
	automationRunCleanupDirtyWorktree
)

// automationRunCleanupSafety is the shared safety predicate A3's retention
// sweep and A4's explicit cleanup both use before touching a run's disk
// footprint. It never mutates anything.
func (d *Daemon) automationRunCleanupSafety(run store.AutomationRun) (automationRunCleanupBlock, error) {
	if run.SessionID != "" && d.store.Get(run.SessionID) != nil {
		return automationRunCleanupLiveSession, nil
	}
	if run.SessionID != "" {
		bound, err := d.store.AutomationSessionHasContinuityBinding(run.SessionID)
		if err != nil {
			return automationRunCleanupOK, err
		}
		if bound {
			return automationRunCleanupBoundThread, nil
		}
	}
	worktree, err := automationRunWorktreePath(run)
	if err != nil {
		return automationRunCleanupOK, err
	}
	if worktree == "" {
		return automationRunCleanupOK, nil
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return automationRunCleanupOK, nil
		}
		return automationRunCleanupOK, statErr
	}
	clean, err := git.IsWorktreeClean(worktree)
	if err != nil {
		return automationRunCleanupOK, err
	}
	if !clean {
		return automationRunCleanupDirtyWorktree, nil
	}
	return automationRunCleanupOK, nil
}

// automationRunWorktreePath resolves a run's worktree directory (if any)
// from its persisted ResolvedLocationJSON — automation.ResolvedLocation, not
// the transient automation.PreparedLocation returned by PrepareLocation.
// Never re-derived by path convention: an absent resolved worktree means
// nothing to remove, not a signal to guess the path.
func automationRunWorktreePath(run store.AutomationRun) (string, error) {
	if strings.TrimSpace(run.ResolvedLocationJSON) == "" {
		return "", nil
	}
	var resolved automation.ResolvedLocation
	if err := json.Unmarshal([]byte(run.ResolvedLocationJSON), &resolved); err != nil {
		return "", fmt.Errorf("automation run %s resolved location: %w", run.ID, err)
	}
	return resolved.Worktree, nil
}

// removeAutomationRunWorktree removes a run's worktree from disk, assuming
// automationRunCleanupSafety already returned automationRunCleanupOK.
// Automation worktrees are created via git.EnsureAutomationSessionWorktree
// (PrepareLocation/PrepareGitHubReviewLocation in automations.go) without
// ever calling store.AddWorktree, so — unlike interactive-session worktrees —
// they are never registered in the store's worktree registry; this always
// goes through git.DeleteWorktree directly rather than Daemon.doDeleteWorktree's
// registry-aware path, which would be a no-op discovery-fallback for these.
func (d *Daemon) removeAutomationRunWorktree(run store.AutomationRun) error {
	if strings.TrimSpace(run.ResolvedLocationJSON) == "" {
		return nil
	}
	var resolved automation.ResolvedLocation
	if err := json.Unmarshal([]byte(run.ResolvedLocationJSON), &resolved); err != nil {
		return fmt.Errorf("automation run %s resolved location: %w", run.ID, err)
	}
	if resolved.Worktree == "" {
		return nil
	}
	if _, err := os.Stat(resolved.Worktree); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return git.DeleteWorktree(resolved.MainRepository, resolved.Worktree, false)
}

// removeAutomationOccurrenceArtifact removes a run's durable occurrence
// payload at <dataRoot>/automation/occurrences/<runID>.json (see
// ensureAutomationOccurrenceInput), ignoring not-exist. Mirrors that
// function's dataRoot-or-socketPath-dir fallback exactly so the sweep and
// the writer never disagree about where the file lives.
func (d *Daemon) removeAutomationOccurrenceArtifact(runID string) error {
	root := strings.TrimSpace(d.dataRoot)
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	path := filepath.Join(root, "automation", "occurrences", runID+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// automationCleanup is A4's explicit, on-demand counterpart to A3's
// automationRetentionSweepPass. Two differences from the sweep: it walks
// every terminal run for id right now, with no keep-window or age-floor
// filter, and it is disk-only — a cleaned run's worktree is removed, but its
// occurrence artifact and run/occurrence rows are left alone, so run history
// stays intact and only reclaims worktree disk space. Uses
// GetAutomationDefinitionIncludingDeleted (not GetAutomationDefinition) so a
// user can reclaim a deleted automation's leftover worktrees without waiting
// for the retention sweep to eventually reach it.
//
// The result is a three-way partition of every terminal run examined:
// cleaned (worktree removed), keptDirty (uncommitted changes), and
// keptActive (the run's thread is still in use — live session or bound
// continuity thread — so its worktree is off limits). keptActive merges two
// distinct safety-predicate blocks on purpose: from the caller's side, "a
// live session" and "a session row that's gone but still bound to a
// continuity thread" both mean the same thing, a worktree the user asked to
// reclaim that isn't reclaimable yet. Runs skipped for any other reason (an
// already-gone worktree, an empty resolved location, a stat/parse error) stay
// out of all three buckets — those aren't things the user asked about.
func (d *Daemon) automationCleanup(ctx context.Context, id string) (cleaned, keptDirty, keptActive []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("deadline exceeded waiting to run automation cleanup: %w", err)
	}
	definition, err := d.store.GetAutomationDefinitionIncludingDeleted(id)
	if err != nil {
		return nil, nil, nil, err
	}
	if definition == nil {
		return nil, nil, nil, fmt.Errorf("automation %q not found", id)
	}
	runs, err := d.store.ListTerminalAutomationRuns(id)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, run := range runs {
		worktree, werr := automationRunWorktreePath(run)
		if werr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, werr)
			continue
		}
		if worktree == "" {
			continue
		}
		if _, statErr := os.Stat(worktree); statErr != nil {
			// Already gone (or inaccessible): nothing new to report either way.
			continue
		}
		block, safetyErr := d.automationRunCleanupSafety(run)
		if safetyErr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, safetyErr)
			continue
		}
		switch block {
		case automationRunCleanupLiveSession:
			// Both cases below merge into the keptActive bucket the caller
			// sees (main.tsp's kept_active) — a user asking "why is this
			// still here" only cares that it's protected, not which reason.
			// The daemon log is where the two reasons are told apart.
			d.logf("automation cleanup %s: run %s: kept active (live session)", id, run.ID)
			keptActive = append(keptActive, run.ID)
			continue
		case automationRunCleanupBoundThread:
			d.logf("automation cleanup %s: run %s: kept active (bound continuity thread)", id, run.ID)
			keptActive = append(keptActive, run.ID)
			continue
		case automationRunCleanupDirtyWorktree:
			keptDirty = append(keptDirty, run.ID)
			continue
		}
		if removeErr := d.removeAutomationRunWorktree(run); removeErr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, removeErr)
			continue
		}
		cleaned = append(cleaned, run.ID)
	}
	if len(cleaned) > 0 || len(keptDirty) > 0 || len(keptActive) > 0 {
		d.logf("automation cleanup %s: cleaned %d worktree(s), kept %d dirty, %d active", id, len(cleaned), len(keptDirty), len(keptActive))
	}
	return cleaned, keptDirty, keptActive, nil
}
