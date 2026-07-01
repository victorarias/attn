# Plan: Architecture review — open candidates needing a design conversation

Companion to the six implementation plans from the 2026-07-01 architecture
review. These five candidates have real friction but a wrong-by-default risk:
each has at least one design fork only Victor can call. This plan captures the
friction (so it isn't lost with the temp-dir review report), what to
investigate up front, and the specific questions to settle — so each grilling
conversation starts from evidence, not from scratch.

**Exit criterion per candidate**: a decision recorded here (and, where the
reason is load-bearing for future reviews, in `docs/decisions/`), then either a
dedicated plan doc like the other six, or an explicit drop.

Line references as of `80c62f6b` — re-anchor by symbol.

## 1. Give the keeper a module

**Friction.** `docs/glossary.md` defines the keeper as one persona with two
causally-coupled duties ("prune *because* the story is preserved"), but the code
has no keeper type: ~30 methods and 8 loose fields on `Daemon` across
`workspace_keeper.go` (682 L), `notebook_narration.go` (749 L),
`notebook_narration_config.go`, `notebook_narration_prompts.go`, with a naming
schism (`keeperCompact*` vs `notebookNarrate*`). The preserve-then-prune
invariant is expressible nowhere.

**Investigate before discussing.**
- [ ] Inventory the daemon dependencies of the 30 methods (store, tasks runner,
      agent driver, notebook paths, settings) to see whether a clean
      `internal/keeper` package is feasible or whether it stays a daemon-side
      type first.
- [ ] Sketch the module's interface twice (package vs daemon-owned struct) and
      compare on locality and import cycles.

**Questions for Victor.**
- Package (`internal/keeper`) or daemon-internal type as the first step?
- Should the preserve-then-prune invariant become *enforced* (compact refuses to
  run unless narration for that span succeeded) or stay best-effort with the raw
  tier as the floor? Enforcement changes failure behavior — that's a product
  call, not a refactor.

## 2. Command registry instead of the two mega-switches

**Friction.** Dispatch is 105 WS arms (`websocket.go:819-1092`) + 48 socket arms
(`daemon.go:1858-2003`) + 181 type assertions with zero behavior; commands exist
twice per transport (paired `handleXxx`/`handleXxxWS`), and
`newInternalWSClient` (`delegate.go:31-49`) has the socket path impersonating a
WS client and unmarshalling its own output. Unknown commands drop silently.

**Investigate before discussing.**
- [ ] Prototype `register[T](cmd, handler)` + a `responder` seam
      (`connResponder`/`wsResponder`) on ONE command cluster (e.g. the PR
      actions) to measure the real boilerplate delta and find where the
      pre-switch cross-cutting (capability gate ~833, recovery gate ~842,
      selection capture, remote routing) resists tabling.
- [ ] Decide what the registry does on unknown cmd (error event vs log) — today's
      silent default is arguably a bug.

**Questions for Victor.**
- Worth the churn now, or sequence strictly after the test harness plan lands
  (recommended: harness first — it's the regression net proving dispatch
  equivalence arm-by-arm)?
- Unknown-command behavior: silent (status quo) or explicit error reply?

## 3. One truth for sessions in the app

**Friction.** A session's truth lives in ~5 places: socket refs
(`useDaemonSocket.ts:785-791`) → 17 `on*Update` callbacks (wired
`App.tsx:558-580`) → `useDaemonStore` + App-local `useState`
(`daemonWorkspaces:324`) → `useSessionStore.syncFromDaemonSessions`
(`App.tsx:1120`) → `enrichedLocalSessions` re-merge — drilled through a
103-member `AppContentProps` (702-807). Stale-state bugs have five candidate
homes (a documented past example: the laggy ticket-panel refresh).

**Investigate before discussing.**
- [ ] Map which of the 5 copies each consumer actually reads (grep the imports
      of the two zustand stores + the props) — the migration order falls out of
      that map.
- [ ] Check what `enrichedLocalSessions` adds beyond daemon data (local-only
      fields?) — those need a home in the normalized store.

**Questions for Victor.**
- Target: one normalized zustand store + `useDaemon()` context, or keep two
  stores (daemon-mirror + local-ui) with a single sync point?
- Appetite: this is the largest frontend program of the set; do we stage it per
  entity (sessions → workspaces → PRs/tickets) or per consumer?

## 4. Terminal invariants: from AGENTS.md prose to modules

**Friction.** Focus ownership (AGENTS.md #6) and geometry/replay authority
(AGENTS.md #7) are enforced only by comments across ≥5 files (`App.tsx:1698,1896`,
`SessionTerminalWorkspace/index.tsx:466-487`,
`GhosttyTerminal.tsx:139-171,2317-2356`, `useGhosttyPaneRuntime.ts:162`,
`terminalQueryResponses.ts:30-43`, `pty/session.go:864-899`), with zero
automated tests — AGENTS.md itself admits the focus spec was removed and never
replaced. The 20-method `GhosttyTerminalHandle` pushes replay-vs-live
correctness onto callers via option flags.

**Investigate before discussing.**
- [ ] Enumerate every focus write (the review counted `focus` refs: 37 App / 45
      STW / 41 GhosttyTerminal / 10 GridView) and classify which are the
      *decision* (who should own focus) vs the *mechanism* (calling
      `.focus()`) — only the decision moves into a `focusOwner` resolver.
- [ ] Draft the intent-level handle (`applyLiveOutput` / `applyReplay` /
      `requestFit`) against the current call sites to confirm nothing needs the
      raw flags.

**Questions for Victor.**
- Is a pure `focusOwner` resolver + packaged-app spec enough, or do we also want
  the handle narrowing in the same effort? (The handle work is riskier — it
  touches the reattach-hang history.)
- Any appetite for a Go-side `GeometryAuthority` type, or is the daemon half
  fine as-is with just a contract test?

## 5. One attach payload across the PTY seam + ReplayPlanner

**Friction.** The attach payload is defined three times (~18 fields:
`pty/manager.go:74`, `ptybackend/backend.go:59`, `ptyworker/protocol.go:111`)
with four hand-written copy functions (`embedded.go:108`, `runtime.go:37`,
`worker.go:645,981`); the 125-line replay-tail selection tree lives in the
daemon (`ws_pty.go:210-335`) one seam away from the ring invariants it
preserves; and startup recovery forks on a `RecoverableRuntime` type-assert that
really means "am I on the worker backend?" (`daemon.go:908,1190`).

**Investigate before discussing.**
- [ ] Determine whether the wire struct (`ptyworker/protocol.go`) can share the
      `pty` type directly (JSON tags, stability across worker RPC versions —
      note RPC compat is never tested across a real version gap,
      `protocol.go:5-21`) or needs codegen.
- [ ] Sketch `pty.ReplayPlanner`'s interface and check the worker path can serve
      it without new RPC methods.

**Questions for Victor.**
- Shared type vs codegen for the wire payload (shared type couples worker RPC
  stability to `internal/pty` — acceptable?).
- Fold the `Recover`-unification in here or treat it as its own small PR?

## Decisions

- These five stay investigation-first because each has a fork where the cheap
  option changes behavior (keeper enforcement, unknown-command reply, handle
  narrowing, RPC coupling) — encoding a guess into an implementation plan would
  hand a weaker model a coin-flip.

## Follow-ups

- Also parked from the review's small list: deriving `ProtocolVersion` from a
  schema hash (formatting-churn question), `transcript.Extract*` options
  collapse, hub `EndpointIDFor(kind, id)` unification, shared WebGL atlas/glyph
  core between `GhosttyWebGlRenderer` and `UnifiedGridRenderer`, and a worker
  RPC version-skew contract test.
