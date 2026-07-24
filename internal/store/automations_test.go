package store

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutomationClaimIsIdempotentAndSnapshotsRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "github.com/owner/repo#42", `{"scope":"tmp"}`, def.Revision, `{"prompt":"first"}`, now, ids)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	other := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "", `{"scope":"changed"}`, def.Revision, `{"prompt":"changed"}`, now.Add(time.Minute), other)
	if err != nil || created {
		t.Fatalf("duplicate claim created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.TicketID != first.TicketID || second.SnapshotJSON != `{"prompt":"first"}` {
		t.Fatalf("duplicate returned different run: %#v", second)
	}
	occurrence, err := s.GetAutomationOccurrence(first.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.SubjectKey != "github.com/owner/repo#42" || occurrence.PayloadJSON != `{"scope":"tmp"}` {
		t.Fatalf("occurrence = %#v err=%v", occurrence, err)
	}
}

func TestScheduledAutomationClaimIsIdempotent(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	key := "scheduled:2026-07-20T03:00:00Z"
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, key, "", def.Revision, `{"provider":"schedule"}`, `{"prompt":"sweep"}`, now, ids)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	other := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, key, "", def.Revision, `{"provider":"schedule","changed":true}`, `{"prompt":"changed"}`, now.Add(time.Minute), other)
	if err != nil || created {
		t.Fatalf("duplicate claim created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.TicketID != first.TicketID || second.SnapshotJSON != `{"prompt":"sweep"}` {
		t.Fatalf("duplicate returned different run: %#v", second)
	}
	var occurrenceCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM automation_occurrences WHERE definition_id=? AND provider='schedule'`, def.ID).Scan(&occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != 1 {
		t.Fatalf("occurrence count=%d, want 1", occurrenceCount)
	}
}

func TestScheduledAutomationClaimRejectsStaleRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	// A second apply bumps the revision, simulating the definition changing
	// between the caller's revision read and this claim's transaction.
	if _, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly","edited":true}`, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, ids); err == nil {
		t.Fatal("expected stale revision claim to be rejected")
	}
	if occurrence, err := s.GetAutomationOccurrence("occ-1"); err != nil || occurrence != nil {
		t.Fatalf("rejected claim left an occurrence: %#v err=%v", occurrence, err)
	}
	if run, err := s.GetAutomationRun("run-1"); err != nil || run != nil {
		t.Fatalf("rejected claim persisted a run: %#v err=%v", run, err)
	}
}

// TestScheduledAutomationClaimRejectsDisabledDefinition pins the store-level
// guard itself (Fix 4b): there is already a daemon-level test that a disabled
// definition never reaches the claim at all, but this proves
// ClaimScheduledAutomationRun refuses on its own and leaves no occurrence or
// run rows, matching the enabled-check every other claim path relies on.
func TestScheduledAutomationClaimRejectsDisabledDefinition(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, false, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, ids); err == nil {
		t.Fatal("expected a disabled definition's claim to be rejected")
	}
	if occurrence, err := s.GetAutomationOccurrence("occ-1"); err != nil || occurrence != nil {
		t.Fatalf("rejected claim left an occurrence: %#v err=%v", occurrence, err)
	}
	if run, err := s.GetAutomationRun("run-1"); err != nil || run != nil {
		t.Fatalf("rejected claim persisted a run: %#v err=%v", run, err)
	}
}

func TestScheduledAutomationDifferentInstantsClaimDifferentRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID == first.ID {
		t.Fatalf("different instants claimed the same run: %#v", second)
	}
}

func TestScheduledAutomationSingletonContinuityReusesBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{ID: first.TicketID, Title: "Nightly", Status: TicketStatusWorking, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:nightly", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID == first.ID {
		t.Fatal("second occurrence should claim a distinct run")
	}
	if second.TicketID != first.TicketID || second.SessionID != first.SessionID || second.WorkspaceID != first.WorkspaceID || second.PaneID != first.PaneID {
		t.Fatalf("singleton continuity did not reuse binding IDs: first=%#v second=%#v", first, second)
	}
}

// TestAutomationContinuityBindingLifecycleReleaseThenReclaim pins the v2
// append-only binding model: a claim creates (or reuses) the ACTIVE row for
// (definition, continuity_key); releasing it flips status/reason without
// deleting the row; and a later claim for the same key appends a fresh
// active row rather than resurrecting the released one.
func TestAutomationContinuityBindingLifecycleReleaseThenReclaim(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:1", "singleton", def.Revision, `{}`, `{}`, now, firstIDs); err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	binding, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
	if err != nil || binding == nil || binding.TicketID != firstIDs.TicketID || binding.Status != AutomationBindingStatusActive {
		t.Fatalf("active binding = %#v err=%v", binding, err)
	}
	firstBindingID := binding.ID

	// Deliver the first run so a second claim isn't refused by the
	// undelivered-predecessor guard (a separate protection, exercised
	// elsewhere) — this test is about binding release/reclaim, not that guard.
	if err := s.MarkAutomationRunDelivered(firstIDs.RunID, `{}`, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := s.ReleaseAutomationContinuityBinding(def.ID, "singleton", AutomationBindingReleasedTicketSwept, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if released, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton"); err != nil || released != nil {
		t.Fatalf("expected no active binding after release, got %#v err=%v", released, err)
	}
	// Releasing again (no active row left) is a no-op, not an error.
	if err := s.ReleaseAutomationContinuityBinding(def.ID, "singleton", AutomationBindingReleasedTicketSwept, now.Add(90*time.Second)); err != nil {
		t.Fatalf("re-release of an already-released binding errored: %v", err)
	}

	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2", "singleton", def.Revision, `{}`, `{}`, now.Add(2*time.Minute), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.TicketID != secondIDs.TicketID {
		t.Fatalf("second claim reused a released binding instead of a fresh one: %#v", second)
	}
	fresh, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
	if err != nil || fresh == nil || fresh.ID == firstBindingID || fresh.TicketID != secondIDs.TicketID {
		t.Fatalf("fresh active binding = %#v err=%v (first id %s)", fresh, err, firstBindingID)
	}

	var releasedStatus, releasedReason string
	if err := s.db.QueryRow(`SELECT status,released_reason FROM automation_continuity_bindings WHERE id=?`, firstBindingID).Scan(&releasedStatus, &releasedReason); err != nil {
		t.Fatal(err)
	}
	if releasedStatus != AutomationBindingStatusReleased || releasedReason != AutomationBindingReleasedTicketSwept {
		t.Fatalf("original binding row = status=%s reason=%s, want released/%s", releasedStatus, releasedReason, AutomationBindingReleasedTicketSwept)
	}
}

// TestAutomationContinuityBindingUniqueActiveIndexRejectsSecondActiveRow pins
// idx_automation_bindings_active: at most one active row may exist per
// (definition_id, continuity_key), enforced at the schema level so a bug in
// the get-or-create path can never silently create two live claimants for
// the same thread.
func TestAutomationContinuityBindingUniqueActiveIndexRejectsSecondActiveRow(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	nowRaw := formatTicketTime(now)
	if _, err := s.db.Exec(`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"binding-1", def.ID, "singleton", "ticket-1", "session-1", "workspace-1", "pane-1", AutomationBindingStatusActive, nowRaw, nowRaw); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"binding-2", def.ID, "singleton", "ticket-2", "session-2", "workspace-2", "pane-2", AutomationBindingStatusActive, nowRaw, nowRaw)
	if err == nil {
		t.Fatal("expected a second active binding row for the same (definition, continuity_key) to be rejected")
	}
}

// TestMarkAutomationRunCancelledSetsStateAndReason pins the cancelled+reason
// terminal transition: it sets state and cancel_reason only, leaving
// last_error and every other column untouched, since cancellation is not a
// delivery failure.
func TestMarkAutomationRunCancelledSetsStateAndReason(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunCancelled(run.ID, AutomationCancelReasonDefinitionDisabled, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.GetAutomationRun(run.ID)
	if err != nil || cancelled == nil || cancelled.State != AutomationRunStateCancelled || cancelled.CancelReason != AutomationCancelReasonDefinitionDisabled {
		t.Fatalf("cancelled run = %#v err=%v", cancelled, err)
	}
	if cancelled.LastError != "" {
		t.Fatalf("cancellation set last_error: %q", cancelled.LastError)
	}
}

// TestListPrunableAndTerminalAutomationRunsIncludeCancelledRuns pins that a
// cancelled run is terminal exactly like delivered/failed for both the
// retention sweep and the on-demand cleanup listing — it shares failed's
// retention window rather than having its own.
func TestListPrunableAndTerminalAutomationRunsIncludeCancelledRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunCancelled(run.ID, AutomationCancelReasonDefinitionDisabled, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.ListTerminalAutomationRuns(def.ID)
	if err != nil || len(terminal) != 1 || terminal[0].ID != run.ID {
		t.Fatalf("terminal runs = %#v err=%v", terminal, err)
	}
	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil || len(prunable) != 1 || prunable[0].ID != run.ID {
		t.Fatalf("prunable runs = %#v err=%v", prunable, err)
	}
}

// TestListWithdrawnGitHubReviewUndeliveredRunsIncludesCancelledReviewWithdrawnNotFailed
// pins the v2 undelivered-withdrawal predicate: state=pending OR
// (state=cancelled AND cancel_reason=review_withdrawn) — the sentinel
// LastError match is gone. A run that failed for an ordinary delivery reason
// (not a withdrawal cancellation) must not be swept up here even once its
// review request is later withdrawn.
func TestListWithdrawnGitHubReviewUndeliveredRunsIncludesCancelledReviewWithdrawnNotFailed(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subjectA = "github.com/owner/repo#1"
	const subjectB = "github.com/owner/repo#2"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subjectA, subjectB}, now); err != nil {
		t.Fatal(err)
	}
	runA, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subjectA, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-a", OccurrenceID: "occ-a", TicketID: "ticket-a", SessionID: "session-a", WorkspaceID: "workspace-a", PaneID: "pane-a",
	})
	if err != nil || !created {
		t.Fatalf("claim A created=%v err=%v", created, err)
	}
	runB, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subjectB, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-b", OccurrenceID: "occ-b", TicketID: "ticket-b", SessionID: "session-b", WorkspaceID: "workspace-b", PaneID: "pane-b",
	})
	if err != nil || !created {
		t.Fatalf("claim B created=%v err=%v", created, err)
	}
	// B fails for an ordinary (non-withdrawal) delivery reason before either
	// request is withdrawn.
	if err := s.MarkAutomationRunFailed(runB.ID, "spawn unavailable", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Both requests are withdrawn.
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Only A's outcome is a withdrawal cancellation (mirrors the daemon's
	// cancelWithdrawnAutomationRun) — B stays failed for its own reason.
	if err := s.MarkAutomationRunCancelled(runA.ID, AutomationCancelReasonReviewWithdrawn, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.ListWithdrawnGitHubReviewUndeliveredRuns(def.ID, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(withdrawn) != 1 || withdrawn[0].ID != runA.ID {
		t.Fatalf("withdrawn runs = %#v, want only cancelled run %s", withdrawn, runA.ID)
	}
}

func TestScheduledAutomationSingletonContinuityBlocksUndeliveredPredecessor(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now, firstIDs); err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	// No ticket was ever created for the first run, so the second occurrence's
	// claim must refuse rather than reuse a binding whose ticket is missing.
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	if _, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), secondIDs); err == nil {
		t.Fatal("expected second claim to be rejected while the first run has no ticket yet")
	}
}

func TestAutomationScheduleCursorGetSetRoundtrip(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetAutomationScheduleCursor(def.ID); err != nil || ok {
		t.Fatalf("missing cursor ok=%v err=%v", ok, err)
	}
	instant := time.Date(2026, 7, 20, 3, 0, 0, 123000000, time.UTC)
	if err := s.SetAutomationScheduleCursor(def.ID, instant); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok {
		t.Fatalf("roundtrip cursor ok=%v err=%v", ok, err)
	}
	if !got.Equal(instant) {
		t.Fatalf("cursor=%v, want %v", got, instant)
	}
	later := instant.Add(time.Hour)
	if err := s.SetAutomationScheduleCursor(def.ID, later); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !got.Equal(later) {
		t.Fatalf("advanced cursor=%v ok=%v err=%v", got, ok, err)
	}
}

func TestListAutomationRunsWithOccurrenceKeysOrdersNewestFirstWithLimit(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(requestID string, at time.Time) *AutomationRun {
		ids := AutomationRunReservation{
			RunID:        "run-" + requestID,
			OccurrenceID: "occ-" + requestID,
			TicketID:     "ticket-" + requestID,
			SessionID:    "session-" + requestID,
			WorkspaceID:  "workspace-" + requestID,
			PaneID:       "pane-" + requestID,
		}
		run, created, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, at, ids)
		if err != nil || !created {
			t.Fatalf("claim %s created=%v err=%v", requestID, created, err)
		}
		return run
	}
	// Distinct clock-injected timestamps, not wall-clock spacing, so ordering
	// is deterministic regardless of test execution speed.
	seed("req-1", base)
	second := seed("req-2", base.Add(time.Minute))
	third := seed("req-3", base.Add(2*time.Minute))

	runs, err := s.ListAutomationRunsWithOccurrenceKeys(def.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs)=%d, want 2: %#v", len(runs), runs)
	}
	if runs[0].ID != third.ID || runs[0].OccurrenceKey != "manual:req-3" {
		t.Fatalf("newest run = %#v, want run %s with occurrence_key manual:req-3", runs[0], third.ID)
	}
	if runs[1].ID != second.ID || runs[1].OccurrenceKey != "manual:req-2" {
		t.Fatalf("second-newest run = %#v, want run %s with occurrence_key manual:req-2", runs[1], second.ID)
	}
	if !runs[0].CreatedAt.After(runs[1].CreatedAt) {
		t.Fatalf("runs not newest-first by created_at: %v then %v", runs[0].CreatedAt, runs[1].CreatedAt)
	}
}

func TestLatestAutomationRunPerDefinitionPicksNewestPerDefinitionAndOmitsZeroRunDefinitions(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	defA, err := s.UpsertAutomationDefinition("def-a", "A", `{"id":"def-a"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	defB, err := s.UpsertAutomationDefinition("def-b", "B", `{"id":"def-b"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	defEmpty, err := s.UpsertAutomationDefinition("def-empty", "Empty", `{"id":"def-empty"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	_ = defEmpty
	seed := func(defID, requestID string, at time.Time) *AutomationRun {
		ids := AutomationRunReservation{
			RunID:        "run-" + defID + "-" + requestID,
			OccurrenceID: "occ-" + defID + "-" + requestID,
			TicketID:     "ticket-" + defID + "-" + requestID,
			SessionID:    "session-" + defID + "-" + requestID,
			WorkspaceID:  "workspace-" + defID + "-" + requestID,
			PaneID:       "pane-" + defID + "-" + requestID,
		}
		run, created, err := s.ClaimManualAutomationRun(defID, requestID, "", `{}`, 1, `{}`, at, ids)
		if err != nil || !created {
			t.Fatalf("claim %s/%s created=%v err=%v", defID, requestID, created, err)
		}
		return run
	}
	seed(defA.ID, "a-1", base)
	newestA := seed(defA.ID, "a-2", base.Add(time.Minute))
	newestB := seed(defB.ID, "b-1", base.Add(30*time.Second))

	latest, err := s.LatestAutomationRunPerDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("len(latest)=%d, want 2: %#v", len(latest), latest)
	}
	got, ok := latest[defA.ID]
	if !ok || got.ID != newestA.ID || got.OccurrenceKey != "manual:a-2" {
		t.Fatalf("def-a latest = %#v (ok=%v), want run %s with occurrence_key manual:a-2", got, ok, newestA.ID)
	}
	got, ok = latest[defB.ID]
	if !ok || got.ID != newestB.ID || got.OccurrenceKey != "manual:b-1" {
		t.Fatalf("def-b latest = %#v (ok=%v), want run %s with occurrence_key manual:b-1", got, ok, newestB.ID)
	}
	if _, ok := latest[defEmpty.ID]; ok {
		t.Fatalf("def-empty should have no entry, got %#v", latest[defEmpty.ID])
	}
}

func TestListPendingAutomationRunsIncludesScheduledProvider(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	pending, err := s.ListPendingAutomationRuns()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range pending {
		if r.ID == run.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending runs=%#v, missing claimed schedule run %s", pending, run.ID)
	}
}

func TestEnsureAutomationTicketAdoptsByRun(t *testing.T) {
	s := New()
	now := time.Now()
	ticket := Ticket{ID: "auto-run-one", Title: "Run", Status: TicketStatusWorking, Assignee: "session-1", AutomationRunID: "run-1"}
	first, err := s.EnsureAutomationTicket(ticket, "automation:cleanup", TicketRoleChiefOfStaff, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnsureAutomationTicket(ticket, "automation:cleanup", TicketRoleChiefOfStaff, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("adopted %q want %q", second.ID, first.ID)
	}
	events, err := s.TicketEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Author != "automation:cleanup" {
		t.Fatalf("events=%#v", events)
	}
	got, err := s.GetTicketByAutomationRunID("run-1")
	if err != nil || got == nil || got.Assignee != "session-1" {
		t.Fatalf("reverse lookup=%#v err=%v", got, err)
	}
}

func TestGitHubReviewEdgeRetriesThenReusesContinuityBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	subject := "github.com/owner/repo#42"
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now)
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("first reconcile = %#v err=%v", candidates, err)
	}
	// A detail-fetch failure leaves the same edge eligible instead of losing it
	// or manufacturing another occurrence.
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("retry reconcile = %#v err=%v", candidates, err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{"head_sha":"one"}`, `{"prompt":"review"}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if first.TicketID != firstIDs.TicketID || first.SessionID != firstIDs.SessionID {
		t.Fatalf("first run links = %#v", first)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{ID: first.TicketID, Title: "Review", Status: TicketStatusWorking, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:review", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("duplicate poll candidates = %#v err=%v", candidates, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(stale) != 0 {
		t.Fatalf("stale observation candidates = %#v err=%v", stale, err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(4*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 2 {
		t.Fatalf("re-request candidates = %#v err=%v", candidates, err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 2, def.Revision, `{"head_sha":"two"}`, `{"prompt":"review"}`, now.Add(4*time.Minute), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID != secondIDs.RunID || second.TicketID != first.TicketID || second.SessionID != first.SessionID || second.WorkspaceID != first.WorkspaceID || second.PaneID != first.PaneID {
		t.Fatalf("continuation did not reuse binding: first=%#v second=%#v", first, second)
	}
	occurrence, err := s.GetAutomationOccurrence(second.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != "review_requested:github.com/owner/repo#42:2" {
		t.Fatalf("second occurrence = %#v err=%v", occurrence, err)
	}
}

func TestGitHubReviewAcceptedPendingRunRemainsRetryableWhileDemandIsActive(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	subject := "github.com/owner/repo#42"
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("first reconcile = %#v err=%v", candidates, err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}

	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("pending retry candidates = %#v err=%v", candidates, err)
	}
	needsClaim, err := s.AutomationReviewRequestNeedsClaim(def.ID, subject, 1)
	if err != nil || !needsClaim {
		t.Fatalf("pending run needs claim=%v err=%v", needsClaim, err)
	}
	retried, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now.Add(time.Minute), AutomationRunReservation{RunID: "unused"})
	if err != nil || created || retried.ID != run.ID {
		t.Fatalf("retry run=%#v created=%v err=%v", retried, created, err)
	}

	if err := s.MarkAutomationRunDelivered(run.ID, `{}`, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("delivered duplicate candidates = %#v err=%v", candidates, err)
	}
}

func TestGitHubReviewReRequestDoesNotReuseWithdrawnUndeliveredBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	first, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunFailed(first.ID, "review withdrawn", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("second candidates=%#v err=%v", candidates, err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "unused-ticket", SessionID: "unused-session", WorkspaceID: "unused-workspace", PaneID: "unused-pane"}
	second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, candidates[0].Cycle, def.Revision, `{}`, `{}`, now.Add(2*time.Minute), secondIDs)
	if err != nil || !created || second == nil {
		t.Fatalf("re-request claim run=%#v created=%v err=%v", second, created, err)
	}
	if second.TicketID != secondIDs.TicketID || second.SessionID != secondIDs.SessionID || second.TicketID == first.TicketID {
		t.Fatalf("re-request reused withdrawn empty binding: first=%#v second=%#v", first, second)
	}
}

func TestGitHubReviewWithdrawalExposesPendingRunAndReleasesEmptyBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if current, err := s.GitHubReviewAutomationRunStillRequested(first.ID); err != nil || !current {
		t.Fatalf("accepted run current=%v err=%v", current, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	first, err = s.GetAutomationRun(first.ID)
	if err != nil || first == nil || first.State != "pending" || first.LastError != "" {
		t.Fatalf("withdrawn run=%#v err=%v", first, err)
	}
	withdrawn, err := s.ListWithdrawnGitHubReviewUndeliveredRuns(def.ID, "github.com")
	if err != nil || len(withdrawn) != 1 || withdrawn[0].ID != first.ID {
		t.Fatalf("current withdrawn runs=%#v err=%v", withdrawn, err)
	}
	if current, err := s.GitHubReviewAutomationRunStillRequested(first.ID); err != nil || current {
		t.Fatalf("withdrawn run current=%v err=%v", current, err)
	}
	if err := s.MarkAutomationRunFailed(first.ID, "review withdrawn", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The binding row is never deleted, only released: its ticket never
	// existed, so ReconcileAutomationReviewRequests' deactivate step already
	// released it above.
	if binding, err := s.GetActiveAutomationContinuityBinding(def.ID, subject); err != nil || binding != nil {
		t.Fatalf("expected no active binding for the empty (never-ticketed) thread, got %#v err=%v", binding, err)
	}
	var releasedReason string
	if err := s.db.QueryRow(`SELECT released_reason FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?`, def.ID, subject).Scan(&releasedReason); err != nil {
		t.Fatal(err)
	}
	if releasedReason != AutomationBindingReleasedTicketSwept {
		t.Fatalf("released_reason = %q, want %q", releasedReason, AutomationBindingReleasedTicketSwept)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("re-request candidates=%#v err=%v", candidates, err)
	}
	withdrawn, err = s.ListWithdrawnGitHubReviewUndeliveredRuns(def.ID, "github.com")
	if err != nil || len(withdrawn) != 0 {
		t.Fatalf("old cycle remained cancellable after re-request: runs=%#v err=%v", withdrawn, err)
	}
	second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, candidates[0].Cycle, def.Revision, `{}`, `{}`, now.Add(2*time.Minute), AutomationRunReservation{
		RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2",
	})
	if err != nil || !created || second.TicketID != "ticket-2" || second.SessionID != "session-2" {
		t.Fatalf("re-request run=%#v created=%v err=%v", second, created, err)
	}
}

func TestGitHubReviewClaimAndTicketEventAreIdempotent(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	subject := "github.com/owner/repo#7"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-a", OccurrenceID: "occ-a", TicketID: "auto-run-a", SessionID: "session-a", WorkspaceID: "workspace-a", PaneID: "pane-a"}
	otherIDs := AutomationRunReservation{RunID: "run-b", OccurrenceID: "occ-b", TicketID: "auto-run-b", SessionID: "session-b", WorkspaceID: "workspace-b", PaneID: "pane-b"}
	type claimResult struct {
		run     *AutomationRun
		created bool
		err     error
	}
	results := make(chan claimResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, ids := range []AutomationRunReservation{firstIDs, otherIDs} {
		ids := ids
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids)
			results <- claimResult{run: run, created: created, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var first *AutomationRun
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
		if first == nil {
			first = result.run
		} else if result.run.ID != first.ID {
			t.Fatalf("concurrent claims returned different runs: %#v and %#v", first, result.run)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created claims = %d, want 1", createdCount)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{ID: first.TicketID, Title: "Review", Status: TicketStatusWorking, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:review", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	// Use the existing run to exercise transactional event dedupe without
	// requiring another provider cycle in this focused test.
	if err := s.EnsureAutomationContinuationTicket(first.TicketID, first.SessionID, first.ID, "/tmp/occ-1.json", "automation:review", now); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAutomationContinuationTicket(first.TicketID, first.SessionID, first.ID, "/tmp/occ-1.json", "automation:review", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	events, err := s.TicketEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%#v, want created plus one occurrence", events)
	}
	if !strings.Contains(events[1].Comment, "/tmp/occ-1.json") {
		t.Fatalf("occurrence event does not expose structured input path: %#v", events[1])
	}
}

// TestReenabledGitHubAutomationCatchesUpCurrentReviewDemand pins that
// UpsertAutomationDefinition never disturbs the enabled column: a definition
// disabled via SetAutomationEnabled stays disabled across an unrelated
// re-apply of the same spec (enabled has exactly one authority — the
// column), and re-enabling via SetAutomationEnabled still catches up current
// review demand exactly as before.
func TestReenabledGitHubAutomationCatchesUpCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids); err != nil || !created {
		t.Fatalf("initial claim created=%v err=%v", created, err)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// An unrelated re-apply of the same spec while disabled must not silently
	// re-enable it: enabled has exactly one authority now, the column.
	if reapplied, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	} else if reapplied.Enabled {
		t.Fatalf("re-apply re-enabled a disabled definition: %#v", reapplied)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, true, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(90*time.Second))
	if err != nil || len(stale) != 0 {
		t.Fatalf("pre-enable observation crossed enable fence: candidates=%#v err=%v", stale, err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 2 {
		t.Fatalf("re-enabled latest catch-up candidates=%#v err=%v", candidates, err)
	}
}

func TestContinuationOccurrenceRecordsOnTerminalTicketExactlyOnce(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	first, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{ID: first.TicketID, Title: "Review", Status: TicketStatusDone, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:review", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("second candidates=%#v err=%v", candidates, err)
	}
	second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, candidates[0].Cycle, def.Revision, `{}`, `{}`, now.Add(2*time.Minute), AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "unused-ticket", SessionID: "unused-session", WorkspaceID: "unused-workspace", PaneID: "unused-pane"})
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.TicketID != first.TicketID || second.SessionID != first.SessionID {
		t.Fatalf("second run did not reuse binding: first=%#v second=%#v", first, second)
	}
	if err := s.EnsureAutomationContinuationTicket(first.TicketID, first.SessionID, second.ID, "/tmp/occ-2.json", "automation:review", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAutomationContinuationTicket(first.TicketID, first.SessionID, second.ID, "/tmp/occ-2.json", "automation:review", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ticket, err := s.GetTicket(first.TicketID)
	if err != nil || ticket == nil || ticket.Status != TicketStatusDone || ticket.ClosedAt == nil {
		t.Fatalf("terminal ticket changed before delivery: ticket=%#v err=%v", ticket, err)
	}
	if len(ticket.Activity) != 1 {
		t.Fatalf("activity=%#v, want one occurrence comment", ticket.Activity)
	}
	if !strings.Contains(ticket.Activity[0].Comment, "/tmp/occ-2.json") {
		t.Fatalf("occurrence activity does not expose structured input path: %#v", ticket.Activity[0])
	}
	if removed, err := s.SweepExpiredTickets(now.Add(2*time.Hour), time.Hour); err != nil || removed != 1 {
		t.Fatalf("sweep removed=%d err=%v", removed, err)
	}
	var orphanEvents int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM automation_ticket_occurrence_events WHERE ticket_id=?`, first.TicketID).Scan(&orphanEvents); err != nil {
		t.Fatal(err)
	}
	if orphanEvents != 0 {
		t.Fatalf("sweep left %d automation occurrence event rows", orphanEvents)
	}
}

func TestGitHubReviewCursorOrdersObservationsWithinOneSecond(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, base)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, base.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, base.Add(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, base.Add(150*time.Millisecond))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("stale same-second candidates=%#v err=%v", candidates, err)
	}
	var active int
	if err := s.db.QueryRow(`SELECT active FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, def.ID, subject).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("stale same-second observation reactivated withdrawn demand")
	}
}

func TestSetAutomationEnabledFlipsStateAndIsIdempotent(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("daily-check", "Daily check", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}

	// Re-applying the current state is a no-op: changed=false, no update.
	got, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(time.Minute))
	if err != nil || changed || got == nil || !got.Enabled {
		t.Fatalf("no-op enable: def=%#v changed=%v err=%v", got, changed, err)
	}
	if !got.UpdatedAt.Equal(def.UpdatedAt) {
		t.Fatalf("no-op enable touched updated_at: got %v, want %v", got.UpdatedAt, def.UpdatedAt)
	}

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(2*time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable: def=%#v changed=%v err=%v", disabled, changed, err)
	}

	againNoOp, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(3*time.Minute))
	if err != nil || changed || againNoOp == nil || againNoOp.Enabled {
		t.Fatalf("no-op disable: def=%#v changed=%v err=%v", againNoOp, changed, err)
	}

	reenabled, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(4*time.Minute))
	if err != nil || !changed || reenabled == nil || !reenabled.Enabled {
		t.Fatalf("re-enable: def=%#v changed=%v err=%v", reenabled, changed, err)
	}

	missing, changed, err := s.SetAutomationEnabled("does-not-exist", true, now)
	if err != nil || changed || missing != nil {
		t.Fatalf("unknown id: def=%#v changed=%v err=%v", missing, changed, err)
	}
}

// TestSetAutomationEnabledReenableCatchesUpCurrentReviewDemand is the
// SetAutomationEnabled parity of TestReenabledGitHubAutomationCatchesUpCurrentReviewDemand
// above: the disable/re-enable cycle drives through SetAutomationEnabled
// instead of UpsertAutomationDefinition, and must produce the identical
// review-request-edge-clear + provider-cursor-fence side effects.
func TestSetAutomationEnabledReenableCatchesUpCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids); err != nil || !created {
		t.Fatalf("initial claim created=%v err=%v", created, err)
	}
	if _, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("disable: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(2*time.Minute)); err != nil || !changed {
		t.Fatalf("re-enable: changed=%v err=%v", changed, err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(90*time.Second))
	if err != nil || len(stale) != 0 {
		t.Fatalf("pre-enable observation crossed enable fence: candidates=%#v err=%v", stale, err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 2 {
		t.Fatalf("re-enabled latest catch-up candidates=%#v err=%v", candidates, err)
	}
}

// TestSetAutomationEnabledNeverTouchesSpecOrRevision pins the v2 contract:
// enabled has exactly one authority, the column. Unlike v1 (where the
// toggle also rewrote spec_json/spec_yaml and bumped revision to keep them
// in sync), a real enabled transition here must leave spec_json and
// revision completely untouched — there is no spec-side echo of enabled
// left to keep in sync, so nothing to bump a stale-save guard for.
func TestSetAutomationEnabledNeverTouchesSpecOrRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	const spec = `{"id":"nightly-sweep"}`
	def, err := s.UpsertAutomationDefinition("nightly-sweep", "Nightly sweep", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	startRevision := def.Revision

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable: def=%#v changed=%v err=%v", disabled, changed, err)
	}
	if disabled.Revision != startRevision {
		t.Fatalf("disable bumped revision: got %d, want unchanged %d", disabled.Revision, startRevision)
	}
	if disabled.SpecJSON != spec {
		t.Fatalf("disable touched spec_json: got %q, want unchanged %q", disabled.SpecJSON, spec)
	}

	// The no-op re-application of the same state must not bump revision either.
	noop, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(2*time.Minute))
	if err != nil || changed || noop == nil {
		t.Fatalf("no-op disable: def=%#v changed=%v err=%v", noop, changed, err)
	}
	if noop.Revision != disabled.Revision {
		t.Fatalf("no-op bumped revision: got %d, want %d", noop.Revision, disabled.Revision)
	}
}

// TestSetAutomationEnabledDegradesGracefullyOnCorruptSpecJSON pins that a row
// whose spec_json cannot be parsed (a corrupt or otherwise unexpected value —
// this store method never validates what UpsertAutomationDefinition was
// given) never blocks the toggle: enabling, and especially disabling — a
// safety control for turning off an unattended cron — must always take
// effect on the column, since the toggle never reads or writes spec_json
// at all.
func TestSetAutomationEnabledDegradesGracefullyOnCorruptSpecJSON(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	const corruptJSON = `not-json`
	def, err := s.UpsertAutomationDefinition("corrupt-spec", "Corrupt spec", corruptJSON, now)
	if err != nil {
		t.Fatal(err)
	}

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable must still succeed on a corrupt spec: def=%#v changed=%v err=%v", disabled, changed, err)
	}
	if disabled.Revision != def.Revision {
		t.Fatalf("disable bumped revision: got %d, want unchanged %d", disabled.Revision, def.Revision)
	}
	if disabled.SpecJSON != corruptJSON {
		t.Fatalf("corrupt spec_json was touched instead of left alone: %s", disabled.SpecJSON)
	}
}

// TestListPrunableAutomationRunsProtectsBoundThreadOrigin pins the fix for
// Slice 7 PR A's continuity-bricking blocker: a thread's ticket.automation_run_id
// is written once at ticket creation and never updated, so it permanently
// points at the thread's oldest (origin) run — exactly the run retention
// would otherwise prune first. As long as the thread's continuity binding
// still exists (the thread can still deliver again), the origin run must
// never be a prunable candidate, however far outside the keep window or age
// floor it is; pruning it would make automationContinuationOrigin
// (internal/daemon/automations.go) fail forever on the next occurrence.
func TestListPrunableAutomationRunsProtectsBoundThreadOrigin(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-origin", OccurrenceID: "occ-origin", TicketID: "ticket-origin", SessionID: "session-origin", WorkspaceID: "workspace-origin", PaneID: "pane-origin"}
	origin, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:1", "singleton", def.Revision, `{}`, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim origin created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunDelivered(origin.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	// The thread's ticket permanently points at the origin run: the real
	// shape validateAutomationContinuation/automationContinuationOrigin rely on.
	if _, err := s.EnsureAutomationTicket(Ticket{ID: origin.TicketID, Title: "Nightly", Status: TicketStatusWorking, Assignee: origin.SessionID, AutomationRunID: origin.ID}, "automation:nightly", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range prunable {
		if r.ID == origin.ID {
			t.Fatalf("expected the bound thread's origin run to be protected, got it in the prunable set: %#v", prunable)
		}
	}
}

// TestListPrunableAutomationRunsStillPrunesNonContinuityRuns guards against
// over-broadening the fix above: every automation run (continuity or not)
// gets a ticket whose own automation_run_id is that run's own id, so an
// unjoined "any run referenced by any ticket" exclusion would protect
// essentially every run and silently turn retention into a no-op. Only the
// join through automation_continuity_bindings should narrow protection to
// threads that can still deliver again — an ordinary non-continuity run's
// own ticket never has a continuity binding, so it must remain prunable.
func TestListPrunableAutomationRunsStillPrunesNonContinuityRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-manual", OccurrenceID: "occ-manual", TicketID: "ticket-manual", SessionID: "session-manual", WorkspaceID: "workspace-manual", PaneID: "pane-manual"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	// This run's own ticket points back at itself, exactly like every
	// automation run's ticket does, with no continuity binding involved.
	if _, err := s.EnsureAutomationTicket(Ticket{ID: run.TicketID, Title: "Cleanup", Status: TicketStatusWorking, Assignee: run.SessionID, AutomationRunID: run.ID}, "automation:cleanup", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	if len(prunable) != 1 || prunable[0].ID != run.ID {
		t.Fatalf("expected the non-continuity run to remain prunable, got %#v", prunable)
	}
}

// TestSweepExpiredTicketsReleasesActiveContinuityBindings pins the other half
// of the fix ListPrunableAutomationRunsProtectsBoundThreadOrigin (above)
// relies on: a binding is only a temporary hold, not a permanent one. Once
// the ticket it documents ages past the TTL, SweepExpiredTickets must
// release the binding (status=released, reason=ticket_swept) in the same
// transaction — never delete the row, since bindings are append-only history
// in v2 — and GetActiveAutomationContinuityBinding must stop returning it so
// the next occurrence claims fresh. A within-TTL or still-open thread's
// binding must be left alone (still active).
func TestSweepExpiredTicketsReleasesActiveContinuityBindings(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	const ttl = 30 * 24 * time.Hour
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, base)
	if err != nil {
		t.Fatal(err)
	}

	bind := func(continuityKey string, closedAt time.Time, terminal bool) *AutomationRun {
		t.Helper()
		ids := AutomationRunReservation{RunID: "run-" + continuityKey, OccurrenceID: "occ-" + continuityKey, TicketID: "ticket-" + continuityKey, SessionID: "session-" + continuityKey, WorkspaceID: "workspace-" + continuityKey, PaneID: "pane-" + continuityKey}
		run, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:"+continuityKey, continuityKey, def.Revision, `{}`, `{}`, base, ids)
		if err != nil || !created {
			t.Fatalf("claim %s created=%v err=%v", continuityKey, created, err)
		}
		if err := s.MarkAutomationRunDelivered(run.ID, "{}", base); err != nil {
			t.Fatal(err)
		}
		status := TicketStatusWorking
		if terminal {
			status = TicketStatusDone
		}
		// EnsureAutomationTicket sets closed_at from the timestamp passed in
		// when the ticket is created directly in a terminal status (mirrors
		// TestFreshThreadAfterTicketSweepGetsItsOwnTicketNotTheOldOne's use of
		// the same mechanism), so a distinct closedAt per thread is enough to
		// place each on either side of the TTL without a separate SetTicketStatus
		// call.
		if _, err := s.EnsureAutomationTicket(Ticket{ID: run.TicketID, Title: "Nightly", Status: status, Assignee: run.SessionID, AutomationRunID: run.ID}, "automation:nightly", TicketRoleChiefOfStaff, closedAt); err != nil {
			t.Fatal(err)
		}
		return run
	}

	bind("swept", base.Add(-40*24*time.Hour), true) // terminal, 40d old: past the 30d TTL.
	bind("recent", base.Add(-5*24*time.Hour), true) // terminal, 5d old: within the TTL.
	bind("open", base.Add(-90*24*time.Hour), false) // never closed: not a sweep candidate regardless of age.

	removed, err := s.SweepExpiredTickets(base, ttl)
	if err != nil {
		t.Fatalf("SweepExpiredTickets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only swept)", removed)
	}

	// Bindings are append-only in v2 (docs/plans/2026-07-21-automations-v2-simplification.md
	// Data Model): a swept binding's row must still exist afterward, released
	// rather than deleted, so history survives. No exported store method lists
	// a binding by status, so query directly.
	bindingStatus := func(continuityKey string) (status, reason string, exists bool) {
		t.Helper()
		err := s.db.QueryRow(`SELECT status,released_reason FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?`, def.ID, continuityKey).Scan(&status, &reason)
		if err == sql.ErrNoRows {
			return "", "", false
		}
		if err != nil {
			t.Fatalf("query binding %s: %v", continuityKey, err)
		}
		return status, reason, true
	}
	if status, reason, exists := bindingStatus("swept"); !exists || status != AutomationBindingStatusReleased || reason != AutomationBindingReleasedTicketSwept {
		t.Fatalf("swept thread's binding = status=%q reason=%q exists=%v, want released/ticket_swept and still present", status, reason, exists)
	}
	if active, err := s.GetActiveAutomationContinuityBinding(def.ID, "swept"); err != nil || active != nil {
		t.Fatalf("swept thread's binding still active = %#v err=%v, want none", active, err)
	}
	if status, _, exists := bindingStatus("recent"); !exists || status != AutomationBindingStatusActive {
		t.Fatalf("within-TTL thread's binding = status=%q exists=%v, want still active", status, exists)
	}
	if status, _, exists := bindingStatus("open"); !exists || status != AutomationBindingStatusActive {
		t.Fatalf("open thread's binding = status=%q exists=%v, want still active (its ticket was never closed)", status, exists)
	}
}

// TestSweepExpiredTicketsUnblocksPruningOfBoundThreadOrigin is the chain
// TestListPrunableAutomationRunsProtectsBoundThreadOrigin's protection exists
// to eventually let go of: once the ticket that keeps a binding alive ages
// out, both AutomationSessionHasContinuityBinding and ListPrunableAutomationRuns
// must reflect the release, or the fix this PR makes is unproven — the guard
// would be permanent instead of TTL-bounded, which is the exact bug this
// branch exists to fix (see the "Fix brief" this test was written from).
func TestSweepExpiredTicketsUnblocksPruningOfBoundThreadOrigin(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-origin", OccurrenceID: "occ-origin", TicketID: "ticket-origin", SessionID: "session-origin", WorkspaceID: "workspace-origin", PaneID: "pane-origin"}
	origin, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:1", "singleton", def.Revision, `{}`, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim origin created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunDelivered(origin.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{ID: origin.TicketID, Title: "Nightly", Status: TicketStatusDone, Assignee: origin.SessionID, AutomationRunID: origin.ID}, "automation:nightly", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	far := now.Add(365 * 24 * time.Hour)

	// Before the TTL sweep: still protected, exactly like
	// TestListPrunableAutomationRunsProtectsBoundThreadOrigin pins.
	if bound, err := s.AutomationSessionHasContinuityBinding(origin.SessionID); err != nil || !bound {
		t.Fatalf("bound=%v err=%v, want bound before the sweep", bound, err)
	}
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range prunable {
		if r.ID == origin.ID {
			t.Fatalf("origin run already prunable before its ticket aged out: %#v", prunable)
		}
	}

	if removed, err := s.SweepExpiredTickets(now.Add(31*24*time.Hour), 30*24*time.Hour); err != nil || removed != 1 {
		t.Fatalf("sweep removed=%d err=%v", removed, err)
	}

	// After the sweep: the chain must actually close.
	if bound, err := s.AutomationSessionHasContinuityBinding(origin.SessionID); err != nil || bound {
		t.Fatalf("bound=%v err=%v, want released after the sweep", bound, err)
	}
	prunable, err = s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range prunable {
		if r.ID == origin.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the origin run to become prunable once its ticket aged out, got %#v", prunable)
	}
}

// TestUpsertAutomationDefinitionBumpsRevisionOnSpecJSONChangeOnly pins the
// v2 revision-bump condition now that spec_yaml storage is gone: a
// byte-identical reapply of spec_json is a no-op (revision must not move),
// and any change to spec_json bumps it. It also pins that an update never
// disturbs the enabled column (see UpsertAutomationDefinition's doc
// comment) — SetAutomationEnabled owns that transition exclusively now.
func TestUpsertAutomationDefinitionBumpsRevisionOnSpecJSONChangeOnly(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if def.Revision != 1 {
		t.Fatalf("initial revision = %d, want 1", def.Revision)
	}

	// A byte-identical reapply is a no-op: revision must not move.
	noop, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if noop.Revision != def.Revision {
		t.Fatalf("no-op reapply revision = %d, want unchanged %d", noop.Revision, def.Revision)
	}
	if !noop.Enabled {
		t.Fatalf("no-op reapply disturbed enabled: %#v", noop)
	}

	// A spec_json change bumps the revision.
	edited, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly","edited":true}`, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if edited.Revision != def.Revision+1 {
		t.Fatalf("spec_json edit revision = %d, want %d", edited.Revision, def.Revision+1)
	}
}
