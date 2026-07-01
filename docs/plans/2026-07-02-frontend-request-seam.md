# Plan: One request seam for the app's socket

From the 2026-07-01 architecture review. Line references are as of commit
`80c62f6b` — re-anchor by symbol name.

## Goal

`app/src/hooks/useDaemonSocket.ts` (4,871 lines) hand-rolls the same
request/result correlation 58 times: `new Promise` → build a string key →
`pendingActionsRef.current.set(key, {resolve, reject})` → `ws.send` →
`setTimeout(30s, reject)` — plus a matching `case '*_result'` arm in the 84-case
`onmessage` switch that looks the key up and settles it. Key formats are
invented per action and have already collided once (`sendCreateWorktree` vs
`sendCreateWorktreeFromBranch`, documented as a gotcha in `app/CLAUDE.md`).

Deepen the correlation into one module. Each `send*` becomes ~3 lines; the
result routing becomes a declarative table; the key-collision bug class dies.

**Behavior-preserving**: same commands on the wire, same timeout semantics, same
rejection on disconnect.

## Architecture Map

```text
Current (x58):
sendPRAction (~3223)
  new Promise -> key = `${id}:${action}` -> pendingActionsRef.set -> ws.send -> setTimeout(30s)
onmessage switch (~1195-2600)
  case 'pr_action_result': rebuild key -> get/delete pending -> resolve/reject
onclose (~910, ~990): iterate pendingActionsRef, reject all ("connection lost")

Target:
requests = createRequestCorrelator({ send: ws.send })     // app/src/pty|utils/requestCorrelator.ts
sendPRAction = (action, id, method) =>
  requests.request({ cmd: `${action}_pr`, id, method }, { key: `pr:${id}:${action}` })

onmessage: RESULT_ROUTES lookup FIRST, then the (shrinking) switch for
           broadcast/store events
RESULT_ROUTES: Record<event, { key(data), settle(data) -> {ok} | {err} }>
onclose: requests.rejectAll('connection lost')
```

## Data Model / Interfaces

```ts
// requestCorrelator.ts — a plain module, no React. Unit-test in isolation.
type Settled<T> = { ok: T } | { err: string };

function createRequestCorrelator(deps: { send(msg: object): boolean }) {
  const pending = new Map<string, { resolve; reject; timer }>();
  return {
    // key must be unique per in-flight request; helper for sequenced keys:
    nextKey(prefix: string): string,               // `${prefix}:${seq++}`
    request<T>(msg: object, opts: { key: string; timeoutMs?: number }): Promise<T>,
    settle(key: string, result: Settled<unknown>): boolean,  // false = no pending entry
    rejectAll(reason: string): void,
  };
}

// result routing — data, not code:
const RESULT_ROUTES: Record<string, (data: any) => { key: string; result: Settled<any> }>
```

Keys get a per-command prefix (`pr:`, `worktree:`, `snapshot:` …) plus the
existing identifying fields; where today's key can collide, use `nextKey` and
echo the request id through the daemon's existing `request_id` fields (several
commands already carry one — e.g. `workspaceActionKey` ~600).

## Boundaries

- The correlator owns: pending map, key uniqueness discipline, timeouts,
  reject-on-disconnect. It knows nothing about React, WebSocket lifecycle, or
  event shapes.
- `RESULT_ROUTES` owns: event-name → key + success/error extraction. It must not
  touch stores or component state — result events that ALSO update a store keep
  that store update in the switch (route first, then fall through if needed).
- `useDaemonSocket` keeps: socket lifecycle, reconnect/backoff, broadcast-event
  handling. Its returned functions become thin `requests.request(...)` calls.

## Implementation Steps

- [ ] PR 1: add `requestCorrelator.ts` + unit tests (resolve, reject-on-error
      result, timeout fires and cleans up, rejectAll, duplicate-key request is a
      programmer error — throw). Wire it into `useDaemonSocket`; migrate the
      canonical `sendPRAction` + `pr_action_result` as the proof, leaving
      everything else untouched. Update the "Async Pattern Guide" comment
      (~727-760) to describe the new one-liner recipe.
- [ ] PR 2..N: migrate the remaining ~57 promise-based senders in clusters
      (PRs/repo, worktrees, workspace actions, fs/notebook, screen snapshots,
      tickets…), one PR per cluster, deleting each matching switch arm as its
      route lands in `RESULT_ROUTES`. Keep each PR under ~400 lines of diff.
- [ ] Final PR: assert the end state (greps below), update `app/CLAUDE.md`
      gotchas #1/#2 — the 4-step ritual and the key-collision warning should be
      replaced by "add a route + a 3-line sender".

## Decisions

- The correlator is a plain module, not a hook: no re-render coupling, trivially
  unit-testable, and `useDaemonSocket` already holds it in a ref.
- Fire-and-forget senders (the optimistic pattern in the guide comment) are NOT
  converted — that's a UX decision per action, out of scope.
- Do not batch-migrate all 58 in one PR: the per-cluster slicing keeps each diff
  reviewable and bisectable.

## Verification

Per PR:

```bash
cd app
pnpm test                      # useDaemonSocket.test.tsx (2,893 lines) is the net —
                               # it drives behavior via MockWebSocket frames and
                               # must stay green with MINIMAL edits (only where a
                               # test asserted an internal key format)
pnpm run typecheck 2>/dev/null || pnpm exec tsc --noEmit
```

End-state structural asserts:

```bash
grep -c 'new Promise' app/src/hooks/useDaemonSocket.ts          # -> ~1 (socket open) or 0
grep -c 'pendingActionsRef' app/src/hooks/useDaemonSocket.ts    # -> 0 (ref replaced by correlator)
```

Behaviors that must survive:

1. 30s default timeout, same rejection message semantics callers display.
2. All in-flight requests reject on socket close/reconnect (today's onclose
   sweep at ~910/~990).
3. Result events that also feed stores (e.g. list-shaped results) still update
   those stores.
4. Manual: `make dev` (or `pnpm run dev`), then approve/merge a PR from the PR
   panel, create + delete a worktree from the UI, and open a notebook file —
   loading states resolve, errors surface as toasts, nothing hangs.

## Follow-ups

- The 17 `on*Update` callbacks / five-sources-of-truth problem (review card #7:
  one normalized store + `useDaemon()` context) builds on this but is its own
  design conversation — do not fold it in here.
