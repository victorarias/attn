# Plan: Ambient "waiting on you" counter

## Goal

One small number, where Victor already looks, that answers "what needs me right now":
the count of tickets that are **blocked** (owe him a reply) UNION **in_review** (owe him
a look). That union is precisely his personal queue, and today it exists only by opening
the board and scanning two columns — or asking the chief. Surface it as a badge on the
existing board button in the sidebar tool row (`sidebarHeaderActions` in `App.tsx`, id
`board`); clicking the badged button opens the board surface pre-filtered to a new
combined **Waiting on you** filter. This is awareness without a dashboard you must visit
(an explicit vision non-goal in `docs/vision/chief-delegation-awareness.md`): the board
informs, never gates; the counter is read-only and ambient.

## Architecture Map

Zero protocol, zero daemon changes. The frontend already receives every non-archived
ticket via the `tickets_updated` broadcast (`useDaemonSocket.ts` → `setTickets` on the
zustand store in `app/src/store/daemonSessions.ts`); everything here is derivation.

```text
Current:
daemon tickets_updated ─> useDaemonSocket ─> useDaemonStore().tickets
  App.tsx (line ~1064 destructure)
    ─> sidebarHeaderActions[board] ── onClick: openBoardSurface ──> TicketBoardSurface
         (no badge)                                                   ─> TicketBoardPanel (filter useState 'all')

Target:
daemon tickets_updated ─> useDaemonStore().tickets
  App.tsx
    ├─ waitingOnYouCount = waitingOnYouTickets(tickets).length      <- NEW shared selector
    ├─ sidebarHeaderActions[board]
    │    badge: count > 0 ? count : undefined                        (Sidebar renders .sidebar-tool-badge,
    │    onClick: () => openBoardSurface(count > 0 ? 'waiting_on_you' : undefined)   '9+' cap for free)
    └─ openBoardSurface(initialFilter?) ─ sets boardInitialFilter + opens
         ─> TicketBoardSurface initialFilter={boardInitialFilter}
              ─> TicketBoardPanel initialFilter  (useState initial value; panel
                   unmounts on close, so each open re-applies it)

Shared seam:
app/src/utils/waitingOnYou.ts   <- owned by THIS plan
  ├─ consumed by App.tsx (badge count)
  ├─ consumed by TicketBoardPanel.applyFilter ('waiting_on_you' branch)
  └─ consumed by the dashboard-state-card plan (sibling 2026-07-02 plan) — it IMPORTS
     this selector, it must not re-derive the union

Tests:
utils test  ─> waitingOnYou.test.ts (pure, statuses in/out)
panel tests ─> TicketBoardPanel.test.tsx (applyFilter + filter pill + initialFilter)
app test    ─> App.waitingOnYouBadge.test.tsx (mocked App scaffold, REAL board surface:
               badge render, hidden at zero, click-through to pre-filtered board)
```

## Data Model / Interfaces

No new persistent or wire state. New frontend shapes only:

```ts
// app/src/utils/waitingOnYou.ts (NEW) — the single definition of "waiting on you"
export const WAITING_ON_YOU_STATUSES: ReadonlyArray<Ticket['status']> =
  [TicketStatus.Blocked, TicketStatus.InReview];
export function isWaitingOnYou(t: Ticket): boolean;
export function waitingOnYouTickets(tickets: Ticket[]): Ticket[]; // tolerant of undefined like applyFilter

// TicketBoardPanel.tsx
export type BoardFilter = 'all' | 'waiting_on_you' | 'blocked' | 'in_review' | 'closed_today';
interface TicketBoardPanelProps { ...; initialFilter?: BoardFilter }   // mount-time only
interface TicketBoardSurfaceProps { ...; initialFilter?: BoardFilter } // pass-through

// App.tsx
const [boardInitialFilter, setBoardInitialFilter] = useState<BoardFilter | undefined>();
const openBoardSurface = useCallback((initialFilter?: BoardFilter) => {
  setBoardInitialFilter(initialFilter);   // every open sets it — undefined clears, no reset-on-close needed
  setBoardSurfaceOpen(true);
}, []);
```

`Ticket` is `app/src/hooks/useDaemonSocket.ts`'s re-export of the generated type;
`TicketStatus` comes from `app/src/types/generated.ts` (values: todo / working / blocked
/ in_review / done / failed / crashed).

## Boundaries

- **`waitingOnYou.ts` owns the definition** of the union. The badge, the board filter,
  and the sibling **dashboard-state-card plan** all consume it — that plan imports this
  selector; neither it nor `applyFilter` re-states the status set.
- **`App.tsx` owns when to pre-filter**: only the badged sidebar button, and only when
  the count is > 0. ⌘⇧T (`board.open`) and the ⌘K `tickets-board` action keep opening
  unfiltered.
- **`TicketBoardPanel` owns filter state.** `initialFilter` is read once as the
  `useState` initial value; the panel does not sync to later prop changes. This is sound
  because `TicketBoardSurface` returns `null` when closed, unmounting the panel — every
  open is a fresh mount.
- **No daemon/protocol surface moves.** The store's `tickets` are already non-archived
  (`daemonSessions.ts` doc comment), so archived tickets are excluded for free. attn
  still authors only `crashed`; the board stays read-only; no OS notifications.

## Implementation Steps

- [ ] **Selector** — add `app/src/utils/waitingOnYou.ts` exporting
      `WAITING_ON_YOU_STATUSES`, `isWaitingOnYou`, `waitingOnYouTickets` (guard
      `tickets ?? []`, mirroring `applyFilter` in `TicketBoardPanel.tsx`). Add
      `app/src/utils/waitingOnYou.test.ts`: blocked and in_review are in; todo, working,
      done, failed, crashed are out; empty/undefined input returns `[]`. Copy the
      `makeTicket` fixture shape from `TicketBoardPanel.test.tsx`.
- [ ] **Board filter** — in `app/src/components/TicketBoardPanel.tsx`: extend the
      `BoardFilter` union; insert `{ id: 'waiting_on_you', label: 'Waiting on you' }`
      into `FILTERS` right after `all`; add the `applyFilter` branch
      `if (filter === 'waiting_on_you') return isWaitingOnYou(t);`. Add
      `initialFilter?: BoardFilter` to `TicketBoardPanelProps` and seed
      `useState<BoardFilter>(initialFilter ?? 'all')`.
- [ ] **Board filter tests** — extend `TicketBoardPanel.test.tsx`: an `applyFilter` case
      (`waiting_on_you` keeps exactly the blocked + in_review ids from the existing
      fixture set); a panel test mirroring `filter "Blocked" narrows...` — click
      `ticket-board-filter-waiting_on_you`, assert blocked and in_review columns each
      count 1 and working counts 0; a mount test with `initialFilter="waiting_on_you"`
      asserting that pill has `aria-checked="true"` and `all` does not.
- [ ] **Surface pass-through** — `app/src/components/TicketBoardSurface.tsx`: add
      `initialFilter?: BoardFilter` to `TicketBoardSurfaceProps` (import the type from
      `TicketBoardPanel`), forward it to `<TicketBoardPanel>`.
- [ ] **App wiring** — in `app/src/App.tsx`:
      - add `boardInitialFilter` state next to `boardSurfaceOpen`; change
        `openBoardSurface` to take `initialFilter?: BoardFilter` as sketched above;
      - compute `waitingOnYouCount` in a `useMemo` over `tickets` (place near the
        `attentionCount` derivation);
      - on the `id: 'board'` entry of `sidebarHeaderActions`: add
        `badge: waitingOnYouCount > 0 ? waitingOnYouCount : undefined` and change
        `onClick: openBoardSurface` to
        `onClick: () => openBoardSurface(waitingOnYouCount > 0 ? 'waiting_on_you' : undefined)`
        — the closure is **required**, not style: `Sidebar` invokes
        `onClick={action.onClick}`, so a bare `openBoardSurface` reference would receive
        the React `MouseEvent` as `initialFilter`. Add `waitingOnYouCount` to the memo
        deps. Mirror the existing `attention` entry's badge
        (`badge: attentionCount > 0 ? attentionCount : undefined`);
      - `onOpenBoard: openBoardSurface` in `useKeyboardShortcuts` is safe unchanged
        (`useShortcut` wraps handlers as `() => handlerRef.current()`), and the ⌘K
        `tickets-board` `run: () => openBoardSurface()` is already a closure — leave both;
      - pass `initialFilter={boardInitialFilter}` to `<TicketBoardSurface>`.
- [ ] **Badge render + click-through test** — new `app/src/App.waitingOnYouBadge.test.tsx`,
      copying the mock scaffold of `App.sessionlessWorkspace.test.tsx` (same `vi.mock`
      set), with two deltas: the `Sidebar` stub renders each `headerActions` entry as a
      button (`data-testid={`tool-${action.id}`}`) with its `badge` as text, and
      `TicketBoardSurface` is **not** mocked (render the real surface + panel). Store
      mock must include `tickets`/`setTickets` (App destructures `tickets`). Cases:
      one blocked + one in_review + one working ticket → board badge text `2`; no
      blocked/in_review tickets → no badge; clicking `tool-board` renders
      `ticket-board-surface` with `ticket-board-filter-waiting_on_you` at
      `aria-checked="true"` and only the blocked + in_review cards visible.
- [ ] **CHANGELOG.md** — one user-facing entry under today's date: the board button now
      shows how many tickets are waiting on you (blocked or in review) and clicking it
      jumps to that filtered view.
- [ ] **Manual check** in the dev app (`make dev`, pre-authorized): with a ticket set to
      `blocked` (`attn ticket status <id> needs_input` from its session, or `attn ticket
      new` + status via the detail panel), confirm the badge appears, click opens the
      board on Waiting on you, and setting the ticket `done` clears the badge.

## Verification

```bash
pnpm --dir app test waitingOnYou TicketBoardPanel   # selector + board filter
pnpm --dir app test                                  # full frontend suite
pnpm --dir app exec tsc --noEmit                     # no typecheck script; build runs tsc
```

## Decisions

(Settled with Victor — not up for re-litigation.)

- **Waiting on you = blocked ∪ in_review**, nothing else. Blocked owes a reply,
  in_review owes a look; todo is backlog, not a debt. Archived tickets excluded by
  construction (store only holds non-archived).
- **Read-only and ambient; badge hidden at zero; no OS notifications in this slice.**
  The counter informs — it never gates, pings, or acts.
- **Zero protocol.** Pure frontend derivation from the `tickets_updated`-fed store; no
  TypeSpec/ProtocolVersion churn for a count the client can compute.
- **The derivation is a shared exported selector** (`app/src/utils/waitingOnYou.ts`),
  not inline in App.tsx — the dashboard-state-card plan (sibling 2026-07-02 plan)
  consumes the same union. This plan owns the selector; that plan imports it.
- **The badge lives on the existing board button** via the already-built
  `SidebarHeaderAction.badge` machinery (attention drawer precedent, `9+` cap included) —
  no new chrome.
- **Pre-filter only from the badged click, only when count > 0** (implementation call):
  ⌘⇧T and ⌘K keep opening the board unfiltered, and a zero-count button click opens the
  ordinary `all` view — the button's old behavior is the default, the queue view is what
  the badge advertises.
- **`waiting_on_you` is a fifth filter pill**, coexisting with the narrower `blocked` /
  `in_review` pills rather than replacing them — those remain useful single-column views.

## Open Questions / Follow-ups

- **Dashboard state card** (sibling plan) imports `waitingOnYouTickets` for its own
  rendering — if its slug/name differs when it lands, keep the import seam, not a copy.
- OS-level surfacing (menu bar / notification) is a possible later slice, explicitly out
  here.
- The status color tokens gotcha (`--color-accent` etc. undefined in App.css) does not
  apply: the badge reuses `.sidebar-tool-badge`, which is styled with `var(--accent)`
  already in production use by the attention badge.
