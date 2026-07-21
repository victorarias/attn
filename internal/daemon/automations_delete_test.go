package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// TestAutomationDeleteHappyPath pins A2's soft-delete side effects: a
// pending run is cancelled with reason definition_deleted (the same
// mechanism automationSetEnabled's disable path uses, with its own reason),
// the definition is soft-deleted (filtered out of
// GetAutomationDefinition), a change broadcast fires, and the run itself
// remains listable/inspectable — delete never touches run/occurrence/ticket
// rows or on-disk artifacts.
func TestAutomationDeleteHappyPath(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	broadcasts := automationBroadcastRecorder(d)

	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, time.Now(), store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}

	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatalf("automationDelete: %v", err)
	}

	got, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected a soft-deleted definition to be filtered from GetAutomationDefinition, got %#v", got)
	}

	reloadedRun, err := s.GetAutomationRun(run.ID)
	if err != nil || reloadedRun == nil {
		t.Fatalf("the run should remain listable after delete, got %#v err=%v", reloadedRun, err)
	}
	if reloadedRun.State != store.AutomationRunStateCancelled || reloadedRun.CancelReason != store.AutomationCancelReasonDefinitionDeleted {
		t.Fatalf("expected the pending run to be cancelled/definition_deleted by delete, got state=%q reason=%q", reloadedRun.State, reloadedRun.CancelReason)
	}

	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected the run to remain listable via ListAutomationRuns, got %#v err=%v", runs, err)
	}

	if len(broadcasts()) == 0 {
		t.Fatal("expected automationDelete to broadcast automations_changed")
	}
}

// TestAutomationDeleteNotFound pins the not-found error for an unknown id.
func TestAutomationDeleteNotFound(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	if err := d.automationDelete(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected an error deleting an unknown definition")
	}
}

// TestAutomationDeleteAlreadyDeleted pins the not-found error for a second
// delete of the same id.
func TestAutomationDeleteAlreadyDeleted(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := d.automationDelete(context.Background(), def.ID); err == nil {
		t.Fatal("expected an error deleting an already-deleted definition")
	}
}

// TestAutomationDeleteThenReapplyResurrects pins A2's resurrection path:
// re-applying the same definition id after a delete brings it back live,
// old runs from before the delete remain listable, and the binding is
// fresh (mirrors the A1 contract-rotation tests' assertion style — a
// resurrection is always treated as a contract change, per automationApply's
// "always rotate when resurrecting a soft-deleted definition" rule).
func TestAutomationDeleteThenReapplyResurrects(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Original prompt.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, `{"prompt":"v1"}`, now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	if err := d.automationDelete(context.Background(), def1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	def2, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("re-apply after delete: %v", err)
	}
	if def2.ID != def1.ID {
		t.Fatalf("expected re-apply to resurrect the same id, got %q want %q", def2.ID, def1.ID)
	}
	if !def2.Enabled {
		t.Fatal("expected the resurrected definition to be live/enabled")
	}

	reloadedDef, err := s.GetAutomationDefinition(def1.ID)
	if err != nil || reloadedDef == nil {
		t.Fatalf("resurrected definition should be visible again, got %#v err=%v", reloadedDef, err)
	}

	// The old run from before the delete is still listable.
	runs, err := s.ListAutomationRuns(def1.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != run1.ID {
		t.Fatalf("expected the pre-delete run to remain listable, got %#v err=%v", runs, err)
	}

	// The binding is fresh: a post-resurrection claim under the same
	// continuity key gets a new reservation, not run1's old one.
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, `{"prompt":"v1"}`, now.Add(24*time.Hour), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after resurrection")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding after resurrection, got ticket=%q session=%q", run2.TicketID, run2.SessionID)
	}
}

// TestAutomationDeleteClearsReviewEdgesBindingsAndFencesProviderCursors pins
// three of automationDelete's five store mutations that no other test
// exercises: DeleteAutomationReviewRequestEdges, DeleteAutomationContinuityBindings,
// and FenceAutomationProviderCursors. None of the three has a direct getter,
// so each is asserted through its own observable effect.
func TestAutomationDeleteClearsReviewEdgesBindingsAndFencesProviderCursors(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	raw := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Sweep.")
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Establish an active, unaccepted GitHub review-request edge — the state
	// DeleteAutomationReviewRequestEdges exists to clear. observedAt must be
	// after automationApply's own enable fence (set from the real wall clock),
	// or this observation would be rejected before ever creating the edge.
	const subject = "github.com/owner/repo#42"
	observedAt := time.Now()
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, observedAt); err != nil {
		t.Fatal(err)
	}
	if needsClaim, err := s.AutomationReviewRequestNeedsClaim(def.ID, subject, 1); err != nil || !needsClaim {
		t.Fatalf("fixture setup: review request needs claim = %v err=%v, want true", needsClaim, err)
	}

	// Establish a continuity binding — the state DeleteAutomationContinuityBindings
	// exists to clear.
	origin, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:1", "singleton", def.Revision, `{}`, `{}`, observedAt, store.AutomationRunReservation{
		RunID: "run-origin", OccurrenceID: "occ-origin", TicketID: "ticket-origin", SessionID: "session-origin", WorkspaceID: "workspace-origin", PaneID: "pane-origin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound, err := s.AutomationSessionHasContinuityBinding(origin.SessionID); err != nil || !bound {
		t.Fatalf("fixture setup: session bound = %v err=%v, want true", bound, err)
	}

	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatalf("automationDelete: %v", err)
	}

	if needsClaim, err := s.AutomationReviewRequestNeedsClaim(def.ID, subject, 1); err != nil || needsClaim {
		t.Fatalf("expected delete to clear the review-request edge, needsClaim=%v err=%v", needsClaim, err)
	}
	if bound, err := s.AutomationSessionHasContinuityBinding(origin.SessionID); err != nil || bound {
		t.Fatalf("expected delete to clear the continuity binding, bound=%v err=%v", bound, err)
	}

	// FenceAutomationProviderCursors: a stale observation from before the
	// delete must not resurrect the edge delete just cleared — mirrors the
	// re-enable fence idiom in TestSetAutomationEnabledReenableCatchesUpCurrentReviewDemand.
	// Reusing observedAt (rather than a fresh, later timestamp) proves the
	// block is the delete's own fence and not merely the earlier host cursor.
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, observedAt)
	if err != nil || len(stale) != 0 {
		t.Fatalf("pre-delete observation crossed the delete's fence: candidates=%#v err=%v", stale, err)
	}
	fresh, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, time.Now().Add(time.Hour))
	if err != nil || len(fresh) != 1 || fresh[0].SubjectKey != subject {
		t.Fatalf("expected a post-fence observation to see the review request again, candidates=%#v err=%v", fresh, err)
	}
}
