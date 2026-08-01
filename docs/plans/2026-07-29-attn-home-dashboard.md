# Plan: the Dashboard becomes attn's home

## Goal

Settling the last owed turn currently does nothing — `nextTurnAfterSettle` returns
`null` and selection stays on the agent you just finished with. Give the queue a
terminus: settling the last turn lands you on the Dashboard, which becomes attn's
*home* — turn-aware, rendered alongside the sidebar like the session view, and
reachable from a fixed Home row at the top of the sidebar.

The Dashboard today groups sessions by `UISessionState` and knows nothing about
turns. In queue mode that makes it contradict the sidebar: it would say
"Waiting for input (3)" about the three agents you just settled. Teaching it
turns is what makes it usable as the all-settled screen, and it is the same fix
either way.

## User-visible shape

```text
┌────────────────┬──────────────────────────────────────────┐
│ [grid] [tools] │  ✓ All settled                           │  ← banner, queue mode,
│  ⌂ Home     ⌘G │    3 working · 1 scheduled               │    only when turns == 0
│ ─────────────  ├──────────────────────────────────────────┤
│  chief         │  Sessions          Chief         PRs      │
│ Your turn   2  │  ┌──────────────┐ ┌────────┐ ┌─────────┐ │
│   agent-a  12m │  │ Your turn  2 │ │        │ │         │ │  ← "Your turn" leads the
│   agent-b   4m │  │  agent-a 12m │ │        │ │         │ │    Sessions card; absent
│ Settled        │  │  agent-b  4m │ │        │ │         │ │    when nothing is owed
│   agent-c      │  │ Settled      │ │        │ │         │ │
│   agent-d      │  │  Working     │ │        │ │         │ │  ← state groups, over
│ ─────────────  │  │   agent-c    │ │        │ │         │ │    settled sessions only
│  workspace/... │  │  Idle        │ │        │ │         │ │
└────────────────┴──────────────────────────────────────────┘
```

- Sidebar header loses the `WORKSPACES` title and the `⌘G` label; the `+` and
  settings gear right-align. The tool-row home button goes away — the Home row
  replaces it.
- Home row sits above everything, including the chief slot, and highlights when
  the dashboard view is active.
- Queue off: the Home row still renders, the Dashboard keeps today's state
  groups, no banner. Only the shell change applies.

## Architecture Map

```text
Current:
App render
  ├─ .view-container (dashboard)     position:absolute inset:0   ← full-window takeover
  │    └─ Dashboard
  ├─ .view-container (session)       position:absolute inset:0
  │    ├─ Sidebar                                                ← sidebar lives INSIDE
  │    ├─ .terminal-pane
  │    └─ RightDock
  └─ .view-container (grid)          position:absolute inset:0, conditional

Target:
App render
  ├─ .app-shell                      position:absolute inset:0, flex row
  │    ├─ Sidebar                                                ← hoisted, always on
  │    └─ .view-stack                flex:1, position:relative
  │         ├─ .view-container (dashboard)
  │         │    └─ Dashboard
  │         └─ .view-container (session)
  │              ├─ .terminal-pane
  │              └─ RightDock
  └─ .view-container (grid)          UNCHANGED full-window overlay, outside the shell

Sidebar internals:
Sidebar
  ├─ .sidebar-header      tool row (no home-btn) + header row (no title, no ⌘G)
  ├─ .sidebar-home-row    NEW — onGoToDashboard, active when view === 'dashboard'
  ├─ QueueBands           chief / Your turn / Settled   (queue mode only)
  └─ .session-list        workspace tree
```

## Data Model / Interfaces

```ts
// Dashboard.tsx — turn facts arrive alongside state, both read from the daemon.
type DashboardSession = {
  id: string; label: string; state: UISessionState; cwd: string;
  endpointName?: string; endpointStatus?: string; chiefOfStaff?: boolean;
  turnOwed?: boolean;        // NEW — daemon's answer, never derived
  turnOpenedAt?: string;     // NEW — drives the age chip + ordering
}

interface DashboardProps {
  // ...existing
  queueModeEnabled: boolean; // NEW — selects turn-first vs today's state shape
}

// Sidebar.tsx
interface SidebarProps {
  // ...existing (onGoToDashboard already exists)
  homeActive?: boolean;      // NEW — view === 'dashboard'
}
```

Turn ordering and age formatting reuse `queueBands.ts` (`formatTurnAge`, the
`turnOpenedAt` ascending + session-id tiebreak comparator) rather than
reimplementing them in the Dashboard.

## Boundaries

- `queueBands.ts` owns turn ordering and age formatting. The Dashboard imports
  them; it does not derive turn membership from state.
- `App.tsx` owns the view state and passes `queueModeEnabled` / `homeActive`
  down. Neither Sidebar nor Dashboard reads the setting itself.
- The Dashboard's state groups are computed over *settled* sessions in queue
  mode, so a session is never in two groups.
- Grid view stays outside the shell. Hoisting the sidebar must not change what
  grid view looks like.

## Implementation Steps

- [x] Hoist `Sidebar` into a new `.app-frame` wrapper; move the dashboard and
      session `.view-container`s into a `.view-stack` column. Leave grid alone.
- [x] `App.css`: `.app-frame` flex row, `.view-stack` flex:1 + position:relative;
      the view-containers become absolute within the stack, not the window.
- [x] Sidebar header cleanup: drop `home-btn`, `sidebar-title` and
      `home-shortcut`; right-align the row (`.sidebar-title { flex: 1 }` was
      carrying the layout).
- [x] Add `.sidebar-home-row` above `QueueBands`; `homeActive` drives the
      selected state. Collapsed rail keeps its icon button and gains the same
      active state.
- [x] Dashboard: accept `turnOwed`/`turnOpenedAt`/`queueModeEnabled`; in queue
      mode lead the Sessions card with a "Your turn" group (age chips, oldest
      first) and compute state groups over settled sessions under a `Settled`
      label.
- [x] Dashboard: all-settled banner above the grid when
      `queueModeEnabled && turns.length === 0`, with a one-line summary of what
      is still running.
- [x] `handleSettleActiveTurn`: `else` branch calls `goToDashboard()` when
      `nextTurnAfterSettle` returns null.
- [x] Tests: `Dashboard.test.tsx` for turn-first grouping, the banner, the chief
      exclusion and the queue-off shape; `Sidebar.test.tsx` for the Home row,
      its active state, the collapsed rail and the header cleanup.
- [x] `CHANGELOG.md` entry.
- [x] Live verification: `make dev` on the dev profile, preflight PASS, queue
      mode on, two agent sessions owing turns, ⌘⇧E chained to zero — the first
      settle jumped to the next agent, the second cleared the selection and
      landed on home showing "All settled".

## Decisions

- **Grid view stays a full-window overlay.** Hoisting the sidebar out of the
  session container would otherwise put a sidebar next to grid view as a silent
  side effect. Grid keeps its own container outside `.app-shell`.
- **The all-settled headline is a banner, not the "Your turn" group's empty
  state.** Saying it in both places is redundant; the banner gives the moment
  weight and the group simply does not render when empty.
- **No auto-jump when a new turn opens while parked on home.** Home is also
  where you go to start work — getting yanked mid-launch is worse than one
  keystroke. Pickup stays manual (⌘J / clicking the queue).
- **State groups survive in queue mode, nested under `Settled`.** Dropping them
  would lose the working/scheduled/recoverable signal that makes an all-settled
  screen worth looking at; nesting them removes the contradiction without
  removing the information.
- **The chief is outside both bands** (`queueBands.ts:92`), so "all settled" can
  be true while the chief wants you. Keeping that consistent with the sidebar is
  the lesser surprise.
- **The dashboard grid scrolls as a whole, with content-sized rows.** Found in
  live verification: beside the sidebar the cards stack into one column, and the
  old `flex: 1` split the column's height three ways, leaving every card too
  short to show anything. `grid-auto-rows: minmax(200px, max-content)` +
  `overflow-y: auto` on the grid, rather than per-card scrolling.

## Open Questions

- The dashboard header (app icon, "attn / attention hub") is app chrome that the
  sidebar now provides. Kept, with the page padding tightened; worth removing
  outright if it keeps reading as wasted height beside the sidebar.
- The banner's summary counts `recoverable` as still running. It is — the daemon
  is reviving those unattended — but "5 recoverable" as the headline detail may
  read more like a problem than a status.

## Follow-ups

- `⌘J` jump-to-waiting still uses unmuted order, not queue order
  (`App.tsx:2880`). Worth reconciling once home lands.
- A pickup affordance on the home screen ("2 waiting — ⏎") if manual pickup
  proves annoying in practice.
