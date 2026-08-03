//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// What a client is told about images, chunk by chunk.
//
// The rules under test are the whole contract: the full set or nothing, only
// when something moved, the empty set when the last image goes, and — on the
// sessions that will never have an image, which is all of them today — not so
// much as one read of the placement set.

import (
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// placementRecorder drives the feeder exactly as the read loop does — feed a
// chunk, then take the changed set under the same stamp — and records what the
// loop would have sent, plus how many times the feed path reached into ghostty
// for placements at all.
type placementRecorder struct {
	feed     *wireFeeder
	term     *ghosttyvt.Terminal
	updates  []PlacementUpdate
	observed int
	seq      uint32
}

func newPlacementRecorder(t *testing.T, cols, rows int, limit uint64) *placementRecorder {
	t.Helper()
	term := newKittyTerminal(t, cols, rows, ghosttyvt.Options{KittyImageStorageLimit: limit})
	feed := newWireFeeder(term)
	if feed == nil {
		t.Fatal("newWireFeeder returned nil for a live terminal")
	}
	t.Cleanup(feed.close)

	rec := &placementRecorder{feed: feed, term: term}
	placementReadHook = func() { rec.observed++ }
	t.Cleanup(func() { placementReadHook = nil })
	return rec
}

func (r *placementRecorder) write(chunk string) {
	r.seq++
	r.feed.feed([]byte(chunk))
	if set, changed := r.feed.changedPlacements(); changed {
		r.updates = append(r.updates, PlacementUpdate{Seq: r.seq, Placements: set})
	}
}

// last is the most recent update, and fails the test when the chunks produced
// none — every caller here is asserting on something that was described.
func (r *placementRecorder) last(t *testing.T) PlacementUpdate {
	t.Helper()
	if len(r.updates) == 0 {
		t.Fatal("no placement update was produced")
	}
	return r.updates[len(r.updates)-1]
}

// The base case: an image is placed, and the client is told where it is with
// the seq of the chunk that placed it. Without this the worker parses kitty for
// nobody — the APC is stripped from the wire and nothing takes its place.
func TestPlacementUpdateDescribesAPlacedImage(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[3;5Hbefore the image")
	rec.write(kittyPlaceRGB(40, 16, 32, ""))

	if len(rec.updates) != 1 {
		t.Fatalf("placement updates = %+v, want exactly the one the image produced", rec.updates)
	}
	update := rec.updates[0]
	if update.Seq != 2 {
		t.Errorf("update seq = %d, want 2: the set describes the grid the second chunk produced", update.Seq)
	}
	if len(update.Placements) != 1 {
		t.Fatalf("placements = %+v, want the one image", update.Placements)
	}
	if got := update.Placements[0].ImageID; got != 40 {
		t.Errorf("described image id = %d, want 40", got)
	}
	// Pixel size, not GridCols/GridRows. A placement transmitted without an
	// explicit cell footprint carries zeros for those — kitty's "natural size" —
	// until something makes ghostty resolve them, so on the real spawn path the
	// FIRST description of an image reports 0x0 cells. The pixel dimensions are
	// populated from the start on every path, and are what a renderer needs to
	// size the image regardless.
	if got := update.Placements[0].PixelHeight; got != 32 {
		t.Errorf("described height = %d px, want the 32 the image was transmitted at", got)
	}
	if got := update.Placements[0].PixelWidth; got != 16 {
		t.Errorf("described width = %d px, want the 16 the image was transmitted at", got)
	}
}

// Placements move under plain output. Nothing about a scroll touches kitty
// state, so ghostty's stamp does not move and the observation has to be taken
// because the chunk COULD have moved something — otherwise every image on
// screen drifts away from where the client last drew it, and stays there until
// the next transmission.
func TestPlacementUpdateFollowsAScrollOnPlainOutput(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[7;1H")
	rec.write(kittyPlaceRGB(41, 16, 32, ""))
	placed := rec.last(t)
	if len(placed.Placements) != 1 {
		t.Fatalf("placements after the image = %+v, want one", placed.Placements)
	}
	startRow := placed.Placements[0].ViewportRow

	rec.write("\r\nplain output\r\nthat scrolls\r\n")

	moved := rec.last(t)
	if len(rec.updates) != 2 {
		t.Fatalf("updates = %d, want the placement and the scroll that moved it", len(rec.updates))
	}
	if len(moved.Placements) != 1 {
		t.Fatalf("placements after the scroll = %+v, want the image still described", moved.Placements)
	}
	if got := moved.Placements[0].ViewportRow; got >= startRow {
		t.Errorf("viewport row after scrolling = %d, want less than the %d it was placed at", got, startRow)
	}
	if moved.Seq != 3 {
		t.Errorf("update seq = %d, want 3: the scroll's own chunk", moved.Seq)
	}
}

// The other half of that rule. Output that moves nothing must say nothing: a
// session with an image on screen produces chunks continuously, and describing
// the same set on every one of them is a message per chunk for the frontend to
// receive, decode, and diff against what it already has.
func TestPlacementUpdateIsSilentWhenNothingMoved(t *testing.T) {
	rec := newPlacementRecorder(t, 40, 12, mirrorStorageLimit)

	rec.write("\x1b[2;1H")
	rec.write(kittyPlaceRGB(42, 16, 32, ""))
	if len(rec.updates) != 1 {
		t.Fatalf("updates after the image = %d, want 1", len(rec.updates))
	}

	// Text well below the image, on a screen with rows to spare: the grid takes
	// it without scrolling, so nothing about the placement changes.
	rec.write("\x1b[9;1Hstatus line")
	rec.write("\x1b[10;1Hanother line")

	if len(rec.updates) != 1 {
		t.Errorf("updates = %+v, want only the one the image produced: unchanged output describes nothing", rec.updates)
	}
}

// The way out. A deleted image is described by an update carrying no
// placements, which is why the set is always whole: "everything is gone" is an
// ordinary set. Without it the client keeps drawing an image the terminal no
// longer holds, with nothing to ever take it off the screen.
func TestPlacementUpdateSendsTheEmptySetWhenTheLastImageGoes(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[2;2Hkeep")
	rec.write(kittyPlaceRGB(43, 16, 32, ""))
	rec.write("\x1b_Ga=d\x1b\\")

	if len(rec.updates) != 2 {
		t.Fatalf("updates = %+v, want the placement and its removal", rec.updates)
	}
	cleared := rec.updates[1]
	if len(cleared.Placements) != 0 {
		t.Errorf("placements after the delete = %+v, want the empty set", cleared.Placements)
	}
	if cleared.Seq != 3 {
		t.Errorf("update seq = %d, want the delete's own chunk (3)", cleared.Seq)
	}
}

// The shipping configuration, and the reason the observation is gated on a
// slice length rather than a terminal read. With a zero storage limit ghostty
// refuses every transmission, so there is nothing to describe — and the feed
// path must not pay a cgo crossing per chunk to rediscover that, on every
// session, all day.
func TestPlacementsCostNothingWhileKittyIsDisabled(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, 0)

	rec.write("plain output\r\n")
	rec.write(kittyPlaceRGB(44, 16, 32, ""))
	rec.write("more output\r\nand more\r\n")

	if len(rec.updates) != 0 {
		t.Errorf("placement updates = %+v with images disabled, want none", rec.updates)
	}
	if rec.observed != 0 {
		t.Errorf("the placement set was read %d times with images disabled, want never", rec.observed)
	}
}

// Ghostty's kitty storage is per-screen and an observation reads the ACTIVE
// one, so a screen switch turns the set over on its own — no flag, no
// bookkeeping, and no way for the two to disagree. This pins that: entering the
// alternate screen empties the described set, and leaving it brings the primary
// screen's image back.
func TestPlacementSetTurnsOverOnAScreenSwitch(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[2;1H")
	rec.write(kittyPlaceRGB(45, 16, 32, ""))
	if got := rec.last(t).Placements; len(got) != 1 {
		t.Fatalf("placements on the primary screen = %+v, want the image", got)
	}

	rec.write("\x1b[?1049h")
	if got := rec.last(t).Placements; len(got) != 0 {
		t.Fatalf("placements on the alternate screen = %+v, want none: the image is on the other screen", got)
	}

	rec.write("\x1b[?1049l")
	back := rec.last(t)
	if len(back.Placements) != 1 {
		t.Fatalf("placements after returning = %+v, want the primary screen's image back", back.Placements)
	}
	if got := back.Placements[0].ImageID; got != 45 {
		t.Errorf("described image id = %d after returning, want 45", got)
	}
}
