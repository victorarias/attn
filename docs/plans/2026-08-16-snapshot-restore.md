# Snapshot restore, and the end of the carried native patch

Written 2026-08-16, continuing
[2026-08-15-first-party-libghostty-vt.md](2026-08-15-first-party-libghostty-vt.md).
That plan moved both runtimes onto ghostty's own pin and left three
follow-ups. This is steps 2 and 3, plus the removal of the daemon's
unmaintained browser client.

## Why these travel together

`ghostty-vt-native.patch` exists for one reason: `ghostty_terminal_serialize_vt`,
which the worker calls to produce the restore payload. Upstream's snapshot API
replaces that call. Deleting the patch without adopting the snapshot API would
leave restore with nothing to call, so the two are one change, not two.

## What restore becomes

Today the worker serializes its terminal to a VT stream and the client replays
it into a fresh model with responses suppressed, plus a corrective `CUP` the
worker appends because the dump emits the cursor before tabstop resets move it.

With the snapshot API the worker encodes a binary record stream and the client
*decodes a terminal from it*. The corrective `CUP` goes away — there is no
replay to get out of order. So does the "reset a fresh model, resize to the
snapshot grid, write the dump suppressed" dance: the decoder hands back a
terminal that already holds the state.

### Receipts

Measured on this pin, native encode on darwin/arm64, decode in the browser
module (node, same wasm the app ships), 200x50 grid fed 12,000 lines of
styled agent-shaped output.

| scrollback budget | history rows kept | snapshot | native encode | wasm READY | wasm history |
|---|---|---|---|---|---|
| 10,000 B (today) | 289 | 178 KB | 0.39 ms | 0.24 ms | 0.6 ms (1 page) |
| 4 MB | 1,955 | 1.1 MB | 0.53 ms | 0.29 ms | 4.6 ms (8 pages) |
| 16 MB | 9,095 | 5.0 MB | 2.60 ms | 0.31 ms | 20.6 ms (38 pages) |
| 64 MB | 11,951 | 6.6 MB | 3.62 ms | 0.36 ms | 22.1 ms (50 pages) |

Two things fall out of that table.

**READY is flat and history is not.** Decoding the renderable prefix costs
~0.3 ms regardless of how much history follows; the history pages are what
scales. So restore decodes READY, paints, and *then* prepends history pages —
the shape the API was built for
(`ghostty_snapshot_decoder_ready` / `_next`). A one-shot `_decode` would put
20 ms of history work in front of the first frame for no reason.

**A snapshot is not free on the wire.** `attach_result.snapshot` is base64 in
JSON, so 6.6 MB of history becomes ~8.8 MB per attach. At today's retained
history it is 178 KB and nobody notices; the wire cost is a function of the
scrollback budget, not of this change.

### Cross-runtime decode is proven

The production path is native encode → browser decode, and the two runtimes
are not configured alike: the worker stores kitty images (320 MB limit),
while the browser module has kitty hard-disabled at build time. Both a
ground-state snapshot and one taken with the alternate screen active *and*
the parser mid-CSI decode cleanly in the browser module.

### Continuation tracking is now mandatory

`ghostty_snapshot_encode` fails outright when the parser sits mid-sequence
unless continuation tracking was enabled *before* the bytes that produced that
state. A pty chunk boundary lands mid-sequence routinely, so the worker enables
tracking at construction, not at snapshot time.

`ContinuationMaxBytes` is ghostty's own kitty APC buffer limit (65 MiB): a
longer sequence never reaches the parser intact, so nothing legitimate sits
above it, and the decoder's default input limit is the same number — a snapshot
we encode is one an unconfigured decoder accepts. It costs no steady-state
memory; only a sequence currently in flight is retained, and the APC handler
already holds those same bytes.

## The scrollback defect this uncovered

`GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES` takes **bytes**. attn passes
`DefaultMaxScrollback = 10000`, documented and named as *lines*. So every
session retains ten kilobytes of scrollback — measured above as **289 rows** at
200 columns, against the 10,000 the name promises. A 1 MB budget measures the
same 289 rows, which puts ghostty's page granularity at roughly one page for
this width; real history needs a budget in the megabytes.

This predates the snapshot work and is not caused by it. It is in scope here
because restore fidelity is exactly what this change is about, and because the
number is a landmine of the kind we do not leave lying: a limit nobody
measured, hit constantly, silently.

The budget is now **8 MB**, measured at ~4,400 rows at 200 columns. It is a
per-session resident cost and it sets the size of every attach payload, so it
was the maintainer's call rather than an implementation detail.

## Deleting the daemon's browser client

`internal/daemon/web/` is a 2,731-line page plus a committed `ghostty-web.js`
and its own `ghostty-vt.wasm`, landed once and never regenerated. It is a
third VT implementation in the system, on a build that does not move with
`ghostty-vt.pin`, and the parity corpus cannot see it.

The remote plumbing around it is worth keeping; the client is not. It goes,
along with `/web-instrumentation`, the embedded asset handler, and its tests.
A browser client, when there is a reason for one, is rebuilt on the same
first-party binding the app uses.

## Work

1. **Worker encodes snapshots.** Continuation tracking at construction;
   `Serialize()` returns snapshot bytes. `SerializeViewport()` is untouched —
   it drives screen snapshots through the plain formatter and never used the
   patch.
2. **Protocol.** `AttachSnapshot.vt_dump_b64` becomes snapshot bytes; version
   bump; `generated.ts` moves with `generated.go`.
3. **Client decodes.** The binding gains a decoder: READY first, history pages
   after the first paint. `GhosttyTerminal` adopts a decoded handle instead of
   replaying a dump, swapping only the libghostty handle so the render state,
   the cell pool, and every React ref survive the restore.
4. **Drop the patch**, republish native prebuilts under the new key, and drop
   the corrective `CUP`. The patch was also the only reason the artifact key
   hashed anything besides the pin, and the "no patch present" path had never
   run — it returned non-zero under `pipefail` and killed the publish script.
5. **Delete the web client.**

Cross-runtime restore is pinned by `app/src/ghostty/testdata/native-snapshot.bin`:
the native encoder writes it (`ATTN_UPDATE_FIXTURES=1 go test ./internal/ghosttyvt`),
a Go test proves it still decodes on this pin, and the browser binding decodes
the same bytes. It carries scrollback across several history pages and a
deliberately unfinished CSI, so continuation restore is covered by the case
where completing the sequence prints nothing.

## Left standing

The frontend's historical-replay machinery — chunked writes, the cooperative
replay budget, the generation fence, the dropped-resize recovery — exists
because a restore used to be megabytes of VT written in slices. A decode is one
fast operation, so the write path's `historicalReplay` option now has no caller.
It is left in place here rather than unpicked from `GhosttyTerminal` in the same
change: `resizeLocal` still uses the same counters, and the write path is the
most delicate code in the app. Removing it is its own PR.
