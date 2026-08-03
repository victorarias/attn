//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

// The cell size a terminal reports its pixel dimensions from.
//
// Every pixel-unit answer the terminal gives — the in-band size report below,
// the kitty cell metrics an image emitter divides by — is the grid multiplied
// by this number, so a placeholder cell is not a harmless approximation: it is
// what made chafa assume an 8 x 11.4 px cell against a real 9 x 22.6 px one and
// emit an image at half the intended row height.
//
// A program that has enabled DEC mode 2048 is the visible witness: it is told
// `ESC[48;rows;cols;height;width t` whenever either factor moves. Note that
// XTWINOPS (CSI 14/16/18 t) is NOT answered by this library at all — measured,
// not assumed — so the in-band report and TIOCGWINSZ are the whole surface.

import (
	"fmt"
	"strings"
	"testing"
)

// enableSizeReports turns on in-band size reports and clears the sink, so what
// a test drains afterwards is only what its own action produced.
func enableSizeReports(t *testing.T, term *Terminal) {
	t.Helper()
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()
}

func TestSetCellPixelSizeReportsTheNewPixelSizeImmediately(t *testing.T) {
	const cols, rows = 40, 12
	term := newT(t, cols, rows)
	term.Write([]byte("a line of text, so the grid is not empty when the cell moves\r\n"))
	enableSizeReports(t, term)

	// A real cell measured on a 2x display: 9 x 22.6 CSS px, rounded and doubled.
	const cellW, cellH = 18, 45
	term.SetCellPixelSize(cellW, cellH)

	// No resize followed, so a terminal that only pushed cell geometry through
	// its next resize reports nothing here.
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows, cols, rows*cellH, cols*cellW)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after SetCellPixelSize(%d,%d) = %q, want %q", cellW, cellH, got, want)
	}

	// And it stays set: the next grid resize measures against the new cell, not
	// the 8x16 placeholder construction started from.
	term.Resize(cols, rows+1)
	want = fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows+1, cols, (rows+1)*cellH, cols*cellW)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after the following resize = %q, want %q", got, want)
	}
}

func TestSetCellPixelSizeIgnoresNonPositiveAndUnchangedGeometry(t *testing.T) {
	const cols, rows = 40, 12
	term := newT(t, cols, rows)
	term.SetCellPixelSize(18, 45)
	enableSizeReports(t, term)

	// Zero and negative are how "the client did not measure the pane" arrives.
	// Taking them would report a pane with no pixels at all.
	for _, size := range [][2]int{{0, 45}, {18, 0}, {-18, -45}, {0, 0}} {
		term.SetCellPixelSize(size[0], size[1])
		if got := string(term.DrainResponses()); got != "" {
			t.Fatalf("SetCellPixelSize(%d,%d) reported %q, want the last good geometry kept silently", size[0], size[1], got)
		}
	}
	// The good value survived every one of them.
	term.Resize(cols, rows+1)
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows+1, cols, (rows+1)*45, cols*18)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after the rejected sets = %q, want %q", got, want)
	}

	// Re-setting the same cell is not news; a fit burst repeats it on every
	// frame and each push costs the child a report it cannot act on.
	term.SetCellPixelSize(18, 45)
	if got := string(term.DrainResponses()); got != "" {
		t.Fatalf("re-setting the same cell size reported %q, want nothing", got)
	}
}

func TestNewTerminalReportsFromThePlaceholderCellUntilOneIsSet(t *testing.T) {
	// The placeholder is deliberate, not a bug to fix here: a terminal that
	// reported a zero pixel size would make an emitter divide by zero. It is
	// only wrong once, and only until the owning session's first fit lands.
	term := newT(t, 10, 4)
	enableSizeReports(t, term)
	term.Resize(10, 5)

	got := string(term.DrainResponses())
	if !strings.HasSuffix(got, fmt.Sprintf(";%d;%dt", 5*defaultCellHeightPx, 10*defaultCellWidthPx)) {
		t.Fatalf("a fresh terminal reported %q, want the %dx%d placeholder cell", got, defaultCellWidthPx, defaultCellHeightPx)
	}
}
