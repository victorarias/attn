//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// A resize moves the grid without producing output, so nothing on the wire
// tells a client its images moved. These pin the emission that does.
//
// Session.resize fans out inline, before it returns, so every assertion here
// reads the channel non-blockingly once Resize has returned. That is a real
// signal rather than a wait: if the emission were going to happen, it already
// has.

import (
	"sync/atomic"
	"testing"
	"time"
)

// heldKittySpawn is newKittySpawn's long-lived sibling: the child blocks again
// after emitting, so the session is still resizable.
func newHeldKittySpawn(t *testing.T, id, payload string) *kittySpawn {
	t.Helper()
	return newKittySpawnCmd(t, id, payload, "read release; cat %s; read hold")
}

// releaseAndPlace lets the child emit and returns the update describing the
// image. Blocking here is what keeps the resize from racing the chunk that
// placed it — the read loop is asynchronous, unlike the resize path.
func releaseAndPlace(t *testing.T, spawn *kittySpawn) PlacementUpdate {
	t.Helper()
	if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	select {
	case update := <-spawn.updates:
		if len(update.Placements) != 1 {
			t.Fatalf("placements = %+v, want the one image the payload emitted", update.Placements)
		}
		return update
	case <-time.After(10 * time.Second):
		t.Fatal("the image was never described")
		return PlacementUpdate{}
	}
}

// The resize moves an image, and the client is told where it went. Nothing else
// can tell it: a resize produces no output, so there is no chunk carrying the
// correction, and on an idle session none is ever coming. Without this the
// image stays drawn at the old grid's position until something types.
//
// Shrinking the screen under the image is what makes this falsifiable. A set
// re-sent from the last observation rather than read fresh after the resize
// still describes the old row, so the assertion is that the row MOVED, not that
// an update merely arrived.
func TestResizeDescribesPlacementsAfterTheResize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const done = "PAYLOAD-END"
	spawn := newHeldKittySpawn(t, "kitty-resize", "\x1b[6;1H"+kittyPlaceRGB(82, 16, 32, "")+done)
	placed := releaseAndPlace(t, spawn)
	before := placed.Placements[0]
	// The payload can span chunks, so the placement's own seq is not necessarily
	// the last one. Wait for the end of the output and take the watermark from
	// the session: after this the child is blocked on a read and nothing else
	// can move it.
	watermark := spawn.waitForOutput(t, done)

	// 12 rows down to 4, with the image at row 6: the grid has to scroll it up
	// to keep the cursor on screen.
	if err := spawn.manager.Resize(spawn.id, 40, 4, 0, 0); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	var resized PlacementUpdate
	select {
	case resized = <-spawn.updates:
	default:
		t.Fatal("the resize described nothing: the client is left drawing at the old geometry")
	}
	if len(resized.Placements) != 1 {
		t.Fatalf("placements after the resize = %+v, want the image still described", resized.Placements)
	}
	after := resized.Placements[0]

	if after.ViewportRow >= before.ViewportRow {
		t.Errorf("viewport row after the resize = %d, want less than the %d it was placed at: the set was not re-read from the resized grid",
			after.ViewportRow, before.ViewportRow)
	}
	if after.ImageID != before.ImageID {
		t.Errorf("described image id = %d after the resize, want %d", after.ImageID, before.ImageID)
	}
	// The watermark, not a fresh seq: no bytes were produced, so the set belongs
	// to the last chunk the client already has. A fresh seq would claim to
	// describe a chunk that never went out, and the client would be holding a
	// set stamped ahead of every byte it has seen.
	if resized.Seq != watermark {
		t.Errorf("resize update seq = %d, want the replay watermark %d", resized.Seq, watermark)
	}
}

// Resizing is constant — every pane drag, every window change, on every session
// — and a session with no image on screen must reach ghostty for placements
// exactly never, or the feed path's careful gating is undone by the one beside
// it.
//
// Two ways a session ends up holding no image, and both have to cost nothing:
// the shipping configuration where images are live and the program simply never
// emitted one, and the escape hatch where storage is off and an image-emitting
// program was refused. Each is deterministic — with no placement stored there is
// never an update to deliver — so the empty channel below is a fact rather than
// a race.
func TestResizeCostsNothingWithoutPlacements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	for _, tc := range []struct {
		name    string
		limit   string
		payload string
	}{
		// Pinned rather than inherited: the default is what the flip changed, and
		// a test that reads its meaning from an unset variable changes meaning
		// with it. This one was written when the empty value meant images off,
		// and went quietly vacuous the day it started meaning 320MB.
		{name: "images live and the program emits none", limit: "", payload: "\x1b[6;1Hplain"},
		{name: "images off and the program emits one", limit: "0", payload: "\x1b[6;1H" + kittyPlaceRGB(83, 16, 32, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(kittyStorageLimitEnv, tc.limit)

			// Atomic because the feed path fires this hook from the read loop while
			// the resize path fires it from here. Registered before the spawn so its
			// cleanup runs after the manager has been shut down and the read loop is
			// gone.
			var reads atomic.Int32
			placementReadHook = func() { reads.Add(1) }
			t.Cleanup(func() { placementReadHook = nil })

			spawn := newHeldKittySpawn(t, "kitty-resize-"+tc.limit+"x", tc.payload)
			if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
				t.Fatalf("Input() error: %v", err)
			}
			if err := spawn.manager.Resize(spawn.id, 40, 4, 0, 0); err != nil {
				t.Fatalf("Resize() error: %v", err)
			}
			if err := spawn.manager.Resize(spawn.id, 100, 30, 0, 0); err != nil {
				t.Fatalf("Resize() error: %v", err)
			}

			select {
			case update := <-spawn.updates:
				t.Fatalf("a placement was described on a session with no image: %+v", update)
			default:
			}
			if got := reads.Load(); got != 0 {
				t.Errorf("the placement set was read %d times on a session with no images, want never", got)
			}
		})
	}
}
