//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"fmt"
	"strings"
	"testing"
)

// screenLines returns the terminal's full-screen text (scrollback + active
// area) as one line per grid row, trailing whitespace trimmed. With
// unwrap=false in the formatter options, PlainText emits one line per grid
// row, so the slice index IS the SCREEN-space y coordinate.
func screenLines(t *Terminal) []string {
	lines := strings.Split(t.PlainText(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

func feedLines(t *Terminal, from, to int) {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "line-%04d\r\n", i)
	}
	t.Write([]byte(b.String()))
}

// TestTrackedRefLeakAccounting verifies the live-ref counter that block-table
// tests use to prove every retirement path frees its native refs: increments
// on successful pins, decrements exactly once per ref no matter how many times
// Free is called.
func TestTrackedRefLeakAccounting(t *testing.T) {
	base := LiveTrackedRefs()
	term, err := New(80, 10, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	r1 := term.TrackCursor()
	r2 := term.TrackCursor()
	if r1 == nil || r2 == nil {
		t.Fatal("TrackCursor returned nil")
	}
	if got := LiveTrackedRefs(); got != base+2 {
		t.Fatalf("after 2 pins: live=%d want %d", got, base+2)
	}
	r1.Free()
	r1.Free() // idempotent: must not double-decrement
	if got := LiveTrackedRefs(); got != base+1 {
		t.Fatalf("after freeing one ref (twice): live=%d want %d", got, base+1)
	}
	r2.Free()
	if got := LiveTrackedRefs(); got != base {
		t.Fatalf("after freeing all: live=%d want %d", got, base)
	}
}

// TestSpikeTrackedRefFollowsScrollPruneReflow verifies the core primitive the
// worker-side block tracker would rely on: a ref pinned at the cursor keeps
// resolving to the same content row while the terminal scrolls, prunes
// scrollback past the cap, and reflows on resize — and reports "no value"
// (rather than a wrong position) once the row is pruned away.
func TestSpikeTrackedRefFollowsScrollPruneReflow(t *testing.T) {
	term, err := New(80, 10, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	feedLines(term, 0, 5)
	term.Write([]byte("MARK-PROMPT $ echo hello\r\n"))
	// Cursor now sits at the start of the line AFTER the mark; pin one row up
	// by pinning before writing the mark instead.
	// Re-do precisely: pin at cursor, then write the mark text on that row.
	ref := term.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor returned nil")
	}
	defer ref.Free()
	term.Write([]byte("MARK-OUTPUT-START\r\n"))

	x, y0, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost immediately")
	}
	if x != 0 {
		t.Fatalf("expected x=0 at pin time, got %d", x)
	}
	if got := screenLines(term)[y0]; got != "MARK-OUTPUT-START" {
		t.Fatalf("pin row mismatch: y=%d text=%q", y0, got)
	}

	// Scroll within the cap: the pinned row's SCREEN y must keep resolving to
	// the same content (pruning may shift y down; content is the contract).
	feedLines(term, 100, 130)
	_, y1, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost after in-cap scroll")
	}
	if got := screenLines(term)[y1]; got != "MARK-OUTPUT-START" {
		t.Fatalf("after scroll: y=%d text=%q", y1, got)
	}

	// Reflow: shrink width so long lines wrap; the ref must still resolve to
	// the marked content.
	term.Resize(40, 10)
	_, y2, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost after reflow")
	}
	if got := screenLines(term)[y2]; !strings.HasPrefix(got, "MARK-OUTPUT-START") {
		t.Fatalf("after reflow: y=%d text=%q", y2, got)
	}

	// Prune past the cap: pruning is page-granular and lazy (probe: with
	// cap=50 it fires between ~1k and ~5k rows), so feed enough to guarantee
	// the marked page is discarded. The ref must cleanly report no value,
	// never a wrong row.
	feedLines(term, 200, 8000)
	if _, y3, ok := ref.ScreenPoint(); ok {
		got := screenLines(term)[y3]
		t.Fatalf("expected ref discarded after prune, still resolves: y=%d text=%q", y3, got)
	}
}

// TestSpikeScreenCoordsAlignAcrossRestore verifies the serialization contract's
// coordinate premise: a SCREEN-space row resolved at serialize time is a valid
// index into the terminal a client rebuilds by writing the VT dump — including
// when scrollback was pruned at the cap before serializing (SCREEN space is
// post-prune by construction, so no offset mapping is needed).
func TestSpikeScreenCoordsAlignAcrossRestore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines int // enough to overflow MaxScrollback=50 in the "pruned" case
	}{
		{name: "within_cap", lines: 20},
		{name: "pruned_at_cap", lines: 8000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := New(80, 10, Options{MaxScrollback: 50})
			if err != nil {
				t.Fatalf("New src: %v", err)
			}
			defer src.Close()

			feedLines(src, 0, 3)
			ref := src.TrackCursor()
			if ref == nil {
				t.Fatal("TrackCursor returned nil")
			}
			defer ref.Free()
			src.Write([]byte("BLOCK-START marker row\r\n"))
			feedLines(src, 1000, 1000+tc.lines)

			_, y, ok := ref.ScreenPoint()
			if tc.name == "pruned_at_cap" {
				// The marked row scrolled past the cap; a real serializer
				// drops this block. Assert the clean signal and move on.
				if ok {
					t.Fatalf("expected pruned ref to report no value, got y=%d", y)
				}
				// Instead align on a row that IS retained: pin the last line.
				ref2 := src.TrackCursor()
				defer ref2.Free()
				src.Write([]byte("BLOCK-START late marker\r\n"))
				var ok2 bool
				_, y, ok2 = ref2.ScreenPoint()
				if !ok2 {
					t.Fatal("late ref lost")
				}
			} else if !ok {
				t.Fatal("ref lost within cap")
			}

			srcLines := screenLines(src)
			if !strings.HasPrefix(srcLines[y], "BLOCK-START") {
				t.Fatalf("src row y=%d is %q, want BLOCK-START*", y, srcLines[y])
			}

			snap := src.Serialize()
			restored, err := New(snap.Cols, snap.Rows, Options{MaxScrollback: 50})
			if err != nil {
				t.Fatalf("New restored: %v", err)
			}
			defer restored.Close()
			restored.Write(snap.VTDump)

			gotLines := screenLines(restored)
			if y >= len(gotLines) {
				t.Fatalf("restored terminal has %d rows, serialize-time y=%d out of range", len(gotLines), y)
			}
			if gotLines[y] != srcLines[y] {
				t.Fatalf("row misalignment at y=%d: src=%q restored=%q", y, srcLines[y], gotLines[y])
			}
			// The alignment must hold globally, not just at the marker.
			if len(gotLines) != len(srcLines) {
				t.Fatalf("row count mismatch: src=%d restored=%d", len(srcLines), len(gotLines))
			}
		})
	}
}

// TestSpikeTrackedRefResolvesWhileAltScreenActive verifies a primary-screen ref
// still resolves (against the primary page list) while the alternate screen is
// active — the serializer runs in exactly that state when a session is inside
// vim/less at snapshot time — and that the resolved row aligns with the
// restored terminal's primary screen after leaving alt.
func TestSpikeTrackedRefResolvesWhileAltScreenActive(t *testing.T) {
	src, err := New(80, 10, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New src: %v", err)
	}
	defer src.Close()

	feedLines(src, 0, 12) // some scrollback on primary
	ref := src.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor returned nil")
	}
	defer ref.Free()
	src.Write([]byte("BLOCK-START before vim\r\n"))

	src.Write([]byte("\x1b[?1049h\x1b[2J\x1b[HALT-SCREEN-CONTENT"))

	_, y, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("primary ref lost while alt active")
	}

	snap := src.Serialize()
	restored, err := New(snap.Cols, snap.Rows, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New restored: %v", err)
	}
	defer restored.Close()
	restored.Write(snap.VTDump)

	// Leave alt on both; primary content must align at the ref's row.
	src.Write([]byte("\x1b[?1049l"))
	restored.Write([]byte("\x1b[?1049l"))

	srcLines, gotLines := screenLines(src), screenLines(restored)
	if !strings.HasPrefix(srcLines[y], "BLOCK-START") {
		t.Fatalf("src primary row y=%d is %q, want BLOCK-START*", y, srcLines[y])
	}
	if y >= len(gotLines) || gotLines[y] != srcLines[y] {
		got := "<out of range>"
		if y < len(gotLines) {
			got = gotLines[y]
		}
		t.Fatalf("alt-screen restore misalignment at y=%d: src=%q restored=%q", y, srcLines[y], got)
	}
}
