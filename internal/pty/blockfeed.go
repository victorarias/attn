package pty

// OSC 133 block-tracking skeleton (Phase 3a rails). This file fixes the
// integration contract for worker-owned command blocks: WHERE the segmenter
// and block table plug into the PTY write path and the attach snapshot, and
// under WHICH lock. The real implementations replace the no-ops in
// newBlockFeeder without touching session.go — call sites, lock placement,
// and the atomic {dump, blocks, watermark} triple are decided here, once.
// Design: docs/plans/2026-07-23-terminal-restore-fidelity.md; implementation
// contract: Phase 3a in docs/plans/2026-07-22-server-authoritative-terminal.md.

import "github.com/victorarias/attn/internal/ghosttyvt"

// osc133MarkerKind enumerates the OSC 133 shell-integration markers.
type osc133MarkerKind byte

const (
	osc133PromptStart osc133MarkerKind = 'A'
	osc133InputStart  osc133MarkerKind = 'B'
	osc133PreExec     osc133MarkerKind = 'C'
	osc133CommandEnd  osc133MarkerKind = 'D'
)

// osc133Marker is one parsed marker. Cmdline is the percent-decoded
// cmdline_url payload of a C marker (nil when absent); ExitCode is the D
// marker's exit status (nil when absent/unparsable).
type osc133Marker struct {
	Kind     osc133MarkerKind
	Cmdline  *string
	ExitCode *int32
}

// osc133Segmenter splits raw PTY output at complete OSC 133 markers,
// buffering partial markers across chunks. The client parser in
// app/src/utils/terminalOsc133.ts is the semantic reference; parity is
// enforced by a shared fixture corpus. emit is called in stream order: the
// bytes BEFORE each marker, then that marker; the final call carries any
// trailing bytes with a nil marker. Fast path requirement: a chunk containing
// no marker prefix while no partial marker is pending must produce exactly
// one emit(chunk, nil) passing the input slice through (no copy, no alloc).
type osc133Segmenter interface {
	Feed(chunk []byte, emit func(segment []byte, marker *osc133Marker))
}

// blockRef is the position pin the block table holds for each marker —
// backed by ghosttyvt.TrackedRef in production, by fakes in pure tests. The
// ref follows its content across scrolling, scrollback pruning, and reflow;
// ScreenPoint reports ok=false once the content is discarded.
type blockRef interface {
	ScreenPoint() (x, y int, ok bool)
	Free()
}

// AttachBlockData is one resolved command block in the attach snapshot. Rows
// are SCREEN-space rows of the serialized VT dump, which equal client buffer
// rows after the dump is written into a fresh same-size terminal (spike-
// verified, including after scrollback pruning). Mirrors the planned protocol
// AttachBlock shape; the protocol slice converts 1:1.
type AttachBlockData struct {
	// ID is server-assigned, monotonic per session — authoritative from day
	// one so a future block_event stream is purely additive.
	ID uint64
	// Pending marks the currently-open block (no command-end yet); at most
	// one entry has it set, and EndRow is absent on it.
	Pending        bool
	PromptRow      int32
	InputRow       *int32
	InputCol       *int32
	OutputStartRow *int32
	// EndRow is exclusive: the row the next prompt renders on.
	EndRow   *int32
	Command  *string
	ExitCode *int32
}

// workerBlockTable owns command-block lifecycle state. The corpus in
// testdata/osc133_block_corpus.json is its executable spec (proven against
// the client TerminalBlockStore by app/src/utils/terminalBlocks.corpus.test.ts).
// Implementations are PURE: no locks (every call arrives under replayMu via
// blockFeeder), no cgo beyond the blockRef handles. Every retired ref —
// cap eviction, self-heal replacement, alt-drop, Close — must be freed;
// tests assert ghosttyvt.LiveTrackedRefs returns to baseline.
type workerBlockTable interface {
	// ApplyMarker applies one marker whose position is pinned by ref. ref may
	// be nil (pin failed, or stub terminal): the marker still advances
	// lifecycle state so self-heal semantics hold, but the affected block
	// becomes unserializable and is dropped at snapshot. altScreen records
	// whether the alternate screen was active at pin time; such blocks are
	// excluded from SnapshotBlocks (blocks are a primary-screen concept).
	ApplyMarker(m osc133Marker, ref blockRef, altScreen bool)
	// SnapshotBlocks resolves all blocks to SCREEN-space rows. A block whose
	// essential refs (prompt or end) no longer resolve is dropped:
	// correct-or-absent, never a wrong row.
	SnapshotBlocks() []AttachBlockData
	// Close frees every held ref. The table is unusable afterwards.
	Close()
}

// blockFeeder owns the ghostty write path for a session: it splits PTY output
// at OSC 133 markers, writes each segment to the terminal, and pins a tracked
// ref at each marker's cursor position for the block table. All methods are
// called under replayMu (the same critical section that assigns the seq
// watermark and serializes the dump), which is what makes the attach snapshot
// an atomic {dump, blocks, watermark} triple.
type blockFeeder struct {
	term  *ghosttyvt.Terminal
	seg   osc133Segmenter
	table workerBlockTable
}

// newBlockFeeder wires the feeder for a session's ghostty terminal. Returns
// nil when the terminal is absent (construction failure, or nothing to feed):
// callers nil-guard exactly like every other ghostty use, and the attach
// snapshot simply carries no blocks.
//
// The real OSC 133 segmenter and worker block table are wired HERE — nowhere
// else. On the non-macOS stub, TrackCursor returns nil so the table pins
// nothing and serves no blocks; the segmenter still runs (pure Go) but its
// markers resolve to unserializable blocks, degrading exactly like every other
// ghostty use off macOS.
func newBlockFeeder(term *ghosttyvt.Terminal) *blockFeeder {
	if term == nil {
		return nil
	}
	return &blockFeeder{term: term, seg: &osc133ScanSegmenter{}, table: newBlockTable()}
}

// feed writes one PTY output chunk into the terminal, pinning block positions
// at marker boundaries. Caller holds replayMu.
func (f *blockFeeder) feed(data []byte) {
	f.seg.Feed(data, func(segment []byte, marker *osc133Marker) {
		if len(segment) > 0 {
			f.term.Write(segment)
		}
		if marker == nil {
			return
		}
		// Pin AFTER the pre-marker bytes are written: the cursor now sits on
		// the cell the marker refers to (the row the prompt/command/output
		// renders on next).
		var ref blockRef
		if r := f.term.TrackCursor(); r != nil {
			ref = r
		}
		f.table.ApplyMarker(*marker, ref, f.term.AltScreenActive())
	})
}

// snapshotBlocks resolves the block table to SCREEN-space rows. Caller holds
// replayMu — the SAME hold that serializes the VT dump and reads the seq
// watermark, so the three cannot disagree.
func (f *blockFeeder) snapshotBlocks() []AttachBlockData {
	return f.table.SnapshotBlocks()
}

// close frees the table's native refs. Called from closePTY before the
// terminal itself is closed.
func (f *blockFeeder) close() {
	f.table.Close()
}
