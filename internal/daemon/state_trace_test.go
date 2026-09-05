package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
)

func newTraceDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
}

func traceOf(t *testing.T, d *Daemon, sessionID string) []statetrace.Observation {
	t.Helper()
	return d.stateTraceRecorder().Observations(sessionID)
}

func onlyObservation(t *testing.T, d *Daemon, sessionID string) statetrace.Observation {
	t.Helper()
	got := traceOf(t, d, sessionID)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 observation, got %d: %+v", len(got), got)
	}
	return got[0]
}

func TestTraceRecordsTheAppliedWorkerPollWithItsSource(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-applied"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateLaunching)

	observed := time.Now().Add(-500 * time.Millisecond)
	d.handlePTYState(id, pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "worker info",
		At:     observed,
	})

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeApplied {
		t.Fatalf("outcome %q, want applied", got.Outcome)
	}
	if got.Source != string(pty.SourceWorkerInfo) {
		t.Fatalf("source %q, want %q", got.Source, pty.SourceWorkerInfo)
	}
	if got.Cause != "live_signal" {
		t.Fatalf("cause %q, want live_signal", got.Cause)
	}
	if got.Claim != protocol.StateWorking || got.Detail != "worker info" {
		t.Fatalf("claim/detail wrong: %+v", got)
	}
	if !got.ObservedAt.Equal(observed) {
		t.Fatalf("ObservedAt %s, want the source's %s", got.ObservedAt, observed)
	}
}

func TestTraceDoesNotRingAnUnknownSession(t *testing.T) {
	d := newTraceDaemon(t)

	d.handlePTYState("ghost", heartbeatObs("busy", "test", time.Now()))

	if got := traceOf(t, d, "ghost"); got != nil {
		t.Fatalf("an unknown session must not create a ring: %+v", got)
	}
}

func TestTraceDoesNotResurrectARingAfterRemoval(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-raced"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.handlePTYState(id, heartbeatObs("not_busy", "test", time.Now()))

	d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})
	d.handlePTYState(id, heartbeatObs("busy", "test", time.Now()))

	if got := traceOf(t, d, id); got != nil {
		t.Fatalf("a late observation resurrected the ring: %+v", got)
	}
	if got := d.stateTraceRecorder().SessionCount(); got != 0 {
		t.Fatalf("recorder holds %d rings, want 0", got)
	}
}

func TestTraceRecordsHookSource(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hook"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateIdle)

	d.handleState(&syncConn{}, &protocol.StateMessage{ID: id, State: protocol.StateWorking})

	got := onlyObservation(t, d, id)
	if got.Source != stateSourceHook || got.Outcome != statetrace.OutcomeObserved {
		t.Fatalf("got %+v", got)
	}
	if got.Claim != protocol.StateWorking {
		t.Fatalf("claim %q, want the state the hook reported", got.Claim)
	}
}

func TestTraceFallsBackToTheCauseNameAsSource(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cause"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.applyState(sessionStateChange{sessionID: id, state: protocol.StateIdle, cause: startupRecovery{}})

	got := onlyObservation(t, d, id)
	if got.Source != "startup_recovery" || got.Cause != "startup_recovery" {
		t.Fatalf("got %+v", got)
	}
}

func TestTraceRecordsSkips(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-skip"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.traceStateSkip(id, stateSourceClassifier, "no_new_assistant_turn")

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeSkipped || got.Claim != "" {
		t.Fatalf("got %+v", got)
	}
	if got.Reason != "no_new_assistant_turn" {
		t.Fatalf("reason %q", got.Reason)
	}
}

func TestTraceIsDroppedWhenTheSessionRecordGoes(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-gone"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.handlePTYState(id, heartbeatObs("not_busy", "test", time.Now()))
	if len(traceOf(t, d, id)) == 0 {
		t.Fatal("precondition: expected a recorded observation")
	}

	d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})

	if got := traceOf(t, d, id); got != nil {
		t.Fatalf("trace survived session removal: %+v", got)
	}
}

// The removal blocks on the recorder's lock, and a synctest bubble cannot observe a goroutine waiting for a mutex.
func TestTraceDoesNotLeakWhenRemovalRacesTheWrite(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-racing"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	removalDone := make(chan struct{})
	var once sync.Once
	stateTraceRecordGateHook = func(observed string) {
		if observed != id {
			return
		}
		once.Do(func() {
			go func() {
				defer close(removalDone)
				d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})
			}()
			time.Sleep(20 * time.Millisecond)
		})
	}
	t.Cleanup(func() { stateTraceRecordGateHook = nil })

	d.handlePTYState(id, heartbeatObs("not_busy", "test", time.Now()))
	<-removalDone

	if got := traceOf(t, d, id); got != nil {
		t.Fatalf("ring survived the racing removal: %+v", got)
	}
	if got := d.stateTraceRecorder().SessionCount(); got != 0 {
		t.Fatalf("recorder holds %d rings after the race, want 0", got)
	}
}

func TestStateExplainResultRendersTheRing(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-explain"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	d.handlePTYState(id, heartbeatObs("busy", "test", time.Now()))

	result := d.stateExplainResult(d.store.Get(id))
	if result.SessionID != id || result.Agent != string(protocol.SessionAgentClaude) {
		t.Fatalf("identity wrong: %+v", result)
	}
	if result.State != protocol.StateWaitingInput {
		t.Fatalf("state %q, want the session's current state", result.State)
	}
	if result.Capacity != statetrace.DefaultCapacity {
		t.Fatalf("capacity %d, want %d", result.Capacity, statetrace.DefaultCapacity)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("want 1 observation, got %d", len(result.Observations))
	}
	entry := result.Observations[0]
	if entry.Source != string(pty.SourceHeartbeat) || entry.Outcome != string(statetrace.OutcomeObserved) {
		t.Fatalf("entry %+v", entry)
	}
	if entry.Cause != nil {
		t.Fatalf("cause %v, want none: the observation was filed as evidence, not applied", *entry.Cause)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.ObservedAt); err != nil {
		t.Fatalf("observed_at %q is not RFC3339Nano: %v", entry.ObservedAt, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.RecordedAt); err != nil {
		t.Fatalf("recorded_at %q is not RFC3339Nano: %v", entry.RecordedAt, err)
	}
}

func heartbeatObs(claim, detail string, at time.Time) pty.Observation {
	return pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: detail, At: at}
}

func TestTraceRecordsHarnessSignalsAsEvidenceOnly(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-evidence"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateIdle)

	observed := time.Now().Add(-200 * time.Millisecond)
	d.handlePTYState(id, heartbeatObs("busy", "⠐ Editing files", observed))

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeObserved {
		t.Fatalf("outcome %q, want observed", got.Outcome)
	}
	if got.Cause != "" || got.Reason != "" {
		t.Fatalf("evidence must carry no cause or reason: %+v", got)
	}
	if got.Source != string(pty.SourceHeartbeat) || got.Claim != "busy" {
		t.Fatalf("got %+v", got)
	}
	if got.Detail != "⠐ Editing files" {
		t.Fatalf("detail %q, want the title verbatim", got.Detail)
	}
	if !got.ObservedAt.Equal(observed) {
		t.Fatalf("ObservedAt %s, want %s", got.ObservedAt, observed)
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("evidence changed state to %q", state)
	}
}

func TestTraceCollapsesRepeatedEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-repeats"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	start := time.Now().Add(-10 * time.Second)
	for i := range 5 {
		d.handlePTYState(id, heartbeatObs("busy", "⠐ working", start.Add(time.Duration(i)*time.Second)))
	}

	got := onlyObservation(t, d, id)
	if got.Repeats != 4 {
		t.Fatalf("Repeats %d, want 4 (5 observations collapsed into 1)", got.Repeats)
	}
	if want := start.Add(4 * time.Second); !got.ObservedAt.Equal(want) {
		t.Fatalf("ObservedAt %s, want the newest %s", got.ObservedAt, want)
	}

	d.handlePTYState(id, heartbeatObs("not_busy", "✳ done", time.Now()))
	if all := traceOf(t, d, id); len(all) != 2 || all[1].Repeats != 0 {
		t.Fatalf("a changed claim must open a new row: %+v", all)
	}
}

func TestTraceCollapsesHeartbeatsAcrossSpinnerFrames(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-spinner-frames"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	start := time.Now().Add(-30 * time.Second)
	frames := []string{"⠐", "⠸", "⠿", "⠇", "⠏", "⠋", "⠙"}
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * time.Second)
		_ = frame
		d.handlePTYState(id, heartbeatObs("busy", "Run background sleep command", at))
	}

	got := onlyObservation(t, d, id)
	if got.Repeats != len(frames)-1 {
		t.Fatalf("Repeats %d, want %d — one row for the whole turn", got.Repeats, len(frames)-1)
	}
	if got.Claim != "busy" {
		t.Fatalf("claim %q, want busy", got.Claim)
	}
	if got.Detail != "Run background sleep command" {
		t.Fatalf("detail %q, want the frame-free summary", got.Detail)
	}
	if want := start.Add(time.Duration(len(frames)-1) * time.Second); !got.ObservedAt.Equal(want) {
		t.Fatalf("ObservedAt %s, want the newest %s", got.ObservedAt, want)
	}

	d.handlePTYState(id, heartbeatObs("busy", "Editing files", start.Add(30*time.Second)))
	if all := traceOf(t, d, id); len(all) != 2 {
		t.Fatalf("a changed summary must open a new row: %+v", all)
	}

	withFrames := newTraceDaemon(t)
	framed := "sess-spinner-unstripped"
	addCharacterizationSession(t, withFrames, framed, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * time.Second)
		withFrames.handlePTYState(framed, heartbeatObs("busy", frame+" Run background sleep command", at))
	}
	if all := traceOf(t, withFrames, framed); len(all) != len(frames) {
		t.Fatalf("volatile details collapsed unexpectedly (%d rows for %d frames); "+
			"the ring pressure this test guards against would be invisible", len(all), len(frames))
	}
}

func callHandler(t *testing.T, call func(net.Conn)) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(server)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	_ = client.Close()
	<-done
	return resp
}

// The Notification hook lands ~6s late, so it is evidence and must not move the session.
func TestTraceRecordsTheNotificationHookAsEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hook-notify"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	resp := callHandler(t, func(conn net.Conn) {
		d.handleHookNotification(conn, &protocol.HookNotificationMessage{
			ID:               id,
			NotificationType: "permission_prompt",
			Message:          protocol.Ptr("Claude needs your permission"),
		})
	})
	if !resp.Ok {
		t.Fatalf("response ok=%v error=%q", resp.Ok, protocol.Deref(resp.Error))
	}

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeObserved {
		t.Fatalf("outcome %q, want observed", got.Outcome)
	}
	if got.Source != stateSourceHookNotify {
		t.Fatalf("source %q, want %q", got.Source, stateSourceHookNotify)
	}
	if got.Claim != "permission_prompt" || got.Detail != "Claude needs your permission" {
		t.Fatalf("got %+v", got)
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("the notification changed state to %q", state)
	}
}

func TestNotificationHookRequiresAType(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hook-notify-bad"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	resp := callHandler(t, func(conn net.Conn) {
		d.handleHookNotification(conn, &protocol.HookNotificationMessage{ID: id, NotificationType: "  "})
	})
	if resp.Ok {
		t.Fatal("a typeless notification should be rejected")
	}
	if got := traceOf(t, d, id); len(got) != 0 {
		t.Fatalf("rejected notification was still recorded: %+v", got)
	}
}

func TestTraceRecordsThePermissionModeReportedByTheStateHook(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-perm-mode"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateIdle)

	callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{
			ID:             id,
			State:          protocol.StateWorking,
			PermissionMode: protocol.Ptr("auto"),
		})
	})

	got := traceOf(t, d, id)
	if len(got) != 2 {
		t.Fatalf("want the mode and the state, got %+v", got)
	}
	if got[0].Source != stateSourceReviewer || got[0].Claim != "auto" {
		t.Fatalf("first observation %+v, want the reviewer level", got[0])
	}
	if got[0].Outcome != statetrace.OutcomeObserved {
		t.Fatalf("the mode is evidence, not a state: outcome %q", got[0].Outcome)
	}
	if got[1].Source != stateSourceHook || got[1].Outcome != statetrace.OutcomeObserved {
		t.Fatalf("second observation %+v, want the hook's state as evidence", got[1])
	}
	if got[1].Claim != protocol.StateWorking {
		t.Fatalf("second observation claim %q, want the reported state", got[1].Claim)
	}
}

func TestStateHookWithoutAPermissionModeRecordsOnlyTheState(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-no-perm-mode"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateIdle)

	callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{ID: id, State: protocol.StateWorking})
	})

	got := onlyObservation(t, d, id)
	if got.Source != stateSourceHook {
		t.Fatalf("got %+v, want only the hook state", got)
	}
}
