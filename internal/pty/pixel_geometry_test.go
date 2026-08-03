//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// Pixel geometry, from the client's report to the two places a program can read
// it back: the kernel's winsize, and the worker terminal's own size reports.
//
// This is the seam image emitters sit on. chafa, timg and kitten icat all size
// an image by asking how many pixels a cell is worth, and the answer is the
// pane total divided by the grid. With no answer at all they guess — measured
// live in A3, chafa assumed roughly 8 x 11.4 px cells against a real 9 x 22.6 px
// cell and emitted an image at half the intended row height. The session is
// what turns one client report into both answers, so both are checked here.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	creackpty "github.com/creack/pty"
)

// The geometry the tests report: a 2x-display cell (9 x 22.6 CSS px rounded and
// doubled) across a grid that divides evenly, so the derived cell is exact and
// a wrong derivation cannot land on the right number by rounding.
const (
	geomCols, geomRows   = 40, 12
	geomCellW, geomCellH = 18, 45
	geomXPixel           = geomCols * geomCellW
	geomYPixel           = geomRows * geomCellH
)

// winsizeHelperEnv, when set, turns a re-executed test binary into the child of
// the session below: it waits to be released, then reports the winsize the
// kernel gave its controlling terminal. Reading it from the child is the point
// — this is the exact ioctl an image emitter makes, from the exact position it
// makes it, rather than the parent reading back what it just wrote.
const winsizeHelperEnv = "ATTN_PTY_WINSIZE_HELPER"

const winsizeHelperMarker = "attn-winsize"

// TestPTYWinsizeHelper is the child process, not a test of anything. It exits
// immediately unless the session spawned it.
func TestPTYWinsizeHelper(t *testing.T) {
	if os.Getenv(winsizeHelperEnv) != "1" {
		t.Skip("helper process for TestResizeReportsPixelGeometryToTheChild")
	}
	// Held until the parent has resized: a child that reported at spawn time
	// would race the resize and read the spawn geometry instead.
	var release string
	if _, err := fmt.Fscanln(os.Stdin, &release); err != nil {
		t.Fatalf("helper never got its release line: %v", err)
	}
	size, err := creackpty.GetsizeFull(os.Stdin)
	if err != nil {
		t.Fatalf("helper TIOCGWINSZ: %v", err)
	}
	fmt.Printf("%s cols=%d rows=%d xpixel=%d ypixel=%d\n",
		winsizeHelperMarker, size.Cols, size.Rows, size.X, size.Y)
}

// newWinsizeHelperSpawn starts a session whose child is this test binary,
// re-executed into TestPTYWinsizeHelper above.
func newWinsizeHelperSpawn(t *testing.T, id string) *kittySpawn {
	t.Helper()
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	spawn := &kittySpawn{
		manager: m,
		id:      id,
		updates: make(chan PlacementUpdate, 16),
		exited:  make(chan struct{}),
		arrived: make(chan struct{}, 1),
	}
	var once sync.Once
	m.SetExitHandler(func(ExitInfo) { once.Do(func() { close(spawn.exited) }) })

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-winsize",
		ExternalCommand: []string{os.Args[0], "-test.run=^TestPTYWinsizeHelper$"},
		ExternalEnv:     []string{winsizeHelperEnv + "=1"},
		// Deliberately not the geometry under test: the resize has to be what
		// puts the pixels there.
		Cols: 20,
		Rows: 6,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	if _, err := m.Attach(id, "test-client", func(data []byte, _ uint32) bool {
		spawn.mu.Lock()
		spawn.output = append(spawn.output, data...)
		spawn.mu.Unlock()
		select {
		case spawn.arrived <- struct{}{}:
		default:
		}
		return true
	}, nil); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	return spawn
}

func TestResizeReportsPixelGeometryToTheChild(t *testing.T) {
	spawn := newWinsizeHelperSpawn(t, "winsize")

	if err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	if err := spawn.manager.Input(spawn.id, []byte("go\n")); err != nil {
		t.Fatalf("releasing the helper: %v", err)
	}
	spawn.waitForOutput(t, winsizeHelperMarker)

	want := fmt.Sprintf("%s cols=%d rows=%d xpixel=%d ypixel=%d",
		winsizeHelperMarker, geomCols, geomRows, geomXPixel, geomYPixel)
	spawn.mu.Lock()
	got := string(spawn.output)
	spawn.mu.Unlock()
	if !strings.Contains(got, want) {
		t.Fatalf("the child read %q from TIOCGWINSZ, want a line containing %q", firstLines(got), want)
	}
}

// TestResizeDerivesTheWorkerCellFromThePaneTotal is the other half: the same
// report has to reach the worker terminal, which is what answers a program that
// asks the terminal rather than the kernel. In-band size reports (DEC mode
// 2048) are that answer — ghostty's VT core does not implement XTWINOPS at all,
// so there is no CSI 14 t to check.
func TestResizeDerivesTheWorkerCellFromThePaneTotal(t *testing.T) {
	spawn := newQuietSpawn(t, "worker-cell", 20, 6)
	term := sessionTerminal(t, spawn)
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()

	if err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	// The report is the grid times the DERIVED cell, so it only lands on these
	// numbers if xpixel/cols and ypixel/rows both reached the terminal. The 8x16
	// placeholder this replaced would report 192;320 at this grid.
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", geomRows, geomCols, geomYPixel, geomXPixel)
	if got := string(term.DrainResponses()); !strings.Contains(got, want) {
		t.Fatalf("the worker terminal reported %q after a resize carrying %dx%d pixels, want %q",
			got, geomXPixel, geomYPixel, want)
	}
}

// TestPixelLessResizeKeepsTheCellItAlreadyHas pins the rule the whole optional
// field rests on. Only a fit measures the pane; the attach-time reconcile and
// the remount hydrate resize carry no pixels, and they arrive AFTER a fit on
// every remount. Treating "no pixels" as "no pixels" rather than "keep the
// cell" would blank the geometry out from under a program that already sized an
// image to it.
func TestPixelLessResizeKeepsTheCellItAlreadyHas(t *testing.T) {
	spawn := newQuietSpawn(t, "pixel-less", geomCols, geomRows)
	term := sessionTerminal(t, spawn)

	if err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() carrying pixels: %v", err)
	}
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()

	// A narrower grid, reported by a client that measured nothing.
	const narrowCols = 30
	if err := spawn.manager.Resize(spawn.id, narrowCols, geomRows, 0, 0); err != nil {
		t.Fatalf("Resize() without pixels: %v", err)
	}

	// The totals move with the grid because the CELL is what was remembered.
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", geomRows, narrowCols, geomYPixel, narrowCols*geomCellW)
	if got := string(term.DrainResponses()); !strings.Contains(got, want) {
		t.Fatalf("the worker terminal reported %q after a pixel-less resize, want %q", got, want)
	}

	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	size, err := creackpty.GetsizeFull(session.ptmx)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	if size.X != narrowCols*geomCellW || size.Y != geomYPixel {
		t.Fatalf("the kernel winsize is %dx%d pixels after a pixel-less resize, want %dx%d",
			size.X, size.Y, narrowCols*geomCellW, geomYPixel)
	}
}

// TestSpawnIsPixelLess pins the deliberate gap: a session starts with no pixel
// geometry and reports none, because nothing has measured a pane yet. The first
// fit is what fixes it, and it always follows.
func TestSpawnIsPixelLess(t *testing.T) {
	spawn := newKittySpawnCmd(t, "spawn-pixels", "", "read hold # %s")
	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	size, err := creackpty.GetsizeFull(session.ptmx)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	if size.X != 0 || size.Y != 0 {
		t.Fatalf("a fresh session reports %dx%d winsize pixels, want none until a client measures the pane", size.X, size.Y)
	}
}

func firstLines(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
