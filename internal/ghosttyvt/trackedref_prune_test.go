//go:build darwin && arm64

package ghosttyvt

import (
	"strings"
	"testing"
)

// A pinned ref must (a) resolve to a stable SCREEN-space row while its cell is
// retained and (b) report ok=false once the cell is pruned past the scrollback
// cap — never a stale coordinate. The block table depends on both: it anchors
// blocks by ScreenPoint while live and treats a dropped ref as "content gone,
// drop the block" rather than trusting a phantom row. Drives real prune with a
// small MaxScrollback so the early mark is guaranteed to fall out.
func TestTrackedRefDropsWhenPruned(t *testing.T) {
	term, err := New(80, 10, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	ref := term.TrackCursor()
	defer ref.Free()
	term.Write([]byte("MARK-EARLY\r\n"))

	// While the mark is still retained, its SCREEN-space y must be valid and
	// never exceed the retained row count (no phantom coordinate).
	feedLines(term, 0, 20)
	if _, y, ok := ref.ScreenPoint(); ok {
		rows := len(strings.Split(term.PlainText(), "\n"))
		if y < 0 || y >= rows {
			t.Fatalf("retained ref y=%d out of range [0,%d)", y, rows)
		}
	}

	// Push far past MaxScrollback; the early mark must eventually be discarded
	// and ScreenPoint must then report ok=false.
	for _, milestone := range []int{1000, 5000, 20000, 60000} {
		feedLines(term, 0, milestone)
		if _, _, ok := ref.ScreenPoint(); !ok {
			return // dropped as required
		}
	}
	t.Fatal("pinned ref never dropped after 60000 lines past a 50-row scrollback cap")
}
