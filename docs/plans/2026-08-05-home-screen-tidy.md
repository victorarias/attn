# Plan: home stops lying about what it is showing

## Why / Alignment

Home is where the queue ends and where a day starts. Three things on it are
either noise or wrong.

**The shortcut strip.** A hardcoded row of four key hints — `⌘T`, `⌘1-9`, `⌘,`,
`⌘/` — pinned under the cards. It was never derived from the shortcut registry,
so it drifts silently whenever a binding moves, and `⌘/` already opens the real
cheatsheet. It is chrome that costs height on every visit to teach four things
once.

**Snoozed agents are miscategorised.** Snooze settles as it defers, so a snoozed
agent carries no `turn_owed` — which is exactly what home's turn/settled split
reads. A deferred agent therefore lands in a plain state group and renders as
"Working" or "Idle", the one thing that is not true of it: it is waiting on a
clock nobody can see from here. The snooze plan deferred any home presence
explicitly; this is that follow-up.

**Your PRs are buried among the ones you were asked to look at.** The card groups
strictly by repo, so a PR you opened sits between two review requests with only a
✏️ against 👀 to tell them apart. The two are different work — one you are
waiting on, one someone is waiting on you for — and the one you come to home to
find is the smaller of the two.

**Aligned on** (Victor, 2026-08-05):

- **The strip goes, with nothing in its place.** `⌘/` already opens the shortcut
  cheatsheet, which is generated from the registry and therefore correct.
- **Snoozed gets a collapsible section at the foot of the Sessions card, above
  Muted Workspaces**, with each agent's wake time and a wake-now action —
  mirroring the sidebar exactly.
- **The PR card splits into two sections: Yours, then Review requested.** Yours
  leads and stays flat with the repo named inline; the review side keeps today's
  collapsible repo groups.

**Deferred.** Persisting the Snoozed section's expanded state. A snooze
affordance on home (deferring is still done from the sidebar row, the shortcut,
or the command menu) — home shows deferrals and undoes them; it does not make
them.

## User-visible shape

```text
┌─────────────────────────────┐  ┌────────────────────────────────────┐
│ SESSIONS              + New │  │ PULL REQUESTS                ↻   5 │
├─────────────────────────────┤  ├────────────────────────────────────┤
│ Your turn  2                │  │ YOURS  2                           │
│   ● agent-a            12m  │  │  ✏️ attn #762  speed cold …   ✓  ● │
│   ● agent-b             4m  │  │  ✏️ attn #761  keep Ghostty …    ● │
│ Settled                     │  │                                    │
│  Working                    │  │ REVIEW REQUESTED  3                │
│   ● agent-c                 │  │ ▾ attn              2 review     ⊘ │
│  Idle                       │  │   👀 #758  read a cursor's anchor  │
│   ● agent-d                 │  │   👀 #757  delete stale plans      │
│ ─────────────────────────── │  │ ▾ pierre/diffs      1 review     ⊘ │
│ ▸ Snoozed  2                │  │   🤖 #12   bump deps               │
│ ▸ Muted Workspaces (1)      │  │                                    │
└─────────────────────────────┘  └────────────────────────────────────┘
                                                (no shortcut strip below)
```

Expanded, a snoozed row reads `● agent-e        2:30 PM  ↩`, the wake button
appearing on hover.

## Boundaries

- `queueBands.ts` owns queue ordering. Home imports `compareWakeOrder` (newly
  exported, for the same reason `compareTurnOrder` already was) rather than
  sorting deferrals its own way — two orders for one set of promises is two sets
  of promises.
- Snooze membership is read from `turnSnoozedUntil` through `isSnoozed`, never
  derived from state.
- The daemon is untouched. `turn_snoozed_until` already rides the wire and
  already reaches home's props; nothing new is persisted, broadcast, or
  computed server-side.
- The PR split reads the existing `role` field. No new wire data.

## Decisions

- **The Snoozed section is not gated on queue mode**, unlike the sidebar's. A
  snooze can only be *made* with the queue on, but it outlives the setting being
  turned off — and the shortcut, the command menu, and the sidebar section are
  all queue-gated. With the arrangement off, this section is the deferral's only
  remaining way out.
- **A deferred chief lands in the Snoozed section**, where the sidebar would keep
  it in its anchored slot. Home's Sessions card lists the chief alongside every
  other agent, so excluding it would put a deferred chief back under a state
  group that cannot describe it. The Chief of Staff card is unaffected.
- **Snoozed agents leave `stillRunning`.** The all-settled banner says what is
  in flight and coming back to you; a deferred agent is not, and the
  `Snoozed (n)` count is its own receipt.
- **Collapsed by default**, matching the sidebar: a snooze surfaces itself when
  it wakes, so the section is for checking on a promise or breaking it early.
- **`.shortcut` / `.shortcut kbd` stay in `Dashboard.css`.** They are the app's
  only definition of a keyboard hint and `LocationPicker` and `AttentionDrawer`
  still render them; only `.dashboard-footer` and `.footer-shortcuts` go.
- **"Yours" carries no repo-mute button.** Muting a repo hides your own PRs in
  it, which is not an act anyone wants; when the repo also has review requests
  the button is right there, and Settings unmutes either way. Known consequence:
  a repo where you only ever author PRs can no longer be muted from home.

## Implementation Steps

- [x] Export `compareWakeOrder` from `utils/queueBands.ts`.
- [x] `Dashboard.tsx`: `turnSnoozedUntil` on `DashboardSession`; partition
      snoozed out of the turn and state groups; collapsible `Snoozed` section
      above Muted Workspaces with wake times and a wake-now button.
- [x] `App.tsx`: pass `onWakeTurn={sendWakeTurn}` to `Dashboard`.
- [x] `Dashboard.tsx`: split the PR card into `Yours` (flat, repo inline) and
      `Review requested` (repo groups); extract the shared `renderPRRow`.
- [x] `Dashboard.tsx`: delete the shortcut footer; `Dashboard.css`: drop
      `.dashboard-footer` / `.footer-shortcuts`, keep `.shortcut`.
- [x] `Dashboard.css`: snoozed group, wake time/button, PR section headers,
      inline repo name.
- [x] Tests: `Dashboard.test.tsx` for the snooze partition, wake order,
      wake-now, queue-off reachability, the deferred chief, a lapsed deadline,
      the absent footer, and the PR split.
- [x] `changelog.d/` fragment.
- [x] `home_get_state` reports the new sections — the snoozed group and its
      rows, which sessions are still grouped by state, both PR sections, and
      whether the shortcut footer is present — so the change has an automation
      witness the way the sidebar's snoozed section already does.
- [x] Live verification on a throwaway `homely` profile (preflight PASS,
      installed from this branch): five injected agents, two snoozed over the
      wire, queue mode on, injected PRs beside real GitHub ones.
      `home_get_state` reported `shortcutFooter: false`; `snoozed` present with
      count 2, collapsed, expanding to rows carrying wake times
      (`11:42 PM`, `tomorrow 6:07 PM`) and a wake control;
      `stateGroupSessionIds` held only the three awake agents, so neither
      deferred agent appeared under a state group. The PR card read
      `Yours 11` as flat rows with the repo inline and no repo groups, then
      `Review requested 3` in two repo groups with the mute button intact.
      Profile cleaned afterwards.
- [x] Narrow `stateGroupSessionIds` to groups that mark themselves
      (`data-session-group="state"`). Review caught that the original
      `[data-testid^="session-group-"]` prefix also matched the turn band, so
      the field named one thing and reported another; an exclusion list would
      have gained a hole the next time a group was added. Covered by a unit
      test reading the same DOM the bridge reads, with an owed turn, a settled
      agent, and a deferred one on screen.

## Follow-ups

- The Snoozed section's expanded state is local and resets on reload, as the
  sidebar's does. Worth persisting both together if it annoys.
- "Review requested" is `role === 'reviewer'`, which the daemon assembles from
  both `review-requested:@me` and `reviewed-by:@me` — so a PR you reviewed
  uninvited reads as requested. Splitting them needs a new wire field.
