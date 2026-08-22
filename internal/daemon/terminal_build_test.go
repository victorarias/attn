package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
)

func newTerminalBuildDaemon(t *testing.T, backend *fakeSpawnBackend) (*Daemon, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "terminal-build.sock"))
	d.ptyBackend = backend
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	return d, id
}

func TestTerminalBuild_MatchingWorkerIsNotStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{
		terminalBuild:      buildinfo.SnapshotFormat,
		terminalBuildKnown: true,
	})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v for a same-build worker, want absent", *clone.TerminalBuildStale)
	}
}

func TestTerminalBuild_DifferentWorkerIsStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
	})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent for a worker built against a different libghostty-vt")
	}
}

// The case the first bump after this change actually produces: every worker
// alive today predates the field and reports nothing at all.
func TestTerminalBuild_SilentWorkerIsStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{terminalBuildKnown: true})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent for a worker that reported no format")
	}
}

// No handshake has landed yet. That is not a verdict, and guessing one would
// flash the notice at every session on every daemon start.
func TestTerminalBuild_UnansweredWorkerIsNotStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v before the worker answered, want absent", *clone.TerminalBuildStale)
	}
}
