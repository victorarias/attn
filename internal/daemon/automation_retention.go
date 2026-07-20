package daemon

import (
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
	pruned, keptDirty := 0, 0
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
	if pruned > 0 || keptDirty > 0 {
		d.logf("automation retention sweep: pruned %d run(s), kept %d dirty", pruned, keptDirty)
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
