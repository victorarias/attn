# Plan: kitty graphics in the attn terminal (worker-authoritative)

## Goal

Render kitty-protocol images (icat, chafa, timg, matplotlib's kitty backend) in
the attn terminal, with restore across detach/reattach, on local and remote
sessions — and, before any of that, stop the false capability advertisement
that desyncs the two terminal grids today.

Companion track: the ghostty WASM crash fix continues under its own plan,
[2026-07-27-ghostty-wasm-model-crashes.md](2026-07-27-ghostty-wasm-model-crashes.md).
The tracks are independent; the one interaction is that stripping kitty APC
from the wire (this plan) means the crash-prone client wasm never parses kitty
bytes at all.

## Today's behavior (evidence, 2026-08-02)

The daemon worker's native terminal (libghostty-vt, pin `ab0b9da`) compiles
kitty graphics **in** and the `.lib` artifact defaults
`kitty_image_storage_limit = 10MB`, so it is live — attn never configures it.
The frontend's wasm model can **never** parse kitty graphics: ghostty disables
it on `wasm32-freestanding` at every pin (the implementation needs
timestamps), so pin convergence does not change this.

Probe (temporary Go test against `internal/ghosttyvt`, darwin/arm64, since
deleted):

```text
direct-rgb     cursor 0,1 -> 0,0 | resp=""                    | text="aftere"
png            cursor 0,1 -> 0,1 | resp=""                    | text="before|after"
query-support  cursor 0,1 -> 0,1 | resp="\x1b_Gi=31;OK\x1b\\" | text="before|after"
```

- attn **advertises kitty image support**: the `a=q` reply reaches the program
  (`drainGhosttyResponses` forwards responses the scanner does not own).
  Feature-detecting tools will send images.
- A direct-RGB image is stored worker-side and **moves the cursor**; the
  client model ignores the APC entirely → the two grids silently desync. The
  worker grid feeds approval classification and the restore dump, so a restore
  visibly changes what the user sees.
- PNG (`f=100`) is dropped worker-side — no `sys.decode_png` host hook — so
  those stay consistent but invisible.
- Sixel does not exist anywhere in ghostty; kitty is the only image protocol
  on the table.
- The current AGENTS.md claim that "sixel/kitty images render from the live
  stream" is false on both halves and is corrected in Phase A1.

## Decision: worker-authoritative, one parser

The worker's native ghostty is the **only** kitty parser in the system. It
already parses today; the client never can. The worker owns image parsing,
storage, and layout; the client receives (a) a plain-VT byte stream with the
APC stripped and layout-equivalent bytes substituted, and (b) structured
placement/image data, rendered as textured quads by the app's own WebGL
renderer.

Rejected alternatives:

- **Client-side TS kitty parser** (xterm.js image-addon shape): keeps the wire
  pristine, but the worker must stay enabled for classification and restore,
  so it means two kitty parsers held in lockstep forever — strictly more
  machinery than the OSC 133 dual-parser precedent, for a far larger protocol.
  It also gets `t=f` (file transmission) wrong for remote sessions: the path
  in the escape names a file on the machine running the PTY, which only the
  remote worker can read.
- **attn-native images, no kitty**: cheapest, but third-party emitters get
  nothing, and the advertise bug still needs the worker-side fix anyway.

Honest costs of the chosen design, and their mitigations:

- **The stream rewrite is a permanently-owned correctness surface.** If the
  synthesized layout bytes ever diverge from what ghostty did, the grids
  desync — the same bug class we are fixing, now ours. Mitigations: synthesis
  observes the authoritative terminal rather than predicting (see below); the
  cross-runtime parity corpus is a merge gate, not a nice-to-have; and any
  case synthesis cannot represent degrades to a forced snapshot re-push (the
  existing server-authoritative restore), which re-syncs by construction —
  divergence can be healed, never silent.
- **Daemon memory holds pixels for every session**, attached or not. Bounded
  by the per-session storage limit; the limit number gets a receipt in A4.
- **Display latency**: the image quad appears when the placement/blob event
  lands, a beat after the text reflows. Accepted; pop-in, not corruption.

## What this generalizes to

The worker is already a semantic tap on the byte stream, with two scanner
classes in production: **look-only** (`oscscan.go`: OSC 0 titles, OSC 777
notifications — the wire is untouched) and **strip-and-structure**
(`osc133.go`: markers stripped from the terminal feed, blocks shipped as
structure). Kitty adds the third class: **keep-for-terminal,
rewrite-on-wire, structure-out**.

Future tenants that reuse the same seams (scanner slot in the feed path,
structured event out, app-native surface) and are explicitly *not* built now:
OSC 9;4 progress → session-tile progress; OSC 9/777 → attention routing;
OSC 52 clipboard (matters most for remotes); iTerm2 OSC 1337 images (ghostty
is kitty-only, but an attn-side OSC 1337 parser could feed the same placement
store — follow-up-sized). Shape the A2/A3 seams so a second tenant is a small
PR; build none of them speculatively.

## Architecture

```text
PTY read loop (worker, per chunk, under replayMu):
  raw chunk
    ├─ kitty segmenter (outer): split at complete APC `ESC _ G … ST` boundaries,
    │    buffer partial APC across chunks (findSafeBoundary only protects the
    │    trailing 64 bytes; payloads run to ~4KB, so spanning is the segmenter's
    │    job, same as OSC 133)
    │      non-APC segments ──► blockFeed.feed (existing OSC 133 strip + ghostty write)
    │      APC bytes ─────────► ghostty term.Write (ghostty parses — the one parser)
    │                            ├─ observe: placement-set diff via kitty_graphics.h
    │                            │    iterator (before/after) → add/remove/update
    │                            ├─ observe: cursor + scroll (TrackedRef pinned at
    │                            │    the cursor before the APC; both it and a ref
    │                            │    at the new cursor resolved after, so the pair
    │                            │    shares one coordinate frame)
    │                            └─ synthesize: SU×scroll + CUU/CUD + CUF/CUB into
    │                                 the wire chunk (relative on both axes:
    │                                 absolute addressing is measured from a frame
    │                                 the worker cannot see — rows from the scroll
    │                                 region under origin mode, columns from the
    │                                 left margin under DECLRMM)
    ├─ lastReplaySeq = seq   (unchanged)
  outside the lock: drain/forward ghostty's `_G` ACKs to the program (unchanged)
  fanOut(wireChunk, seq)     ← rewritten bytes, same seq

structured side (same replayMu hold as the diff):
  placement events {server-assigned id, session, buffer-row anchor, grid rect,
                    z, image ref, seq}
  image blob events {image ref, pixel data, dims, seq}   (size-capped, see A4)

attach snapshot: atomic {dump, blocks, placements+image refs, watermark}
  — the existing triple grows one member, read under the same replayMu hold.

frontend:
  placement store — buffer-row anchored, re-anchored via the block-store
    contract (dump-coordinates at restore, reanchorDelta thereafter)
  GhosttyWebGlRenderer — textured-quad pass beside the existing overlay pass;
    blob cache keyed by image ref
```

Design rules:

- **The cursor rides the content.** A tracked ref pinned at the cursor reports
  how far the cursor moved relative to the cell it was on, not how far the grid
  moved: a placement that scrolls carries that cell along with everything else.
  The scroll is `(rowBefore - rowAfter) + refDelta`, an identity that holds on
  the primary screen, on the alternate, and inside a scroll region — the three
  places the coordinate frames differ. Measured, not assumed: ghostty moves the
  cursor down by the image's row count and then back up one, so a one-row image
  on the bottom row still scrolls.
- **A discarded ref clamps; it does not fail.** `ScreenPoint` reports ok=false
  only when the cell is pruned out of a *scrollback* that is already at its cap
  (thousands of rows, so an image can never do it). On the alternate screen,
  which keeps no history, a scroll destroys the cell and the ref silently
  resolves to the top row instead — which reads as a shorter scroll than
  happened. Synthesis therefore treats "the anchor reached row 0 while the grid
  scrolled" as unrepresentable and re-pushes, rather than trusting the number.
- **Observe, never interpret.** The worker never implements kitty semantics.
  Deletes (`a=d`), chunked transmissions (`m=1`), ids, z-order, quiet flags —
  all fall out of diffing ghostty's authoritative placement list before/after
  the feed. The segmenter's only protocol knowledge is finding APC
  boundaries. Layout synthesis likewise reproduces *observed* deltas (cursor,
  scroll), not spec-predicted ones.
- **Ids are server-assigned and monotonic** per session (the `AttachBlockData.ID`
  precedent); kitty's client-chosen ids never reach the protocol.
- **PNG support** = a Go `decode_png` host hook (`image/png` via cgo callback,
  `GHOSTTY_SYS_OPT_DECODE_PNG`). Ghostty stores decoded pixels; the wire
  ships ghostty's stored data (RGBA), deflated. If measured sizes make raw
  pixels too heavy, shipping original encodings is the follow-up (requires
  retaining originals keyed by image id — light APC key parse, noted, not v1).
- **Blob transport** rides the existing WebSocket as a dedicated payload
  (binary-frame or event — decided in A3 by measuring real emitter sizes).
  The remote relay already carries WS traffic hub→client, so remotes work
  without a new channel; a per-image size cap with a named, visible error is
  part of A4's receipts. A daemon HTTP endpoint was considered and rejected:
  no remote path exists for it, and one image is one WS message — the
  256-message client buffer is a message-count limit, not a byte limit.

## Replay and attach safety

"Replay" today is not a buffer — raw replay was deleted with the
server-authoritative restore. It is a dedup contract: an attach serves the
snapshot plus `LastSeq`; the client applies live chunks with `seq > LastSeq`.
The invariants that keep images from breaking it:

1. **One rewrite point, upstream of sequencing.** The transform happens in the
   read loop under `replayMu`, in the same critical section that feeds the
   terminal and advances `lastReplaySeq`. The rewritten chunk keeps its seq.
   Nothing downstream (fan-out, dedup, diagnostics of the wire) ever sees raw
   kitty bytes, so nothing downstream changes.
2. **The snapshot atomicity contract extends, not bends.** Every byte baked
   into the dump has `seq <= LastSeq`; every chunk the client will apply has
   `seq > LastSeq`. Placements join the atomic snapshot read under the same
   lock, and placement/blob events carry the seq of the chunk that produced
   them, so the client dedups them against `LastSeq` with the *same rule* as
   bytes: seed from the snapshot, apply events with `seq > LastSeq`, drop the
   rest. No hole, no double-apply.
3. **Mid-APC attach is safe by construction.** A partial APC has no grid
   effect until its terminator, so a snapshot taken mid-transmission serves
   the pre-placement grid; the terminator's chunk (grid effect + synthesized
   bytes + placement event, all in one critical section) necessarily carries
   `seq > LastSeq` and lands on the restored client. Held wire bytes are
   bounded (`APC_MAX_BYTES` option, plus segmenter abandon-on-oversize like
   `osc133MaxPendingBytes`) and flushed through on abandon.
4. **The VT dump stays image-free and consistent.** Ghostty's formatter emits
   no image state, and the synthesized bytes reproduce exactly the cursor and
   scroll the images caused — so a dump written into a fresh client model
   agrees with a model that lived through the rewritten stream. That
   equivalence is precisely what the parity corpus asserts.
5. **The escape hatch is the existing restore.** If synthesis meets a case it
   cannot represent (an anchor that no longer resolves or that clamped to the
   top of history, a grid that moved backwards), the worker drops the session's
   subscribers with a named reason — the same `pty_desync` round trip an
   overflowing client buffer already takes, after which the frontend re-attaches
   and is served a fresh snapshot. Worst case is a re-sync the user doesn't
   notice, not a silent desync.

## Verification

The hard gate — **cross-runtime parity corpus**, precedent
`testdata/osc133_segmenter_corpus.json`:

- `internal/pty/testdata/kitty_rewrite_corpus.json`: each entry holds raw
  input (captured real emitter output and synthetic cases: chunked `m=1`,
  deletes, z-layers, images taller than the screen, scroll regions, alt
  screen, interleaved resizes), and the Go side records the produced wire
  bytes plus the native ghostty grid text and cursor.
- The Go test asserts segmenter/synthesis behavior against the native grid;
  the vitest side replays the recorded wire bytes into the real wasm model
  and asserts its grid and cursor equal the recording. Grids agreeing across
  both runtimes *is* the no-desync property. New synthesis code does not
  merge without corpus coverage of what it synthesizes.

Layers around the gate:

- **Go unit**: segmenter split-at-every-byte-offset property tests (chunking
  must never change output — the osc133 corpus pattern); synthesis goldens;
  native-handle leak checks against the `ghosttyvt.LiveTrackedRefs` baseline
  pattern.
- **Fuzz** (Go): randomized interleavings of text, images, deletes, resizes,
  scroll regions, alt-screen flips; any failure is minimized and committed as
  a corpus entry, which the wasm side then replays too.
- **Go integration**: a real PTY session end-to-end — placement/blob events
  emitted with correct seqs, snapshot carries the quadruple atomically
  (extend the existing `readLoopSeqGapHook` race tests to placements).
- **Packaged harness**: new `real-app:scenario-terminal-image` — a
  deterministic emitter script prints a known image; screenshot-verify pixels
  (app-owned screenshots), then scroll, resize, detach/reattach restore, and
  a `Cmd+T` focus sanity pass. Rebuild before evidence runs (baked
  fingerprint).
- **Live verification** (throwaway profile, never dev for daemon+protocol
  changes): real emitters — chafa, timg, kitten icat, a matplotlib
  kitty-backend script — locally and on a remote session via the OrbStack VM
  (`attn-remote@orb`), plus the A1 negative check (tools stop detecting
  image support while the feature is dark).

## Phases

### A1 — stop the lie (small PR, ships first)

- [ ] Set the worker terminal's kitty image storage limit to 0
      (`GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT`, plumbed through
      `ghosttyvt.Options`).
- [ ] Verify limit-0 silences the `a=q` OK reply; if ghostty still ACKs,
      filter `_G` responses in the worker's response drain instead. Test:
      no support reply reaches the program, no cursor movement from a
      direct-RGB write — the probe scenario, kept this time.
- [ ] Correct the AGENTS.md terminal section (images do not render today;
      sixel does not exist in ghostty).
- [ ] Changelog fragment.

### A2 — worker: segmenter, observation, synthesis (feature dark, one live fix)

Feature-dark with one exception, and the PR description has to say so: the
prompt-marker half of the ESC-parity rule below fixes a divergence that is live
in shipped attn today, unrelated to images. Everything else here is unreachable
until the storage limit flips in A4.

- [x] `ghosttyvt`: expose the kitty C API — storage-limit/APC-max options, the
      `decode_png` hook, placement iterator + `image_get`, on the same
      darwin/linux cgo tuples; stubs degrade like every other ghostty use.
- [x] Kitty APC segmenter (wire side) + feed-path composition (kitty outer,
      OSC 133 inner), under `replayMu`.
- [x] Placement-set diff + layout synthesis from observed deltas; forced
      snapshot re-push fallback.
- [x] Segmenter framing: extract only from ground, with every transition
      measured against the terminal (`TestKittySegmenterGroundMatchesGhostty`).
      A kitty APC whose introducing ESC also ends the sequence before it cannot
      be cut out without taking that exit with it, so it stays on the wire and
      the feeder resyncs on the image it places (`kitty_undescribed_image`).
- [x] Parity corpus (39 entries, replayed into native ghostty and the shipped
      wasm model) + the unit layers above. No protocol change; the wire still
      carries nothing new (limit stays 0 until A4).
- [x] **One segmenter for both sequence families.** The OSC 133 scanner used to
      find its marker by byte pattern — the same defect the kitty segmenter had
      shed, in both directions: `\x1b]133;\x1b00` swallowed past a stray ESC
      that ends the OSC for ghostty, and `\x1b\x1b]133;\x1b\\00` stripped a
      marker whose `ESC ]` was never in ground, taking the lone ESC's meaning
      with it. Both reproduced through `blockFeeder` alone, with no kitty escape
      in the stream. The mode machine now emits a third disposition instead, so
      one parser-state tracker frames both families and the byte scan is gone.
      Terminations were measured at dispatch level rather than read off the
      spec: BEL and ST extract; CAN, SUB and a stray ESC all DISPATCH the marker
      in ghostty, but are replayed as plain anyway, because the client's own
      parser knows only BEL and ST and stripping a marker it would not have
      recognised leaves the two block tables disagreeing.
- [x] **Fuzz soak, split by configuration.** The whole-path mirror property
      answers a different question in each of the two kitty configurations, so
      it runs as two targets. `FuzzKittyWireMirrorShipping` puts the worker at
      the zero storage limit production runs today, which makes the property
      pure DISPOSAL — which bytes reach the terminal, which reach the wire.
      `FuzzKittyWireMirror` keeps kitty live, exercises synthesis, and is
      knowingly red on the A4 defects, so it is not run with `-fuzz` in CI.
      Both targets' SEEDS run on every `go test` and are green, so the recorded
      corpus stays honest without reddening the build.
      `FuzzKittySegmenterFraming` soaks the framing rules alone: 3 x 3 min
      clean, 16.9M / 9.6M / 10.3M execs.

      `FuzzKittyWireMirrorShipping` is clean at 3 x 3 min: 9.4M / 6.6M / 6.8M
      execs, corpus 296 -> 466 inputs. Each run started from the previous run's
      warm corpus, which is the harder start — it already carried the inputs
      that found the earlier defects.

      It took three tries to get there, and the failures are the record worth
      keeping: the first run died at 96s on the UTF-8 abort, and the second at
      6.3s on the minimized input `"00000000000000000000\xe1\x1b_G"` — the wrap
      column. Both are fixed by the ESC-parity rule below; neither was
      reachable from the seeds alone.
- [x] **Both streams get an ESC-led no-op wherever they differ.** One rule, three
      defects, all reachable at storage limit 0 — so all three are SHIPPING
      defects, not deferred ones. Two were found by
      `FuzzKittyWireMirrorShipping`; the third was found by following the rule
      backwards and is a live defect on `main`.

      **The design rule.**

      > Wherever the two streams differ, BOTH sides get an ESC-led no-op at that
      > position, so both parsers cross every extraction point in the same state.

      Extracting only from ground keeps the two VT PARSERS in step, and that is
      the property the segmenter was built to give. It is not the whole of the
      state ground implies: ground also holds a UTF-8 decoder, which may be
      part-way through a character. An ESC ends that decode. So whichever side
      loses the bytes must be handed an ESC in their place, and `ESC \` — ST,
      always the 7-bit form — is the cheapest one: two bytes, no cells, a no-op
      from ground on both parsers.

      The rule is directional, and both directions occur:

      | bytes | reaches | substitute goes to |
      | --- | --- | --- |
      | kitty APC | worker, not the wire | the **wire** |
      | OSC 133 marker | the wire, not the worker | the **worker** |

      **1. The wire ST (class 1).** Measured at 20 columns with the cursor on the
      last column, feeding `<19 zeros>\xe1`, a stripped APC, then a tail:

      | tail | without the ST | with it |
      | --- | --- | --- |
      | nothing | worker has U+FFFD, client none — transient | agree |
      | `X` | heals on its own | agree |
      | `\xa5` | **permanent**: worker two U+FFFD, client one character | agree |
      | `\xa5rest` | **permanent**: text lands, character stays wrong | agree |

      The permanent rows are the point: both sides end holding nothing pending,
      so no later byte heals them. An earlier revision recorded "more text |
      heals"; re-measurement contradicts it and the table above replaces it.

      **2. The same ST, written to the WORKER, before anything is measured
      (class 2).** This one is about measurement, not decoding. Ending a decode
      is a GRID event: it writes a replacement character, and on the wrap column
      that character commits the deferred wrap and moves the cursor to the next
      row. Pinned before the abort, that movement lands inside the measured
      window and is attributed to the image; the client then performs the same
      abort itself off the wire's ST **and** applies the synthesized movement,
      landing a row low. It surfaced only in the row because the column was
      described absolutely (`CHA`, idempotent) and the row relatively (`CUD`,
      which double-applies). A4 made the column relative too, for the margin
      reason recorded there, so both axes double-apply now — which is exactly
      what pinning after the abort keeps correct.

      Doing the abort on the worker first leaves the measured window holding
      only what the image did. Measured, worker delta against what the client
      actually needs:

      | case | delta pinned before | pinned after | client needs |
      | --- | --- | --- | --- |
      | shipping, n=19 | `(0,0)` | `(0,0)` | `(0,0)` |
      | shipping, n=20 | `(-18,+1)` | `(0,0)` | `(0,0)` |
      | kitty live, n=19 | `(0,+1)` | `(0,+1)` | `(0,+1)` |
      | kitty live, n=20 | `(-16,+2)` | `(+2,+1)` | `(+2,+1)` |

      Pinning after makes measured delta equal required delta by construction,
      because both sides then start the APC from the same state. The worker's
      grid is byte-identical with and without the pre-ST in all four cases —
      pinned by `TestWireFeedPreSTOnlyEndsTheDecode` rather than asserted.

      **3. The marker ST, written to the worker (class 3).** The mirror image,
      and a defect that predates this branch: `main:internal/pty/blockfeed.go`
      writes only the pre-marker bytes to the terminal, so a marker arriving
      mid-character ends the CLIENT's decode and not the worker's. Measured
      against the shipped wasm, feeding `000\xe1`, `OSC 133;A`, `\xa5 done`:

      | | cursor | row |
      | --- | --- | --- |
      | worker, no substitute | `(9,0)` | `000<FFFD> done` |
      | real wasm client | `(10,0)` | `000<FFFD><FFFD> done` |

      One extra cell and a column of drift, permanent in the live session;
      attach heals it, since the dump is worker-authoritative. Low likelihood —
      it needs a program truncated mid-character just before the shell prompts —
      but not kitty-gated, so it is live in shipped attn. Carried here rather
      than fixed on main: the fix there would have to be written against the
      byte-scan path this branch deletes, with witness infrastructure that only
      exists here. It gets its own user-facing changelog fragment; the rest of
      A2 stays feature-dark.

      The substitute must be an ST and NOT the marker itself. Feeding a real
      `133;A` to the native worker breaks the line (see the pin-skew section),
      which is the divergence this whole file exists to prevent.

      **Ordering, twice.** The worker ST precedes the cursor pin AND the tracked
      ref in `writeAPC`, or the abort is back inside the measured window. The
      marker ST precedes `blocks.mark`, or the block pins the row the cursor
      left rather than the row the prompt renders on — the grids agree either
      way there, so that one is asserted on the pin.

      **Why this and not codepoint-boundary refusal.** The considered
      alternative was to make extraction require true ground — VT ground AND not
      mid-codepoint — via a rolling UTF-8 tail tracker, leaving a mid-character
      APC on the wire for both sides to abort on identically. It was measured
      and it does work for classes 1 and 2. Both halves were measured, and the
      numbers are recorded here so nobody has to re-run them:

      - A raw kitty APC is a perfect grid no-op in the shipped wasm, at any
        payload size and either terminator, and its leading ESC ends a held
        decode exactly as a bare ST does. `ab` + APC + `c` lands on `(3,0)`
        `abc`, the same as `abc` alone, for a small APC, a real 1.5 KB
        placement, and a `0x9c`-terminated one alike.
      - A hand-rolled lead-byte tracker declines an extraction ghostty would
        allow on 5 of 27 probed prefixes: `\xe0\x9f`, `\xe0\x80`, `\xed\xa0`,
        `\xf0\x8f` and `\xf4\x90`. Each is a second byte UTF-8 forbids under one
        of the four constrained leads — an overlong, a surrogate half, or a
        codepoint above U+10FFFF — which ghostty rejects at that second byte
        while the lead-byte tracker still counts one owing. All five err the
        same way: over-reporting "pending" only ever declines an extraction,
        never permits a wrong one.

      That figure is the discarded implementation's own characterization test
      speaking — it prints the count and names the prefixes on every run, so it
      re-measures as ghostty moves. It exists only on that line. Here it is a
      recorded measurement, not a live receipt, because the shipped design has
      no tracker to characterize.

      Read the number with its history: this paragraph carried two different
      hand-measured counts before the test settled it, each from a probe set too
      narrow to reach the next mechanism. That is the argument for the rejection
      rather than against it — the imprecision was never a couple of special
      cases but the whole class of second-byte restrictions on the four
      constrained leads (`0xe0`, `0xed`, `0xf0`, `0xf4`), and a hand-written
      table missed part of it twice.

      Rejected because it cannot reach class 3 — a marker cannot replay as
      plain, since feeding it to the native worker breaks the line — so the
      ESC-led no-op has to exist anyway. Choosing it would therefore mean TWO
      mechanisms: the marker substitute plus a decoder tracker that models
      ghostty's UTF-8 handling forever, which is the same parallel-model trap
      this file's framing rules exist to kill. The ESC-parity rule is one rule at
      three sites and needs no model of the decoder at all.

      **Disposition.** Neither design was argued down on paper. Both were built
      in full and measured on the same gates, and the alternative was rejected
      from a working implementation rather than from a sketch: the tracker, its
      characterization tests, mid-codepoint fuzz seeds, mutation receipts per
      class, a clean three-by-three-minute shipping soak and a live class-3
      witness on a real daemon — all green. A local archive ref held it for the
      life of this branch and was dropped at merge, so what survives is this
      record: anyone revisiting the question is re-deciding it against a design
      known to work, not guessing whether it would have.

      What settled it beyond the two-mechanisms argument is what the measuring
      itself exposed. The hand-written tracker disagreed with ghostty on a class
      its author had not enumerated, and the recorded count had to be corrected
      twice before it held. A model of someone else's decoder drifts before it
      even ships; here that arrived as evidence rather than as prediction, which
      is the strongest form the argument can take.

      **Mutation receipts**, each landing on a named case:

      | removal | what goes red |
      | --- | --- |
      | the wire ST | `TestWireFeedStripsAPCsWithKittyDisabled`, on exact bytes |
      | the worker pre-ST, or pinning before it | `TestWireFeedPinsTheCursorAfterTheDecodeEnds` — wire back to `\x1b\\\x1b[1B\x1b[2G` and client `(1,2)` vs worker `(1,1)` |
      | the marker ST | the mirror battery's `a prompt marker splitting a character on the wrap column`, and both wasm witnesses |
      | `blocks.mark` before the substitute | `TestWireFeedPinsTheBlockAfterTheDecodeEnds`, prompt row 0 for 1 |

      The class-3 receipt is the authoritative one: regenerating the corpus with
      the substitute removed fails exactly the two marker entries in
      `kittyWireRewrite.parity.test.ts` against the REAL shipped wasm — expected
      `000<FFFD><FFFD> done`, recorded `000<FFFD> done` — with the other 34
      passing.

      **Live verification.** Class 3 is a shipping fix rather than a dark one,
      so it was exercised against a real daemon and a real PTY built from this
      branch, on a throwaway `kittyseg` profile (`~/.attn-kittyseg`, port 22964,
      removed with `attn profile clean` afterwards):

      - A program writing `000\xe1` + `OSC 133;A` + `\xa5 done` in one call,
        through a real fish session, leaves the worker's authoritative grid
        (`get_screen_snapshot`) reading `000␦␦ done` — ten cells, the TWO
        replacement characters the wasm client also produces. Without the
        substitute the worker keeps decoding and shows one.
      - Smoke, because the feed path sees every byte of every session:
        `café 日本語 ── 😀 ✓` lands intact — accents, wide CJK cells, box
        drawing and an emoji all correct.

      A trap worth recording: `ATTN_DATA_DIR` alone does NOT move a daemon off
      production. With it — plus `ATTN_DB_PATH`, `ATTN_SOCKET_PATH` and
      `ATTN_WS_PORT` — exported, `attn profile resolve --json` still reported
      `~/.attn` and port 9849, so `attn daemon ensure` would have driven the
      production daemon. Only `ATTN_PROFILE` relocates it. Resolve and check
      before any lifecycle command, not after.

      **Harness fidelity.** `writeAsClient` now substitutes an ST for each
      dropped marker instead of dropping the bytes outright. Dropping them
      modelled a client whose decoder never flinches, and that blindness is what
      hid class 3 from the mirror gate; the substitution keeps `133;A`
      grid-inert (the pin skew) while reproducing the abort the real wasm
      performs.

#### Pin skew: `OSC 133;A` is not grid-neutral, and the two ghosttys disagree

Stripping markers from the worker terminal used to rest on the claim that they
produce no cells, so both grids land in the same place either way. Half of that
is false. Measured against the NATIVE ghostty the worker links, `OSC 133;A` with
the cursor mid-line performs a line break — ghostty's "a prompt starts on a fresh
line" rule. `0\x1b]133;A\x1b\\` leaves the cursor at `(0,1)`; the same stream
without the marker leaves it at `(1,0)`. `B`, `C`, `D` and unknown subtypes are
neutral; only `A` is not.

The conclusion that first looked obvious — that the worker should therefore be
fed the marker so both sides break together — is WRONG, and the corpus caught it.
The app does not render the ghostty the worker links. `ghostty-vt-native.pin`
records a native pin of `ab0b9da` against the frontend's ghostty-web at `29d4aba`
and says converging them is a follow-up, and at that older pin the wasm model
does NOT break the line. Feeding markers to the worker terminal makes the worker
break where the real client does not, which is the divergence this phase exists
to remove. Verified both directions: with the marker withheld the wasm replay
agrees with the worker; with it written, the same entry goes red in the Go
recording and the wasm replay at once.

The client's indifference is the wasm build's own, not something the app arranges:
the frontend never filters markers out before writing. `terminalOsc133.ts` tees —
its segments span the whole `ESC ] 133 ; … BEL`, and `GhosttyTerminal.tsx` writes
`segment.bytes` through to the wasm terminal and calls `blockStore.applyMarker`
separately. So the marker bytes do reach the model, and it simply does not act on
them at this pin.

So today's disposal is correct and load-bearing rather than incidental, and
`internal/pty/testdata/kitty_rewrite_corpus.json` pins it under
"a prompt marker after output with no trailing newline" — output with no
trailing newline, then a prompt marker, recorded with the worker NOT breaking
the line and replayed into real wasm to prove the client agrees.

Three consequences worth carrying forward:

- **The Go-side client model had to be corrected, not the feed path.** The
  corpus replay and the mirror fuzz targets stand a native terminal in for the
  frontend, and a native terminal handed the raw wire acts on OSC 133 when the
  real client would not. `writeAsClient` now drops recognised markers on the way
  into that stand-in. The wasm replay is the authority; the Go model exists to
  agree with it.
- **A bounded live divergence remains, and it is the pin skew, not the design.**
  A marker the segmenter replays as plain rather than extracting — a malformed
  terminator (CAN, SUB, a stray ESC), or an introducer that was never in ground
  — reaches the worker terminal as ordinary bytes, and the native build acts on
  it while the wasm client does not. Only `A` matters, only mid-line, and only on
  a malformed stream. It closes when the pins converge; until then it is a known
  limit rather than a bug to chase, and `writeAsClient` deliberately does not
  paper over it.
- **A wasm pin bump can flip the whole conclusion, so it is tripwired.** Every
  argument above rests on the shipped ghostty-web ignoring `133;A`, which is a
  property of one pin and not a guarantee. The corpus entry is the guard: its
  WIRE carries the marker, and the wasm parity test replays that wire RAW — no
  `writeAsClient`, no filtering — into the real shipped module, so it passes only
  while the actual wasm still treats the marker as grid-inert. A pin that
  implements prompt-start reddens it on the next run, which reopens the disposal
  question with evidence instead of letting a grid drift in production. Note the
  asymmetry on purpose: the Go-side stand-in is shimmed, the wasm witness is not.

### A3 — protocol + frontend rendering

- [x] Protocol: placement/blob events + snapshot placements
      (`main.tsp` → `make generate-types` → `constants.go` ProtocolVersion →
      `useDaemonSocket.ts` — all three lockstep spots). ProtocolVersion 205.
- [x] Frontend: placement store with block-style anchoring/reanchor, blob
      cache, textured-quad pass in `GhosttyWebGlRenderer`.
- [x] Blob transport decision with measured emitter sizes (the receipt for
      the cap and for frame-vs-event).
- [x] Packaged harness scenario — `real-app:scenario-terminal-kitty-image`.
      Deliberately out of `scenarioCatalog.mjs`: it restarts the profile daemon
      twice to move `ATTN_KITTY_STORAGE_LIMIT` in and out of the worker's
      environment, the same reason `scenario-automation-scheduled-cleanup` sits
      outside the catalog.
- [x] Restore path: snapshot placements seed the store after the dump write
      (blocks precedent), verified live across detach/reattach and a full app
      restart against a live daemon. Moved up from A4 — the work landed here.
- [x] Remote-session verification via the OrbStack VM. Passes end to end, and
      found the one A3 defect that only a remote session could show: the hub
      dropped every relayed kitty event. See "A3 verification record" below.

#### A3 verification record

Evidence behind the boxes above, so a later reader can tell what was actually
observed from what was assumed.

**Packaged harness (`real-app:scenario-terminal-kitty-image`).** Writes a
raw-bytes kitty APC into a shell pane from a file (never through `write_pane`
JS strings — an escape does not survive shell quoting), then asserts through
the `get_pane_placement_state` bridge action. It covers: the placement appears
with its blob resident and visible; it rides the text it sits in across two
scroll bursts; the program's own `a=d` empties the set; and — with the
override absent — an identical session produces no placements at all, which is
the shipping default. The first APC and the delete carry `q=2`: without it the
terminal answers `\x1b_Gi=<id>;OK\x1b\\` on the PTY, and at a shell prompt with
nobody reading, that reply is typed into the next command line. Real emitters
either set `q` or read the reply; kitty behaves the same way.

**Live tier (real emitters, throwaway profile).** chafa 1.18.2 and timg 1.6.3
against a 3000x2000 photo both produced resident, visible placements
(240x160 px and 270x180 px). Scrolling held the anchor; switching to another
session and back preserved the placement; a full app restart against the live
daemon restored it from the attach snapshot with the blob re-pulled; `a=d`
emptied the set.

**Identity epoch (review fix).** Image identities are per terminal INSTANCE, not
per process: `internal/pty` mints a random epoch when a session's ghostty
terminal is built and folds it into every generation that leaves the worker (the
placement read and the image serve, the only two exits). Ghostty's own stamps
restart with each worker process while a session id does not — `runtime_respawned`
replaces the worker, and so do a daemon restart and a revive — so raw stamps
would let a replacement worker describe (same session, same image id, same
generation) for different pixels, and the app's blob cache and GPU textures key
on exactly that. With the epoch, a respawned worker can never mint an identity a
client still holds pixels for. The window is `[2^32, 2^52)`: generations ride
JSON into JS Numbers, exact only to 2^53 and dropped outright past
`Number.MAX_SAFE_INTEGER` by the binary-frame decoder, so starting below 2^52
leaves 2^52 of stamp headroom (a stamp moves by one per storage mutation), while
the 2^32 floor keeps every epoched identity disjoint from a raw one. No protocol
change and no frontend change — the frontend already treats a generation as
opaque, so a fresh epoch is a new cache key by construction.

**Geometry gap (A4 input, measured not guessed).** chafa asked for a 30x14
cell area and emitted a 240x160 px image, i.e. it assumed roughly 8 x 11.4 px
cells, because the PTY reports no `ws_xpixel`/`ws_ypixel`. The real cell is
about 9 x 22.6 CSS px, and the client draws image pixels as CSS px, so on a 2x
display the image lands at about half its intended row height and twice its
native size in device pixels. Plausible, not pixel-perfect — exactly what the
design predicted. Reporting pixel geometry on the PTY is the fix and belongs
with the flip.

**Wrap anchoring gap (A4 input).** In a pane whose prompt wraps (a long cwd),
the first scroll after a placement moves its mapped buffer row by one; every
later scroll holds. Short-prompt panes never drift. It is not a race — two
reads 500 ms apart agree. Root-caused and fixed in A4 (resize reflow
asymmetry; see the decision record there), and the harness scenario now asserts
the strict invariant on both bursts.

**Remote leg: passes end to end.** Against a real OrbStack VM daemon
(`attn-remote@orb`), the whole chain runs: the app opens a shell session on the
endpoint, `cat` of a kitty APC file on the VM reaches the VM's worker, that
worker describes the placement, the hub relays the description, the app pulls
the blob it has no pixels for, and the image draws — a checkerboard, confirmed
in a native window capture rather than from state alone. The program's own
`a=d` then empties the set over the same relay, so the way out is proven too.
The generation on the wire was epoch-folded (2762831881943625), which is the
identity epoch above working on a freshly spawned remote session. Images were
on for that run only because the VM's daemon was started by hand with
`ATTN_KITTY_STORAGE_LIMIT` in its environment — the unsupported route, and the
reason this leg proves the pipeline rather than the switch: the hub forwards a
fixed env allowlist, so a remote daemon has no supported way to be told to
store images at all. Supplying one is what A4's remote item owes.

**The A3 defect it found.** The hub dropped every relayed kitty event. Its
`forwardsRawEvent` allowlist (`internal/hub/manager.go`) never listed
`kitty_placements` or `kitty_image_result`, so `consumeRemote` discarded both
before the daemon's routing for them could run. That routing was correct and
unit-tested, which is what made the hole invisible: a client attached straight
to the remote daemon received placements, the same session through the hub
received nothing, and every test stayed green. Fixed on this branch, with the
two events pinned by name so the allowlist cannot lose them again. Only a
remote session can show this — the whole class of defect is why the leg is
worth running.

**What first blocked the leg is an environment bug, pre-existing on main and
out of scope here.** A profile derives its WebSocket port from its name, so the
same profile on the host and on the VM lands on the same port, and OrbStack
republishes the VM's listener on the host's localhost whenever that port is
free. The local daemon's own bind then fails, is logged at INFO, and is
ignored — so the app silently attaches to the VM's daemon while the CLI keeps
talking to the local one, and nothing in either surface says so. A bind failure
on the daemon's own port has to be fatal. That fix ships as its own PR; it is
not an images problem and does not belong in this one.

Two more remote-side gaps found on the way, each worth fixing independently:

- `attn daemon stop` cannot run on a stock Debian remote: its lock check shells
  out to `lsof`, which is not installed, so it refuses with
  `could not verify pid N holds the daemon lock`. The hub's own stop script
  uses `ss` and works.
- An endpoint whose remote binary does not match the local one parks in
  `binary_mismatch`, and every forwarded command is then refused with
  `endpoint not found: <id>` — for an endpoint the same daemon reports as
  `connected`. The text is misleading rather than wrong, and the state is easy
  to reach: any local edit that moves the source fingerprint while a remote
  daemon is already running, since the hub only reinstalls the remote binary
  when it starts one. The remedy is a Sync click, which the harness cannot
  trigger.

**What the leg establishes about the relay itself.** The remote daemon's log
records the hub's hello verbatim — `kind="hub"` with
`capabilities=[workspace_sessions kitty_images]`. The relay asks for image
descriptions and never claims `binary_pty_output`, which is the capability
split's whole point: placements and blobs cross the relay as JSON.

### A4 — enable, restore, remote, receipts

Synthesis defects are known and deliberately deferred to here. All are
unreachable while the storage limit is 0 — nothing dispatches, so the grid never
moves and `writeAPC` returns early — and all become live the moment the limit is
flipped. None may ship with the flip.

`FuzzKittyWireMirror` is the target that reaches them: it runs the mirror
property with kitty live. All of them are now closed — the two placement-diff
items and the stamp-claim item by fixing the accounting, the CHA-under-margins
item by describing the column relatively, and the two MEASUREMENT items the later
soaks turned up (the over-tall scroll and the margin-box scroll) by tripwire
resyncs rather than cleverer synthesis. Soaking is part of this phase's work, not
A2's.
(The pin skew noted under A2 is NOT one of these — it is a live limit today,
and gated on the two ghostty pins converging rather than on the storage flip.)

- [x] **CHA is wrong under DECLRMM + origin mode.** Synthesis ended with an
      absolute column move, and a client with left/right margins enabled
      measures `CHA` from the left MARGIN while the worker reports a column
      from the screen edge. `\x1b[?69h\x1b[4;14s\x1b[?6h\x1b[3;2Hxy` plus a
      placement puts the worker at column 11 and the client at 13.
      **Decided: relative moves, the same doctrine the rows already followed** —
      `CUF`/`CUB` by `movedCol - col`, so the column is expressed as a distance
      from a position both terminals already hold rather than against a frame
      the worker cannot see. Corpus entries: the repro above, the same margins
      with origin mode OFF (measured NOT to displace an absolute column at this
      ghostty pin — margins alone are not enough — kept so the two modes are
      pinned apart), and a wide placement pushing the cursor right inside
      margins.
- [x] **An undescribed image forces a snapshot on every occurrence.** A kitty
      APC introduced from inside another sequence reaches the terminal whole
      and places an image the wire cannot describe, so the feeder resyncs
      (`kitty_undescribed_image`). Correct but blunt: a producer that emits
      images from a non-ground state would re-push a snapshot per image.
      **Decided by measurement, not by code: the blunt resync is the accepted
      cost.** The emitter sweep (chafa, timg, kitten icat; ~600MB of captured
      output) found ZERO unextracted APCs in any default configuration — every
      emitter starts its APC from ground, which is the state the segmenter can
      cut from. The one exception is `chafa --passthrough`, auto-triggered by a
      stale `$TMUX` or `TERM=screen*`, which costs one snapshot per still image;
      attn sets `TERM=xterm-256color`, so the default path is ground. The
      wire-side sequence-abort byte that was sketched for this stays rejected —
      it would buy nothing measurable (see the segmenter's header).
- [x] **The undescribed-image check only sees images APPEAR.** The end-of-feed
      comparison resynced on `delta.Added` alone, because `Updated` looked like
      scroll noise: `ViewportCol`/`ViewportRow` are viewport-relative, so
      ordinary scrolling moves every live placement. The cost was two
      divergences that stayed silent on a verbatim APC — a re-place of an
      existing `{ImageID, PlacementID}` at a new spot, and a retransmission
      that changes the image content under a live id (`ImageGeneration` moves,
      the placement key does not). **Decided: include `Updated`.** The
      scroll-noise worry does not apply, because a plain scroll does not move
      ghostty's kitty stamp and this check runs only when the stamp moved on
      bytes nothing accounted for. See the decision record below.
- [x] **A placement that appears and dies inside one chunk is invisible.** The
      end-of-feed check runs ONE diff per feed call, against the placement set
      from before it. An image that is displayed and then deleted in the same
      PTY chunk left that set unchanged, so `Added` was empty and no resync
      fired — while the scroll the placement caused was still on the worker's
      grid and never reached the wire. Found by `FuzzKittyWireMirror` at ~30s
      on a transmit-and-display followed by `\x1b_Ga=d\x1b\\`, which reports
      `gen 0->4, added=0 removed=0 updated=0` and leaves the worker at `(3,1)`
      against the client's `(1,0)`. It needs no exotic stream: PTY reads are
      4 KiB and up, so an emitter that draws an image and clears it lands both
      in one read. **Decided: resync when the stamp moved and the diff found
      nothing at all** — the stamp is the only witness such a chunk leaves. See
      the decision record below; this and the item above were one choice.
- [x] **An extractable APC erased an earlier undescribed one in the same
      chunk.** `writeAPC` claimed the stamp wholesale (`f.generation = stamped`)
      and read its own "before" stamp AFTER the chunk's earlier plain bytes had
      already reached the terminal. So an undescribed APC followed by ANY
      extractable APC in the same chunk left the end-of-feed check nothing to
      see: the stamp read as accounted for, `writeAPC` observed nothing (its
      own before and after are equal when the second APC is a no-op), and no
      resync fired. Found by `FuzzKittyWireMirror` at ~8m on a placement APC
      terminated by a stray ESC — which ghostty DISPATCHES, so the image lands
      while the segmenter replays the bytes as plain — followed by `\x1bi` and
      an empty `\x1b_G\x1b\\`; the worker ended at `(2,1)` against the client's
      `(0,0)`. It reproduced identically with the old `Added`-only predicate: a
      blind spot in the ACCOUNTING, not in the predicate.
      **Decided: settle before every described dispatch.**
      `settleUnaccounted` — read the stamp, and when it moved, claim it,
      observe, and charge the result through `unaccountedResync` — now runs
      both at the end of a feed and at `writeAPC`'s ENTRY, before the pre-ST
      and the cursor pin. A stamp is one number for the whole terminal, so the
      only way a dispatch can claim just its own move is for everything earlier
      to be settled first. `writeAPC` then reads its pre-dispatch generation
      from `f.generation` instead of crossing into ghostty again, so the dark
      path pays no extra cgo. Corpus entries: "an undescribed image, then an
      extractable apc in the same chunk" (the fuzz stream) and "an undescribed
      placement and a described one in the same chunk"; the second is also
      pinned byte-for-byte by
      `TestWireFeedStillDescribesTheAPCThatSettlesAnUndescribedOne`, because a
      resync must not stop the settling APC from being described.
- [x] **A synthesized scroll taller than the screen loses history.** Synthesis
      expresses the measured scroll as `CSI n S`, and ghostty CLAMPS `SU` to the
      height of the scroll region: on an 8-row screen, `CSI 47 S` pushes 8 rows
      into scrollback, while the placement that caused it pushed 47. The
      viewport and the cursor still agree — only the client's history comes out
      short, by exactly `scroll - rows` rows, which the user meets as missing
      scrollback rather than as a wrong screen. Measured on
      `\x1b[2;2Hkeep` + a 2x2 image placed with `r=53` + `\r\ntail`: worker
      history 55 rows, client 16. Bisected on the same shape — `SU 6` agrees,
      `SU 14` diverges — so the threshold is exactly the screen height. Cheap to
      reach because kitty's `r=` lets a small image claim any number of rows.
      Found by `FuzzKittyWireMirror` at ~1m30s after the settle fix; it predates
      both this phase's fixes, which never touch `writeAPC`'s measurement.
      **Decided: resync past the boundary, measured rather than assumed.** The
      boundary is the screen height exactly — the largest scroll one `SU`
      reproduces byte for byte, pinned on both sides by
      `TestWireFeedSynthesizesTheLargestScrollOneSUCarries`:

      | screen | last agreeing | first diverging |
      | --- | --- | --- |
      | 8 rows | `r=14` → `SU 8` | `r=15` → `SU 9`, a row of history lost |
      | 12 rows | `r=22` → `SU 12` | `r=23` → `SU 13`, a row of history lost |

      A DECSTBM region does not shrink it: measured on a 12-row screen with a
      4-row region, a placement inside it never scrolls more than one row no
      matter how tall `r=` makes it, so the region case cannot reach the clamp.
      Past the boundary `writeAPC` now emits `kitty_layout_scroll_clamped`
      instead of the clamped `SU`. Splitting the scroll into region-height
      `SU`s was rejected: no measured emitter draws an image taller than the
      screen, so the split would be unexercised cleverness on a path a resync
      already covers. Corpus entry: "placement scrolling further than one su
      can carry", resync-exempt from replay.
- [x] **A scroll confined to the left/right margin box is measured as zero.**
      With DECLRMM on (`\x1b[?69h` plus `DECSLRM`), a placement at the bottom of
      the margin box scrolls only the columns inside it, and the tracked-ref
      measurement does not see that as movement: `scrolled` comes out 0, the
      wire carries no `SU` at all, and the client's text stays where it was.
      Measured on a 20x8 screen, margins `\x1b[4;14s`, `top` written at column 1
      and `xy` at the bottom row inside the box, then a 2x2 placement:

      | modes | wire | worker | client |
      | --- | --- | --- | --- |
      | margins + origin | `ESC\ CSI 1 A CSI 5 C` | `xy` at row 5 | row 7 |
      | margins, no origin | `ESC\ CSI 1 A CSI 2 C` | `xy` at row 5 | row 7 |
      | origin, no margins | `ESC\ CSI 2 S CSI 1 A CSI 2 C` | row 5 | row 5 |
      | neither | `ESC\ CSI 2 S CSI 1 A CSI 2 C` | row 5 | row 5 |

      DECLRMM alone is the trigger; origin mode has nothing to do with it. The
      cursor agrees in every row — this is the TEXT moving under an agreed
      cursor, which is what separates it from the two cursor defects above. In
      the margin rows `top` survives at row 0 on the worker, outside the box:
      proof the scroll is confined to the margin columns rather than missing.
      Found by `FuzzKittyWireMirror` at ~1m48s once A4's margin corpus entries
      gave it margins to mutate. Predates the relative-column fix — measured by
      restoring the absolute `CHA` and re-running the same probe, which shows
      the identical text divergence with the column defect stacked on top.
      **Decided: resync while the mode is on.** `writeAPC` reads DECLRMM through
      the new `ghosttyvt.Terminal.LeftRightMarginMode` and emits
      `kitty_layout_margin_mode` on every described dispatch while the mode is
      set, without asking whether that dispatch scrolled the box — nothing can
      tell those apart from this side, which is the defect itself. Measuring the
      scroll in a frame that includes a margin-box scroll was rejected on the
      same ground as the `SU` split above: no measured emitter enables DECLRMM,
      so the blunt tripwire costs nothing real, and a margin-aware measurement
      would be unexercised machinery. The dispatch is still described in full —
      the cursor moves are the part margins do not spoil, and they keep a client
      that has not re-attached yet closer to the truth.
      Cost if some future emitter does set the mode: one snapshot per image,
      the same bill the undescribed-image item accepts.
      `TestWireFeedResyncsWhileLeftRightMarginsAreSet` pins it against a
      no-margins control that must stay silent and byte-equal; corpus entry
      "placement scrolling the box while left and right margins are set". The
      three margin entries from the CHA fix now record this resync instead of a
      replayed grid — the mode is covered wherever it appears, and the relative
      column they were written for is pinned by every non-margin entry and still
      governs a client whose mode read fails.

**Decision record — what an unaccounted kitty mutation costs.** A resync exists
for grid SCROLL the wire never expressed, never for knowledge of the placement
set: the set reaches the client on its own through the placement fan-out,
whatever happened here. And only bringing a placement into existence or putting
a live one somewhere new can scroll the grid — retiring one gives back no rows.
So when ghostty's stamp moves on bytes the wire carried verbatim:

- no delta at all → resync, `kitty_stamp_without_delta`. The sets on both sides
  of the diff are equal, so nothing but the stamp can witness the placement that
  appeared and died.
- any `Added` or `Updated` → resync, `kitty_undescribed_image`. A retransmission
  that scrolls nothing is charged with it too: this check cannot tell one from a
  re-place, and it does not need to.
- nothing but `Removed` → silent. That is the alternate-screen prune and the
  undescribed delete, and it is the ONLY exemption.

Accepted cost: plain bytes that scroll a live placement BEFORE an undescribed
APC in the same chunk read as `Updated` and resync. It takes an undescribed APC
to reach at all — one introduced from a non-ground parser state — and how rare
those are in real emitters is the measurement the item above owes.
`unaccountedResync` in `internal/pty/wirefeed.go` is the rule; the corpus
entries named "an undescribed …" and
`TestWireFeedResyncsOnEveryUnaccountedMutationButAPureRemoval` are its truth
table, red in both directions (measured: reverting to `Added`-only reddens the
three resync rows, resyncing on every delta reddens the two silent ones).

- [x] **Flip the storage limit on.** `kittyStorageLimit` returns 320,000,000
      bytes with nothing in the environment. **Receipt:** that is ghostty app's
      own default and within 5% of kitty's, and the failure past it is total
      rather than gradual — ghostty refuses a single image larger than the WHOLE
      limit outright (`addImage` → `error.OutOfMemory`), and every emitter in the
      sweep transmits with `q=2`, which suppresses kitty's response, so an
      over-limit image does not degrade, it silently does not appear. The largest
      legitimate single image the sweep produced is ~81.4MB (a full-screen Pro
      Display XDR capture at 2x), so the default clears the biggest real image by
      about 4x. Under the limit, hitting it is ordinary: ghostty evicts the
      oldest image to admit a new one, which is what an animation does all day.
      `ATTN_KITTY_STORAGE_LIMIT` is now a tuning override rather than a feature
      flag, and an unparseable value falls back to the DEFAULT rather than to
      zero — a typo in a tuning variable must not silently turn the feature off.
      `=0` remains the escape hatch, and `FuzzKittyWireMirrorShipping` still
      guards it.
      **Named refusal logging.** A limit someone can hit is a limit they must
      see, and this one is invisible everywhere else: kitty's own error goes
      nowhere under `q=2`. The worker is the only witness, so `writeAPC` judges
      one thing — a transmission that COMPLETED and moved no kitty generation —
      and logs the variable, its value, and the ask (`s`×`v`×4 for a raw format,
      payload bytes for PNG), saying in the same breath that eviction is not
      this case. Measured first, because most of the shapes that reach this path
      also fail to move the generation:

      | APC | generation |
      | --- | --- |
      | single transmission that fits | moves |
      | single transmission over the limit | **unchanged** — the one true positive |
      | chunked `m=1` that fits | unchanged on every intermediate, moves only on the completing `m=0` |
      | chunked over the limit | unchanged throughout, `m=0` included |
      | `a=q` query | unchanged |
      | `a=p` re-place, `a=d` of a live id | moves |
      | `a=d` of an id that is not there | unchanged |
      | eviction (a third image into a one-image store) | moves |

      So intermediate escapes are accumulated and never judged — an emitter
      sends dozens per image, and judging them would put a line in the log for
      ordinary output — queries and deletes are never judged at all, and
      eviction is invisible because admitting the new image moves the stamp like
      any other store. Pinned by
      `TestWireFeedLogsATransmissionTheStorageLimitRefused` and
      `TestWireFeedKeepsQuietForEverythingThatIsNotARefusal`.
      **Recorded observability gap, no machinery:** the limit is per TERMINAL,
      and a session holds a primary and an alternate screen, so the worst case a
      session can occupy is 2x the number above. Nothing tracks or reports total
      image memory across sessions today. If image memory ever shows up in a
      profile, that is the measurement to take first.
- [x] **Report pixel geometry on the PTY (`ws_xpixel`/`ws_ypixel`).** Emitters
      size images from it; without it chafa guessed ~8 x 11.4 px cells against a
      real 9 x 22.6 CSS px cell (measured in A3's live tier), and the worker
      terminal reported its own pixel size from a hardcoded 8x16 placeholder.
      Protocol 207.

      **The wire carries the pane TOTAL in device pixels, optional, 0 == absent
      == unknown.** `pty_resize` and its `pty_resized` echo each gained
      `xpixel?`/`ypixel?`. Total rather than per-cell because the total is what
      a client can actually measure and what `TIOCGWINSZ` speaks; device rather
      than CSS pixels because that is the unit an image is made of. A resize
      that measured nothing omits the fields rather than sending zeros, and the
      echo leaves them off rather than echoing zeros — a receiving client that
      read 0 as a real pane would size images against a degenerate cell. The
      daemon drops a pair it cannot represent (the kernel's winsize fields are
      uint16) and names it in the log, because a silently ignored geometry looks
      exactly like a client that never sent one.

      **Two conversions, each in one place.** Total → cell happens once, in
      `Session.resize` (`xpixel/cols`, `ypixel/rows`), and everything below it
      speaks cells: the new `ghosttyvt.Terminal.SetCellPixelSize` pushes the
      cell into the native terminal immediately at the current grid, and the
      winsize ioctl carries the total through unchanged. Device → CSS happens
      once, in `placementQuad`, before any clipping arithmetic mixes a
      placement's device-pixel box with the renderer's CSS-pixel cell metrics.
      Measured, not assumed: `WebGlTerminalRenderer.cellWidth/cellHeight` are
      **CSS** pixels — the canvas is styled `cols * cellWidth` and backed by
      that times `dpr` — so a fit multiplies by `dpr` on the way out and the
      draw path divides by it on the way in.

      **The session remembers the cell, not the total.** Only a fit measures a
      pane; the attach-time reconcile and the remount hydrate resize carry no
      pixels and arrive *after* a fit on every remount. A total is meaningless
      without the grid it was measured at, so the session keeps the derived cell
      and re-derives the total for a pixel-less resize. Spawn stays pixel-less
      by design — nothing has measured a pane yet, and the first fit always
      follows.

      **XTWINOPS is not the surface, and this was measured rather than
      assumed.** ghostty's VT core answers no `CSI 14/16/18 t` at all — the
      embedder is expected to encode those itself (`ghostty_size_report_encode`)
      — so nothing in attn answers them today and this change does not add one.
      What the library *does* emit is the in-band size report (DEC mode 2048),
      `ESC[48;rows;cols;height;width t`, whenever either factor moves; that
      report used to carry the 8x16 lie and now carries the truth, and it is
      what the Go tests assert against. Emitters that only know XTWINOPS fall
      back to `TIOCGWINSZ`, which is the half this change fixes for them.

      **v1 limitations, both deliberate.** Mixed-DPR multi-client is last-resize
      wins: the session holds one cell size, so two clients on displays with
      different ratios overwrite each other rather than each getting their own
      geometry. And a genuine cell change emits two mode-2048 reports for one
      resize (`SetCellPixelSize` then the grid resize); the steady state emits
      one, because setting an unchanged cell is a no-op.
- [x] **Fix the one-row anchoring drift on a wrapped prompt row. Root cause:
      resize reflow asymmetry.** The worker reflowed on resize
      (`ghosttyvt.Terminal.Resize`) while every client frame deliberately does
      not — the app's fit and its historical replay both write `ESC[?7l`,
      resize, `ESC[?7h` (`resizeGhosttyWithoutReflow`), load-bearing since
      PR #306 for the block store and replay at historical geometry. After any
      width-changing fit with wrapped content on screen, the same bytes then
      occupied a different number of rows on the two grids, and the client's
      mapping mixes the two frames: `bufferRow = clientScrollback +
      workerViewportRow`. That is also why block anchoring and annotation
      alignment clear on a width change.

      **Fixed by making every frame take the no-reflow path.**
      `ghosttyvt.ResizeNoReflow` runs the client's own recipe inside the worker
      terminal — read DEC mode 7, and only when it is on write `ESC[?7l`,
      resize, `ESC[?7h` — and `Session.resize` calls it. The frontend's
      `resizeLocal` joins them: its live branch (the daemon's `pty_resized`
      echo, the only resize a non-owner hub mirror ever sees) still resized
      plainly, so a mirror would have held a third frame.

      **Measured.** A 36-character wrapped prompt across a 20→40 column resize
      occupies one row on a reflowing worker and two on a client; δ reached 2
      rows with more wrapped lines and flips sign on narrowing. It self-corrects
      once enough rows scroll into history — the reported "drift" was the
      correction, not the error. `TestResizeKeepsAPlacementsBufferRow` pins the
      repro (buffer row 3→4 before the fix);
      `TestSessionResizeKeepsTheWorkerFrameEqualToAClientFrame` pins the grids
      themselves against a control terminal driven by the client's recipe,
      including alt screen and a program that turned DECAWM off.

      **Rejected.** Making the client's fit reflow instead: the no-reflow path
      is load-bearing for the block store. A wire-side absolute anchor: an
      absolute row is meaningless while the two frames disagree, which is the
      actual defect. Reattaching on every resize: a full dump per drag, and a
      visible reset.

      **Cost, honestly.** Ghostty's no-reflow resize was already the client's
      path but is newly exercised on the worker, where the restore dump and
      approval classification read from. The frame-parity test is what covers
      it, on a real spawned session rather than a hand-built terminal.
- [x] **A supported way to turn images on — now off — for a remote daemon.** A3
      ran the remote leg end to end, so what was left was the switch rather than
      the pipeline: the hub forwards a fixed env allowlist and the storage-limit
      override had no route through it. **Decided: forward
      `ATTN_KITTY_STORAGE_LIMIT` under its own name** (`remoteShellEnvScript` in
      `internal/hub/ssh.go`), not as an `ATTN_REMOTE_`-prefixed twin like the
      socket and port overrides — the hub and its remotes should run the same
      image budget, so one variable governs both ends. The flip inverts what
      this is for: the default now travels by being the default on both sides,
      and what needs a route is the way OUT. `=0` is non-empty, so it exports
      and disables the remote exactly as it disables the hub. Pinned by
      `TestRemoteShellCommandCarriesTheKittyDisableToTheRemote` and its
      unset-stays-silent twin.
- [x] AGENTS.md: write the new truth; changelog fragment (user-visible). Written
      in A3 for the dark default and rewritten at the flip: the terminal section
      now states that kitty images are worker-authoritative and ON by default,
      names the 320MB limit and the `=0` hatch, keeps the reasons the client
      never parses kitty and the relay never advertises `binary_pty_output`, and
      keeps sixel's absence. One user-facing fragment covers the PR.
- [ ] **A placement on a row that is already full: the deferred wrap synthesis
      cannot measure.** Found by `FuzzKittyWireMirror` after the A4 work landed,
      as `62f19a45d7a5c8c7`: on a 20x8 grid, print exactly 20 characters, place
      an image, print one more. The worker ends at (19,0) and the client at
      (1,1) with no resync between them.

      **Root cause, measured.** Printing the twentieth character leaves the
      cursor at the last column with a wrap PENDING — the next printable byte
      wraps before it lands. The placement consumes that pending wrap. But
      `CursorPos()` reports column 19 both before and after, because the pending
      bit is not part of a position, so the measured delta is zero and the wire
      carries no movement at all. The client, which never sees the APC, keeps
      its pending wrap; its next character wraps and the worker's does not. The
      same stream with the cursor mid-row is correct — `CSI 1 C`, both grids
      agree — which is what isolates the pending bit as the whole cause.

      **It predates this phase's flip.** Reproduced unchanged at `17540431`
      (kitty still dark by default) and at `28d360cd` (before the pixel-geometry
      commit joined the branch), so neither the flip nor the geometry merge
      introduced it; the earlier 15m soak simply never reached the input. No
      corpus entry was added, because a corpus entry would pin the wrong grid.

      **Shape of the answer, not yet decided.** Either the measurement grows to
      see the pending bit — which needs a native accessor ghostty does not
      expose today — or `writeAPC` treats an anchor sitting at the last column
      with a wrap pending as another thing it cannot measure and resyncs, the
      same answer the margin box and the over-tall `SU` got.

## Open questions

- ~~Alt-screen snapshot semantics: do snapshot placements carry a screen
  flag?~~ Answered in A3: no flag, and none is needed. The dump and the
  placement set are taken from the same terminal at the same instant and both
  describe whatever screen is active, so a client that writes the dump and
  seeds placements from the same `attach_result` cannot disagree with itself.
  Blocks needed a flag because they are primary-only; images are not.
- Animations (`a=a`): the diff would emit a blob update per frame — a wire
  flood. V1 declares them out of scope; decide whether to coalesce or drop,
  with an event-volume tripwire either way.
- Unicode-placeholder (virtual) placements: rendered via placeholder cells,
  not cursor placements. Likely excluded from v1 as a named limitation —
  confirm what the diff exposes for them.
- Does limit-0 actually silence `a=q`? A1's first verification step; the
  response-drain filter is the fallback.
