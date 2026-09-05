package daemon

import (
	"io"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func addLedgerTestSession(t *testing.T, d *Daemon, id, directory string) {
	t.Helper()
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: directory, WorkspaceID: "ws-" + id,
		State:      protocol.SessionStateWaitingInput,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

// handleUnregister writes its response to the conn, so a test needs one that drains.
func drainedConn(t *testing.T) net.Conn {
	t.Helper()
	server, client := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, client) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return server
}

func TestClosingASessionKeepsItsLedgerRowAndHidesItEverywhereLive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "closing", t.TempDir())
	addLedgerTestSession(t, d, "staying", t.TempDir())
	if !d.store.OpenTurnIfClosed("closing", time.Now()) {
		t.Fatal("open a turn on the session about to close")
	}

	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: "closing"})

	entry := d.store.SessionLedgerEntry("closing")
	if entry == nil {
		t.Fatal("SessionLedgerEntry(closing) = nil, want the close recorded")
	}
	if protocol.Deref(entry.ClosedAt) == "" {
		t.Error("closed_at is empty, want the instant the session closed")
	}
	if by := protocol.Deref(entry.ClosedBy); by != store.SessionClosedByUser {
		t.Errorf("closed_by = %q, want %q", by, store.SessionClosedByUser)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "" {
		t.Errorf("close_reason = %q, want a user close to carry none", reason)
	}

	broadcast := d.mergedSessionsForBroadcast()
	if len(broadcast) != 1 || broadcast[0].ID != "staying" {
		t.Fatalf("broadcast sessions = %v, want only the session that stayed", broadcastIDs(broadcast))
	}
	for _, session := range d.currentStateProjection().Sessions {
		if session.ID == "closing" {
			t.Error("the Initial State projection still carries the closed session")
		}
	}
	if owed := d.turnOwedIDs(); slices.Contains(owed, "closing") {
		t.Errorf("turn accounting = %v, want the closed session's open turn to stop counting", owed)
	}
}

func broadcastIDs(sessions []protocol.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

// turnOwedIDs mirrors what the broadcast decorates, which is where the queue reads from.
func (d *Daemon) turnOwedIDs() []string {
	var owed []string
	for _, session := range d.mergedSessionsForBroadcast() {
		if protocol.Deref(session.TurnOwed) {
			owed = append(owed, session.ID)
		}
	}
	return owed
}

func TestARestartNeitherResurrectsNorReapsAClosedSession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	first, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "first.sock"))
	_ = d.store.Close()
	d.store = first
	addLedgerTestSession(t, d, "closed-before-restart", t.TempDir())
	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: "closed-before-restart"})
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	second := NewForTesting(filepath.Join(t.TempDir(), "second.sock"))
	_ = second.store.Close()
	second.store = reopened

	// The reaper and the recoverable sweep both walk the live list; a closed row
	// must be in neither, and must still be in the ledger when they are done.
	second.pruneSessionsWithoutPTY(time.Now().Add(time.Hour))

	if got := reopened.Get("closed-before-restart"); got != nil {
		t.Errorf("Get after restart = %+v, want the close to survive the sweep", got)
	}
	entry := reopened.SessionLedgerEntry("closed-before-restart")
	if entry == nil {
		t.Fatal("the closed session was reaped by a restart; the ledger is meant to outlive it")
	}
	if protocol.Deref(entry.ClosedAt) == "" {
		t.Error("the restart cleared closed_at, resurrecting the session")
	}
}

func TestARuntimeOutlivingItsCloseIsStoppedRatherThanRebuilt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "stubborn", t.TempDir())
	if _, err := d.store.CloseSession("stubborn", store.SessionClose{By: store.SessionClosedByUser}, time.Now()); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The tombstone sweep already ran, so nothing but the ledger says this is closed.
	d.store.ClearSessionIntentionalClose("stubborn")
	backend := &fakeSpawnBackend{sessionIDs: []string{"stubborn"}}
	d.ptyBackend = backend

	d.reconcileSessionsWithWorkerBackendState(t.Context(), false, false, time.Now())

	if got := d.store.Get("stubborn"); got != nil {
		t.Errorf("Get = %+v, want the reconcile to leave the session closed", got)
	}
	if killed := backend.killedSessionIDs(); !slices.Contains(killed, "stubborn") {
		t.Errorf("terminated %v, want the closed session's runtime stopped", killed)
	}
}

func TestClosingWithAReasonRecordsTheCloserAndTheReason(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "delegate", t.TempDir())

	d.closeSession("delegate", store.SessionClose{By: "sess-dispatcher", Reason: "brief delivered"})

	entry := d.store.SessionLedgerEntry("delegate")
	if entry == nil {
		t.Fatal("SessionLedgerEntry(delegate) = nil")
	}
	if by := protocol.Deref(entry.ClosedBy); by != "sess-dispatcher" {
		t.Errorf("closed_by = %q, want the closing session's id", by)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "brief delivered" {
		t.Errorf("close_reason = %q, want %q", reason, "brief delivered")
	}
}

func (b *fakeSpawnBackend) killedSessionIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.killed)
}
