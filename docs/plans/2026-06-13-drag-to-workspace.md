# Plan: Drag-driven workspace management (reorder + new-workspace)

## Goal
Two small PRs:
- **PR1 — Reorder.** Drag a workspace header in the sidebar to reorder it. Order is
  backed by a per-workspace **rank key**; default order = opening (creation) order.
- **PR2 — New workspace from drag.** Drag a session/leaf into a "New workspace"
  drop-zone to split it out into a brand-new workspace.

Chosen UI is prototype **Variant A** (`tmp/drag-new-workspace-proto/variant-a.html`):
dedicated bottom drop-zone for new-workspace; seam-based reorder; live rank badges.

Why rank keys (not an integer `position`): a reorder is a **single-row write** (only
the moved workspace's key changes), which is naturally safe across the daemon's
websocket fan-out to multiple clients. See [[drag-to-workspace-mechanism]].

## Architecture Map

```text
PR1 reorder — runtime:
  Sidebar workspace-header pointerdown (Sidebar.tsx:552)
    -> App leafWorkspaceDrag-style state + insertion seams between groups
      -> drop on seam between prevWs / nextWs (frontend sends IDS, not a computed key)
        -> sendSetWorkspaceRank(wsId, prevWsId?, nextWsId?)   [useDaemonSocket.ts ~2672 pattern]
          -> ws cmd set_workspace_rank
            -> daemon handleSetWorkspaceRank              [workspacelayout.go, near :686]
              -> rank := rankkey.Between(rank(prevWs)|MIN, rank(nextWs)|MAX)  [Go helper]
              -> store.UpdateWorkspaceRank(id, rank)      [workspace.go, after UpdateWorkspaceTitle]
              -> workspaces.applyRank(id, rank)           [workspace.go, mirror applyStatus]
              -> broadcast workspace_state_changed (snapshot carries rank)
                -> frontend sorts by rank -> visualOrder + visualIndexByWorkspaceId recomputed

PR2 new-workspace — runtime (mirrors move_leaf_to_workspace):
  active leafDrag over bottom "New workspace" zone (Sidebar.tsx)
    -> handleWorkspaceDragDrop detects zone (App.tsx ~2665)
      -> sendMoveLeafToNewWorkspace(sourceWsId, leafId, edge?, ratio?)
        -> daemon handleWorkspaceLayoutMoveLeafToNewWorkspace
          -> register new ws (uuid, rank = rankAfter(maxRank), default title)
          -> reuse moveLeafToWorkspace(source -> newWs)   [workspacelayout.go:686-812]
          -> broadcast workspace_registered BEFORE layout_updated; then session_state_changed
          -> unregisterWorkspaceIfEmptyAfterMove(source)

Order derivation (today vs target):
  Current: store ListWorkspaces ORDER BY created_at
           -> buildWorkspaceViewModels iterates in input order (workspaceViewModels.ts:85)
  Target:  store ListWorkspaces ORDER BY rank, created_at
           -> snapshot.rank flows to frontend -> same view-model order (already sorted)

Tests:
  store_test (real SQLite, NewWithDB): rank persisted + ordered
  daemon workspace_rank_protocol_test.go: set_rank reorders, survives reconnect
  daemon workspace_moveleaf_protocol_test.go: move-to-new creates ws + transfers ownership
  frontend: rankBetween/rankAfter unit, workspaceViewModels ordering, Sidebar drag (createMockDaemon)
```

## Data Model / Interfaces

```ts
// Workspace snapshot — add one field (main.tsp:158, owned by store, read by frontend)
model Workspace { id; title; directory; status; muted; rank: string; layout? }

// PR1 command — frontend sends the drop position as neighbour IDS; daemon computes the key
{ cmd: "set_workspace_rank"; workspace_id: string; prev_workspace_id?: string; next_workspace_id?: string }
//   prev_workspace_id = the workspace that should end up ABOVE the moved one (empty = move to top)
//   next_workspace_id = the workspace that should end up BELOW it          (empty = move to bottom)

// PR2 command (mirrors workspace_layout_move_leaf_to_workspace, no target)
{ cmd: "workspace_layout_move_leaf_to_new_workspace";
  source_workspace_id: string; leaf_id: string; anchor_id?: string; edge?: DockEdge; ratio?: float64 }

// Rank key = lexicographic fractional-index string (NOT integer position).
// ALL rank math lives in ONE Go helper package (internal/.../rankkey), unit-tested:
rankkey.Between(a, b string) string   // strictly a < result < b by byte order (a="" => MIN, b="" => MAX)
rankkey.Seed(n int) []string          // n evenly-spaced opening-order keys (migration backfill + AddWorkspace)
rankkey.After(max string) string      // result > max (new-workspace append)
// Frontend never generates rank strings — it only reads snapshot.rank to sort + show the badge.
```

Ownership: **store** persists rank and owns the canonical order (seed on create, migration
backfill via `rankkey.Seed`). **daemon** owns ALL rank computation (`rankkey.Between/After`)
— seeds new workspaces and computes the key for a reorder from neighbour ids. **frontend**
never generates rank strings; it sends neighbour ids and *reads* order from the sorted snapshot.

## Boundaries
- Daemon `set_workspace_rank` computes the new key from neighbour ids and writes ONE row.
  The "insert between A and B" math is `rankkey.Between` (Go), not duplicated in TS.
- Reorder operates only on the **unmuted, same-endpoint, visible** set. Muted workspaces
  keep a separate order space; never reorder across endpoints (reuse the `canAcceptLeafDrag`
  endpoint check). Respect `showSessionless` when rendering seams.
- A workspace drag's **body hover = no-op** (snap to nearest seam). Workspaces never merge.
- Generated files (`internal/protocol/generated.go`, `app/src/types/generated.ts`) are
  do-not-hand-edit — change `main.tsp` and run `make generate-types`.

## Implementation Steps

### PR1 — Reorder (protocol 102 → 103)
- [ ] **rank helper**: new Go pkg (e.g. `internal/rankkey`) with `Between(a,b)`, `Seed(n)`, `After(max)` + thorough unit tests (byte-order strictness, no-room subdivision, MIN/MAX edges).
- [ ] store: migration **49** `ALTER TABLE workspaces ADD COLUMN rank TEXT NOT NULL DEFAULT ''`; `applyMigration49` backfills existing rows via `rankkey.Seed(n)` in `created_at` order (idempotent: only where rank=''). (real DBs at v48 — 49 is clean)
- [ ] store: `AddWorkspace` persists rank; `ListWorkspaces`/`GetWorkspace` read rank; `ORDER BY rank, created_at`; add `UpdateWorkspaceRank(id, rank)`.
- [ ] daemon: `workspaceEntry.rank`; `snapshotEntry` includes rank; `applyRank` (mirror `applyStatus`); `register` reads stored rank on re-register (don't reset); seed rank on first creation via `rankkey.After(currentMax)`.
- [ ] daemon: `handleSetWorkspaceRank` (resolve prev/next ranks → `rankkey.Between` → `store.UpdateWorkspaceRank` → `applyRank` → broadcast `workspace_state_changed` → action result). Wire `websocket.go` dispatch + `getWorkspaceIDFromMessage` + `command_meta.go`.
- [ ] protocol: add `rank: string` to `Workspace`; `SetWorkspaceRankMessage{workspace_id, prev_workspace_id?, next_workspace_id?}`; `CmdSetWorkspaceRank` + ParseMessage case; bump `ProtocolVersion` → `103`; `make generate-types`.
- [ ] frontend: sort by rank feeding `buildWorkspaceViewModels`; `sendSetWorkspaceRank(wsId, prevWsId?, nextWsId?)` (thread App→AppContent 4 places).
- [ ] frontend: Sidebar workspace-header pointer-drag + insertion seams + live rank badge + distinct workspace-card ghost; drop sends the seam's neighbour ids; recompute BOTH `visualOrder` and `visualIndexByWorkspaceId`.
- [ ] tests: `rankkey` unit tests; store `TestSetWorkspaceRank`, `TestListWorkspacesOrderedByRank`; daemon `workspace_rank_protocol_test.go` (neighbour-id reorder → order changes, survives reconnect); frontend `workspaceViewModels` ordering + `Sidebar` reorder + `useDaemonSocket` sender/result.
- [ ] `make install` (protocol bump → reconnect); rebuild `./attn` for e2e; CHANGELOG entry.

### PR2 — New workspace from drag (protocol 103 → 104)
- [ ] protocol: `MoveLeafToNewWorkspaceMessage`; const + ParseMessage; bump → `104`; `make generate-types`.
- [ ] daemon: `handleWorkspaceLayoutMoveLeafToNewWorkspace` (uuid + register new ws with `rankAfter(max)` + default title → reuse `moveLeafToWorkspace`); broadcast `workspace_registered` before `layout_updated`; action result `entityId = leafId`; empty-source teardown; routing maps.
- [ ] frontend: `sendMoveLeafToNewWorkspace` (thread 4 places); Sidebar "New workspace" drop-zone shown at foot of list during active `leafDrag` (group bodies stay merge targets); `handleWorkspaceDragDrop` detects zone → new-workspace sender.
- [ ] tests: daemon `TestMoveLeafToNewWorkspaceCreatesWorkspace` (no pre-created target; verify new ws + moved pane + session ownership + empty-source teardown); Sidebar drop-zone; `useDaemonSocket` sender/result.
- [ ] `make install`; rebuild `./attn`; CHANGELOG entry.

## Decisions
- **All rank math lives in Go; frontend sends neighbour ids.** The daemon computes the key
  via `rankkey.Between` from the seam's prev/next workspace ids and writes ONE row. (Rejected:
  frontend computes the midpoint — would force a second fractional-index implementation in TS
  to keep in sync. Single-row-write is preserved either way; one impl wins.)
- **Fractional rank string, not integer `position`.** O(1) write per move + multi-client
  friendly. Integer positions would rewrite N rows per reorder.
- **Two PRs, each bumps ProtocolVersion** (103 then 104). Reorder is self-contained and
  ships first; new-workspace builds on the same drag wiring.
- **Workspace-drag body = no-op (snap to seam); workspaces never merge** — chosen by Victor
  to keep reorder and session-merge as distinct gestures by drag source.

## Gotchas / Verification
- Protocol bump is **breaking**: the daemon survives app rebuilds, so a stale daemon + new
  app must reconnect-fail — run `make install` after each bump. Rebuild `./attn` or terminal
  e2e fails at session creation.
- Broadcast order: `workspace_layout_updated` **before** `session_state_changed` (frontend
  filters sessions by layout). New-workspace: `workspace_registered` before `layout_updated`.
- Workspace `status` is runtime-only — never persist it; it rolls up from member sessions.

## Follow-ups
- Real fractional-index rebalancing if keys ever grow long (irrelevant at attn's scale).
- Keyboard reorder (move workspace up/down) reusing the same `set_workspace_rank` path.
- Prune throwaway prototype variants B/C once the build lands.
