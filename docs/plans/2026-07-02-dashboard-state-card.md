# Plan: Deterministic "state of the world" card on the home dashboard

## Goal

Give Victor a **return digest**: he comes back after hours away and the home
Dashboard (`app/src/components/Dashboard.tsx`) opens on one coherent picture of
delegated work — **named tickets, not counts** — without prompting the chief.
Three sections, in this order: **Waiting on you** (blocked + in_review, title +
status + age) first, **In flight** (working), **Closed today** (done/failed/crashed
closed today). Clicking a ticket opens its detail panel. Settled with Victor: this
is **tied to the harness — deterministic from the tickets store; no LLM, no chief
turn, always current**. The chief's narrative version (the WHY behind the rows)
stays available on demand and is guidance work owned by the chief-guidance-brief-craft
plan (`hooks.ChiefGuidance` in `internal/hooks/hooks.go` is that seam). Spine:
awareness, not autonomy — the card informs and navigates; it mutates nothing.

## Architecture Map

```text
Current:
daemon tickets_updated broadcast
  -> useDaemonSocket -> useDaemonStore().tickets   (non-archived, created_at desc — internal/store ListTickets ORDER BY)
    -> App.tsx (AppContent, `const { ..., tickets } = useDaemonStore()`)
      -> TicketBoardSurface (fullscreen overlay, ⇧⌘T board.open / ⌘K action)
        -> TicketBoardPanel cards -> onOpenTicket -> handleOpenTicketDetail
          -> ticketDetail dock panel (TicketDetailPanel) — rendered INSIDE the
             session view-container (z-index-hidden when view === 'dashboard')
Dashboard: sessions + chief + PRs cards only. No ticket surface on the home view.

Target:
useDaemonStore().tickets ──(new prop)──> Dashboard ──> StateOfTheWorldCard   (new, pure)
  sections (pure functions over props, computed in render):
    Waiting on you  = waitingOnYouTickets(tickets)           [selector OWNED by the
                                                              waiting-on-you-counter plan — imported, never redefined]
    In flight       = tickets.filter(status === 'working')   [store order preserved;
                                                              optional session-state chip seam for ticket-went-quiet plan]
    Closed today    = applyFilter(tickets, 'closed_today')   [imported from TicketBoardPanel — same predicate as the board filter]
  row click  -> onOpenTicket(id) ──> App.handleRevealTicketDetail(id):
                 setView('session') + setSelectedTicketId + openDockPanel('ticketDetail')
  header btn -> onOpenBoard ──> App.openBoardSurface

Tests:
StateOfTheWorldCard.test.tsx  -> render card directly with fixture tickets
                                 (mirror makeTicket() in TicketBoardPanel.test.tsx)
Dashboard.test.tsx            -> existing prop-driven pattern (no store mock);
                                 pass tickets + onOpenTicket as props
```

Why the click-through must switch views: the `ticketDetail` dock panel host lives
inside the session `view-container` in `App.tsx`; when `view === 'dashboard'` that
container is `z-index: 0; pointer-events: none` (`.view-container.hidden` in
`App.css`). Opening the panel without `setView('session')` opens it invisibly.
This is a **latent bug today** for the board surface opened over the dashboard
(⇧⌘T works there via `useKeyboardShortcuts` `onOpenBoard`); routing the surface's
`onOpenTicket` through the same new handler fixes it in one line.

## Data Model / Interfaces

No protocol, store, or daemon changes. Frontend-only; `tickets_updated` and
session broadcasts already carry everything.

```ts
// app/src/components/StateOfTheWorldCard.tsx (new)
interface StateOfTheWorldCardProps {
  /** useDaemonStore().tickets — non-archived, created_at desc. Read-only. */
  tickets: Ticket[];
  /** App.handleRevealTicketDetail: setView('session') + open ticketDetail dock. */
  onOpenTicket: (ticketId: string) => void;
  /** App.openBoardSurface — header "Board" affordance. */
  onOpenBoard?: () => void;
  /**
   * SEAM for the ticket-went-quiet plan: projects a working ticket's bound
   * session (ticket.assignee IS the session id — createDelegatedTicket) to a
   * runtime state for an In-flight chip. Absent, or undefined for a ticket,
   * => no chip renders. This plan leaves it UNWIRED in App.tsx.
   */
  sessionStateForTicket?: (ticket: Ticket) => UISessionState | undefined;
  /** Injectable clock for deterministic tests (mirrors relativeTime/isSameLocalDay). */
  now?: Date;
}
```

Reused, not copied (all already exported from `TicketBoardPanel.tsx`):
`applyFilter`, `isSameLocalDay`, `relativeTime`, `STATUS_LABEL`. Closed-today
semantics are exactly the board's: `isSameLocalDay(t.closed_at)` — the store
stamps `closed_at` entering a terminal status and **clears it on reopen**
(`internal/store/tickets.go`, `UpdateTicketStatus`), so reopened tickets drop out
for free.

Row shape per section:
- **Waiting on you**: title + `STATUS_LABEL[status]` chip + `relativeTime(updated_at)`
  (how long it has been sitting).
- **In flight**: title + `relativeTime(updated_at)` + optional session-state chip
  (render `StateIndicator` with the projected state, `size="sm"`, as the Sessions
  card rows do — only when `sessionStateForTicket?.(ticket)` returns a value).
- **Closed today**: title + `STATUS_LABEL[status]` chip + `relativeTime(closed_at)`,
  sorted `closed_at` desc; failed/crashed rows carry `data-terminal="bad"`
  (mirror the board's Closed sub-lane styling hook).

## Boundaries

- `StateOfTheWorldCard` is a pure render over props: no store reads, no daemon
  calls, no timers, no mutations. Its only outputs are the two navigation
  callbacks. The board stays the full drill-in surface; the card is the digest.
- It does **not** define waiting-on-you. It imports `waitingOnYouTickets` from
  `app/src/utils/waitingOnYou.ts`, the shared module the waiting-on-you-counter
  plan owns, and renders the returned order verbatim — one definition of
  "waiting on you" across counter + card.
- It does **not** detect "went quiet". The ticket-went-quiet plan owns that
  projection; this card only exposes the optional `sessionStateForTicket` prop.
- It does **not** narrate. The deterministic card is the "what"; the on-demand
  chief narrative (the "why") is the chief-guidance-brief-craft plan's territory
  (`hooks.ChiefGuidance`).
- attn authors nothing here: the card displays statuses agents/humans authored
  (plus attn's one `crashed`). The board informs, never gates — no status
  mutation affordances on the card.
- `App.tsx` owns view/dock orchestration (`handleRevealTicketDetail`); the card
  and Dashboard never touch `setView`/`openDockPanel` directly.

## Implementation Steps

- [ ] **`app/src/components/StateOfTheWorldCard.tsx` + `.css` (new).** Card shell
      reuses the existing `dashboard-card` / `card-header` / `card-body scrollable`
      classes (mirror the Sessions card markup in `Dashboard.tsx`); header
      `<h2>State of the world</h2>` plus a `card-action` button "Board" calling
      `onOpenBoard` (mirror the Sessions card's `+ New` button). Sections computed
      in render: `waitingOnYouTickets(tickets)`,
      `tickets.filter((t) => t.status === 'working')`,
      `applyFilter(tickets, 'closed_today', now)` sorted `closed_at` desc. An empty
      section is omitted entirely; when all three are empty render one quiet
      zero-state line ("All quiet — nothing waiting on you.",
      testid `sow-empty`). Rows are `<button type="button">` (as `BoardCard` is)
      calling `onOpenTicket(ticket.id)`, with a descriptive `aria-label` (mirror
      `BoardCard`'s). Testids: `state-of-world-card`, `sow-section-waiting`,
      `sow-section-inflight`, `sow-section-closed`, `sow-row` (+ `data-ticket-id`,
      `data-status`), `sow-empty`. CSS prefix `sow-`; status-chip colors must use
      fallbacks, not bare `var(--color-accent)` etc. (those tokens are undefined —
      see TicketDetailPanel.css gotcha).
- [ ] **Dashboard wiring.** Add required props `tickets: Ticket[]`,
      `onOpenTicket: (id: string) => void` and optional `onOpenBoard?: () => void`
      to `DashboardProps`; render `<StateOfTheWorldCard …/>` as the **first** card
      in `.dashboard-grid` (grid is `auto-fit minmax(300px, 1fr)` — a fourth card
      just flows). Update the 6 existing `render(<Dashboard …/>)` calls in
      `Dashboard.test.tsx` with `tickets={[]}` + `onOpenTicket={vi.fn()}`
      (mechanical).
- [ ] **App.tsx.** Next to `handleOpenTicketDetail`, add
      `handleRevealTicketDetail = useCallback((ticketId) => { setView('session'); setSelectedTicketId(ticketId); openDockPanel('ticketDetail'); }, …)`.
      Pass to `<Dashboard tickets={tickets} onOpenTicket={handleRevealTicketDetail} onOpenBoard={() => openBoardSurface()} …/>`
      (tickets is already destructured from `useDaemonStore()` in AppContent — no
      socket-prop plumbing, the two-component gotcha does not apply). The
      `onOpenBoard` closure is **required**, not style: the waiting-on-you-counter
      plan changes `openBoardSurface` to take `initialFilter?: BoardFilter`, so a
      bare reference would let the card button's React `MouseEvent` land in
      `initialFilter` and seed the board filter with garbage — the same trap that
      plan documents for the Sidebar entry. Also switch
      the `<TicketBoardSurface onOpenTicket>` inline callback from
      `handleOpenTicketDetail` to `handleRevealTicketDetail` (keeping its
      `closeBoardSurface()`), fixing the invisible-detail gap when the board is
      opened over the dashboard/grid views.
- [ ] **Selector dependency.** Import `waitingOnYouTickets` from
      `app/src/utils/waitingOnYou.ts` — the module the waiting-on-you-counter
      plan (`docs/plans/2026-07-02-waiting-on-you-counter.md`) creates. If that
      module does not exist yet when executing, STOP and coordinate — do not
      redefine the selector here (settled: that plan owns it).
- [ ] **`app/src/components/StateOfTheWorldCard.test.tsx` (new).** Copy the
      `makeTicket()` fixture from `TicketBoardPanel.test.tsx`; use noon-UTC
      timestamps for day-boundary fixtures (same local-day-flip gotcha documented
      in that file's `isSameLocalDay` test) and pass an explicit `now`. Tests, by
      name:
      - `renders named ticket titles grouped into the right sections` (blocked +
        in_review → waiting; working → in flight; done/failed/crashed closed
        today → closed; todo appears nowhere)
      - `waiting-on-you section renders first` (DOM order of the section testids)
      - `waiting rows show status label and age` (asserts `Blocked` / `In review`
        text + `relativeTime` output with injected now)
      - `closed today excludes tickets closed on a previous day and labels
        failed/crashed` (asserts `data-terminal="bad"` on the crashed row)
      - `clicking a row calls onOpenTicket exactly once with the ticket id`
        (mirror the board's click-through test)
      - `empty sections are omitted while populated ones render`
      - `all-empty renders the quiet zero-state and no sections`
      - `session-state chip renders only when the projection helper is provided
        and returns a state` (absent prop → no chip; helper returning
        `'working'` → chip; helper returning undefined → no chip)
      - `Board button calls onOpenBoard with no arguments` (assert the mock was
        called with zero args — guards the MouseEvent-into-`initialFilter` trap
        named in the App wiring step)
- [ ] **`Dashboard.test.tsx` addition.** One integration-lite test: rendering
      Dashboard with a blocked fixture ticket shows the card and clicking the row
      forwards the id to `onOpenTicket` (guards the prop plumbing).
- [ ] **CHANGELOG.md.** One user-facing entry: the home dashboard now opens with
      a "state of the world" card — what's waiting on you, what's in flight, and
      what closed today, each ticket clickable through to its detail.

## Verification

```bash
pnpm --dir app test StateOfTheWorldCard Dashboard TicketBoardPanel
pnpm --dir app test                    # full frontend suite
pnpm --dir app exec tsc --noEmit      # type check (build script's tsc step)
```

Manual (dev profile — pre-authorized): `make dev`; `./attn profile-env --fish dev | source`;
seed a few tickets with `attn ticket new --title …`, open the board (⇧⌘T), open
each ticket's detail and change statuses to blocked / in_review / working / done
via the panel's status control (`onChangeStatus` is wired in-app); return to the
home dashboard and confirm the three named sections, ordering, ages, and that
clicking a row lands on the visible detail panel (view switches to session).
Also confirm the previously-broken path: from the dashboard, ⇧⌘T → click a board
card → detail is visible, not hidden behind the dashboard.

## Decisions

- **Deterministic from the store — no LLM, no chief turn** (settled, Victor). The
  return digest must be always-current and free: pure selectors over
  `useDaemonStore().tickets`. The chief narrative stays on demand, owned by the
  chief-guidance-brief-craft plan.
- **Named lists, not counts; Waiting on you first** (settled, Victor). The digest
  answers "what needs me" before "what's moving".
- **Reuse the board's closed-today logic by import** (settled, Victor + Core rule
  against copying production code): `applyFilter(tickets, 'closed_today', now)` /
  `isSameLocalDay` from `TicketBoardPanel.tsx` — one definition of "closed today".
- **The waiting-on-you selector is imported, never redefined here** (settled,
  Victor): the waiting-on-you-counter plan owns `waitingOnYouTickets`; the card
  renders its order verbatim so counter and card can never disagree.
- **Quiet chip = optional projection prop** (settled, Victor): the
  ticket-went-quiet plan owns quiet detection; this card ships the
  `sessionStateForTicket` seam unwired and renders session-state info only when
  the helper exists — graceful degradation if that plan hasn't landed. We
  deliberately do NOT wire an interim assignee→session-state mapping from the
  Dashboard's existing `sessions` prop; that would duplicate the projection that
  plan will own.
- **Empty state: the card always renders, with a quiet zero-state** (recorded per
  brief). A card that disappears makes "nothing needs you" — the best possible
  digest — indistinguishable from "the card is broken / I'm looking in the wrong
  place". Empty *sections* are omitted to keep the quiet state quiet.
- **Click-through switches to the session view** (`handleRevealTicketDetail`):
  forced by the dock architecture — the ticketDetail panel host lives inside the
  session view-container, which is z-index-hidden on the dashboard. The board
  surface's `onOpenTicket` is routed through the same handler, folding in the
  one-line fix for the existing invisible-detail gap.
- **Props-driven card, no store read inside** — mirrors how Dashboard and the
  board panel are built and keeps tests fixture-based with zero store mocks.
- **No live age ticker.** Ages are computed at render from `now`; re-renders ride
  `tickets_updated` broadcasts — identical to the board's `relativeTime` behavior.
  A 30s stale age on an untouched dashboard is acceptable for a digest.
- **Todo and older-closed tickets are excluded.** The digest is about the return
  window; the backlog and history belong to the board (one click away via the
  header Board button).

## Open Questions / Follow-ups

- **Execution-order coupling:** the waiting-on-you-counter plan
  (`docs/plans/2026-07-02-waiting-on-you-counter.md`) specifies
  `app/src/utils/waitingOnYou.ts` exporting
  `waitingOnYouTickets(tickets: Ticket[]): Ticket[]` (plus `isWaitingOnYou` and
  `WAITING_ON_YOU_STATUSES`); the module is not in the tree yet, so verify it
  landed before starting — and STOP and coordinate if it hasn't.
- The App.tsx wiring of `sessionStateForTicket` is **owned by the
  ticket-went-quiet plan's slice 3** (its App.tsx step already passes
  `daemonSessions`; when this card exists it also passes
  `assigneeSessionState`-based `sessionStateForTicket` — one line). This plan
  ships the prop unwired and does nothing further; noted here for
  discoverability only.
- Long lists: `card-body scrollable` handles overflow, but if In-flight regularly
  exceeds ~10 rows consider a "+N more" collapse — defer until it hurts.
- A "brief me" affordance on the card (ask the chief for the narrative behind the
  rows) — belongs to the chief-guidance-brief-craft plan's surface work, not here.
