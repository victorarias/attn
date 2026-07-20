package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func automationResolvedLocationJSON(t *testing.T, mainRepo, worktree string) string {
	t.Helper()
	resolved, err := json.Marshal(automation.ResolvedLocation{Type: "repository_worktree", MainRepository: mainRepo, Worktree: worktree})
	if err != nil {
		t.Fatal(err)
	}
	return string(resolved)
}

// claimTerminalAutomationRun claims and immediately delivers a manual run
// under def, backdating created_at/delivered_at to observedAt so retention
// age math is exact and deterministic in tests.
func claimTerminalAutomationRun(t *testing.T, s *store.Store, def *store.AutomationDefinition, requestID string, observedAt time.Time, resolvedLocationJSON string) *store.AutomationRun {
	t.Helper()
	run, created, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, observedAt, store.AutomationRunReservation{
		RunID: "run-" + requestID, OccurrenceID: "occ-" + requestID, TicketID: "ticket-" + requestID, SessionID: "session-" + requestID, WorkspaceID: "workspace-" + requestID, PaneID: "pane-" + requestID,
	})
	if err != nil || !created {
		t.Fatalf("claim %s created=%v err=%v", requestID, created, err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, resolvedLocationJSON, observedAt); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.GetAutomationRun(run.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reload %s: %#v err=%v", requestID, reloaded, err)
	}
	return reloaded
}

// TestAutomationRetentionSweepCountBoundary pins A3's keep-window boundary:
// with N total terminal runs and keep=N, none are candidates (the Nth-oldest
// is protected); adding one older run past that (N+1 total) makes exactly
// that oldest one prunable, the newest N stay protected. Mirrors the
// 199/200/201 boundary from the brief, shrunk via ATTN_AUTOMATION_RETENTION_KEEP
// so the test doesn't need to mint 201 real runs.
func TestAutomationRetentionSweepCountBoundary(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "3")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sweepAt := base.Add(48 * time.Hour)

	// Three runs, oldest-to-newest: none should ever be pruned while there
	// are only `keep` of them.
	r1 := claimTerminalAutomationRun(t, s, def, "r1", base.Add(1*time.Minute), "{}")
	r2 := claimTerminalAutomationRun(t, s, def, "r2", base.Add(2*time.Minute), "{}")
	r3 := claimTerminalAutomationRun(t, s, def, "r3", base.Add(3*time.Minute), "{}")
	d.automationRetentionSweepPass(sweepAt)
	for _, r := range []*store.AutomationRun{r1, r2, r3} {
		if got, err := s.GetAutomationRun(r.ID); err != nil || got == nil {
			t.Fatalf("run %s should be protected at the keep boundary (3 runs, keep=3), got %#v err=%v", r.ID, got, err)
		}
	}

	// A fourth, OLDER run pushes the count past keep=3: it becomes the sole
	// candidate, the newest 3 remain protected.
	r0 := claimTerminalAutomationRun(t, s, def, "r0", base, "{}")
	d.automationRetentionSweepPass(sweepAt)
	if got, err := s.GetAutomationRun(r0.ID); err != nil || got != nil {
		t.Fatalf("expected the oldest run past the keep boundary to be pruned, got %#v err=%v", got, err)
	}
	for _, r := range []*store.AutomationRun{r1, r2, r3} {
		if got, err := s.GetAutomationRun(r.ID); err != nil || got == nil {
			t.Fatalf("run %s should still be protected by the keep window, got %#v err=%v", r.ID, got, err)
		}
	}
}

// TestAutomationRetentionSweepPendingRunsNeverPruned pins that a pending run
// is never a candidate regardless of age or the keep window (ListPrunableAutomationRuns
// only considers delivered/failed runs).
func TestAutomationRetentionSweepPendingRunsNeverPruned(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run, created, err := s.ClaimManualAutomationRun(def.ID, "pending-1", "", `{}`, def.Revision, `{}`, old, store.AutomationRunReservation{
		RunID: "run-pending-1", OccurrenceID: "occ-pending-1", TicketID: "ticket-pending-1", SessionID: "session-pending-1", WorkspaceID: "workspace-pending-1", PaneID: "pane-pending-1",
	})
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a pending run must never be pruned, got %#v err=%v", got, err)
	}
}

// TestAutomationRetentionSweepYoungRunsNeverPruned pins the age floor: a
// terminal run younger than the min-age threshold is never a candidate even
// with keep=0.
func TestAutomationRetentionSweepYoungRunsNeverPruned(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "young-1", now, "{}")

	// Sweep at the same instant the run was delivered: age is 0 < 1h floor.
	d.automationRetentionSweepPass(now)

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a run younger than the age floor must never be pruned, got %#v err=%v", got, err)
	}
}

// TestAutomationRetentionSweepDirtyWorktreeBlocksPruning pins that a
// candidate run whose worktree has uncommitted changes is skipped entirely
// (row, artifact, and worktree all survive) — dirty evidence is never
// deleted.
func TestAutomationRetentionSweepDirtyWorktreeBlocksPruning(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--dirty")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/dirty", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "dirty-1", old, automationResolvedLocationJSON(t, mainRepo, worktree))

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a run with a dirty worktree must not be pruned, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("dirty worktree must not be removed, got err=%v", err)
	}
}

// TestAutomationRetentionSweepCleanWorktreeRemovesEverything pins the
// success path: a candidate run with a clean (or absent) worktree has its
// worktree, occurrence artifact, and run+occurrence rows all removed.
func TestAutomationRetentionSweepCleanWorktreeRemovesEverything(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/clean", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "clean-1", old, automationResolvedLocationJSON(t, mainRepo, worktree))

	artifactDir := filepath.Join(root, "automation", "occurrences")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(artifactDir, run.ID+".json")
	if err := os.WriteFile(artifactPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got != nil {
		t.Fatalf("expected the run row to be removed, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the clean worktree to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("expected the occurrence artifact to be removed, stat err=%v", err)
	}
}

// TestAutomationRetentionSweepLiveSessionSkipped pins that a candidate run
// whose session still exists in the store is skipped, regardless of age or
// the keep window — the run must not be pruned out from under a live
// session.
func TestAutomationRetentionSweepLiveSessionSkipped(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "live-1", old, "{}")
	s.Add(&protocol.Session{
		ID: run.SessionID, Label: "reviewer", Agent: string(protocol.SessionAgentCodex), Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: old.Format(time.RFC3339), StateUpdatedAt: old.Format(time.RFC3339), LastSeen: old.Format(time.RFC3339), WorkspaceID: run.WorkspaceID,
	})

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a run whose session is still live must not be pruned, got %#v err=%v", got, err)
	}
}
