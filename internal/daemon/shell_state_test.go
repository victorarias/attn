package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/statetrace"
)

// A shell's state is resolved from its foreground heartbeat; the worker poll
// must not touch it. The concrete failure this pins down: the worker runtime
// caches state "working" from birth, nothing ever sets a shell worker's state,
// and the watch-subscribe replay reports that cache as a worker-info claim —
// which would flip every freshly spawned shell out of `idle`.
//
// The veto is no longer shell-specific: no agent's worker caches a real state
// any more, so a worker-info claim may only end `launching`. A shell never sits
// there, which is why it is still the sharpest case to pin.
func TestShellIgnoresTheWorkerPollsStateClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "shell-veto.sock"))
	const id = "shell-worker-info"
	addCharacterizationSession(t, d, id, protocol.SessionAgentShell, protocol.SessionStateIdle)

	d.handlePTYState(id, pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "watch subscribe replay",
		At:     time.Now(),
	})

	if got := d.store.Get(id).State; got != protocol.SessionStateIdle {
		t.Fatalf("state=%q: the worker poll moved a shell", got)
	}
	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeVetoed || got.Reason != "resolver_owned" {
		t.Fatalf("trace = %+v, want vetoed/resolver_owned", got)
	}
}

// The foreground heartbeat is a shell's entire state pipeline: busy resolves to
// working, the prompt returning resolves back to idle, and neither transition
// puts the shell in the attention queue.
func TestShellForegroundHeartbeatDrivesItsState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "shell-heartbeat.sock"))
	const id = "shell-heartbeat"
	addCharacterizationSession(t, d, id, protocol.SessionAgentShell, protocol.SessionStateIdle)

	d.handlePTYState(id, heartbeatObs("busy", "foreground command running", time.Now()))
	d.resolveAllSessions(time.Now())
	if got := d.store.Get(id).State; got != protocol.SessionStateWorking {
		t.Fatalf("state=%q, want working while a foreground command runs", got)
	}

	d.handlePTYState(id, heartbeatObs("not_busy", "shell at prompt", time.Now()))
	d.resolveAllSessions(time.Now())
	session := d.store.Get(id)
	if session.State != protocol.SessionStateIdle {
		t.Fatalf("state=%q, want idle at the prompt", session.State)
	}

	// Real states must not put the pane in the queue: attention.Owed excludes
	// shells, and that exclusion is now a policy choice rather than a
	// consequence of shells never changing state.
	d.decorateSessionWithTurn(session)
	if session.TurnOwed != nil {
		t.Fatalf("TurnOwed=%v: a shell state change entered the queue", *session.TurnOwed)
	}
}
