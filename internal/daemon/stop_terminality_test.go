package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// TestStopIsNonTerminal locks which Stops leave the turn able to resume itself,
// and therefore which ones skip the end-of-turn work. The relax cases lock the
// chief-of-staff relaxation: a chief's background work no longer defers the end
// of its turn, while a parked schedule still does.
//
// What color the session shows while it waits is not decided here any more; the
// resolver decides it from the facts recorded alongside this call, and
// TestResolve covers the precedence between them.
//
// The status strings are the agent harness's, captured from live Claude Code Stop
// payloads; cmd/attn's TestStopFacts covers extracting them from those payloads.
func TestStopIsNonTerminal(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		crons    int
		relax    bool
		want     bool
	}{
		{
			name:     "background work still running",
			statuses: []string{"running"},
			want:     true,
		},
		{
			// A wakeup hours away does not make this turn unfinished. The
			// transcript is flushed and the turn is over, so the end-of-turn work
			// applies — including asking the classifier how it ended, which a
			// cron-parked session was previously never asked.
			name:  "parked on a scheduled wakeup: the turn still ended",
			crons: 1,
			want:  false,
		},
		{
			// The background task is what defers the end of the turn. The cron
			// alongside it changes nothing.
			name:     "both outstanding",
			statuses: []string{"running"},
			crons:    1,
			want:     true,
		},
		{
			name:     "a finished background task is not outstanding work",
			statuses: []string{"completed"},
			want:     false,
		},
		{
			name:     "mixed statuses count as running if any is",
			statuses: []string{"completed", "running"},
			want:     true,
		},
		{
			name:     "status casing is the harness's, not ours",
			statuses: []string{"Running"},
			want:     true,
		},
		{
			name: "nothing outstanding: the turn ended",
			want: false,
		},
		{
			name:     "chief relax: its background work does not defer the end of the turn",
			statuses: []string{"running"},
			relax:    true,
			want:     false,
		},
		{
			name:     "chief relax: a parked schedule does not defer it either",
			statuses: []string{"running"},
			crons:    1,
			relax:    true,
			want:     false,
		},
		{
			name:  "chief relax: cron only, still a real end of turn",
			crons: 1,
			relax: true,
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &protocol.StopMessage{
				Cmd:                    protocol.CmdStop,
				ID:                     "sess",
				BackgroundTaskStatuses: tc.statuses,
			}
			if tc.crons > 0 {
				msg.PendingSessionCrons = protocol.Ptr(tc.crons)
			}
			if got := stopIsNonTerminal(msg, tc.relax); got != tc.want {
				t.Fatalf("stopIsNonTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestStopIsNonTerminal_LegacyHookClassifies covers the version skew the
// optional fields buy: a hook binary predating them reports neither fact, and the
// stop must read as terminal rather than parking the session in a state the
// daemon cannot see a reason for.
func TestStopIsNonTerminal_LegacyHookClassifies(t *testing.T) {
	msg := &protocol.StopMessage{Cmd: protocol.CmdStop, ID: "sess", TranscriptPath: "/tmp/t.jsonl"}
	if stopIsNonTerminal(msg, false) {
		t.Fatal("a legacy hook reports neither fact; the stop must read as terminal")
	}
}

// TestDaemon_StopCommand_BackgroundWork_StaysWorking is the wiring test: the hook
// reports facts rather than a state, handleStop files them, and the resolver's
// next tick is what colors the session. It also pins that the stop does not fall
// through to the end-of-turn path — classification on a yield reads a
// not-yet-flushed transcript and is what used to mis-detect these sessions as
// idle/unknown.
func TestDaemon_StopCommand_BackgroundWork_StaysWorking(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()
	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("bg-session", "Test", "/tmp/test"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// A transcript path that cannot be classified: if the stop wrongly falls
	// through to the end-of-turn path, the session lands on unknown/idle rather
	// than staying working.
	if err := c.SendStop("bg-session", "/nonexistent/transcript.jsonl", client.StopFacts{
		BackgroundTaskStatuses: []string{"running"},
	}); err != nil {
		t.Fatalf("SendStop error: %v", err)
	}

	waitForResolvedState(t, d, "bg-session", protocol.SessionStateWorking)
}

// TestDaemon_StopCommand_PendingCron_Parks pins that a cron-parked stop still
// reads as scheduled — now by way of the settle rather than by short-circuiting
// it, so the classifier gets its say about whether the turn asked for anything.
func TestDaemon_StopCommand_PendingCron_Parks(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()
	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("cron-session", "Test", "/tmp/test"); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := c.SendStop("cron-session", "/nonexistent/transcript.jsonl", client.StopFacts{
		PendingSessionCrons: 1,
	}); err != nil {
		t.Fatalf("SendStop error: %v", err)
	}

	waitForResolvedState(t, d, "cron-session", protocol.SessionStateScheduled)
}

// waitForResolvedState waits out the resolve tick. Nothing applies a state at the
// moment a source speaks any more, so a state assertion that reads the store
// straight after the socket call is asserting on the tick's timing rather than on
// the rule under test.
//
// Waiting here is also what surfaced the startup-recovery gap these tests used to
// out-run: both register while the listener is up but the startup prune has not
// run, and until pruneSessionsWithoutPTY took a cutoff it marked such a session
// `recoverable` — a state the resolver does not own and so never takes back.
func waitForResolvedState(t *testing.T, d *Daemon, sessionID string, want protocol.SessionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last protocol.SessionState
	for time.Now().Before(deadline) {
		if session := d.store.Get(sessionID); session != nil {
			last = session.State
			if last == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s state = %s, want %s", sessionID, last, want)
}
