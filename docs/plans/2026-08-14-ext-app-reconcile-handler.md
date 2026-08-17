# App reconcile handler

Designed 2026-08-14 and approved by Victor. This follows
[A4's app registry and runtime](2026-08-06-ext-a4-app-registry-and-runtime.md),
[A1's durable consumer contract](2026-08-01-ext-a1-event-bus.md), and
[A5's manifest and SDK shape](2026-08-13-ext-a5-ui-host-and-app-sdk.md).

## Problem

An app's collections are a materialized view over current state. A fact wakes
the app, but the collection is what survives. Three real paths can leave that
view stale:

1. A disabled app is re-enabled after retention passed its cursor. (Closed by
   the retention policy at the end of this doc: an installed app's lane is
   retained while disabled, so this path collapses into path 2's machinery
   and only broken states reach it.)
2. An enabled consumer resumes below the oldest surviving fact.
3. A different app version derives different collection contents from the
   same current state.

The bus currently repairs the first two by logging the gap and moving the
consumer directly to head (`internal/bus/bus.go`, `reconcileGap`). The app is
not called. A version move re-points the consumer and its declarations
(`internal/daemon/app_apply.go`, `internal/daemon/app_consumers.go`) but never
rebuilds existing documents. In both cases the runtime proceeds as though the
view were current.

Views are not affected. Every live-query delivery is a complete window; a
later window supersedes anything a view missed. This design is only for
headless app handlers and the collections they maintain.

## Gate answers

### 1. Trigger once from a durable request, not from a synthetic fact

Recommendation: each condition writes a durable reconcile request. The
consumer lane coalesces every request already pending for the app into one
invocation. Reasons are a set, so a version move while disabled followed by a
second version move invokes once with `version_changed` carrying both previous
versions. Re-enable is not a trigger: under the retention policy at the end of
this doc an installed app's facts wait while it is disabled, so re-enable is a
paused durable consumer resuming from its frozen cursor — A1's ordinary
contract, no rebuild. A version move that happened while disabled left its
pending request, and the resuming consumer runs it before delivering anything.

```text
version move / resume below retention
  persist app_reconcile_request(reason, version, through_seq, detail)
  wake the app's ordinary bus consumer
    coalesce pending requests
    run version's reconcile handler
    mark claimed requests complete
    advance cursor through through_seq
    deliver every retained fact above the fence in seq order
```

The two detectors are:

- **Resume with a gap.** `internal/bus.Bus` already reads `earliest`, `head`,
  and the consumer cursor before it advances. Durable registration gains a
  pre-drain hook. For an app consumer, the hook inserts a `gap` request carrying
  `{cursor, earliest, through_seq: earliest - 1, missed}` before anything
  moves — the fence covers exactly the lost range, and retained facts from
  `earliest` up are delivered after the run. A
  non-app consumer keeps A1's current log-and-resume behavior.
- **Version change.** A pointer move to a different version inserts
  `version_changed` in the same transaction as the pointer move, carrying the
  previous and target version ids. Initial installation has no previous
  version and does not reconcile. Re-applying the already-serving content
  moves no pointer and creates no request, matching
  `publishAppVersionChanged` in `internal/daemon/app_apply.go`. Rollback is a
  version change and does reconcile.

`through_seq` is the fence the rebuilt view supersedes, and it covers only
facts the app has already received or can never receive — never a retained,
undelivered fact. A `version_changed` request carries the consumer's cursor
read inside the pointer transaction: the rebuild supersedes what the old
version saw, and everything the app has not seen yet is delivered after the
run, in order, under the new version. A `gap` request carries `earliest - 1`:
the rebuild covers exactly the hole, and the retained facts from `earliest`
up are delivered after. A head fence was the original shape and was rejected
in review: it silently discarded retained facts a history-accumulating app
was owed — the very facts the retention policy below exists to keep.

`app.version.changed` and `app.enabled.changed` remain domain facts for other
consumers and the UI. They are not the orchestration mechanism: consuming an
app's own fact would put the trigger behind the cursor whose movement it must
control.

Requests that arrive before a run starts coalesce. A request that arrives
after a run has claimed its fence cannot join that run honestly; it remains
owed and causes one more reconcile before fact delivery resumes.

### 2. Declare the handler; pass reasons and a current-state snapshot

Recommendation: the manifest declares support explicitly:

```toml
reconcile = true
```

The bundle follows A5's structured exports: one map per handler kind, with
reconcile as its own sibling export. Kind is structure, never a prefix encoded
into a flat handler key:

```ts
export const subscriptions = {
  "ticket.*": onTicket,
} satisfies Subscriptions

export const commands = {
  refresh: onRefresh,
} satisfies Commands

export const reconcile: ReconcileHandler<AppCollections> =
  async (reason, ctx) => { /* converge collections */ }
```

Apply still never evaluates app code. The manifest tells the daemon that the
capability exists; codegen plus `tsc` proves the function is present. This
extends the same declaration-to-generated-group shape A5 uses for
subscriptions and commands. The sidecar selects `subscriptions` for a fact,
`commands` for a command envelope, and `reconcile` for this lifecycle call;
collisions between kinds are inexpressible.

The SDK contract is:

```ts
type ReconcileCause = "gap" | "version_changed"

type ReconcileReason = {
  causes: readonly ReconcileCause[]
  version: number
  throughSeq: number
  gap?: {
    cursor: number
    earliest: number
    missed: number
  }
  previousVersions: readonly number[]
}

type ReconcileHandler<Collections> = (
  reason: ReconcileReason,
  ctx: AppContext<Collections>,
) => void | Promise<void>
```

Causes are sorted in the order above, not in arrival order. `version` is the
version whose handler is running. `previousVersions` contains the distinct
versions named by coalesced pointer moves, in request order. A retry receives
the same claimed reason. A later trigger produces a later invocation.

The current A4 context exposes only the app's declared document collections
(`apphost/src/dispatch.ts`, `internal/daemon/app_runtime_rpc.go`). That can
store a projection but cannot rebuild one from attn's current state. Add one
read-only surface to `AppContext`, available to ordinary event handlers too:

```ts
type AppContext<Collections> = {
  app: string
  version: number
  collections: Collections
  current: {
    snapshot(): Promise<CurrentStateSnapshot>
  }
}
```

`CurrentStateSnapshot` reuses the daemon's existing initial-state projection
instead of inventing a second meaning of "current". Its exact fields are:

```ts
type CurrentStateSnapshot = {
  asOfSeq: number
  sessions: Session[]
  endpoints: EndpointInfo[]
  workspaces: Workspace[]
  prs: PR[]
  repos: RepoState[]
  authors: AuthorState[]
  githubHosts: string[]
  tickets: TicketRow[]
  seeds: Seed[]
  crew: CrewMember[]
  apps: AppRegistryEntry[]
}
```

These are the state-bearing domain fields assembled for `InitialStateMessage`
in `internal/daemon/websocket.go`; A5 adds `apps` as its registry projection,
not the operator-oriented `AppSummary`. Protocol
identity, warnings, client-only metadata, and the untyped `settings` map are
not app state and are not included. In particular, `settings` is the daemon's
entire settings table plus derived client capability flags, not a bounded app
read contract. An app that needs configuration gets a purpose-built typed
reader later. The builder reads local and relayed domain state exactly as
Initial State does. The snapshot call captures `asOfSeq` first, then builds the
projection; every mutation committed at or below that position is therefore
already visible, while a later mutation still has a fact above the fence.

The complete reconcile-time surface is deliberately small:

- `ctx.current.snapshot()` reads attn's current state.
- `ctx.collections.<declared>` provides the existing `get`, `put`, `delete`,
  `query`, and `count` operations in the app's own namespace.
- The bundle retains the ordinary Bun Web and Node APIs. An app whose truth is
  an external service may read it directly.

There is no bus replay, raw database access, arbitrary namespace read, live
query, or cursor mutation in this contract. A state domain not present in the
snapshot must first gain an ordinary typed current-state read; a reconcile
handler must not pretend an old fact payload is current truth.

### 3. Run in the shared sidecar, with the existing dispatch tripwires

Recommendation: reconcile is an ordinary version-stamped dispatch on the
single-threaded event loop in the one shared Bun sidecar
(`apphost/src/index.ts`). It uses the existing 60-second dispatch timeout and
2-second liveness ping from
`internal/daemon/app_runtime.go` and `internal/daemon/app_consumers.go`.

The receipt is A4's live measurement: a warm document-writing handler took
0–1ms, cold process start plus import and first write took 41–77ms, and an
answered sidecar ping took 344–416us. Sixty seconds is four to five orders of
magnitude beyond an ordinary SDK round trip; the two-second ping is roughly
5,000 times its measured healthy cost. A reconcile large enough to need more
than that checkpoints progress in its own collection and remains idempotent
across attempts. The failure names `timeout=60s` and the operation it
abandoned.

The exit proof records the full seeded rebuild duration. If a healthy rebuild
approaches this tripwire, the implementation changes the timeout from that
receipt before merging; it does not add an unmeasured second budget.

A timeout must interrupt the work, not merely stop waiting for it. After the
ping attributes the dispatch, the daemon terminates that sidecar generation
and lets the existing supervisor start a new one. This covers both shapes:

- a yielding promise that never settles is removed with the old process;
- a non-yielding loop cannot prevent the daemon's Go timer from firing, and
  cannot starve other apps after the generation is terminated.

Other in-flight apps receive runtime failures and retry from their unchanged
cursors. The worst broken reconcile can hold the shared sidecar is the existing
dispatch timeout plus its liveness attribution; it cannot park the process in a
permanent frozen state. The stop must be generation-fenced through
`internal/supervise`, never a process-name kill.

### 4. Keep reconcile owed until success; make every attempt visible

Recommendation: the request is the durable statement that reconciliation is
owed. It is complete only when the handler returns successfully and the
consumer cursor is advanced through the claimed fence in the same transaction.

The runtime writes an invocation row with `status=running` before dispatch and
settles it to `ok`, `error`, `runtime_error`, or `interrupted`. A row still
running when the daemon starts is marked interrupted. Its reconcile request is
unchanged, so the next enabled drain runs it again.

A throw or timeout follows the existing app failure policy:

- keep the request and cursor in place;
- record the attempt under handler `reconcile`, stamped with the serving
  version and the structured reasons;
- retry with A1's capped exponential backoff;
- after A4's existing 15-minute stall window on the same claimed request,
  auto-disable the app and write the existing durable notification.

There is no fixed attempt count. It retries at least once and as many times as
fit before success or the existing stall clock disables it. Re-enabling clears
the stall clock but not the owed request.

A daemon stop or sidecar restart is interruption, not app failure. It records
`interrupted` when the daemon can, otherwise startup closes the leftover
`running` row. It does not advance the app's failure clock. A sidecar transport
failure records `runtime_error`; the supervisor's existing park and notification
remain the loud terminal state for a broken runtime.

The user-facing surfaces are the same three levels as handler failure:

- every attempt is in the invocation log and `attn app dev` stream;
- `attn app status <name>` shows `reconcile owed`, its reasons and fence, the
  current attempt, and the last error;
- a durable notification is written when auto-disable makes the failure need
  user action.

This is intentionally not an immediate notification on every throw. Ordinary
handler failures are visible while retrying and notify when attn stops trying;
reconcile follows the same posture.

### 5. Reconcile is a fence in the app's fact-delivery lane

Recommendation: an app receives no fact while any reconcile request is owed or
running. The bus's pre-drain hook runs after the enabled bit and log bounds are
read, but before the first `Since(cursor)` call.

On success, one transaction marks the claimed requests complete and advances
the consumer to `max(cursor, through_seq)`. Facts at or below the fence are not
replayed: the current-state rebuild supersedes them. Facts appended during the
run have a greater seq and remain in the log. They are delivered in order after
the hook finds no later reconcile request.

This gives every trigger the same sequence:

```text
old in-flight handler settles
  reconcile current version through fence N
    trigger during run? reconcile again through its later fence
  deliver N+1, N+2, ...
```

A snapshot may include a mutation above the claimed fence. Its later fact is
still delivered. That is an allowed duplicate observation under the bus's
existing at-least-once contract; it is never a missed mutation.

A5 commands are not bus deliveries. While reconcile is owed or running, the
daemon refuses a command for that app with a named `reconcile_owed` result and
the current reasons. Letting commands mutate the same collections halfway
through a rebuild would make the fence meaningless. Views remain mounted and
may observe intermediate document writes; a reconcile that needs atomic visual
replacement writes a generation marker in its collection and switches that
marker last.

### 6. The handler converges; the runtime serializes and redelivers

Recommendation: the app contract states this plainly:

- A reconcile handler is idempotent. Running it twice with the same reason, or
  after a partially completed attempt, must converge to the same collection
  contents.
- It removes rows that no longer exist in current truth as well as adding and
  updating rows that do. A rebuild that only upserts is incomplete.
- An ordinary event handler remains tolerant of redelivery. It may receive a
  fact already reflected in the snapshot used by reconcile.

The runtime guarantees:

- at most one reconcile runs for an app at a time;
- no fact handler or app command for that app runs across its reconcile fence;
- the serving version and claimed reason are fixed for one attempt;
- the request and cursor advance survive or fail together;
- interruption and failure never complete the request or advance the cursor;
- a callback through `ctx` is accepted only while that dispatch is in flight,
  matching today's `lookupAppDispatch` fence.

Different apps still run concurrently on the sidecar's event loop. A handler
that yields does not hold them up; a handler that does not yield loses its
sidecar generation at the timeout.

### 7. No handler means no silent resume

Recommendation: a subscribed app without `reconcile = true` must never cross a
trigger while still enabled.

- **Version change:** refuse apply or rollback before the pointer moves. The
  error names the current and requested versions and says to declare and
  implement `reconcile`. Initial install is allowed because there is no old
  derived state.
- **Re-enable:** allowed. Re-enable is not a trigger — the consumer resumes
  from its frozen cursor and delivers the retained backlog, which needs no
  handler. A gap discovered at that resume follows the gap rule below.
- **Gap discovered:** leave the cursor in place, disable the app, record a
  `missing_reconcile` invocation, and write a durable notification. Retrying
  code that does not exist cannot heal. Under the retention policy at the end
  of this doc, an installed app's gap means genuinely broken state —
  corruption or a cursor from a removed install — so loud is correct.

An A5 view-only app keeps the sentinel consumer row used for uniform lifecycle
handling, but its filter matches nothing. It cannot miss a subscribed fact, so
reconcile obligations are inert for it. Inert means the trigger is never
recorded: completion advances a bus cursor, and an app whose filter matches
nothing has none to advance, so a request written for it could only sit owed
forever and refuse the app's commands. As built in slice 1 the version-change
trigger did fire for such an app; slice 3 guards it on the filter.

Silently keeping the old A1 skip-to-head behavior was rejected. So was running
an optional no-op reconcile: both certify stale collections as current.

### 8. Existing apps are grandfathered, not silently declared safe

Recommendation: the migration creates no reconcile request merely because attn
was upgraded. Existing installed versions continue to run under their frozen
declarations, and their cursors do not move.

`attn app status` reports `reconcile: unsupported` for a subscribed version
whose frozen declaration predates the field. The next trigger follows the loud
rules above: a version move is refused, and a discovered gap disables and
notifies; re-enable resumes from the frozen cursor and needs no handler.
Re-applying byte-identical content is allowed because it moves no pointer and
triggers nothing.

The scaffold declares and implements reconcile from this stage forward. Its
example rebuilds from `ctx.current.snapshot()`, deletes stale documents, and
upserts the desired set. The generated SDK and scaffold guidance say that
reconcile is idempotent and may be interrupted.

## Persistent shape and ownership

Two small records keep trigger history separate from attempts:

```text
app_reconcile_requests
  id                  monotonic claim boundary
  app_name
  reason              gap | version_changed
  version_id          target version when requested
  through_seq         fence: cursor at trigger (version_changed) or earliest-1 (gap)
  previous_version_id nullable
  cursor              gap only
  earliest            gap only
  missed              gap only
  created_at

app_reconcile_progress
  app_name             primary key
  completed_request_id highest request completed for this app
  updated_at
```

The consumer lane selects requests above `completed_request_id`, captures the
largest id, and folds their reasons. Completion advances only through that
captured id. A request inserted during the run therefore remains pending
without a `running` state machine. A unique trigger key makes retrying the bus's
gap hook idempotent.

`app_invocations` remains the attempt log. Its event fields become optional for
non-event invocations, and its shape gains an invocation kind plus reconcile
reason/fence. Invocation labels are rendering, not dispatch keys: logs may say
`subscription:ticket.*`, `command:refresh`, or `reconcile`, while the sidecar
dispatches through the structured exports above. This joins A5's command and
render-error invocation work instead of encoding a fake `app.reconcile` bus
fact.

The store owns atomic trigger writes and completion-plus-cursor advance.
`internal/bus` owns only when its pre-drain hook runs and the fallback behavior
for consumers without one. `internal/daemon` owns app policy, dispatch,
auto-disable, status, and notification. `apphost` only loads the named version,
builds the SDK context, and invokes the reconcile export.

Removing an app deletes its reconcile requests and progress with the registry
and consumer. Version history, invocation history, and `app/<name>` documents
keep A4's existing retention rules.

## Delivery

This changes daemon lifecycle, persistence, the app-runtime RPC, the SDK, and
user-visible status. Every slice after the store seam needs a running
non-production app; the final slice carries the stale-before/fixed-after live
proof.

1. **Durable trigger and lane fence.** Add the reconcile request/progress
   migration and store transactions; extend `internal/bus` registration with
   the pre-drain hook; write the trigger paths and completion-plus-cursor
   transaction. As built (PR #906), this slice shipped a third trigger —
   `re_enabled` inserted on the enable flip — before review removed re-enable
   from the design; slice 3 deletes that insert and its enum value and
   re-fences `version_changed` and `gap` from bus head to cursor and
   `earliest - 1` respectively, replacing the tests that pin the shipped
   behavior with witnesses for the new contract: re-enable resumes with no
   request and delivers the retained backlog in order, `version_changed`
   delivers facts the old version never received, and no fence ever covers a
   retained undelivered fact. Unit and integration tests cover coalescing, a trigger during a
   run, restart before completion, cursor fencing, and non-app gap parity.
   Verification tier: full non-production app/daemon install because this
   changes a live durable consumer's background loop, plus direct inspection of
   cursor and request rows; no new app code runs yet.
2. **Manifest, SDK, and sidecar dispatch.** Add `reconcile = true`, the typed
   reconcile export beside A5's subscription and command maps,
   `ReconcileReason`, `ctx.current.snapshot()`, the shared Initial State domain
   projection builder, and the reconcile sidecar dispatch. Extend the version
   hash through the frozen declaration as today. Verification tier:
   full non-production app install because the daemon/runtime RPC and packaged
   sidecar change; Linux host build and tests apply because `apphost` and
   `internal/**` run on remotes.
3. **Failure, interruption, and operator surfaces.** Start/settle reconcile
   invocation rows, startup interruption repair, timeout-driven generation
   recycle, app status, command refusal, auto-disable, notifications, and the
   loud missing-handler paths. This slice also lands the retention policy from
   the end of this doc as code, both halves of one promise: keep the facts —
   `TrimBusEvents` floors at the minimum cursor across installed app
   consumers, enabled or disabled, with "disabled must not pin" narrowed to
   rows no install serves, replacing the shipped test that pins the old
   contract (`TestTrimBusEventsIgnoresDisabledConsumers`) with its witness: a
   disabled installed app's rows survive a trim that would have deleted them,
   while orphaned rows still trim — and deliver the facts: slice 1's as-built
   cleanup
   (delete the `re_enabled` insert and enum value, re-fence `version_changed`
   to cursor and `gap` to `earliest - 1`) with the three witnesses named in
   slice 1's note. The per-lane cap stays out until its number has a receipt
   from measured lane growth. Protocol changes follow `main.tsp` generation
   and increment `ProtocolVersion`; generated Go and TypeScript are never
   edited by hand. Verification tier: full non-production app plus packaged-app
   harness. Prove an infinite loop loses its generation and another app resumes.
4. **Scaffold and exit proof.** Teach the handler in the SDK docs. Slice 3
   already gave `attn app new` a declared, converging reconcile handler,
   because gate 7's version-move refusal otherwise scaffolds an app that can
   never be updated; what remains here is the prose that teaches it. First demonstrate today's stale state. Then show disable, publish
   facts, enable delivering the retained backlog in order with no reconcile;
   force a gap and show the loud disable; apply a version deriving a new field
   and show the rebuild followed by delivery of facts the old version never
   received; interrupt a reconcile by restarting the dev daemon; and show each
   case converges before later facts run. Record
   the visible status and notification paths. Verification tier: full packaged
   app on a throwaway profile, with a Linux remote dispatch witness.

Every slice adds focused tests. The bus timing tests run under `synctest`; no
sleeps or polling. The live proof uses a seeded realistic app collection rather
than an empty database.

## Retention is the gap story

Added 2026-08-15, after asking how often gaps actually happen and what attn
tells an app that hits one. The answer changes the retention policy underneath
the gates, and review showed it reaches back into them: keeping facts is only
half the promise, delivering them is the other. Gates 1, 5, 7, and 8 above
already read as amended — re-enable is no longer a trigger, and fences sit at
the consumer's cursor or `earliest - 1` instead of bus head, so a retained
fact is never superseded undelivered.

### Receipts

- The retention window is 30 days, trimmed hourly (`internal/bus/bus.go`,
  `DefaultRetention`, `DefaultTrimInterval`).
- Trim deletes only rows older than the window AND at or below the minimum
  cursor across *enabled* consumers (`internal/store/bus.go`,
  `TrimBusEvents`). An enabled consumer can never gap, however far behind it
  falls; one that pins the floor too long raises the pin alarm
  (`DefaultPinAlarmAge`) rather than losing anything.
- Disabled consumers are excluded from that floor on purpose — "a killed
  consumer must not pin the log" — and disabling an app flips its consumer's
  enabled bit (`internal/daemon/apps.go` on the manual path,
  `internal/daemon/app_consumers.go` on auto-disable).

So a gap has exactly one cause: an app stayed disabled while subscribed facts
aged past the window. Gap frequency is not weather; it is one knob against one
behavior, and both are ours.

### Two kinds of apps, two exposures

- **Projection apps** — collections derivable from the current-state
  snapshot. A rebuild recovers everything; a gap costs nothing once the
  handler exists.
- **History apps** — collections that accumulate facts as they arrive. A
  missed fact is information the app never received. No handler rebuilds it,
  and no app-side checkpoint recovers it: checkpoints protect state the app
  already had, and a gap is by definition facts it never got. App-side
  snapshot machinery was considered for this and rejected. Only retention
  closes the loss, and retention is what opened it.

### Decision: installed apps pin retention

An installed app's consumer pins the retention floor whether enabled or
disabled. "Disabled must not pin" narrows to its real target — orphaned rows
no install serves — which `Unregister` already deletes on uninstall.

The story this buys: **an installed app never loses facts.** Disabled for a
year, its lane waited. Reconcile exists for version moves, corruption, and
cursors from removed installs — not as an apology for pruning.

The cost is a disabled app with chatty subscriptions holding its slice of the
log indefinitely. Facts are small rows in attn's own single-tenant SQLite, so
reserve big: a generous per-lane cap — its number needs a receipt from
measuring real lane growth before it is written — crossed loudly with a
durable notification naming the app, the size held, and the two exits (enable
it or uninstall it). Forcing that conversation beats silent loss.

Two consequences follow, and they are why the gates read as they now do
rather than as first approved:

- **Re-enable stopped being a trigger.** The `re_enabled` reconcile existed
  only because trim used to open a gap on the disable path. With the lane
  retained, re-enable is a paused durable consumer resuming — A1's ordinary
  contract delivers the backlog in order, correct for both app kinds, no
  rebuild. A handler-less app can therefore be re-enabled when no gap exists:
  there is nothing to reconcile. Review caught the first draft promising
  retention while the head-high `re_enabled` fence quietly discarded the
  retained facts on arrival; dropping the trigger, rather than fencing it
  lower, was chosen because the rebuild the lower fence preserved was
  redundant work the replay right behind it would supersede.
- **Fences moved off bus head.** `version_changed` fences at the consumer's
  cursor and `gap` at `earliest - 1` (Gate 1), so a fence can only cover
  facts already seen or forever lost — never a retained, undelivered one.

Gate 7's gap rules stand. Under this policy a live gap means corruption or a
cursor from a removed install — genuinely broken states — and a broken thing
being loud is the point.

## Open questions

None. Victor approved the two review calls on 2026-08-14:

- expose the state-bearing Initial State domain projection as one snapshot and
  no raw store or settings API; typed domain readers may be added later without
  breaking it;
- refuse a subscribed legacy version's pointer move before it happens rather
  than install a version attn already knows it cannot serve safely.

Victor also required A5's structured export shape: subscription and command
maps are separate, and reconcile is its own sibling export. Retry, ordering,
interruption, missing-handler behavior, and the sidecar timeout therefore have
no implementation-time product choice left.
