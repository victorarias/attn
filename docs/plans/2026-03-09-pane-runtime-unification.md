# Pane Runtime Unification Plan

Date: 2026-03-09  
Status: Proposed  
Owner: frontend workspace / PTY lifecycle

## Summary

Refactor terminal pane handling so every terminal pane, including the main Claude/Codex pane, uses the same pane-to-runtime binding model.

The target behavior is:

1. a pane remount is safe and idempotent
2. input/output lifecycle does not depend on whether a pane is "main" or "utility"
3. split, close, focus, maximize, and session switches do not relaunch or mis-bind live PTYs
4. pane bugs are diagnosable at the pane/runtime boundary instead of being spread across store and UI code
5. future pane types can be added without introducing another terminal lifecycle path

This is a structural refactor, not just a focus bug fix.

## Handover status

This document is intended to be sufficient for a fresh coding agent to take over without reading the entire conversation history.

### Branch and working tree

Last known pushed branch state:

1. branch: `persistent-split-workspaces`
2. latest pushed commit before this investigation: `8642d45` (`Fix split pane focus and spatial navigation`)

Current local working tree during this investigation is intentionally dirty and includes temporary debug work. At the time of writing, the relevant local-only files are:

1. [`app/src/store/sessions.ts`](/Users/victor.arias/projects/victor/attn/app/src/store/sessions.ts)
2. [`app/src/App.tsx`](/Users/victor.arias/projects/victor/attn/app/src/App.tsx)
3. [`app/src/components/Terminal.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/Terminal.tsx)
4. [`app/src/components/SessionTerminalWorkspace/index.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/index.tsx)
5. [`app/e2e/utility-terminal-realpty.spec.ts`](/Users/victor.arias/projects/victor/attn/app/e2e/utility-terminal-realpty.spec.ts)

Treat these local changes as **investigation scaffolding**, not final architecture.

### What has been proven

These findings are the important handoff facts:

1. the bug is not isolated to Claude; it can move between panes after split-tree changes
2. the current design has two different PTY/pane ownership paths:
   1. main pane via session store
   2. utility panes via workspace component
3. that asymmetry is sufficient to explain why bugs show up in one pane but not another
4. real-PTY reproduction is possible and should be used instead of speculative fixes

### What has been observed directly in automation

Using a targeted real-PTY Playwright repro:

1. a split can be created through daemon workspace commands
2. pane remounts happen during split-layout changes
3. main-pane input events can still be emitted while the visible buffer changes in unexpected ways
4. earlier versions of the local patch set showed the main pane reconnecting during split flow
5. later local patches reduced some stale-ref issues, but the system still does not have a coherent terminal lifecycle model

### What should not be assumed

Do **not** assume the current local patch set is the right fix.

In particular, do not lock in:

1. temporary debug event hooks on `window`
2. any ad hoc reconnect bypass in the session store as final architecture
3. the current real-PTY test assertions as the final regression shape

Use the findings, not the current patch set, as the handoff artifact.

## Problem

The current design has two different ownership models for terminal panes:

1. the main session terminal is owned by the session store in [`app/src/store/sessions.ts`](/Users/victor.arias/projects/victor/attn/app/src/store/sessions.ts)
2. utility panes are owned by the workspace component in [`app/src/components/SessionTerminalWorkspace/index.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/index.tsx)

That asymmetry leaks implementation history into user behavior.

Observed symptoms:

1. one pane can lose input while another pane in the same workspace still works
2. main-pane remounts can behave differently from utility-pane remounts
3. split-related bugs move between panes instead of staying localized
4. focus, attach, restore, and input subscription bugs are hard to reason about because ownership is split

## Investigation findings

This section captures the most important concrete observations from the debugging pass.

### 1. Ownership asymmetry is real, not theoretical

Main session terminal lifecycle currently lives in the session store:

1. bind
2. input subscription
3. PTY spawn/attach path
4. some remount handling

Utility pane lifecycle currently lives in the workspace view:

1. xterm refs
2. input subscriptions
3. pending PTY output queue
4. restore attach path

This is the strongest architectural smell in the current implementation.

### 2. User symptom is pane-relative, not process-relative

Observed user reports during this investigation:

1. originally: after split, returning to Claude could lose input
2. later: Claude recovered, but one utility pane lost input while another still worked

That is classic evidence of per-pane lifecycle bugs, not a single bad “Claude focus” bug.

### 3. Repro must use real PTY

Mock PTY is not good enough for this class of issue.

The right reproduction surface is:

1. launch a real session
2. create splits through the real workspace control path
3. move focus between panes
4. assert interactive behavior per pane

### 4. Startup UX can distort the repro

Claude may enter:

1. workspace trust prompt
2. resume/session picker
3. loading conversations state

depending on cwd and session setup.

Any regression test should control for that or explicitly tolerate it.

## Architectural diagnosis

The user-facing model is:

1. a session owns a workspace
2. a workspace owns panes
3. terminal panes bind to PTY runtimes

The code today does not match that model.

Instead it has:

1. a special-case main terminal path
2. a separate utility-pane path
3. duplicated input/focus/restore logic
4. mixed durable state and live xterm instance ownership

That is not intentful enough for long-term maintenance.

## Design goals

1. one abstraction for terminal pane lifecycle
2. explicit separation between durable workspace state and live terminal bindings
3. idempotent remount behavior
4. clear API boundaries
5. deep modules with narrow interfaces
6. extensibility for future pane types

## Reproduction and debugging guide

### Recommended repro command

Use the real-PTY Playwright path with a short temp root:

```bash
TMPDIR=/tmp VITE_MOCK_PTY=0 VITE_FORCE_REAL_PTY=1 ATTN_E2E_BIN=./attn \
  pnpm --dir app exec playwright test app/e2e/utility-terminal-realpty.spec.ts
```

For targeted runs:

```bash
TMPDIR=/tmp VITE_MOCK_PTY=0 VITE_FORCE_REAL_PTY=1 ATTN_E2E_BIN=./attn \
  pnpm --dir app exec playwright test app/e2e/utility-terminal-realpty.spec.ts \
  -g "main session keeps keyboard interactivity after returning from a split"
```

Notes:

1. `TMPDIR=/tmp` matters because Unix socket path length can otherwise break split worker creation in the managed daemon harness
2. use the real daemon/worker path, not mock PTY

### Manual repro worth preserving

The user-reported repro shape that matters most is:

1. create a split workspace with three panes
2. layout example: `claude | terminal a / terminal b`
3. move back and forth between panes
4. one pane can stop accepting input while another remains interactive

### Suggested debug questions

When a pane fails, answer these in order:

1. did the visible xterm instance remount?
2. did `onData` on the visible pane still fire?
3. did `ptyWrite` fire for the pane runtime?
4. did the visible buffer keep updating?
5. did the pane accidentally re-enter attach/spawn/recovery behavior?

## Non-goals

1. changing daemon-owned workspace authority
2. redesigning the PTY backend protocol
3. adding pane drag-and-drop in this pass
4. changing session/sidebar semantics

## Target model

### 1. Session

A user-visible container.

Owns:

1. label
2. cwd
3. agent/session metadata
4. workspace snapshot reference

Does not own:

1. live xterm instances
2. pane input subscriptions
3. PTY attach/restore logic

### 2. Workspace

A pure pane/layout model.

Owns:

1. pane ids
2. active pane
3. split tree
4. pane metadata
5. pane-to-runtime mapping

Does not own:

1. xterm instances
2. PTY stream subscriptions

### 3. Runtime binding

A live controller that binds one pane to one runtime.

Owns:

1. xterm mount/unmount
2. input subscription
3. output restore/reattach
4. resize
5. focus
6. remount idempotence

This is the missing abstraction today.

## Recommended module boundaries

### `workspace model`

Pure layout domain in [`app/src/types/workspace.ts`](/Users/victor.arias/projects/victor/attn/app/src/types/workspace.ts) plus daemon-side workspace domain.

Owns:

1. pane graph
2. active pane
3. spatial navigation
4. validation and normalization

### `pty runtime client`

Thin layer over the existing PTY backend and daemon attach/restore semantics.

Owns:

1. attach
2. detach
3. write
4. resize
5. restore payload handling

Should present one interface regardless of pane type.

### `terminal pane binder`

New frontend module. This should be the only place that knows how a mounted xterm talks to a PTY runtime.

Owns:

1. bind runtime to xterm
2. unbind on unmount
3. rebind on remount without relaunching
4. restore output to fresh xterm instances
5. attach custom key handlers
6. own `onData -> ptyWrite`

This module should be reusable by:

1. main session pane
2. shell utility panes
3. future log/output panes if they become interactive

### `workspace view`

[`app/src/components/SessionTerminalWorkspace/index.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/index.tsx) should become a renderer/controller only.

Owns:

1. pane rendering
2. split controls
3. focus commands
4. maximize state

Does not own:

1. raw PTY lifecycle details
2. duplicated input wiring logic

### `session store`

[`app/src/store/sessions.ts`](/Users/victor.arias/projects/victor/attn/app/src/store/sessions.ts) should hold durable app state, not live terminal object lifecycle.

Owns:

1. local session metadata
2. daemon sync state
3. active session selection

Does not own:

1. main-pane xterm binding logic

## API sketch

The main API should look like one binder/controller instead of separate main/utility flows.

Example shape:

```ts
interface PaneRuntimeBinding {
  bindTerminal(paneId: string, runtimeId: string, terminal: XTerm): void;
  unbindTerminal(paneId: string): void;
  focusPane(paneId: string): boolean;
  resizePane(paneId: string, cols: number, rows: number): void;
}
```

Internally it may track:

1. pane id -> mounted terminal instance
2. runtime id -> stream/attach state
3. pending output for unmounted panes
4. input subscription cleanup handles

But those details should stay private to the module.

## Current code hotspots

These are the most relevant files for the refactor:

### Frontend workspace rendering

1. [`app/src/components/SessionTerminalWorkspace/index.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/index.tsx)

Why it matters:

1. owns utility pane xterm refs
2. owns utility input subscriptions
3. owns restore attach path for utility panes
4. currently knows too much about PTY wiring

### Main session terminal lifecycle

1. [`app/src/store/sessions.ts`](/Users/victor.arias/projects/victor/attn/app/src/store/sessions.ts)

Why it matters:

1. owns the main pane binding path
2. stores live terminal instance reference
3. wires `onData -> ptyWrite`
4. currently mixes state store responsibilities with xterm lifecycle

### Terminal wrapper

1. [`app/src/components/Terminal.tsx`](/Users/victor.arias/projects/victor/attn/app/src/components/Terminal.tsx)

Why it matters:

1. owns xterm construction/disposal
2. exposes the imperative terminal handle
3. is where stale ref and disconnected-instance mistakes become easy

### PTY transport bridge

1. [`app/src/pty/bridge.ts`](/Users/victor.arias/projects/victor/attn/app/src/pty/bridge.ts)
2. [`app/src/hooks/useDaemonSocket.ts`](/Users/victor.arias/projects/victor/attn/app/src/hooks/useDaemonSocket.ts)

Why they matter:

1. attach/restore already exists in the daemon socket layer
2. any new pane binder should reuse existing transport semantics, not invent a parallel PTY protocol

## Key design rules

### Rule 1: no special main-pane terminal lifecycle

The main pane is just another terminal pane bound to a different runtime kind.

### Rule 2: no live xterm instances in long-lived app state

Zustand/session state should not be the owner of mounted xterm lifecycle.

### Rule 3: remount must not imply respawn

A pane remount is a UI event, not a runtime lifecycle event.

### Rule 4: one source of truth for input binding

`onData -> ptyWrite` should live in one module, not in both the store and the workspace renderer.

### Rule 5: output restore is attach semantics, not spawn semantics

If a pane gets a new xterm instance, it should rebind to the existing runtime and restore visible output without perturbing the underlying process.

## Migration plan

### Phase 1: freeze behavior with reproduction coverage

1. keep the real-PTY reproductions for:
   1. main pane after split
   2. 3-pane workspace where one utility pane can lose input
2. add assertions around:
   1. pane remount count
   2. input event emission
   3. visible buffer restore

### Phase 2: introduce the binder

1. extract duplicated utility-pane terminal lifecycle code into a reusable binder
2. keep behavior unchanged initially
3. make the binder reusable for one pane at a time

Deliverable:

1. new binder module or hook
2. no behavioral migration yet
3. unit coverage for mount/unmount/rebind semantics

### Phase 3: move main pane onto the binder

1. stop letting the session store own main-pane xterm lifecycle
2. route main-pane bind/unbind/input/restore through the same binder
3. remove special-case reconnect logic from the store

Deliverable:

1. no live xterm instance stored in Zustand
2. one input subscription path for all terminal panes

### Phase 4: collapse duplicated lifecycle code

1. remove duplicated `attachCustomKeyEventHandler`
2. remove duplicated `onData` wiring
3. remove duplicated pending-output queues where possible

### Phase 5: cleanup

1. remove temporary instrumentation
2. keep the regression tests
3. document the new ownership boundaries in code comments where needed

## Recommended implementation order

If starting from scratch with a fresh agent, this is the sequence I recommend:

1. cleanly inspect and, if necessary, discard the current investigation-only local edits
2. preserve or rebuild one trustworthy real-PTY repro for:
   1. main pane after split
   2. 3-pane split where one utility pane can fail
3. implement a `pane runtime binder` without migrating behavior
4. move utility panes onto the binder first, since their lifecycle is already more local
5. move the main pane onto the same binder
6. delete main-pane xterm lifecycle ownership from the session store
7. rerun the real-PTY repros
8. remove debug instrumentation

Why this order:

1. utility panes already live near the workspace view
2. main-pane migration is the architectural win
3. deleting store-owned xterm state should be the end of the migration, not the first step

## Risks

1. accidental PTY relaunch on remount
2. losing visible scrollback during rebind
3. broken keyboard shortcuts because key handlers move layers
4. stale pane refs surviving unmount
5. hidden-pane resize weirdness surfacing during migration
6. duplicating attach/restore logic instead of centralizing it

## Success criteria for handoff

A handoff receiver should consider this refactor complete only when all of the following are true:

1. the main pane and utility panes are bound through the same lifecycle abstraction
2. the session store no longer owns live xterm object lifecycle
3. remounting a pane does not change the underlying runtime state
4. the 3-pane real-PTY repro passes reliably
5. temporary debug hooks have been removed
6. the code can be explained in one sentence:

`A session owns a workspace, a workspace owns panes, and terminal panes bind to runtimes through one binder.`

## What to ignore from the current investigation

Unless explicitly reused as part of the final refactor, the following are disposable:

1. temporary `window.__TEST_*` debug surfaces
2. any partial reconnect workaround in the store
3. any one-off focus retry tweak that exists only to paper over the split ownership model

Keep:

1. the architectural direction
2. the real-PTY repro strategy
3. the requirement that all panes share the same runtime binding semantics

## Preserved behaviors

These must survive the refactor:

1. main Claude/Codex session remains the primary pane
2. shell panes remain daemon-owned runtimes
3. `Cmd+D` and `Cmd+Shift+D` split the active pane
4. `Cmd+Alt+Arrow` spatial navigation still works
5. pane focus should remain local and immediate
6. switching sessions should preserve pane interactivity
7. dashboard/session roundtrips should preserve pane interactivity
8. long-running PTYs should not be relaunched by pane remounts

## Test strategy

### Unit tests

1. pane binder bind/unbind/remount semantics
2. pane-to-runtime rebind without respawn
3. pending output restore to newly mounted xterm

### Frontend integration tests

1. split one pane into two and return to main pane
2. split into three panes and verify all panes keep input
3. switch sessions and back with split panes
4. dashboard roundtrip with split panes

### Real-PTY tests

Required:

1. main Claude pane accepts input before split
2. main Claude pane accepts input after split/remount
3. both utility panes accept input after creating a 3-pane layout
4. no pane relaunches or re-enters startup UI because of a remount

## Acceptance criteria

1. there is one terminal pane lifecycle path for main and utility panes
2. no live xterm ownership remains in the session store
3. remounting any pane does not relaunch or mis-bind its runtime
4. the 3-pane real-PTY reproduction passes reliably
5. the code is easier to explain in terms of `session -> workspace -> pane -> runtime`

## Open questions

1. should the binder live in a standalone hook, service module, or component-local controller?
2. should PTY attach/restore be surfaced directly through `pty/bridge`, or should the binder continue to rely on the current daemon socket layer indirectly?
3. should pane output restoration prefer screen snapshots over text replay for all pane types, or only for full-screen agents?

## Recommendation

Proceed with the refactor.

Do not keep patching the current split ownership model.

The system should be reorganized so that pane/runtime binding is the explicit frontend primitive and every terminal pane goes through it.
