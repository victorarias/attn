# Plan: Server-authoritative terminal (kill raw replay)

## Goal

Move attn's PTY restore path to the tmux/zellij model: the daemon-side worker
owns an authoritative parsed terminal (grid + scrollback), and a client attach
is served by **serializing that grid** — never by replaying the app's raw byte
stream. Live output keeps streaming as raw bytes exactly as today; only the
attach/restore path changes.

Why: nearly all of attn's terminal scar tissue (replay-vs-snapshot decision
tree in `buildAttachReplayPayload`, the 64KB replay tail, oracle verification,
seq/watermark race hooks, mid-replay resize cancellation, truncated-restore
warnings) exists to make *raw bytes* safe to re-parse on a second terminal.
A server-rendered snapshot is deterministic output of parsed state: it cannot
open mid-escape-sequence, cannot carry stale geometry, cannot contain terminal
queries, and cannot disagree with the live screen. tmux and zellij have no
replay bugs because they have no replay.

The enabler is running **libghostty-vt** (Ghostty's VT core, a C library) in
the worker via cgo. The frontend already renders with Ghostty WASM built from
the same source (`app/scripts/build-ghostty-vt-wasm.sh`). Same emulator family
on both sides means server snapshots are faithful to what the client would
have rendered — the property today's vt10x oracle lacks (visible frame only,
partial attributes, no scrollback).

**Pin caveat (verified 2026-07-22):** the Terminal C API does NOT exist at the
WASM pin `29d4aba` (that commit has parser-only headers: osc/key/sgr/paste).
It exists on ghostty main — verified working at `ab0b9da03...` (2026-07-22).
The native lib therefore pins its OWN recent commit (`ghostty-vt-native.pin`),
separate from the WASM pin, until the frontend's ghostty-web dependency can
move forward. Both are Ghostty; version skew between them is bounded and
acceptable for restores (see Decisions).

**Phase 0 has been spiked and is GO** — results are recorded inline in
Phase 0 below and in Decisions. Phase 1+ can start immediately; the remaining
Phase 0 checkboxes are productionizing the spike (build script + pin file),
not research.

**Status (2026-07-23):** Phase 0 + Phase 1 are DONE and MERGED to the epic
branch `epic/server-authoritative-terminal` (PR #639, merge commit `7e8e1865`).
Main is untouched. Phase 2 is in progress on the epic branch. Per Victor's
standing instruction, every phase PR bases on and merges into the epic branch;
main stays untouched until the whole initiative is complete — including Phase 3,
which ships to the epic branch, not main.

Phase 3 is code-complete but **HELD** on the OSC 133 command-block gap (see
Phase 3). The 2026-07-23 spike resolved the approach — design in
[2026-07-23-terminal-restore-fidelity.md](2026-07-23-terminal-restore-fidelity.md)
— and the implementation contract is **Phase 3a** below, which lands before the
Phase 3 flip.

## Architecture Map

```text
Current (replay-based):
child app → ptmx → worker read loop (internal/pty/session.go)
  ├─ scan bytes for CPR/DA1/OSC-color queries → answer into PTY input
  ├─ RingBuffer (1MB raw)            \
  ├─ ReplayLog (8MB raw segments)     } three parallel stores, under replayMu
  ├─ vt10x virtualScreen (oracle)    /
  └─ fanOut(raw bytes, seq) → WS → client Ghostty WASM parses + renders

attach → buildAttachReplayPayload (internal/daemon/ws_pty.go:212)
  → prefer raw ReplayLog tail (≤64KB), verified against vt10x oracle
  → fallback: vt10x renderVisibleFrame (visible frame only, no scrollback)
  → client replays bytes through its parser, dedups live stream by seq

Target (server-authoritative restore):
child app → ptmx → worker read loop
  ├─ ghosttyvt.Terminal (cgo, libghostty-vt)   ← single authoritative store
  │    ├─ grid + scrollback + modes + cursor (native memory, capped)
  │    ├─ answers ALL terminal queries (CPR/DA1/kitty/OSC…) → PTY input
  │    └─ Serialize() → {scrollbackANSI, frameANSI, cursor, modes, geometry}
  └─ fanOut(raw bytes, seq) → WS → client Ghostty WASM   (UNCHANGED)

attach → buildAttachSnapshotPayload
  → worker serializes terminal state under the same lock that applies chunks
  → client ingests scrollback lines + frame into a fresh Ghostty model,
    then applies live stream from seq watermark (dedup contract UNCHANGED)

Tests:
go corpus tests → recorded real-session byte fixtures → ghosttyvt.Terminal
  → Serialize() → feed into a second ghosttyvt.Terminal → screens must match
real-app harness → attach/detach/relaunch scenarios → pane text assertions
```

Explicitly OUT of scope (follow-ups, do not attempt here): streaming rendered
diffs instead of raw bytes; slow-client discard-and-resnapshot; daemon-side
geometry arbitration; removing vt10x from the state classifier.

## Data Model / Interfaces

The C API (verified against ghostty main `ab0b9da`, spike code in Phase 0
notes) maps almost 1:1 onto what we need — the wrapper is thin:

```go
// internal/ghosttyvt — new package, cgo bindings over include/ghostty/vt.h.
// Everything below runs inside the worker process (or embedded backend).
// Underlying C calls are named in comments; do not invent others.
type Terminal struct { /* opaque handle + mutex + response buffer */ }

func New(cols, rows int, opts Options) (*Terminal, error)
// ghostty_terminal_new(GhosttyTerminalOptions{cols, rows, max_scrollback}).
// Install GHOSTTY_TERMINAL_OPT_WRITE_PTY callback at construction: the lib
// invokes it synchronously during Write() with query-response bytes (CPR,
// DA1, kitty CSI ? u, DECRQM…). The callback appends to the Terminal's
// response buffer. Callback must NOT call back into vt_write (no reentrancy).

func (t *Terminal) Write(p []byte)         // ghostty_terminal_vt_write; never
                                           // fails, malformed input is safe
func (t *Terminal) Resize(cols, rows int)  // ghostty_terminal_resize; reflows
func (t *Terminal) DrainResponses() []byte // returns + clears the response
                                           // buffer filled by the callback
func (t *Terminal) PlainText() string      // formatter, FORMAT_PLAIN
func (t *Terminal) Serialize() Snapshot    // formatter, FORMAT_VT — see below
func (t *Terminal) Close()                 // ghostty_terminal_free — MUST be
                                           // called; add a finalizer guard

type Snapshot struct {
    Cols, Rows int
    // One self-contained VT stream produced by ghostty_formatter_format_*
    // with GHOSTTY_FORMATTER_FORMAT_VT, unwrap=false, all "extra" fields on
    // (modes, palette, scrolling region, tabstops, cursor, style, kitty
    // keyboard, charsets). Verified properties: includes FULL scrollback
    // (NULL selection = entire screen incl. history); soft-wrap survives the
    // dump (restored terminal reflows correctly on later resize); replaying
    // the dump into a fresh same-size terminal reproduces identical plain
    // text. Post-process: append a final CUP for the true cursor position —
    // upstream emits cursor BEFORE tabstop resets, which move the cursor
    // (verified bug; report upstream, keep the trailing-CUP fix regardless).
    VTDump []byte
    ScrollbackTruncated bool // scrollback hit max_scrollback cap
}
```

Protocol (TypeSpec → `internal/protocol/schema/main.tsp`, then
`make generate-types`, bump `ProtocolVersion` in constants.go **and**
`app/src/hooks/useDaemonSocket.ts` — three lockstep spots, re-grep after any
rebase):

```text
attach_result gains (alongside the existing replay fields during transition):
  snapshot?: {
    cols, rows: int
    vt_dump_b64: string        // Snapshot.VTDump (one self-contained stream)
    scrollback_truncated: bool
  }
  // last_seq watermark semantics UNCHANGED: every byte reflected in the
  // snapshot has seq <= last_seq; live chunks with seq <= last_seq drop.
```

## Boundaries

- `internal/ghosttyvt` owns all cgo; nothing outside it may include vt.h or
  know about native handles. It exposes only the Go types above.
- `internal/pty/session.go` owns the ordering invariant: Write(), seq
  assignment, and Serialize() happen under the same lock (`replayMu` today) so
  a snapshot + its `last_seq` watermark stay an atomic pair. This invariant is
  the load-bearing one in the whole design — there are existing race tests
  (`infoSnapshotHook`, `readLoopSeqGapHook`) that must keep passing.
- The frontend must not know how a snapshot was produced. It receives ANSI
  bytes + geometry, writes them into a fresh model with responses suppressed,
  and applies the existing seq dedup. All replay *decision* logic
  (`attachPlanning.ts` classify/plan functions) shrinks; it must not grow.
- The build script for the native lib mirrors
  `app/scripts/build-ghostty-vt-wasm.sh` but reads its OWN pin file
  (`ghostty-vt-native.pin`) — the Terminal C API does not exist at the WASM
  pin (see Goal). Never float either commit. Build command at the native pin:
  `zig build -Demit-lib-vt=true -Dtarget=aarch64-macos` (the `lib-vt` build
  step from the WASM era was renamed; it is now an option, not a step). Zig
  0.16.0 required (installed via asdf; the WASM pin uses 0.15.x — both live
  side by side). Converging the two pins is a follow-up.

## Implementation Steps

### Phase 0 — Spike: prove libghostty-vt from Go — **DONE, verdict GO**
      (spiked 2026-07-22 against ghostty main `ab0b9da`; cgo spike source at
      `/private/tmp/claude-501/-Users-victor-projects-victor-attn--tmux/e616331e-14c5-480b-8b50-5c3948eab5cf/scratchpad/vt-spike/`
      — port it into `internal/ghosttyvt` tests rather than rewriting; if the
      scratchpad is gone, the API names + build command in this section and
      Data Model are sufficient to recreate it)

Go/no-go checklist, answered empirically (a Go cgo binary linking the static
lib, run on macOS arm64):

1. **Styled read: YES.** The formatter API (`ghostty/vt/formatter.h`) emits
   the whole terminal as VT sequences (`GHOSTTY_FORMATTER_FORMAT_VT`)
   preserving colors/styles, with opt-in extras for cursor, SGR state,
   modes, palette, scrolling region, tabstops, kitty keyboard, charsets.
   No hand-written cell→ANSI serializer needed.
2. **Scrollback: YES.** NULL selection formats the entire screen including
   history (verified: line scrolled 30 rows past a 10-row viewport appears
   in the dump). `GhosttyPointTag` has HISTORY/SCREEN coordinates for
   partial ranges if ever needed.
3. **Soft-wrap: YES.** A 210-char line on an 80-col terminal round-trips
   through the dump and reflows correctly after resizing the restored
   terminal to 40 cols. (`unwrap` formatter option exists; we keep it false.)
4. **Query responses: YES — better than hoped.** Installing the
   `GHOSTTY_TERMINAL_OPT_WRITE_PTY` callback yields responses synchronously
   during write. Verified answered: CPR (`ESC[6n` → `ESC[10;15R`), DA1
   (`ESC[c` → `ESC[?62;22c`), **kitty `CSI ? u` → `ESC[?0u`** — the exact
   query behind the codex fresh-spawn replay carve-out. Per-query override
   callbacks also exist (DA, XTVERSION, XTWINOPS size, color scheme).
5. **Reflow inside the lib: YES** (see 3).
6. **Memory: PASS by ~30x.** 100k lines of 190-char text through a 200x50
   terminal with `max_scrollback=10000`: process RSS delta **768KB**
   (budget was 25MB/session).
7. Robustness: garbage/malformed/truncated escape input is safe by design
   (`vt_write` "never fails"; verified with a hostile byte soup).
8. Speed: serializing the test terminal took ~150–185µs.

Known upstream nits found (neither is gating):
- **Cursor-vs-tabstops ordering bug:** the VT dump emits the cursor CUP
  before tabstop resets, and setting tabstops moves the cursor — so a
  restored terminal's cursor lands on the last tabstop column. Fix on our
  side: always append a final CUP after the dump. Report upstream.
- **Per-cell OSC 8 hyperlinks are not serialized** (only cursor-current
  hyperlink state). Restored scrollback loses clickable OSC 8 links. Minor
  for attn (path/URL detection is client-side); note in CHANGELOG if user
  visible, candidate upstream contribution.

Residual items to productionize (not research):

- [x] `scripts/build-libghostty-vt.sh` + `ghostty-vt-native.pin` (pinned
      `ab0b9da`) — DONE 2026-07-22. Clones ghostty at the pin, applies
      `ghostty-vt-native.patch` if present, runs
      `zig build -Demit-lib-vt=true -Dtarget=aarch64-macos` (zig 0.16),
      installs `libghostty-vt.a` + `include/ghostty/` under
      `third_party/ghostty-vt/` (gitignored; script is source of truth).
      Verified reproducible: fresh build sha == spike-built lib
      (`978c1d02…`). cgo `internal/ghosttyvt` links it; `cmd/attn` grows to
      ~39MB and codesigns cleanly (no ad-hoc fallback) — the plan's cgo
      signing worry is retired.
- [x] **Primary-screen-behind-alt-screen: FIXED via carried patch — DONE
      2026-07-23.** `ghostty-vt-native.patch` adds one C-shim function,
      `ghostty_terminal_serialize_vt`, that composes the upstream
      `TerminalFormatter` + per-screen `ScreenFormatter` in Zig: when the
      alternate screen is active it emits palette → primary screen
      (scrollback + frame) → `?1049h\x1b[H` → alt frame, so leaving alt after a
      restore reveals the primary content. `Serialize()` now calls it.
      `TestRoundTripAltScreen` proves both screens survive and 1049l reveals the
      primary prompt + scrollback. Reproducible build verified (build script
      applies the patch; clean re-run). Patch is upstream-candidate. Below is the
      original analysis, retained for context:

      DEFERRED to Phase 2
      (Serialize needs it; Phase 1 shadow mode does not). The build script
      already applies `ghostty-vt-native.patch` when present, so landing the
      patch is drop-in. Spike-verified: the
      formatter only dumps the ACTIVE screen, so a dump taken while the app
      is in alt-screen (vim, less…) drops the primary screen AND all
      scrollback; after restore, leaving alt-screen shows a blank shell.
      Fix: carry `ghostty-vt-native.patch` (applied by the build script,
      like the WASM build's patches) exposing the EXISTING Zig
      `ScreenFormatter` through the C API. Upstream already supports this
      internally — `src/terminal/formatter.zig` has
      `ScreenFormatter{ screen: *const Screen }` documented as "formats a
      single terminal screen (e.g. primary vs alt)"; the patch is C-shim
      plumbing in `src/terminal/c/formatter.zig` + a header entry (e.g.
      `ghostty_formatter_screen_new(terminal, GHOSTTY_SCREEN_PRIMARY, opts)`),
      NOT emulator changes. Serializer for alt-active terminals then emits:
      primary screen (scrollback + frame) → `1049h` → alt frame. Submit the
      patch upstream so it eventually drops out of our pile. Add a Go test:
      feed shell history → enter alt → dump → restore → `1049l` → primary
      prompt and scrollback must be present (the spike's failing case).
- [ ] Fuzz-ish smoke with REAL corpora (moved into Phase 1 once the recorder
      exists): feed 100MB of mixed real-session bytes in random chunk sizes;
      no crash, RSS stable across 10 create/feed/close cycles.

### Phase 1 — Emulator in the worker, shadow mode (no behavior change)

Deliverable: every session feeds bytes to a ghosttyvt.Terminal alongside the
existing three stores. Nothing reads from it in production yet except
divergence logging. App behavior is identical; this phase is pure risk burn.

- [x] `internal/ghosttyvt` package with the interface above + `Close`
      lifecycle tied to session teardown (`closePTY`) — DONE 2026-07-22.
      cgo owned entirely by the package (New/Write/Resize/DrainResponses/
      PlainText/Serialize/Close, cgo.Handle-routed write_pty callback,
      finalizer backstop). Worker cgo: default `CGO_ENABLED=1` on macOS
      picks it up automatically — no Makefile change needed; full `make
      install` AND `make install-daemon` both produced a signed working
      sidecar (verified live, no ad-hoc fallback).
- [x] Feed path — DONE 2026-07-22. `session.go` read loop (both the main
      chunk site and the carryover flush) writes `s.ghostty.Write(data)`
      under `replayMu` alongside the three stores; `resize()` calls
      `s.ghostty.Resize`. `DrainResponses()` output is discarded this phase
      (scanner stays sole answerer). Every use nil-guarded — a construction
      failure never breaks a session.
- [ ] Byte-corpus recorder + fixtures — NOT DONE. Round-trip tests currently
      use a synthetic in-test corpus (`styledCorpus`). Real recorded corpora
      (claude/codex/vim/emoji/long-scroll) become load-bearing for Phase 2
      serializer correctness; do this next.
- [x] Round-trip invariant test — DONE 2026-07-22
      (`internal/ghosttyvt/ghosttyvt_test.go`): plain-text round-trip,
      cursor-position round-trip (exercises the trailing-CUP fix), and
      reflow-after-restore vs a directly-resized terminal. Also: query
      responses (codex trio incl. kitty `CSI ? u`), malformed-input safety,
      no-interrogative-sequences guard, Close idempotency. All green.
      (Uses synthetic corpus pending the recorder above.)
- [x] Shadow divergence metric — DONE 2026-07-22. On session close, behind
      `ATTN_GHOSTTY_SHADOW_DIVERGENCE=1`, compares ghostty viewport vs vt10x
      `renderedText()` and logs the first differing row to the worker log.
      Pure helpers unit-tested (`shadow_divergence_test.go`).
- [x] Verify — DONE 2026-07-22. Full `go test ./...` green. Live: throwaway
      `vtshadow` profile, full `make install`, preflight PASS (protocol 180).
      `real-app:scenario-ghostty-scroll` PASS (both anchor assertions) —
      existing behavior unchanged. Worker log shows the ghostty viewport
      **matched vt10x exactly (26 rows)** on a real streamed session; zero
      panics / cgo faults / construction failures across spawn→feed→teardown.
      Profile torn down + orphan workers reaped. (Serial matrix not run —
      single scenario was sufficient signal since nothing reads the terminal;
      run the full matrix before Phase 2 flips reads on.)

### Phase 2 — Snapshot-first attach behind a flag (raw replay still exists)

Deliverable: with `ATTN_ATTACH_SNAPSHOT=1` (env/config read by the daemon),
every attach serves a ghostty-serialized snapshot; without it, behavior is
exactly today's. Protocol carries both shapes during transition.

- [x] Serializer: `Serialize()` implemented per the Snapshot contract above.
      Rather than the "one formatter call + trailing CUP" recipe (which only
      serializes the ACTIVE screen and thus loses primary+scrollback whenever a
      dump is taken in alt-screen), it uses a carried Zig C-shim patch,
      `ghostty_terminal_serialize_vt` (`ghostty-vt-native.patch`, applied by
      `scripts/build-libghostty-vt.sh`). The shim composes upstream
      `TerminalFormatter` (palette only) + per-screen `ScreenFormatter`
      (primary then, if alt is active, `\x1b[?1049h\x1b[H` then the alt frame),
      so leaving alt-screen after a restore shows the primary content. Go side
      (`internal/ghosttyvt/ghosttyvt.go`) calls it via `serializeVTLocked()`.
      Covered by `TestRoundTripAltScreen` + the existing round-trip corpus.
- [x] Protocol: `snapshot` field added (TypeSpec `AttachSnapshot`
      {cols, rows, vt_dump_b64, scrollback_truncated} on `AttachResultMessage`);
      `make generate-types` regenerated Go+TS; `ProtocolVersion` 180→181;
      `useDaemonSocket.ts` PROTOCOL_VERSION bumped to match.
- [x] Daemon: `buildAttachReplayPayload` returns the ghostty snapshot when the
      flag is on and one is present, zeroing every raw-replay field with
      `replay_decision=use_ghostty_snapshot`. Worker always serializes into
      `AttachInfo.GhosttySnapshot` under `replayMu` (atomic with the watermark,
      like `info()`); the daemon decides whether to serve it. Flag off = every
      existing decision path intact (verified byte-identical by the branch being
      skipped entirely). Wire boundary plumbed pty→ptybackend→ptyworker→back.
- [x] Frontend: `attachPlanning.ts` classifies a present `data.snapshot` as a
      `ghostty_snapshot` replay kind that supersedes screen-snapshot/raw
      scrollback, is always allowed, never geometry-skipped, and always written
      with `suppressResponses`; `useDaemonSocket.ts` resets the model → resizes
      to the snapshot grid → writes the base64 VT dump → replay_complete. 6 new
      `attachPlanning.test.ts` cases; full frontend suite green (1963).
- [x] Codex bootstrap: instead of keeping raw replay for codex fresh-spawn, the
      worker forwards ghostty's query responses the scanner does NOT cover
      (kitty `CSI ? u`, DECRQM `$y`, …) via `drainGhosttyResponses` +
      `stripScannerOwnedResponses`, keeping the scanner the sole answerer for
      CPR/DA1/OSC-color. Env-gated (`attachSnapshotMode`) so flag-off drains and
      discards exactly as today. Covered by `TestStripScannerOwnedResponses`
      (9 cases incl. the codex bootstrap trio).
- [ ] Verify (flag ON via a throwaway profile — NEVER smoke daemon changes on
      the dev profile; use `eval "$(./attn profile-env <name>)"` + fresh
      install; run bundled preflight first):
      1. Go: round-trip tests from Phase 1 now run against `Serialize()`
         production code; codex query test above.
      2. Live: start claude session → produce >2 screens of styled output →
         quit app → reopen → session restores with scrollback, correct
         colors, cursor at prompt; resize window after restore → text
         reflows without stair-stepping (soft-wrap check); run vim, detach,
         reattach → alt-screen intact; `Cmd+T` utility terminal still
         focuses (per AGENTS.md manual checks).
      3. Codex fresh spawn with flag on: codex TUI reaches its prompt (the
         historical hang is the regression to watch).
      4. Harness: full `real-app:serial-matrix` with the flag on in the
         profile env; compare failures against a flag-off baseline run.
      5. Kill the app mid-stream (`pkill` the app, not the daemon), relaunch,
         attach: no lost/duplicated tail (seq contract), no mid-escape
         garbage ever (structural guarantee — but assert pane text anyway).

### Phase 3a — Block foundation: worker-owned OSC 133 block table

Unblocks the Phase 3 hold. Design + rationale + spike evidence:
[2026-07-23-terminal-restore-fidelity.md](2026-07-23-terminal-restore-fidelity.md)
— read it before starting; it records the two-rule principle (emulator-consumed
state → dump; app-consumed state → typed structure), the verified primitives,
and the rejected alternatives (do NOT re-emit OSC 133 bytes into the dump).
This section is the implementation contract; the design doc is the why.

Deliverable: `AttachSnapshot` carries a structured `blocks` field; the frontend
seeds `TerminalBlockStore` from it on restore;
`real-app:scenario-terminal-block-resize` Phase B passes with snapshot attach
ON. Ships to the epic branch like every phase.

**Execution model:** the judgment-heavy areas of this phase (native ref
lifecycle, `replayMu` atomicity, self-heal semantics) are concentrated into one
small **rails** pass, done first by a strong agent. The rails convert each
judgment into a mechanism — a leak counter, an executable behavioral corpus,
and a pre-placed locked skeleton — so every later item is pure code against an
executable spec, safe to fan out to cheaper agents. After rails, a wrong
implementation cannot pass the suite.

- [x] **Rails (one strong-agent pass; small — do this first) — DONE
      2026-07-23** on `feat/server-auth-terminal-phase3a`: (1) leak counter in
      `spike_trackedref.go` + `TestTrackedRefLeakAccounting`; (2) corpus
      `internal/pty/testdata/osc133_block_corpus.json` (15 cases) proven by
      `app/src/utils/terminalBlocks.corpus.test.ts`; (3) skeleton
      `internal/pty/blockfeed.go` + session/manager wiring, atomicity proven
      by `TestBlockSnapshotAtomicity` (incl. `-race`), linux stub build green;
      (4) worker-RPC plumbing of `GhosttyBlocks` end-to-end
      (pty → ptyworker wire `AttachBlock` → ptybackend `AttachInfo`), additive
      + omitempty like `GhosttySnapshot`, so implementers touch no process
      boundary. Original spec:
      1. Ref-leak accounting in `internal/ghosttyvt`: a package-level live
         counter incremented by `TrackCursor`, decremented by the first
         `Free`; `LiveTrackedRefs() int` exposed for tests. Every
         block-table test asserts zero live refs at teardown — a missed
         `Free` on any retirement path (cap eviction, alt-drop, self-heal
         replacement, session close) is a red test, not a production leak.
      2. Block-table behavioral corpus: JSON fixtures (marker sequences
         with positions → expected table state: blocks, fields, pending
         flag, evictions) derived from the existing
         `app/src/utils/terminalBlocks` test cases plus the self-heal, cap,
         and lost-D edges. A TS corpus-runner test FIRST proves the corpus
         against the existing `TerminalBlockStore`; the Go table is then
         written against the same corpus. Semantics get ported as an
         executable spec, never by reading code and hoping.
      3. Locked integration skeleton: define the segmenter and block-table
         interfaces in `internal/pty` as PURE components (no locks, no cgo
         beyond graduated `TrackedRef` handles); place their call sites —
         the read-loop feed seam (segment-wise Write + marker pinning) and
         the block resolution inside the `AttachInfo` serialize section —
         under `replayMu`, wired to no-op implementations, app behavior
         unchanged. Extend the existing race tests (`infoSnapshotHook`
         pattern) to assert that under concurrent writes, resolved block
         rows always index the snapshot dump at the expected text. The
         atomicity invariant is decided ONCE, here; implementers of the
         items below never touch locking or `session.go`.

Everything below is weak-agent-safe after rails (the segmenter and protocol
items don't even depend on them):

- [x] ghosttyvt API graduation (`internal/ghosttyvt`) — DONE 2026-07-23. The
      spike files are renamed to the real API (`trackedref.go`,
      `trackedref_test.go`, `trackedref_prune_test.go`), Spike naming dropped,
      header reframed as the production block-tracker primitive. The prune probe
      graduated into a real assertion (`TestTrackedRefDropsWhenPruned`: a pinned
      ref reports ok=false once pruned, never a stale row). Alt-screen exclusion
      is carried by `(*Terminal).AltScreenActive() bool` (the boolean the block
      table consumes) rather than the planned `ActiveScreen()` enum. Original
      spec:
      - `(*Terminal).TrackCursor() *TrackedRef` — pins the cursor cell via
        `ghostty_terminal_grid_ref_track` with an ACTIVE-space point; nil on
        closed terminal or failure.
      - `(*TrackedRef).ScreenPoint() (x, y int, ok bool)` —
        `ghostty_tracked_grid_ref_point` with `GHOSTTY_POINT_TAG_SCREEN`;
        ok=false when the cell was pruned. Callers synchronize with writes
        externally (in production everything runs under `replayMu`).
      - `(*TrackedRef).Free()` — `ghostty_tracked_grid_ref_free`, idempotent.
        Refs MUST be freed when a block retires (cap eviction, alt-drop,
        session close) — they are native memory.
      - `(*Terminal).ActiveScreen()` — `ghostty_terminal_get` with
        `GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN` (returns
        `GHOSTTY_TERMINAL_SCREEN_PRIMARY|ALTERNATE`).
      The spike tests graduate too (prune probe can stay a probe). Non-darwin
      stub: `TrackCursor` returns nil; everything downstream nil-guards.
- [x] Go OSC 133 segmenter — DONE 2026-07-23 (`internal/pty/osc133.go`,
      `osc133_test.go`). Semantics-identical port of
      `app/src/utils/terminalOsc133.ts`; the worker STRIPS marker bytes (client
      keeps them) so OSC 133 stays out of the VT dump — grids stay identical.
      Shared corpus `testdata/osc133_segmenter_corpus.json` (12 cases) proves
      marker parity against BOTH the Go test and the new frontend
      `terminalOsc133.parity.test.ts`. Original spec: port
      `app/src/utils/terminalOsc133.ts` (~180 lines) semantics-identical:
      scan for `ESC ] 1 3 3 ;` prefix, split-across-chunk pending buffer
      (MAX_PENDING_BYTES=4096), BEL and ST terminators, markers
      A (prompt) / B (input) / C (pre-exec, `cmdline_url=` payload → command
      text, percent-decoded) / D (end, exit code). Output: segments of raw
      bytes + markers between them, so callers write segments and act on
      markers in stream order.
      **Parity is test-enforced:** one shared fixture corpus (JSON: named
      cases, input chunk sequences → expected markers/segments) consumed by
      BOTH the Go test and a new frontend parity test against the TS parser.
      Start by generating the corpus from the existing TS unit tests' cases.
- [x] Worker block table — DONE 2026-07-23 (`internal/pty/blocktable.go`,
      `blocktable_test.go`). Implements the rails `workerBlockTable` against the
      rails corpus: at most one pending block, self-heal on lost `133;D`, cap
      200 oldest-first. Positions are reference-counted `sharedRef` wrappers over
      `blockRef` (a self-heal reuses one marker's ref for two blocks, so an rc
      frees the native ref exactly once); alt-screen-pinned blocks are excluded
      at snapshot; `TestBlockTableCorpus` asserts `freed == created` after Close.
      Semantics (already encoded in the corpus, restated for
      orientation): mirrors `TerminalBlockStore`
      (`app/src/utils/terminalBlocks.ts`) — at most one pending block,
      self-heal when `133;D` is lost (a new A while pending completes the
      old block at the new prompt row), cap 200 with oldest-first eviction —
      but positions are four TrackedRefs (prompt, input, output-start, end)
      instead of row numbers + text anchors. Command text and exit code are
      captured at parse time (they are unrecoverable later — the whole
      point of this phase). Record `ActiveScreen()` at pin time; blocks
      pinned while the alternate screen is active are excluded at serialize
      (blocks are a primary-screen concept). Refs freed on every retirement
      path — guarded by the rails leak counter (`LiveTrackedRefs()==0` at
      teardown in every test).
- [x] Feed + serialize wiring — DONE 2026-07-23 (`internal/pty/blockfeed.go`
      `newBlockFeeder` now wires `&osc133ScanSegmenter{}` + `newBlockTable()`;
      dead `passthroughSegmenter`/`noopBlockTable` removed). Call sites, lock
      placement, and the atomic {dump, blocks, watermark} triple were untouched.
      Fast path preserved (no-ESC chunk → single `Write`, no alloc); pins are
      guarded so a nil `TrackCursor` stays a nil `blockRef` interface, not a
      typed-nil. Original text: swap the rails no-op implementations for the
      real segmenter and block table. Call sites, lock placement, and the
      atomic {dump, blocks, watermark} triple were fixed by the rails
      skeleton — do NOT move them. Behavior to preserve (rails race + fast
      path tests already assert it): a chunk with no `ESC ] 1 3 3` and no
      pending partial marker stays a single `Write` with zero extra
      allocation; at serialize, a block whose prompt or end ref reports
      no-value is dropped (correct-or-absent — same philosophy as the
      client's anchor refusal); no ghostty terminal → no block table →
      snapshot simply has no blocks (nil-guard like every existing ghostty
      use). Spike-verified premise the resolution relies on: a SCREEN-space
      y at serialize time IS the row index in the restored terminal — no
      offset mapping, including after pruning (SCREEN space is post-prune
      by construction).
- [x] `Snapshot.ScrollbackTruncated` — DONE 2026-07-23: DELETED. It was dead
      (only read by a spike probe log), and `AttachSnapshot.scrollback_truncated`
      was never consumed by the frontend, so both are removed
      (`ghosttyvt.Snapshot`, `AttachSnapshot` model). The unrelated
      `AttachResultMessage.scrollback_truncated` (the live raw-replay-truncation
      signal, still consumed by `attachPlanning.ts`) is untouched.
- [x] Protocol — DONE 2026-07-23. `AttachSnapshot` gains `blocks?:
      AttachBlock[]`; regenerated `generated.go`/`generated.ts`;
      `ProtocolVersion` bumped to `182` in `constants.go` AND
      `useDaemonSocket.ts`. `ws_pty.go` maps `pty.AttachBlockData` →
      `protocol.AttachBlock` (`attachBlocksToProtocol`). Original spec:
      `AttachSnapshot` gains `blocks?: AttachBlock[]`:
      `id` (uint64, server-assigned, monotonic per session — authoritative
      from day one so the future `block_event` stream is additive),
      `pending` (bool, at most one true), `prompt_row` (int32, dump-row
      space == client buffer row post-restore), `input_row?`, `input_col?`,
      `output_start_row?`, `end_row?` (exclusive, absent while pending),
      `command?` (string), `exit_code?` (int32).
      TypeSpec `main.tsp` → `make generate-types` → bump `ProtocolVersion`
      in constants.go AND `useDaemonSocket.ts` (three lockstep spots,
      re-grep after any rebase; rebuild `./attn` before frontend e2e).
- [x] Frontend seed — DONE 2026-07-23. `TerminalBlockStore.seed(blocks,
      rowTextAt)` (`terminalBlocks.ts`) clears state, lands completed blocks
      with locally-computed `anchorText`, re-arms the pending block, and
      continues `nextId` above the max seeded id. `useDaemonSocket.ts` emits a
      `seed_blocks` PTY event after the dump write; it rides the write chain
      (`GhosttyTerminal.seedBlocks` → `enqueueOperation`) so anchor text reads
      the restored buffer. No snapshot → no seed. Original spec: on the
      ghostty-snapshot attach path in `useDaemonSocket.ts`, after model reset +
      resize + dump write: call a
      new `TerminalBlockStore.seed(blocks)` — completed blocks enter with
      their server rows, `anchorText` computed locally from the restored
      buffer (same row-text source `applyMarker` uses), the pending block
      re-arms `pending`, and the store's next id continues above the max
      seeded id. The live stream then keeps appending blocks through the
      existing client `parseOsc133` path, UNCHANGED this phase. No snapshot
      → no seed → existing degradation untouched.
- [x] Tests — DONE 2026-07-23 (all green: `go test ./internal/pty/
      ./internal/ghosttyvt/ ./internal/daemon/`, `-race` on the atomicity test,
      linux stub build; frontend `vitest run` 1992 passed, `tsc --noEmit`
      clean). The native round-trip against a live serialized terminal — REAL
      segmenter + REAL block table, fish-like fixture with 133 marks →
      serialize → each block's rows index the restored dump at the right text,
      command text + exit codes captured at parse time, alt-screen block
      excluded — is `TestBlockFeedRoundTrip` (`blockfeed_roundtrip_test.go`,
      darwin/arm64). Coverage:
      1. Go: segmenter parity corpus (shared with TS); block-table
         lifecycle (pending/self-heal/cap/eviction frees refs);
         round-trip — feed a fish-like fixture with 133 marks → serialize →
         assert each block's rows index the restored terminal at the right
         text (extend the spike round-trip pattern); prune drops early
         blocks cleanly, keeps late ones; alt-screen-active serialize keeps
         primary blocks, excludes alt-pinned ones.
      2. Frontend: `seed()` unit tests (rows land, anchorText from buffer,
         pending re-arm, id continuation); parity test consuming the corpus.
      3. Cross-platform: non-darwin stub build stays green (`GOOS=linux go
         build ./...`) — block-table code paths inert without ghostty.
- [ ] Verify (throwaway profile — never dev — snapshot ON): fish session →
      run several commands → quit app → reopen → blocks are
      clickable/copyable with correct command text, exit codes, and regions;
      then `real-app:scenario-terminal-block-resize` FULL pass including
      Phase B (`phase_b_relaunch_replay`); bash/zsh legs still assert
      blocks ABSENT. Full `make test-harness` before the phase PR.

Exit gate: Phase B green with snapshot attach on ⇒ remove the Phase 3 hold.

### Phase 3 — Flip default, delete raw replay

**HELD (2026-07-23) — OSC 133 command blocks lost on restore.**
Shell-integration command blocks (the clickable prompt/command/output regions
from `OSC 133;A/B/C/D`) do not survive a snapshot restore. The VT dump carries
rendered grid cells, not the `OSC 133` marks, so the frontend rebuilds zero
blocks after attach and `real-app:scenario-terminal-block-resize` Phase B fails
(0 blocks tracked). Raw replay preserved blocks incidentally by re-feeding the
byte stream through `parseOsc133`; the server-authoritative dump does not.

Victor's call (2026-07-23): this is **not** an acceptable regression and not a
follow-up in the images/hyperlinks bucket. It is a fundamental gap that must be
solved at the foundation level. Phase 3 does not merge until it is; a separate
brainstorm/spike owns the approach.

**Resolution (2026-07-23):** the spike ran and verified its primitives; the
design is
[2026-07-23-terminal-restore-fidelity.md](2026-07-23-terminal-restore-fidelity.md)
(worker-owned block table serialized as structured data beside the VT dump).
**Phase 3a** above is the implementation. Phase 3 stays held until 3a lands and
`real-app:scenario-terminal-block-resize` Phase B is green with snapshot attach
on; then this phase proceeds exactly as already coded.

Only start after Phase 2 has soaked on Victor's dev profile for real use;
ask him before flipping.

- [x] Default snapshot path on; remove the env flag (`ATTN_ATTACH_SNAPSHOT`
      gone; `drainGhosttyResponses` now unconditionally forwards the query gap).
- [x] Delete: `ReplayLog` + whole-segment machinery (`replaylog.go`,
      `ringbuffer.go`), the replay-vs-snapshot decision tree and oracle
      verification in `ws_pty.go` (`buildAttachReplayPayload` collapsed to
      omit/`no_snapshot`/`use_ghostty_snapshot`; `LimitReplaySegmentsTail`,
      `maxAgentRawReplayBytes`, `limitReplayTail`, segment plumbing removed),
      `ScreenSnapshotFromReplay*` derivation, frontend replay classification in
      `attachPlanning.ts` (kept geometry planning + seq dedup; collapsed to
      ghostty-snapshot-or-none), the codex raw-replay carve-out
      (`deriveAttachReplayPreference`, `respondToTerminalQueries`,
      `shouldWarnTruncatedRestore` — client now always writes the dump
      suppressed).
      KEPT: vt10x `virtualScreen` + `renderedText()` (state classifier,
      grid-tile seeding via `get_screen_snapshot`, CPR replies — untouched);
      `stripScannerOwnedResponses` on the worker (still needed for the gap the
      scanner doesn't cover); seq/watermark contract and its race tests
      (`attach_race_test.go` rewritten representation-independent so it passes
      on the Linux stub).
- [x] Sweep for dead protocol fields: removed `scrollback`,
      `scrollback_truncated`, `replay_segments`, `screen_*` from
      `AttachResultMessage` in `main.tsp` + regenerated; `ProtocolVersion`
      181→182 (both lockstep spots). `get_screen_snapshot` (grid tiles) keeps
      its `screen_*` fields — separate command, untouched.
- [x] Update AGENTS.md Terminal section + CHANGELOG (user-visible: deeper,
      more faithful session restore; images text-only on restore).
- [ ] Verify: full `make test-harness`; the specific scenarios
      `real-app:scenario-ghostty-scroll`, terminal-block-copy, diff-review,
      tr401 local-window-resize; grep daemon/worker logs for any
      `replay_decision` stragglers; soak on dev profile.

Status (2026-07-23): Go + frontend suites green (`make test`; `pnpm test`
1943), but **HELD** — the OSC 133 command-block gap above blocks merge. Ships to
`epic/server-authoritative-terminal`, not main, once resolved.

## Decisions

- **Live streaming stays raw bytes.** Only the attach/restore path becomes
  server-authoritative. This keeps attn transparent to sequences the middle
  emulator doesn't model (kitty graphics, OSC 133 command blocks render from
  the live stream exactly as today) and limits fidelity risk to restores.
  Full tmux-style diff streaming is a possible future, not this plan.
- **libghostty-vt over improving vt10x**: same emulator as the client makes
  snapshot fidelity a non-question by construction; vt10x would need
  truecolor, scrollback, reflow, kitty modes — that's rewriting Ghostty badly.
  Cost: cgo + an explicitly unstable C API, mitigated by hard-pinning.
- **Spike verdict 2026-07-22: GO** (full results in Phase 0). Every gate
  passed: styled VT serialization via the upstream formatter, scrollback
  included, soft-wrap/reflow round-trips, queries answered incl. kitty
  `CSI ? u`, RSS ~0.8MB for a 10k-line scrollback, malformed input safe,
  ~180µs to serialize. Two non-gating upstream nits (cursor/tabstop
  ordering; per-cell OSC 8 loss) with fixes noted.
- **Alt-screen primary loss → carried C-shim patch (Victor, 2026-07-22).**
  Expose upstream's existing `ScreenFormatter` through the C API via
  `ghostty-vt-native.patch`; rejected the no-patch cached-dump-at-1049h
  workaround as more fragile (mid-chunk splits, 47/1047/1049 variants).
  Submit upstream.
- **Images (sixel/kitty) lost on restore: accepted (Victor, 2026-07-22).**
  Live streaming still renders them; restores are text-only where images
  were. CHANGELOG note when Phase 3 ships; image-preserving restore is a
  follow-up, not a gate.
- **Two ghostty pins, not one.** The Terminal C API doesn't exist at the
  frontend WASM pin `29d4aba`; the native lib pins recent main (verified at
  `ab0b9da`). Rejected alternative: bumping the WASM pin in the same project
  — it drags the ghostty-web compat patch along and couples a renderer
  upgrade into this plan. Restore fidelity is still emulator-verified by the
  round-trip tests; pin convergence is a follow-up.
- **vt10x stays (for now)** as the state-classifier's text source. Swapping
  the classifier input mid-plan couples an approval-detection regression risk
  into a terminal-fidelity project. Follow-up owns that swap.
- **Query responder**: scanner stays authoritative for CPR/DA1/OSC-color;
  ghostty's drained responses fill only the gap (kitty, etc.) with per-type
  dedup. Single-responder-by-construction comes when the scanner retires in
  a follow-up.
- **Worker-only cgo concern**: a native crash in libghostty-vt kills one
  session's worker, not the daemon — the per-session worker architecture is
  exactly the right blast-radius container for this experiment. The embedded
  backend gets the same code path but is dev-only.

## Open Questions

None — all resolved (see Decisions). Scrollback cap: 10k lines stands
(≈0.8MB RSS measured).

## Follow-ups

- Slow-client handling: replace slow-count disconnect with tmux-style
  discard + fresh snapshot push (`pty_desync` machinery already exists).
- Daemon-side geometry arbitration (per-client sizes, latest-active policy,
  250ms coalescing, tmux double-pulse) → retire suspicious-size guard,
  overflow re-assertion, bottom-clip repair.
- Swap state classifier text source to ghosttyvt; then delete vt10x.
- Retire scan-based query responder in favor of ghostty responses only.
- Consider `PaneRenderUpdate`-style full-snapshot-then-diffs for web/remote
  clients (zellij's subscriber model).
- Converge the native and WASM ghostty pins (requires ghostty-web moving to
  a commit that has the Terminal C API); report the cursor/tabstop formatter
  ordering bug and the per-cell OSC 8 serialization gap upstream.
- Restore-fidelity roadmap (details + rule classification in
  [2026-07-23-terminal-restore-fidelity.md](2026-07-23-terminal-restore-fidelity.md)):
  - OSC 8 per-cell hyperlinks into the VT dump (formatter gap; extend
    `ghostty-vt-native.patch` or upstream).
  - Image-preserving restore: mini-spike first — the C API exposes kitty
    graphics storage + placements (`kitty_graphics.h`); choose re-emit-in-dump
    vs structured sidecar.
  - Block events (`block_event` stream): worker becomes the only OSC 133
    interpreter; retire the client-side parser + the Go/TS parity corpus.
    Phase 3a's server-assigned ids make this additive.
