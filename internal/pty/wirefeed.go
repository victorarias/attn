package pty

// Outer half of the worker's PTY feed path: kittyseg.go cuts the stream, this
// file routes it. The terminal gets plain runs, kitty APCs, and OSC 133
// markers; the wire gets the same stream with each APC replaced in position by
// an ST plus the OBSERVED scroll/cursor effect — never predicted. When an
// observation cannot answer, the session forces a snapshot re-push instead of
// guessing. All of it happens under the caller's replayMu, in the same critical
// section that advances the seq watermark, which is what keeps attach
// snapshots consistent. ATTN_KITTY_STORAGE_LIMIT=0 turns the protocol off.
// Design: docs/plans/2026-08-02-terminal-kitty-images.md.

import (
	"bytes"
	"strconv"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// Resync reasons. They travel to the client as the pty_desync reason and land
// in the daemon log; each names the observation that failed.
const (
	// kittyResyncAnchorLost: the cursor's pre-APC cell could not be resolved
	// afterwards, so how far the grid moved is unknowable.
	kittyResyncAnchorLost = "kitty_layout_anchor_lost"
	// kittyResyncAnchorClamped: the scroll pushed the anchor to the top of
	// retained history (ghostty clamps a discarded ref), where a cell that fell
	// off is indistinguishable from one that stopped there. Reached by an image
	// taller than the alternate screen, which keeps no history.
	kittyResyncAnchorClamped = "kitty_layout_anchor_clamped"
	// kittyResyncReverseScroll: the grid moved DOWN under the cursor, which no
	// placement does and synthesis cannot express.
	kittyResyncReverseScroll = "kitty_layout_reverse_scroll"
	// kittyResyncUndescribedImage: kitty state moved on bytes that went to the
	// wire verbatim (an APC the segmenter could not cut out — see kittyseg.go)
	// and the diff found a placement created, re-placed, or retransmitted. The
	// worker's grid moved and the client's did not; only a snapshot settles it.
	kittyResyncUndescribedImage = "kitty_undescribed_image"
	// kittyResyncStampWithoutDelta: verbatim bytes moved ghostty's kitty stamp
	// and the diff found NOTHING — a placement created and destroyed inside one
	// chunk. Whatever it scrolled has no witness but the stamp.
	kittyResyncStampWithoutDelta = "kitty_stamp_without_delta"
	// kittyResyncMarginMode: DECLRMM (DEC mode 69) was on. A margin-box scroll
	// moves text without moving rows, so the tracked pair reads no scroll and no
	// SU goes out. Measured at da5ddcb: margins `\x1b[4;14s` + placement at the
	// box bottom climbs the worker's text a row while the client's stays put
	// (it was two rows at d760ee9; how far a placement scrolls is upstream's
	// call, the divergence is not). Fires on
	// every described dispatch while margins are on — a tripwire, not a repair;
	// no emitter in the A4 sweep enables DECLRMM.
	kittyResyncMarginMode = "kitty_layout_margin_mode"
	// kittyResyncScrollClamped: the placement scrolled further than one SU can
	// express (ghostty clamps SU to the scroll region height), so the client's
	// history would come out short. A tripwire on this ghostty pin: a placement's
	// scroll no longer tracks the row count `r=` claims and stays inside the
	// screen, so `r=`, the only knob that dials it, no longer reaches. Re-probed
	// at da5ddcb over 645 shapes (heights 1..40 plus 64, 128 and 129, cursor on
	// the first, second and last row, on 2-, 3-, 4-, 8- and 12-row screens):
	// none reached this, and worker and client agreed on every one. It stays
	// because the divergence it names is silent and a pin bump restoring
	// proportional scrolling would ship it.
	kittyResyncScrollClamped = "kitty_layout_scroll_clamped"
	// kittyResyncPendingWrap: the cursor sat in the LAST COLUMN, where a
	// dispatch may consume a pending-wrap bit CursorPos cannot see. That
	// divergence was measured at d760ee9: print a screen's width, place an
	// image, print one more character, and the worker stayed on row 0 while the
	// client wrapped to row 1.
	//
	// At da5ddcb it is gone. Re-probed over 336 shapes (2-, 3-, 8-, 20- and
	// 80-column screens of 2, 3, 8 and 24 rows, images 1..3 cells wide by 1..3
	// rows, on the first and last row): the placement's own cursor move now
	// describes the wrap, and worker and client agreed on every one.
	//
	// It stays, and the cost of that is worth naming: this fires on the COLUMN,
	// not on a measured divergence, so it resyncs on 336 of 336. It is free only
	// because no emitter in the A4 sweep places an image without positioning the
	// cursor first. What it buys is that nothing exposes the pending-wrap bit,
	// so 336 agreeing shapes do not prove the wire describes it in general, and
	// the failure it names is silent. Removing it is a decision about trusting
	// the description, on its own evidence.
	kittyResyncPendingWrap = "kitty_layout_pending_wrap"
)

// kittyPlacementKey identifies a placement across observations: kitty's image
// id plus placement id, the only pair ghostty keeps stable.
type kittyPlacementKey struct {
	ImageID     uint32
	PlacementID uint32
}

// kittyPlacementDelta is what one observation found changed in the active
// screen's placement set. Updated carries placements whose fields moved for ANY
// reason, a scrolled viewport position included.
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

	// wire is the assembly buffer for a rewritten chunk, reused across calls;
	// the slice feed hands out is valid only until the next feed.
	wire []byte

	// generation is ghostty's kitty stamp as of the last change this feeder
	// ACCOUNTED for; a difference against the terminal's own stamp is exactly
	// the undescribed kind, read in settleUnaccounted. Raw — the epoch is never
	// folded into this internal change detector.
	generation uint64

	// epoch is the offset folded into every generation handed out; must match
	// Session.kittyEpoch. See mintKittyEpoch.
	epoch uint64

	// placements is the set as of the last observation, the left side of the
	// next diff.
	placements []ghosttyvt.KittyPlacement
	// deltas holds what the MOST RECENT feed's observations found (bounded by
	// one chunk). Never handed out: unaccountedResync reads its tail,
	// changedPlacements reads its emptiness.
	deltas []kittyPlacementDelta

	// resync names the observation that failed during this feed, "" when none.
	resync string

	// logf reports a refused transmission, nothing else. nil in tests.
	logf LogFunc

	// kittyLimit is the cap this terminal was BUILT with, carried so the log
	// names the number in force even if the environment moved after spawn.
	kittyLimit uint64

	// pending is the transmission being assembled across m=1 escapes, so a
	// refusal is judged once, at completion. Zero between transmissions.
	pending kittyTransmission
}

// newWireFeeder wires the feed path for a session's ghostty terminal. Returns
// nil when the terminal is absent, exactly like newBlockFeeder: callers
// nil-guard, and a session without a terminal fans out raw bytes unchanged.
// epoch must be the same value the caller holds on the Session (mintKittyEpoch).
func newWireFeeder(term *ghosttyvt.Terminal, epoch uint64, logf LogFunc, kittyLimit uint64) *wireFeeder {
	blocks := newBlockFeeder(term)
	if blocks == nil {
		return nil
	}
	return &wireFeeder{
		term:       term,
		blocks:     blocks,
		epoch:      epoch,
		logf:       logf,
		kittyLimit: kittyLimit,
		generation: term.KittyGeneration(),
	}
}

// feed writes one PTY chunk into the terminal and returns the bytes the wire
// should carry, plus a resync reason when the chunk's grid effect could not be
// expressed (see Session.forceResync). Caller holds replayMu.
//
// The returned slice is the INPUT slice itself when no rewriting was needed —
// the common path allocates and copies nothing — otherwise the feeder's
// assembly buffer, valid until the next feed. An empty result means the whole
// chunk was held (an unterminated APC); the caller skips the fan-out, and
// downstream dedup (`seq > last_seq`) tolerates the missing seq.
func (f *wireFeeder) feed(data []byte) ([]byte, string) {
	f.deltas = f.deltas[:0]
	f.resync = ""
	f.wire = f.wire[:0]
	if len(data) == 0 {
		return nil, ""
	}

	// whole marks the passthrough case: a plain run that IS the input slice.
	// Every other emitted slice aliases a buffer the segmenter may rewrite
	// before Feed returns, so it is copied on the spot.
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
			// Write before mark() so the block-table pin lands on Ghostty's
			// post-marker cursor.
			f.wire = append(f.wire, seg.Bytes...)
			f.blocks.write(seg.Bytes)
			f.blocks.mark(seg.Marker)
		}
		first = false
	})

	// Settle whatever the chunk's last bytes did to kitty state against what
	// the wire carried for them.
	settled := f.settleUnaccounted()

	// A live placement moves on bytes that touch no kitty state (a scroll), and
	// the stamp does not move with it — so re-observe at the end of any chunk
	// that could have moved one, or a described position runs behind its grid.
	// The gate is a slice length: with no placements this costs one comparison
	// and never crosses into cgo.
	if !settled && len(f.placements) > 0 {
		f.observe()
	}

	if whole {
		return data, f.resync
	}
	return f.wire, f.resync
}

// wireST is ST in its 7-bit form (ESC backslash) — the substitute written
// wherever the two streams differ. Always 7-bit even when the APC ended in C1
// ST: a raw 0x9c on the wire is a stray UTF-8 continuation byte to the client,
// not an ST.
//
// The rule every extraction here obeys: wherever the two streams differ, BOTH
// sides get an ESC-led no-op at that position. Ground also holds a UTF-8
// decoder that may be mid-character; an ESC ends that decode, so whichever side
// loses bytes must be given an ESC in their place or the two decoders resolve
// the same character differently.
var wireST = []byte{0x1b, '\\'}

// writeAPC feeds one complete kitty APC to the terminal and appends whatever
// the wire needs in its place. Ordering is the contract: end the pending decode
// on both sides BEFORE anything is measured, then pin the cursor before the
// write so a tracked ref can report how far the grid moved.
func (f *wireFeeder) writeAPC(apc []byte) {
	// Settle earlier bytes first: an undescribed kitty escape ahead of this APC
	// leaves a stamp move the claim below would otherwise absorb.
	f.settleUnaccounted()

	// The abort, to both sides, ahead of every measurement. On the WIRE it
	// stands in for the APC's leading ESC. On the WORKER it is not redundant:
	// ending a decode is a GRID event (a replacement character on the last
	// column commits the deferred wrap), and doing it before the pin keeps the
	// measured window holding only what the image did. Safe unconditionally:
	// from ground ghostty treats ST as a no-op —
	// TestWireFeedPreSTOnlyEndsTheDecode pins that.
	f.term.Write(wireST)
	f.wire = append(f.wire, wireST...)

	// The settle above left f.generation equal to the terminal's stamp.
	generation := f.generation
	col, row := f.term.CursorPos()
	before := f.term.TrackCursor()

	f.term.Write(apc)

	stamped := f.term.KittyGeneration()
	// Claimed here, once: every branch below has either described the dispatch
	// or resynced over it, so no later settle may see it again.
	f.generation = stamped
	f.noteTransmission(apc, stamped != generation)
	movedCol, movedRow := f.term.CursorPos()
	// Unchanged generation means no placement appeared, so nothing scrolled on
	// an image's behalf — only then is the viewport cursor enough to decide.
	// This is the shipping configuration's every APC.
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

	// The tracked pair reports cursor movement relative to CONTENT; taking the
	// viewport movement back out leaves the scroll. The identity holds on both
	// screens and inside a scroll region.
	scrolled := (row - movedRow) + (landed - anchor)
	if scrolled < 0 {
		f.failResync(kittyResyncReverseScroll)
		return
	}
	// On the alternate screen an anchor at row 0 only means the pin was CLAMPED
	// there (no history), and the scroll amount is unrecoverable. Measured: a
	// placement that fits and one that scrolls the top row away both report
	// (anchor 0, scrolled 0). On the primary the pin follows its cell into
	// history; losing it there reads as anchor-lost above.
	if anchor == 0 && f.term.AltScreenActive() {
		f.failResync(kittyResyncAnchorClamped)
		return
	}

	// One SU carries at most a screen's worth of rows (ghostty clamps to the
	// scroll region), so a taller scroll would leave the client's history short.
	if _, screenRows := f.term.Size(); scrolled > screenRows {
		f.failResync(kittyResyncScrollClamped)
		return
	}

	// Margin-confined scroll is movement this measurement cannot see. The
	// cursor moves are still trustworthy (they agreed in every measured margin
	// case), so the dispatch is described in full and the resync repairs the
	// text — a resync is never a stop order.
	if f.term.LeftRightMarginMode() {
		f.failResync(kittyResyncMarginMode)
	}

	// Same shape: state changed that the measurement cannot read. Sits after
	// the early return on measurement — a dispatch that changed nothing (query,
	// delete of an absent id) needs no resync even in the last column.
	if screenCols, _ := f.term.Size(); col == screenCols-1 {
		f.failResync(kittyResyncPendingWrap)
	}

	// All moves are RELATIVE, on both axes: absolute addressing is measured
	// from a frame the worker cannot see (origin mode for rows, DECLRMM's left
	// margin for columns). A relative step is the same step in every frame. SU
	// leaves the cursor's viewport position alone.
	f.wire = appendCSI(f.wire, scrolled, 'S')
	if movedRow > row {
		f.wire = appendCSI(f.wire, movedRow-row, 'B')
	} else {
		f.wire = appendCSI(f.wire, row-movedRow, 'A')
	}
	if movedCol > col {
		f.wire = appendCSI(f.wire, movedCol-col, 'C')
	} else {
		f.wire = appendCSI(f.wire, col-movedCol, 'D')
	}
}

// kittyTransmission is a transmission being assembled: kitty splits a large
// image across several escapes, each carrying `m=1` until the last, and nothing
// is stored until that last one lands.
type kittyTransmission struct {
	// ask is the storage the image will occupy once decoded, from the declared
	// geometry. Zero when none was declared (f=100 PNG); payload stands in.
	ask uint64
	// payload counts the base64 bytes seen across every escape so far.
	payload uint64
}

// noteTransmission logs the one failure ghostty cannot report: an image refused
// for exceeding the storage limit. Every measured emitter sends q=2, which
// suppresses kitty's own response, so the worker is the only witness.
//
// stored says whether ghostty's kitty generation moved on this escape, which is
// the whole signal. Measured:
//
//	single transmission that fits      generation moves
//	single transmission over the limit  UNCHANGED — the one true positive
//	chunked (m=1 …) that fits           unchanged on every intermediate escape,
//	                                    moves only on the completing m=0
//	chunked over the limit              unchanged throughout, m=0 included
//	a=q query                           unchanged — never a transmission
//	a=p re-place, a=d delete of a live id   moves
//	a=d delete of an id that is not there   unchanged — never a transmission
//	eviction (a third image into a store that holds one)   moves
//
// So intermediate escapes accumulate and are never judged, and eviction is
// invisible here because admitting the new image moves the stamp.
func (f *wireFeeder) noteTransmission(apc []byte, stored bool) {
	ask, more, ok := parseKittyTransmission(apc)
	if !ok {
		return
	}
	f.pending.payload += ask.payload
	if ask.ask > 0 {
		f.pending.ask = ask.ask
	}
	if more {
		return
	}

	want := f.pending.ask
	if want == 0 {
		want = f.pending.payload
	}
	f.pending = kittyTransmission{}
	if stored || f.logf == nil {
		return
	}
	f.logf(
		"pty kitty storage: an image transmission stored nothing — %s=%d bytes, this image asks for about %d. "+
			"An image larger than the whole limit is refused outright; raise the limit or have the program send a smaller one. "+
			"(Evicting an older image to fit a new one is not this, and is never logged.)",
		kittyStorageLimitEnv,
		f.kittyLimit,
		want,
	)
}

// parseKittyTransmission reads the keys a refusal check needs out of one
// complete APC. Deliberately not a kitty parser: it reads what it recognizes
// and treats the rest as absent — the worst a misread does is drop a log line.
// kitty's default action is `t`, so an escape with no `a=` is a transmission,
// which is what a continuation escape looks like.
func parseKittyTransmission(apc []byte) (t kittyTransmission, more bool, ok bool) {
	body := apc
	body = bytes.TrimPrefix(body, []byte("\x1b_G"))
	if len(body) == len(apc) {
		return t, false, false
	}
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte("\x1b\\")), []byte{0x9c})

	control := body
	if i := bytes.IndexByte(body, ';'); i >= 0 {
		control = body[:i]
		// Base64: 4 encoded characters carry 3 raw bytes.
		t.payload = uint64(len(body)-i-1) * 3 / 4
	}

	action := byte('t')
	var width, height uint64
	for _, pair := range bytes.Split(control, []byte(",")) {
		key, value, found := bytes.Cut(pair, []byte("="))
		if !found || len(key) != 1 || len(value) == 0 {
			continue
		}
		switch key[0] {
		case 'a':
			action = value[0]
		case 'm':
			more = value[0] == '1'
		case 's':
			width, _ = strconv.ParseUint(string(value), 10, 64)
		case 'v':
			height, _ = strconv.ParseUint(string(value), 10, 64)
		}
	}
	if action != 't' && action != 'T' {
		return kittyTransmission{}, false, false
	}
	// Ghostty stores decoded RGBA whatever the wire format was, so declared
	// pixels are the honest ask.
	t.ask = width * height * 4
	return t, more, true
}

// appendCSI writes `ESC [ n <final>`, or nothing when n is zero — every
// sequence synthesis uses is a no-op at zero.
func appendCSI(dst []byte, n int, final byte) []byte {
	if n == 0 {
		return dst
	}
	dst = append(dst, 0x1b, '[')
	dst = strconv.AppendInt(dst, int64(n), 10)
	return append(dst, final)
}

// placementReadHook is a test-only seam fired on every read of the placement
// set, so a test can assert a session with no images never reaches ghostty for
// placements. nil in production.
var placementReadHook func()

// readPlacements is the only place the placement set is read out of ghostty,
// and therefore the one fold of the epoch on the placement side — every
// placement exit (live fan-out, resize re-describe, attach snapshot) draws from
// here. The set is freshly copied per call. Callers hold replayMu.
func (f *wireFeeder) readPlacements() []ghosttyvt.KittyPlacement {
	if placementReadHook != nil {
		placementReadHook()
	}
	placements := f.term.KittyPlacements()
	for i := range placements {
		placements[i].ImageGeneration += f.epoch
	}
	return placements
}

// observe diffs ghostty's placement set against the last observation. Called
// when the generation stamp moved, and at the end of any chunk that could have
// moved a placement the terminal does not consider changed (see feed).
func (f *wireFeeder) observe() {
	current := f.readPlacements()
	delta := diffKittyPlacements(f.placements, current)
	f.placements = current
	if !delta.empty() {
		f.deltas = append(f.deltas, delta)
	}
}

// settleUnaccounted closes the books on kitty state changes no writeAPC
// dispatch accounted for, and reports whether there were any. Called at the end
// of every feed AND at the entry of every writeAPC — the second is what keeps
// the claim honest: without it, a described APC silently absorbs an undescribed
// stamp move earlier in the same chunk. After it returns, f.generation IS the
// terminal's current stamp.
func (f *wireFeeder) settleUnaccounted() bool {
	stamped := f.term.KittyGeneration()
	if stamped == f.generation {
		return false
	}
	f.generation = stamped

	// Judge only the observations THIS settle records; earlier deltas were
	// already described or resynced over.
	before := len(f.deltas)
	f.observe()
	if reason, ok := unaccountedResync(f.deltas[before:]); ok {
		f.failResync(reason)
	}
	return true
}

// unaccountedResync names what a generation move on verbatim bytes costs the
// client; false when it costs nothing. A resync exists for grid SCROLL the wire
// never expressed, not for knowledge of the set (that fans out on its own).
// Only creating or moving a placement can scroll, and retiring one moves
// nothing — so a removals-only delta is exempt; everything else resyncs:
// no delta at all (a placement born and dead inside one chunk, witnessed only
// by the stamp), Added, or Updated (re-placed or retransmitted — charged
// together because this check cannot tell them apart). Ordinary scrolls cannot
// reach here: they move placements without moving the stamp.
func unaccountedResync(deltas []kittyPlacementDelta) (string, bool) {
	if len(deltas) == 0 {
		return kittyResyncStampWithoutDelta, true
	}
	for _, delta := range deltas {
		if len(delta.Added) > 0 || len(delta.Updated) > 0 {
			return kittyResyncUndescribedImage, true
		}
	}
	return "", false
}

// failResync records the first observation failure of this chunk.
func (f *wireFeeder) failResync(reason string) {
	if f.resync == "" {
		f.resync = reason
	}
}

// changedPlacements reports the active screen's whole placement set when this
// feed moved it, and nothing when it did not. No copy needed: observe REPLACES
// the set rather than mutating it.
func (f *wireFeeder) changedPlacements() ([]ghosttyvt.KittyPlacement, bool) {
	if len(f.deltas) == 0 {
		return nil, false
	}
	return f.placements, true
}

// snapshotBlocks resolves the block table under the caller's replayMu — the
// same hold that serializes the VT dump and reads the seq watermark.
func (f *wireFeeder) snapshotBlocks() []AttachBlockData {
	return f.blocks.snapshotBlocks()
}

// snapshotPlacements resolves the active screen's placement set under the
// caller's replayMu, same hold as the dump/blocks/watermark. Also the resize
// path's read (Session.resize). Read fresh from the terminal, not from the last
// observation: a resize reflows under the same lock without feeding a chunk.
//
// The bool says whether this feeder holds ANY placement, not whether the
// returned set is non-empty: held-but-empty (a reflow dropped the last one)
// tells a client to stop drawing. Only an unheld set skips the terminal, which
// keeps imageless sessions off cgo. The stored set is deliberately left alone —
// it is the left side of the next diff.
func (f *wireFeeder) snapshotPlacements() ([]ghosttyvt.KittyPlacement, bool) {
	if len(f.placements) == 0 {
		return nil, false
	}
	return f.readPlacements(), true
}

// close frees the native refs the block table holds. Called from closePTY
// before the terminal itself is closed.
func (f *wireFeeder) close() {
	f.blocks.close()
}

// trackedRows resolves where the cursor's cell ended up (anchor) and where the
// cursor is now (landed), in rows from the top of retained history. Both are
// read AFTER the write so they share one coordinate frame — scrollback pruning
// can shift the frame between reads — which is what makes their difference
// meaningful on both screens.
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

// diffKittyPlacements reports what changed between two observations. Placements
// are compared whole: all scalar fields, and a field-naming rule would rot on a
// pin bump.
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
