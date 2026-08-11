TerminalTheme carries the frontend's resolved terminal colors as "#rrggbb"
hex strings. Zero-value fields fall back to built-in dark defaults.

Default OSC 10/11/12 colors, used for any TerminalTheme field that is empty
or fails hex validation. These match the frontend's built-in dark theme.

infoSnapshotHook is a test-only seam invoked inside info() after the ghostty
snapshot is serialized but before the attach sequence watermark (LastSeq) is
read. It is nil in production. Tests set it to inject PTY writes into that
window to deterministically reproduce the snapshot/watermark consistency race.

readLoopSeqGapHook is a test-only seam invoked in the read loop after a
chunk's sequence number is allocated but before the chunk is applied to
replay/screen state under replayMu. It is nil in production. Tests set it
to take snapshots inside that gap and deterministically verify the
snapshot's watermark never claims chunks its screen does not contain.

onPlacements receives the kitty placement set whenever a chunk moved it,
interleaved with this subscriber's own byte stream. nil for subscribers
that do not draw (the debug capture, the orphan-activity probe) and for
every subscriber of a session that never stores an image. See OnPlacements.

osc10/osc11/osc12 count OCCURRENCES in the chunk, not presence — a chunk
containing three OSC 11 queries (e.g. a TUI probing color support) must
get three replies, or the caller under-answers and the program hangs
waiting for a reply that already went out for an earlier query. Derived
from oscQueryOrder below.

oscQueryOrder lists the OSC color codes (10/11/12) queried in this
chunk in the order they appeared. Real terminals answer OSC queries in
ask order, and a client that writes a burst of mixed OSC10/11/12
queries and pairs replies positionally depends on that order — a
fixed-order reply (e.g. all OSC10 first) would mispair against it.

da1BeforeCPR records that the chunk asked DA1 before CPR. Query-driven
programs read replies sequentially, so the daemon answers in ask order.

Cell size in device pixels, derived from the total pane pixels a client
last reported. Zero until some client reports geometry — a session spawns
pixel-less and stays that way until the first fit. It is the cell size
rather than the total that is remembered, because the total only means
anything paired with the grid it was measured at: a later pixel-less
resize re-derives its total from these.

cleanup removes spawn-time resources that must outlive shell startup,
such as an isolated startup-file overlay for an interactive shell pane.

ghostty is the server-authoritative parsed terminal (libghostty-vt). It
backs approval-state detection, CPR replies, the grid/automation screen
snapshot (Manager.Snapshot), and attach restore (see info()). It answers
query responses during Write; the read loop forwards the responses the
scan-based responder does not cover (e.g. kitty CSI ? u) so the worker is
the complete query answerer and a snapshot-restored client can suppress
every response.

wireFeed owns writes into ghostty: it splits kitty APCs out of the
stream, runs everything else through the OSC 133 scanner that maintains
the worker-side command-block table (Phase 3a), and returns the bytes the
wire carries in place of what it fed. nil exactly when ghostty is nil;
every use is nil-guarded like ghostty's.

kittyEpoch is the offset folded into every kitty generation that leaves
this session, so a worker that replaces another under the same session id
can never mint an identity a client already holds pixels for. Set where
ghostty is constructed and never after; wireFeed holds the same value for
the placement half, this field serves the image half (kittyImage). See
mintKittyEpoch.

replayMu makes Ghostty feeds and lastReplaySeq atomic for snapshots, so a
re-attaching frontend never drops a chunk that landed between the payload
snapshot and the watermark read. Held briefly around each feed and around
snapshot serialization; fanOut stays outside it.

writeMu guards every ptmx access that is not a Read: writes, the resize
ioctl, and the close itself. os.File tolerates Read racing Close, but
Fd() (which the resize ioctl needs) does not — it reads the descriptor
while Close is destroying it. ptmxClosed makes the ordering explicit so a
late resize or query reply becomes a no-op instead of touching a dead fd.

themeMu guards theme, which seeds OSC 10/11/12 (fg/bg/cursor color)
replies. Set at spawn (SpawnOptions.Theme) and updated live via SetTheme;
read from the read loop on every OSC color query.

harnessSignals reads the agent's own OSC state signals off the PTY stream.
Read-only: it never alters the bytes.

shellSignals merges the foreground poll and the OSC 133 marker stream
into one heartbeat claim for shell panes; nil for every other agent.
Read-only on the stream: it never alters the bytes.

lastSignal is the most recent observation either observer emitted, kept so
the level can be *read* rather than only heard as it goes past. A level says
"this is still true", and a daemon that was not running when it was painted
has no other way to learn it: an agent parked at its prompt writes nothing to
the PTY, so a daemon restart would otherwise leave it with no evidence at all
until the user typed. Written by both emitters, read from the info RPC.

fanOutPlacements hands one placement update to every subscriber that asked
for placements.

Called from the read loop AFTER the chunk's bytes are fanned out, and with
replayMu released. The order is the contract: an update states where images
sit on the grid THOSE bytes produce, so a client that applied it first would
draw them against a screen it has not scrolled yet. The set itself was
captured inside the same critical section that stamped the seq, so what is
delivered here cannot have moved since.

forceResync drops every subscriber with reason, which reaches each client as
a pty_desync: the frontend resets its terminal and immediately re-attaches,
and that attach serves a fresh server-authoritative snapshot. It is the same
round trip an overflowing client buffer already takes, reused as the escape
hatch for a chunk whose grid effect the wire could not express (wireFeeder) —
re-syncing by construction beats guessing at bytes.

Must be called with replayMu released; the subscriber callbacks take their
own locks, exactly like fanOut.

PTY reads are coalesced before fan-out so sustained output (builds, logs,
`seq`-style floods) produces few large downstream messages instead of one
per read. macOS pty reads return tiny chunks under load (~100 bytes, the
tty queue's pacing), and every message costs real memory in the WebKit
frontend regardless of size — message count, not byte volume, is what
balloons the app during heavy output. Interactive traffic must not pay for
this: a read with nothing queued behind it is emitted immediately, so echo
latency is unchanged, and a flood batch is bounded by ptyCoalesceWindow.

nextCoalescedRead returns the next batch of PTY output, blocking for the
first read. If no further read is already queued the first one is returned
as-is — the interactive path adds zero latency. A queued read means the
producer is outpacing the pipeline, so reads are folded in until the batch
reaches maxBytes or the window elapses. The returned error belongs to the
last read folded into the batch; callers must not receive again after it.

The worker is the single, always-on responder for CPR, DA1, and
OSC 10/11/12 — race-free regardless of frontend attach/replay
timing, and unaffected by whether an interactive subscriber is
attached. CPR and DA1 are answered below from the screen model
/ a static capability string (AGENTS.md pattern #7). OSC 10/11/12
(fg/bg/cursor color) are answered here from the daemon-pushed
theme (see SetTheme); the frontend does not answer any of these.

Feed the server-authoritative terminal under the same lock
as the seq watermark so a snapshot stays atomic with it.
The feeder pins block positions at OSC 133 markers and
hands back the wire bytes for this chunk, which differ
from the raw ones only when it extracted a kitty APC.

Read the placement set in the same hold: it was measured on
the grid this chunk produced, and the seq below is what ties
it to those bytes for the client.

Drain ghostty's query responses AFTER the lock (the sink has
its own mutex) and forward the responses the scanner does not
cover (kitty CSI ? u, etc.) so the worker answers every query
and a snapshot-restored client can suppress all of them.

The daemon is the single authority for CPR (cursor position)
and DA1 (device attributes) replies. Answer after the chunk is
applied so the reported cursor is current, and reply in the
order the chunk asked (fish sends ESC[6n ESC[0c, but other
programs may ask DA1 first and read replies sequentially). fish
blocks its prompt redraw on the resize-triggered CPR+DA1 until it
gets both; routing them through the daemon makes the replies
race-free regardless of frontend attach/replay timing (the
frontend no longer answers either). See writeCursorPositionResponse
and writeDeviceAttributesResponse.

An empty wire chunk means the feeder is holding an
unterminated escape: there is nothing for this seq to carry,
and downstream dedup is `seq > last_seq`, which does not
require the numbers to be dense.

After the bytes, never before them: the set describes the grid
they produce.

The signal observers read the RAW chunk, not the wire: they
look for the agent's own OSC state signals, which the feeder
never touches.

attachSnapshotEnv gates server-authoritative snapshot attach. When set to "1"
the daemon serves a ghostty-serialized snapshot on attach and the worker
drainGhosttyResponses clears the ghostty terminal's accumulated query
responses and forwards the responses the scan-based responder does not cover
(kitty CSI ? u and any other non-CPR/DA1/OSC-color reports) to the PTY, so the
worker answers every query and a snapshot-restored client can suppress all
responses. Must be called after replayMu is released; the sink has its own lock.

The nil check and the drain are one critical section: teardown nils the
field under replayMu, so checking outside the lock would let a freed
terminal be drained.

stripScannerOwnedResponses removes, from a ghostty query-response stream, the
response classes the scan-based responder already emits — CPR (CSI … R), DA
(CSI … c), and OSC 10/11/12 color reports — so forwarding the remainder never
double-answers a query the scanner handles. Kitty keyboard reports (CSI ? … u),
DECRQM reports (CSI ? … $ y), and anything else are kept. Unrecognized bytes
are preserved so a partial/interleaved stream is never silently dropped.

CPR (R) and DA (c) are the scanner's; drop them. Everything else
(kitty u, DECRQM $y, …) is a gap the scanner misses — keep it.

emitSignal is the single exit for both signal observers: it remembers the
observation before handing it on, so the level survives in a readable form as
well as travelling as an event. Both callers run on their own goroutine (the
read loop, the shell foreground poller), which is why the field is guarded.

LastSignal is the most recent level either observer emitted, and false when
the session has not produced one. It is what a reconnecting daemon reads to
recover evidence it was not running to hear.

Serialize the server-authoritative ghostty terminal and read the sequence
watermark atomically, so a re-attaching frontend can dedup the live stream
against LastSeq without a hole: every byte in the dump has seq <= LastSeq,
and a live chunk it will apply has seq > LastSeq. Without this atomicity a
chunk written between the serialize and the watermark read is in neither —
lost. Supported-platform sessions always have a Ghostty terminal; the
unsupported-platform buildability stub serializes no dump.

libghostty-vt does not surface a scrollback-truncation flag (the vestigial
ghosttyvt.Snapshot.ScrollbackTruncated was removed in Phase 3a as always
false), so the signal is reported false until the native serializer exposes
one. The field is still plumbed for that future and for observability.

Test seam: drives a PTY write into the post-snapshot window to expose the
race on unfixed code. Fired after the unlock so it never deadlocks the
read loop. nil (zero overhead) in production.

LastSeq is the dedup boundary: it names the last chunk covered by this
snapshot, so the frontend applies live chunks with seq > LastSeq and
drops the rest as already-replayed. screenSnapshot() reports the same
covered-chunk semantics; the two must not diverge or the first live
chunk after an attach is silently lost (or double-applied).

Under replayMu like every other terminal read: teardown nils the terminal
under that lock, so an unlocked check-then-use could copy out of a freed
handle. A session with no terminal, and an id the storage does not hold, give
the same ordinary answer — ErrKittyImageNotFound naming the id.

The second and last fold of the session's epoch (the placement read is the
other). A client asks for pixels because a placement named a generation it
has none for, and it stores what comes back under the generation the
answer carries — so the two halves have to speak the same numbering or the
pull repeats forever. See mintKittyEpoch.

screenSnapshot is a lean, read-only ghostty viewport serialization plus the
sequence watermark. Its styled VT stream replays into a fresh Ghostty model;
unlike info() it omits scrollback and replay history, so it is cheap enough to
call for many sessions at once (e.g. seeding every grid tile). It registers no
subscriber and claims no geometry.

The viewport and its watermark are captured atomically under replayMu — the
same critical section the read loop uses to apply a chunk and advance
lastReplaySeq — so LastSeq names exactly the last chunk baked into this
snapshot, matching info()/Attach semantics (the two must not diverge).
seqCounter would be wrong here: the read loop increments it BEFORE applying
the chunk, so a snapshot landing in that gap would claim to cover bytes the
screen does not contain, and an observer deduping the live stream against
LastSeq would silently drop the chunk carrying them.

resize applies a client's geometry to the grid, the worker terminal and the
kernel's winsize. xpixel/ypixel are the pane's TOTAL size in device pixels;
zero means the client has no pixel geometry, and the session then reports the
totals its remembered cell size implies rather than reporting zeros — an
attach-time reconcile must not blank out what a fit already measured.

The pane total is what a client can measure, but the cell is what an
emitter and the terminal's own size reports are built from, so the
derivation happens once, here, and everything downstream speaks cells.

The resize mutates the same terminal info() serializes and the block feed
resolves rows against, so it belongs in that critical section.

No-reflow, because every client frame is: the app's fit and its replay
both resize with DEC wraparound off (app/src/utils/ghosttyResize.ts), so a
reflowing worker would re-wrap history the clients keep unwrapped and the
same bytes would occupy different rows on the two grids. Every row-indexed
mapping across the wire — placements above all — rides on them being equal.

Before the grid resize so the terminal never answers a size report
from the old cell against the new grid.

A resize moves images without producing a byte of output, so no chunk
carries the correction and the client cannot derive it: it never saw the
placements move. Deferring to "the next chunk" fails exactly when no next
chunk comes, which is the common case — a resize on an idle session leaves
every image drawn where the old grid put it until something types.

Stamped with the replay watermark rather than a fresh seq: no bytes were
produced, so the set describes the grid as of the last chunk the client
has. That makes a resize emission raceable against a concurrent chunk's
emission at an equal or lower seq, and the resolution is the ordering rule
clients apply — a set is taken when its seq is >= the last one applied.
Because every emission carries the WHOLE set, any interleaving that drops
one is healed by the next, and the loser is never a partial update.

X/Y are ws_xpixel/ws_ypixel: the pane's total pixel size, which is what an
image emitter reads through TIOCGWINSZ to decide how large to draw.

closePTMX closes the pty exactly once, shutting out the writers and the
resize ioctl that share writeMu. Both the read loop's deferred cleanup and
closePTY funnel through here.

sigtermToHUPGrace is how long kill waits for a SIGTERM'd child before
escalating to SIGHUP. Interactive shells ignore SIGTERM by design but
every shell honors terminal hangup; without this escalation a shell
pane close stalls the full kill timeout and ends in SIGKILL.

The wire feed and the Ghostty terminal own native refs that info() resolves
and resize() moves, so their teardown takes replayMu — the same lock those
readers hold. Manager.Remove can hand an already-looked-up session to an
in-flight attach before closing it; without this hold that attach could read
or free the same native ref concurrently. Both fields are nil'd under the
lock so a reader that arrives after teardown sees absence, not a freed
handle.

SetTheme replaces the colors used to answer OSC 10/11/12 queries. Safe to
call concurrently with the read loop.

writeOSCColorResponses answers every OSC 10/11/12 query in queries.oscQueryOrder,
one reply per query in the order the chunk asked — real terminals answer OSC
queries in ask order, and a client that writes a burst of mixed OSC10/11/12
queries and pairs replies positionally depends on that order.

hexColorToOSCValue converts a "#rrggbb" hex color into the "rgb:RRRR/GGGG/BBBB"
value XTerm-style OSC color replies use, doubling each 8-bit channel to
16-bit by repeating its hex pair. Falls back to fallbackHex (assumed valid)
when value is malformed or empty.

writeCursorPositionResponse answers a CPR (cursor position report) query from
the authoritative screen model. The daemon is the single CPR responder for a
session: fish blocks its prompt redraw on the resize-triggered CPR until it
gets a reply, and routing every CPR through the daemon (which owns geometry,
AGENTS.md pattern #7) makes the reply race-free regardless of frontend
attach/replay timing. The frontend deliberately does not answer CPR, so there
is no double-reply to confuse the shell.

Read the cursor under replayMu: teardown nils the terminal under that
lock, so an unlocked check-then-use could query a freed handle.

writeDeviceAttributesResponse answers a DA1 (primary device attributes) query.
Like CPR, the daemon is the single DA1 responder for a session: fish blocks its
prompt redraw on the resize-triggered DA1 until it gets a reply, and after a
reattach the frontend can be mid-remount/replay and miss it (fish then stalls
for its ~10 s query timeout). The reply is a static capability string identical
to the one the frontend would send, so routing every DA1 through the daemon
(which owns geometry/capabilities, AGENTS.md pattern #7) is safe and race-free.
The frontend deliberately does not answer DA1, so there is no double-reply.

indexDA1Query returns the offset of the first CSI Primary Device Attributes
query (ESC [ c  or  ESC [ 0 c) in data, or -1. It ignores DA2 (ESC [ > c)
and other variants.

indexCPRQuery returns the offset of the first DSR 6 / CPR query
(ESC [ 6 n) in data, or -1.

oscColorQueryPrefixes are the recognized OSC color query prefixes (ESC ]
<code> ; ?, terminated by BEL or ST — the prefix match is sufficient). An
OSC color SET (e.g. "\x1b]11;#000000\x1b\\", no "?") never matches: the
prefix requires "?" immediately after ";".

scanOSCColorQueries scans data for non-overlapping OSC 10/11/12 color
queries and returns their codes in encounter order — the order real
terminals answer in, and the order a positional-pairing client depends on.
