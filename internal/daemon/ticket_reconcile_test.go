package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// reconcileComments returns the attn-authored reconciliation comments (verdict
// or failure notes) on a ticket, oldest first.
func reconcileComments(t *testing.T, d *Daemon, ticketID string) []string {
	t.Helper()
	full, err := d.store.GetTicket(ticketID)
	if err != nil || full == nil {
		t.Fatalf("GetTicket %s: %v, %v", ticketID, full, err)
	}
	var out []string
	for _, a := range full.Activity {
		if a.Kind == store.TicketActivityComment && a.Author == store.TicketAuthorAttn &&
			strings.HasPrefix(a.Comment, ticketReconcileCommentPrefix) {
			out = append(out, a.Comment)
		}
	}
	return out
}

func reconciledAt(t *testing.T, d *Daemon, ticketID string) *time.Time {
	t.Helper()
	full, err := d.store.GetTicket(ticketID)
	if err != nil || full == nil {
		t.Fatalf("GetTicket %s: %v, %v", ticketID, full, err)
	}
	return full.ReconciledAt
}

// armReconcileObserver wires a done-channel observation hook plus a fake
// classifier exec. Returns the channel and a pointer to the call count.
func armReconcileObserver(d *Daemon, result agentdriver.HeadlessTaskResult, execErr error) (chan string, *int) {
	done := make(chan string, 8)
	calls := 0
	d.ticketReconcileDone = func(ticketID string) { done <- ticketID }
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		calls++
		return result, execErr
	}
	return done, &calls
}

func waitReconcileDone(t *testing.T, done chan string) string {
	t.Helper()
	select {
	case id := <-done:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reconciliation to finish")
		return ""
	}
}

// A neutral-end death (agent stopped at rest, then the session died) claims the
// flag and — with no transcript resolvable — posts the rule-7 failure note
// instead of vanishing. The column never moves.
func TestReconcileSeamNeutralEndPostsFailureNote(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, calls := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)
	waitReconcileDone(t, done)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want working (no auto-transition on the orphan path)", ticket.Status)
	}
	if ticket.ReconciledAt == nil {
		t.Fatal("ReconciledAt not claimed")
	}
	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1 (%v)", len(comments), comments)
	}
	if !strings.Contains(comments[0], "could not determine") || !strings.Contains(comments[0], "could not locate") {
		t.Fatalf("failure note = %q, want could-not-determine with transcript reason", comments[0])
	}
	if *calls != 0 {
		t.Fatalf("classifier exec ran %d times, want 0 (no transcript to read)", *calls)
	}
}

// A mid-flight death keeps the blunt Crashed stamp AND gets a reconciliation
// claim + comment (Victor 2026-07-01: crashes get verdicts too).
func TestReconcileSeamMidFlightStampsCrashedAndClaims(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)
	waitReconcileDone(t, done)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status = %q, want crashed (stamp unchanged)", ticket.Status)
	}
	if ticket.ReconciledAt == nil {
		t.Fatal("ReconciledAt not claimed")
	}
	if comments := reconcileComments(t, d, ticketID); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
}

// The seam double-fires on a user close (handlePTYExit then dropSessionRecord);
// the claim dedupes so exactly one verdict lands.
func TestReconcileSeamDoubleFireSingleClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)
	waitReconcileDone(t, done)
	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)

	if comments := reconcileComments(t, d, ticketID); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want exactly 1 after a double-fire", len(comments))
	}
}

// A structured verdict from the classifier renders as the verdict comment; the
// column still never moves.
func TestRunTicketReconciliationPostsVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation(ticketID, time.Now()); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}
	var seen ticketReconcileInputs
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		seen = in
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"last turn: tests pass except e2e"}`),
			TotalCostUSD:     0.12,
			NumTurns:         4,
		}, nil
	}

	d.runTicketReconciliation(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Migrate the store to X",
		Brief:          "Move the store onto the new backend.",
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "the session was closed (user close or teardown) while the ticket was working",
	})

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	for _, want := range []string{
		"Assessment: partial (confidence: medium)",
		"What's left: e2e spec never ran",
		"Evidence: last turn",
	} {
		if !strings.Contains(comments[0], want) {
			t.Fatalf("verdict comment missing %q:\n%s", want, comments[0])
		}
	}
	if seen.TranscriptPath != transcript {
		t.Fatalf("exec saw transcript %q, want %q", seen.TranscriptPath, transcript)
	}
	ticket, _ := d.store.GetTicket(ticketID)
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want working (verdict never moves the column)", ticket.Status)
	}
}

// A status change during the classifier run means someone acted; the stale
// verdict is dropped silently.
func TestRunTicketReconciliationDropsVerdictWhenStatusMoved(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		// The move happens while the classifier runs.
		if _, err := d.store.SetTicketStatus(ticketID, store.TicketStatusDone, store.TicketAuthorYou, "", time.Now()); err != nil {
			t.Errorf("SetTicketStatus during run: %v", err)
		}
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"high","whats_left":"x","evidence":"y"}`),
		}, nil
	}

	d.runTicketReconciliation(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
	})

	if comments := reconcileComments(t, d, ticketID); len(comments) != 0 {
		t.Fatalf("reconcile comments = %d, want 0 (verdict dropped after status move)", len(comments))
	}
}

// Classifier failure (exec error, cap-hit, schema mismatch) is not a verdict —
// it surfaces as the rule-7 failure note.
func TestRunTicketReconciliationExecErrorPostsFailureNote(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "headless agent process failed"}, errors.New("exit status 1")
	}

	d.runTicketReconciliation(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "claude",
		TranscriptPath: transcript,
	})

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	if !strings.Contains(comments[0], "could not determine") || !strings.Contains(comments[0], "headless agent process failed") {
		t.Fatalf("failure note = %q", comments[0])
	}
}

// The sweep claims a dead-owner ticket only after the grace period, then runs
// the same reconciliation path.
func TestSweepClaimsDeadOwnerAfterGrace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	// No session row for the assignee: the owner is dead (rows are deleted on close).
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "orphaned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusInReview,
	}, "chief", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t0 := time.Now()
	d.ticketReconcileSweepPass(t0)
	if got := reconciledAt(t, d, "orphaned"); got != nil {
		t.Fatalf("claimed on first sight (%v), want grace period first", got)
	}

	d.ticketReconcileSweepPass(t0.Add(ticketReconcileGrace() + time.Minute))
	waitReconcileDone(t, done)
	if got := reconciledAt(t, d, "orphaned"); got == nil {
		t.Fatal("not claimed after grace")
	}
	if comments := reconcileComments(t, d, "orphaned"); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	ticket, _ := d.store.GetTicket("orphaned")
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (sweep never moves the column)", ticket.Status)
	}
}

// Live owners, human-owned, and unassigned tickets are never sweep candidates.
func TestSweepSkipsLiveHumanAndUnassigned(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)

	// A session row without a backend runtime reads as live (CLI/remote sessions
	// have no daemon PTY; their death-hook is the unregister path).
	d.store.Add(&protocol.Session{ID: "sess-live", Label: "live", Directory: t.TempDir()})
	now := time.Now()
	mk := func(id, assignee string) {
		if _, err := d.store.CreateTicket(store.Ticket{ID: id, Title: "t", Assignee: assignee, Status: store.TicketStatusWorking}, "chief", now.Add(-time.Hour)); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
	}
	mk("live-owner", "sess-live")
	mk("human-owned", store.TicketAuthorYou)
	mk("unassigned", "")

	d.ticketReconcileSweepPass(now)
	d.ticketReconcileSweepPass(now.Add(ticketReconcileGrace() + time.Minute))

	for _, id := range []string{"live-owner", "human-owned", "unassigned"} {
		if got := reconciledAt(t, d, id); got != nil {
			t.Fatalf("%s was claimed (%v), want skipped", id, got)
		}
	}
}

// A claim whose verdict never landed (daemon died mid-run) is repaired with the
// rule-7 failure note — but only when nothing else happened since the claim.
func TestSweepRepairsAbandonedClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	past := time.Now().Add(-time.Hour)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "abandoned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", past); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation("abandoned", past); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}

	d.ticketReconcileSweepPass(time.Now())

	comments := reconcileComments(t, d, "abandoned")
	if len(comments) != 1 || !strings.Contains(comments[0], "interrupted before a verdict landed") {
		t.Fatalf("repair comments = %v, want one interrupted-note", comments)
	}
	// Idempotent: the marker comment suppresses a second repair.
	d.ticketReconcileSweepPass(time.Now())
	if comments := reconcileComments(t, d, "abandoned"); len(comments) != 1 {
		t.Fatalf("repair comments after second pass = %d, want 1", len(comments))
	}
}

// Post-claim activity (someone acted: a deliberate verdict drop, a human move)
// suppresses the repair note — a late failure note would be noise.
func TestSweepRepairSkipsWhenActedUpon(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	past := time.Now().Add(-time.Hour)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "acted", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", past); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation("acted", past); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}
	if _, err := d.store.AddTicketComment("acted", "chief", "taking a look", time.Now().Add(-30*time.Minute)); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}

	d.ticketReconcileSweepPass(time.Now())

	if comments := reconcileComments(t, d, "acted"); len(comments) != 0 {
		t.Fatalf("repair comments = %v, want none (post-claim activity)", comments)
	}
}
