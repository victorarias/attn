# Design: Full-fidelity server-authoritative restore (blocks first)

Spike output (2026-07-23) for the Phase 3 hold in
[2026-07-22-server-authoritative-terminal.md](2026-07-22-server-authoritative-terminal.md):
OSC 133 command blocks are lost on snapshot restore. Victor's framing: solve at
the foundation level — the serialization contract and the app's consumption of
it are both open to change, at any size.

**This doc is the design record (why + verified evidence + roadmap). The
implementation contract and live status live in the main plan's Phase 3a
section — start there; this doc does not track progress.**

## Principle

The VT dump is not the snapshot. The snapshot is **the serialized state of the
session's terminal**, of which the dump is one component. Two rules decide
where any piece of terminal state travels:

1. **Emulator-consumed state goes in the dump.** If the client-side emulator
   re-ingests it faithfully from VT bytes (grid, styles, modes, cursor,
   hyperlinks, potentially images), the dump carries it. Gaps here are
   serializer gaps — fixed in the formatter/carried patch/upstream.
2. **App-consumed state goes as structure.** If the consumer is not the
   emulator (the block store today; classifier state later), re-encoding it as
   VT bytes for a second parser to re-derive is the replay pattern this epic
   exists to kill. The worker owns it as first-class state and the snapshot
   carries it as typed data.

Blocks fall under rule 2 twice over: ghostty's retained OSC 133 state
(per-cell/per-row semantic class) cannot reproduce the marker payloads —
command text and exit codes are consumed at parse time — so no dump-side fix
can ever be faithful. The stream must be interpreted once, at the worker, and
the result serialized as structure.

## Spike-verified primitives (2026-07-23)

Verified empirically against the vendored libghostty-vt via
`internal/ghosttyvt/spike_trackedref*.go` (all green):

1. **Tracked grid refs** (`ghostty_terminal_grid_ref_track` /
   `ghostty_tracked_grid_ref_point`) pinned at the cursor keep resolving to the
   same content row across scrolling, scrollback pruning, and resize/reflow —
   and report a clean "no value" (never a wrong row) once the row's page is
   pruned.
2. **Coordinate alignment across restore:** a SCREEN-space row resolved at
   serialize time is a valid row index into a terminal rebuilt by writing the
   VT dump — including when scrollback was pruned before serializing, because
   SCREEN space is post-prune by construction. **No offset mapping is needed,
   ever.** Row counts match globally, not just at markers.
3. **Alt-screen:** a primary-screen ref resolves correctly (against the primary
   page list) while the alternate screen is active — the exact state the
   serializer runs in when a session sits inside vim at snapshot time — and
   alignment holds through alt-active serialize → restore → `1049l`.
4. Prune probe: `max_scrollback` prunes lazily at whole-page granularity (with
   cap=50, the prune fired between ~1.1k and ~5k rows and retained 782). The
   cap is a floor, not an exact count. Also found: `Snapshot.ScrollbackTruncated`
   is never set by `serializeLocked` — dormant field (see roadmap).

## Block architecture

### Worker (authoritative block table)

- **Segmenter:** port `app/src/utils/terminalOsc133.ts` (~180 lines) to Go,
  with its test corpus, as the worker's OSC 133 interpreter. Parity between the
  two parsers is test-enforced (shared fixture corpus) until the client parser
  retires (events phase).
- **Feed site:** the read loop in `internal/pty/session.go`, under `replayMu`,
  at the existing `s.ghostty.Write(data)` site. Fast path (no `ESC ] 1 3 3 ;`
  prefix in the chunk — the segmenter already has this bail) stays a single
  `Write`. Marker path: write per-segment; after each marker, read the ghostty
  cursor and pin a tracked ref.
- **Block table:** mirrors `TerminalBlockStore` semantics — pending/completed,
  self-heal on lost `133;D`, cap 200 — but positions are tracked refs (prompt,
  input start, output start, end) instead of row numbers + text anchors. The
  library moves the refs through scroll/prune/reflow; a block whose essential
  refs report no value is dropped at serialize (correct-or-absent, same
  philosophy as the client's anchor refusal). Payloads (command text from
  `cmdline_url`, exit code from `D`) are captured at parse time.
- **Alt-screen edge:** markers arriving while the alternate screen is active
  pin refs against the alt page list; blocks are a primary-screen concept, so
  record the active screen at pin time and drop alt-pinned blocks at serialize.
- **Serialize:** refs resolve to SCREEN-space rows inside the same
  `replayMu`-held section that produces the dump and `last_seq` — the snapshot
  becomes an atomic triple {dump, blocks, watermark}. Resolution cost is
  bounded (≤200 blocks × 4 refs, attach-frequency only).
- cgo stays inside `internal/ghosttyvt` (grows: `TrackCursor`, tracked-ref
  resolve/free, active-screen query — the spike files are the seed). The
  segmenter and block table are pure Go in `internal/pty`. The non-macOS stub
  has no ghostty and therefore no block table: snapshot-less attach already
  keeps client state; degradation is unchanged.

### Contract (TypeSpec, `main.tsp`)

`AttachSnapshot` gains:

```text
blocks?: AttachBlock[]
AttachBlock {
  id: uint64            // server-assigned, monotonic per session
  pending: boolean      // the currently-open block (no D yet), at most one
  prompt_row: int32     // dump-row space == client buffer rows post-restore
  input_row?, input_col?: int32
  output_start_row?: int32
  end_row?: int32       // exclusive, absent while pending
  command?: string
  exit_code?: int32
}
```

`make generate-types`; `ProtocolVersion` bump (constants.go +
`useDaemonSocket.ts` — three lockstep spots, re-grep after rebase).

### Frontend (seed on restore)

On the ghostty-snapshot attach path (`useDaemonSocket.ts`), after the dump is
written: `blockStore.seed(snapshot.blocks)` — completed blocks enter the store
with their server rows, `anchorText` computed locally from the restored buffer
(`selectionLineAtBufferRow`), the pending block re-arms `pending`, and the
store's `nextId` continues above the max seeded id. The live stream then keeps
appending blocks through the existing `parseOsc133` path, unchanged. No
snapshot → no seed → today's degradation, untouched.

### Event-ready (designed now, built later)

Server block ids are authoritative from day one; the snapshot is semantically a
*full-table sync*. The future increment is a `block_event` stream
(`opened | updated | completed | dropped`, carrying the same `AttachBlock`
shape) that makes the worker the only interpreter: the client stops deriving
blocks from bytes entirely, and per-block server features (agents reading a
command's output, block-scoped search) fall out naturally. The seed schema is
designed so events are additive — no breaking contract change when they land.

## Full-fidelity roadmap (rule-driven)

| State | Rule | Path |
|---|---|---|
| OSC 133 blocks | 2 | This design (Phase 3a) |
| OSC 8 per-cell hyperlinks | 1 | Serializer gap: formatter emits only cursor-current hyperlink state. Extend `ghostty-vt-native.patch` (or upstream) to re-open/close OSC 8 around cell runs in the dump. |
| Kitty/sixel images | 1 (likely) | Ghostty core retains full kitty-graphics state, and the C API exposes image storage, image data, and placement iteration with grid geometry (`kitty_graphics.h`). Path (a): re-emit kitty transmit+placement sequences in the dump — needs the WASM-pin client to ingest them (verify). Path (b): structured sidecar for the renderer. Mini-spike owns the choice; the contract admits either. |
| `ScrollbackTruncated` | — | Currently never set. Wire it from the page-prune signal or delete the field; decide during 3a. |
| Classifier text source → ghosttyvt | 2 | Existing follow-up, unchanged; the block table is a template for it. |

## Sequencing

1. **Phase 3a — block foundation** (new, before the flip): ghosttyvt API
   graduation, Go segmenter + parity corpus, worker block table + tracked
   refs, contract field, frontend seed, tests (segmenter parity; block
   round-trip incl. prune/reflow/alt-screen; `terminal-block-resize` Phase B
   green with snapshot attach on). Phase-sized, comparable to Phase 2.
2. **Phase 3 — flip + delete** proceeds exactly as already coded on
   `feat/server-auth-terminal-phase3` once 3a lands under it; the held
   scenario is the merge gate.
3. **Later phases:** hyperlinks in the dump; images (mini-spike first); block
   events + retire client-side block derivation.

## Decisions / accepted costs

- Two OSC 133 parsers (TS + Go) exist until the events phase; parity is
  fixture-tested, and the TS one is already stable/battle-tested.
- Block retention floor equals the scrollback cap at page granularity; blocks
  on pruned pages drop rather than risk wrong rows.
- Rejected: re-emitting OSC 133 sequences into the dump (rule 2 violation, and
  unfaithful — payloads unrecoverable from retained state). Rejected:
  replacing the VT dump with a fully structured grid snapshot (rewrites a
  healthy, emulator-verified layer to fix a gap beside it).
