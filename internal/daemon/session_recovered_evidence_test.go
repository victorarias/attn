package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
)

// heartbeat builds the level a worker reports back for a session it has been
// running since before this daemon existed.
func heartbeat(claim string, at time.Time) pty.Observation {
	return pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: "a turn summary", At: at}
}

func addRecoveredSession(t *testing.T, d *Daemon, id string, state protocol.SessionState, stateSince time.Time) {
	t.Helper()
	stamp := stateSince.UTC().Format(time.RFC3339Nano)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/" + id,
		State:          state,
		StateSince:     stamp,
		StateUpdatedAt: stamp,
		LastSeen:       stamp,
	})
}

func runningInfo(signal *pty.Observation) ptybackend.SessionInfo {
	info := ptybackend.SessionInfo{
		Running: true,
		Agent:   string(protocol.SessionAgentClaude),
		CWD:     "/tmp/recovered",
		// The literal string a worker with no observer of its own reports for
		// every agent. Recovery used to read it as a claim.
		State: protocol.StateWorking,
	}
	if signal != nil {
		info.LastSignal = *signal
		info.HasLastSignal = true
	}
	return info
}

// The headline: a daemon restart is not new information about an agent, so it
// must not overwrite what the last daemon concluded. Every one of these states
// used to come back as `launching`, and for a quiet agent it stayed there — no
// evidence ever arrives to move a session that is parked at its prompt.
func TestReconcileKeepsPersistedStateOfLiveSessions(t *testing.T) {
	states := []protocol.SessionState{
		protocol.SessionStateIdle,
		protocol.SessionStateWorking,
		protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateUnknown,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			addRecoveredSession(t, d, "live", state, time.Now().Add(-time.Hour))
			d.ptyBackend = &fakeWorkerReconcileBackend{
				liveIDs: []string{"live"},
				info:    map[string]ptybackend.SessionInfo{"live": runningInfo(nil)},
			}

			report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

			if report.StateUpdated != 0 {
				t.Fatalf("state_updated = %d, want 0", report.StateUpdated)
			}
			if got := d.store.Get("live").State; got != state {
				t.Fatalf("recovered state = %q, want %q", got, state)
			}
		})
	}
}

// The worker's child is gone. That is the one thing recovery can still prove on
// its own, and it must keep proving it.
func TestReconcileMarksExitedWorkerIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "dead", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	info := runningInfo(nil)
	info.Running = false
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"dead"},
		info:    map[string]ptybackend.SessionInfo{"dead": info},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if got := d.store.Get("dead").State; got != protocol.SessionStateIdle {
		t.Fatalf("recovered state = %q, want idle", got)
	}
}

// The level the worker still holds becomes evidence, so the resolver has a basis
// for its very first tick instead of an empty row it can say nothing about.
func TestReconcileSeedsHeartbeatEvidenceFromWorker(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	observedAt := time.Now().Add(-2 * time.Minute)
	addRecoveredSession(t, d, "live", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	signal := heartbeat("not_busy", observedAt)
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live"},
		info:    map[string]ptybackend.SessionInfo{"live": runningInfo(&signal)},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	evidence, ok := d.evidenceTable().snapshot("live")
	if !ok {
		t.Fatal("no evidence recorded for the recovered session")
	}
	if evidence.Heartbeat == nil {
		t.Fatal("heartbeat evidence missing")
	}
	if evidence.Heartbeat.Claim != sessionstate.ClaimSettled {
		t.Fatalf("heartbeat claim = %q, want settled", evidence.Heartbeat.Claim)
	}
	// The real observation time, not the recovery time: a level's age is what
	// decides whether the resolver still believes it, and stamping it `now` would
	// make a two-minute-old glyph look freshly painted.
	if !evidence.Heartbeat.ObservedAt.Equal(observedAt.UTC()) && !evidence.Heartbeat.ObservedAt.Equal(observedAt) {
		t.Fatalf("heartbeat observed_at = %s, want %s", evidence.Heartbeat.ObservedAt, observedAt)
	}
}

// Seeded evidence has to be enough for the resolver to correct a state that went
// stale while the daemon was down. The agent was working when the daemon died and
// finished before it came back: its title now says not-busy, and that has to
// settle the session rather than leave it green forever.
func TestRecoveredSessionResolvesOffStaleWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "live", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	signal := heartbeat("not_busy", time.Now().Add(-30*time.Second))
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live"},
		info:    map[string]ptybackend.SessionInfo{"live": runningInfo(&signal)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	d.resolveAllSessions(time.Now())

	if got := d.store.Get("live").State; got != protocol.SessionStateIdle {
		t.Fatalf("resolved state = %q, want idle", got)
	}
}

// An agent blocked on an approval paints exactly the same not-busy glyph as one
// that has finished, so the level alone would settle a recovered approval to
// idle. The persisted state is the record that the edge was outstanding.
func TestRecoveredApprovalSurvivesTheResolver(t *testing.T) {
	for _, tc := range []struct {
		state protocol.SessionState
	}{
		{protocol.SessionStatePendingApproval},
		{protocol.SessionStateWaitingInput},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			concludedAt := time.Now().Add(-10 * time.Minute)
			addRecoveredSession(t, d, "blocked", tc.state, concludedAt)
			// Painted before the daemon concluded the state, i.e. nothing has
			// happened since — which is exactly what an agent sitting on an
			// unanswered prompt looks like.
			signal := heartbeat("not_busy", concludedAt.Add(-time.Second))
			d.ptyBackend = &fakeWorkerReconcileBackend{
				liveIDs: []string{"blocked"},
				info:    map[string]ptybackend.SessionInfo{"blocked": runningInfo(&signal)},
			}
			d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

			d.resolveAllSessions(time.Now())

			if got := d.store.Get("blocked").State; got != tc.state {
				t.Fatalf("resolved state = %q, want %q", got, tc.state)
			}
		})
	}
}

// The other direction, and the reason the edge is not restored unconditionally:
// the agent painted a title after the daemon concluded the approval, so the
// prompt was answered and the turn ran on while nobody was watching. Restoring
// the edge there would pin a session on a question that is already over.
func TestRecoveredApprovalDropsWhenTheAgentMovedOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	concludedAt := time.Now().Add(-10 * time.Minute)
	addRecoveredSession(t, d, "answered", protocol.SessionStatePendingApproval, concludedAt)
	signal := heartbeat("not_busy", concludedAt.Add(time.Minute))
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"answered"},
		info:    map[string]ptybackend.SessionInfo{"answered": runningInfo(&signal)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	evidence, ok := d.evidenceTable().snapshot("answered")
	if !ok {
		t.Fatal("no evidence recorded for the recovered session")
	}
	if evidence.LastHarnessEvent != nil {
		t.Fatalf("harness edge restored for an agent that moved on: %+v", evidence.LastHarnessEvent)
	}

	d.resolveAllSessions(time.Now())

	if got := d.store.Get("answered").State; got != protocol.SessionStateIdle {
		t.Fatalf("resolved state = %q, want idle", got)
	}
}

// The symptom Victor reported, end to end. A snooze is the promise that an agent
// comes back at a time the user named; the wake only opens a turn if the session
// is in a state that wants the user, so a recovery that reset the state to
// `launching` cashed the deadline and delivered nothing. The row left the snoozed
// band and was never seen again.
func TestSnoozeWakeSurvivesDaemonRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "snoozed", protocol.SessionStateIdle, time.Now().Add(-time.Hour))
	deadline := time.Now().Add(time.Hour)
	if !d.store.SnoozeTurn("snoozed", deadline, time.Now()) {
		t.Fatal("failed to snooze the session")
	}
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"snoozed"},
		info:    map[string]ptybackend.SessionInfo{"snoozed": runningInfo(nil)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if !attention.OpensTurn(d.store.Get("snoozed").State) {
		t.Fatalf("recovered state %q opens no turn, so the wake has nothing to deliver",
			d.store.Get("snoozed").State)
	}

	d.wakeSnooze("snoozed", deadline, "deadline")

	stamps := d.store.TurnStamps("snoozed")
	if !stamps.SnoozedUntil.IsZero() {
		t.Fatalf("snooze still armed after the wake: %s", stamps.SnoozedUntil)
	}
	if !stamps.OpenedAt.After(stamps.SettledAt) {
		t.Fatalf("wake opened no turn: opened=%s settled=%s", stamps.OpenedAt, stamps.SettledAt)
	}
}

// A worker-info claim is the invented `working` default, and its only job is
// ending `launching`. Applied any wider it overwrites a state something actually
// observed — which on a restart is every live session at once, via the
// watch-subscribe replay.
func TestWorkerInfoClaimOnlyEndsLaunching(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial protocol.SessionState
		want    protocol.SessionState
	}{
		{"ends launching", protocol.SessionStateLaunching, protocol.SessionStateWorking},
		{"leaves idle alone", protocol.SessionStateIdle, protocol.SessionStateIdle},
		{"leaves pending approval alone", protocol.SessionStatePendingApproval, protocol.SessionStatePendingApproval},
		{"leaves unknown alone", protocol.SessionStateUnknown, protocol.SessionStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			addRecoveredSession(t, d, "s", tc.initial, time.Now().Add(-time.Hour))

			d.handlePTYState("s", pty.Observation{
				Source: pty.SourceWorkerInfo,
				Claim:  protocol.StateWorking,
				Detail: "watch subscribe replay",
				At:     time.Now(),
			})

			if got := d.store.Get("s").State; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}
