package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

func TestUpdateSessionActivityRoundTripsTheLineAndItsCursor(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	if !s.UpdateSessionActivity("s1", "running the frontend test suite", at, "1024") {
		t.Fatal("update reported no change")
	}

	record := s.GetSessionActivity("s1")
	if record.Line != "running the frontend test suite" {
		t.Errorf("line = %q", record.Line)
	}
	if !record.At.Equal(at) {
		t.Errorf("at = %v, want %v", record.At, at)
	}
	// The cursor is the whole reason a refresh is cheap: it is where the next
	// read seeks to, and comparing it against the transcript's head is what says
	// a session has written nothing new.
	if record.Cursor != "1024" {
		t.Errorf("cursor = %q, want the cursor it was generated through", record.Cursor)
	}
}

// The wire carries the line and its stamp together or not at all — a client
// ages a line out by its stamp, so a line without one cannot be judged stale.
func TestGetAndListCarryTheActivityPair(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	s.UpdateSessionActivity("s1", "fixing a failing migration", at, "512")

	session := s.Get("s1")
	if protocol.Deref(session.Activity) != "fixing a failing migration" {
		t.Errorf("Get activity = %q", protocol.Deref(session.Activity))
	}
	if protocol.Deref(session.ActivityAt) != at.Format(docstore.TimeFormat) {
		t.Errorf("Get activity_at = %q", protocol.Deref(session.ActivityAt))
	}

	listed := s.List("")
	if len(listed) != 1 {
		t.Fatalf("List returned %d sessions", len(listed))
	}
	if protocol.Deref(listed[0].Activity) != "fixing a failing migration" {
		t.Errorf("List activity = %q", protocol.Deref(listed[0].Activity))
	}
	if protocol.Deref(listed[0].ActivityAt) != at.Format(docstore.TimeFormat) {
		t.Errorf("List activity_at = %q", protocol.Deref(listed[0].ActivityAt))
	}
}

func TestSessionWithoutAnActivityLineCarriesNeitherField(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	session := s.Get("s1")
	if session.Activity != nil || session.ActivityAt != nil {
		t.Errorf("a session that never generated a line carries activity=%v at=%v", session.Activity, session.ActivityAt)
	}
	if got := s.GetSessionActivity("s1"); got != (SessionActivity{}) {
		t.Errorf("GetSessionActivity = %+v, want zero", got)
	}
}

// The generator is the only writer. A session is re-added on every state change
// and on respawn, and neither may drop a line the generator paid for.
func TestReAddingASessionKeepsItsActivity(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	s.UpdateSessionActivity("s1", "running the frontend test suite", at, "1024")

	addTurnSession(t, s, "s1", protocol.SessionStateIdle)

	if got := protocol.Deref(s.Get("s1").Activity); got != "running the frontend test suite" {
		t.Errorf("activity = %q after a re-add, want it kept", got)
	}
	if got := s.GetSessionActivity("s1").Cursor; got != "1024" {
		t.Errorf("cursor = %q after a re-add, want it kept", got)
	}
}

// Clearing is the "this line is wrong, forget it" path. It drops the cursor
// too, so the next run re-seeds from head instead of reading a delta against a
// line that no longer exists.
func TestClearingActivityAlsoDropsTheCursor(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)
	s.UpdateSessionActivity("s1", "running the frontend test suite", time.Now(), "1024")

	if !s.UpdateSessionActivity("s1", "", time.Time{}, "") {
		t.Fatal("clear reported no change")
	}
	if got := s.GetSessionActivity("s1"); got != (SessionActivity{}) {
		t.Errorf("GetSessionActivity = %+v after a clear, want zero", got)
	}
	session := s.Get("s1")
	if session.Activity != nil || session.ActivityAt != nil {
		t.Errorf("activity=%v at=%v after a clear, want both absent", session.Activity, session.ActivityAt)
	}
}

func TestUpdateSessionActivityReportsAMissingSession(t *testing.T) {
	s := newTurnStore(t)
	if s.UpdateSessionActivity("nobody", "doing something", time.Now(), "1") {
		t.Error("writing activity for a session that does not exist reported a change")
	}
}
