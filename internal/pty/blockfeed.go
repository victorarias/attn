package pty

// OSC 133 command-block integration: where the segmenter and block table plug
// into the PTY write path and the attach snapshot, and under which lock.
// Design: docs/plans/2026-07-23-terminal-restore-fidelity.md; contract:
// Phase 3a in docs/plans/2026-07-22-server-authoritative-terminal.md.

import "github.com/victorarias/attn/internal/ghosttyvt"

// osc133MarkerKind enumerates the OSC 133 shell-integration markers.
type osc133MarkerKind byte

const (
	osc133PromptStart osc133MarkerKind = 'A'
	osc133InputStart  osc133MarkerKind = 'B'
	osc133PreExec     osc133MarkerKind = 'C'
	osc133CommandEnd  osc133MarkerKind = 'D'
)

// osc133Marker is one parsed marker. Cmdline is a C marker's percent-decoded
// cmdline_url (nil when absent); ExitCode is a D marker's exit status (nil
// when absent/unparsable).
type osc133Marker struct {
	Kind     osc133MarkerKind
	Cmdline  *string
	ExitCode *int32
}

// blockRef is the position pin the block table holds per marker — a
// ghosttyvt.TrackedRef in production. It follows its content across scrolling,
// pruning, and reflow; ScreenPoint reports ok=false once the content is gone.
type blockRef interface {
	ScreenPoint() (x, y int, ok bool)
	Free()
}

// AttachBlockData is one resolved command block in the attach snapshot. Rows
// are SCREEN-space rows of the serialized VT dump.
type AttachBlockData struct {
	// ID is server-assigned, monotonic per session.
	ID uint64
	// Pending marks the currently-open block (at most one; EndRow absent).
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

// workerBlockTable owns command-block lifecycle state; its executable spec is
// testdata/osc133_block_corpus.json, shared with the client TerminalBlockStore.
// Implementations are PURE: no locks (calls arrive under replayMu), no cgo
// beyond the blockRef handles; every retired ref must be freed.
type workerBlockTable interface {
	// ApplyMarker applies one marker pinned by ref. A nil ref still advances
	// lifecycle state but makes the block unserializable. altScreen blocks are
	// excluded from SnapshotBlocks — blocks are a primary-screen concept.
	ApplyMarker(m osc133Marker, ref blockRef, altScreen bool)
	// SnapshotBlocks resolves all blocks to SCREEN-space rows, dropping any
	// whose essential refs no longer resolve: correct-or-absent.
	SnapshotBlocks() []AttachBlockData
	// Close frees every held ref. The table is unusable afterwards.
	Close()
}

// blockFeeder owns the ghostty write path for a session: plain output to the
// terminal, plus a tracked ref pinned at each OSC 133 marker's cursor. All
// methods run under replayMu — what makes the attach snapshot an atomic
// {dump, blocks, watermark} triple.
type blockFeeder struct {
	term  *ghosttyvt.Terminal
	table workerBlockTable
}

// newBlockFeeder wires the feeder — and the worker block table, nowhere else —
// for a session's ghostty terminal. Returns nil when the terminal is absent:
// callers nil-guard, and the attach snapshot carries no blocks.
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

// mark applies one OSC 133 marker at the cursor. Must be called in stream
// order, after the marker's preceding plain bytes are written, so the cursor
// sits on the cell the pin captures. A nil marker is consumed with nothing
// recorded. Caller holds replayMu.
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
// replayMu — the same hold that serializes the VT dump and reads the watermark.
func (f *blockFeeder) snapshotBlocks() []AttachBlockData {
	return f.table.SnapshotBlocks()
}

// close frees the table's native refs. Called from closePTY before the
// terminal itself is closed.
func (f *blockFeeder) close() {
	f.table.Close()
}
