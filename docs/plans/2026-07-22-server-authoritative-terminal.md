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
- [ ] **Primary-screen-behind-alt-screen: CONFIRMED LOST — fix via a small
      carried patch (decided with Victor 2026-07-22).** DEFERRED to Phase 2
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

- [ ] Serializer: implement `Serialize()` per the Snapshot contract above —
      it is a thin call into the upstream formatter, NOT a hand-rolled
      cell walker. Rules a weaker model must not improvise on:
      - one formatter call, `FORMAT_VT`, `unwrap=false`, all extras on,
        NULL selection; then append the trailing CUP for the true cursor
        position (upstream ordering bug, see Phase 0 notes).
      - alt-screen: when the terminal is in alt-screen, use the patched
        screen-selector API (see Phase 0 residual items) to emit primary
        screen (scrollback + frame), then `1049h`, then the alt frame.
        Leaving alt-screen after a restore MUST show the primary content.
      - audit the dump for host-affecting sequences: it must never contain
        anything interrogative (queries) nor OSC 52 clipboard writes. Add a
        Go test asserting the dump of a corpus-fed terminal contains none
        (scan for the sequence classes `stripDaemonOwnedResponses` handles,
        plus OSC 52).
      - never snapshot mid-`Write` — the existing lock already guarantees
        the dump reflects committed state only (DEC 2026 etc.).
- [ ] Protocol: add the `snapshot` field per Data Model section; TypeSpec →
      `make generate-types` (rm -rf tsp-output first) → constants.go bump →
      useDaemonSocket.ts bump → rebuild `./attn` (stale binary breaks e2e).
- [ ] Daemon: in `buildAttachReplayPayload`, when the flag is on, return the
      snapshot (serialized under `replayMu` with `last_seq`, reusing the
      `info()` atomic-pair pattern) and set replay fields empty with
      `replay_decision=use_ghostty_snapshot`. Keep every existing decision
      path intact when the flag is off.
- [ ] Frontend: in `attachPlanning.ts` + `useGhosttyPaneRuntime.ts`, when
      `snapshot` is present: reset model → write scrollback → write frame,
      all with `suppressResponses` (source `attach_replay`), then existing
      seq dedup applies untouched. Geometry: model is created at snapshot
      cols/rows; the existing fit/resize flow then runs as normal.
- [ ] Codex bootstrap: with the flag on, `shouldIncludeAttachReplay`'s codex
      fresh-spawn carve-out must keep working. The reason it exists: codex
      emits startup queries (incl. kitty `CSI ? u`) that today's scan-based
      responder does NOT answer — the client parser answers them from
      replayed bytes. The spike confirmed libghostty-vt answers kitty
      `CSI ? u` natively (`ESC[?0u` via the WRITE_PTY callback). Fix
      properly: extend the worker's responder using
      `ghostty.DrainResponses()` for exactly the queries the scanner misses
      (drain after each Write; forward to PTY input; suppress any response
      classes the scanner already answers to avoid double replies — dedup by
      query type). Then codex fresh-spawn needs no replay at all. This is
      the trickiest step in the plan; add a dedicated Go test feeding the
      recorded codex startup corpus and asserting each query gets exactly
      one reply, in ask order.
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

### Phase 3 — Flip default, delete raw replay

Only start after Phase 2 has soaked on Victor's dev profile for real use;
ask him before flipping.

- [ ] Default snapshot path on; remove the env flag.
- [ ] Delete: `ReplayLog` + whole-segment machinery (`replaylog.go`), the
      replay-vs-snapshot decision tree and oracle verification in
      `ws_pty.go` (`rawReplaySegmentsMatchFreshSnapshot`,
      `LimitReplaySegmentsTail`, decision/reason plumbing), vt10x
      `renderVisibleFrame` snapshot path, frontend replay classification in
      `attachPlanning.ts` (keep geometry planning + seq dedup), the codex
      raw-replay carve-out, `stripDaemonOwnedResponses` only if the ghostty
      responder now covers everything the client model could try to answer —
      otherwise keep it and file a follow-up.
      KEEP: `RingBuffer` scrollback if anything still reads it (check
      `scrollbackTruncated` consumers); vt10x + `renderedText()` for the
      state classifier (untouched by this plan); seq/watermark contract and
      its race tests.
- [ ] Sweep for dead protocol fields; deprecate in TypeSpec rather than
      breaking if the daemon must keep serving older apps (it shouldn't —
      version skew fails explicitly; prefer removal + version bump).
- [ ] Update AGENTS.md Terminal section + CHANGELOG (user-visible: deeper,
      more faithful session restore).
- [ ] Verify: full `make test-harness`; the specific scenarios
      `real-app:scenario-ghostty-scroll`, terminal-block-copy, diff-review,
      tr401 local-window-resize; grep daemon/worker logs for any
      `replay_decision` stragglers; soak on dev profile.

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
