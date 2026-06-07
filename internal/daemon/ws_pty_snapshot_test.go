package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/ptybackend"
)

// snapshotSeedScreen backs grid-tile seeding. Older workers that survive a
// daemon upgrade can't render a screen on demand (no snapshot RPC), so the
// daemon must fall back to deriving the visible frame from buffered output.
// These cases lock in that resolution order: fresh screen wins, otherwise we
// derive from replay segments, otherwise from flat scrollback.

func TestSnapshotSeedScreenPrefersFreshWorkerScreen(t *testing.T) {
	info := ptybackend.AttachInfo{
		ScreenSnapshot:      []byte("rendered-by-worker"),
		ScreenCols:          80,
		ScreenRows:          24,
		ScreenSnapshotFresh: true,
		// Scrollback is present too, but must be ignored in favor of the fresh frame.
		Scrollback: []byte("stale scrollback"),
		Cols:       80,
		Rows:       24,
	}

	screen, derived, ok := snapshotSeedScreen(info)
	if !ok {
		t.Fatal("expected a screen when a fresh worker snapshot is present")
	}
	if derived {
		t.Fatal("a fresh worker snapshot must not be reported as derived")
	}
	if string(screen.Payload) != "rendered-by-worker" {
		t.Fatalf("expected the worker payload, got %q", screen.Payload)
	}
}

func TestSnapshotSeedScreenDerivesFromScrollback(t *testing.T) {
	// No fresh worker screen: the old-worker case. We must still produce a
	// paintable frame from the scrollback the worker returns on attach.
	info := ptybackend.AttachInfo{
		Scrollback: []byte("hello from a recovered session"),
		Cols:       80,
		Rows:       24,
	}

	screen, derived, ok := snapshotSeedScreen(info)
	if !ok {
		t.Fatal("expected a derived screen from non-empty scrollback")
	}
	if !derived {
		t.Fatal("expected the screen to be reported as derived")
	}
	if len(screen.Payload) == 0 {
		t.Fatal("derived screen payload must not be empty")
	}
	if screen.Cols != 80 || screen.Rows != 24 {
		t.Fatalf("derived screen geometry = %dx%d, want 80x24", screen.Cols, screen.Rows)
	}
}

func TestSnapshotSeedScreenPrefersReplaySegmentsOverScrollback(t *testing.T) {
	info := ptybackend.AttachInfo{
		ReplaySegments: []ptybackend.ReplaySegment{
			{Cols: 100, Rows: 40, Data: []byte("from segments")},
		},
		Scrollback: []byte("from flat scrollback"),
		Cols:       80,
		Rows:       24,
	}

	screen, derived, ok := snapshotSeedScreen(info)
	if !ok || !derived {
		t.Fatalf("expected a derived screen, got ok=%v derived=%v", ok, derived)
	}
	// Segment geometry (100x40) wins over the flat scrollback geometry (80x24).
	if screen.Cols != 100 || screen.Rows != 40 {
		t.Fatalf("derived screen geometry = %dx%d, want 100x40 from replay segments", screen.Cols, screen.Rows)
	}
}

func TestSnapshotSeedScreenReturnsNothingWithoutBuffer(t *testing.T) {
	_, _, ok := snapshotSeedScreen(ptybackend.AttachInfo{Cols: 80, Rows: 24})
	if ok {
		t.Fatal("expected no screen when neither a fresh frame nor any buffer is available")
	}
}
