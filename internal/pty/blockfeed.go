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

// blockFeeder owns the ghostty write path for a session: it writes plain
// output to the terminal and pins a tracked ref at each OSC 133 marker's
// cursor position for the block table. Where a marker begins and ends is the
// feed segmenter's decision (kittyseg.go); this half only reacts to it. All
// methods are called under replayMu (the same critical section that assigns
// the seq watermark and serializes the dump), which is what makes the attach
// snapshot an atomic {dump, blocks, watermark} triple.
type blockFeeder struct {
	term  *ghosttyvt.Terminal
	table workerBlockTable
}

// newBlockFeeder wires the feeder for a session's ghostty terminal. Returns
// nil when the terminal is absent (construction failure, or nothing to feed):
// callers nil-guard exactly like every other ghostty use, and the attach
// snapshot simply carries no blocks.
//
// The worker block table is wired HERE — nowhere else. On the non-macOS stub,
// TrackCursor returns nil so the table pins nothing and serves no blocks;
// markers still arrive but resolve to unserializable blocks, degrading exactly
// like every other ghostty use off macOS.
func newBlockFeeder(term *ghosttyvt.Terminal) *blockFeeder {
	if term == nil {
		return nil
	}
	return &blockFeeder{term: term, table: newBlockTable()}
}

// write feeds one run of plain output to the terminal. Caller holds replayMu.
func (f *blockFeeder) write(segment []byte) {
	if len(segment) > 0 {
		f.term.Write(segment)
	}
}

// mark applies one OSC 133 marker at the cursor. It must be called in stream
// order, after the plain bytes that preceded the marker have been written: the
// cursor then sits on the cell the marker refers to (the row the prompt,
// command or output renders on next), which is what the pin captures. A nil
// marker is a sequence with no block event defined for its subtype — consumed,
// with nothing to record. Caller holds replayMu.
func (f *blockFeeder) mark(marker *osc133Marker) {
	if marker == nil {
		return
	}
	var ref blockRef
	if r := f.term.TrackCursor(); r != nil {
		ref = r
	}
	f.table.ApplyMarker(*marker, ref, f.term.AltScreenActive())
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
