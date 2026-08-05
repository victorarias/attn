# Plan: Fix ghostty WASM terminal model crashes (resize-triggered faults)

## Goal

Root-cause and fix the recurring frontend terminal crashes behind the
"Terminal issue recovered. We reloaded it for you." toast. The crash is a WASM
trap inside the vendored ghostty VT core (`app/vendor/ghostty-vt/ghostty-vt.wasm`,
pinned at `29d4aba` when this plan opened; now sharing `ab0b9da` with the native
worker through `ghostty-vt.pin`), not a rendering
or lifecycle bug in the React layer. Recovery already works
(server-authoritative snapshot remount in ~20ms, no data loss);
the goal is that the model stops faulting, plus permanent instrumentation so
any future fault of this class arrives with its own repro.

## Resolution (2026-08-05)

The capture ring made the production trap deterministic. The latest fault
carried a complete 101,873-byte restore dump plus 11 live operations containing
256,617 write bytes; the earlier complete capture carried a 59,390-byte restore
plus five operations / 99,806 write bytes.

The root cause was Ghostty's `ReleaseSmall` page allocator. Reused page buffers
were not always zeroed. After scroll/reflow, `cursorScrollAbove` could expose a
recycled row whose stale cells still carried hyperlink flags even though the
new page had no corresponding hyperlink map. Later hyperlink capacity growth
hit an internal invariant and trapped as `unreachable`; render traversal of the
same corrupt state explains the out-of-bounds variant. Upstream commit
`420de124` makes the allocator always zero reused buffers.

The frontend now builds from the worker's existing `ab0b9da` source revision,
which contains that fix. A WASM-only adapter preserves the
`ghostty-web@0.4.0` JavaScript ABI over Ghostty's current terminal C API. The
latest capture replayed cleanly five consecutive times on the resulting binary;
the earlier capture also replayed cleanly. The minimized resize-hang regression
and the native-to-WASM kitty/OSC 133 parity corpus are green.

## Evidence (2026-07-27, production profile)

Three faults on record in `$APPLOCALDATA/debug/terminal-diagnostics.jsonl`
(window starts 2026-07-08), all on one codex session (`thunk`,
`8f9f5813-…`, a docked agent tile):

| time (UTC) | operation | error | context |
|---|---|---|---|
| 11:52:00 | `render` | `Out of bounds memory access` in `update()` (wasm fn 461) | cols 134 |
| 19:09:33 | `write` | `Unreachable code should not be executed` in `ghostty_terminal_write` (wasm 510→219→213→328→460) | first PTY write after fit resize 59→60 @58 rows |
| 19:09:56 | `write` | same trap, same stack | 23s after recovery: remount at 60 → drag resizes 75…93→73→66→67→133→134 → first write → trap |

A fourth fault mode was found synthetically (fuzz sweep, 2026-07-27, verified
2/2 deterministic): an **infinite loop** — `resize()` never returns, 100% CPU,
no trap. Frozen as `app/scripts/repro-ghostty-vt-resize-hang.mjs`:

- 59x58 terminal, one 153-char write mixing truncated OSC 8 fragments,
  box-drawing, and a 104-char overflowing `w` run (plain ASCII of the same
  length does **not** reproduce — the content is load-bearing), then
  `resize(69)` → `resize(68)` → `resize(67)`. The second consecutive narrow
  hangs.
- Boundary-bisected requirements: widen ≥ +10 cols (59→69 hangs, 59→68
  doesn't), then two further resize calls (one narrow alone doesn't hang;
  narrow-then-widen also hangs).
- Reproduces identically through **both** app resize call sites: plain
  `resize()` (reflow) and the mode-7 no-reflow wrapper. The app's
  `resizeGhosttyWithoutReflow` dance is **not** the enabler.
- The production trap signatures (`write` unreachable, render OOB) were *not*
  synthetically reproduced in ~1500 seeded iterations across five content/
  resize strategies. The hang is a sibling finding in the same
  resize/reflow+OSC-8 territory (hyperlinks again — same family as the June
  `startHyperlink` fix), but equating it with the production traps is
  unproven.

Key facts established:

- Both write traps land on the **first content write after a burst of
  single-column fit resizes** (divider drag). `Unreachable` is a Zig
  assertion/panic under `ReleaseSmall` — internal page state is corrupt before
  the write arrives.
- The daemon worker's **native** terminal (libghostty-vt pin `ab0b9da`,
  2026-07-22) processed the *identical* byte stream and resize sequence
  (confirmed in the worker log) without faulting. Two differences, either of
  which may explain it: newer core, and plain resizes (the worker never uses
  the no-reflow path below).
- Every app-side fit resize goes through `resizeGhosttyWithoutReflow`
  (`app/src/utils/ghosttyResize.ts`): `getMode(7)` → `write('\x1b[?7l')` →
  `resize()` → `write('\x1b[?7h')`. This deliberately selects ghostty's
  no-reflow resize path and is **load-bearing** (PR #306 / commit `139d50ae`:
  block-store correct-or-absent semantics and replay-at-historical-geometry
  depend on it). The live `resizeLocal` path uses plain `terminal.resize()`.
- Each pane instantiates its **own** WASM instance (`Ghostty.load` per mount,
  no caching), so cross-terminal memory corruption (ghostty-web issue #141)
  is ruled out. The 0.4.0 wrapper's `write` builds fresh TypedArray views per
  call, and attn drives its own renderer — so ghostty-web PR #132's
  detached-view mechanism does not match our call pattern either.
- The trap fires inside the core with valid input bytes: this is (or is
  enabled by) a core-state corruption during resize at pin `29d4aba`.

Upstream landscape (researched 2026-07-27):

- ghostty-web has **no stable release after 0.4.0** (Dec 2025); ~20 fixes sit
  on `main` as `0.4.0-next.*` prerelease tags only (open request for a cut:
  coder/ghostty-web#137). Its ghostty submodule pin is *older* than ours —
  upstream offers no newer core.
- Relevant upstream issues: PR #132 (merged, resize-crash fix, wrong mechanism
  for us), issue #139 (viewport corruption across internal page boundaries,
  column-width dependent — plausible cousin of our render-OOB fault; fix PRs
  #133/#177 both **unmerged**), issue #141 (ruled out above).
- Bumping the WASM to the native pin `ab0b9da` is a **rewrite, not a bump**
  (trial-built 2026-07-27): ghostty redesigned its Terminal C API upstream
  (`ghostty_terminal_write` → `ghostty_terminal_vt_write`, flat getters →
  enum-based `ghostty_terminal_get`/`render_state_get`, scrollback getters →
  `grid_ref` API). ghostty-web's wasm-api patch cannot apply (the files it
  used to create now exist natively with different shapes); a vanilla ab0b9da
  WASM builds with zig 0.16 but lacks 23 exports the JS wrapper needs. Full
  mapping table from the trial lives in the session scratchpad
  (`wasm-bump/NOTES.md`) — fold it into the follow-up plan when that work
  starts.

## Architecture map (crash path)

```text
Current (crash):
PTY bytes ── daemon worker (native ghostty ab0b9da — survives)
   └─ WebSocket pty_output
       └─ GhosttyTerminal write chain (enqueueOperation, serialized)
           └─ ghostty-web 0.4.0 wrapper .write()
               └─ wasm ghostty_terminal_write (pin 29d4aba)  ← TRAP (unreachable)

fit resize (divider drag, ResizeObserver):
fit() ─ applyFitDimensions ─ resizeGhosttyWithoutReflow
   └─ write(?7l) → terminal.resize(cols,rows) → write(?7h)   ← suspected corruption site

render:
renderSurface → renderer.update() → render_state getters      ← TRAP (OOB) variant

recovery (works today):
trap → recoverFromModelFault → noteRecovery(modelFault) → remount epoch
   └─ fresh Ghostty.load + attach_result.snapshot restore → toast to user
```

## Phase 1 — Deterministic repro (gates everything)

- [x] Fuzz/replay harness against the real vendored wasm through the real
      ghostty-web wrapper (pattern: `app/src/utils/ghosttyHyperlinks.test.ts`),
      codex-TUI-like content, production resize semantics. **Outcome: found
      and minimized the resize-hang repro** (see Evidence), frozen as
      `app/scripts/repro-ghostty-vt-resize-hang.mjs` (exit 0 = fixed build,
      1 = hang, 2 = trap — it is the bisect test). The production trap
      signatures did not fall out of ~1500 iterations.
- [ ] Add **capture-on-fault** instrumentation for the *trap* fault modes and
      wait for the next real fault (this class recurs; 3 faults today).
      *In flight as a separate change; not in the pin-bump PR.*
      Per-pane bounded ring of raw model inputs, dumped into the existing
      `ghostty_model_fault` diagnostics record:

```ts
// owned by GhosttyTerminal, per pane, memory-bounded (e.g. last 512KB bytes + 2k ops)
type ModelOpRing = Array<
  | { t: number; kind: 'write'; bytes: Uint8Array }        // raw, pre-wrapper
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  | { t: number; kind: 'reset' }
>
// on trap: base64 the ring into the ghostty_model_fault JSONL record
// replay = new terminal at ring-start geometry, apply ops in order
```

      The ring starts at model construction (post-snapshot restore), so a
      replay needs the snapshot dump too — capture the attach snapshot
      (`attach_result.snapshot` dump + grid) alongside, same cap.
- [x] Minimize any repro to a fixture and commit it as a vitest regression
      test (`node` environment, real wasm, no mocks) — done for the hang, see
      Phase 4.

## Phase 2 — Root-cause discrimination (with repro in hand)

**Answered for the hang** (2026-07-27): it reproduces with plain `resize()`
and with the mode-7 wrapper alike → pure core bug at `29d4aba`, not enabled by
the app's resize dance. Option 3C is off the table as a primary fix.

Bisect result (2026-08-02) — **the pin itself introduced the hang**:

- [x] Bisect ghostty `29d4aba..<ceiling>` using
      `repro-ghostty-vt-resize-hang.mjs` as the test (wasm-build each step,
      zig 0.15.2). **Verdict: `29d4aba` is the only commit in ghostty history
      that reproduces the hang.** It is a *mid-PR* commit inside ghostty PR
      #10337 and carries a hand-rolled capacity-doubling loop in
      `cursorSetHyperlink` (the author's own `// FIXME: This SUCKS`). First
      fixed commit = its direct child `25b7cc9f2` ("terminal: hyperlink state
      uses increaseCapacity on screen"), which replaces the loop with
      `increaseCapacity(.string_bytes)` — the same OSC 8 capacity family as
      the June `startHyperlink` fix.
- [x] Find the bisect ceiling. Two distinct ceilings, both well past the fix:
      textual — the ghostty-web wasm-api patch stops applying at `1844a5f7b`
      (broken by #11506); build — the last commit that builds in this
      configuration is `4244c38be` (broken by #10383).
- [x] Hang vs. traps: two complete production captures now replay the trap
      family. The shared `ab0b9da` build applies both cleanly; the latest was
      repeated five times.

## Phase 3 — Fix options, ranked

- [x] **3A/3B collapsed — pin bump to `56237efee`** (PR #10337 as merged to
  ghostty main). The bisect made the choice: the fixing commit is the pin's
  direct child, so cherry-picking it (3A) and bumping the pin (3B) reach the
  same code, and the merge commit additionally moves us off mid-PR pinning —
  the practice that made this bug reachable — and brings the rest of #10337's
  page-overflow hardening. `ghostty_commit` in
  `app/scripts/build-ghostty-vt-wasm.sh` bumped, wasm rebuilt with zig 0.15.2
  (both patches apply verbatim; export surface identical at 79 exports;
  reproducible sha256
  `6c4f21f514be21b13ff0911817458c69f26d22fb41469c10b68c322627266e85`), README
  pin/sha/rationale updated.
- [x] **Pin convergence — shared `ab0b9da` source.** `ghostty-vt.pin` is now
  the only source revision for native and WASM builds. The WASM build moved to
  Zig 0.16 and Ghostty's current `-Demit-lib-vt=true` build; the carried
  compatibility adapter implements the stable ghostty-web 0.4.0 surface over
  the current C API. The old ghostty-web source patch is no longer fetched.
- **3C. Client-side avoidance** — *demoted by Phase 2*: the hang reproduces
  through both resize call sites, so changing the mode-7 dance cannot be the
  fix for this bug. Retained only as a shape for any future fault that Phase 2
  shows to be call-site-specific. Constraint if ever used: no-reflow semantics
  must survive (block store correctness + replay at historical geometry).
- **3D. If no repro materializes** even via capture-on-fault: ship the
  capture instrumentation permanently and stop — recovery already contains
  the damage; do not fix blind.

## Phase 4 — Hardening + verification (any fix path)

- [x] Regression test: minimized repro as a vitest node-env test against the
      real wasm (`app/src/utils/ghosttyVtWasm.resizeHang.test.ts`, spawns
      `repro-ghostty-vt-resize-hang.mjs` and asserts exit 0). Verified to fail
      on the old `29d4aba` wasm (exit 1, HANG) and pass on the `56237efee`
      build.
- [ ] Keep a slim capture-on-fault ring in production builds permanently
      (bounded; this is the third crash class in this component in two months
      — startup hyperlink corruption in June, `bottom_clip`/`blank_after_resize`
      incidents ongoing, now this). *In flight as a separate change; not in
      the pin-bump PR.*
- [x] Live verification (isolated `ghostty-pin` profile, 2026-08-05): bundled
      preflight passed; `terminal-block-resize` passed across fish/bash/zsh
      resize and relaunch; `terminal-osc8-link` and `webgl-recovery` passed;
      and the real-agent `TR-401-CODEX-MAIN` scenario preserved Codex's complete
      frame through a 1280→704→1280 window cycle. Slow user-configured MCP
      servers were disabled for the final deterministic Codex startup. The
      profile diagnostics contained 316 events with zero `model_fault` records
      and zero model-fault recoveries before cleanup.
- [x] Changelog fragment (`changelog.d/ghostty-wasm-hang-pin-bump.yaml`).
- [x] Trap root-cause and shared-pin changelog fragment
      (`changelog.d/ghostty-vt-shared-pin-crash-fix.yaml`).

## Decisions

- **Not upgrading to ghostty-web `-next`**: no stable release; the one merged
  crash fix (#132) addresses a wrapper mechanism attn doesn't use; wrapper
  churn without evidence.
- **Pin convergence completed on 2026-08-05**: the trial's compatibility seam
  became the WASM-only adapter. A single `ghostty-vt.pin` now prevents the
  browser and worker cores from drifting independently.
- **Root-cause over live-with-recovery**: recovery masks the trap but the
  model state is corrupt *before* the trap fires — silent misrendering may
  precede the crash (see render-OOB variant and the open upstream #139), and
  the crash loop (twice in 23s) shows recovery alone doesn't converge.
- **Fuzz-first, capture second**: a synthetic repro is faster if it lands;
  capture-on-fault is the guaranteed-but-slower path and is worth shipping
  regardless (Phase 4).

## Open questions

- Does the render-OOB fault share the same stale-page root? The allocator
  violation explains both signatures and the captured write traps are fixed,
  but no self-contained render-OOB capture exists to prove identity.
- Why does a synchronous wasm infinite loop in production manifest as a trap
  instead of a frozen UI? (Or does it — are there unexplained UI freezes?) If
  the hang can fire in production, the write chain never drains and the pane
  wedges without a `model_fault` record; worth checking whether any
  `blank_after_resize` incidents are actually this hang.

## Follow-ups

- Report the resize-hang repro upstream to coder/ghostty-web (and ghostty-org
  once the culprit/fixing commit is identified) — also nudges #137 (stable
  release). The frozen repro is self-contained and ready to attach.
- The chronic `bottom_clip` / `blank_after_resize` incident families (44 on
  record since 2026-07-08) are adjacent but distinct geometry bugs — not in
  scope here; ticket separately if they persist after this fix.
