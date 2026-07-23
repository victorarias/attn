package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/ptybackend"
)

// snapshotSeedScreen backs grid-tile seeding from the worker's fresh
// vt10x-rendered screen (Manager.Snapshot). It seeds only when a fresh frame is
// present.

func TestSnapshotSeedScreenPrefersFreshWorkerScreen(t *testing.T) {
	info := ptybackend.AttachInfo{
		ScreenSnapshot:      []byte("rendered-by-worker"),
		ScreenCols:          80,
		ScreenRows:          24,
		ScreenSnapshotFresh: true,
		Cols:                80,
		Rows:                24,
	}

	screen, ok := snapshotSeedScreen(info)
	if !ok {
		t.Fatal("expected a screen when a fresh worker snapshot is present")
	}
	if string(screen.Payload) != "rendered-by-worker" {
		t.Fatalf("expected the worker payload, got %q", screen.Payload)
	}
	if screen.Cols != 80 || screen.Rows != 24 {
		t.Fatalf("screen geometry = %dx%d, want 80x24", screen.Cols, screen.Rows)
	}
}

func TestSnapshotSeedScreenReturnsNothingWithoutFreshScreen(t *testing.T) {
	// No fresh worker screen (e.g. a session that has produced no output yet, or
	// a non-macOS build where vt10x has nothing to render): nothing to seed.
	if _, ok := snapshotSeedScreen(ptybackend.AttachInfo{Cols: 80, Rows: 24}); ok {
		t.Fatal("expected no screen when no fresh frame is available")
	}
}
