package pty

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestVirtualScreenSnapshot_RoundTrip(t *testing.T) {
	screen := newVirtualScreen(20, 6)
	screen.Observe([]byte("hello\r\nworld"))

	snap, ok := screen.Snapshot()
	if !ok {
		t.Fatal("expected snapshot to be available")
	}
	if snap.cols != 20 || snap.rows != 6 {
		t.Fatalf("snapshot size = %dx%d, want 20x6", snap.cols, snap.rows)
	}
	if len(snap.payload) == 0 {
		t.Fatal("expected non-empty snapshot payload")
	}

	replayed := vt10x.New(vt10x.WithSize(int(snap.cols), int(snap.rows)))
	if _, err := replayed.Write(snap.payload); err != nil {
		t.Fatalf("replay write error: %v", err)
	}

	got := replayed.String()
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected replayed screen to contain hello, got:\n%s", got)
	}
	if !strings.Contains(got, "world") {
		t.Fatalf("expected replayed screen to contain world, got:\n%s", got)
	}
}

func TestVirtualScreenSnapshot_PreservesAltScreenVisibleFrame(t *testing.T) {
	screen := newVirtualScreen(20, 6)
	screen.Observe([]byte("primary"))
	screen.Observe([]byte("\x1b[?1049h\x1b[2J\x1b[HALT"))

	snap, ok := screen.Snapshot()
	if !ok {
		t.Fatal("expected snapshot to be available")
	}

	replayed := vt10x.New(vt10x.WithSize(int(snap.cols), int(snap.rows)))
	if _, err := replayed.Write(snap.payload); err != nil {
		t.Fatalf("replay write error: %v", err)
	}

	got := replayed.String()
	if !strings.Contains(got, "ALT") {
		t.Fatalf("expected replayed alt-screen frame to contain ALT, got:\n%s", got)
	}
	if replayed.Mode()&vt10x.ModeAltScreen == 0 {
		t.Fatal("expected replayed terminal to remain in alt-screen mode")
	}
}

func TestVirtualScreenSnapshot_PreservesForegroundColor(t *testing.T) {
	screen := newVirtualScreen(10, 4)
	screen.Observe([]byte("\x1b[31mR\x1b[0m"))

	snap, ok := screen.Snapshot()
	if !ok {
		t.Fatal("expected snapshot to be available")
	}

	replayed := vt10x.New(vt10x.WithSize(int(snap.cols), int(snap.rows)))
	if _, err := replayed.Write(snap.payload); err != nil {
		t.Fatalf("replay write error: %v", err)
	}

	replayed.Lock()
	cell := replayed.Cell(0, 0)
	replayed.Unlock()

	if cell.Char != 'R' {
		t.Fatalf("cell(0,0).char = %q, want R", cell.Char)
	}
	if cell.FG != vt10x.Red {
		t.Fatalf("cell(0,0).fg = %v, want %v", cell.FG, vt10x.Red)
	}
}

func TestSessionInfo_IncludesScreenSnapshotWhenAvailable(t *testing.T) {
	session := &Session{
		id:         "codex-1",
		agent:      "codex",
		cols:       12,
		rows:       4,
		scrollback: NewRingBuffer(1024),
		screen:     newVirtualScreen(12, 4),
		running:    true,
	}
	session.screen.Observe([]byte("snapshot"))

	info := session.info()
	if len(info.ScreenSnapshot) == 0 {
		t.Fatal("expected non-empty screen snapshot in attach info")
	}
	if !info.ScreenSnapshotFresh {
		t.Fatal("expected screen snapshot to be marked fresh")
	}
	if info.ScreenCols != 12 || info.ScreenRows != 4 {
		t.Fatalf("screen size = %dx%d, want 12x4", info.ScreenCols, info.ScreenRows)
	}
}

func TestSessionInfo_OmitsScreenSnapshotForNonCodexSessions(t *testing.T) {
	session := &Session{
		id:         "shell-1",
		agent:      "shell",
		cols:       12,
		rows:       4,
		scrollback: NewRingBuffer(1024),
		running:    true,
	}

	info := session.info()
	if len(info.ScreenSnapshot) != 0 {
		t.Fatalf("expected no screen snapshot, got %d bytes", len(info.ScreenSnapshot))
	}
	if info.ScreenSnapshotFresh {
		t.Fatal("expected ScreenSnapshotFresh=false for non-codex session")
	}
}

func TestVirtualScreenSnapshot_RoundTripFullWidthParity(t *testing.T) {
	screen := newVirtualScreen(5, 3)
	screen.Observe([]byte("ABCDE\r\n12345\r\nxy"))

	snap, ok := screen.Snapshot()
	if !ok {
		t.Fatal("expected snapshot to be available")
	}

	replayed := vt10x.New(vt10x.WithSize(int(snap.cols), int(snap.rows)))
	if _, err := replayed.Write(snap.payload); err != nil {
		t.Fatalf("replay write error: %v", err)
	}

	want := screen.term.String()
	got := replayed.String()
	if got != want {
		t.Fatalf("replayed screen mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}
