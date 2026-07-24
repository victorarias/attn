# vt10x retirement: single ghostty-backed terminal model

**Status: APPROVED 2026-07-24.** Decisions: extend the epic (Phases 1–3 stack onto
`epic/server-authoritative-terminal`); parity gate = corpus + live verify (no
extra burn-in); `ghosttyvt.New` failure becomes spawn-fatal on supported
platforms once vt10x is gone.

## Goal

Replace the vt10x `virtualScreen` with `internal/ghosttyvt` everywhere, ending the
dual-emulator era: one parsed terminal per session, worker-owned, backing all four
consumers. Delete vt10x from `go.mod`. PR #648 (Linux ghostty, merged into the
epic) removed the platform blocker: ghosttyvt now links and runs on darwin/arm64,
linux/amd64, and linux/arm64 — every shipped target.

This is cleanup *and* refactoring: the dual feed, the vt10x-internals bit
mirroring, and the two parallel snapshot payload families all go away.

## Current state (verified 2026-07-24)

vt10x has exactly four consumers, all via `virtualScreen`
(`internal/pty/snapshot.go`):

| Consumer | Call site | Needs |
|---|---|---|
| Approval classifier | `session.go` `evaluateApproval` → `renderedText()` | plain text, viewport rows, trailing blanks trimmed |
| CPR replies | `session.go` `writeCursorPositionResponse` → `Snapshot()` | cursor x,y (viewport, 0-indexed) |
| Grid tiles / automation | `session.go` `screenSnapshot()` → `Snapshot()` → `get_screen_snapshot` → `GridCompositor.seedTile` | styled byte stream replayable into a fresh frontend Ghostty model + cols/rows |
| Shadow-divergence oracle | `logShadowDivergence` | deleted by PR #647 |

`seedTile` resizes a frontend Ghostty model and writes raw bytes — exactly the
shape of a ghostty VT dump. The attach-restore path already serves that dump; this
plan converges the tile path onto the same payload family.

ghosttyvt gaps (all natively supported by libghostty-vt, wrapper just doesn't
expose them yet): cursor position (internal `cursorXYLocked` exists), cursor
visibility (`ghostty_terminal_mode_get` + `GHOSTTY_MODE_CURSOR_VISIBLE`),
viewport-only plain text (formatter plain emit + `GhosttySelection`), viewport-only
styled dump (formatter styled emit + selection).

## Phases

Each phase is one PR. Base: after PR #647 lands (it deletes the shadow-divergence
code this plan would otherwise collide with; it currently has a Frontend check
failure to fix first).

### Phase 0 — prerequisites

- Fix #647's Frontend check failure; merge #647 into the epic.
- DECIDED: extend the epic — Phases 1–3 land as PRs against
  `epic/server-authoritative-terminal`; the epic merges to main with retirement
  included.

### Phase 1 — ghosttyvt API additions (parity-proven against vt10x)

New wrapper surface (pinned; implementer does not reshape):

```go
// CursorPos returns the active-screen cursor position in 0-indexed
// viewport coordinates.
func (t *Terminal) CursorPos() (x, y int)

// CursorVisible reports DECTCEM (DEC private mode 25).
func (t *Terminal) CursorVisible() bool

// ViewportText returns the visible screen as plain text: one line per
// viewport row, trailing blanks trimmed per row, every row terminated
// with \n. Matches the shape virtualScreen.renderedText produced.
func (t *Terminal) ViewportText() string

// SerializeViewport returns a self-contained styled VT stream of the
// visible screen only (no scrollback): reset + styled viewport paint +
// cursor position + cursor visibility. Replaying it into a fresh
// terminal of Snapshot.Cols x Snapshot.Rows reproduces the viewport.
func (t *Terminal) SerializeViewport() Snapshot
```

Stub versions: `CursorPos` → (0,0), `CursorVisible` → false, `ViewportText` → "",
`SerializeViewport` → `Snapshot{Cols,Rows}` with nil VTDump (mirrors `Serialize`).

Parity harness (the load-bearing part): while vt10x is still in the tree, add a
corpus test that feeds recorded real PTY byte streams into both emulators and
asserts `ViewportText()` == `renderedText()` and `CursorPos()` == vt10x cursor.
This is the classifier-parity gate — the corpus must include every prompt shape
`approvalResolver` matches on.

Fixture recording (decided): capture with `script` (raw output bytes) around
real sessions — no product change needed, since the corpus test only needs
realistic byte streams, not attn's own PTY plumbing. Corpus contents:
- claude approval prompt (tool approval), codex approval prompt
- claude working/streaming output, a Stop→idle tail
- fish prompt + resize-triggered redraw traffic
- an alt-screen TUI (e.g. vim or htop burst)
- a color/SGR-heavy stream (exercises styles for `SerializeViewport` round-trip)
Recorded at 80x24 and one non-default size (e.g. 120x40). Fixtures live in
`internal/pty/testdata/corpus/` (or ghosttyvt testdata — implementer follows
where the test lands) with a README documenting each fixture's origin, terminal
size, and what it exercises. Keep each fixture under ~200KB; trim long sessions
to the interesting window. Divergence failures must print the first differing
row of both renders.

Risk note: `SerializeViewport` depends on selection-restricted styled formatting
composing into a self-contained stream. Validate by round-trip (replay into fresh
ghostty, compare `ViewportText` + cursor). Fallback if selection semantics fight
us: serve the full `Serialize()` dump for tiles (correct, bigger payload) and keep
viewport-only as a follow-up.

### Phase 2 — repoint the three live consumers

- `writeCursorPositionResponse`: cursor from `s.ghostty.CursorPos()` (nil ghostty
  → 1;1 as today).
- `evaluateApproval`: `s.ghostty.ViewportText()` replaces
  `s.screen.renderedText()`.
- `screenSnapshot()` / `get_screen_snapshot`: payload from
  `s.ghostty.SerializeViewport()`; cursor fields from `CursorPos`/`CursorVisible`.
  Protocol shape unchanged in this phase (fields keep their meaning); frontend
  `seedTile` expected unchanged (bytes replay into a Ghostty model either way) —
  verify via grid e2e + live grid check.
- vt10x still present and fed in this phase — one PR of behavior change with an
  instant revert path (flip call sites back).
- Live verification: real approval flow (claude + codex) on a throwaway profile,
  fish resize (CPR consumer), grid view tiles, `needs_review` flows.

### Phase 3 — delete vt10x

- Delete `virtualScreen`, `renderVisibleFrame`, style/color encoding machinery,
  `ReplayScreenSnapshot`, the dual feed in the read loop, and the vt10x
  `go.mod`/`go.sum` entries.
- Corpus test converts from vt10x-as-oracle to golden fixtures (same inputs,
  ghostty output snapshotted) so classifier-shape regressions stay caught.
- `snapshot_test.go` round-trip tests move to ghosttyvt level (replay
  `SerializeViewport` into fresh ghostty).
- Simplify locking: read loop feeds only `blockFeed`; document the reduced
  `replayMu` invariant.
- Docs: AGENTS.md terminal section, server-authoritative-terminal plan doc,
  memory notes.

### Phase 4 — post-retirement cleanup (candidates, confirm scope at the time)

- Protocol slimming: `get_screen_snapshot_result` carries cursor/geometry fields
  the self-contained dump makes redundant; possible convergence with the attach
  snapshot message family. ProtocolVersion bump + three-file lockstep.
- `AttachInfo` carries two snapshot families (`ScreenSnapshot*` and `Ghostty*`);
  collapse to one.

## Risks and mitigations

1. **Classifier regression** (safety-relevant `pending_approval` gating). The
   corpus parity test in Phase 1 is the primary gate; Phase 2 adds live
   verification with real agents. DECIDED: corpus + live verify is the gate; no
   additional time-boxed burn-in.
2. **ghostty construction failure becomes load-bearing.** Today `ghosttyvt.New`
   failure degrades to vt10x silently. After Phase 3, a nil ghostty means no
   classifier, no CPR, no tiles, no attach restore. DECIDED: spawn-fatal on
   supported platforms (loud, honest). Stub platforms never fail `New` and run
   degraded. Note: Phase 3 removes the nil-guard pattern for supported builds
   accordingly (`manager.go` error path returns the spawn error).
3. ~~Stub platforms lose the screen model~~ RESOLVED (Victor, 2026-07-24):
   Intel Macs are not supported by attn at all; darwin/arm64 + linux amd64/arm64
   is the complete support matrix. The ghosttyvt stub remains only as a
   buildability shim for unsupported GOOS/GOARCH combos, never a product path —
   Phase 3 should say so in its doc comment.
4. **CPR coordinate semantics.** Ghostty cursor must be viewport-relative on the
   active screen (fish blocks its prompt on this reply). Covered by corpus
   cursor parity + live fish-resize check.
5. **Tile payload size.** Full dumps include up to 10k scrollback lines;
   `SerializeViewport` keeps tile payloads viewport-sized. If we hit the
   fallback (full dump), measure grid-open payload sizes before accepting.
6. **PR #647 overlap** — land it first (Phase 0).
7. **figgyster native witness** — Phases 1–3 touch darwin cgo paths; each PR
   needs a current-head native-tier live witness posted before review.

## Code smells found during exploration (tracked)

Addressed by this plan:
- `snapshotAttr*` constants mirror vt10x *internal* glyph-mode bits — hidden
  coupling beyond the public API (`snapshot.go:12-19`). Dies in Phase 3.
- `appendColorParams` guesses at vt10x's ambiguous palette-vs-truecolor int
  encoding (`snapshot.go:310-331`). Dies in Phase 3.
- Dual-emulator feed: two parsers, three locks (`replayMu` + `screen.mu` +
  `ghostty.mu`) for one byte stream. Simplifies in Phase 3.
- `ReplayScreenSnapshot` is an exported field-for-field twin of the private
  `screenSnapshot`. Dies in Phase 3.
- Two snapshot payload families in `AttachInfo` + protocol. Phase 4.

Noted during execution (append here as found):
- **Parity finding (2026-07-24): vt10x mis-parses the codex TUI's scroll-region
  traffic** (DECSTBM margins + CSI S scroll-up): interleaved rows and a wrong
  cursor (vt10x (0,3) vs ghostty (0,17)). A real tmux replay of the fixture
  agrees with ghostty exactly (render + cursor). So current production CPR
  replies and classifier text are subtly wrong during codex sessions —
  retirement fixes a live bug. Encoded in the corpus test as a golden-file
  fixture with a tripwire (fails if vt10x ever starts agreeing).
- ghosttyvt.ViewportText blank-screen contract bug caught by the vim fixtures
  (empty formatter output conflated with failure → "" instead of rows×"\n");
  fixed on the Phase 1 branch before it ever shipped.
- Flaky test (hit as #647's "failed" Frontend check, pre-existing on the epic):
  `SessionTerminalWorkspace.ticketOverlay.test.tsx` waits for
  `ticket-detail-panel` (the container) then immediately fires a change event at
  `ticket-status-select`, which only renders after the async `fetchTicket` mock
  resolves — a load-dependent race. Fix: wait for the interactive element
  itself. Being fixed as its own small PR.
