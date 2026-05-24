# Native Canvas MVP — Scope

> **Archived direction (2026-05-23).** This MVP is no longer the product
> target. The retained code lives under `attn-native-canvas-archive`; the
> replacement proposal is `native-gpui-workspace-client-prototype.md`.

Companion to `2026-04-28-canvas-perf-spike.md`. The spike proved we can build
a Rust + GPUI canvas client. This doc scopes the path from spike to
daily-driver.

## North Star

**The author can stop using the Tauri app for canvas work.** Built-from-source
is fine for a long time — no signing, packaging, or auto-update story needed
until much later.

## Locked Decisions

1. **Coexist with Tauri.** New bundle (`attn-native.app`), shared daemon.
   Tauri keeps shipping. Both clients speak the same protocol; the daemon is
   the source of truth, so they can run side-by-side without drift.
2. **MVP first, parity later.** Canvas-only first release. The author will
   dogfood and shape the "light" experience iteratively. Non-canvas surfaces
   (PR review, etc.) stay in Tauri until we get to them.
3. **Panel = one running thing.** A panel hosts exactly one of: a coding
   agent, a terminal, a todo list, a diff viewer, etc. Never two agents in
   one panel; never multiple terminals fused. **Splits are first-class** — a
   single key/gesture spawns a new adjacent panel. Navigation is
   keyboard-first, tiled-WM-like, but with canvas freedom (panels aren't
   confined to a strict grid).
4. **Daemon-synced everything.** Workspaces, sessions, panel layout, viewport
   state — all daemon-authoritative. Closing the app and reopening loses
   nothing. Different machines reading the same daemon (eventually) see the
   same canvas.
5. **Two-stage focus model.** A panel can be _selected_ (canvas-level focus,
   keyboard navigation operates on it) or _selected-with-input-focus_
   (keystrokes route into the panel's PTY/widget). Switching between the two
   is explicit and visible — the user always knows where keys go.

## Milestones

### M1 — Daily-driver canvas

The threshold: author runs real attn sessions in `attn-native.app` for a
working day without falling back to Tauri. Three sub-milestones, ordered by
"what unlocks dogfood":

#### M1.0 — Lifecycle + input

You can do work in the app, even if the UX is rough.

- Real terminal input (typing, paste, modifier keys). Daemon-side
  `send_input` is missing — both ends need wiring.
- Create / destroy workspaces from the canvas.
- Create / destroy sessions (Claude / Codex / Shell / etc.) within a
  workspace, choosing agent + directory + branch.
- Panel layout persisted daemon-side. Close-and-reopen restores everything.
- Bare-bones focus model: one panel "active", keys land there.

#### M1.1 — Keyboard-native canvas

You stop reaching for the trackpad.

- Canvas navigation by keyboard: focus next/prev/up/down/left/right panel.
- **Splits as first-class operation**: a single binding spawns a new panel
  adjacent to the current one (left/right/up/down) and seeds it with a
  workspace-default agent or a chooser.
- Visible distinction between _selected_ and _selected-with-input-focus_,
  plus the binding to toggle.
- Pan/zoom via keyboard, not just trackpad. Zoom-to-fit, zoom-to-panel.

#### M1.2 — Awareness

You can leave panels running and the app tells you when to look.

- Per-panel state indicators (working / waiting_input / pending_approval /
  idle) — the same surface Tauri shows.
- Workspace status rollup color scheme (matching the existing protocol).
- OS notifications + sound for state transitions, with per-session muting
  and the same opt-out the Tauri app already supports.

### M2 — Non-canvas surfaces

Once M1 lands, the gap to "Tauri can be uninstalled" is the non-canvas
features. Order TBD; **start with Settings** because configuration is
load-bearing for daily use.

- Settings.
- PR review UI (or whatever the right replacement looks like — may differ
  from Tauri).
- Notification center / history.
- Onboarding / first-run.
- Profile (prod/dev) switcher.

### M3 — Ship

Off in the distance. Packaging, code signing, auto-update, telemetry, crash
reporting, deprecation of Tauri. Not on the critical path until M1+M2 are
real.

## Cross-Cutting Workstreams

These run in parallel to the milestones, evolving as features arrive.

- **Test infrastructure as a first-class concern.** Extend the automation
  sidecar from a perf harness into the e2e harness. Each new feature ships
  with the automation actions needed to drive it. Renderer snapshot tests
  where useful. `make test-native` becomes a real target.
- **Protocol additions.** Anything M1 needs that the daemon doesn't expose
  yet — panel layout storage, multi-panel-per-workspace, viewport
  persistence — is additive. Bump `ProtocolVersion` when the wire changes
  (per `AGENTS.md`).
- **Code organization.** Promote the spike: a new `attn-native` bin
  alongside `attn-spike{3,4,5}`, sharing `daemon_client`, `terminal_*`, the
  automation crate, and the protocol crate. Spikes stay buildable until
  they're clearly subsumed.
- **Daemon-side work.** Layout persistence, panel-create/destroy commands,
  splits semantics — all need Go-side handlers. Single source of truth means
  the daemon does real work, not just relay.

## Things To Figure Out As We Go

Listed for visibility, not for early decisions.

- **Splits semantics in a free canvas.** Tiling WMs have a clean
  "split-current-pane" because panes are tree nodes. We have a free canvas.
  Likely answer: snap-on-create (new panel docks adjacent to the source),
  free-drag thereafter. To validate by use.
- **The "selected vs. focused" binding.** Vim-style two-stage modes? A
  single Enter/Esc toggle? Sticky focus that releases on canvas keys? Pick
  one early, expect to revise.
- **Layout sync granularity.** Do we send every drag delta to the daemon,
  or settle on drag-end? Probably drag-end for v1.
- **Multi-machine canvas** — same daemon, two laptops. Does the canvas
  state travel? Likely yes given decision (4), but the conflict-resolution
  story is an M3+ problem.
- **Panel types beyond terminals.** Todo list, diff viewer, etc. Each new
  type is a small workstream of its own; defer until terminals + agents are
  rock-solid.
- **Performance under real load.** The spike held 60fps with 8 synthetic
  panels. Real workloads have more variance. Re-benchmark when M1.0 is real.

## Suggested First Step

M1.0 starts with `send_input` because nothing else matters without it. The
work breaks roughly into:

1. Daemon: confirm/extend the `send_input` command and its event
   acknowledgement. (May already exist for Tauri — audit before adding.)
2. Rust: wire keyboard events from the focused panel into the daemon.
3. Automation: a `type_into_panel` action so we can regression-test it.

Everything else in M1.0 (workspace/session create/destroy, layout
persistence) builds on the same protocol/automation muscles.
