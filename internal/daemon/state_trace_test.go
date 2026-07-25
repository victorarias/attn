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

func TestTraceRecordsAppliedPTYObservationWithItsSource(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-applied"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)

	observed := time.Now().Add(-500 * time.Millisecond)
	d.handlePTYState(id, pty.Observation{
		Source: pty.SourceScreen,
		Claim:  protocol.StateWorking,
		Detail: "screen scrape",
		At:     observed,
	})

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeApplied {
		t.Fatalf("outcome %q, want applied", got.Outcome)
	}
	// The source must survive the trip: liveSignal is shared by every trusted PTY
	// source, so the cause alone cannot tell a screen scrape from an approval edge.
	if got.Source != string(pty.SourceScreen) {
		t.Fatalf("source %q, want %q", got.Source, pty.SourceScreen)
	}
	if got.Cause != "live_signal" {
		t.Fatalf("cause %q, want live_signal", got.Cause)
	}
	if got.Claim != protocol.StateWorking || got.Detail != "screen scrape" {
		t.Fatalf("claim/detail wrong: %+v", got)
	}
	if !got.ObservedAt.Equal(observed) {
		t.Fatalf("ObservedAt %s, want the source's %s", got.ObservedAt, observed)
	}
}

// The whole point of the trace: an observation a driver's transition filter
// refuses never reaches the store and appears in no other log line, which is
// exactly the case a stuck color needs explained.
func TestTraceRecordsDriverVetoedObservation(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-vetoed"
	// Claude's ShouldApplyPTYState guards a scheduled session against the screen
	// detector knocking it out of the parked state.
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateScheduled)

	d.handlePTYState(id, screenObs(protocol.StateIdle))

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeVetoed {
		t.Fatalf("outcome %q, want vetoed", got.Outcome)
	}
	if got.Reason != "driver_transition_filter" {
		t.Fatalf("reason %q, want driver_transition_filter", got.Reason)
	}
	if got.Claim != protocol.StateIdle {
		t.Fatalf("claim %q, want the rejected claim %q", got.Claim, protocol.StateIdle)
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateScheduled {
		t.Fatalf("tracing must not change arbitration: state is %q", state)
	}
}

// An id with no store row can never be read back — `attn state explain` needs a
// row — and can never be cleaned up, because cleanup hangs off session removal.
// Ringing one would leak a map entry per stale id for the daemon's lifetime, so
// these observations are log-only.
func TestTraceDoesNotRingAnUnknownSession(t *testing.T) {
	d := newTraceDaemon(t)

	d.handlePTYState("ghost", screenObs(protocol.StateWorking))

	if got := traceOf(t, d, "ghost"); got != nil {
		t.Fatalf("an unknown session must not create a ring: %+v", got)
	}
}

// The stale-worker race: a session is removed while one of its state updates is
// still in flight. The late observation must not resurrect a ring for an id that
// will never be removed again.
func TestTraceDoesNotResurrectARingAfterRemoval(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-raced"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.handlePTYState(id, screenObs(protocol.StateIdle))

	d.dropSessionRecord(id)
	// The worker event the daemon was already holding when the row went away.
	d.handlePTYState(id, screenObs(protocol.StateWorking))

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
	if got.Source != stateSourceHook || got.Outcome != statetrace.OutcomeApplied {
		t.Fatalf("got %+v", got)
	}
}

// A change with no origin still has to be attributable, or the internal
// transitions (recovery, process exit) become blanks in the middle of a trace.
func TestTraceFallsBackToTheCauseNameAsSource(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cause"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.applyState(sessionStateChange{sessionID: id, state: protocol.StateIdle, cause: processExit{}})

	got := onlyObservation(t, d, id)
	if got.Source != "process_exit" || got.Cause != "process_exit" {
		t.Fatalf("got %+v", got)
	}
}

// A stale classifier result is refused by the store's commit rule rather than by
// a caller-side guard, so it is a discard and not a veto — the distinction points
// at a different layer when a color is wrong.
func TestTraceRecordsStoreDiscardedClassifierResult(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stale"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	applied := d.applyState(sessionStateChange{
		sessionID: id,
		state:     protocol.StateIdle,
		// Older than the session's StateSince, which the characterization helper
		// backdates to characterizationOldTimestamp.
		cause:  classifierObservation{observedAt: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)},
		origin: stateOrigin{source: stateSourceClassifier, detail: "classifier"},
	})
	if applied {
		t.Fatal("a classifier result older than the current state should not commit")
	}

	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeDiscarded || got.Reason != "store_rejected" {
		t.Fatalf("got %+v", got)
	}
	if got.Source != stateSourceClassifier {
		t.Fatalf("source %q, want %q", got.Source, stateSourceClassifier)
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
	d.handlePTYState(id, screenObs(protocol.StateIdle))
	if len(traceOf(t, d, id)) == 0 {
		t.Fatal("precondition: expected a recorded observation")
	}

	d.dropSessionRecord(id)

	if got := traceOf(t, d, id); got != nil {
		t.Fatalf("trace survived session removal: %+v", got)
	}
}

// The interleaving the store-row gate has to survive: a writer passes the
// liveness check, the session is removed and its ring forgotten, and only then
// does the writer reach the append. If the check and the append are not atomic,
// the writer creates a ring for an id nothing will ever forget again — one
// leaked map entry per race, for the daemon's lifetime.
//
// The hook fires inside the recorder's lock, which is exactly where the removal
// must be attempted for the race to be real.
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
			// Remove the session from under the in-flight writer. This blocks on
			// the recorder's lock inside forgetStateTrace, which is what proves
			// the two operations are serialized rather than interleaved.
			go func() {
				defer close(removalDone)
				d.dropSessionRecord(id)
			}()
			// Give the removal a chance to get as far as it can before the write
			// proceeds. Without RecordIf's lock it would complete here.
			time.Sleep(20 * time.Millisecond)
		})
	}
	t.Cleanup(func() { stateTraceRecordGateHook = nil })

	d.handlePTYState(id, screenObs(protocol.StateIdle))
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
	d.handlePTYState(id, screenObs(protocol.StateWorking))

	result := d.stateExplainResult(d.store.Get(id))
	if result.SessionID != id || result.Agent != string(protocol.SessionAgentClaude) {
		t.Fatalf("identity wrong: %+v", result)
	}
	if result.State != protocol.StateWorking {
		t.Fatalf("state %q, want the session's current state", result.State)
	}
	if result.Capacity != statetrace.DefaultCapacity {
		t.Fatalf("capacity %d, want %d", result.Capacity, statetrace.DefaultCapacity)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("want 1 observation, got %d", len(result.Observations))
	}
	entry := result.Observations[0]
	if entry.Source != string(pty.SourceScreen) || entry.Outcome != string(statetrace.OutcomeApplied) {
		t.Fatalf("entry %+v", entry)
	}
	if entry.Cause == nil || *entry.Cause != "live_signal" {
		t.Fatalf("cause %v", entry.Cause)
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

// The harness signals speak "busy"/"not_busy", not protocol states. They are
// recorded as evidence for a later resolver to weigh and must never be offered
// to applyState, where "busy" is not even a valid state.
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
	// No cause: the observation never entered the commit path, so attributing one
	// would imply the store saw and refused it.
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

// A busy heartbeat repeats once a second for the length of a turn. Without
// collapsing, a long turn would push every other observation out of the ring and
// make `attn state explain` useless exactly when it is needed.
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
	// The surviving row must report the latest sighting, not the first: freshness
	// is the only thing a heartbeat contributes.
	if want := start.Add(4 * time.Second); !got.ObservedAt.Equal(want) {
		t.Fatalf("ObservedAt %s, want the newest %s", got.ObservedAt, want)
	}

	// A different claim is news and starts its own row.
	d.handlePTYState(id, heartbeatObs("not_busy", "✳ done", time.Now()))
	if all := traceOf(t, d, id); len(all) != 2 || all[1].Repeats != 0 {
		t.Fatalf("a changed claim must open a new row: %+v", all)
	}
}

// The contract phase 1a is judged on: a long busy turn must not consume one ring
// slot per second. The heartbeat re-emits its unchanged level once a keepalive,
// and the spinner frame differs on every one of those emissions — if the frame
// reaches the trace, each row is distinct, Repeats never increments, and 256
// seconds of work evicts every other kind of evidence from the ring.
func TestTraceCollapsesHeartbeatsAcrossSpinnerFrames(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-spinner-frames"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	start := time.Now().Add(-30 * time.Second)
	frames := []string{"⠐", "⠸", "⠿", "⠇", "⠏", "⠋", "⠙"}
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * time.Second)
		// What the observer produces for that frame. It strips the glyph, so the
		// summary is identical across frames — see
		// TestHeartbeatDetailIsStableAcrossSpinnerFrames for the proof that it
		// does, and the sub-case below for what happens when it does not.
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
	// The surviving row reports the latest sighting: freshness is the only thing
	// a repeated heartbeat contributes.
	if want := start.Add(time.Duration(len(frames)-1) * time.Second); !got.ObservedAt.Equal(want) {
		t.Fatalf("ObservedAt %s, want the newest %s", got.ObservedAt, want)
	}

	// A genuinely different summary is news and opens its own row.
	d.handlePTYState(id, heartbeatObs("busy", "Editing files", start.Add(30*time.Second)))
	if all := traceOf(t, d, id); len(all) != 2 {
		t.Fatalf("a changed summary must open a new row: %+v", all)
	}

	// And the failure this guards against: had the glyph reached the detail,
	// every frame would be a distinct row rather than a repeat.
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

// callHandler runs a conn-taking daemon handler against a pipe and returns the
// response it wrote.
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

// Claude's Notification hook is the harness saying out loud that it is blocked
// on the user, and it says which kind. It lands ~6s late, so it is recorded as
// evidence and must not move the session.
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
	// The type is the load-bearing half: it separates "blocked on approval" from
	// "waiting on a reply" without parsing an English sentence.
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

// The permission mode rides along on the state hook because attn's own launch
// flags are not authoritative: a user's global agent settings can put a guardian
// in the loop for a session attn launched without asking for one.
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
	// The state itself must still be applied — reporting the mode is additive.
	if got[1].Source != stateSourceHook || got[1].Outcome != statetrace.OutcomeApplied {
		t.Fatalf("second observation %+v, want the applied hook state", got[1])
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working", state)
	}
}

// Codex reports no permission mode. An absent one must not open a row that says
// the reviewer is unknown — that is indistinguishable from a real claim.
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
