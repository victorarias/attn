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
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, true, now)
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

func TestGitHubReviewReRequestDoesNotReuseWithdrawnUndeliveredBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, true, now)
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

func TestGitHubReviewWithdrawalCancelsPendingRunAndReleasesEmptyBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, true, now)
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
	if err != nil || first == nil || first.State != "failed" || !strings.Contains(first.LastError, "withdrawn") {
		t.Fatalf("withdrawn run=%#v err=%v", first, err)
	}
	if current, err := s.GitHubReviewAutomationRunStillRequested(first.ID); err != nil || current {
		t.Fatalf("withdrawn run current=%v err=%v", current, err)
	}
	var bindings int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?`, def.ID, subject).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("empty binding count=%d err=%v", bindings, err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("re-request candidates=%#v err=%v", candidates, err)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, true, now)
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
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, true, now.Add(2*time.Minute)); err != nil {
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, true, now)
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, true, base)
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
