//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"strings"
	"testing"
)

// rowsHeld counts the buffer rows a terminal is holding: one line per grid row,
// trailing blank rows trimmed by the formatter. It is the number a reflow moves
// and a no-reflow resize leaves alone.
func rowsHeld(term *Terminal) int {
	lines := strings.Split(term.PlainText(), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return len(lines)
}

// The recipe reaches through three native calls under one mutex, so the first
// thing to prove is that it returns at all: Write and Resize each take the
// terminal's lock, and a ResizeNoReflow built out of the public methods would
// deadlock on its own first write. This test hangs rather than fails if that
// regresses, which is the only signal a deadlock can give.
//
// The rows are the second half of the claim. A 70-column line is two rows at 40
// columns; a reflow would make it three at 24.
func TestResizeNoReflowKeepsTheRowsWithWraparoundEnabled(t *testing.T) {
	term := newT(t, 40, 12)
	term.Write([]byte(strings.Repeat("p", 70) + "\r\ntail"))
	before := rowsHeld(term)

	term.ResizeNoReflow(24, 12)

	if cols, rows := term.Size(); cols != 24 || rows != 12 {
		t.Fatalf("Size() = %dx%d after ResizeNoReflow(24,12), want 24x12", cols, rows)
	}
	if got := rowsHeld(term); got != before {
		t.Errorf("the grid holds %d rows after narrowing, want the %d it held: the content was re-wrapped", got, before)
	}
	// The mode has to come back exactly as the program left it, or every later
	// line stops wrapping.
	term.Write([]byte("\r\n" + strings.Repeat("w", 30)))
	if got, want := rowsHeld(term), before+2; got != want {
		t.Errorf("a 30-column line on a 24-column grid left %d rows, want %d: wraparound was not restored", got, want)
	}
}

// With DECAWM already off ghostty does not reflow, so the recipe must not run:
// writing the mode back on would enable wrapping the program deliberately
// disabled, and every later line would wrap where it used to overwrite.
func TestResizeNoReflowLeavesWraparoundDisabled(t *testing.T) {
	term := newT(t, 40, 12)
	term.Write([]byte("\x1b[?7l" + strings.Repeat("p", 70) + "\r\ntail"))
	before := rowsHeld(term)

	term.ResizeNoReflow(24, 12)

	if got := rowsHeld(term); got != before {
		t.Errorf("the grid holds %d rows after narrowing, want the %d it held", got, before)
	}
	term.Write([]byte("\r\n" + strings.Repeat("w", 30)))
	if got, want := rowsHeld(term), before+1; got != want {
		t.Errorf("a 30-column line on a 24-column grid left %d rows, want %d: the resize turned wraparound back on", got, want)
	}
}
