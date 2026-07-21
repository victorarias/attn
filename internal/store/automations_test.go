package store

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutomationClaimIsIdempotentAndSnapshotsRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
	if err != nil {
		t.Fatal(err)
	}
	// A second apply bumps the revision, simulating the definition changing
	// between the caller's revision read and this claim's transaction.
	if _, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly","edited":true}`, "", true, now); err != nil {
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", false, now)
	if err != nil {
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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

// TestAutomationContinuityRunHistoryReturnsPriorRuns covers
// AutomationContinuityRunHistory directly: no history for a continuity key
// returns nothing; a prior run under the same key returns that run's own
// pinned snapshot_json paired with its own ticket_id, excluding the current
// run itself. The daemon (see hasPriorAutomationContinuityRun in
// internal/daemon/automations.go) is responsible for deciding what a
// returned entry *means* (by comparing its ContinuationContract to the
// current request and checking its own ticket's existence) — this test only
// pins what the store hands back.
func TestAutomationContinuityRunHistoryReturnsPriorRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
	if err != nil {
		t.Fatal(err)
	}

	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{"prompt":"v1"}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}

	// No history yet for a run's own continuity key excludes itself; nothing prior.
	history, err := s.AutomationContinuityRunHistory(def.ID, "singleton", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no prior history for the first run, got %#v", history)
	}

	if _, err := s.EnsureAutomationTicket(Ticket{ID: first.TicketID, Title: "Nightly", Status: TicketStatusWorking, Assignee: first.SessionID, AutomationRunID: first.ID}, "automation:nightly", TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{"prompt":"v1"}`, now.Add(24*time.Hour), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}

	history, err = s.AutomationContinuityRunHistory(def.ID, "singleton", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].SnapshotJSON != `{"prompt":"v1"}` || history[0].TicketID != "ticket-1" {
		t.Fatalf("expected the first run's own pinned snapshot+ticket as second's prior history, got %#v", history)
	}

	// Empty continuity key never has history to report.
	if history, err := s.AutomationContinuityRunHistory(def.ID, "", second.ID); err != nil || len(history) != 0 {
		t.Fatalf("expected no history for an empty continuity key, got %#v err=%v", history, err)
	}
}

func TestScheduledAutomationSingletonContinuityBlocksUndeliveredPredecessor(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, "", true, base)
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

func TestListPendingAutomationRunsIncludesScheduledProvider(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", true, now)
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
	if err := s.MarkAutomationRunFailed(first.ID, AutomationReviewWithdrawnError, now.Add(time.Minute)); err != nil {
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", true, now)
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
	if err := s.MarkAutomationRunFailed(first.ID, AutomationReviewWithdrawnError, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var bindings int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?`, def.ID, subject).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("empty binding count=%d err=%v", bindings, err)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, "", true, now)
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

func TestReenabledGitHubAutomationCatchesUpCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, "", true, now)
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
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, "", false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, "", true, now.Add(2*time.Minute)); err != nil {
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", true, base)
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
	def, err := s.UpsertAutomationDefinition("daily-check", "Daily check", `{}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, "", true, now)
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

// TestSweepExpiredTicketsCascadesToContinuityBindings pins the other half of
// the fix ListPrunableAutomationRunsProtectsBoundThreadOrigin (above) relies
// on: a binding is only a temporary hold, not a permanent one. Once the
// ticket it documents ages past the TTL, SweepExpiredTickets must release
// the binding in the same transaction — a within-TTL or still-open thread's
// binding must be left alone.
func TestSweepExpiredTicketsCascadesToContinuityBindings(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	const ttl = 30 * 24 * time.Hour
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, base)
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

	bindingExists := func(continuityKey string) bool {
		t.Helper()
		var exists int
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?)`, def.ID, continuityKey).Scan(&exists); err != nil {
			t.Fatalf("query binding %s: %v", continuityKey, err)
		}
		return exists != 0
	}
	if bindingExists("swept") {
		t.Fatal("swept thread's binding survived its ticket's sweep")
	}
	if !bindingExists("recent") {
		t.Fatal("within-TTL thread's binding was released early")
	}
	if !bindingExists("open") {
		t.Fatal("open thread's binding was released even though its ticket was never closed")
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
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "", true, now)
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

// TestUpsertAutomationDefinitionBumpsRevisionOnSpecYAMLOnlyChange pins the
// revision-bump condition against the class of edit this editor exists to
// make: comments live only in spec_yaml (never re-derived into spec_json),
// so a comment-only save leaves spec_json byte-identical while spec_yaml
// changes. Before this fix the bump condition only compared spec_json, so
// that edit persisted new YAML while leaving revision unchanged — and the
// daemon's stale-save guard (automationApplyWithGuards), which compares only
// revision, was structurally blind to it. This also pins the sibling
// contract: a byte-identical reapply of both spec_json and spec_yaml must
// still not bump.
func TestUpsertAutomationDefinitionBumpsRevisionOnSpecYAMLOnlyChange(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "prompt: sweep\n", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if def.Revision != 1 {
		t.Fatalf("initial revision = %d, want 1", def.Revision)
	}

	// A byte-identical reapply (same spec_json, same spec_yaml) is a no-op:
	// revision must not move.
	noop, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "prompt: sweep\n", true, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if noop.Revision != def.Revision {
		t.Fatalf("no-op reapply revision = %d, want unchanged %d", noop.Revision, def.Revision)
	}

	// spec_yaml changes (a comment added) while spec_json stays byte-identical:
	// this must still bump.
	commented, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, "# swept nightly\nprompt: sweep\n", true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if commented.Revision != def.Revision+1 {
		t.Fatalf("comment-only edit revision = %d, want %d", commented.Revision, def.Revision+1)
	}
	if commented.SpecYAML != "# swept nightly\nprompt: sweep\n" {
		t.Fatalf("comment-only edit spec_yaml = %q, want the new YAML persisted", commented.SpecYAML)
	}
}
