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
    │                            └─ synthesize: SU×scroll + CUU/CUD + CHA into the
    │                                 wire chunk (relative and column-only: absolute
    │                                 row addressing is measured from the scroll
    │                                 region under origin mode)
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

### A2 — worker: segmenter, observation, synthesis (feature dark)

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
- [x] Parity corpus (29 entries, replayed into native ghostty and the shipped
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
- [ ] **Stripping an APC desyncs the client on two independent axes.** Found by
      `FuzzKittyWireMirrorShipping`, so both are defects in the SHIPPING
      configuration, not deferred ones. The worker receives the APC and the wire
      does not, and the bytes the client never sees were doing work.

      **1. The UTF-8 abort.** The APC's leading ESC aborts a UTF-8 sequence the
      client is still holding open. Reproduced by
      `"0000000000000000000\xc5\x1b_G"` (and `\xe1`, a 3-byte lead). Measured,
      holding the cursor at column 19:

      | what follows the stripped APC | result |
      | --- | --- |
      | nothing (quiesced) | worker has U+FFFD, client has none — transient |
      | an ASCII byte | heals |
      | a valid continuation byte | **permanent**: worker `\uFFFD\uFFFD`, client `ť` |
      | more text | heals |

      Emitting `ESC \` (ST) on the wire where the APC was fixes every one of
      these: ST from ground is a no-op and its ESC aborts the client's pending
      sequence exactly as the APC's did for the worker.

      **2. The pending-wrap row.** Independent of the first, and NOT fixed by
      ST. With the cursor at exactly the wrap column and an incomplete UTF-8
      byte pending, worker and client end with identical text and cursor rows
      one apart. Measured at 20 columns, feeding `<n zeros>\xe1\x1b_G`:

      | zeros | baseline | with ST on the wire |
      | --- | --- | --- |
      | 18 | agree | agree |
      | 19 | diverge (class 1) | agree |
      | 20 | diverge: worker `(1,1)`, client `(1,2)`, same text | **still diverges** |
      | 21 | agree | agree |

      Soak evidence: 3 x 3 min on the fixed harness is NOT clean. First run
      found class 1 at 96s from a cold corpus; once the fuzz cache knows the
      shape it reappears in ~2s, and with ST applied locally the wall moves to
      26s and lands on class 2.

      Neither fix is taken here. Class 1's changes the wire contract — whether
      synthesis may emit bytes when the grid did not move, where `appendCSI`
      deliberately emits nothing at zero. Class 2 is not yet root-caused, and a
      cursor-row difference with matching text points at deferred-wrap state the
      wire has no way to carry.

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

- [ ] Protocol: placement/blob events + snapshot placements
      (`main.tsp` → `make generate-types` → `constants.go` ProtocolVersion →
      `useDaemonSocket.ts` — all three lockstep spots).
- [ ] Frontend: placement store with block-style anchoring/reanchor, blob
      cache, textured-quad pass in `GhosttyWebGlRenderer`.
- [ ] Blob transport decision with measured emitter sizes (the receipt for
      the cap and for frame-vs-event).
- [ ] Packaged harness scenario.

### A4 — enable, restore, remote, receipts

Four synthesis defects are known and deliberately deferred to here. All are
unreachable while the storage limit is 0 — nothing dispatches, so the grid never
moves and `writeAPC` returns early — and all become live the moment the limit is
flipped. None may ship with the flip.

`FuzzKittyWireMirror` is the target that reaches them: it runs the mirror
property with kitty live, and it is knowingly red until the last two below are
decided. Soaking it is part of this phase's work, not A2's. (The pin skew noted
under A2 is NOT one of these — it is a live limit today, and gated on the two
ghostty pins converging rather than on the storage flip.)

- [ ] **CHA is wrong under DECLRMM + origin mode.** Synthesis ends with an
      absolute column move, and a client with left/right margins enabled
      measures `CHA` from the left MARGIN while the worker reports a column
      from the screen edge. `\x1b[?69h\x1b[4;14s\x1b[?6h\x1b[3;2Hxy` plus a
      placement puts the worker at column 11 and the client at 13. Fix by
      making the column move relative (`CUF`/`CUB` from the pre-APC column) or
      by resyncing when margins are on; either way it needs a corpus entry.
- [ ] **An undescribed image forces a snapshot on every occurrence.** A kitty
      APC introduced from inside another sequence reaches the terminal whole
      and places an image the wire cannot describe, so the feeder resyncs
      (`kitty_undescribed_image`). Correct but blunt: a producer that emits
      images from a non-ground state would re-push a snapshot per image.
      Measure real emitters before deciding whether that needs a cheaper
      repair (a wire-side sequence-abort byte was sketched and rejected as
      unproven; see the segmenter's header).
- [ ] **The undescribed-image check only sees images APPEAR.** The end-of-feed
      comparison resyncs on `delta.Added` alone, because `Updated` is scroll
      noise: `ViewportCol`/`ViewportRow` are viewport-relative, and observation
      happens only on APC writes, so ordinary scrolling between two of them
      moves every live placement and would resync constantly. The cost is two
      divergences that stay silent on a verbatim APC — a re-place of an
      existing `{ImageID, PlacementID}` at a new spot, and a retransmission
      that changes the image content under a live id (`ImageGeneration` moves,
      the placement key does not). Decide at the flip: include `Updated` and
      accept the re-pushes, or key the diff on the fields a scroll cannot move.
- [ ] **A placement that appears and dies inside one chunk is invisible.** The
      end-of-feed check runs ONE diff per feed call, against the placement set
      from before it. An image that is displayed and then deleted in the same
      PTY chunk leaves that set unchanged, so `Added` is empty and no resync
      fires — while the scroll the placement caused is still on the worker's
      grid and never reached the wire. Found by `FuzzKittyWireMirror` at ~30s
      on a transmit-and-display followed by `\x1b_Ga=d\x1b\\`, which reports
      `gen 0->4, added=0 removed=0 updated=0` and leaves the worker at `(3,1)`
      against the client's `(1,0)`. It needs no exotic stream: PTY reads are
      4 KiB and up, so an emitter that draws an image and clears it lands both
      in one read. The generation stamp is the honest signal here — it moved
      four times while the diff saw nothing — but keying purely on the stamp
      resyncs on prunes too, which is what the `Added`-only rule was avoiding.
      Decide with the item above; they are the same choice seen twice.

- [ ] Flip the storage limit on (measured number, named limit errors surfaced
      through kitty's own response channel and the daemon log).
- [ ] Restore path: snapshot placements seed the store after the dump write
      (blocks precedent); live verification of detach/reattach and revive.
- [ ] Remote-session verification via the OrbStack VM.
- [ ] AGENTS.md: write the new truth; changelog fragment (user-visible).

## Open questions

- Alt-screen snapshot semantics: the dump serializes the active screen; do
  snapshot placements carry a screen flag (blocks are primary-only, images
  are not)? Decide in A3 with the restore work.
- Animations (`a=a`): the diff would emit a blob update per frame — a wire
  flood. V1 declares them out of scope; decide whether to coalesce or drop,
  with an event-volume tripwire either way.
- Unicode-placeholder (virtual) placements: rendered via placeholder cells,
  not cursor placements. Likely excluded from v1 as a named limitation —
  confirm what the diff exposes for them.
- Does limit-0 actually silence `a=q`? A1's first verification step; the
  response-drain filter is the fallback.
