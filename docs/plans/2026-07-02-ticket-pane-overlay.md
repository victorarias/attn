# Plan: Ticket chip + overlay on the agent pane

## Goal

One-click access to the ticket bound to the session Victor is looking at: a **ticket
chip** on the agent pane header (ticket title + status color + unread-activity dot)
that expands the **existing `TicketDetailPanel`** as an overlay on top of that agent
pane. Click again or Escape closes it and returns focus to the correct terminal.
Why: the reverse channel — steering an agent by editing its ticket
([docs/vision/chief-delegation-awareness.md](../vision/chief-delegation-awareness.md):
"the reverse channel must be as ambient as the forward one") — is underused by its
primary human. Victor almost never answers agents in the ticket because reaching it
is a ⌘K → board → card hop instead of ambient. This is deliberately the smallest
slice that makes steering one click, and it is **also the access-pattern experiment
that gates the separate ticket-rich-content plan** (planned sibling doc,
`docs/plans/2026-07-02-ticket-rich-content.md`): if a one-click editable ticket still
goes unused, richer ticket content isn't the bottleneck.

Frontend-only. All four edits are already covered by existing ws actions (verified
below) — no protocol change, no daemon change.

## Architecture Map

```text
Current (bound-ticket access is a palette hop):
⌘K "Open ticket board" -> TicketBoardSurface (fullscreen, FocusTrap, useEscapeStack)
  -> card click -> closeBoardSurface() + handleOpenTicketDetail(ticketId)
    -> ticketDetail DOCK panel (App.tsx) hosting TicketDetailPanel
       editable because App wires onChangeStatus/onAddComment/onEditDescription
       to useDaemonSocket sendTicketChangeStatus/sendTicketAddComment/
       sendTicketEditDescription, onResume to handleResumeTicket

session -> bound ticket resolution (exists, automation bridge only):
useUiAutomationBridge `ticket_open_via_dashboard`:
  tickets.find(t => t.assignee === sessionId)     // tickets = useDaemonStore().tickets

pane header (SessionTerminalWorkspace/index.tsx renderPaneSurface):
  headerVisible = showPaneHeader (split) || nudgeMode != null
  contents: title, rename button, HeaderNudgeIndicator

Target (bound-ticket access is one click, on the pane itself):
App.tsx (AppContent, everything already in scope)
  -> workspaceSessions entry += ticket: boundTicketForSession(tickets, entry.id)
  -> ticketActions prop = { fetchTicket, onChangeStatus, onAddComment,
                            onEditDescription, onResume } (memoized)
SessionTerminalWorkspace
  -> pane header: <PaneTicketChip ticket unread onToggle/> (only when bound ticket)
     headerVisible ||= paneTicket != null            // header becomes ambient
  -> local state ticketOverlayPaneId: string | null  // one overlay per workspace
  -> overlay INSIDE .workspace-pane-body (absolute inset:0, header stays clickable)
       hosting <TicketDetailPanel isOpen ticketId ticketRow={paneTicket}
                fetchTicket {...ticketActions} onClose={closeTicketOverlay}/>
  -> Escape: useEscapeStack(closeTicketOverlay, open)   // shared LIFO stack
  -> every close path ends in focusActivePane()          // pattern #6, see below

Tests (jsdom, GhosttyTerminal mocked as in the closeFocusedLeaf spec):
PaneTicketChip.test.tsx                    -> chip alone (mirror NudgeIndicator.test.tsx)
SessionTerminalWorkspace.ticketOverlay.test.tsx -> wiring + Escape/focus restore
```

**Focus ownership (AGENTS.md critical pattern #6).** Opening the overlay takes focus
(overlay root gets `tabIndex={-1}` and is focused on open so keystrokes stop reaching
the PTY). Closing must NOT blindly refocus a "main" terminal: the restore path is the
workspace's existing `focusActivePane()` callback (`runtime.focusPane(activePaneId, 0)`
in `SessionTerminalWorkspace/index.tsx`), which focuses through the active
`GhosttyTerminal` handle's `focus()` — the same path the `focusRequestToken` effect
uses. `activePaneId` is the single focus authority (last-focused pane, main or
utility/shell split), so restore lands on whichever terminal actually owned focus.
Every close path — Escape, chip re-click, the panel's ✕, Resume — funnels through one
`closeTicketOverlay` that calls it.

## Data Model / Interfaces

No protocol change. Verified coverage of all four edits with existing machinery:

- status → `sendTicketChangeStatus` (`ticket_change_status` ws cmd, useDaemonSocket)
- comment → `sendTicketAddComment` (`ticket_add_comment`)
- description → `sendTicketEditDescription` (`ticket_edit_description`)
- resume → `handleResumeTicket` (App.tsx) — an **opaque App callback** from this
  plan's point of view. Today it is frontend orchestration over
  `planTicketResume`/`executeTicketResumePlan`; the sibling
  [2026-07-02-ticket-resume-fix.md](2026-07-02-ticket-resume-fix.md) plan replaces
  that implementation with a daemon-owned `ticket_resume` command and deletes
  `app/src/utils/ticketResume.ts`. The injected `onResume: (ticketId) => void`
  contract survives either landing order — if that plan lands first, do NOT
  "restore" `planTicketResume`
- full record → `fetchTicket` (`get_ticket`, correlated via `ticket_result`)
- live refresh → `ticketRow.updated_at` as `refreshKey` inside TicketDetailPanel;
  the bound row comes from `useDaemonStore().tickets`, refreshed by `tickets_updated`

New frontend shapes only:

```ts
// app/src/utils/tickets.ts (new) — single home for the session->ticket rule;
// extracted from useUiAutomationBridge's ticket_open_via_dashboard, which then reuses it.
function boundTicketForSession(tickets: Ticket[], sessionId: string): Ticket | undefined
  // = tickets.find(t => t.assignee === sessionId); store rows are non-archived,
  // created_at desc, so multi-assignment (post-`take`) resolves to the newest.

// SessionTerminalWorkspaceProps additions
workspaceSessions: Array<{ ...existing; ticket?: Ticket }>   // bound board row
ticketActions?: {
  fetchTicket: (ticketId: string) => Promise<Ticket>;
  onChangeStatus: (ticketId: string, status: Ticket['status'], comment?: string) => Promise<void>;
  onAddComment: (ticketId: string, comment: string) => Promise<void>;
  onEditDescription: (ticketId: string, description: string) => Promise<void>;
  onResume: (ticketId: string) => void;
};

// PaneTicketChip props (new component, app/src/components/PaneTicketChip.tsx)
{ ticket: Ticket; unread: boolean; open: boolean; onToggle: () => void }
```

## Boundaries

- **App.tsx (AppContent)** owns the session→ticket lookup and the daemon-facing
  action functions. Everything needed is already in AppContent scope (`tickets` from
  `useDaemonStore()`, the four senders arrive as existing AppContent props, and
  `handleResumeTicket` is defined there) — **no new AppContent prop threading** (the
  4-place gotcha in app/CLAUDE.md does not fire).
- **SessionTerminalWorkspace** owns overlay open state and focus restore — it is the
  pane-focus authority. It never talks to the socket; it calls the injected actions.
- **TicketDetailPanel is unchanged.** It already renders editable exactly when the
  optional handlers are provided; the overlay is a third owner of the same contract
  (dock panel, automation bridge, now the pane overlay).
- Spine: the overlay adds **no new write primitive** — it reuses the same chief/user
  actions the dock panel wires. attn still authors only `crashed`; the board (and
  this chip) inform, never gate.

## Implementation Steps

- [ ] **`boundTicketForSession` helper** — new `app/src/utils/tickets.ts`; replace the
      inline `tickets.find(t => t.assignee === sessionId)` in
      `useUiAutomationBridge.ts` (`ticket_open_via_dashboard`) with it. No dedicated
      test (a one-line find; behavior is covered by the workspace spec below).
- [ ] **`PaneTicketChip`** — new `app/src/components/PaneTicketChip.tsx` + `.css`,
      mirroring `HeaderNudgeIndicator` in `NudgeIndicator.tsx`: a button rendered
      inside `.workspace-pane-header`, `onPointerDown={e => e.stopPropagation()}`
      (same leaf-drag guard as the nudge/rename buttons), `onClick` stops propagation
      and calls `onToggle` (mirror `triggerHandler`). Contents: status dot + truncated
      title + unread dot when `unread` (drive color via `data-status={ticket.status}`,
      copying the `--tb-tint` pattern from `TicketBoardPanel.css` — only DEFINED
      tokens or the documented `#ef4444` fallback, never bare `--color-accent` /
      `--color-error`, which are undefined in App.css). `data-testid`
      `ticket-chip-<sessionId>`.
- [ ] **Workspace wiring** (`SessionTerminalWorkspace/index.tsx`):
      - extend the `workspaceSessions` entry type with `ticket?: Ticket` and add the
        `ticketActions` prop group;
      - in `renderPaneSurface`, resolve `paneTicket = paneSession?.ticket`; extend the
        header rule to `headerVisible = showPaneHeader || nudgeMode != null ||
        paneTicket != null` and update its comment (the nudge precedent, now ambient
        for bound-ticket sessions); render the chip next to `HeaderNudgeIndicator`;
      - add `const [ticketOverlayPaneId, setTicketOverlayPaneId] = useState<string |
        null>(null)` plus a cleanup effect that clears it when the pane leaves
        `paneIds` (mirror the `maximizedPaneId` cleanup effect);
      - render the overlay inside `.workspace-pane-body` when
        `ticketOverlayPaneId === agentPane.id && paneTicket && ticketActions`:
        a `div.workspace-pane-ticket-overlay` (`data-testid`
        `ticket-overlay-<paneId>`, `tabIndex={-1}`, focused on open via ref callback
        with `preventScroll`) hosting `TicketDetailPanel` with `ticketId={paneTicket.id}`,
        `ticketRow={paneTicket}`, the `ticketActions` handlers, and
        `onClose={closeTicketOverlay}`; `onResume` wraps: close overlay, then
        `ticketActions.onResume(ticketId)` (App's `handleResumeTicket` also clears the
        dock-panel state — harmless no-op here);
      - one `closeTicketOverlay` callback = `setTicketOverlayPaneId(null)` +
        `focusActivePane()`; register `useEscapeStack(closeTicketOverlay,
        ticketOverlayPaneId !== null)`;
      - CSS in `SessionTerminalWorkspace.css`: `.workspace-pane-body { position:
        relative }` (verify it isn't already), overlay `position:absolute; inset:0;`
        z-index above the terminal canvas, `background: var(--color-bg-panel)`.
- [ ] **App.tsx wiring**: in the `workspaceViews.map` render where `workspaceSessions`
      entries are built (next to `ticketUnread: entry.ticketUnread`), add
      `ticket: boundTicketForSession(tickets, entry.id)`; pass a memoized
      `ticketActions` object (`fetchTicket`, `sendTicketChangeStatus`,
      `sendTicketAddComment`, `sendTicketEditDescription`, `handleResumeTicket`).
      Do **not** add the overlay to `blockingOverlayOpen` — it is pane-scoped, not
      app-modal.
- [ ] **Tests** (jsdom; mock `GhosttyTerminal` exactly as
      `SessionTerminalWorkspace.closeFocusedLeaf.test.tsx` does, but expose an
      imperative handle whose `focus()` records the call and returns `true` so
      `runtime.focusPane` resolves on the first attempt; call
      `_resetEscapeStackForTest()` in teardown):
      - `PaneTicketChip.test.tsx` (mirror the `HeaderNudgeIndicator` describe block in
        `NudgeIndicator.test.tsx`): renders title + `data-status`; unread dot iff
        `unread`; click fires `onToggle` without bubbling to the pane.
      - `SessionTerminalWorkspace.ticketOverlay.test.tsx` (mirror the closeFocusedLeaf
        fixture/render helper): chip renders for a bound session and not for an
        unbound one; header is visible on a single-pane workspace when the session has
        a bound ticket; chip click opens the overlay (`ticket-detail-panel` present,
        `fetchTicket` called once with the ticket id); changing the status select
        calls `onChangeStatus` (wiring proof — the panel's own edit flows stay covered
        by `TicketDetailPanel.test.tsx`, don't duplicate them); Escape closes the
        overlay and the mock terminal handle's `focus()` was called (focus restore);
        chip re-click and the panel ✕ also close and restore focus.
- [ ] **CHANGELOG.md**: one user-facing entry (ticket chip on delegated agent panes;
      click to open and edit the ticket in place; Escape returns to the terminal).

## Verification

```bash
pnpm --dir app test PaneTicketChip SessionTerminalWorkspace TicketDetailPanel
pnpm --dir app test               # full suite
pnpm --dir app run build          # tsc gate
```

Manual (dev profile — no automated focus-ownership spec exists, per AGENTS.md):
`make dev`, delegate a session from the chief, confirm the chip appears on the
delegated pane (and not on the chief), open → change status / add a comment →
`attn ticket inbox` on the agent sees it; Escape → typing lands in the terminal
without an extra click, including with a utility split focused.

## Decisions

(Settled with Victor — capture, don't re-litigate.)

- **Ship this slice alone first.** It is the experiment for whether one-click access
  changes ticket-answering behavior; the ticket-rich-content plan is gated on what
  this shows.
- **Frontend-only.** Verified: status/comment/description ride the existing
  `ticket_change_status`/`ticket_add_comment`/`ticket_edit_description` ws actions,
  and resume is whatever App's `handleResumeTicket` does (frontend orchestration
  today; a daemon `ticket_resume` command once ticket-resume-fix lands) — this
  plan itself makes no protocol change and needs no ProtocolVersion bump either
  way.
- **Chip appears only on sessions with a bound ticket** (`assignee === sessionId`,
  the same rule as the bridge's `ticket_open_via_dashboard`). No status filter: a
  done/failed ticket keeps its chip (with its status color) while the row is
  non-archived; the chief has no chip (it is never the assignee of its delegations).
- **Reuse `TicketDetailPanel` as-is** — the editable-via-optional-props contract was
  built for exactly this; no fork, no new panel.
- **The overlay is pane-scoped and non-modal**: no FocusTrap (unlike
  `TicketBoardSurface`), not part of `blockingOverlayOpen`, clicking another pane
  while it is open is allowed. Escape rides the shared `useEscapeStack` (LIFO keeps
  nesting with the board surface correct). One overlay per workspace at a time.
- **The pane header becomes permanently visible for bound-ticket sessions** (extends
  the existing nudge rule `headerVisible = showPaneHeader || nudgeMode != null`).
  Ambient means always there — a chip that only exists in splits isn't ambient.
- **Focus restore = `focusActivePane()`** (critical pattern #6): via the active
  `GhosttyTerminal` handle, honoring `activePaneId` — never a blind main-terminal
  `focus()`.

## Open Questions / Follow-ups

- **Packaged-app scenario is a follow-up, not in scope** (per brief). When it lands,
  add automation-bridge ops for the chip/overlay next to `ticket_open_via_dashboard`.
- **⌘W while focused inside the overlay** closes the pane/session today (DOM
  `terminal.close` → `handleCloseFocusedLeaf`, and in the packaged app the native
  "Close Pane" menu item claims ⌘W separately). Acceptable for this slice — the
  session-close prompt guards it. If it grates, fix BOTH handlers (the
  `data-pane-kind="tile"` detection precedent), never just the DOM one.
- **Escape drops an in-progress draft** (comment/description) — same behavior as the
  board surface today; revisit only if it bites.
- **Chip + `HeaderNudgeIndicator` coexist** in one header (dot on the chip, nudge
  chip beside it). If the header gets crowded, fold the nudge affordance into the
  ticket chip once the experiment validates the chip earns its place.
