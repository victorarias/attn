package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"

	"github.com/victorarias/attn/internal/protocol"
)

func newTurnDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func addTurnSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent, workspaceID string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Agent:          agent,
		Label:          id,
		Directory:      "/tmp/" + id,
		WorkspaceID:    workspaceID,
		State:          protocol.StateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
}

func moveTo(d *Daemon, id, state string) {
	d.applyState(sessionStateChange{sessionID: id, state: state, cause: liveSignal{}})
}

func owed(t *testing.T, d *Daemon, id string) bool {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.TurnOwed)
}

// The core rule: prompting an agent does not settle it. Only the user does.
func TestTurnSurvivesTheAgentGoingBackToWork(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	if owed(t, d, "s1") {
		t.Fatal("a launching session owes a turn")
	}

	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Fatal("a session waiting for input owes no turn")
	}
	openedAt := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)

	// The user prompts it. It is still theirs.
	moveTo(d, "s1", protocol.StateWorking)
	if !owed(t, d, "s1") {
		t.Fatal("prompting the agent settled its turn; only the user settles")
	}

	// It stops again. The turn keeps the age it opened at, so the row does not
	// move in the queue while the user works with it.
	moveTo(d, "s1", protocol.StateWaitingInput)
	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt); got != openedAt {
		t.Errorf("turn_opened_at = %q, want %q (unchanged across the run)", got, openedAt)
	}
}

// A run you asked for that finished without a question is still yours to read.
func TestAFinishedRunOwesATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	if owed(t, d, "s1") {
		t.Fatal("a working session owes a turn")
	}

	moveTo(d, "s1", protocol.StateIdle)
	if !owed(t, d, "s1") {
		t.Fatal("a finished run owes no turn; nobody has read its result")
	}
}

// A session you launched and have not spoken to resolves to idle sitting at its
// prompt. Nothing will ever happen in it until you type, so it owes a turn — the
// same one a finished run owes, which is why the resolver need not distinguish
// them.
func TestASessionAtItsPromptOwesATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	d.applyState(sessionStateChange{
		sessionID: "s1",
		state:     protocol.StateIdle,
		cause:     resolverObservation{},
	})

	if !owed(t, d, "s1") {
		t.Fatal("a launched session sitting at its prompt owes no turn")
	}
}

func TestSettleIsTheOnlyExit(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Fatal("no turn to settle")
	}

	d.handleSettleTurn(&protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})
	if owed(t, d, "s1") {
		t.Fatal("settle did not close the turn")
	}

	// A later turn-opening state brings it back, at a new age.
	moveTo(d, "s1", protocol.StateWorking)
	if owed(t, d, "s1") {
		t.Fatal("working re-opened a turn; only turn-opening states do")
	}
	moveTo(d, "s1", protocol.StatePendingApproval)
	if !owed(t, d, "s1") {
		t.Fatal("a session waiting for approval after being settled owes no turn")
	}
}

// Settling an agent that is still running is the ordinary move — it is what
// keeps an empty queue reachable while agents work.
func TestSettleWhileWorking(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	moveTo(d, "s1", protocol.StateWorking)
	d.handleSettleTurn(&protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})

	if owed(t, d, "s1") {
		t.Fatal("settling a running agent left the turn open")
	}
}

func TestSettleBroadcastsTheSession(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	capture := captureBroadcasts(d)
	d.handleSettleTurn(&protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})

	events := capture.snapshot()
	if len(events) == 0 {
		t.Fatal("settle broadcast nothing; a second client would still show the row")
	}
	last := events[len(events)-1]
	if last.Session == nil || protocol.Deref(last.Session.TurnOwed) {
		t.Errorf("broadcast session = %+v, want turn_owed absent", last.Session)
	}
}

func TestShellSessionsNeverOweATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "shell1", protocol.SessionAgentShell, "ws1")

	// A shell that somehow reaches a turn-opening state still never queues:
	// nothing would ever settle a terminal pane.
	moveTo(d, "shell1", protocol.StateWaitingInput)
	if owed(t, d, "shell1") {
		t.Fatal("a shell pane owes a turn")
	}
	if d.store.TurnStamps("shell1").OpenedAt.IsZero() {
		t.Fatal("no turn was opened at all; the case proves nothing about the exclusion")
	}
}

func TestChiefOfStaffNeverOwesATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "chief", protocol.SessionAgentClaude, "ws1")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatalf("set chief: %v", err)
	}

	moveTo(d, "chief", protocol.StateWaitingInput)
	if owed(t, d, "chief") {
		t.Fatal("the chief queues; it has its own anchored slot instead")
	}
	if d.store.TurnStamps("chief").OpenedAt.IsZero() {
		t.Fatal("no turn was opened at all; the case proves nothing about the exclusion")
	}
	if !protocol.Deref(d.sessionForBroadcast(d.store.Get("chief")).ChiefOfStaff) {
		t.Fatal("the session is not decorated as the chief; the case proves nothing")
	}
}

func TestPinnedAndMutedWorkspacesAreFilteredAtRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(d *Daemon, workspaceID string)
	}{
		{"pinned", func(d *Daemon, id string) { d.store.SetWorkspacePinned(id, true) }},
		{"muted", func(d *Daemon, id string) { d.store.SetWorkspaceMuted(id, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTurnDaemon(t)
			d.store.AddWorkspace(&protocol.Workspace{ID: "ws1", Title: "ws1", Directory: "/tmp/ws1"})
			addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

			tc.set(d, "ws1")
			moveTo(d, "s1", protocol.StateWaitingInput)
			if owed(t, d, "s1") {
				t.Fatalf("a session in a %s workspace owes a turn", tc.name)
			}

			// The stamp was still taken, so lifting the exclusion surfaces what
			// was outstanding at its true age rather than starting from nothing.
			if d.store.TurnStamps("s1").OpenedAt.IsZero() {
				t.Fatal("the exclusion suppressed the stamp; it must filter at read")
			}
		})
	}
}

// A settle must survive the agent working on with no bracket open.
//
// This is the live failure that produced the resolver's settle window, stated
// where it was felt. Claude repaints its title every ~1.92s while a `/compact`
// runs, and a compaction runs between turns, so the previous turn's bracket is
// closed and the title is the only evidence left. Reading a gap past the 1.5s
// heartbeat TTL as a settle made the resolver publish idle whenever a frame aged
// out and working when the next one landed — and every one of those idle edges
// opens a turn. The session the user had just settled was back in the queue a
// second later, with no way to get it out for as long as the compaction ran.
func TestASettleSurvivesAnAgentRepaintingSlowerThanTheHeartbeatTTL(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentClaude, "ws1")

	// A turn ran and ended: the bracket opened and closed, which is the state a
	// compaction starts from.
	d.recordBracketEvidence("s1", protocol.StateWorking)
	d.recordBracketEvidence("s1", protocol.StateIdle)
	moveTo(d, "s1", protocol.StateIdle)
	if !owed(t, d, "s1") {
		t.Fatal("a finished turn owes nothing; nothing to settle")
	}

	d.handleSettleTurn(&protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})
	if owed(t, d, "s1") {
		t.Fatal("settle did not close the turn")
	}

	// The compaction: title frames at the measured cadence, resolver ticks once
	// a second, so the same frame is read at several ages.
	const repaint = 1920 * time.Millisecond
	base := time.Now()
	for tick := 1; tick <= 30; tick++ {
		at := base.Add(time.Duration(tick) * time.Second)
		d.recordPTYEvidence("s1", pty.Observation{
			Source: pty.SourceHeartbeat,
			Claim:  "busy",
			Detail: "⠐ compacting",
			At:     base.Add((at.Sub(base) / repaint) * repaint),
		})
		d.resolveAllSessions(at)

		if owed(t, d, "s1") {
			t.Fatalf("tick %d (%s in): the settled turn re-opened while the agent was still working",
				tick, at.Sub(base))
		}
	}
	if state := d.store.Get("s1").State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working: the agent was painting busy frames throughout", state)
	}
}
