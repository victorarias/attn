# Plan: the app's ticket surfaces become garden surfaces

The garden-era epic's wire-moving step. Written 2026-08-18, after
[delegation reporting on seeds](2026-08-18-delegation-reporting-on-seeds.md)
landed: a delegation now binds a seed and reports to it as well as to its
ticket, which is what makes this swap safe to make.

Authority: [the garden-era epic](2026-08-14-garden-era-epic.md), checklist
entry "App ticket surfaces → garden surfaces; this step moves the wire and
carries the ProtocolVersion bump in every lockstep spot."

## What changes

Every app surface that showed or acted on a ticket now shows a seed. The
ticket board, the ticket detail panel and the pane-header ticket chip go;
the garden panel, a new fullscreen garden surface, the seed tile and a
pane-header seed chip take their place. The protocol messages those
surfaces rode go with them.

| ticket surface | garden surface |
| --- | --- |
| `TicketBoardSurface` / `TicketBoardPanel` (⌘⇧T) | `GardenSurface` over the existing `GardenPanel` (⌘⇧T) |
| `TicketDetailPanel` dock panel | the garden panel's read-only drill, and the seed tile for reading and annotating |
| `PaneTicketChip` + the in-pane ticket overlay | `PaneSeedChip`, which opens the seed's tile |
| ticket board font scale | garden font scale, same setting |

The pane chip is the one place the app has to know which unit of work a
session reports to. Tickets answered it with `assignee == session`; the
garden's answer is the dispatch record, so the session's bound seed reaches
the app as a typed field rather than being inferred from a tender that a
crown dispatch never sets.

## The wire

ProtocolVersion 260 → 261.

Added:

- `Session.seed_id` — the seed a session reports to, read from its dispatch
  record once per broadcast the way `crew_member` reads the roster.
- `seed_resume` / `seed_resume_result` — reopen the agent that tends a seed.
  It is the way back to a delegate whose session is gone, which the ticket
  detail panel's Resume was; without it, removing that panel would be a
  one-way door.

Removed — the ticket protocol surface the app consumed, and nothing else:

- `initial_state.tickets`, `tickets_updated`, and the `TicketRow` model
- `get_ticket` / `ticket_result`
- `ticket_change_status`, `ticket_add_comment`, `ticket_edit_description`,
  and their shared `ticket_action_result`
- `ticket_resume` / `ticket_resume_result`

Every agent-facing ticket command stays exactly as it is: `ticket_list`,
`ticket_show`, `ticket_inbox`, `set_ticket_status`, `ticket_create`,
`ticket_comment`, `ticket_subscribe`, `ticket_unsubscribe`, `ticket_take`
and `ticket_attach`. So does the whole CLI. Retiring those is the signpost
step's business, not this one.

The six `ticket.*` facts keep being published and lose their projection —
no client reads a board any more. They move to `factsWithoutWire` rather
than disappearing: they are the durable record behind the read verbs, and
an app may subscribe to them.

## In-flight delegations

A delegation dispatched before this ships keeps its ticket and keeps
reporting to it: the CLI, the hooks, the nudge countdown and the unread
indicator are untouched. What it loses is a ticket-shaped *view* in the
app — and it has a seed to be seen through, because every delegation has
bound one since #946. `Session.ticket_unread` and `nudge_fires_at` stay on
the wire for exactly that reason: the doorbell is how an in-flight
delegation is still reached.

## Resume

`seed_resume` resolves the seed's tender session, reads that session's
dispatch record for the directory and agent it was launched with, and runs
the same composite `ticket_resume` ran — register the workspace, add the
pane, spawn with the resume picker. The mirrored resume id is already keyed
by session id, so a resumed delegate picks up its own conversation exactly
as it did through the ticket.

An untended seed refuses by name, the way `attn agent msg` at a seed does:
there is nobody to reopen.

## What else moved with the surfaces

- The automations panel's run rows navigated to a run's ticket first and its
  session second. With no ticket surface to open, they navigate to the
  session; a run naming no session is not navigable.
- The packaged-app harness dropped the two scenarios that drove the ticket
  board and its detail panel. `real-app:scenario-garden-seed-reopen` replaces
  the resume one: it delegates, reads the pane's seed chip, opens the seed as
  a tile, closes the tender, and reopens it from the panel drill. The two
  scenarios that only *read* the board through the bridge now read it through
  `attn ticket list --json`, which is where the board lives now.
- The apps SDK still promises `currentState.tickets`. It is served by a
  daemon-local row (`internal/daemon/current_state.go`) rather than by the
  wire model, so an app that reads a board keeps working.

## Decisions

- No kanban columns for seeds. The ticket board's shape was its statuses;
  the garden's shape is plots, and the panel already renders them. A
  fullscreen garden is the same panel with room, not a second design.
- The pane chip opens the tile rather than an in-pane overlay. Ruling A
  gives the seed one annotated surface, and an overlay would be a second.
- `seed_id` is decorated at broadcast, not stored on the session row. The
  dispatch record stays the single answer to "what does this session report
  to"; a copy on the session could disagree with it.
