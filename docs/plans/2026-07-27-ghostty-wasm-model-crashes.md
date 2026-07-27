# Plan: Fix ghostty WASM terminal model crashes (resize-triggered faults)

## Goal

Root-cause and fix the recurring frontend terminal crashes behind the
"Terminal issue recovered. We reloaded it for you." toast. The crash is a WASM
trap inside the vendored ghostty VT core (`app/vendor/ghostty-vt/ghostty-vt.wasm`,
pin `29d4aba`), not a rendering or lifecycle bug in the React layer. Recovery
already works (server-authoritative snapshot remount in ~20ms, no data loss);
the goal is that the model stops faulting, plus permanent instrumentation so
any future fault of this class arrives with its own repro.

## Evidence (2026-07-27, production profile)

Three faults on record in `$APPLOCALDATA/debug/terminal-diagnostics.jsonl`
(window starts 2026-07-08), all on one codex session (`thunk`,
`8f9f5813-…`, a docked agent tile):

| time (UTC) | operation | error | context |
|---|---|---|---|
| 11:52:00 | `render` | `Out of bounds memory access` in `update()` (wasm fn 461) | cols 134 |
| 19:09:33 | `write` | `Unreachable code should not be executed` in `ghostty_terminal_write` (wasm 510→219→213→328→460) | first PTY write after fit resize 59→60 @58 rows |
| 19:09:56 | `write` | same trap, same stack | 23s after recovery: remount at 60 → drag resizes 75…93→73→66→67→133→134 → first write → trap |

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

- [ ] Land a fuzz/replay harness that loads the real vendored wasm through the
      real ghostty-web wrapper (pattern: `app/src/utils/ghosttyHyperlinks.test.ts`),
      generates codex-TUI-like content (alt-screen repaints, OSC 8 hyperlinks,
      wide chars, soft-wrapped primary scrollback), and replays production's
      exact resize semantics: mixed `resizeGhosttyWithoutReflow` and plain
      `resize()`, single-column bursts up and down, stale-width writes after
      resize. Seeded and minimizing. (A first version exists in the session
      scratchpad, `fuzz.mjs`; sweep was still running when this plan was
      written — fold its result in here.)
- [ ] If fuzzing does not reproduce: add **capture-on-fault** instrumentation
      and wait for the next real fault (this class recurs; 3 faults today).
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
- [ ] Minimize any repro to a fixture and commit it as a vitest regression
      test (`node` environment, real wasm, no mocks).

## Phase 2 — Root-cause discrimination (with repro in hand)

```text
repro crashes with plain resize() substituted for the no-reflow wrapper?
├─ yes → pure core bug at 29d4aba
│        └─ bisect ghostty 29d4aba..<last old-C-API commit>, wasm-build each
│           step (zig 0.15.2 while it works), find fixing commit → Phase 3A/3B
└─ no  → the mode-7 no-reflow dance is the enabler
         ├─ still bisect (the fix may exist upstream regardless)
         └─ evaluate 3C alongside
```

Note the bisect ceiling: the old ghostty-web wasm-api patch only applies up to
the commit where upstream landed the new Terminal C API. Find that boundary
commit first; it caps how far forward 3B can go.

## Phase 3 — Fix options, ranked

- **3A. Cherry-pick the fixing commit onto `29d4aba`** (preferred; exact
  precedent: the June `startHyperlink` capacity fix, see
  `app/vendor/ghostty-vt/README.md`). Extend
  `ghostty-web-v0.4.0-compat.patch`, rebuild via
  `app/scripts/build-ghostty-vt-wasm.sh`, update the README sha + rationale,
  regression test green.
- **3B. Bump the WASM pin forward** to the newest pre-API-redesign commit, if
  the fixing commit lies within the patchable range and the patch still
  applies. Same build/README mechanics as 3A.
- **3C. Client-side avoidance** — only if Phase 2 shows the mode-7 dance is
  required for the trap and no upstream fix is cherry-pickable. Constraint:
  no-reflow semantics must survive (block store correctness + replay at
  historical geometry). Candidate shapes, in order: reorder the dance so mode
  writes and resize cannot straddle corrupt state (e.g. drop the mode-7 trick
  for live fit and instead re-request the server snapshot after a resize
  burst, which the server-authoritative restore makes cheap); or gate
  no-reflow to replay-only where it originated.
- **3D. If no repro materializes** even via capture-on-fault: ship the
  capture instrumentation permanently and stop — recovery already contains
  the damage; do not fix blind.

## Phase 4 — Hardening + verification (any fix path)

- [ ] Regression test: minimized repro as a vitest node-env test against the
      real wasm (fails on `29d4aba`, passes on the fixed build).
- [ ] Keep a slim capture-on-fault ring in production builds permanently
      (bounded; this is the third crash class in this component in two months
      — startup hyperlink corruption in June, `bottom_clip`/`blank_after_resize`
      incidents ongoing, now this).
- [ ] Live verification (dev profile, `make dev`): codex session in
      `~/projects/thunk`, divider-drag resize soak across single-column steps
      at 58 rows; confirm zero `model_fault` records in the profile's
      `terminal-diagnostics.jsonl` after a soak that previously trapped twice
      in 23s. Existing packaged scenario `terminal-block-resize` must stay
      green (it exercises the no-reflow path's block semantics).
- [ ] CHANGELOG entry (user-visible: terminal no longer flashes/reloads
      during split-divider drags).

## Decisions

- **Not upgrading to ghostty-web `-next`**: no stable release; the one merged
  crash fix (#132) addresses a wrapper mechanism attn doesn't use; wrapper
  churn without evidence.
- **Not converging pins now**: trial build proved `ab0b9da` needs a new
  ~15-function wasm shim against the redesigned C API (`render.h`,
  `grid_ref.h`) — a scoped project, tracked as a follow-up, not this fix.
- **Root-cause over live-with-recovery**: recovery masks the trap but the
  model state is corrupt *before* the trap fires — silent misrendering may
  precede the crash (see render-OOB variant and the open upstream #139), and
  the crash loop (twice in 23s) shows recovery alone doesn't converge.
- **Fuzz-first, capture second**: a synthetic repro is faster if it lands;
  capture-on-fault is the guaranteed-but-slower path and is worth shipping
  regardless (Phase 4).

## Open questions

- Fuzz sweep outcome (running at time of writing) — determines whether Phase 1
  finishes synthetically or via capture-on-fault.
- Is the render-OOB fault (11:52) the same corruption observed at a different
  entry point, or a second bug (upstream #139's page-boundary shape is
  column-width dependent — 120/130 repro, 80/140 don't — suspicious for our
  134-col pane)? The repro/bisect should try to answer both with one fixture.

## Follow-ups

- **Pin convergence** (single ghostty commit for native + WASM): rewrite the
  wasm-api shim against ghostty's new Terminal C API, dropping the
  ghostty-web patch dependency; zig 0.16; `-Demit-lib-vt=true` build form.
  Deserves its own plan; the trial-build mapping table is the starting point.
- Report the repro upstream to coder/ghostty-web (and ghostty-org if the
  fixing commit is identified) — also nudges #137 (stable release).
- The chronic `bottom_clip` / `blank_after_resize` incident families (44 on
  record since 2026-07-08) are adjacent but distinct geometry bugs — not in
  scope here; ticket separately if they persist after this fix.
