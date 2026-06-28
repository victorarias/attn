# Vision: The chief in the loop

> **What vs. why.** This doc is the durable **why** — the north star for the
> chief's awareness of, and reach into, its delegations. The **what** — the domain
> model and the surfaces that realize it — lives in
> [docs/plans/2026-06-26-work-tracker.md](../plans/2026-06-26-work-tracker.md) and the
> **Ticket** / **Chief of staff** entries in [docs/glossary.md](../glossary.md). The
> loop shipped as the **ticket** model. The `dispatch`-based mechanism this doc
> originally sketched — a per-thread `dispatch watch`, a separate mailbox, a Monitor
> the chief had to keep armed — was retired once delegated work moved onto durable
> tickets (ProtocolVersion 130).

## End state (the why)

The chief of staff stops being a switchboard. A switchboard fires work into the
void — delegate an agent and forget it, learning the outcome only if Victor goes
looking or the chief remembers to poll. The end state is a chief that **holds
every thread it spun up**: it knows what each delegated agent is doing, sees what
they produce the moment they finish, and folds those outputs back into its
understanding of Victor's world.

It holds them the *durable* way. Each delegation is a **ticket** — a tracked work
item with its own state, history, and artifacts — not something the chief has to
keep alive in its own attention. The thread persists on the board whether or not
the chief is looking, so awareness survives a compaction, a restart, tomorrow.
When three agents are working, Victor gets **one coherent picture** from the
chief, not three dashboards to reconcile himself. When an agent finishes, the
chief already knows the natural next step. When two agents' work collides, the
chief is the one that catches it. Awareness is the entire difference between a
dispatcher and a chief of staff — it is what lets the chief actually help, rather
than just route.

Think of the chief as Alfred to Victor's Batman. Alfred runs the house and keeps
track of every thread, so the moment Batman asks how things are going, he already
knows. He handles the small, obvious things himself and hands the real calls to
Batman — he is there to *inform and tee up decisions*, never to make them for
him. That is the spirit of this loop: it exists to make the chief **aware, not
autonomous**. The point is a chief that is *ready to help*, with Victor firmly in
control — not a step toward automating Victor out of his own decisions.

**Where the analogy breaks.** Alfred sits *between* Batman and the field — Batman
works through him. Victor doesn't. He works at every level at once: he talks to
the chief, and he reaches into the agents directly and steers them himself. So
the chief is **not a gatekeeper Victor routes through — it is an awareness layer
he works alongside.** That is exactly why every change to a ticket is authored by
*anyone* — agent, chief, or Victor — and flows through the same history: the
chief's picture stays whole even for threads Victor drove without it. The chief's
worth is being one complete, durable picture Victor can return to — to resume a
conversation, track a project across days — not a controller that owns the work.
Miss this and the chief silently goes stale the moment Victor steers an agent
himself, and stops being a place he can pick the thread back up.

But observing is only half a loop — a chief that can *see* a running agent yet
not *reach* it is a sensor, not a hand. So the loop closes both ways: the chief,
and Victor through it, can steer work already in flight — answer a blocker,
re-brief it, feed it context — by editing the same ticket. And the reverse
channel must be as ambient as the forward one: a steer to an agent never rots in a
queue no one watches. A self-monitoring agent learns of it the moment it lands; an
agent that can't is nudged to check at a sensible moment; and Victor sees the
pending signal in the sidebar where he already looks — never buried in a dashboard
he never opens.

## The shape: an issue tracker, not a switchboard

The realization is an **issue tracker** — Linear, not Jira — with one twist: the
assignees are autonomous agents that **report their own status**, and the chief is
*notified the instant a ticket moves* instead of polling. Borrow Linear's restraint:
a handful of fields, no sprints, no required transitions, no custom machinery. **The
board informs; it never gates.** That sentence is the whole spine — awareness, not
autonomy — made concrete.

How it runs:

- **Delegating mints a durable ticket** bound to the new session — the brief is its
  description, the session is its assignee, and it opens in the **Working** column.
  The ticket has its own identity, so it can be resumed, reassigned, or sit in a
  backlog without depending on a live session.
- **The agent moves its own ticket** with `attn ticket status` (`in_progress` /
  `needs_input` / `ready_for_review` / `completed` / `failed`), each optionally
  carrying a comment. attn authors exactly one status itself — **crashed** — the one
  a dead worker can't report.
- **The chief reads the board** (Todo · Working · Blocked · In Review · Done) and is
  pushed a `tickets_updated` the instant a ticket moves; it opens a ticket for the
  full activity thread and attachments only when a decision needs the detail.
- **Steering back goes through the same ticket** — a comment, a re-brief, a status
  nudge — which the agent picks up with `attn ticket inbox`. There is no separate
  mailbox; the reverse channel is just more events on the ticket.
- **So the chief's loop is "delegate → record → report, then the turn is done."**
  The board carries the state, so the chief re-engages on a signal (a ticket needs
  input, fails, or finishes) instead of babysitting a running agent.

## North-star principles

- **Event-driven where we can; attn-triggered where we can't.** A ticket move emits
  an event. A self-monitoring agent (Claude) watches the event stream live and drains
  it the moment it lands; an agent that can't (codex, etc.) gets a fixed doorbell
  typed into its PTY *only when it is idle*, nudging it to run `attn ticket inbox` — a
  sensible-moment poke, not a busy-loop timer. The split is a first-class capability
  (`HasSelfMonitor`), resolved from the driver registry, never guessed.
- **The loop runs both ways.** Observing is half a loop; the chief must also steer a
  running agent. Steering is just another authored event on the ticket (comment,
  re-brief, status), delivered by the same layer and read with `attn ticket inbox` —
  the reverse channel gets the same ambient treatment as the forward one.
- **One source of truth for doneness.** The ticket's status enum *is* the definition
  of "done" — derived once, read everywhere (the board column, the notification, the
  chief's view). Nothing defines "done" twice. The board derives from the status; it
  never holds a second opinion.
- **The daemon delivers a doorbell, never content.** attn triggers a *read*; it never
  streams message content into a PTY. The durable ticket and its event log hold the
  payload; the agent pulls it. This boundary is load-bearing — breaking it turns an
  ambient nudge into the daemon putting words in an agent's mouth.
- **Signal, not noise.** A ticket moves on terminal completion, genuine blockers, and
  failures — never on routine tool-approval prompts. And silence never equals success:
  a mid-flight death surfaces as **crashed**, captured from the session's pre-clobber
  runtime state the instant its process exits, not left as a stale Working column.
- **Distilled by default, drill-in on demand.** The activity thread — status changes
  plus comments — is the digest the chief ingests; it opens the full ticket (history,
  attachments, the handover artifact, the transcript) only when a decision needs it.
  Big outputs land as **attachments** or a Notebook note referenced from the ticket,
  so awareness makes the chief *more* useful, not drowned.
- **Awareness lives in durable structure, not just attention.** The ticket and board
  persist on disk, so a *future* chief — post-compaction, next day, another machine —
  still holds the thread without re-arming anything. Live context for immediate help;
  the durable board and the journal for coherence across time.
- **Awareness serves Victor — and stops short of deciding for him.** The chief informs
  and tees up. It acts on its own only on small, reversible coordination — answering a
  trivial blocker, posting an update, carrying out the one delegation Victor asked for —
  and hands anything with real consequence (fanning out more agents, creating
  workspaces, an irreversible or outward-facing call) to Victor and waits. And it
  *surfaces* delegated outcomes; it does **not** validate or accept specialist work —
  reviewing the code, the implementation, the engineering is Victor's. The one exception
  is work within the chief's own competence: documentation and prose, which it can
  review on the merits. (Alfred proofreads the correspondence; he doesn't sign off on
  the rebuilt engine.) Inform-and-prepare by default; a confident-but-wrong steer is
  worse than waiting.

## Scope & non-goals

**In scope.** The durable **ticket** model (status, activity thread, attachments,
resume); the **board** as the read-only awareness surface the chief reads instead of
polling; agent self-report via `attn ticket status`; the reverse channel via
`attn ticket inbox`; the **notification split** (a live watch for self-monitoring
agents, an idle pty-nudge for the rest) and the per-identity read cursor that tracks
delivery; and attn-authored **crash** capture so a silent death is still visible.

**Non-goals.**
- *Not Jira.* Linear's restraint — a few fields, and a board that **informs, never
  gates**. No sprints, workflows, required transitions, or custom fields.
- *Not a new dashboard you must visit.* The board is an ambient surface (⌘K) and the
  signal lives in the chief's cognition plus small per-session affordances (badges) —
  not a place you have to go babysit.
- *Not auto-acting on agent output.* Awareness enables proactivity, but irreversible
  or outward-facing actions stay gated behind Victor.
- *Not a replacement for Victor steering agents directly.* He works at every level;
  the ticket stays coherent because every event is authored-by-anyone, including him.
- *Not projects or grouping yet.* The ticket carries a `project_id` seam, unused for
  now.

## The arc — shipped and open

The work-tracker epic delivered the loop across slices 1–7.

**Shipped:**
- [x] **Durable ticket store + lifecycle** — status enum, activity thread (status
  changes + comments, no separate "report"), attachments, memorable slugs, archive/TTL.
- [x] **Event-driven notification core** — one event log, per-identity per-ticket
  unread cursors, dedup.
- [x] **Delegation ⇄ tickets** — delegating mints and binds a ticket (description =
  brief, assignee = session, opens **Working**).
- [x] **Agent forward channel** — `attn ticket status`; attn-authored **crashed** on a
  mid-flight close.
- [x] **Agent reverse channel** — `attn ticket inbox` consume path; the steering edits
  the chief makes are read here.
- [x] **Notification split by capability** — `HasSelfMonitor`: Claude watches live;
  codex is idle-nudged with a fixed doorbell.
- [x] **Ticket view + resume + attachments** — the chief edits, comments, and changes
  status from the UI; **resume** reopens a stopped agent on the same ticket (its cwd +
  last agent id); `attn ticket attach` hands over a file.
- [x] **The board** — status columns + a Todo backlog + filters (blocked / in review /
  closed today), read-only, live `tickets_updated`.
- [x] **The `dispatch` namespace retired** — tickets-only delegation via
  `delegatedTicketPrompt`; the dispatch CLI, store, handlers, and protocol removed.
- [x] **Backlog without a delegation** — `attn ticket new --title [--description]
  [--id]` mints an unbound `todo` with no session, so the Todo column fills from
  first-class captures, not just delegation. User-triggered only: an agent may surface a
  ticket worth filing, but never creates one on its own initiative.

**Still open:**
- [ ] **The "went quiet" floor.** A ticket sitting in **Working** that simply stops
  emitting — no terminal event — is the one silence the model doesn't yet catch. A
  floor that notices a thread has gone quiet (vs. crashed, vs. done) is still owed.
- [ ] **Push vs. hold.** When does an ingested move make the chief proactively ping
  Victor vs. hold it until he asks? The threshold is unsettled.
- [ ] **Multi-delegation synthesis.** Several tickets moving close together should
  become one coherent update, not N pings.
- [ ] **Ticket export** (slice 8) — a self-contained archive of a ticket's state;
  deferred to a standalone PR on `main`.

**Resolved by the ticket model** (questions the old `dispatch` design carried, now
dissolved):
- **Re-arming watches across sessions.** The old design's hardest question —
  re-establishing a Monitor after the chief compacts or restarts — is gone: the board
  is durable state, so there is nothing to re-arm. The chief just reads it.
- **Ack ergonomics.** Replaced by the per-identity read cursor that `attn ticket
  inbox` advances — delivered/seen is tracked without ceremony.
- **Doneness-on-close.** Settled: a mid-flight death is **crashed** (captured from
  pre-clobber runtime state); a clean close leaves the agent's last reported column
  untouched. attn never guesses a `done`.
