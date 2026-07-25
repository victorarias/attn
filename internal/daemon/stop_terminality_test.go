package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// TestNonTerminalStopState locks the non-terminal-Stop precedence: running
// background work outranks a parked schedule, and either outranks classification.
// The relax cases lock the chief-of-staff relaxation: background work no longer
// pegs "working", but a parked schedule still parks "scheduled".
//
// The status strings are the agent harness's, captured from live Claude Code Stop
// payloads; cmd/attn's TestStopFacts covers extracting them from those payloads.
func TestNonTerminalStopState(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		crons    int
		relax    bool
		want     string
	}{
		{
			name:     "background running and cron pending -> working wins",
			statuses: []string{"running"},
			crons:    1,
			want:     protocol.StateWorking,
		},
		{
			name:  "cron pending, no background -> scheduled",
			crons: 1,
			want:  protocol.StateScheduled,
		},
		{
			name:     "background running, no cron -> working",
			statuses: []string{"running"},
			want:     protocol.StateWorking,
		},
		{
			name:     "completed background and cron pending -> scheduled (completed is not running)",
			statuses: []string{"completed"},
			crons:    1,
			want:     protocol.StateScheduled,
		},
		{
			name:     "mixed statuses count as running if any is",
			statuses: []string{"completed", "running"},
			want:     protocol.StateWorking,
		},
		{
			name:     "status casing is the harness's, not ours",
			statuses: []string{"Running"},
			want:     protocol.StateWorking,
		},
		{
			name: "nothing pending -> classify (empty)",
			want: "",
		},
		{
			name:     "chief relax: background running, no cron -> classify (empty), not working",
			statuses: []string{"running"},
			relax:    true,
			want:     "",
		},
		{
			name:     "chief relax: background running + cron pending -> scheduled, not working",
			statuses: []string{"running"},
			crons:    1,
			relax:    true,
			want:     protocol.StateScheduled,
		},
		{
			name:  "chief relax: cron only -> scheduled (unchanged by relax)",
			crons: 1,
			relax: true,
			want:  protocol.StateScheduled,
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
			if got := nonTerminalStopState(msg, tc.relax); got != tc.want {
				t.Fatalf("nonTerminalStopState() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNonTerminalStopState_LegacyHookClassifies covers the version skew the
// optional fields buy: a hook binary predating them reports neither fact, and the
// stop must read as terminal rather than parking the session in a state the
// daemon cannot see a reason for.
func TestNonTerminalStopState_LegacyHookClassifies(t *testing.T) {
	msg := &protocol.StopMessage{Cmd: protocol.CmdStop, ID: "sess", TranscriptPath: "/tmp/t.jsonl"}
	if got := nonTerminalStopState(msg, false); got != "" {
		t.Fatalf("nonTerminalStopState(legacy) = %q, want the terminal path", got)
	}
}

// TestDaemon_StopCommand_BackgroundWork_StaysWorking is the wiring test: the hook
// now reports facts rather than a state, so handleStop must apply the non-terminal
// state itself. It also pins that the stop does not fall through to the
// end-of-turn path — classification on a yield reads a not-yet-flushed transcript
// and is what used to mis-detect these sessions as idle/unknown.
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

	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].State != protocol.SessionStateWorking {
		t.Fatalf("state = %s, want %s", sessions[0].State, protocol.SessionStateWorking)
	}
}

// TestDaemon_StopCommand_PendingCron_Parks covers the other non-terminal branch:
// a stop parked on a scheduled wakeup reads as scheduled, not classified.
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

	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].State != protocol.SessionStateScheduled {
		t.Fatalf("state = %s, want %s", sessions[0].State, protocol.SessionStateScheduled)
	}
}
