package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func newTurnStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addTurnSession(t *testing.T, s *Store, id string, state protocol.SessionState) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	if err := s.AddChecked(&protocol.Session{
		ID:             id,
		Label:          id,
		Directory:      "/tmp/" + id,
		WorkspaceID:    "ws-1",
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	}); err != nil {
		t.Fatalf("add session %s: %v", id, err)
	}
}

func TestTurnStampsStartEmpty(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	stamps := s.TurnStamps("s1")
	if !stamps.OpenedAt.IsZero() || !stamps.SettledAt.IsZero() {
		t.Fatalf("stamps = %+v, want both zero", stamps)
	}
}

// The guard on OpenTurnIfClosed is what keeps a row from moving in the queue
// while the user works with the agent: a second turn-opening state must not
// re-age an outstanding turn.
func TestOpenTurnIfClosedDoesNotMoveAnOpenTurn(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWaitingInput)

	first := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if !s.OpenTurnIfClosed("s1", first) {
		t.Fatal("first open reported no change")
	}
	if s.OpenTurnIfClosed("s1", first.Add(time.Hour)) {
		t.Error("second open reported a change; the turn was already open")
	}
	if got := s.TurnStamps("s1").OpenedAt; !got.Equal(first) {
		t.Errorf("opened_at = %v, want %v", got, first)
	}
}

func TestSettleThenOpenStartsANewTurn(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWaitingInput)

	opened := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	settled := opened.Add(time.Minute)
	reopened := opened.Add(time.Hour)

	s.OpenTurnIfClosed("s1", opened)
	if !s.SettleTurn("s1", settled) {
		t.Fatal("settle reported no change")
	}
	stamps := s.TurnStamps("s1")
	if stamps.OpenedAt.After(stamps.SettledAt) {
		t.Fatalf("still owed after settling: %+v", stamps)
	}

	if !s.OpenTurnIfClosed("s1", reopened) {
		t.Fatal("re-open after settling reported no change")
	}
	stamps = s.TurnStamps("s1")
	if !stamps.OpenedAt.Equal(reopened) {
		t.Errorf("opened_at = %v, want %v", stamps.OpenedAt, reopened)
	}
	if !stamps.OpenedAt.After(stamps.SettledAt) {
		t.Error("session does not owe a turn after re-opening")
	}
}

// Settling a session that is still running is the ordinary move, not an edge
// case — it is what keeps an empty queue reachable while agents work.
func TestSettleWithoutAnOpenTurnIsRecorded(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	settled := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if !s.SettleTurn("s1", settled) {
		t.Fatal("settle reported no change")
	}
	if got := s.TurnStamps("s1").SettledAt; !got.Equal(settled) {
		t.Errorf("settled_at = %v, want %v", got, settled)
	}
	// A turn opened before that settle stays closed.
	if s.OpenTurnIfClosed("s1", settled.Add(-time.Hour)) {
		if stamps := s.TurnStamps("s1"); stamps.OpenedAt.After(stamps.SettledAt) {
			t.Error("a turn opened before the settle stamp still owes")
		}
	}
}

func TestTurnStampsForUnknownSession(t *testing.T) {
	s := newTurnStore(t)
	if s.OpenTurnIfClosed("nope", time.Now()) {
		t.Error("opened a turn on a session that does not exist")
	}
	if s.SettleTurn("nope", time.Now()) {
		t.Error("settled a turn on a session that does not exist")
	}
	if stamps := s.TurnStamps("nope"); !stamps.OpenedAt.IsZero() {
		t.Errorf("stamps = %+v, want zero", stamps)
	}
}

// The migration backfills sessions already sitting in a turn-opening state, so
// enabling the queue shows the honest outstanding board rather than an empty one.
func TestMigration81BackfillsOpenTurnsFromStateSince(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-81.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen) VALUES
			('waiting',  'Waiting',  '/tmp/a', 'waiting_input',    '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z'),
			('approval', 'Approval', '/tmp/b', 'pending_approval', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z'),
			('unknown',  'Unknown',  '/tmp/c', 'unknown',          '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z'),
			('working',  'Working',  '/tmp/d', 'working',          '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z'),
			('idle',     'Idle',     '/tmp/e', 'idle',             '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z');
		ALTER TABLE sessions DROP COLUMN turn_opened_at;
		ALTER TABLE sessions DROP COLUMN turn_settled_at;
		DELETE FROM schema_migrations WHERE version >= 81;
	`); err != nil {
		t.Fatalf("seed pre-81 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-81 database: %v", err)
	}

	migrated, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	for id, want := range map[string]string{
		"waiting":  "2026-07-26T10:00:00Z",
		"approval": "2026-07-26T11:00:00Z",
		"unknown":  "2026-07-26T12:00:00Z",
	} {
		got := migrated.TurnStamps(id).OpenedAt
		if got.IsZero() {
			t.Errorf("%s: no turn opened by the backfill", id)
			continue
		}
		if got.Format(time.RFC3339) != want {
			t.Errorf("%s: opened_at = %s, want %s (the age it has been waiting)", id, got.Format(time.RFC3339), want)
		}
	}
	for _, id := range []string{"working", "idle"} {
		if !migrated.TurnStamps(id).OpenedAt.IsZero() {
			t.Errorf("%s: backfill opened a turn for a state that does not open one", id)
		}
	}
}
