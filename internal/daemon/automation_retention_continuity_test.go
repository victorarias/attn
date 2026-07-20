package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/store"
)

// TestAutomationRetentionSweepPreservesBoundThreadOriginRunAndContinuationStillResolves
// is the regression test for Blocker 1: a still-bound continuity thread's
// origin run — the one tickets.automation_run_id permanently points at — must
// survive the sweep even when it is far outside both the keep window and the
// age floor, and automationContinuationOrigin must still resolve it for the
// thread's next occurrence. Before ListPrunableAutomationRuns excluded it,
// this run was pruned first (it's the oldest), permanently bricking the
// thread with "continuity origin run missing".
func TestAutomationRetentionSweepPreservesBoundThreadOriginRunAndContinuationStillResolves(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Sweep.")
	def, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	spec, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	origin, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:1", "singleton", def.Revision, `{}`, string(snapshotJSON), old, store.AutomationRunReservation{
		RunID: "run-origin", OccurrenceID: "occ-origin", TicketID: "ticket-origin", SessionID: "session-origin", WorkspaceID: "workspace-origin", PaneID: "pane-origin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(origin.ID, "{}", old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: origin.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: origin.SessionID, AutomationRunID: origin.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, old); err != nil {
		t.Fatal(err)
	}

	d.automationRetentionSweepPass(time.Now())

	reloadedOrigin, err := s.GetAutomationRun(origin.ID)
	if err != nil || reloadedOrigin == nil {
		t.Fatalf("expected the bound thread's origin run to survive the sweep, got %#v err=%v", reloadedOrigin, err)
	}

	// A subsequent occurrence must still resolve the origin without error —
	// this is the actual failure mode the brief describes: a hard error on
	// every delivery from here on, with no recovery path.
	req := automation.WorkRequest{
		RunID: "run-next", DefinitionID: def.ID, ContinuityKey: "singleton", Provider: "schedule",
		Prompt: snapshot.Prompt, Launch: snapshot.Launch, Location: snapshot.Location,
		IDs: automation.DeliveryIDs{TicketID: origin.TicketID, SessionID: origin.SessionID},
	}
	resolvedOrigin, err := d.automationContinuationOrigin(req)
	if err != nil {
		t.Fatalf("automationContinuationOrigin after sweep: %v", err)
	}
	if resolvedOrigin == nil || resolvedOrigin.ID != origin.ID {
		t.Fatalf("expected automationContinuationOrigin to resolve the surviving origin run, got %#v", resolvedOrigin)
	}
}

// TestAutomationRetentionAndCleanupPreserveBoundThreadSharedWorktree is the
// regression test for Blocker 2. Every run in a continuity thread reuses the
// thread's one session id, and the worktree path is keyed on session id
// alone (worktrees/<sessionID>/<repo>) — so a non-origin terminal run shares
// its worktree with the thread's live current run. Blocker 1's SQL fix only
// excludes the origin row from ListPrunableAutomationRuns, so this run — an
// ordinary second occurrence, not the origin — is still a candidate by
// count/age; only automationRunCleanupSafety's automationRunCleanupBoundThread
// check protects its shared worktree, for both the sweep and A4's on-demand
// cleanup. Retiring the automation drops its continuity bindings, so the same
// worktree becomes reclaimable afterward — the design A4 relies on.
func TestAutomationRetentionAndCleanupPreserveBoundThreadSharedWorktree(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--shared")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/shared", worktree)
	resolvedLocationJSON := automationResolvedLocationJSON(t, mainRepo, worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	dir := t.TempDir()

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Sweep.")
	def, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	spec, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	origin, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:1", "singleton", def.Revision, `{}`, string(snapshotJSON), old, store.AutomationRunReservation{
		RunID: "run-origin", OccurrenceID: "occ-origin", TicketID: "ticket-origin", SessionID: "session-shared", WorkspaceID: "workspace-origin", PaneID: "pane-origin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(origin.ID, resolvedLocationJSON, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: origin.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: origin.SessionID, AutomationRunID: origin.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, old); err != nil {
		t.Fatal(err)
	}

	// A second, non-origin occurrence of the same thread: the continuity
	// binding makes it reuse ticket-origin/session-shared regardless of the
	// reservation ids passed here, exactly like a live thread's next run
	// would.
	second, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:2", "singleton", def.Revision, `{}`, string(snapshotJSON), old.Add(time.Minute), store.AutomationRunReservation{
		RunID: "run-second", OccurrenceID: "occ-second", TicketID: "ticket-second", SessionID: "session-second", WorkspaceID: "workspace-second", PaneID: "pane-second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.TicketID != origin.TicketID || second.SessionID != origin.SessionID {
		t.Fatalf("expected the second occurrence to reuse the bound thread's ticket/session, got ticket=%q session=%q", second.TicketID, second.SessionID)
	}
	if err := s.MarkAutomationRunDelivered(second.ID, resolvedLocationJSON, old.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// second is terminal and is not the ticket's origin run, so Blocker 1's
	// SQL exclusion does not cover it — only the safety predicate can.

	d.automationRetentionSweepPass(time.Now())
	if got, err := s.GetAutomationRun(second.ID); err != nil || got == nil {
		t.Fatalf("expected the bound thread's non-origin run to survive the sweep, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected the shared worktree to survive the sweep, stat err=%v", err)
	}

	cleaned, _, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 0 {
		t.Fatalf("expected cleanup to leave the bound thread's shared worktree alone, cleaned=%v", cleaned)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected the shared worktree to survive cleanup, stat err=%v", err)
	}

	// Retiring the automation drops its continuity bindings, so the same
	// worktree becomes reclaimable — exactly A4's stated purpose.
	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatal(err)
	}
	cleaned, _, err = d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) == 0 {
		t.Fatal("expected cleanup to reclaim the shared worktree once the definition's bindings were dropped")
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the shared worktree to be removed after retirement, stat err=%v", err)
	}
}
