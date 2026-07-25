//go:build darwin && arm64

package pty

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// restoredScreenLines rebuilds the terminal a client would see from a VT dump
// and returns one trimmed line per SCREEN-space row, so the slice index IS the
// row a resolved block points at.
func restoredScreenLines(t *testing.T, snap ghosttyvt.Snapshot) []string {
	t.Helper()
	restored, err := ghosttyvt.New(snap.Cols, snap.Rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New (restore): %v", err)
	}
	defer restored.Close()
	restored.Write(snap.VTDump)
	lines := strings.Split(restored.PlainText(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

func rowText(lines []string, row int32) string {
	if int(row) < 0 || int(row) >= len(lines) {
		return "<out of range>"
	}
	return lines[row]
}

// TestBlockFeedRoundTrip is the phase's end-to-end native check: the REAL OSC
// 133 segmenter and REAL block table, fed a fish-like session through a live
// ghostty terminal, must resolve blocks whose rows index the RESTORED dump at
// the right text — with command text and exit codes captured at parse time
// (unrecoverable from the grid later) — and must exclude blocks opened while
// the alternate screen was active. The pure-fake blocktable_test proves
// lifecycle; the ghosttyvt tests prove the coordinate premise; this proves the
// two wired together against a real serialize/restore.
func TestBlockFeedRoundTrip(t *testing.T) {
	refBase := ghosttyvt.LiveTrackedRefs()

	term, err := ghosttyvt.New(80, 24, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	feeder := newBlockFeeder(term)
	if feeder == nil {
		t.Fatal("newBlockFeeder returned nil for a live terminal")
	}

	// A couple of plain rows first so the prompts don't land at row 0 — the
	// SCREEN-space offset must survive the round trip, not just row 0.
	feeder.feed([]byte("welcome\r\nto the shell\r\n"))

	// Command 1: `echo hello`, exit 0. The newline that commits the command
	// line is echoed BEFORE the C (pre-exec) marker, exactly as fish emits it,
	// so output starts on the row after the prompt.
	feeder.feed([]byte(
		"\x1b]133;A\x07prompt$ \x1b]133;B\x07echo hello\r\n" +
			"\x1b]133;C;cmdline_url=echo%20hello\x07hello\r\n" +
			"\x1b]133;D;0\x07",
	))
	// Command 2: `make`, exit 2, distinct output. Split across two feed calls to
	// exercise the segmenter's cross-chunk pending buffer on a real stream.
	feeder.feed([]byte("\x1b]133;A\x07prompt$ \x1b]133;B\x07make\r\n\x1b]133;C;cmdline_url=ma"))
	feeder.feed([]byte("ke\x07build failed\r\n\x1b]133;D;2\x07"))

	// A third command opened WHILE the alternate screen is active (a session
	// inside vim/less at snapshot time). It completes normally but must be
	// excluded — blocks are a primary-screen concept.
	feeder.feed([]byte("\x1b[?1049h")) // enter alt screen
	feeder.feed([]byte(
		"\x1b]133;A\x07alt$ \x1b]133;B\x07vimcmd\r\n" +
			"\x1b]133;C;cmdline_url=vimcmd\x07alt output\r\n" +
			"\x1b]133;D;0\x07",
	))
	feeder.feed([]byte("\x1b[?1049l")) // leave alt screen

	blocks := feeder.snapshotBlocks()
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (alt-screen block must be excluded): %+v", len(blocks), blocks)
	}

	lines := restoredScreenLines(t, term.Serialize())

	// Block 1
	b1 := blocks[0]
	if b1.ID != 1 {
		t.Fatalf("block 1 id = %d, want 1", b1.ID)
	}
	if b1.Pending {
		t.Fatal("block 1 must be completed, not pending")
	}
	if b1.Command == nil || *b1.Command != "echo hello" {
		t.Fatalf("block 1 command = %v, want %q (cmdline_url decoded)", b1.Command, "echo hello")
	}
	if b1.ExitCode == nil || *b1.ExitCode != 0 {
		t.Fatalf("block 1 exit = %v, want 0", b1.ExitCode)
	}
	if got := rowText(lines, b1.PromptRow); !strings.HasPrefix(got, "prompt$") {
		t.Fatalf("block 1 promptRow %d indexes %q, want prompt$*", b1.PromptRow, got)
	}
	if b1.OutputStartRow == nil {
		t.Fatal("block 1 missing outputStartRow")
	}
	if got := rowText(lines, *b1.OutputStartRow); got != "hello" {
		t.Fatalf("block 1 outputStartRow %d indexes %q, want %q", *b1.OutputStartRow, got, "hello")
	}

	// Block 2 — server-assigned ids are monotonic across the alt cycle's
	// consumed id, so the second PRIMARY block is not necessarily id 2; assert
	// monotonic and distinct instead.
	b2 := blocks[1]
	if b2.ID <= b1.ID {
		t.Fatalf("block ids not monotonic: %d then %d", b1.ID, b2.ID)
	}
	if b2.Command == nil || *b2.Command != "make" {
		t.Fatalf("block 2 command = %v, want %q", b2.Command, "make")
	}
	if b2.ExitCode == nil || *b2.ExitCode != 2 {
		t.Fatalf("block 2 exit = %v, want 2", b2.ExitCode)
	}
	if b2.OutputStartRow == nil || rowText(lines, *b2.OutputStartRow) != "build failed" {
		got := "<nil>"
		if b2.OutputStartRow != nil {
			got = rowText(lines, *b2.OutputStartRow)
		}
		t.Fatalf("block 2 output row indexes %q, want %q", got, "build failed")
	}
	// endRow is exclusive — the row the next prompt renders on. Block 1's end
	// must be at or above block 2's prompt (they are stacked in order).
	if b1.EndRow == nil || *b1.EndRow > b2.PromptRow {
		t.Fatalf("block 1 endRow %v should not exceed block 2 promptRow %d", b1.EndRow, b2.PromptRow)
	}

	// Teardown: close frees the table's refs, then the terminal. No native ref
	// may survive (cap eviction never fired here, so this is the plain path).
	feeder.close()
	term.Close()
	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked: live=%d baseline=%d", got, refBase)
	}
}
