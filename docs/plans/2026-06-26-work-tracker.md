# Tickets — attn as an agent work tracker

> North star: `docs/vision/chief-delegation-awareness.md` — read that for the *why*.
> This doc is the *what*: the domain model and surfaces. It **supersedes and folds in**
> `docs/plans/2026-06-25-communication-gateway.md` — the gateway's delivery mechanics
> now live here as the **event-driven activity + notification** layer of the tracker.

## The reframe

The chief delegates work and watches its state come back. That is an **issue tracker**
— Linear / Jira / Asana — with one twist: the assignees are autonomous agents that
**report their own status**, and the chief is *notified the instant a ticket moves*
instead of polling a board.

attn already has the bones. `DispatchWorkState` today (`in_progress / needs_input /
ready_for_review / completed / failed`) is an issue-status enum, and the session list
is a proto-board. We **promote** what exists — lift `work_state` to a first-class
status, lift the session list to the board — rather than build a tracker from scratch.

**Borrow Linear's restraint, not Jira's machinery.** The model is a handful of fields.
No sprints, no workflows, no custom fields, no required transitions. The board
*informs*; it never *gates*. (Spine: awareness, not autonomy.)

> *Naming:* the unit is a **ticket** — "item", "card", and "issue" each collide with
> other concepts in attn; "ticket" is unambiguous.

## The ticket

A **durable ticket**, independent of any session:

| field | meaning |
|---|---|
| `id` | human-friendly handle (see **Ticket ids** below) |
| `title` | a short label — what shows on the board (e.g. "Migrate store to X") |
| `description` | the full brief — **this is the delegation prompt** when the ticket is handed to an agent |
| `status` | the column (below) |
| `assignee` | the agent on it, **or you**, or unassigned |
| `activity[]` | the history thread — **status changes + comments** (see below) |
| `attachments[]` | files handed over with the work — **handover artifacts** (see below) |
| `cwd`, `last_agent_id` | the last session's working dir + agent id, for **resume** |
| `project_id` | *future* grouping — nullable, **not built now** |

The ticket exists on its own. It can sit in a backlog unassigned, be delegated to an
agent, be reassigned, or be resumed — all keeping **one identity**.

### Ticket ids

Human-friendly **memorable slugs**, not opaque uuids — **the creator (usually the
agent) names the ticket from the work**, so the handle is descriptive:
`store-migration`, `notebook-scroll-fix`. Speakable, typeable, distinctive ("resume
`store-migration`"), and no project prefix needed — it works today, before projects
exist.

- **Uniqueness check at creation** — slugs are unique.
- **On collision**, creation **fails with a clear error** — *"that id is already in use:
  pick a new name, or append a random number"* (e.g. `store-migration-318`). The agent
  reads the error and decides which fits.

(Ordering isn't encoded in the id; `created_at` + the board's status grouping cover it.)

### Activity: status changes + comments (no separate "report")

There is no distinct "report" type — that was old dispatch vocabulary. Activity is just
two things:

- a **status change** — the ticket moved column (may carry a comment, e.g. → Blocked
  with "need a decision on X");
- a **comment** — a freeform note from anyone (you or the agent).

What used to be an agent's "report" is simply *a status change with a comment*. One
concept, authored by either side.

### Attachments (handover files)

A ticket can carry **attachments** — files handed over with the work: a diff, a results
bundle, a screenshot, the large-artifact handoff that today goes through
`dispatch handoff`. They live on the ticket, show in the ticket view, and travel with it
across resume and reassignment. The existing handoff machinery becomes "**attach a file
to a ticket**" — one operation, no separate handoff verb.

## Statuses (the columns)

```
Todo  →  Working  ⇄  Blocked          ( backlog → in flight, may park on you )
            │
            ▼
        In Review  →  Done             ( done-pending-your-look → closed )

Failed     Crashed                     ( terminal-bad )
```

| status | meaning | who sets it |
|---|---|---|
| **Todo** | backlog — created, not started | chief / you |
| **Working** | actively being worked | the agent (on pickup) |
| **Blocked** | paused, **owes you a reply** | the agent |
| **In Review** | done, awaiting your look/approval | the agent |
| **Done** | closed, worked | the agent / you |
| **Failed** | finished, didn't work | the agent |
| **Crashed** | died without reporting | **attn** — the one transition a dead worker can't make itself |

## Ticket ↔ session

A ticket has **0-or-1 active session**. The binding happens three ways:

- **Delegate** a ticket → opens an agent session with the **description as the prompt**;
  the ticket binds to it. **Delegated sessions always have a ticket** — the chief can't
  track them otherwise.
- **Bare sessions are fine.** You can open a session with no ticket.
  `Cmd+K → assign ticket to session` attaches a ticket to an already-open session.
- **Resume** a closed ticket → a **button in the ticket view** (below) that reopens the
  agent bound to this ticket.

From inside a session, `Cmd+K → open ticket` jumps to its ticket.

## The ticket view (your side of surface b)

Reachable from the board, or `Cmd+K → open ticket` inside a session. Shows
**title, description, status, history, attachments**. You can:

- **edit the description**
- **add a comment**
- **change the status**
- **add / open attachments**
- **Resume** — a button that reopens the agent for this ticket (reload from `cwd` +
  `last_agent_id`, **bound to the same ticket**) and **closes the ticket view**, landing
  you in the live session. Same ticket identity across the resume.

Every edit (description / comment / status / attachment) is an **activity entry** that
**emits an event** (below) — so editing the ticket *is* how you steer the agent. There is
no separate mailbox.

## The board (surface b)

The session list grows into the board: tickets grouped by status column, with the
**Todo backlog** included. Filter "what's blocked / in review / what closed today."
This is the chief's at-a-glance awareness surface.

## Notifications — event-driven

A ticket is a **shared object**, and notification is modeled as an **event-driven
system**, not hard-wired "forward/reverse" calls. Three decoupled layers:

**1. Mutations emit events.** Every ticket change emits a domain event and the mutator
knows *nothing* about who's listening:

- `TicketCreated`, `TicketStatusChanged` (from→to), `TicketCommented`,
  `TicketAssigned`, `TicketDescriptionEdited`, `TicketAttachmentAdded`.

**2. Observers subscribe** to events for tickets they care about. Identity is uniform
— **you, the chief, and each agent are all just identities** (the chief is one of the
agents). An identity is *involved* with a ticket when it is assigned to it or has
authored an event on it:

- the **chief** observes events on the tickets it delegated / owns (it authored their
  `created` event, so it needs no special "sees everything" scope);
- an **assignee agent** observes events on its ticket that it *didn't author* (the
  chief's comments, status changes, re-briefs).

Each identity keeps its **own cursor per ticket**, not one global cursor. So a ticket
newly assigned to an agent — or reassigned to it — arrives with its **full history**
(the brief and prior steers), and an agent's progress on one ticket never advances its
bookmark on another it hasn't looked at.

**3. Decoupled handlers** turn an event into a notification — adding a channel is adding
a handler, never touching the mutators:

- **Claude-watch handler** — the observer armed a Monitor; its `watch` returns the
  event as it lands. Best experience, no injection.
- **Codex-nudge handler** — the observer has no self-monitor and is idle; attn
  **pty-nudges** it to go read its ticket. **Content never enters the PTY.**

attn's session-close detection is *just another event producer*: on a close without a
terminal status it emits `TicketStatusChanged → Crashed`. No special path.

The gateway's settled mechanics map onto the event log:

| gateway mechanic | here |
|---|---|
| dedup by content | idempotent events (no double-emitted transition/comment) |
| ack = output; consume returns pending | **unread** — events past the identity's per-(identity, ticket) cursor |
| bundle by sender | a consume groups pending events **by ticket** |
| hardcoded chief id | the chief is a well-known observer (literal constant) |
| two consumers (watch / nudge) | the two decoupled handlers above |

## Lifetime

- **Open tickets are durable.** Todo / Working / Blocked / In Review never expire — the
  backlog and everything in flight persist until you act on them.
- **Closed tickets are cleared from the active board** — either you **archive** one, or
  it's **auto-removed after a 30-day TTL** (Done / Failed / Crashed). A backlog is never
  garbage; only settled work ages out.

## API surface — retire `dispatch`, expose `ticket`

The whole `dispatch` CLI/IPC namespace is **replaced**, not extended. Clearing it is a
completion requirement — leaving `inbox`, `report`, `message`, etc. alongside the ticket
model is an incomplete migration, not an acceptable end state.

**Old → new (every verb has a home):**

| retired `dispatch …` | replacement |
|---|---|
| `watch <id>` | `ticket watch <id>` — the event-driven observer |
| `report --done/--failed/--blocked/…` | `ticket status <id> <status>` (+ comment) |
| `message` (chief→agent mail) | `ticket comment <id>` / edit |
| `inbox` / `messages` (agent reads mail) | reading the ticket's **unread** activity |
| `resolve` (answer a blocker) | `ticket comment` + `ticket status` |
| `status <id>` | `ticket show <id>` |
| `handoff --file …` | `ticket attach <id> --file …` |
| `list` | `ticket list` (the board) |

**Also removed (no parallel path):**

- the durable **mailbox** → the ticket's activity thread;
- the raw-tier **journal / "delivery ledger"** → the ticket's history;
- `dispatch.Classify()` → gone; status is explicit, set by whoever moves the ticket;
- the **#397 crash machinery** → folded into the attn-emitted **Crashed** event.

The new surface is one namespace: **`ticket`** — `new`, `list`, `show`, `comment`,
`status`, `attach`, `assign`, `resume`, `watch`.

## PR plan — testable slices

All slices land on a **long-lived `feat/tickets` branch**, merged PR-by-PR for review;
the finished system merges to `main` as **one changeset**. That keeps each slice
reviewable while ensuring `main` never sees a half-migrated world (both `dispatch` and
`ticket` live at once) — the `dispatch` retirement lands atomically with the `ticket`
surface that replaces it.

Sequenced so each slice is independently testable and lands real value. Not granular;
each is a meaningful, verifiable chunk.

> **Rule of play:** this plan is the source of truth for progress. Each PR **updates its
> own row** here as part of the change, so the doc always reflects where we are.
> Legend: ⬜ not started · 🟡 in progress · ✅ merged into `feat/tickets`.

| # | slice | status |
|---|---|---|
| 1 | Ticket store + lifecycle | ✅ |
| 2 | Event-driven notification core | ✅ |
| 3 | Delegation ⇄ tickets (live wiring) | ✅ (sub-split — see `2026-06-26-work-tracker-slice3.md`) |
| 4 | Ticket view + resume + attachments | ✅ (sub-split — see `2026-06-26-work-tracker-slice4.md`) |
| 5 | Board view | ✅ (status columns + Todo backlog + filters; read-only awareness surface, opened from ⌘K) |
| 6 | Codex nudge path | ✅ (self-monitor formalized as an `agent.Capabilities` flag; daemon resolves it from the driver registry; pure `ticketnotify` + end-to-end codex-nudge roundtrip test) |
| 7 | Retire the `dispatch` namespace | ✅ (atomic: CLI/handlers/store/protocol removed, delegation rewired to tickets-only via `delegatedTicketPrompt`, ProtocolVersion 129→130; dispatch tables orphaned not dropped — append-only migration history; notebook delivery-ledger removed, shared raw-tier kept) |
| 8 | Export ticket state (CLI) — *post-merge, lands on `main`* | ⬜ |

1. **Ticket store + lifecycle.** Fresh `tickets` / `ticket_activity` /
   `ticket_attachments` tables (migration 55), the status enum (incl. Todo / Crashed),
   human-friendly slug ids (uniqueness check + collision guidance), CRUD + permissive
   status transitions, and the archive / 30-day-TTL sweep. Store-local row types (the
   wire shape comes later). The old `chief_of_staff_dispatches` table is left untouched
   and dies with its namespace in slice 7 — a fresh table is cleaner than evolving one we
   tear out anyway. *Test:* store-level — create/read/update, transitions, archival, TTL
   with injected time.
2. **Event-driven notification core.** Mutations emit events; observers + cursors;
   the two decoupled handlers (watch-consume / nudge), bundled-by-ticket, dedup,
   unread. Built against the store, **proven by a simulation harness** (drives
   producers + both handlers, nudge included; real Monitor arming skippable). *Test:*
   harness scenarios — emit → consume → unread cursor, dedup, bundle, nudge path.
3. **Delegation ⇄ tickets (live wiring).** Delegating creates/binds a ticket
   (description = prompt); the agent's self-reported status moves the ticket (forward);
   editing the ticket emits events the agent observes (reverse). Wires the event core to
   real sessions; **removes the dispatch report + mailbox paths**. *Test:* end-to-end
   with a real Claude agent — delegate, agent reports, chief observes; chief comments,
   agent picks it up.
4. **Ticket view + resume + attachments.** The detail view (title / description / status
   / history / attachments; edit description, add comment, change status, attach files)
   and the **Resume button** (reload agent bound to the ticket, close the view). Plus
   `Cmd+K` assign-ticket-to-session and open-ticket-from-session. Folds `dispatch
   handoff` into `ticket attach`. *Test:* open a ticket, edit it, attach a file, resume a
   closed one, bind a bare session.
5. **Board view.** The session list as status columns with the Todo backlog and
   filters. *Test:* tickets appear in the right columns; backlog persists; filters work.
6. **Codex nudge path.** `HasSelfMonitor` capability + the idle-nudge handler for
   non-Claude agents, both chief→agent and agent→chief. *Test:* a codex agent gets
   nudged on a ticket change and consumes it.
7. **Retire the `dispatch` namespace (completion bar).** Remove every old verb
   (`watch / report / message / inbox / messages / resolve / status / handoff / list`)
   and the mailbox / journal / `Classify` / #397 remnants — once the `ticket` surface
   covers them. *Test:* the `dispatch` surface is gone and nothing references it.

8. **Export ticket state (CLI).** *Lands after `feat/tickets` merges to `main`, as a
   standalone PR to `main` — not on the integration branch.* `ticket export <id>`
   writes a **self-contained archive** of the whole ticket — the full record (id, title,
   description, status, assignee, cwd / last agent, timestamps), the complete activity
   thread (every status change + comment), and the attachment files themselves — so the
   finished work can be **archived outside attn** and survives the closed-ticket sweep.
   *Test:* export a worked ticket; the archive round-trips its activity + attachments
   with nothing left dangling in attn.

## Deferred / open

- **Projects** (grouping tickets) — wanted, **later**; leave the `project_id` seam.
- **(C)** deep cross-compaction durability and **(D)** the "went quiet without
  crashing" floor — still deferred, unchanged.

## Spine check

Awareness, not autonomy — the board *informs*, never *gates*. Assignees move their own
tickets; attn writes only **Crashed**; the chief is *notified* (watch / nudge), never
polls. **You are an assignee too** — a thread you drive solo is a ticket on the chief's
board, so its context folds back with zero inference.
