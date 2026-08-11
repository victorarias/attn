Outer half of the worker's PTY feed path: kittyseg.go cuts the stream, this
file routes it. The terminal gets plain runs, kitty APCs, and OSC 133
markers; the wire gets the same stream with each APC replaced in position by
an ST plus the OBSERVED scroll/cursor effect — never predicted. When an
observation cannot answer, the session forces a snapshot re-push instead of
guessing. All of it happens under the caller's replayMu, in the same critical
section that advances the seq watermark, which is what keeps attach
snapshots consistent. ATTN_KITTY_STORAGE_LIMIT=0 turns the protocol off.
Design: docs/plans/2026-08-02-terminal-kitty-images.md.

Resync reasons. They travel to the client as the pty_desync reason and land
in the daemon log; each names the observation that failed.

kittyResyncAnchorLost: the cursor's pre-APC cell could not be resolved
afterwards, so how far the grid moved is unknowable.

kittyResyncAnchorClamped: the scroll pushed the anchor to the top of
retained history (ghostty clamps a discarded ref), where a cell that fell
off is indistinguishable from one that stopped there. Reached by an image
taller than the alternate screen, which keeps no history.

kittyResyncReverseScroll: the grid moved DOWN under the cursor, which no
placement does and synthesis cannot express.

kittyResyncUndescribedImage: kitty state moved on bytes that went to the
wire verbatim (an APC the segmenter could not cut out — see kittyseg.go)
and the diff found a placement created, re-placed, or retransmitted. The
worker's grid moved and the client's did not; only a snapshot settles it.

kittyResyncStampWithoutDelta: verbatim bytes moved ghostty's kitty stamp
and the diff found NOTHING — a placement created and destroyed inside one
chunk. Whatever it scrolled has no witness but the stamp.

kittyResyncMarginMode: DECLRMM (DEC mode 69) was on. A margin-box scroll
moves text without moving rows, so the tracked pair reads no scroll and no
SU goes out. Measured: margins `\x1b[4;14s` + placement at the box bottom
climbs the worker's text two rows while the client's stays put. Fires on
every described dispatch while margins are on — a tripwire, not a repair;
no emitter in the A4 sweep enables DECLRMM.

kittyResyncScrollClamped: the placement scrolled further than one SU can
express (ghostty clamps SU to the scroll region height), so the client's
history would come out short. Reachable because kitty's `r=` lets a 2x2
image claim any number of rows.

kittyResyncPendingWrap: the cursor sat in the LAST COLUMN, where a
dispatch may consume a pending-wrap bit CursorPos cannot see. Measured:
print a screen's width, place an image, print one more character — the
worker stays on row 0, the client wraps to row 1. Fires on the column
itself; every emitter in the A4 sweep positions the cursor first.

kittyPlacementKey identifies a placement across observations: kitty's image
id plus placement id, the only pair ghostty keeps stable.

kittyPlacementDelta is what one observation found changed in the active
screen's placement set. Updated carries placements whose fields moved for ANY
reason, a scrolled viewport position included.

wireFeeder splits PTY output into plain runs and kitty APCs, feeds all of it
to the terminal, and returns what the wire should carry instead. Every method
is called under replayMu, like blockFeeder's.

wire is the assembly buffer for a rewritten chunk, reused across calls;
the slice feed hands out is valid only until the next feed.

generation is ghostty's kitty stamp as of the last change this feeder
ACCOUNTED for; a difference against the terminal's own stamp is exactly
the undescribed kind, read in settleUnaccounted. Raw — the epoch is never
folded into this internal change detector.

epoch is the offset folded into every generation handed out; must match
Session.kittyEpoch. See mintKittyEpoch.

placements is the set as of the last observation, the left side of the
next diff.

deltas holds what the MOST RECENT feed's observations found (bounded by
one chunk). Never handed out: unaccountedResync reads its tail,
changedPlacements reads its emptiness.

resync names the observation that failed during this feed, "" when none.

kittyLimit is the cap this terminal was BUILT with, carried so the log
names the number in force even if the environment moved after spawn.

pending is the transmission being assembled across m=1 escapes, so a
refusal is judged once, at completion. Zero between transmissions.

newWireFeeder wires the feed path for a session's ghostty terminal. Returns
nil when the terminal is absent, exactly like newBlockFeeder: callers
nil-guard, and a session without a terminal fans out raw bytes unchanged.
epoch must be the same value the caller holds on the Session (mintKittyEpoch).

feed writes one PTY chunk into the terminal and returns the bytes the wire
should carry, plus a resync reason when the chunk's grid effect could not be
expressed (see Session.forceResync). Caller holds replayMu.

The returned slice is the INPUT slice itself when no rewriting was needed —
the common path allocates and copies nothing — otherwise the feeder's
assembly buffer, valid until the next feed. An empty result means the whole
chunk was held (an unterminated APC); the caller skips the fan-out, and
downstream dedup (`seq > last_seq`) tolerates the missing seq.

whole marks the passthrough case: a plain run that IS the input slice.
Every other emitted slice aliases a buffer the segmenter may rewrite
before Feed returns, so it is copied on the spot.

Write before mark() so the block-table pin lands on Ghostty's
post-marker cursor.

Settle whatever the chunk's last bytes did to kitty state against what
the wire carried for them.

A live placement moves on bytes that touch no kitty state (a scroll), and
the stamp does not move with it — so re-observe at the end of any chunk
that could have moved one, or a described position runs behind its grid.
The gate is a slice length: with no placements this costs one comparison
and never crosses into cgo.

wireST is ST in its 7-bit form (ESC backslash) — the substitute written
wherever the two streams differ. Always 7-bit even when the APC ended in C1
ST: a raw 0x9c on the wire is a stray UTF-8 continuation byte to the client,
not an ST.

The rule every extraction here obeys: wherever the two streams differ, BOTH
sides get an ESC-led no-op at that position. Ground also holds a UTF-8
decoder that may be mid-character; an ESC ends that decode, so whichever side
loses bytes must be given an ESC in their place or the two decoders resolve
the same character differently.

writeAPC feeds one complete kitty APC to the terminal and appends whatever
the wire needs in its place. Ordering is the contract: end the pending decode
on both sides BEFORE anything is measured, then pin the cursor before the
write so a tracked ref can report how far the grid moved.

Settle earlier bytes first: an undescribed kitty escape ahead of this APC
leaves a stamp move the claim below would otherwise absorb.

The abort, to both sides, ahead of every measurement. On the WIRE it
stands in for the APC's leading ESC. On the WORKER it is not redundant:
ending a decode is a GRID event (a replacement character on the last
column commits the deferred wrap), and doing it before the pin keeps the
measured window holding only what the image did. Safe unconditionally:
from ground ghostty treats ST as a no-op —
TestWireFeedPreSTOnlyEndsTheDecode pins that.

Claimed here, once: every branch below has either described the dispatch
or resynced over it, so no later settle may see it again.

Unchanged generation means no placement appeared, so nothing scrolled on
an image's behalf — only then is the viewport cursor enough to decide.
This is the shipping configuration's every APC.

The tracked pair reports cursor movement relative to CONTENT; taking the
viewport movement back out leaves the scroll. The identity holds on both
screens and inside a scroll region.

On the alternate screen an anchor at row 0 only means the pin was CLAMPED
there (no history), and the scroll amount is unrecoverable. Measured: a
placement that fits and one that scrolls the top row away both report
(anchor 0, scrolled 0). On the primary the pin follows its cell into
history; losing it there reads as anchor-lost above.

One SU carries at most a screen's worth of rows (ghostty clamps to the
scroll region). Receipt: TestWireFeedSynthesizesTheLargestScrollOneSUCarries
pins both sides of the boundary on 8- and 12-row screens.

Margin-confined scroll is movement this measurement cannot see. The
cursor moves are still trustworthy (they agreed in every measured margin
case), so the dispatch is described in full and the resync repairs the
text — a resync is never a stop order.

Same shape: state changed that the measurement cannot read. Sits after
the early return on measurement — a dispatch that changed nothing (query,
delete of an absent id) needs no resync even in the last column.

All moves are RELATIVE, on both axes: absolute addressing is measured
from a frame the worker cannot see (origin mode for rows, DECLRMM's left
margin for columns). A relative step is the same step in every frame. SU
leaves the cursor's viewport position alone.

kittyTransmission is a transmission being assembled: kitty splits a large
image across several escapes, each carrying `m=1` until the last, and nothing
is stored until that last one lands.

ask is the storage the image will occupy once decoded, from the declared
geometry. Zero when none was declared (f=100 PNG); payload stands in.

noteTransmission logs the one failure ghostty cannot report: an image refused
for exceeding the storage limit. Every measured emitter sends q=2, which
suppresses kitty's own response, so the worker is the only witness.

stored says whether ghostty's kitty generation moved on this escape, which is
the whole signal. Measured:

single transmission that fits      generation moves
single transmission over the limit  UNCHANGED — the one true positive
chunked (m=1 …) that fits           unchanged on every intermediate escape,
moves only on the completing m=0
chunked over the limit              unchanged throughout, m=0 included
a=q query                           unchanged — never a transmission
a=p re-place, a=d delete of a live id   moves
a=d delete of an id that is not there   unchanged — never a transmission
eviction (a third image into a store that holds one)   moves

So intermediate escapes accumulate and are never judged, and eviction is
invisible here because admitting the new image moves the stamp.

parseKittyTransmission reads the keys a refusal check needs out of one
complete APC. Deliberately not a kitty parser: it reads what it recognizes
and treats the rest as absent — the worst a misread does is drop a log line.
kitty's default action is `t`, so an escape with no `a=` is a transmission,
which is what a continuation escape looks like.

Ghostty stores decoded RGBA whatever the wire format was, so declared
pixels are the honest ask.

appendCSI writes `ESC [ n <final>`, or nothing when n is zero — every
sequence synthesis uses is a no-op at zero.

placementReadHook is a test-only seam fired on every read of the placement
set, so a test can assert a session with no images never reaches ghostty for
placements. nil in production.

readPlacements is the only place the placement set is read out of ghostty,
and therefore the one fold of the epoch on the placement side — every
placement exit (live fan-out, resize re-describe, attach snapshot) draws from
here. The set is freshly copied per call. Callers hold replayMu.

observe diffs ghostty's placement set against the last observation. Called
when the generation stamp moved, and at the end of any chunk that could have
moved a placement the terminal does not consider changed (see feed).

settleUnaccounted closes the books on kitty state changes no writeAPC
dispatch accounted for, and reports whether there were any. Called at the end
of every feed AND at the entry of every writeAPC — the second is what keeps
the claim honest: without it, a described APC silently absorbs an undescribed
stamp move earlier in the same chunk. After it returns, f.generation IS the
terminal's current stamp.

Judge only the observations THIS settle records; earlier deltas were
already described or resynced over.

unaccountedResync names what a generation move on verbatim bytes costs the
client; false when it costs nothing. A resync exists for grid SCROLL the wire
never expressed, not for knowledge of the set (that fans out on its own).
Only creating or moving a placement can scroll, and retiring one moves
nothing — so a removals-only delta is exempt; everything else resyncs:
no delta at all (a placement born and dead inside one chunk, witnessed only
by the stamp), Added, or Updated (re-placed or retransmitted — charged
together because this check cannot tell them apart). Ordinary scrolls cannot
reach here: they move placements without moving the stamp.

changedPlacements reports the active screen's whole placement set when this
feed moved it, and nothing when it did not. No copy needed: observe REPLACES
the set rather than mutating it.

snapshotBlocks resolves the block table under the caller's replayMu — the
same hold that serializes the VT dump and reads the seq watermark.

snapshotPlacements resolves the active screen's placement set under the
caller's replayMu, same hold as the dump/blocks/watermark. Also the resize
path's read (Session.resize). Read fresh from the terminal, not from the last
observation: a resize reflows under the same lock without feeding a chunk.

The bool says whether this feeder holds ANY placement, not whether the
returned set is non-empty: held-but-empty (a reflow dropped the last one)
tells a client to stop drawing. Only an unheld set skips the terminal, which
keeps imageless sessions off cgo. The stored set is deliberately left alone —
it is the left side of the next diff.

close frees the native refs the block table holds. Called from closePTY
before the terminal itself is closed.

trackedRows resolves where the cursor's cell ended up (anchor) and where the
cursor is now (landed), in rows from the top of retained history. Both are
read AFTER the write so they share one coordinate frame — scrollback pruning
can shift the frame between reads — which is what makes their difference
meaningful on both screens.

diffKittyPlacements reports what changed between two observations. Placements
are compared whole: all scalar fields, and a field-naming rule would rot on a
pin bump.
