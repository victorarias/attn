//go:build darwin && arm64

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// The block feed and the Ghostty terminal are native memory. info() resolves
// tracked refs against them and resize() reflows them, while closePTY frees
// both. Manager.Remove hands an already-looked-up session to an in-flight
// attach before closing it, so those two can genuinely overlap in production.
//
// These tests drive that overlap directly. They are the regression for the
// unlocked teardown: with closePTY freeing outside replayMu, a concurrent
// info() or resize() reads or reflows a freed handle, which -race reports (or
// which crashes the process outright in the native allocator). Run them under
// `go test -race ./internal/pty` for the full signal.

// newLifetimeRaceSession builds a Session wired to a real Ghostty terminal and
// block feed, backed by a pipe rather than a spawned process.
func newLifetimeRaceSession(t *testing.T, id string, cols, rows int) (*Session, *os.File) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}

	s := &Session{
		id:          id,
		cols:        uint16(cols),
		rows:        uint16(rows),
		ptmx:        r,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() errors, never panics
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	s.ghostty = gt
	s.blockFeed = &blockFeeder{term: gt, seg: markerScanSegmenter{}, table: newBlockTable()}
	return s, w
}

// TestAttachRacesRemove drives info() against closePTY the way Manager.Remove
// does: the attach already holds the session pointer when teardown starts. The
// snapshot must either observe a live terminal or an absent one — never a freed
// handle.
func TestAttachRacesRemove(t *testing.T) {
	const cols, rows = 80, 24
	refBase := ghosttyvt.LiveTrackedRefs()

	for i := 0; i < 40; i++ {
		func() {
			s, w := newLifetimeRaceSession(t, fmt.Sprintf("attach-remove-%d", i), cols, rows)
			go s.readLoop(nil, func(string, ...any) {})

			// Pin some blocks so teardown has refs to free and info() has refs
			// to resolve — an empty table would race nothing.
			for j := 0; j < 8; j++ {
				if _, err := fmt.Fprintf(w, "\x1b]133;A\x07MARK-%02d\r\n", j); err != nil {
					t.Errorf("pipe write: %v", err)
					return
				}
			}

			// The attach holds the session pointer before teardown begins,
			// exactly as Manager.Remove leaves it.
			var wg sync.WaitGroup
			start := make(chan struct{})

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 20; k++ {
					info := s.info()
					// Blocks must index their own dump or be absent; a block
					// resolved from freed memory would be neither.
					for _, b := range info.GhosttyBlocks {
						if b.PromptRow < 0 {
							t.Errorf("negative prompt row %d from a torn-down session", b.PromptRow)
							return
						}
					}
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.closePTY()
			}()

			close(start)
			wg.Wait()

			// A snapshot taken strictly after teardown is well-defined: no
			// terminal, so no dump and no blocks.
			after := s.info()
			if len(after.GhosttySnapshot) != 0 {
				t.Fatalf("post-teardown snapshot returned %d bytes, want none", len(after.GhosttySnapshot))
			}
			if len(after.GhosttyBlocks) != 0 {
				t.Fatalf("post-teardown snapshot returned %d blocks, want none", len(after.GhosttyBlocks))
			}
		}()
	}

	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked across attach/remove races: live=%d baseline=%d", got, refBase)
	}
}

// TestResizeRacesSnapshot drives resize() against info(). The reflow mutates
// the same grid the snapshot serializes and the block refs resolve against, so
// the two must not overlap; teardown joins the race to cover reflow-vs-free.
func TestResizeRacesSnapshot(t *testing.T) {
	const cols, rows = 80, 24
	refBase := ghosttyvt.LiveTrackedRefs()

	for i := 0; i < 20; i++ {
		func() {
			s, w := newLifetimeRaceSession(t, fmt.Sprintf("resize-snapshot-%d", i), cols, rows)
			go s.readLoop(nil, func(string, ...any) {})

			for j := 0; j < 12; j++ {
				if _, err := fmt.Fprintf(w, "\x1b]133;A\x07MARK-%02d\r\nfiller-%02d\r\n", j, j); err != nil {
					t.Errorf("pipe write: %v", err)
					return
				}
			}

			var wg sync.WaitGroup
			start := make(chan struct{})

			// Reflow across a range of widths: narrowing is what actually moves
			// tracked refs, so it is the case that exposes an unlocked resize.
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				widths := []uint16{40, 120, 60, 100, 30, 80}
				for _, cw := range widths {
					_ = s.resize(cw, rows)
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 30; k++ {
					info := s.info()
					// Every block row must fall inside the grid the same
					// snapshot reports — a row resolved against a
					// concurrently-reflowed terminal would not.
					for _, b := range info.GhosttyBlocks {
						if b.PromptRow < 0 || b.PromptRow >= int32(info.Rows) {
							t.Errorf("block %d row %d outside its own snapshot grid (%dx%d)",
								b.ID, b.PromptRow, info.Cols, info.Rows)
							return
						}
					}
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.closePTY()
			}()

			close(start)
			wg.Wait()
		}()
	}

	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked across resize/snapshot races: live=%d baseline=%d", got, refBase)
	}
}
