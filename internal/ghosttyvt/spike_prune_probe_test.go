//go:build darwin && arm64

package ghosttyvt

import (
	"strings"
	"testing"
)

// Probe (not an assertion of desired behavior): how does max_scrollback prune —
// at what volume does a pinned early row actually get discarded, and how many
// rows does the terminal retain? Informs the block-tracker design's assumptions
// about when serialized blocks can drop.
func TestSpikePruneProbe(t *testing.T) {
	term, err := New(80, 10, Options{MaxScrollback: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	ref := term.TrackCursor()
	defer ref.Free()
	term.Write([]byte("MARK-EARLY\r\n"))

	for _, milestone := range []int{100, 1000, 5000, 20000, 60000} {
		start := 0
		feedLines(term, start, milestone)
		start = milestone
		rows := len(strings.Split(term.PlainText(), "\n"))
		_, y, ok := ref.ScreenPoint()
		snap := term.Serialize()
		t.Logf("after ~%d lines: retained rows=%d ref ok=%v y=%d truncated=%v dump=%dKB",
			milestone, rows, ok, y, snap.ScrollbackTruncated, len(snap.VTDump)/1024)
		if !ok {
			return
		}
	}
	t.Log("ref never dropped")
}
