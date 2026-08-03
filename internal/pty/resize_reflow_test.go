//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// The grid-equality invariant across a RESIZE.
//
// Every client frame resizes with DEC wraparound off: the app's fit and its
// historical replay both go through resizeGhosttyWithoutReflow
// (app/src/utils/ghosttyResize.ts), load-bearing since the block store started
// holding rows. A worker that reflowed instead would re-wrap history the
// clients keep unwrapped, and from then on the same bytes would occupy
// different numbers of rows on the two grids — which is exactly the frame the
// wire's row-indexed mappings assume is shared. A kitty placement is the
// visible casualty: the client maps it as `scrollbackLength + viewport_row`.
//
// The two tests below judge that outcome from opposite ends. The first pins the
// grids themselves against a control driven by the client's own recipe; the
// second pins the one arithmetic the frames feed, on the shape the drift was
// first measured on.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// sessionTerminal reaches the worker's authoritative terminal — the one
// Session.resize resizes and the one every placement and restore is read from.
func sessionTerminal(t *testing.T, spawn *kittySpawn) *ghosttyvt.Terminal {
	t.Helper()
	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	if session.ghostty == nil {
		t.Fatal("the session has no ghostty terminal")
	}
	return session.ghostty
}

// newQuietSpawn holds a session open with a child that writes nothing, so the
// worker terminal starts empty and the only bytes in it are the test's.
func newQuietSpawn(t *testing.T, id string, cols, rows uint16) *kittySpawn {
	t.Helper()
	spawn := newKittySpawnCmd(t, id, "", "read hold # %s")
	if err := spawn.manager.Resize(id, cols, rows); err != nil {
		t.Fatalf("Resize() to the starting geometry: %v", err)
	}
	return spawn
}

// historyRows is how many buffer rows sit above the viewport — the worker's
// side of what the client reads as getScrollbackLength(). Derived the way the
// feeder derives a block's row: pin the cursor's cell, ask for its position
// counted from the top of retained history, and subtract the viewport-relative
// row of the same cell.
func historyRows(t *testing.T, term *ghosttyvt.Terminal) int {
	t.Helper()
	ref := term.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor() returned nil: the history depth cannot be derived")
	}
	defer ref.Free()
	_, fromTop, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ScreenPoint() failed: the history depth cannot be derived")
	}
	_, viewportRow := term.CursorPos()
	return fromTop - viewportRow
}

// framesAgree is the whole claim: the same bytes, the same rows. Text alone is
// not enough — the viewport is what the user looks at, and the cursor is what
// the next byte lands on.
func framesAgree(t *testing.T, worker, control *ghosttyvt.Terminal, when string) {
	t.Helper()
	if got, want := worker.PlainText(), control.PlainText(); got != want {
		t.Errorf("%s: the worker history diverged from a client frame\nworker:\n%s\nclient:\n%s", when, got, want)
	}
	if got, want := worker.ViewportText(), control.ViewportText(); got != want {
		t.Errorf("%s: the worker viewport diverged from a client frame\nworker:\n%s\nclient:\n%s", when, got, want)
	}
	wx, wy := worker.CursorPos()
	cx, cy := control.CursorPos()
	if wx != cx || wy != cy {
		t.Errorf("%s: cursor at (%d,%d) on the worker, (%d,%d) on a client frame", when, wx, wy, cx, cy)
	}
}

// A prompt long enough to wrap at every width used below, so history holds
// soft-wrapped rows whose count depends on which resize path ran.
const wrappingPrompt = "~/projects/victor/attn/worktrees/a4-reflow $ echo hello wrapped world"

// The frame-parity gate. The worker resizes through the real Session.resize;
// the control is a second real terminal fed the same bytes and resized with the
// client's explicit recipe. Reverting session.go to the reflowing Resize reddens
// the two width-changing rows.
func TestSessionResizeKeepsTheWorkerFrameEqualToAClientFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	cases := []struct {
		name           string
		cols, rows     uint16
		toCols, toRows uint16
		chunks         []string
		// wraparoundOff says the stream turned DECAWM off, so the client's
		// recipe is a plain resize — ghostty does not reflow with the mode
		// already off, and writing it back on would enable what the program
		// disabled.
		wraparoundOff bool
	}{
		{
			name: "widening with wrapped history",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks: []string{wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(90, 16, 32, ""), "tail"},
		},
		{
			name: "narrowing with wrapped history",
			cols: 40, rows: 8, toCols: 20, toRows: 8,
			chunks: []string{wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(91, 16, 32, ""), "tail"},
		},
		{
			name: "widening while the alternate screen is active",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks: []string{
				"primary " + wrappingPrompt + "\r\n",
				"\x1b[?1049h\x1b[2;1H" + wrappingPrompt,
				kittyPlaceRGB(92, 16, 32, ""),
			},
		},
		{
			name: "widening with wraparound disabled by the program",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks:        []string{"\x1b[?7l", wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(93, 16, 32, ""), "tail"},
			wraparoundOff: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(kittyStorageLimitEnv, fmt.Sprint(mirrorStorageLimit))

			spawn := newQuietSpawn(t, "resize-frame", tc.cols, tc.rows)
			worker := sessionTerminal(t, spawn)
			control := newKittyTerminal(t, int(tc.cols), int(tc.rows), ghosttyvt.Options{
				KittyImageStorageLimit: mirrorStorageLimit,
			})
			framesAgree(t, worker, control, "before any output")

			for _, chunk := range tc.chunks {
				worker.Write([]byte(chunk))
				control.Write([]byte(chunk))
			}
			framesAgree(t, worker, control, "after the output, before the resize")

			if err := spawn.manager.Resize(spawn.id, tc.toCols, tc.toRows); err != nil {
				t.Fatalf("Resize() error: %v", err)
			}
			if tc.wraparoundOff {
				control.Resize(int(tc.toCols), int(tc.toRows))
			} else {
				control.Write([]byte("\x1b[?7l"))
				control.Resize(int(tc.toCols), int(tc.toRows))
				control.Write([]byte("\x1b[?7h"))
			}
			framesAgree(t, worker, control, fmt.Sprintf("after resizing to %dx%d", tc.toCols, tc.toRows))

			// Output after the resize is what a leaked mode shows up in: the
			// worker's toggle has to leave DECAWM exactly as the program left
			// it, or this line wraps on one grid and overwrites on the other.
			after := strings.Repeat("z", int(tc.toCols)+7) + "\r\nend"
			worker.Write([]byte(after))
			control.Write([]byte(after))
			framesAgree(t, worker, control, "after output that reaches the wrap column")
		})
	}
}

// The mapping the frames feed, on the shape the drift was measured on: a
// wrapped prompt above a placement, a width-changing resize, then scrolling.
// The client draws the image at `scrollbackLength + viewport_row`, so that sum —
// the placement's absolute buffer row — is what must not move. A reflowing
// worker re-wraps the prompt into an extra row and pushes the image down by one;
// the following scrolls then correct it, which is why the defect read as a
// one-row "drift" that healed itself.
func TestResizeKeepsAPlacementsBufferRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, fmt.Sprint(mirrorStorageLimit))

	const placedMarker = "PLACED"
	const scrolledMarker = "SCROLLED"
	// The child emits the wrapped prompt and the image, waits, then scrolls a
	// screenful and a half past it. `q=2` because ghostty answers a transmission
	// on the program's own stdin: an unsuppressed OK lands in the child's line
	// buffer and eats the read this handshake is built on.
	spawn := newKittySpawnCmd(t, "resize-mapping",
		wrappingPrompt+"\r\n"+kittyPlaceRGB(94, 16, 32, ",q=2")+placedMarker,
		"read release; cat %s; read scroll; seq 1 20; echo "+scrolledMarker+"; read hold")

	if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	spawn.waitForOutput(t, placedMarker)
	worker := sessionTerminal(t, spawn)

	// bufferRow is the client's arithmetic, computed from the worker's own grid.
	bufferRow := func(when string) int {
		t.Helper()
		placements := worker.KittyPlacements()
		if len(placements) != 1 {
			t.Fatalf("%s: placements = %+v, want the one image", when, placements)
		}
		return historyRows(t, worker) + int(placements[0].ViewportRow)
	}

	placed := bufferRow("once the image is on the grid")

	// 40 -> 24 columns: the 69-char prompt is two rows wide at 40 and three at
	// 24, so a reflow inserts a row above the image and moves it down one.
	if err := spawn.manager.Resize(spawn.id, 24, 12); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	if got := bufferRow("after the width change"); got != placed {
		t.Errorf("the image's buffer row moved from %d to %d across a resize: the client draws it %d rows off until something scrolls",
			placed, got, got-placed)
	}

	if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	spawn.waitForOutput(t, scrolledMarker)
	if got := bufferRow("after the image scrolled into history"); got != placed {
		t.Errorf("the image's buffer row moved from %d to %d as the grid scrolled: scrolling must only move it between history and screen",
			placed, got)
	}
}
