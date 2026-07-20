package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/store"
)

// TestAutomationApplyContractEditRotatesContinuityBindings covers A1's core
// semantic and its singleton-scheduled special case in one test (they are
// the same mechanism): editing a scheduled singleton definition's prompt is
// a ContinuationContract change, so automationApply must drop its
// continuity binding. The next occurrence then mints a brand-new
// ticket/session reservation (rather than reusing the pre-edit one) and
// validateAutomationContinuation must accept it — no "contract changed"
// refusal, and (per the hasPriorAutomationContinuityRun fix this rotation
// required) no spurious "ticket is missing" refusal either.
func TestAutomationApplyContractEditRotatesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Original prompt.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	// Contract edit: prompt changes.
	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Updated prompt.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after rotation")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding reservation after a contract edit, got ticket=%q session=%q (pre-edit was ticket-1/session-1)", run2.TicketID, run2.SessionID)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def2.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot2.Prompt, Launch: snapshot2.Launch, Location: snapshot2.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("post-rotation delivery should pass the continuation check, got: %v", err)
	}
}

// TestAutomationApplyNonContractEditPreservesContinuityBindings pins the
// other half of A1: editing only cron/catch_up (not Prompt/Launch/Location)
// leaves the ContinuationContract unchanged, so the binding — and its
// ticket/session ids — must survive the edit, and the next occurrence
// continues the same thread instead of starting a fresh one.
func TestAutomationApplyNonContractEditPreservesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Sweep.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	// Non-contract edit: cron and catch_up change, prompt/launch/location don't.
	v2 := scheduledDefinitionYAML(dir, "*/10 * * * *", "singleton", "skip", "Sweep.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected the occurrence to claim a new run")
	}
	if run2.TicketID != run1.TicketID || run2.SessionID != run1.SessionID {
		t.Fatalf("non-contract edit should preserve the binding, got ticket=%q session=%q, want ticket=%q session=%q", run2.TicketID, run2.SessionID, run1.TicketID, run1.SessionID)
	}
}

// TestAutomationApplyPreservesPinnedSnapshotOfAlreadyClaimedRun pins the A1
// investigation finding: a run's snapshot_json is captured once at claim
// time (automation.Effective) and deliverAutomationRun/validateAutomationContinuation
// read only that pinned copy, never a live re-read of the current spec. So a
// run claimed before a contract-changing edit keeps delivering under its own
// old snapshot regardless of any edit made after it was claimed — apply
// never mutates an existing run's snapshot_json.
func TestAutomationApplyPreservesPinnedSnapshotOfAlreadyClaimedRun(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "fresh", "latest", "Original prompt.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "fresh", "latest", "Updated prompt.")
	if _, err := d.automationApply(v2); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	reloaded, err := s.GetAutomationRun(run1.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloaded run=%#v err=%v", reloaded, err)
	}
	var pinned automation.Snapshot
	if err := json.Unmarshal([]byte(reloaded.SnapshotJSON), &pinned); err != nil {
		t.Fatal(err)
	}
	if pinned.Prompt != "Original prompt." {
		t.Fatalf("apply mutated an already-claimed run's pinned snapshot: prompt=%q, want %q", pinned.Prompt, "Original prompt.")
	}
}

// TestAutomationApplyRevertAllowsFreshThreadWhenOldTicketSurvives pins
// scenario 3 of the A1-fix matrix: edit A→B→A (revert). The reverted
// contract now equals the original A thread's (run1/ticket T1) contract
// again, but T1's own ticket is still alive — it was merely rotated away
// from, not swept. A fresh post-revert thread (run3/ticket T3) must be
// allowed to deliver: hasPriorAutomationContinuityRun must not treat "some
// same-contract history exists" as disqualifying on its own.
func TestAutomationApplyRevertAllowsFreshThreadWhenOldTicketSurvives(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	// T1 stays alive throughout — it was rotated away from, never swept.
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	// Edit to B: rotates the binding.
	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt B.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, _, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run2.ID, "{}", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run2.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run2.SessionID, AutomationRunID: run2.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Revert back to A: rotates the binding again. The reverted contract
	// now equals run1's (T1's) contract.
	v3 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def3, err := d.automationApply(v3)
	if err != nil {
		t.Fatalf("apply v3 (revert): %v", err)
	}
	run3, fresh, err := s.ClaimScheduledAutomationRun(def3.ID, "schedule:3", "singleton", def3.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3", TicketID: "ticket-3", SessionID: "session-3", WorkspaceID: "workspace-3", PaneID: "pane-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after the revert rotation")
	}

	req := automation.WorkRequest{RunID: run3.ID, DefinitionID: def3.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot1.Prompt, Launch: snapshot1.Launch, Location: snapshot1.Location, IDs: automation.DeliveryIDs{TicketID: run3.TicketID, SessionID: run3.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("revert with the old same-contract thread's ticket (T1) still alive must be allowed, got: %v", err)
	}
}

// TestAutomationApplyRevertRefusesWhenOldTicketWasSwept pins scenario 4 of
// the A1-fix matrix: edit A→B→A (revert), but the original A thread's
// ticket (T1) was itself later swept (removed) rather than merely rotated
// away from. hasPriorAutomationContinuityRun must still refuse in this case
// — "refuse if ANY prior same-contract thread's own ticket is missing", not
// "allow if any prior same-contract thread's ticket happens to be alive".
func TestAutomationApplyRevertRefusesWhenOldTicketWasSwept(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	// T1 is deliberately never created here (or was created and later
	// removed by a sweep) — simulate the "genuinely gone" case.

	// Edit to B: rotates the binding.
	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt B.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, _, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run2.ID, "{}", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run2.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run2.SessionID, AutomationRunID: run2.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Revert back to A: rotates the binding again. The reverted contract
	// now equals run1's (T1's) contract, but T1 was swept.
	v3 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def3, err := d.automationApply(v3)
	if err != nil {
		t.Fatalf("apply v3 (revert): %v", err)
	}
	run3, fresh, err := s.ClaimScheduledAutomationRun(def3.ID, "schedule:3", "singleton", def3.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3", TicketID: "ticket-3", SessionID: "session-3", WorkspaceID: "workspace-3", PaneID: "pane-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after the revert rotation")
	}

	req := automation.WorkRequest{RunID: run3.ID, DefinitionID: def3.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot1.Prompt, Launch: snapshot1.Launch, Location: snapshot1.Location, IDs: automation.DeliveryIDs{TicketID: run3.TicketID, SessionID: run3.SessionID}}
	if err := d.validateAutomationContinuation(req); err == nil {
		t.Fatal("expected refusal: the reverted contract's original thread (T1) had its own ticket swept")
	}
}

// scheduledDefinitionYAMLWithModel is scheduledDefinitionYAML plus an
// explicit launch model, for tests that need to vary ContinuationContract's
// Launch field specifically while holding Prompt and Location fixed. Codex
// (HasModelPin/HasEffortPin) accepts any model string without an allowlist,
// so this doesn't need to name a real model.
func scheduledDefinitionYAMLWithModel(dir, cron, continuity, catchUp, prompt, model string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
enabled: true
trigger:
  type: scheduled
  schedule: {cron: %q, time_zone: UTC}
prompt: %s
launch: {driver: codex, model: %s}
location: {type: directory, path: %s}
policy: {continuity: %s, catch_up: %s}
`, cron, prompt, model, dir, continuity, catchUp)
}

// TestAutomationApplyLaunchEditRotatesContinuityBindings pins the Launch
// third of ContinuationContract.Equal's comparison — until now only Prompt
// was exercised at apply time. Editing just the launch model (prompt and
// location held fixed) must still be treated as a contract change and rotate
// the continuity binding, exactly like TestAutomationApplyContractEditRotatesContinuityBindings
// does for Prompt.
func TestAutomationApplyLaunchEditRotatesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAMLWithModel(dir, "*/5 * * * *", "singleton", "latest", "Sweep.", "fast")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	// Contract edit: launch model changes, prompt and location don't.
	v2 := scheduledDefinitionYAMLWithModel(dir, "*/5 * * * *", "singleton", "latest", "Sweep.", "slow")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot1.ContinuationContract().Equal(snapshot2.ContinuationContract()) {
		t.Fatal("expected a launch model change to be a ContinuationContract change")
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after rotation")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding reservation after a launch edit, got ticket=%q session=%q (pre-edit was ticket-1/session-1)", run2.TicketID, run2.SessionID)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def2.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot2.Prompt, Launch: snapshot2.Launch, Location: snapshot2.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("post-rotation delivery should pass the continuation check, got: %v", err)
	}
}

// TestAutomationApplyLocationEditRotatesContinuityBindings pins the Location
// third of ContinuationContract.Equal's comparison. Editing just the
// location path (prompt and launch held fixed) must still be treated as a
// contract change and rotate the continuity binding.
func TestAutomationApplyLocationEditRotatesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(t.TempDir(), "*/5 * * * *", "singleton", "latest", "Sweep.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	// Contract edit: location path changes (a different directory), prompt
	// and launch don't.
	v2 := scheduledDefinitionYAML(t.TempDir(), "*/5 * * * *", "singleton", "latest", "Sweep.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot1.ContinuationContract().Equal(snapshot2.ContinuationContract()) {
		t.Fatal("expected a location path change to be a ContinuationContract change")
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after rotation")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding reservation after a location edit, got ticket=%q session=%q (pre-edit was ticket-1/session-1)", run2.TicketID, run2.SessionID)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def2.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot2.Prompt, Launch: snapshot2.Launch, Location: snapshot2.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("post-rotation delivery should pass the continuation check, got: %v", err)
	}
}
