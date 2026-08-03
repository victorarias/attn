package pty

// Feed composition: the outer half of the worker's PTY feed path.
//
// kittyseg.go owns the framing — one machine that tracks where ghostty's parser
// stands and cuts the stream into plain runs, complete kitty APCs, and complete
// OSC 133 markers. This file decides where each of those goes, turning one
// arriving chunk into two different things:
//
//   - the TERMINAL feed: the plain runs and every complete kitty APC, because
//     ghostty is the system's only kitty parser. OSC 133 markers are WITHHELD —
//     they are not grid-inert in the ghostty the worker links — and an ST goes
//     in their place, with blockfeed.go pinning a block-table entry there.
//   - the WIRE bytes: the same stream with each APC replaced, in position, by
//     bytes that leave a kitty-ignorant terminal on the same grid — an ST, then
//     the scroll and the cursor the placement caused. In the shipping
//     configuration the ST is all of it. Markers go out untouched; the client
//     parses its own.
//
// So each stream is missing something the other has, and in both directions the
// gap is filled with an ST. That is one rule, not two accidents: an ESC ends a
// part-built UTF-8 character, so a side that loses the bytes must be given an
// ESC in their place or the two decoders resolve the same character differently.
// The APC also needs its ST on the WORKER, ahead of everything measured, because
// ending a decode is itself a grid event. See wireST and writeAPC.
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
// Live by default: a session's terminal is built with kittyStorageLimitDefault,
// so this path runs for real and its synthesis is what keeps the two grids
// equal. ATTN_KITTY_STORAGE_LIMIT=0 turns the protocol off again — ghostty then
// refuses every transmission, the generation stamp never moves, and the only
// visible effect is that APC bytes are dropped from the wire instead of being
// sent to a client that cannot parse them.
// Design: docs/plans/2026-08-02-terminal-kitty-images.md.

import (
	"bytes"
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
	// went to the wire verbatim rather than through synthesis, and the diff
	// found a placement created, re-placed, or retransmitted. That happens when
	// an APC is one ghostty parses as kitty but the segmenter cannot cut out —
	// an APC introduced from inside another sequence, whose leading ESC is also
	// that sequence's exit (see kittyseg.go). Replaying those bytes keeps the
	// two PARSERS in step, which is what the segmenter guarantees, but the
	// client cannot parse kitty, so the image the worker just placed moved its
	// grid and not the client's. Only a snapshot can settle that.
	kittyResyncUndescribedImage = "kitty_undescribed_image"
	// kittyResyncStampWithoutDelta: the same verbatim bytes moved ghostty's
	// kitty stamp and the diff that followed found NOTHING — the set before and
	// the set after are equal. That is a placement created and destroyed inside
	// one chunk, which an emitter reaches by drawing an image and clearing it in
	// one 4 KiB PTY read. Whatever it scrolled is on the worker's grid, no wire
	// byte described it, and the two sets being equal means no observation can
	// ever name what moved. The stamp is the only witness there is.
	kittyResyncStampWithoutDelta = "kitty_stamp_without_delta"
	// kittyResyncMarginMode: DECLRMM (DEC private mode 69) was on when a
	// described dispatch landed. A scroll confined to the left/right margin box
	// moves the text inside those columns without moving the rows, and the
	// tracked pair below measures rows — so a margin-box scroll reads as no
	// scroll at all and the wire carries no SU for it. Measured: with margins
	// `\x1b[4;14s` and a placement at the bottom of the box, the worker's text
	// climbs two rows and the client's stays put under a cursor that agrees.
	// This is a tripwire, not a repair: it fires on every described dispatch
	// while margins are on, whether or not that dispatch actually scrolled the
	// box, because the measurement cannot tell those apart. Nothing pays for it
	// in practice — no emitter in the A4 sweep enables DECLRMM.
	kittyResyncMarginMode = "kitty_layout_margin_mode"
	// kittyResyncScrollClamped: the placement scrolled the grid further than one
	// SU can express. ghostty clamps SU to the height of the scroll region, so a
	// taller scroll would push the client's oldest rows nowhere and leave its
	// history short of the worker's while the viewport still agreed. Reachable
	// because kitty's `r=` lets a 2x2 image claim any number of rows.
	kittyResyncScrollClamped = "kitty_layout_scroll_clamped"
)

// kittyPlacementKey identifies a placement across observations: kitty's own
// image id plus placement id, which is the only pair ghostty keeps stable.
type kittyPlacementKey struct {
	ImageID     uint32
	PlacementID uint32
}

// kittyPlacementDelta is what one observation found changed in the active
// screen's placement set. It answers two questions and neither is the wire's:
// whether bytes the wire could not describe did anything but RETIRE a
// placement (a resync — see unaccountedResync), and whether anything at all
// moved (an update for the client, which carries the whole set rather than
// this).
//
// Updated carries placements whose fields moved for ANY reason, a viewport
// position that changed because the screen scrolled included.
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
	// describes it on the wire, or is one the wire cannot describe — and a
	// difference between this and the terminal's own stamp is exactly the
	// second kind. settleUnaccounted is where that difference is read, and it
	// runs both at the end of a feed and before every described dispatch, so
	// the two kinds can never be folded into one number.
	//
	// Raw, and deliberately: this is an internal change detector that never
	// leaves the process, so folding the epoch into it would buy nothing and
	// invite the two to be confused.
	generation uint64

	// epoch is the terminal-instance offset folded into every generation this
	// feeder hands out; see mintKittyEpoch. Session.kittyEpoch carries the same
	// value for the image half, and the two must match.
	epoch uint64

	// placements is the placement set as of the last observation, the left side
	// of the next diff.
	placements []ghosttyvt.KittyPlacement
	// deltas holds what the observations in the MOST RECENT feed call found, so
	// it stays bounded by one chunk. It is never handed out: unaccountedResync
	// reads the tail of it for the resync decision, and changedPlacements()
	// reads its emptiness as the whole test for "this chunk moved something".
	deltas []kittyPlacementDelta

	// resync names the observation that failed during this feed, "" when none.
	resync string

	// logf reports a refused transmission, and nothing else. nil in tests that
	// do not care.
	logf LogFunc

	// kittyLimit is the storage cap this session's terminal was BUILT with,
	// carried rather than re-read so the log names the number in force here
	// even if the environment moved after the spawn.
	kittyLimit uint64

	// pending is the transmission being assembled across m=1 escapes, so a
	// refusal is judged once, where a completed transmission should have
	// stored. Zero between transmissions.
	pending kittyTransmission
}

// newWireFeeder wires the feed path for a session's ghostty terminal. Returns
// nil when the terminal is absent, exactly like newBlockFeeder: callers
// nil-guard, and a session without a terminal fans out its raw bytes unchanged.
//
// epoch is the session's kitty identity offset (mintKittyEpoch), which the
// caller must also hold on the Session so the image serve folds the same one.
// kittyLimit is the cap the terminal was built with, for the refusal log.
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
			//
			// Withholding the bytes is what keeps the two grids equal, and it
			// is load-bearing rather than incidental. A marker is NOT inert in
			// the native ghostty the worker links: measured, `OSC 133;A` with
			// the cursor mid-line breaks the line, because a prompt starts on a
			// fresh one. The wasm ghostty the app renders is a different pin and
			// does not — and it is fed the marker bytes, unfiltered, by
			// GhosttyTerminal; it simply does not act on them. So writing
			// markers to the worker terminal would move the worker's grid and
			// not the client's, the exact divergence this file exists to
			// prevent. The corpus entry "a prompt marker after output with no
			// trailing newline" is the witness, and is tripwired against a wasm
			// pin bump: see the pin-skew note in the plan.
			//
			// But the marker's own ESC is not grid-inert on either side: it ends
			// a part-built character. The client gets that ESC and the worker
			// does not, so a marker arriving mid-character left the worker
			// holding a decode the client had already resolved — one extra cell
			// on the client, permanently. This is the same leak as the APC's,
			// running the other way, so it takes the same substitute: an ST to
			// the WORKER in the marker's place.
			//
			// Written before mark(), so the pin the block table takes lands on
			// the cursor as it is AFTER the abort — the cell the marker actually
			// refers to. Pinning first would record the pre-abort cell and put
			// the block on the wrong row at the wrap column.
			f.wire = append(f.wire, seg.Bytes...)
			f.blocks.write(wireST)
			f.blocks.mark(seg.Marker)
		}
		first = false
	})

	// Whatever the chunk's last bytes did to the terminal's kitty state, settled
	// against what the wire carried for them. One cheap read per chunk buys the
	// guarantee that no image ever lands on the worker's grid alone.
	settled := f.settleUnaccounted()

	// A live placement moves on bytes that touch no kitty state at all — a
	// scroll is the common one — and ghostty's stamp does not move with it, so
	// the check above cannot see it. Re-reading at the end of every chunk that
	// could have moved one is what keeps a described position from running a
	// chunk (or a screenful) behind the grid it refers to. It is also why an
	// observation the APC's own dispatch already took is not enough on its own:
	// plain bytes after the APC, in the same chunk, scroll what it just placed.
	//
	// The gate is a slice length, not a terminal read. With no placements —
	// every chunk of every session while the feature is dark — this costs one
	// comparison and never crosses into cgo, and nothing except a placement's
	// own dispatch can create the first one.
	if !settled && len(f.placements) > 0 {
		f.observe()
	}

	if whole {
		return data, f.resync
	}
	return f.wire, f.resync
}

// wireST is the ESC-led no-op this file substitutes wherever the two streams
// differ: ST in its 7-bit form, ESC then backslash.
//
// Always the 7-bit form, even when the APC the worker consumed was terminated by
// C1 ST (0x9c). A raw 0x9c on the wire is not an ST to the client — in ground
// the stream is UTF-8, where 0x9c is a stray continuation byte that decodes
// toward U+FFFD and puts a cell on the grid. The two-byte form is the only one
// that means "no-op" on both sides.
//
// The rule it serves, which every extraction in this file obeys:
//
//	Wherever the two streams differ, BOTH sides get an ESC-led no-op at that
//	position, so both parsers cross every extraction point in the same state.
//
// Extracting only from ground keeps the two VT PARSERS in step, and that is the
// property the segmenter was built to give. It is not the whole of the state
// ground implies: ground also holds a UTF-8 decoder, which may be part-way
// through a character. An ESC ends that decode. So whichever side loses the
// bytes must be given an ESC in their place, or the two decoders resolve the
// same character differently and nothing later heals it.
//
// Both directions occur. A kitty APC reaches the worker and not the wire, so the
// WIRE gets one (see writeAPC). An OSC 133 marker reaches the wire and not the
// worker, so the WORKER gets one (see feed). The APC case needs it on the worker
// too, for a reason that is about measurement rather than decoding: see writeAPC.
var wireST = []byte{0x1b, '\\'}

// writeAPC feeds one complete kitty APC to the terminal and appends whatever
// the wire needs in its place. The ordering is the contract, and it has two
// halves: end the pending decode on both sides BEFORE anything is measured, then
// pin the cursor before the write, because a tracked ref is the only way to see
// afterwards how far the grid moved under it.
func (f *wireFeeder) writeAPC(apc []byte) {
	// Settle the chunk's earlier bytes before anything here is measured or
	// claimed. Plain bytes ahead of this APC can carry a kitty escape ghostty
	// dispatches and the segmenter could not extract; the stamp move that
	// leaves is this feeder's only record of it, and the claim at the end of
	// this function would take it as its own. See settleUnaccounted.
	f.settleUnaccounted()

	// The abort, given to both sides, ahead of every measurement below.
	//
	// On the WIRE it stands in for the APC's own leading ESC, which the client
	// would otherwise never see: it would keep holding a part-built character,
	// and a following continuation byte would complete a different one from the
	// replacement character the worker already resolved.
	//
	// On the WORKER it looks redundant — the APC's ESC would end the decode a
	// moment later anyway — and it is not, because ending a decode is a GRID
	// event. It writes a replacement character, and on the last column that
	// character commits the deferred wrap and moves the cursor to the next row.
	// Pinned before the abort, that movement falls inside the measured window and
	// is attributed to the image; the client then performs the same abort itself
	// off the ESC above AND applies the synthesized movement, landing a row too
	// far. Doing the abort here, before the pin, leaves the measured window
	// holding only what the image did. Both sides then start the APC from the
	// same state, so measured movement is movement the client still needs.
	//
	// Safe unconditionally: the segmenter extracts only from ground, and from
	// ground ghostty treats ST as a no-op, so on a stream with nothing pending
	// this writes no cell and moves nothing. TestWireFeedPreSTOnlyEndsTheDecode
	// pins that.
	f.term.Write(wireST)
	f.wire = append(f.wire, wireST...)

	// The settle above left f.generation equal to the terminal's stamp, so this
	// is the pre-dispatch generation without a second crossing into ghostty.
	generation := f.generation
	col, row := f.term.CursorPos()
	before := f.term.TrackCursor()

	f.term.Write(apc)

	stamped := f.term.KittyGeneration()
	// Claimed here rather than at each exit below: every branch from this point
	// on has either described the dispatch or resynced over it, so no later
	// settle may see it again. The settle at entry is what makes the claim
	// honest — it covers this dispatch's move and nothing that ran before it.
	f.generation = stamped
	f.noteTransmission(apc, stamped != generation)
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

	// One SU carries at most a screen's worth of rows into history: ghostty
	// clamps it to the scroll region, and a region is never taller than the
	// screen. Past that the client's history comes out short by exactly the
	// overflow while its viewport still agrees, so the tripwire is set at the
	// largest scroll a single SU reproduces. Receipt:
	// TestWireFeedSynthesizesTheLargestScrollOneSUCarries, which pins both sides
	// of the boundary on an 8-row and a 12-row screen.
	if _, screenRows := f.term.Size(); scrolled > screenRows {
		f.failResync(kittyResyncScrollClamped)
		return
	}

	// A margin-confined scroll is movement this measurement cannot see, so while
	// DECLRMM is on the numbers below are not trustworthy on their own. The
	// cursor moves still are — they agreed in every measured margin case — so the
	// dispatch is described in full and the resync repairs the text: a resync is
	// never a stop order, and a client that has not re-attached yet is better off
	// with its cursor where the worker's is.
	if f.term.LeftRightMarginMode() {
		f.failResync(kittyResyncMarginMode)
	}

	// SU scrolls the active scroll region and leaves the cursor's viewport
	// position alone, so the moves that follow are plain relative steps from
	// where the pre-APC bytes already left the client's cursor.
	//
	// Every one of them is relative, and on both axes, for the same reason:
	// absolute addressing is measured from a frame the worker cannot see. A row
	// (CUP, VPA) is counted from the scroll region under origin mode; a column
	// (CHA) is counted from the LEFT MARGIN when DECLRMM is on (`\x1b[?69h`), so
	// an absolute column measured at the screen edge lands somewhere else on a
	// client with margins set. The worker reports viewport coordinates and has
	// no business knowing which modes the program turned on — a relative step is
	// the same step in every frame.
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
	// ask is the storage the image will occupy once decoded, from the geometry
	// the first escape declared. Zero when it declared none (f=100, a PNG whose
	// decoded size only ghostty knows), and then payload stands in.
	ask uint64
	// payload counts the base64 bytes seen across every escape so far.
	payload uint64
	// open is set while an escape has promised more to come.
	open bool
}

// noteTransmission logs the one failure ghostty has no way to report: an image
// refused for exceeding the storage limit. Every emitter measured in the A4
// sweep transmits with `q=2`, which suppresses kitty's own response, so the
// program is not told and neither is anyone else — the image simply never
// appears. The worker is the only place that can see it, which is why this file
// interprets an APC here and nowhere else.
//
// stored says whether ghostty's kitty generation moved on this escape, which is
// the whole signal. Measured, and every row of it is a case this must not
// mistake for a refusal:
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
// So intermediate escapes are accumulated and never judged, and eviction — the
// ordinary way an animation reuses its budget — is invisible here because
// admitting the new image moves the stamp like any other store.
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
		f.pending.open = true
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

// parseKittyTransmission reads the four keys a refusal check needs out of one
// complete APC — the action, `m`, and the declared geometry — plus the payload
// length. It reports ok only for a transmission: kitty's default action is `t`,
// so an escape with no `a=` at all is one, which is exactly what a continuation
// escape looks like.
//
// Deliberately not a kitty parser. It reads what it recognizes and treats the
// rest as absent, because the worst a misread can do here is drop a log line.
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
		// Base64 in, raw bytes out: 4 encoded characters carry 3.
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
	// pixels are the honest ask; a format that declares none (PNG) leaves this
	// zero and the payload stands in.
	t.ask = width * height * 4
	return t, more, true
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

// placementReadHook is a test-only seam fired on every read of the placement
// set, so a test can hold both the feed path and the resize path to their cost:
// a session with no images must never reach ghostty for placements at all. nil
// (a single branch) in production.
var placementReadHook func()

// readPlacements is the only place the placement set is read out of ghostty —
// the cgo crossing every caller here is gated to avoid. Callers hold replayMu.
//
// Being the only read is also what makes it the only place a generation crosses
// out of ghostty's process-local numbering into the session's: every placement
// exit (the live fan-out, the resize re-describe, the attach snapshot) draws
// from here, so one fold covers all three. The set is freshly copied out per
// call, so stamping it mutates nothing shared, and the offset is constant, so
// the diff against the last observation is untouched.
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
// when the generation stamp moved — the terminal's own statement that the set
// or its images changed — and at the end of any chunk that could have moved a
// placement the terminal does not consider changed at all (see feed).
func (f *wireFeeder) observe() {
	current := f.readPlacements()
	delta := diffKittyPlacements(f.placements, current)
	f.placements = current
	if !delta.empty() {
		f.deltas = append(f.deltas, delta)
	}
}

// settleUnaccounted closes the books on everything the terminal's kitty state
// has done that no dispatch through writeAPC accounted for, and reports whether
// there was any. Whatever it finds happened on bytes the wire carried verbatim,
// and the client ignores those, so the cost is decided by unaccountedResync.
//
// Called at two moments, and the pair is the whole accounting rule: at the end
// of every feed, and at the ENTRY of every writeAPC, before that dispatch is
// measured. The second is what keeps the claim honest. writeAPC ends by taking
// the terminal's stamp as its own, and a stamp is a single number for the whole
// terminal — so without a settle first, a described APC silently absorbs an
// undescribed one that ran earlier in the same chunk, and the end-of-feed check
// finds a stamp that already looks accounted for. Settling first means writeAPC
// can only ever claim the move it made itself.
//
// After it returns, f.generation IS the terminal's current stamp, which is why
// writeAPC reads its pre-dispatch generation from the field rather than from
// ghostty: the settle already paid for that crossing.
func (f *wireFeeder) settleUnaccounted() bool {
	stamped := f.term.KittyGeneration()
	if stamped == f.generation {
		return false
	}
	f.generation = stamped

	// Scoped to the observations THIS settle records: the deltas already in the
	// slice belong to dispatches that were described on the wire or resynced
	// over at the time, and must not be judged a second time.
	before := len(f.deltas)
	f.observe()
	if reason, ok := unaccountedResync(f.deltas[before:]); ok {
		f.failResync(reason)
	}
	return true
}

// unaccountedResync names what a generation move on bytes the wire carried
// VERBATIM costs the client, given the observations that move produced. Reports
// false when it costs nothing.
//
// The rule rests on one distinction: a resync exists for grid SCROLL the wire
// never expressed, never for knowledge of the placement set. The set reaches
// the client on its own, through changedPlacements' fan-out, whatever happened
// here. And only bringing a placement into existence or putting a live one
// somewhere new can scroll the grid — retiring one moves nothing, because
// ghostty does not give back the rows an image took. So the exemption is
// exactly a delta that is nothing but removals, and everything else resyncs:
//
//   - no delta at all, with the stamp moved: a placement that appeared and died
//     inside this chunk. It left the before and after sets equal while its
//     scroll stayed on the worker's grid, so nothing but the stamp can see it.
//   - Added: a placement the wire never described came into existence.
//   - Updated: a live {ImageID, PlacementID} put at a new spot, or an image
//     retransmitted under one (ImageGeneration moves, the key does not). The
//     first can scroll; the second is charged with it because this check cannot
//     tell them apart, and an undescribed APC is rare enough to pay for that.
//
// Scroll noise cannot reach here: an ordinary scroll moves every live placement
// but leaves ghostty's stamp alone, so this runs only when the terminal itself
// says its kitty state changed on bytes nothing accounted for.
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

// failResync records the first observation failure of this chunk. The APC's
// grid effect is already in the terminal; the wire gets nothing for it, and the
// snapshot the client re-attaches for carries the truth.
func (f *wireFeeder) failResync(reason string) {
	if f.resync == "" {
		f.resync = reason
	}
}

// changedPlacements reports the active screen's whole placement set when this
// feed moved it — membership, geometry, or position — and nothing when it did
// not. The caller stamps the set with the chunk's seq (Session.readLoop) and
// hands it to the client.
//
// The returned slice needs no copy: observe REPLACES the set rather than
// mutating it, so what is handed out stays the truth of the moment it
// described.
func (f *wireFeeder) changedPlacements() ([]ghosttyvt.KittyPlacement, bool) {
	if len(f.deltas) == 0 {
		return nil, false
	}
	return f.placements, true
}

// snapshotBlocks resolves the block table under the caller's replayMu — the
// SAME hold that serializes the VT dump and reads the seq watermark.
func (f *wireFeeder) snapshotBlocks() []AttachBlockData {
	return f.blocks.snapshotBlocks()
}

// snapshotPlacements resolves the active screen's placement set under the
// caller's replayMu — the same hold as the dump, the blocks, and the watermark,
// so an attaching client gets one consistent picture rather than four readings
// of a moving terminal. Also the resize path's read (Session.resize).
//
// Read fresh from the terminal rather than reused from the last observation: a
// resize reflows the grid under that same lock without feeding a chunk, so the
// stored positions can be one reflow stale.
//
// The bool says whether this feeder holds any placement, which is NOT the same
// as the returned set being non-empty: a reflow can drop the last placement, and
// that reads as held-but-empty — the one thing that tells a client to stop
// drawing. Only an unheld set skips the terminal, which is what keeps a session
// without images off cgo here too.
//
// The stored set is deliberately left alone. It is the left side of the next
// diff, and writing it from outside feed() would let a reflow absorb a mutation
// the end-of-feed check has to see.
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
