//go:build darwin && arm64

package pty

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// markerScanSegmenter is a deliberately minimal stand-in for the real OSC 133
// segmenter: it only recognizes the complete prompt-start sequence the test
// writes (findSafeBoundary keeps escape sequences unsplit across read-loop
// chunks, so the test never sees a partial marker). The real segmenter's
// partial-marker handling is corpus-tested separately; THIS test exists to
// prove the skeleton's locking contract, not parsing.
type markerScanSegmenter struct{}

var testPromptMark = []byte("\x1b]133;A\x07")

func (markerScanSegmenter) Feed(chunk []byte, emit func([]byte, *osc133Marker)) {
	for {
		i := bytes.Index(chunk, testPromptMark)
		if i < 0 {
			emit(chunk, nil)
			return
		}
		emit(chunk[:i], &osc133Marker{Kind: osc133PromptStart})
		chunk = chunk[i+len(testPromptMark):]
	}
}

// pinningBlockTable keeps every successfully pinned primary-screen ref and
// resolves them at snapshot time — the smallest table that exercises the
// {dump, blocks, watermark} atomicity the skeleton promises. All calls arrive
// under replayMu (blockFeeder's contract), so no internal locking.
type pinningBlockTable struct {
	refs []blockRef
}

func (t *pinningBlockTable) ApplyMarker(_ osc133Marker, ref blockRef, altScreen bool) {
	if ref == nil {
		return
	}
	if altScreen {
		ref.Free()
		return
	}
	t.refs = append(t.refs, ref)
}

func (t *pinningBlockTable) SnapshotBlocks() []AttachBlockData {
	var out []AttachBlockData
	for i, r := range t.refs {
		if _, y, ok := r.ScreenPoint(); ok {
			out = append(out, AttachBlockData{ID: uint64(i + 1), PromptRow: int32(y)})
		}
	}
	return out
}

func (t *pinningBlockTable) Close() {
	for _, r := range t.refs {
		r.Free()
	}
	t.refs = nil
}

// TestBlockSnapshotAtomicity proves the Phase 3a rails invariant: block rows,
// the VT dump, and the seq watermark are captured under one replayMu hold, so
// a snapshot taken WHILE the read loop is scrolling the terminal still has
// every block row pointing at the right content in its own dump. If block
// resolution ever moves outside the hold (or the feed path pins outside it),
// rows resolved against a moved terminal index the wrong dump line and this
// fails.
func TestBlockSnapshotAtomicity(t *testing.T) {
	const cols, rows = 80, 24
	const marks = 150

	refBase := ghosttyvt.LiveTrackedRefs()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	table := &pinningBlockTable{}
	s := &Session{
		id:          "block-race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	s.ghostty = gt
	s.blockFeed = &blockFeeder{term: gt, seg: markerScanSegmenter{}, table: table}
	go s.readLoop(nil, func(string, ...any) {})

	// Writer: each iteration opens a "block" (marker pins the cursor row, then
	// the MARK line renders on it) followed by filler that keeps the terminal
	// scrolling under the snapshotter. Total rows stay far below the
	// scrollback cap so every ref must remain resolvable. Paced so the read
	// loop applies markers incrementally instead of one coalesced pipe read —
	// the snapshotter must observe genuinely mid-stream states.
	go func() {
		for i := 0; i < marks; i++ {
			line := fmt.Sprintf("\x1b]133;A\x07MARK-%04d\r\nfiller-%04d-a\r\nfiller-%04d-b\r\n", i, i, i)
			if _, werr := w.Write([]byte(line)); werr != nil {
				t.Errorf("pipe write: %v", werr)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	restoredLines := func(info AttachInfo) []string {
		restored, rerr := ghosttyvt.New(int(info.Cols), int(info.Rows), ghosttyvt.Options{})
		if rerr != nil {
			t.Fatalf("ghosttyvt.New (restore): %v", rerr)
		}
		defer restored.Close()
		restored.Write(info.GhosttySnapshot)
		lines := strings.Split(restored.PlainText(), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " ")
		}
		return lines
	}
	assertBlocksIndexDump := func(info AttachInfo) {
		t.Helper()
		lines := restoredLines(info)
		for _, b := range info.GhosttyBlocks {
			y := int(b.PromptRow)
			if y < 0 || y >= len(lines) {
				t.Fatalf("block %d row %d out of range (restored dump has %d rows)", b.ID, y, len(lines))
			}
			if !strings.HasPrefix(lines[y], "MARK-") {
				t.Fatalf("block %d row %d points at %q in its own dump — snapshot triple not atomic", b.ID, y, lines[y])
			}
		}
	}

	// Hammer snapshots while the writer scrolls the terminal: every snapshot
	// must be self-consistent regardless of where it lands in the stream.
	// Partial tables (0 < blocks < marks) prove the checks genuinely raced
	// the read loop rather than only observing the settled end state.
	partialChecks := 0
	deadline := time.Now().Add(10 * time.Second)
	for {
		info := s.info()
		if n := len(info.GhosttyBlocks); n > 0 {
			assertBlocksIndexDump(info)
			if n < marks {
				partialChecks++
			} else {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for all marks to reach the block table")
		}
	}
	if partialChecks == 0 {
		t.Fatal("every snapshot saw the settled table; the race was never exercised")
	}

	// Settled state: every mark pinned, in order, each resolving to its own
	// MARK line in the final dump.
	settled := s.info()
	if len(settled.GhosttyBlocks) != marks {
		t.Fatalf("settled snapshot has %d blocks, want %d", len(settled.GhosttyBlocks), marks)
	}
	lines := restoredLines(settled)
	for i, b := range settled.GhosttyBlocks {
		want := fmt.Sprintf("MARK-%04d", i)
		if got := lines[int(b.PromptRow)]; got != want {
			t.Fatalf("settled block %d resolves to row %d = %q, want %q", b.ID, b.PromptRow, got, want)
		}
	}

	// Teardown: the pipe closes, the read loop exits, closePTY's ordering
	// (table refs freed, then the terminal) leaves no live refs — the leak
	// contract every real block-table test must also assert.
	_ = w.Close()
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not exit after pipe close")
	}
	s.closePTY()
	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked: live=%d baseline=%d", got, refBase)
	}
}
