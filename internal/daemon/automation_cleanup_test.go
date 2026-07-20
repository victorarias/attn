package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// TestAutomationCleanupPartitionsCleanAndDirtyWorktrees pins A4's core
// behavior: given two terminal runs with real worktrees, one clean and one
// dirty, cleanup removes the clean one's worktree and reports it in
// `cleaned`, leaves the dirty one's worktree in place and reports it in
// `kept_dirty` — with no age/count gate (unlike A3's retention sweep).
func TestAutomationCleanupPartitionsCleanAndDirtyWorktrees(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	cleanWorktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-clean", cleanWorktree)

	dirtyWorktree := filepath.Join(root, "repo--dirty")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-dirty", dirtyWorktree)
	if err := os.WriteFile(filepath.Join(dirtyWorktree, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Recent runs (well within any retention window) confirm cleanup has no
	// age or count gate: A3 would leave these alone entirely.
	now := time.Now()
	cleanRun := claimTerminalAutomationRun(t, s, def, "cleanup-clean-1", now, automationResolvedLocationJSON(t, mainRepo, cleanWorktree))
	dirtyRun := claimTerminalAutomationRun(t, s, def, "cleanup-dirty-1", now, automationResolvedLocationJSON(t, mainRepo, dirtyWorktree))

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != cleanRun.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, cleanRun.ID)
	}
	if len(keptDirty) != 1 || keptDirty[0] != dirtyRun.ID {
		t.Fatalf("keptDirty = %v, want [%s]", keptDirty, dirtyRun.ID)
	}
	if len(keptActive) != 0 {
		t.Fatalf("keptActive = %v, want none", keptActive)
	}
	if _, err := os.Stat(cleanWorktree); !os.IsNotExist(err) {
		t.Fatalf("expected the clean worktree to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(dirtyWorktree); err != nil {
		t.Fatalf("expected the dirty worktree to survive, stat err=%v", err)
	}
}

// TestAutomationCleanupNeverTouchesRowsOrArtifacts pins that, unlike A3's
// retention sweep, cleanup is disk-only: after cleaning a clean worktree,
// the run/occurrence rows and the occurrence artifact file are all still
// present — only the worktree is gone.
func TestAutomationCleanupNeverTouchesRowsOrArtifacts(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-rows", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-rows-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	artifactDir := filepath.Join(root, "automation", "occurrences")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(artifactDir, run.ID+".json")
	if err := os.WriteFile(artifactPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("keptDirty = %v, want none", keptDirty)
	}
	if len(keptActive) != 0 {
		t.Fatalf("keptActive = %v, want none", keptActive)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the worktree to be removed, stat err=%v", err)
	}
	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("run row must survive cleanup, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("occurrence artifact must survive cleanup, stat err=%v", err)
	}
}

// TestAutomationCleanupSecondRunIsNoOp pins that invoking cleanup again
// after a successful pass reports nothing new: the worktree that was
// already removed is not re-reported as cleaned.
func TestAutomationCleanupSecondRunIsNoOp(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-noop", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-noop-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("first pass cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("first pass keptDirty = %v, want none", keptDirty)
	}
	if len(keptActive) != 0 {
		t.Fatalf("first pass keptActive = %v, want none", keptActive)
	}

	cleaned2, keptDirty2, keptActive2, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned2) != 0 {
		t.Fatalf("second pass cleaned = %v, want none", cleaned2)
	}
	if len(keptDirty2) != 0 {
		t.Fatalf("second pass keptDirty = %v, want none", keptDirty2)
	}
	if len(keptActive2) != 0 {
		t.Fatalf("second pass keptActive = %v, want none", keptActive2)
	}
	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("run row must still survive after the second pass, got %#v err=%v", got, err)
	}
}

// TestAutomationCleanupLiveSessionSkipped pins that a terminal run whose
// session still exists in the store keeps its worktree, and that the user
// is told so via keptActive rather than the run disappearing from the
// result with no explanation. Unlike A3's retention sweep, cleanup has no
// age/count gate at all, so without the safety check it would remove a live
// thread's worktree out from under it on the very next on-demand cleanup.
func TestAutomationCleanupLiveSessionSkipped(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--live")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-live", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run := claimTerminalAutomationRun(t, s, def, "cleanup-live-1", now, automationResolvedLocationJSON(t, mainRepo, worktree))
	s.Add(&protocol.Session{
		ID: run.SessionID, Label: "reviewer", Agent: string(protocol.SessionAgentCodex), Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now.Format(time.RFC3339), StateUpdatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), WorkspaceID: run.WorkspaceID,
	})

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 0 || len(keptDirty) != 0 {
		t.Fatalf("expected a live-session run to appear in neither cleaned nor keptDirty, cleaned=%v keptDirty=%v", cleaned, keptDirty)
	}
	if len(keptActive) != 1 || keptActive[0] != run.ID {
		t.Fatalf("expected the live-session run to be reported kept_active, got %v", keptActive)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected the live-session worktree to survive cleanup, stat err=%v", err)
	}
}

// TestAutomationCleanupReclaimsSoftDeletedDefinition pins that cleanup still
// works after its definition is soft-deleted: automationCleanup deliberately
// uses GetAutomationDefinitionIncludingDeleted rather than
// GetAutomationDefinition, precisely so a user can reclaim a retired
// automation's worktrees on demand instead of waiting for the retention
// sweep to eventually reach it.
func TestAutomationCleanupReclaimsSoftDeletedDefinition(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--deleted-def")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-deleted-def", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-deleted-def-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatal(err)
	}

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("keptDirty = %v, want none", keptDirty)
	}
	if len(keptActive) != 0 {
		t.Fatalf("keptActive = %v, want none", keptActive)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the deleted definition's worktree to be reclaimed, stat err=%v", err)
	}
}

// TestAutomationCleanupBoundThreadReportsKeptActive pins the exact bug found
// live (packaged run automation-lifecycle-2026-07-20T19-58-08-613Z, leg 3): a
// terminal run whose session row is already gone but whose continuity
// binding survives must land in keptActive, not vanish from the result. A
// continuity thread reuses one session id and one shared worktree across
// every occurrence, so a session row being absent does not mean the thread —
// or the worktree this run points at — is dead.
func TestAutomationCleanupBoundThreadReportsKeptActive(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--bound")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-bound", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	dir := t.TempDir()
	def, err := d.automationApply(scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Bound."))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	run, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:1", "singleton", def.Revision, `{}`, `{}`, now, store.AutomationRunReservation{
		RunID: "run-bound-1", OccurrenceID: "occ-bound-1", TicketID: "ticket-bound-1", SessionID: "session-bound-1", WorkspaceID: "workspace-bound-1", PaneID: "pane-bound-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, automationResolvedLocationJSON(t, mainRepo, worktree), now); err != nil {
		t.Fatal(err)
	}
	// No session row is ever added: the thread's session has already been
	// garbage-collected, exactly like leg 3 closing the session before
	// cleanup. Only the surviving continuity binding protects the worktree.

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 0 || len(keptDirty) != 0 {
		t.Fatalf("expected the bound-thread run to appear in neither cleaned nor keptDirty, cleaned=%v keptDirty=%v", cleaned, keptDirty)
	}
	if len(keptActive) != 1 || keptActive[0] != run.ID {
		t.Fatalf("expected the bound-thread run to be reported kept_active, got %v", keptActive)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected the bound thread's shared worktree to survive cleanup, stat err=%v", err)
	}
}

// TestAutomationCleanupThreeWayPartition pins the full three-bucket contract
// in one call: a clean unbound run, a dirty unbound run, and a bound run
// under one definition land in cleaned, kept_dirty, and kept_active
// respectively — exactly one id per bucket, no id in two.
func TestAutomationCleanupThreeWayPartition(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	cleanWorktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/partition-clean", cleanWorktree)
	dirtyWorktree := filepath.Join(root, "repo--dirty")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/partition-dirty", dirtyWorktree)
	if err := os.WriteFile(filepath.Join(dirtyWorktree, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	boundWorktree := filepath.Join(root, "repo--bound")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/partition-bound", boundWorktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	dir := t.TempDir()
	def, err := d.automationApply(scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Partition."))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	claim := func(occurrenceKey, continuityKey, suffix, worktree string) *store.AutomationRun {
		t.Helper()
		run, _, err := s.ClaimScheduledAutomationRun(def.ID, occurrenceKey, continuityKey, def.Revision, `{}`, `{}`, now, store.AutomationRunReservation{
			RunID: "run-" + suffix, OccurrenceID: "occ-" + suffix, TicketID: "ticket-" + suffix, SessionID: "session-" + suffix, WorkspaceID: "workspace-" + suffix, PaneID: "pane-" + suffix,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkAutomationRunDelivered(run.ID, automationResolvedLocationJSON(t, mainRepo, worktree), now); err != nil {
			t.Fatal(err)
		}
		return run
	}
	// Empty continuity keys never write a binding (ClaimManualAutomationRun's
	// "" subjectKey has the same effect elsewhere in this file), so clean and
	// dirty are genuinely unbound; only "singleton" binds boundRun's session.
	cleanRun := claim("schedule:clean", "", "partition-clean", cleanWorktree)
	dirtyRun := claim("schedule:dirty", "", "partition-dirty", dirtyWorktree)
	boundRun := claim("schedule:bound", "singleton", "partition-bound", boundWorktree)

	cleaned, keptDirty, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != cleanRun.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, cleanRun.ID)
	}
	if len(keptDirty) != 1 || keptDirty[0] != dirtyRun.ID {
		t.Fatalf("keptDirty = %v, want [%s]", keptDirty, dirtyRun.ID)
	}
	if len(keptActive) != 1 || keptActive[0] != boundRun.ID {
		t.Fatalf("keptActive = %v, want [%s]", keptActive, boundRun.ID)
	}
}

// TestAutomationCleanupLogsDistinguishLiveSessionFromBoundThread pins the
// kept_active schema comment's claim (main.tsp, ~:2702) that "only the
// daemon log distinguishes them": a live-session run and a bound-thread run
// both land in the same keptActive bucket the caller sees, so the only place
// a user (or an on-call reading daemon.log) can tell which is which is the
// log line cleanup emits per run. Assert against the captured log content
// itself, not just the buckets already covered above.
func TestAutomationCleanupLogsDistinguishLiveSessionFromBoundThread(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	liveWorktree := filepath.Join(root, "repo--live")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/log-live", liveWorktree)
	boundWorktree := filepath.Join(root, "repo--bound")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/log-bound", boundWorktree)

	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	defer logger.Close()

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub(), logger: logger}
	dir := t.TempDir()
	def, err := d.automationApply(scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Log."))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// Live session: an unbound (empty continuityKey) run whose session row
	// still exists.
	liveRun, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:live", "", def.Revision, `{}`, `{}`, now, store.AutomationRunReservation{
		RunID: "run-log-live", OccurrenceID: "occ-log-live", TicketID: "ticket-log-live", SessionID: "session-log-live", WorkspaceID: "workspace-log-live", PaneID: "pane-log-live",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(liveRun.ID, automationResolvedLocationJSON(t, mainRepo, liveWorktree), now); err != nil {
		t.Fatal(err)
	}
	s.Add(&protocol.Session{
		ID: liveRun.SessionID, Label: "reviewer", Agent: string(protocol.SessionAgentCodex), Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now.Format(time.RFC3339), StateUpdatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), WorkspaceID: liveRun.WorkspaceID,
	})

	// Bound thread: a "singleton"-continuity run whose session row is gone
	// but whose binding survives.
	boundRun, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:bound", "singleton", def.Revision, `{}`, `{}`, now, store.AutomationRunReservation{
		RunID: "run-log-bound", OccurrenceID: "occ-log-bound", TicketID: "ticket-log-bound", SessionID: "session-log-bound", WorkspaceID: "workspace-log-bound", PaneID: "pane-log-bound",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(boundRun.ID, automationResolvedLocationJSON(t, mainRepo, boundWorktree), now); err != nil {
		t.Fatal(err)
	}

	_, _, keptActive, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keptActive) != 2 {
		t.Fatalf("keptActive = %v, want both %s and %s", keptActive, liveRun.ID, boundRun.ID)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := string(logged)
	if !strings.Contains(body, liveRun.ID) || !strings.Contains(body, "live session") {
		t.Fatalf("expected a live-session log line naming %s, got:\n%s", liveRun.ID, body)
	}
	if !strings.Contains(body, boundRun.ID) || !strings.Contains(body, "bound continuity thread") {
		t.Fatalf("expected a bound-thread log line naming %s, got:\n%s", boundRun.ID, body)
	}
}
