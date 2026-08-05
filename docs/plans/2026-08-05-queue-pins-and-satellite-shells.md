# Plan: queue pins and satellite shells

## Why / Alignment

Queue mode works, but the workspace model and shells have friction with it, and
this closes the gap. Two changes, one PR, both invisible while queue mode is off:

1. **Pin the agent, not just the workspace.** Today the only way to take one
   agent out of the queue is `pin_workspace`, which drags every sibling along.
   A per-session pin joins the exclusion list with the exact mechanics of the
   workspace pin: filter at read, turn stamps keep accruing, unpinning surfaces
   the outstanding turn at its true age.
2. **Satellite shells.** A shell's attachment to the agent it was opened from is
   pure pane geometry today — no stored link. It becomes a persisted
   `parent_session_id`, resolved daemon-side at spawn, always pointing at an
   *agent* (a shell split from a shell inherits that shell's agent). A shell
   with a live parent in its workspace has no sidebar row in queue mode; you
   reach it through its agent's panes. A shell without one (agent closed,
   pre-migration, ⌘N into another workspace) keeps its *Settled* row, so every
   session stays reachable — through its parent or through its own row.

**Aligned on** (Victor, 2026-08-05): flat **Pinned** band of one-row sessions,
below *Settled* and above the pinned-workspace tree; satellites invisible while
attached (no sub-rows); single PR.

Standing order with the mode on becomes:

```text
chief (anchored)
── Your turn ──          flat, oldest-owed first            (unchanged)
── Settled ──            flat; satellites no longer here
── Pinned ──             flat, individually pinned sessions, pin-time order  [new]
── pinned workspaces ──  grouped tree                       (unchanged)
── Snoozed ── / ── Muted ──                                 (unchanged)
```

## Data model

```sql
-- migration 92 (receipt 2026-08-05: MAX(version) across every local profile DB
-- is 91 — posw; production is 90; code head is 91). One migration, two ALTERs,
-- each guarded on columnExists.
ALTER TABLE sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN pinned_at         TEXT NOT NULL DEFAULT '';
```

- `parent_session_id` — spawn-time provenance, written once, never cascaded.
  Always an agent session's id, never a shell's, so there are no chains to walk.
  Empty = workspace-scoped shell (every pre-migration shell; no backfill — the
  association is unknowable retroactively).
- `pinned_at` — one column doing two jobs: non-empty means pinned, and the
  timestamp is the Pinned band's sort key (append at the bottom, stable;
  re-pinning lands at the bottom again, which is right). No separate bool.

**Attachment validity is evaluated at read, not maintained by writes.** A shell
is *attached* iff its `parent_session_id` matches a session in the same
workspace. Nothing clears the column: move the shell's pane to another
workspace and the check fails → its row resurfaces; move it back → it
re-attaches; close the agent → orphan row appears. No write cascades in
`move_leaf_to_workspace`, session close, or re-home — the rule self-heals.

## API changes

### Protocol (`internal/protocol/schema/main.tsp`)

`Session` gains two optional fields (present only when set — both `Ptr`-typed in
Go, like `turn_snoozed_until`):

```tsp
model Session {
  // ...
  pinned_at?: string;          // ISO instant this session was pinned out of the
                               // queue. Presence = pinned; the Pinned band's
                               // sort key. Filter-at-read like workspace pin:
                               // turn stamps accrue underneath.
  parent_session_id?: string;  // Satellite link: the agent session this shell
                               // was split from. Spawn-time provenance, always
                               // an agent id. Attachment is judged at read
                               // (parent live in the same workspace), so the
                               // field itself never needs clearing.
}
```

`SpawnSessionMessage` gains the base of a split:

```tsp
  spawned_from?: string;       // Session the user split this one from. Only
                               // consulted for agent "shell": the daemon
                               // resolves it to the owning agent.
```

One new command (fire-and-forget + broadcast, the `pin_workspace` pattern — no
`*_result` event):

```tsp
model PinSessionMessage {
  cmd: "pin_session";
  session_id: string;
  pinned: boolean;
}
```

Plus: `CmdPinSession` constant + decode case in `constants.go`, and a
**`ProtocolVersion` bump in all three lockstep spots** — `constants.go`,
`app/src/hooks/useDaemonSocket.ts`. Regenerate with `make generate-types`
(delete `tsp-output` first; never hand-edit `generated.go`/`generated.ts`).

### Daemon

```text
spawn_session (spawn_pipeline.go, around isShell := agent == AgentShellValue :105)
  -> resolveSpawnParent(msg.SpawnedFrom):                              [new]
       base := store.Get(spawnedFrom); base == nil        -> ""
       base.Agent != "shell"                              -> base.ID
       base.Agent == "shell"                              -> Deref(base.ParentSessionID)
       resolved parent's WorkspaceID != msg.WorkspaceID   -> ""   (⌘N-elsewhere guard)
  -> buildSpawnSessionRecord (ws_pty.go:222) stamps ParentSessionID,
     carrying it from `existing` on respawn/revive like EndpointID already is

ws pin_session
  -> store.SetSessionPinned(id, pinned, now)     // writes/clears pinned_at
  -> reject when session.ChiefOfStaff            // the chief is already anchored
  -> d.publishFact(FactSessionPinChanged, id, nil)   // subject-only: store-backed

wireProjections (bus.go)
  FactSessionPinChanged = "session.pin.changed"  -> session re-push, same group
  as the other session-shaped facts; TestWireTrafficComesFromProjections covers it

decorateSessionWithTurn (turn.go:59)
  in.SessionPinned = session has pinned_at       [new Input field]
  (decoration also stamps PinnedAt + ParentSessionID onto the broadcast clone
   if they don't ride store.Get directly — see store notes)
```

`internal/attention/turn.go`:

```go
type Input struct {
    // ...
    // SessionPinned excludes an individually pinned session, filtering at read
    // exactly like WorkspacePinned: the stamps accrue, unpinning surfaces the
    // outstanding turn at its true age.
    SessionPinned bool
}
func Owed(in Input) bool {
    if in.IsShell || in.ChiefOfStaff || in.SessionPinned ||
        in.WorkspacePinned || in.WorkspaceMuted { return false }
    return in.OpenedAt.After(in.SettledAt)
}
```

### Store (`internal/store`)

- `SetSessionPinned(id string, pinned bool, now time.Time) error` — SQLite
  `UPDATE sessions SET pinned_at = ...` + the in-memory map branch (turn.go's
  dual-branch pattern, not `SetWorkspacePinned`'s silent-nil one).
- Both new columns join **every** session SELECT/scan (`Get`, the list scans)
  and `cloneSession` (two new pointer fields — miss one and you alias).
- `AddChecked`: `parent_session_id` joins the INSERT + ON CONFLICT list (the
  record carries it; respawn carries it via `existing`). `pinned_at` stays
  **out** of the upsert entirely — the dedicated setter owns it, and a column
  absent from ON CONFLICT survives respawns untouched. The in-memory branch
  replaces the whole record, so `buildSpawnSessionRecord` carrying both fields
  from `existing` is load-bearing there, not a nicety (see pitfalls).

### Hub (`internal/hub/manager.go`)

`sessionsMatch` (:1352) gains `Deref(PinnedAt)` and `Deref(ParentSessionID)`.
Without them a remote pin changes nothing the hub compares, the re-broadcast is
suppressed, and the row never leaves the queue — the exact trap the turn and
snooze fields each hit in their own PRs.

### Frontend (`app/src`)

```text
useDaemonSocket.ts   sendPinSession(sessionId, pinned); PtySpawnArgs.spawned_from;
                     sendSpawnSession forwards it; ProtocolVersion bump
daemonSessions /     carry pinnedAt + parentSessionId into the enriched session
enrichment           model EXPLICITLY — fields do not survive on their own
                     (state_since never made it; the turn plan hit this)
App.tsx              createSplitSession (:1931) sets spawned_from: activeSession.id
                     (the daemon resolves shell-of-shell; the app sends the base);
                     ⌘K pin/unpin-active-agent entries beside pin-active-workspace
queueBands.ts        buildQueueBands routing, after the pinned/muted-workspace skip:
                       session.pinnedAt          -> pinned band (pinnedAt asc)
                       shell + live same-workspace parent -> no row at all
                       otherwise                 -> settled (as today)
                     bands type gains pinned: QueueRow[]
QueueBands.tsx       Pinned band rendered below Settled; rows show live state +
                     "(workspace)" suffix like the turns band; unpin affordance.
                     The row-level onPin becomes PIN AGENT (it silently pinned
                     the whole workspace before — :264/:289); workspace pin
                     stays on group headers and ⌘K
advanceAfterTurnClosed  arrival in `pinned` is NOT a close (same rule as
                     workspace pin: advance requires arrival in settled/snoozed)
                     — new test, since pinned is a new destination
```

## Boundaries

- `internal/attention` gains one Input field and nothing else; stays pure.
- The daemon owns parent resolution and pin persistence; it never re-implements
  `Owed` inline, and it never cascades attachment updates.
- The app owns *arrangement*: the settled/pinned routing and the
  attachment-at-read check are band-building, which was always client-side.
  Membership (`turn_owed`) remains the daemon's answer alone.
- Queue mode off: zero behavior change. Tree renders every session as today
  (satellites included); `wantsAttention` keeps the state predicate, so pinned
  agents keep their badges in the arrangement where pinning means kept in view;
  both new fields are dormant metadata.

## Pitfalls

- **Migration numbering is spoken for through 91.** Receipt above; take 92. If
  main moves before merge, re-check — docstore burned 86–91 in two weeks.
- **The three-spot ProtocolVersion lockstep**, and rebases merge the constant
  *down* — re-grep after every rebase onto main.
- **`make generate-types` traps:** stale `tsp-output/` orphans, quicktype
  collapsing value-identical enums, comment-only tsp edits producing zero-byte
  diffs. Confirm regen by mtime and run the frontend typecheck after.
- **Store divergence between branches.** SQLite preserves `pinned_at` on
  respawn because it's absent from the upsert; the in-memory branch replaces
  whole records. If `buildSpawnSessionRecord` doesn't carry both fields from
  `existing`, memory-store tests will pass where production preserves (or vice
  versa). Enumerate `AddChecked` callers while implementing; any that build a
  fresh `protocol.Session` for an existing id can silently clear the new
  fields in the memory branch.
- **`cloneSession` must copy the two new pointers** or store reads alias the
  cached record.
- **Hub `sessionsMatch`** — see above; this is the third time this trap fires
  if missed.
- **Fields must be carried into the app's enriched model explicitly.** The
  frontend drops wire fields it doesn't map; `state_since` never existed
  client-side. Both new fields need deliberate plumbing before the band builder
  sees them.
- **No backfill means no visible change for existing shells.** Every shell
  alive before the migration is an orphan and keeps its Settled row. Only
  newly split shells disappear. Name this in the changelog so live
  verification doesn't read it as a bug.
- **Pin interactions that must stay dormant, not conflict:** a pinned session
  inside a pinned or muted workspace never reaches the bands (the existing
  workspace skip runs first — mute stays absolute, workspace pin keeps its
  group); pinning the chief is refused; snoozing a pinned agent is harmless
  (wakes into a still-pinned, still-excluded session). A *pinned orphan shell*
  is legitimate and lands in the Pinned band — the all-day scratch terminal.
- **Attention surfaces go quiet for pinned agents in queue mode** (⌘J, rail
  dot, tile glow, drawer all follow `turn_owed`). Already true for workspace
  pin, deliberate per the vision — the row's state color is the remaining
  signal. Chosen, not stumbled into.
- **Handover on pin must leave you where you are.** `advanceAfterTurnClosed`
  already requires arrival in settled/snoozed, but pinned is a new destination
  — cover it, don't assume it.
- **Harness/e2e mechanics:** the packaged-app harness checks the bundle's baked
  fingerprint — full `make install PROFILE=<throwaway>` after any commit before
  evidence runs; never parallel scenarios; rebuild `./attn` for frontend e2e
  after the ProtocolVersion bump; `attn profile clean` when done.
- **`spawn_session` callers that must not regress:** automations
  (`automations_deliver.go:623`) and delegation spawn agents with no
  `spawned_from` — resolution must be a no-op there; the UI-automation bridge
  `splitPane` already passes `baseSessionId` and should ride the same field.

## Implementation steps

- [x] Migration 92 (both columns, `columnExists`-guarded).
- [x] Store: SELECT/scan + `cloneSession` + `AddChecked` changes;
      `SetSessionPinned` (both branches); tests — pin/unpin/re-pin ordering,
      respawn preserves both fields in both branches.
- [x] Protocol: fields, `spawned_from`, `PinSessionMessage`; regenerate; bump
      (three spots); frontend typecheck.
- [x] Daemon: `resolveSpawnParent` + record stamping; `pin_session` handler +
      chief refusal; `FactSessionPinChanged` + projection;
      `attention.Input.SessionPinned`; hub `sessionsMatch`. Tests: resolution
      table (agent base / shell base / cross-workspace / missing / absent),
      pinned excluded from `turn_owed`, unpin resurfaces at true age, remote
      pin re-broadcasts.
- [x] App: senders, enrichment plumbing, `createSplitSession`, band routing +
      unit tests (pin-time order; attached shell dropped; orphan kept; pinned
      workspace skip precedence), `QueueBands` Pinned band + row/⌘K
      affordances (pin agent vs pin workspace labeled apart), unpin path,
      `advanceAfterTurnClosed` pinned-arrival test, queue-off invariant test
      (tree unchanged with both new fields set).
- [x] `queue_get_state` bridge gains the Pinned band; extend
      `real-app:scenario-agent-queue`: split a shell from an agent → no Settled
      row and no tree row; split from that shell → still no row; pin an agent
      from its queue row → lands in Pinned with its workspace untouched; unpin
      from that band → back in the turns band on the same `turn_opened_at`.
- [x] Docs: glossary section (*turn/queue*, *pinned agent*, *satellite*), vision
      amendment (standing order, pin aimed at the row, satellites), two
      `changelog.d/` fragments.
- [ ] Live verification on a throwaway profile (full install), then
      `attn profile clean`.

## Decisions

- **Attachment is judged at read, never maintained by writes.** The stored id
  is spawn-time provenance; liveness + same-workspace is checked where bands
  are built. Rejected: clearing `parent_session_id` in every pane-move/re-home/
  close path — a cascade with N call sites, each a place to forget, for a rule
  one predicate expresses self-healingly.
- **`pinned_at` instead of a bool.** The band needs a stable order; pin time is
  the honest one and the presence check is free. A bool would need a second
  column the moment the band holds three rows.
- **The parent is always the agent, resolved at spawn.** Storing the immediate
  base would create chains that must be walked (and broken links mid-chain);
  Victor's rule — a shell of a shell inherits the agent — is exactly "resolve
  once, store flat".
- **Daemon resolves `spawned_from`; the app just names its base.** The CLI and
  the automation bridge get identical semantics without duplicating the
  shell-of-shell rule client-side.
- **Session pin filters at read like workspace pin, not suppress-at-open like
  snooze.** Pin means *kept, out of the queue* — unpinning must surface what
  accumulated at its true age. Snooze stays the only verb that forgives age.
- **Pinned band sorts by pin time, not hand-order.** Stable and predictable;
  hand-ordering (rank keys) is a follow-up if living with it demands it.
- **The chief refusal asks the role registry, not the session record.**
  Implementation found `chief_of_staff` is decorated onto a session at broadcast
  from `GetProfileRole`, never stored — so the obvious `Deref(session.ChiefOfStaff)`
  on a `store.Get` result is always nil and the refusal never fires.
  `d.isChiefOfStaffSession(id)` is the only honest question. Any future guard on
  a decorated field has the same trap.
- **`pin_session` is session-scoped for the hub.** `remoteCommandSessionID` must
  name it, or a hub answers the command locally and a remote agent's pin is
  written on the wrong daemon. `TestSessionScopedCommandsReachTheSessionOwner`
  caught this, which is what that guard exists for.
- **No backfill for shells that already exist.** They keep their own rows as
  orphans. A migration cannot recover which agent a shell was split from —
  pane geometry is the only trace and it has already moved for some — and
  guessing would hide a row the user can still reach.

## Follow-ups

- Indefinite backgrounding (agent-level mute) — same column pattern, mute
  semantics; completes the Deferral rock.
- Born-pinned agents in the ⌘T flow, now that a per-agent mechanism exists.
- Satellites follow their agent on re-home/drag (move the shell panes with the
  agent's), and close-time cleanup offers for satellites.
- Hand-ordering for the Pinned band (fractional rank), if pin-time order chafes.
- CLI surface for pins (`attn session pin`?) — none exists for `pin_workspace`
  either today; decide both together.
