# Persistent Split Workspaces Plan

Date: 2026-03-09  
Status: Proposed  
Owner: session/workspace architecture

## Summary

Persist and recover split-pane session workspaces so that attached terminals reopen exactly as they were after app close/reopen or daemon restart.

The target behavior is:

1. a top-level session still appears once in the sidebar
2. the main Claude/Codex terminal remains the primary pane
3. attached shell panes are first-class recoverable runtimes
4. the split tree, active pane, titles, and pane-to-runtime mapping survive restart
5. daemon recovery reconstructs the workspace before the app renders it

This should be implemented by making the daemon authoritative for workspace state and by separating **workspace sessions** from **PTY runtimes**.

## Product goal

Users should be able to:

1. split the current pane vertically or horizontally
2. create nested split structures
3. close/reopen the app and find the same split layout restored
4. have shell panes resume using the same underlying runtime whenever possible
5. switch sessions without losing pane layout or focused pane

## Why the current implementation does not persist

The current split workspace state is frontend-local.

Today:

1. the app stores split state in the browser-side Zustand store in [`app/src/store/sessions.ts`](/Users/victor.arias/projects/victor/attn/app/src/store/sessions.ts)
2. the daemon/store persists only the top-level session records
3. attached shell panes are spawned through the PTY path, but their pane metadata and layout tree are not persisted
4. app startup hydrates only from daemon sessions, so pane topology disappears on restart

Result:

1. Claude/Codex sessions survive because daemon/store + PTY worker recovery already support them
2. split panes do not survive because neither the layout tree nor the utility pane runtime metadata is daemon-owned

## Architectural decision

The daemon must become the source of truth for workspace state.

Do **not** persist pane topology only in the app.

Do **not** duplicate split-tree mutation logic in both TypeScript and Go.

Instead:

1. frontend sends workspace intents
2. daemon applies workspace mutations
3. daemon persists workspace state
4. daemon emits the full workspace snapshot back to clients

This is the same control-plane pattern already used for durable session/runtime state elsewhere in the app.

## Core design

Separate these concepts explicitly:

### 1. Workspace Session

The user-facing top-level session shown in the sidebar.

Responsibilities:

1. label
2. directory
3. branch/worktree metadata
4. visible session state
5. split workspace metadata

### 2. Runtime Session

Any recoverable PTY-backed process.

Examples:

1. the main Claude/Codex/Copilot session runtime
2. an attached shell terminal pane runtime

Responsibilities:

1. PTY identity
2. agent/shell kind
3. PTY lifecycle
4. worker recovery
5. resume/recoverability

### 3. Workspace Layout

A split tree that references pane IDs, not PTY IDs directly.

Responsibilities:

1. split structure
2. active pane
3. pane titles
4. pane-to-runtime mapping
5. future pane sizes/ratios

## Recommended module boundaries

### `internal/workspace`

Pure layout-domain module.

Owns:

1. tree types
2. validation
3. normalization
4. split/close/focus operations
5. future resize ratio operations

This module should not know about PTYs, workers, or the frontend.

### `internal/workspaceruntime` or equivalent daemon service

Bridges workspace actions to PTY/runtime creation and teardown.

Owns:

1. spawning attached shell runtimes
2. removing attached pane runtimes
3. reconciling recovered runtimes with persisted pane metadata
4. translating runtime failures into workspace updates

### Store

Owns durable records for:

1. workspaces
2. panes
3. pane layout
4. attached pane runtime mapping

### Frontend

Becomes a renderer/controller only.

Owns:

1. local focus polish
2. transient selection visuals
3. optimistic loading states if desired

Does not own:

1. canonical split tree
2. pane membership
3. attached pane persistence

## Data model

## Option A: minimal refactor

Extend current persisted session rows with role/visibility and reuse them for attached panes.

Pros:

1. maximum reuse of current PTY recovery path
2. fewer new persistence primitives

Cons:

1. the current `Session` model is already overloaded
2. attached shell panes are not user-visible sessions
3. store/protocol/UI would need more filtering exceptions
4. current agent normalization paths are not designed for `shell`

## Option B: recommended

Introduce explicit workspace persistence separate from top-level session persistence, while reusing PTY recovery concepts for the runtime layer.

Pros:

1. clear boundary between user-visible session and runtime process
2. no sidebar/filtering hacks for hidden shell panes
3. cleaner long-term support for pane types beyond shell
4. better fit for daemon-owned workspace authority

Cons:

1. requires more upfront refactor

Recommendation: **Option B**

### Proposed tables

1. `session_workspaces`
- `session_id` primary key
- `active_pane_id`
- `layout_json`
- `updated_at`

2. `workspace_panes`
- `pane_id` primary key
- `session_id`
- `runtime_id`
- `kind` (`main`, `shell`)
- `title`
- `created_at`
- `updated_at`

3. `workspace_runtimes` or equivalent
- durable runtime record for attached panes
- references PTY/runtime identity and recovery metadata

If introducing a separate runtime table is too much for the first pass, an acceptable intermediate design is:

1. keep top-level session persistence as-is
2. add `attached_pane_runtimes`
3. centralize daemon recovery over both top-level sessions and attached pane runtimes

## Layout JSON shape

Persist pane IDs, not PTY IDs.

Recommended shape:

```json
{
  "type": "split",
  "split_id": "split-root",
  "direction": "vertical",
  "ratio": 0.5,
  "children": [
    { "type": "pane", "pane_id": "main" },
    {
      "type": "split",
      "split_id": "split-right",
      "direction": "horizontal",
      "ratio": 0.5,
      "children": [
        { "type": "pane", "pane_id": "pane-b" },
        { "type": "pane", "pane_id": "pane-c" }
      ]
    }
  ]
}
```

Notes:

1. include `ratio` now even if the UI does not yet persist custom sizes
2. use stable `split_id` values for future resize operations
3. use `pane_id = "main"` for the main agent pane of the workspace session

## Protocol design

Daemon should own all workspace mutations.

Frontend commands should be intent-based:

1. `workspace_get`
2. `workspace_split_pane`
3. `workspace_close_pane`
4. `workspace_focus_pane`
5. `workspace_rename_pane`
6. `workspace_resize_split` (future-ready; can be stubbed or deferred)

Daemon events should include:

1. `workspace_snapshot`
2. `workspace_updated`
3. `workspace_runtime_exited`

The frontend should not mutate the canonical tree locally. It should only render snapshots and send commands.

## Protocol generation workflow

Because this is protocol work, the implementation must:

1. update [`internal/protocol/schema/main.tsp`](/Users/victor.arias/projects/victor/attn/internal/protocol/schema/main.tsp)
2. run `make generate-types`
3. add new constants and parser cases
4. increment `ProtocolVersion`
5. reinstall/restart daemon after the protocol change

## Recovery behavior

The daemon recovery path should become:

1. recover top-level session runtimes
2. recover attached pane runtimes
3. load persisted workspace metadata
4. reconcile workspace panes against recovered runtimes
5. normalize layout if a pane runtime is missing
6. emit a workspace snapshot to the app

### Reconciliation rules

1. if the main pane runtime is missing, the workspace session behaves like the session itself today
2. if an attached pane runtime is missing, remove that pane from the tree and normalize the layout
3. if all attached panes are missing, collapse to the main pane only
4. if a recovered attached runtime exists but its pane metadata is missing, quarantine or prune it using the same conservative policy already used for worker mismatch cleanup

## Pane lifecycle rules

### Create pane

1. daemon receives split intent with `target_pane_id` and `direction`
2. daemon creates a new attached shell runtime with a stable runtime ID
3. daemon creates a new pane record
4. daemon applies split operation to layout tree
5. daemon persists everything atomically enough to avoid partial UI states
6. daemon emits updated workspace snapshot

### Close pane

1. daemon receives close intent
2. if pane is attached shell:
- kill/remove runtime
- remove pane from layout
- normalize layout
3. if pane is main:
- define explicit product rule

Recommended product rule:

1. closing the main pane does **not** kill the whole workspace session through the generic pane-close action
2. main-pane close is rejected or treated as session close only via the top-level session close action

This keeps `Cmd+W` from ambiguously destroying the top-level session.

### Runtime exits

1. if attached shell runtime exits naturally, daemon removes pane automatically
2. daemon updates layout tree and broadcasts workspace update
3. frontend does not decide this locally

## Frontend refactor

The current workspace logic in [`SessionTerminalWorkspace`](/Users/victor.arias/projects/victor/attn/app/src/components/SessionTerminalWorkspace/index.tsx) should be demoted from state owner to renderer.

After refactor it should:

1. render daemon-provided tree
2. render main pane + attached panes by pane/runtime ID
3. send split/focus/close intents
4. react to daemon updates

Things that should move out of frontend-local state:

1. `layoutTree`
2. `activePaneId`
3. pane title durability
4. attached pane membership

## Existing infra to leverage

These are the pieces we should reuse aggressively:

1. PTY worker recovery infrastructure in the daemon
2. daemon-owned command/result WebSocket patterns
3. SQLite-backed store migrations
4. protocol generation via TypeSpec
5. session reconciliation patterns already used at startup

Do not duplicate:

1. PTY runtime ownership in the frontend
2. split-tree mutation logic across TS and Go
3. separate ad hoc recovery logic for attached panes if the runtime abstraction can unify it

## Refactor strategy

Implement in phases.

### Phase 1: workspace domain

1. add `internal/workspace` with pure tree ops and tests
2. define persisted workspace schema
3. add store methods for get/save workspace

### Phase 2: daemon-owned workspace commands

1. add protocol commands/events
2. daemon loads and emits workspace snapshots
3. frontend reads workspace from daemon but attached panes can still be recreated fresh if needed

This phase proves daemon authority before full runtime recovery.

### Phase 3: attached pane runtime persistence

1. persist attached pane runtime metadata
2. daemon recovers attached pane runtimes on startup
3. reconcile runtimes against persisted workspaces

### Phase 4: frontend cleanup

1. remove frontend-local canonical workspace state
2. keep only transient UI state in app store
3. remove obsolete bottom-panel and legacy utility-terminal assumptions fully

## Migration concerns

1. old sessions without workspace metadata should default to a single main pane
2. existing attached pane UI state in local storage or frontend memory can be discarded
3. layout JSON must be validated defensively
4. invalid pane references should be normalized, not crash startup

## Testing plan

### Store tests

1. workspace save/load roundtrip
2. invalid layout normalization
3. pane removal collapsing parent split correctly

### Daemon tests

1. split pane command persists workspace
2. close pane command kills runtime and persists normalized layout
3. restart recovery restores attached panes
4. missing attached runtime prunes pane cleanly
5. main workspace session still appears once in top-level session list

### Frontend tests

1. app renders daemon-owned workspace snapshots
2. pane focus updates on `workspace_updated`
3. reconnect path hydrates existing workspace without requiring a new split

### E2E tests

1. split a session into nested panes, close app, reopen, verify layout restored
2. type in attached shell, close app, reopen, verify attached shell runtime still present
3. let attached shell exit naturally, verify pane disappears
4. switch sessions and back, verify focused pane is restored

## Acceptance criteria

1. a nested split workspace survives app close/reopen
2. a nested split workspace survives daemon restart
3. attached shell panes recover using the same durable PTY runtime approach as main sessions
4. top-level session list remains one row per user session, not one row per pane
5. pane mutations are daemon-owned and not duplicated in frontend business logic

## Open questions to lock before implementation

1. Should main-pane close ever be allowed from the generic pane-close action?
- Recommendation: no

2. Should attached panes always be shell panes in v1?
- Recommendation: yes

3. Should pane size ratios be persisted in v1?
- Recommendation: yes, even if manual resizing lands slightly later

4. Should workspace snapshots be sent in `initial_state` or via a separate bootstrapping event?
- Recommendation: include them in initial boot payload or send them immediately after initial state in the same connection phase, but keep the daemon as the only authority

## Recommended implementation order for another agent

1. introduce `internal/workspace` tree types and pure operations
2. add workspace persistence tables and store methods
3. add daemon-side workspace service and protocol
4. refactor frontend workspace state to consume daemon snapshots
5. add attached pane runtime persistence and recovery
6. delete remaining frontend-only pane ownership code

## Decision

Proceed with the full version using daemon-owned workspace state and persistent attached pane runtimes.

Do it as an architectural refactor, not as a frontend-only persistence patch.
