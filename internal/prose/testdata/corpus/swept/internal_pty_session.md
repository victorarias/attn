TerminalTheme carries the frontend's resolved terminal colors as "#rrggbb"
hex strings; zero-value fields fall back to built-in dark defaults.

infoSnapshotHook is a test-only seam fired inside info() between the ghostty
serialize and the LastSeq read. nil in production.

readLoopSeqGapHook is a test-only seam fired after a chunk's seq is
allocated but before the chunk is applied under replayMu. nil in production.

onPlacements receives the kitty placement set whenever a chunk moved it;
nil for subscribers that do not draw. See OnPlacements.

osc10/osc11/osc12 count OCCURRENCES, not presence: each query needs its
own reply or the program hangs. Derived from oscQueryOrder.

oscQueryOrder lists the OSC color codes (10/11/12) queried, in ask order
— clients that pair replies positionally depend on it.

Cell size in device pixels; zero until the first fit. The cell is
remembered rather than the total so a pixel-less resize can re-derive
its total.

ghostty is the server-authoritative parsed terminal (libghostty-vt):
approval detection, query replies, screen snapshots, attach restore.

wireFeed owns writes into ghostty and returns the bytes the wire carries
instead. nil exactly when ghostty is nil; every use is nil-guarded.

kittyEpoch is the offset folded into every kitty generation this session
reports; wireFeed holds the same value for the placement half, this
field serves kittyImage. Set at construction. See mintKittyEpoch.

replayMu makes Ghostty feeds and lastReplaySeq atomic for snapshots, so
a re-attaching frontend never drops a chunk that landed between the
payload snapshot and the watermark read. fanOut stays outside it.

writeMu guards every ptmx access that is not a Read: writes, the resize
ioctl, and the close — Fd() must not race Close. ptmxClosed makes a late
caller a no-op instead of a use of a dead fd.

harnessSignals and shellSignals read state signals off the RAW stream;
neither alters the bytes. shellSignals is nil for non-shell agents.

lastSignal is the most recent observation either observer emitted, kept
so a restarted daemon can READ the level: an agent parked at its prompt
writes nothing, so there would otherwise be no evidence until the user
typed. Written by both emitters, read from the info RPC.

fanOutPlacements hands one placement update to every subscriber that asked
for placements. Called AFTER the chunk's bytes are fanned out and with
replayMu released: an update states where images sit on the grid THOSE bytes
produce, so it must not arrive first.

forceResync drops every subscriber with reason, reaching each client as a
pty_desync: the frontend resets and re-attaches for a fresh snapshot — the
escape hatch for a chunk whose grid effect the wire could not express
(wireFeeder). Call with replayMu released; the callbacks take their own
locks.

PTY reads are coalesced before fan-out: macOS pty reads return tiny chunks
under load (~100 bytes), and MESSAGE COUNT, not byte volume, balloons the
WebKit frontend. A read with nothing queued behind it is emitted
immediately — echo latency unchanged; a flood batch is bounded by
ptyCoalesceWindow.

nextCoalescedRead returns the next batch of PTY output, blocking for the
first read; with no further read queued it is returned as-is. The returned
error belongs to the last read folded in; callers must not receive after it.

The worker is the single, always-on responder for CPR, DA1,
and OSC 10/11/12 — race-free regardless of frontend
attach/replay timing; the frontend answers none of these.

Feed under the same lock as the seq watermark so a
snapshot stays atomic with it; the placement set read in
the same hold is tied to these bytes by the seq.

Drain ghostty's query responses AFTER the lock (the sink has
its own mutex).

Answer CPR/DA1 after the chunk is applied so the reported
cursor is current, in ask order — fish sends ESC[6n ESC[0c
and blocks its prompt redraw until it gets both.

An empty wire chunk means the feeder is holding an
unterminated escape; dedup (`seq > last_seq`) tolerates the
missing seq.

After the bytes, never before: the set describes the grid
they produce.

drainGhosttyResponses clears the ghostty terminal's accumulated query
responses and forwards the ones the scan-based responder does not cover
(kitty CSI ? u, etc.) to the PTY, so the worker answers every query and a
snapshot-restored client can suppress all responses. Call after replayMu is
released; the sink has its own lock.

The nil check and the drain are one critical section: teardown nils the
field under replayMu, so checking outside would drain a freed terminal.

stripScannerOwnedResponses removes the response classes the scan-based
responder already emits — CPR (CSI … R), DA (CSI … c), OSC 10/11/12 color
reports — so forwarding the remainder never double-answers. Unrecognized
bytes are preserved so a partial stream is never silently dropped.

emitSignal is the single exit for both signal observers: it remembers the
observation before handing it on. Both callers run on their own goroutine,
hence the guard.

LastSignal is the most recent level either observer emitted, false when none
— what a reconnecting daemon reads to recover evidence it missed.

Serialize the ghostty terminal and read the watermark atomically: every
byte in the dump has seq <= LastSeq, every live chunk to apply has
seq > LastSeq. Without this a chunk written between the two is lost.

libghostty-vt surfaces no scrollback-truncation flag yet; reported false
until the native serializer exposes one.

Test seam; fired after the unlock so it never deadlocks the read loop.

LastSeq is the dedup boundary. screenSnapshot() reports the same
covered-chunk semantics; the two must not diverge or the first live
chunk after an attach is silently lost (or double-applied).

kittyImage copies one stored image out of the session's terminal. Under
replayMu like every terminal read: teardown nils the terminal under that
lock. No terminal and an unknown id give the same ordinary answer.

The second and last fold of the epoch (readPlacements is the other): the
two halves must speak the same numbering or the pull repeats forever.

screenSnapshot is a lean, read-only ghostty viewport serialization plus the
sequence watermark — no scrollback or replay history, cheap enough for many
sessions at once; no subscriber, no geometry claim.

Captured under replayMu so LastSeq names exactly the last chunk baked in,
matching info()/Attach semantics. seqCounter would be wrong here: the read
loop increments it BEFORE applying the chunk, so a snapshot in that gap
would claim bytes the screen does not contain.

resize applies a client's geometry to the grid, the worker terminal and the
kernel's winsize. xpixel/ypixel are the pane's TOTAL device pixels; zero
means no pixel geometry, and the session then reports the totals its
remembered cell size implies — an attach-time reconcile must not blank out
what a fit already measured.

The resize mutates the same terminal info() serializes, so it belongs in
that critical section. No-reflow because every client frame is: the
app's fit and replay resize with DEC wraparound off
(app/src/utils/ghosttyResize.ts), and every row-indexed mapping across
the wire — placements above all — rides on the grids being equal.

Before the grid resize so the terminal never answers a size report
from the old cell against the new grid.

A resize moves images without producing a byte of output, so no chunk
carries the correction; deferring to "the next chunk" fails on an idle
session, the common case.

Stamped with the replay watermark, not a fresh seq: no bytes were
produced. Clients take a set whose seq is >= the last applied, and every
emission carries the WHOLE set, so any dropped one is healed by the next.

X/Y are ws_xpixel/ws_ypixel: the pane's total pixel size, which an image
emitter reads through TIOCGWINSZ to decide how large to draw.

closePTMX closes the pty exactly once, shutting out the writers and the
resize ioctl that share writeMu.

sigtermToHUPGrace is how long kill waits for a SIGTERM'd child before
escalating to SIGHUP: interactive shells ignore SIGTERM by design but every
shell honors terminal hangup.

closePTY releases the pty and the native terminal state behind it. Teardown
takes replayMu — the same lock info() and resize() hold — because
Manager.Remove can hand an already-looked-up session to an in-flight attach;
both fields are nil'd under the lock so a late reader sees absence, not a
freed handle.

SetTheme replaces the colors used to answer OSC 10/11/12 queries. Safe to
call concurrently with the read loop.

writeOSCColorResponses answers every OSC 10/11/12 query in
queries.oscQueryOrder, one reply per query in ask order — the order a
positional-pairing client depends on.

hexColorToOSCValue converts "#rrggbb" into the "rgb:RRRR/GGGG/BBBB" value
XTerm-style OSC color replies use, doubling each 8-bit channel by repeating
its hex pair. Falls back to fallbackHex (assumed valid) when malformed.

writeCursorPositionResponse answers a CPR query from the authoritative
screen model. The daemon is the single CPR responder — fish blocks its
prompt redraw on the resize-triggered CPR — and the frontend deliberately
does not answer, so there is no double-reply.

writeDeviceAttributesResponse answers a DA1 query. Like CPR, the daemon is
the single responder: after a reattach the frontend can be mid-remount and
miss it, and fish then stalls for its ~10 s query timeout.

indexDA1Query returns the offset of the first CSI Primary Device Attributes
query (ESC [ c or ESC [ 0 c), or -1. It ignores DA2 (ESC [ > c).

indexCPRQuery returns the offset of the first DSR 6 / CPR query
(ESC [ 6 n), or -1.

oscColorQueryPrefixes are the recognized OSC color query prefixes (ESC ]
<code> ; ?). An OSC color SET (no "?") never matches.

scanOSCColorQueries scans data for non-overlapping OSC 10/11/12 color
queries and returns their codes in encounter order.
