package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
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
// Boundary-bound: this and the cron test below run a started daemon — a real
// unix listener, a real client dialing it. Their yielded-stop siblings bubble
// because they drive handleStop directly.
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

// TestDaemon_StopCommand_PendingCron_Settles pins that a cron-parked stop runs
// the end-of-turn path like any other: the classifier gets its say, and the
// session settles into the user's queue instead of being excused from it.
// Boundary-bound: started daemon and a real socket, as above.
func TestDaemon_StopCommand_PendingCron_Settles(t *testing.T) {
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

	waitForResolvedState(t, d, "cron-session", protocol.SessionStateIdle)
}

// recordingClassifier captures the input it was asked to judge and answers with
// a fixed verdict.
type recordingClassifier struct {
	state string
	mu    sync.Mutex
	texts []string
}

func (c *recordingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return c.state, nil
}

func (c *recordingClassifier) Texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// yieldedStopDaemon is the shared fixture for the yield-judgment wiring tests: a
// claude session whose turn ran, settled its heartbeat, and then yielded on a
// Stop reporting one running background task, with the judge's answer faked.
// It replays the 2026-08-01 incident's timeline up to the verdict.
func yieldedStopDaemon(t *testing.T, d *Daemon, verdict string) (*Daemon, *recordingClassifier) {
	t.Helper()
	judge := &recordingClassifier{state: verdict}
	d.classifier = judge
	d.classificationTranscriptExtractor = func(*protocol.Session, string, int, time.Time) (string, string, error) {
		return "The profile build is still running in the background; I'll continue when it completes.", "turn-1", nil
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "yielded",
		Agent:          protocol.SessionAgentClaude,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
		// Open todos on purpose: a yielded stop must not read them as "waiting
		// on the user" — the plan is unfinished precisely because the turn is not.
		Todos: []string{"[→] wait for the build", "[ ] verify live"},
	})
	d.recordBracketEvidence("yielded", protocol.StateWorking)
	d.recordPTYEvidence("yielded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now.Add(-time.Second)})
	d.recordPTYEvidence("yielded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.handleStop(drainingConn(t), &protocol.StopMessage{
		Cmd:                    protocol.CmdStop,
		ID:                     "yielded",
		TranscriptPath:         "/tmp/transcript.jsonl",
		BackgroundTaskStatuses: []string{"running"},
	})

	// The judgment is dispatched async, on the retry loop handleStop owns; run it
	// out and the verdict is either filed or never coming.
	settleStopClassification(t)
	if e, ok := d.evidenceTable().snapshot("yielded"); !ok || e.LastClassifier == nil {
		t.Fatalf("yield verdict never landed as evidence (classifier calls: %d)", len(judge.Texts()))
	}
	return d, judge
}

// TestDaemon_YieldedStop_ParkedVerdictHoldsWorkingPastPromptIdle is the
// 2026-08-01 incident, end to end: the yield is judged parked, and claude's flat
// 60s idle_prompt notification — which used to settle the session idle and ring
// the user mid-build — no longer outranks the verdict.
func TestDaemon_YieldedStop_ParkedVerdictHoldsWorkingPastPromptIdle(t *testing.T) {
	base := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, base)
		d, judge := yieldedStopDaemon(t, base, classifier.VerdictParked)

		// The judge must have seen the yield: the harness-facts line is the
		// precondition of the parked verdict.
		texts := judge.Texts()
		if len(texts) != 1 || !strings.Contains(texts[0], "[harness facts]") {
			t.Fatalf("judge input missing the harness-facts line: %q", texts)
		}

		// The notification that broke the incident open, a minute into the wait.
		d.recordNotificationEvidence("yielded", notifyIdlePrompt, "Claude is waiting for your input")
		d.resolveAllSessions(time.Now())

		sess := d.store.Get("yielded")
		if sess == nil {
			t.Fatal("session missing after resolve")
		}
		if sess.State != protocol.StateWorking {
			t.Fatalf("state = %s, want %s: a parked verdict outranks the prompt-idle confirmation", sess.State, protocol.StateWorking)
		}
	})
}

// TestDaemon_YieldedStop_DoneVerdictSettles pins the inverse failure the parked
// hold must not reintroduce: a turn that finished but left a process running
// (a dev server, a watcher) settles into the user's queue on its verdict instead
// of sitting green forever behind a task that will never exit.
func TestDaemon_YieldedStop_DoneVerdictSettles(t *testing.T) {
	base := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, base)
		d, _ := yieldedStopDaemon(t, base, protocol.StateIdle)

		d.resolveAllSessions(time.Now())

		sess := d.store.Get("yielded")
		if sess == nil {
			t.Fatal("session missing after resolve")
		}
		if sess.State != protocol.StateIdle {
			t.Fatalf("state = %s, want %s: an idle verdict on a yield means the running process is a leftover", sess.State, protocol.StateIdle)
		}
	})
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
