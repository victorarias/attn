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
	"github.com/victorarias/attn/internal/tasks"
)

// installReconcileRunner builds and starts a durable runner with only the
// reconcile executor registered, then publishes it on the daemon — so the
// session-end seam and the sweep have a live runner to Enqueue onto. A tiny poll
// interval avoids real-time waits. Callers arm the classifier + done hook (see
// armReconcileObserver) before triggering.
func installReconcileRunner(t *testing.T, d *Daemon) {
	t.Helper()
	runner := tasks.New(tasks.Options{
		Root:         filepath.Join(t.TempDir(), "tasks"),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWith(reconcileKind, d.reconcileTaskExecutor, tasks.ExecutorConfig{
		Timeout:       ticketReconcileTimeout(),
		MaxConcurrent: ticketReconcileConcurrency,
	}); err != nil {
		t.Fatalf("register reconcile: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.compactRunner = runner
}

// reconcileTask wraps captured inputs into the durable record the executor reads,
// so a test can drive reconcileTaskExecutor directly without a running runner.
func reconcileTask(in ticketReconcileInputs) *tasks.Task {
	return &tasks.Task{
		ID:      tasks.TaskID(reconcileKind, in.TicketID),
		Kind:    reconcileKind,
		Subject: in.TicketID,
		Meta:    reconcileInputsToMeta(in),
	}
}

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
	installReconcileRunner(t, d)

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
	installReconcileRunner(t, d)

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
	installReconcileRunner(t, d)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)
	waitReconcileDone(t, done)
	// The second fire's claim fails (set-if-unset), so it never enqueues a second
	// task — exactly one verdict lands.
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
	var seen ticketReconcileInputs
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		seen = in
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"last turn: tests pass except e2e"}`),
			TotalCostUSD:     0.12,
			NumTurns:         4,
		}, nil
	}

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Migrate the store to X",
		Brief:          "Move the store onto the new backend.",
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "the session was closed (user close or teardown) while the ticket was working",
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

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

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

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
		// Real shape: stderr leads with the startup error, stdout ends with the
		// result event carrying the human-readable error.
		return agentdriver.HeadlessTaskResult{
			Diagnostics: "headless agent MCP tool server failed",
			FailureOutput: "stderr: MCP server \"claude.ai Slack\" needs authentication\nstdout: " +
				strings.Repeat("x", 2000) + `{"type":"result","is_error":true,"result":"model not found"}`,
		}, errors.New("headless agent MCP tool server failed: exit status 1")
	}

	if err := d.reconcileTaskExecutor(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "claude",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileTaskExecutor: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	// The failure note carries the actual error — the exec error summary plus
	// both bounded ends of the raw output (stderr head, trailing result event)
	// — never only a keyword bucket.
	for _, want := range []string{
		"could not determine",
		"classifier run failed: headless agent MCP tool server failed: exit status 1",
		`MCP server "claude.ai Slack" needs authentication`,
		`"result":"model not found"`,
		"…(truncated)",
	} {
		if !strings.Contains(comments[0], want) {
			t.Fatalf("failure note missing %q:\n%s", want, comments[0])
		}
	}
	if strings.Contains(comments[0], strings.Repeat("x", 1000)) {
		t.Fatalf("failure note not truncated:\n%s", comments[0])
	}
}

// The sweep claims a dead-owner ticket only after the grace period, then enqueues
// the durable reconcile task, which runs the same reconciliation path.
func TestSweepClaimsDeadOwnerAfterGrace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)
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
	installReconcileRunner(t, d)

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

// An abandoned session-end claim — reconciled_at stamped but the daemon died
// before the durable task was enqueued — is recovered by the sweep. With no
// reconcile task on record, the sweep re-enqueues after grace and the executor
// posts a REAL verdict/failure note. This replaces the old bespoke
// maybeRepairAbandonedReconcileClaim pass (and its generic "interrupted before a
// verdict landed" note) with a genuine reconciliation run.
func TestSweepRecoversAbandonedClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)
	past := time.Now().Add(-time.Hour)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "abandoned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", past); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// The crash gap: the seam claimed (badge stamped) but the enqueue never landed,
	// so no reconcile task exists.
	if claimed, err := d.store.ClaimTicketReconciliation("abandoned", past); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}

	t0 := time.Now()
	d.ticketReconcileSweepPass(t0) // first sight: grace not elapsed, no enqueue yet
	if comments := reconcileComments(t, d, "abandoned"); len(comments) != 0 {
		t.Fatalf("reconciled before grace elapsed: %v", comments)
	}
	d.ticketReconcileSweepPass(t0.Add(ticketReconcileGrace() + time.Minute))
	waitReconcileDone(t, done)

	// A real reconciliation ran (no transcript resolvable ⇒ the rule-7 failure
	// note), not the old generic interrupted-claim string.
	comments := reconcileComments(t, d, "abandoned")
	if len(comments) != 1 || !strings.Contains(comments[0], "could not locate") {
		t.Fatalf("recovered comments = %v, want one could-not-locate failure note", comments)
	}
}

// Once a reconcile task exists for a ticket (the seam or a prior sweep enqueued
// it), the sweep leaves it to the runner and never enqueues a duplicate — the
// durable task record is the "already triggered" ledger.
func TestSweepSkipsTicketWithExistingTask(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	installReconcileRunner(t, d)
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "already", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", now.Add(-time.Hour)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// A terminal reconcile task already on record stands in for "already handled".
	runner := d.compactRunnerRef()
	if _, err := runner.Enqueue(reconcileKind, "already", tasks.EnqueueOptions{
		Meta: reconcileInputsToMeta(ticketReconcileInputs{TicketID: "already"}),
	}); err != nil {
		t.Fatalf("seed reconcile task: %v", err)
	}

	// Even well past grace, the sweep must not re-claim: a task exists.
	d.ticketReconcileSweepPass(now.Add(ticketReconcileGrace() + time.Hour))
	if got := reconciledAt(t, d, "already"); got != nil {
		t.Fatalf("sweep re-claimed a ticket with an existing task (%v)", got)
	}
}
