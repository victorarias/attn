package daemon

import (
	"path/filepath"
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

func TestTraceRecordsObservationForUnknownSession(t *testing.T) {
	d := newTraceDaemon(t)

	d.handlePTYState("ghost", screenObs(protocol.StateWorking))

	got := onlyObservation(t, d, "ghost")
	if got.Outcome != statetrace.OutcomeVetoed || got.Reason != "session_not_found" {
		t.Fatalf("got %+v", got)
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
	d.traceStateSkip("sess-skip", stateSourceClassifier, "no_new_assistant_turn")

	got := onlyObservation(t, d, "sess-skip")
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
