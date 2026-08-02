package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// A snooze closes the open turn as it defers. The two are one write because a
// deadline stamped beside a still-open turn would be a row in the queue that no
// state change could ever move.
func TestSnoozeTurnSettlesAsItDefers(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWaitingInput)

	opened := time.Now().Add(-time.Hour)
	if !s.OpenTurnIfClosed("s1", opened) {
		t.Fatal("setup: no turn opened")
	}

	now := time.Now()
	until := now.Add(30 * time.Minute)
	if !s.SnoozeTurn("s1", until, now) {
		t.Fatal("SnoozeTurn reported no write")
	}

	stamps := s.TurnStamps("s1")
	if !stamps.SettledAt.After(stamps.OpenedAt) {
		t.Errorf("settled=%s opened=%s: the snooze left a turn open", stamps.SettledAt, stamps.OpenedAt)
	}
	if got := stamps.SnoozedUntil.UTC(); !got.Equal(until.UTC().Truncate(time.Nanosecond)) {
		t.Errorf("snoozed_until = %s, want %s", got, until.UTC())
	}
}

// Snooze reaches any agent, not only one that owes a turn: deferring a run
// before it finishes is the case the feature exists for.
func TestSnoozeTurnWithNoOpenTurn(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	now := time.Now()
	if !s.SnoozeTurn("s1", now.Add(time.Hour), now) {
		t.Fatal("SnoozeTurn reported no write")
	}
	if s.TurnStamps("s1").SnoozedUntil.IsZero() {
		t.Error("no deadline stored for a session that had no turn open")
	}
}

func TestWakeTurnClearsTheDeadlineOnlyOnce(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateIdle)

	now := time.Now()
	s.SnoozeTurn("s1", now.Add(time.Hour), now)

	if !s.WakeTurn("s1") {
		t.Fatal("WakeTurn reported no change on a live snooze")
	}
	if !s.TurnStamps("s1").SnoozedUntil.IsZero() {
		t.Error("deadline survived the wake")
	}
	// The second wake reporting false is what lets the daemon stay quiet when a
	// timer and a hand wake race.
	if s.WakeTurn("s1") {
		t.Error("WakeTurn reported a change on a session that was not snoozed")
	}
}

// A wake must not re-open the turn the snooze closed. Whether one opens is the
// daemon's call, from the state the agent is actually in.
func TestWakeTurnDoesNotReopenTheTurn(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWaitingInput)

	now := time.Now()
	s.OpenTurnIfClosed("s1", now.Add(-time.Hour))
	s.SnoozeTurn("s1", now.Add(time.Hour), now)
	s.WakeTurn("s1")

	stamps := s.TurnStamps("s1")
	if stamps.OpenedAt.After(stamps.SettledAt) {
		t.Error("the wake re-opened the turn by itself")
	}
}

func TestSnoozedSessionsListsLiveDeadlines(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "deferred", protocol.SessionStateIdle)
	addTurnSession(t, s, "lapsed", protocol.SessionStateIdle)
	addTurnSession(t, s, "awake", protocol.SessionStateIdle)

	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	s.SnoozeTurn("deferred", future, now)
	// A deadline already past is stored, not rejected: the daemon fires on it
	// immediately at start-up, which is how a snooze that lapsed while the daemon
	// was down behaves like one that lapsed while it was up.
	s.SnoozeTurn("lapsed", past, now)

	snoozed := s.SnoozedSessions()
	if len(snoozed) != 2 {
		t.Fatalf("SnoozedSessions returned %d entries, want 2: %+v", len(snoozed), snoozed)
	}
	if got := snoozed["deferred"].UTC(); !got.Equal(future.UTC()) {
		t.Errorf("deferred deadline = %s, want %s", got, future.UTC())
	}
	if got := snoozed["lapsed"].UTC(); !got.Equal(past.UTC()) {
		t.Errorf("lapsed deadline = %s, want %s", got, past.UTC())
	}
	if _, ok := snoozed["awake"]; ok {
		t.Error("a session that was never snoozed is listed")
	}
}

// The deadline is in the database, not in the daemon's memory. This is the only
// thing that makes a snooze survive a restart, so it is pinned against the real
// SQLite path rather than the in-memory branch.
func TestSnoozeSurvivesReopeningTheDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "snooze.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addTurnSession(t, s, "s1", protocol.SessionStateIdle)
	now := time.Now()
	until := now.Add(90 * time.Minute)
	s.SnoozeTurn("s1", until, now)
	s.Close()

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if got := reopened.TurnStamps("s1").SnoozedUntil.UTC(); !got.Equal(until.UTC()) {
		t.Errorf("deadline after reopen = %s, want %s", got, until.UTC())
	}
	if _, ok := reopened.SnoozedSessions()["s1"]; !ok {
		t.Error("the reopened store does not list the snooze, so no wake timer would be rebuilt")
	}
}

func TestSnoozeTurnRejectsUnknownSession(t *testing.T) {
	s := newTurnStore(t)
	now := time.Now()
	if s.SnoozeTurn("nope", now.Add(time.Hour), now) {
		t.Error("snoozed a session that does not exist")
	}
	if s.WakeTurn("nope") {
		t.Error("woke a session that does not exist")
	}
}
