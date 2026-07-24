package pty

import (
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func TestScreenSnapshot_IncludesScreenSnapshotWhenAvailable(t *testing.T) {
	gt := newTestGhostty(t, 12, 4)
	session := &Session{
		id:      "codex-1",
		agent:   "codex",
		cols:    12,
		rows:    4,
		ghostty: gt,
		running: true,
	}
	gt.Write([]byte("snapshot"))

	info := session.screenSnapshot()
	if len(info.ScreenSnapshot) == 0 {
		t.Fatal("expected non-empty screen snapshot")
	}
	if !info.ScreenSnapshotFresh {
		t.Fatal("expected screen snapshot to be marked fresh")
	}
	if info.ScreenCols != 12 || info.ScreenRows != 4 {
		t.Fatalf("screen size = %dx%d, want 12x4", info.ScreenCols, info.ScreenRows)
	}
	assertScreenSnapshotReplays(t, gt, info)
}

func assertScreenSnapshotReplays(t *testing.T, source *ghosttyvt.Terminal, info AttachInfo) {
	t.Helper()
	restored, err := ghosttyvt.New(int(info.ScreenCols), int(info.ScreenRows), ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("new restored ghostty terminal: %v", err)
	}
	t.Cleanup(restored.Close)
	restored.Write(info.ScreenSnapshot)
	if got, want := restored.ViewportText(), source.ViewportText(); got != want {
		t.Fatalf("replayed viewport text = %q, want %q", got, want)
	}
	gotX, gotY := restored.CursorPos()
	wantX, wantY := source.CursorPos()
	if gotX != wantX || gotY != wantY {
		t.Fatalf("replayed cursor = (%d,%d), want (%d,%d)", gotX, gotY, wantX, wantY)
	}
}

func TestScreenSnapshot_IncludesScreenSnapshotForShellSessions(t *testing.T) {
	gt := newTestGhostty(t, 12, 4)
	session := &Session{
		id:      "shell-1",
		agent:   "shell",
		cols:    12,
		rows:    4,
		ghostty: gt,
		running: true,
	}
	gt.Write([]byte("prompt"))

	info := session.screenSnapshot()
	if len(info.ScreenSnapshot) == 0 {
		t.Fatal("expected non-empty screen snapshot for shell session")
	}
	if !info.ScreenSnapshotFresh {
		t.Fatal("expected ScreenSnapshotFresh=true for shell session")
	}
}

func TestScreenSnapshot_ReadOnlyAndLean(t *testing.T) {
	gt := newTestGhostty(t, 12, 4)
	session := &Session{
		id:          "codex-1",
		agent:       "codex",
		cols:        12,
		rows:        4,
		ghostty:     gt,
		running:     true,
		subscribers: make(map[string]*sessionSubscriber),
	}
	gt.Write([]byte("snapshot"))
	// A session that has applied 7 chunks holds seqCounter=7 AND
	// lastReplaySeq=7; the snapshot's LastSeq reports the replay-locked
	// watermark (the last chunk actually baked into the terminal), never the
	// raw counter — see TestScreenSnapshotSeqConsistency.
	session.seqCounter.Store(7)
	session.lastReplaySeq = 7

	info := session.screenSnapshot()

	if len(info.ScreenSnapshot) == 0 || !info.ScreenSnapshotFresh {
		t.Fatal("expected a fresh screen snapshot")
	}
	if info.ScreenCols != 12 || info.ScreenRows != 4 {
		t.Fatalf("screen size = %dx%d, want 12x4", info.ScreenCols, info.ScreenRows)
	}
	if info.LastSeq != 7 {
		t.Fatalf("LastSeq = %d, want 7", info.LastSeq)
	}
	if !info.Running {
		t.Fatal("expected Running=true")
	}

	// Read-only: no subscriber registered.
	if len(session.subscribers) != 0 {
		t.Fatalf("snapshot must not register a subscriber, got %d", len(session.subscribers))
	}
}

func TestManagerSnapshot_UnknownSessionErrors(t *testing.T) {
	m := NewManager(nil)
	if _, err := m.Snapshot("does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown session")
	}
}
