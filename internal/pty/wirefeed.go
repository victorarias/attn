package pty

// Feed composition: the outer half of the worker's PTY feed path.
//
// kittyseg.go owns the framing — one machine that tracks where ghostty's parser
// stands and cuts the stream into plain runs, complete kitty APCs, and complete
// OSC 133 markers. This file decides where each of those goes, turning one
// arriving chunk into two different things:
//
//   - the TERMINAL feed: every byte in order except the markers, which
//     blockfeed.go turns into block-table entries instead. A complete kitty APC
//     goes to ghostty whole, because ghostty is the system's only kitty parser.
//   - the WIRE bytes: the same stream with each APC replaced, in position, by
//     bytes that leave a kitty-ignorant terminal on the same grid — the scroll
//     and the cursor the placement caused, and nothing else. Usually that is no
//     bytes at all. Markers go out untouched; the client parses its own.
//
// Both are produced in one call, under the caller's replayMu, in the same
// critical section that advances the seq watermark. That is what keeps the
// attach contract intact: a snapshot taken mid-transmission serves the
// pre-placement grid, and everything the completing chunk did — terminal write,
// observation, synthesized bytes — carries a seq above that snapshot's LastSeq.
//
// The synthesized bytes are OBSERVED, never predicted. Ghostty runs the APC and
// the difference between the cursor before and after — pinned as tracked grid
// refs, which makes a scroll visible as movement — is what gets written.
// Nothing here knows what an image is. When the observation cannot answer (a
// ref that no longer resolves, a cursor that moved backwards) the session
// forces a snapshot re-push instead of guessing: a resync nobody notices beats
// a silent divergence between the worker grid and the client's.
//
// Feature-dark today. Production terminals run with a zero kitty storage limit
// (ghosttyvt.Options), so ghostty refuses every transmission, the generation
// stamp never moves, and the only visible effect is that APC bytes are dropped
// from the wire instead of being sent to a client that cannot parse them.
// Design: docs/plans/2026-08-02-terminal-kitty-images.md.

import (
	"strconv"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// Resync reasons. They travel to the client as the pty_desync reason and land
// in the daemon log, so each one names the observation that failed rather than
// saying "kitty".
const (
	// kittyResyncAnchorLost: the cell the cursor sat on before the APC could
	// not be resolved afterwards, so how far the grid moved is unknowable.
	kittyResyncAnchorLost = "kitty_layout_anchor_lost"
	// kittyResyncAnchorClamped: the scroll pushed that cell to the top of
	// retained history, where a cell that fell off the end is indistinguishable
	// from one that stopped there — ghostty clamps a discarded ref rather than
	// invalidating it. Reached by an image taller than the alternate screen,
	// which keeps no history at all.
	kittyResyncAnchorClamped = "kitty_layout_anchor_clamped"
	// kittyResyncReverseScroll: the grid moved DOWN under the cursor. Nothing a
	// placement does looks like that, and synthesis does not express it.
	kittyResyncReverseScroll = "kitty_layout_reverse_scroll"
	// kittyResyncUndescribedImage: ghostty's kitty state moved on bytes that
	// went to the wire verbatim rather than through synthesis. That happens when
	// an APC is one ghostty parses as kitty but the segmenter cannot cut out —
	// an APC introduced from inside another sequence, whose leading ESC is also
	// that sequence's exit (see kittyseg.go). Replaying those bytes keeps the
	// two PARSERS in step, which is what the segmenter guarantees, but the
	// client cannot parse kitty, so the image the worker just placed moved its
	// grid and not the client's. Only a snapshot can settle that.
	kittyResyncUndescribedImage = "kitty_undescribed_image"
)

// kittyPlacementKey identifies a placement across observations: kitty's own
// image id plus placement id, which is the only pair ghostty keeps stable.
type kittyPlacementKey struct {
	ImageID     uint32
	PlacementID uint32
}

// kittyPlacementDelta is what one APC did to the active screen's placement set.
// It is the shape the protocol events of the next phase are cut from; nothing
// consumes it yet.
//
// Updated carries placements whose fields moved for ANY reason, including a
// viewport position that changed because the screen scrolled since the last
// observation — observation only happens on APC writes, so scroll-induced
// movement surfaces late, at the next one, rather than as it happens.
type kittyPlacementDelta struct {
	Added   []ghosttyvt.KittyPlacement
	Removed []kittyPlacementKey
	Updated []ghosttyvt.KittyPlacement
}

func (d kittyPlacementDelta) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Updated) == 0
}

// wireFeeder splits PTY output into plain runs and kitty APCs, feeds all of it
// to the terminal, and returns what the wire should carry instead. Every method
// is called under replayMu, like blockFeeder's.
type wireFeeder struct {
	term   *ghosttyvt.Terminal
	blocks *blockFeeder
	seg    feedSegmenter

	// wire is the assembly buffer for a chunk the feeder had to rewrite. It is
	// reused across calls and handed out by feed, so it is valid only until the
	// next feed — fanOut copies before it returns, which is the only consumer.
	wire []byte

	// generation is ghostty's kitty stamp as of the last change this feeder
	// ACCOUNTED for. Every dispatch either goes through writeAPC, which
	// describes it on the wire, or is one the wire cannot describe — and the
	// difference between this and the terminal's own stamp at the end of a feed
	// is exactly the second kind.
	generation uint64

	// placements is the placement set as of the last observation, the left side
	// of the next diff.
	placements []ghosttyvt.KittyPlacement
	// deltas holds what the APCs in the MOST RECENT feed call did to that set,
	// so it stays bounded by one chunk. The next phase replaces it with an
	// event sink; until then only tests read it.
	deltas []kittyPlacementDelta

	// resync names the observation that failed during this feed, "" when none.
	resync string
}

// newWireFeeder wires the feed path for a session's ghostty terminal. Returns
// nil when the terminal is absent, exactly like newBlockFeeder: callers
// nil-guard, and a session without a terminal fans out its raw bytes unchanged.
func newWireFeeder(term *ghosttyvt.Terminal) *wireFeeder {
	blocks := newBlockFeeder(term)
	if blocks == nil {
		return nil
	}
	return &wireFeeder{term: term, blocks: blocks, generation: term.KittyGeneration()}
}

// feed writes one PTY chunk into the terminal and returns the bytes the wire
// should carry for it, plus a resync reason when the chunk's grid effect could
// not be expressed on the wire (see Session.forceResync). Caller holds replayMu.
//
// The returned slice is the INPUT slice itself whenever the chunk needed no
// rewriting — no kitty APC in it, nothing held from a previous chunk. That is
// every chunk of every session today, so the common path allocates nothing and
// copies nothing. Otherwise it is the feeder's assembly buffer, valid until the
// next feed call.
//
// An empty result means the whole chunk was held: an APC that has not been
// terminated yet contributes nothing to the wire until it completes. The caller
// skips the fan-out rather than sending an empty message; downstream dedup is
// `seq > last_seq`, which does not care that a seq never appeared.
func (f *wireFeeder) feed(data []byte) ([]byte, string) {
	f.deltas = f.deltas[:0]
	f.resync = ""
	f.wire = f.wire[:0]
	if len(data) == 0 {
		return nil, ""
	}

	// whole is set only by a plain run that IS the input slice, which no other
	// emission can follow — that is the passthrough case, and it is the only
	// emission allowed to survive its callback. Every other emitted slice
	// aliases a buffer the segmenter may rewrite before Feed returns, so it is
	// copied on the spot.
	whole := false
	first := true
	f.seg.Feed(data, func(seg feedSegment) {
		switch seg.Kind {
		case feedSegPlain:
			f.blocks.write(seg.Bytes)
			if first && len(seg.Bytes) == len(data) && &seg.Bytes[0] == &data[0] {
				whole = true
			} else {
				f.wire = append(f.wire, seg.Bytes...)
			}
		case feedSegKittyAPC:
			f.writeAPC(seg.Bytes)
		case feedSegOSC133:
			// The wire carries the marker and the terminal does not: the client
			// runs its own OSC 133 parser over the wire, and the worker's block
			// table takes the parsed marker in place of the bytes.
			f.wire = append(f.wire, seg.Bytes...)
			f.blocks.mark(seg.Marker)
		}
		first = false
	})

	// Anything the terminal's kitty state did that writeAPC did not account for
	// happened on bytes the wire carries verbatim, and the client ignores them.
	// One cheap read per chunk buys the guarantee that no image ever lands on
	// the worker's grid alone.
	//
	// Only an APPEARING placement is a divergence. The stamp also moves when
	// ghostty prunes placements the screen no longer holds — leaving the
	// alternate screen is the common one — and a placement going away costs the
	// client nothing: it never drew the image, and the mode switch that pruned
	// it is on the wire already.
	if stamped := f.term.KittyGeneration(); stamped != f.generation {
		f.generation = stamped
		before := len(f.deltas)
		f.observe()
		if f.appeared(before) {
			f.failResync(kittyResyncUndescribedImage)
		}
	}

	if whole {
		return data, f.resync
	}
	return f.wire, f.resync
}

// writeAPC feeds one complete kitty APC to the terminal and appends whatever
// the wire needs in its place. The ordering is the contract: pin the cursor
// before the write, because a tracked ref is the only way to see afterwards how
// far the grid moved under it.
func (f *wireFeeder) writeAPC(apc []byte) {
	generation := f.term.KittyGeneration()
	col, row := f.term.CursorPos()
	before := f.term.TrackCursor()

	f.term.Write(apc)

	stamped := f.term.KittyGeneration()
	// Claimed here rather than at each exit below: every branch from this point
	// on has either described the dispatch or resynced over it, so the
	// end-of-feed check must not see it a second time.
	f.generation = stamped
	movedCol, movedRow := f.term.CursorPos()
	// An unchanged generation means the storage did not change, so no placement
	// appeared, so nothing scrolled the grid on an image's behalf — which is
	// what makes the viewport cursor enough to decide here. On its own it would
	// not be: a placement on the bottom row scrolls the grid while leaving the
	// cursor's viewport row exactly where it was.
	//
	// This is the shipping configuration's every APC. A zero kitty storage
	// limit makes ghostty refuse the transmission outright, so nothing is
	// stamped and nothing moves.
	if stamped == generation && movedCol == col && movedRow == row {
		freeTrackedRef(before)
		return
	}

	if stamped != generation {
		f.observe()
	}

	after := f.term.TrackCursor()
	anchor, landed, ok := trackedRows(before, after)
	freeTrackedRef(before)
	freeTrackedRef(after)
	if !ok {
		f.failResync(kittyResyncAnchorLost)
		return
	}

	// The tracked pair reports how far the cursor moved relative to the CONTENT
	// it sits on, which is not how far the grid moved: a placement that scrolls
	// carries the cursor's own cell up with everything else. Taking the cursor's
	// viewport movement back out of it leaves the scroll, and that identity
	// holds on both screens and inside a scroll region — the three cases where
	// the coordinate frames differ.
	scrolled := (row - movedRow) + (landed - anchor)
	if scrolled < 0 {
		f.failResync(kittyResyncReverseScroll)
		return
	}
	// An anchor on row 0 only means the pin was CLAMPED there on the alternate
	// screen, which keeps no history: a cell that scrolls off has nowhere to go,
	// so it stops at the top and the scroll it took with it is unrecoverable.
	// Measured, and both directions of it: on alt, a placement that fits and one
	// that scrolls the top row away report the identical (anchor 0, scrolled 0),
	// so the amount cannot be recovered from the numbers and every alt anchor at
	// row 0 has to resync — not only the ones that already look scrolled. On the
	// primary screen the same reading is trustworthy, because the pin follows
	// its cell into retained history and keeps reporting a real row; the only
	// way to lose it there is the scrollback cap discarding the cell, which
	// ScreenPoint reports as gone and kittyResyncAnchorLost handles above.
	if anchor == 0 && f.term.AltScreenActive() {
		f.failResync(kittyResyncAnchorClamped)
		return
	}

	// SU scrolls the active scroll region and leaves the cursor's viewport
	// position alone, so the row move that follows is a plain relative step from
	// where the pre-APC bytes already left the client's cursor. Everything here
	// is relative or column-only on purpose: absolute row addressing (CUP, VPA)
	// is measured from the scroll region under origin mode, which the worker
	// cannot see and must not have to.
	f.wire = appendCSI(f.wire, scrolled, 'S')
	if movedRow > row {
		f.wire = appendCSI(f.wire, movedRow-row, 'B')
	} else {
		f.wire = appendCSI(f.wire, row-movedRow, 'A')
	}
	if movedCol != col {
		f.wire = appendCSI(f.wire, movedCol+1, 'G')
	}
}

// appendCSI writes `ESC [ n <final>`, or nothing when n is zero — every
// sequence synthesis uses is a no-op at zero, and an empty wire chunk is what
// lets the caller skip a fan-out entirely.
func appendCSI(dst []byte, n int, final byte) []byte {
	if n == 0 {
		return dst
	}
	dst = append(dst, 0x1b, '[')
	dst = strconv.AppendInt(dst, int64(n), 10)
	return append(dst, final)
}

// observe diffs ghostty's placement set against the last observation. Called
// only when the generation stamp moved, which is the terminal's own statement
// that the set or its images changed.
func (f *wireFeeder) observe() {
	current := f.term.KittyPlacements()
	delta := diffKittyPlacements(f.placements, current)
	f.placements = current
	if !delta.empty() {
		f.deltas = append(f.deltas, delta)
	}
}

// appeared reports whether the observations recorded past index from brought
// any placement into existence, as opposed to only retiring or moving ones the
// wire has already accounted for.
func (f *wireFeeder) appeared(from int) bool {
	for _, delta := range f.deltas[from:] {
		if len(delta.Added) > 0 {
			return true
		}
	}
	return false
}

// failResync records the first observation failure of this chunk. The APC's
// grid effect is already in the terminal; the wire gets nothing for it, and the
// snapshot the client re-attaches for carries the truth.
func (f *wireFeeder) failResync(reason string) {
	if f.resync == "" {
		f.resync = reason
	}
}

// snapshotBlocks resolves the block table under the caller's replayMu — the
// SAME hold that serializes the VT dump and reads the seq watermark.
func (f *wireFeeder) snapshotBlocks() []AttachBlockData {
	return f.blocks.snapshotBlocks()
}

// close frees the native refs the block table holds. Called from closePTY
// before the terminal itself is closed.
func (f *wireFeeder) close() {
	f.blocks.close()
}

// trackedRows resolves where the cursor's cell ended up (anchor) and where the
// cursor itself is now (landed), in rows counted from the top of retained
// history.
//
// Both are read AFTER the write so they share one coordinate frame; resolving
// the first one earlier would put it in a frame that scrollback pruning may
// have shifted out from under it. That shared frame is what makes their
// difference meaningful on both screens: on the primary the pinned cell keeps
// its row while the active area slides down past it, on the alternate the
// active area is fixed and the pinned cell slides up.
func trackedRows(before, after *ghosttyvt.TrackedRef) (anchor, landed int, ok bool) {
	if before == nil || after == nil {
		return 0, 0, false
	}
	_, anchor, ok = before.ScreenPoint()
	if !ok {
		return 0, 0, false
	}
	_, landed, ok = after.ScreenPoint()
	if !ok {
		return 0, 0, false
	}
	return anchor, landed, true
}

func freeTrackedRef(ref *ghosttyvt.TrackedRef) {
	if ref != nil {
		ref.Free()
	}
}

// diffKittyPlacements reports what changed between two observations of the
// active screen's placement set. Placements are compared whole: the fields are
// all scalars, and a rule that named specific ones would rot the first time
// ghostty resolved a new piece of geometry.
func diffKittyPlacements(before, after []ghosttyvt.KittyPlacement) kittyPlacementDelta {
	var delta kittyPlacementDelta
	if len(before) == 0 && len(after) == 0 {
		return delta
	}

	prior := make(map[kittyPlacementKey]ghosttyvt.KittyPlacement, len(before))
	for _, p := range before {
		prior[kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}] = p
	}
	live := make(map[kittyPlacementKey]struct{}, len(after))
	for _, p := range after {
		key := kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}
		live[key] = struct{}{}
		switch old, ok := prior[key]; {
		case !ok:
			delta.Added = append(delta.Added, p)
		case old != p:
			delta.Updated = append(delta.Updated, p)
		}
	}
	for _, p := range before {
		key := kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}
		if _, ok := live[key]; !ok {
			delta.Removed = append(delta.Removed, key)
		}
	}
	return delta
}
