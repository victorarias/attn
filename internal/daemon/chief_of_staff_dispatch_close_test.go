package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/dispatch"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// addDispatchSession registers a delegated session in the given runtime state plus
// the chief-of-staff dispatch tracking it, the shape a real delegated worker has
// just before its process ends.
func addDispatchSession(t *testing.T, d *Daemon, sessionID, dispatchID string, state protocol.SessionState) {
	t.Helper()
	addIdleNotebookSession(d, sessionID, state)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: dispatchID, ChiefSessionID: "chief", SessionID: sessionID, WorkspaceID: "ws",
		Label: "Task", Agent: "claude", CreatedAt: "2026-06-22", UpdatedAt: "2026-06-22",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
}

// classifyDispatch reads the durable dispatch for a session, decorates it exactly
// as the watch/status paths do, and runs the shared signal classifier — the full
// chain a chief sees.
func classifyDispatch(t *testing.T, d *Daemon, sessionID string) (*protocol.ChiefOfStaffDispatch, dispatch.Event) {
	t.Helper()
	rec := d.store.GetChiefOfStaffDispatchBySession(sessionID)
	if rec == nil {
		t.Fatalf("no dispatch for session %s", sessionID)
	}
	decorated := d.decorateChiefOfStaffDispatch(rec)
	return decorated, dispatch.Classify(*decorated)
}

// A worker killed mid-work must read as a failure, not a neutral end. handlePTYExit
// captures the pre-clobber "working" state before forcing the lingering session to
// idle; the decorated dispatch then projects a closed status carrying that
// close-state, so the classifier surfaces a real crash instead of crying nothing.
func TestDispatchCloseStateCrashSurfacesAsFailed(t *testing.T) {
	d := newNotebookDaemon(t)
	addDispatchSession(t, d, "worker-1", "dsp-1", protocol.SessionStateWorking)

	d.handlePTYExit(ptybackend.ExitInfo{ID: "worker-1", ExitCode: 137, Signal: "SIGKILL"})

	rec := d.store.GetChiefOfStaffDispatchBySession("worker-1")
	if rec == nil || protocol.Deref(rec.ClosedState) != string(protocol.SessionStateWorking) {
		t.Fatalf("captured close-state = %+v", rec)
	}
	// The session record lingers (idle-clobbered), yet the dispatch must read closed.
	if got := d.store.Get("worker-1"); got == nil || got.State != protocol.SessionStateIdle {
		t.Fatalf("lingering session state = %+v, want idle-clobbered", got)
	}
	decorated, ev := classifyDispatch(t, d, "worker-1")
	if decorated.Status != dispatch.SessionClosedStatus {
		t.Fatalf("decorated status = %q, want closed", decorated.Status)
	}
	if ev.Kind != dispatch.KindFailed || ev.Reason != "session_crashed" {
		t.Fatalf("classify = %+v, want failed/session_crashed", ev)
	}
}

// A worker that closed from a clean idle rest must stay neutral Ended: attn does
// not infer a success the agent never declared, and a clean stop is not a crash.
func TestDispatchCloseStateCleanCloseStaysNeutral(t *testing.T) {
	d := newNotebookDaemon(t)
	addDispatchSession(t, d, "worker-1", "dsp-1", protocol.SessionStateIdle)

	d.handlePTYExit(ptybackend.ExitInfo{ID: "worker-1", ExitCode: 0})

	rec := d.store.GetChiefOfStaffDispatchBySession("worker-1")
	if protocol.Deref(rec.ClosedState) != string(protocol.SessionStateIdle) {
		t.Fatalf("captured close-state = %q, want idle", protocol.Deref(rec.ClosedState))
	}
	_, ev := classifyDispatch(t, d, "worker-1")
	if ev.Kind != dispatch.KindEnded || ev.Reason != "session_closed" {
		t.Fatalf("classify = %+v, want ended/session_closed", ev)
	}
}

// The real crash sequence: the process exits (handlePTYExit captures the true
// mid-flight state and clobbers to idle), and only later is the record removed
// (dropSessionRecord, whose backstop read would see the clobbered idle).
// First-writer-wins must keep the genuine "working" close-state through both.
func TestDispatchCloseStateFirstWriterWinsAcrossExitThenRemoval(t *testing.T) {
	d := newNotebookDaemon(t)
	addDispatchSession(t, d, "worker-1", "dsp-1", protocol.SessionStateWorking)

	d.handlePTYExit(ptybackend.ExitInfo{ID: "worker-1", ExitCode: 1})
	d.dropSessionRecord("worker-1")

	rec := d.store.GetChiefOfStaffDispatchBySession("worker-1")
	if rec == nil || protocol.Deref(rec.ClosedState) != string(protocol.SessionStateWorking) {
		t.Fatalf("close-state after exit+removal = %+v", rec)
	}
	if d.store.Get("worker-1") != nil {
		t.Fatal("session record was not removed")
	}
	_, ev := classifyDispatch(t, d, "worker-1")
	if ev.Kind != dispatch.KindFailed {
		t.Fatalf("classify = %+v, want failed", ev)
	}
}

// A removal path that bypasses handlePTYExit entirely (reaped on restart, liveness
// sweep, worktree teardown) must still capture the last state via the
// dropSessionRecord backstop, so a crash that never emitted an exit event is not
// silently lost.
func TestDispatchCloseStateDropBackstopCaptures(t *testing.T) {
	d := newNotebookDaemon(t)
	addDispatchSession(t, d, "worker-1", "dsp-1", protocol.SessionStateWorking)

	d.dropSessionRecord("worker-1")

	rec := d.store.GetChiefOfStaffDispatchBySession("worker-1")
	if rec == nil || protocol.Deref(rec.ClosedState) != string(protocol.SessionStateWorking) {
		t.Fatalf("backstop close-state = %+v", rec)
	}
	if d.store.Get("worker-1") != nil {
		t.Fatal("session record was not removed")
	}
	_, ev := classifyDispatch(t, d, "worker-1")
	if ev.Kind != dispatch.KindFailed {
		t.Fatalf("classify = %+v, want failed", ev)
	}
}

// A non-dispatch session ending is a silent no-op: capture must not invent a
// dispatch or error when the closing session was never delegated.
func TestDispatchCloseStateIgnoresNonDispatchSession(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "plain-1", protocol.SessionStateWorking)

	d.handlePTYExit(ptybackend.ExitInfo{ID: "plain-1", ExitCode: 0})

	if rec := d.store.GetChiefOfStaffDispatchBySession("plain-1"); rec != nil {
		t.Fatalf("non-dispatch session produced a dispatch: %+v", rec)
	}
}
