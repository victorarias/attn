# Vision: The chief in the loop

> **Implementation note.** The "why" below is the durable goal. The implementation
> shipped as the **ticket** model (the work-tracker epic — see
> [docs/plans/2026-06-26-work-tracker.md](../plans/2026-06-26-work-tracker.md) and the
> Ticket entry in [docs/glossary.md](../glossary.md)), which **superseded** the
> `dispatch`-based approach sketched in the roadmap checkboxes further down. References
> to `attn dispatch …`, `internal/dispatch`, the dispatch mailbox, and `.attn/raw/dispatches`
> below are historical: that surface was retired once delegated work moved onto tickets.

## End state (the why)

The chief of staff stops being a switchboard. Today it can fire work into the
void — delegate an agent and forget it, learning the outcome only if Victor
goes looking or the chief remembers to poll. The end state is a chief that
**holds every thread it spun up**: it knows what each delegated agent is doing,
sees what they produce the moment they finish, and folds those outputs back into
its understanding of Victor's world.

When three agents are working, Victor gets **one coherent picture** from the
chief, not three dashboards to reconcile himself. When an agent finishes, the
chief already knows the natural next step. When two agents' work collides, the
chief is the one that catches it. The outputs are what make the chief *useful* —
awareness is the entire difference between a dispatcher and a chief of staff.
This is the capability that lets the chief actually help, rather than just route.

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
he works alongside.** That is exactly why context must flow *back* to the chief
even for threads Victor (or an agent) drove without it: the chief's worth is
being one complete, durable picture Victor can return to — to resume a
conversation, track a project across days — not a controller that owns the work.
Miss this and the chief silently goes stale the moment Victor steers an agent
himself, and stops being a place he can pick the thread back up.

But observing is only half a loop — a chief that can *see* a running agent yet
not *reach* it is a sensor, not a hand. So the loop closes both ways: the chief,
and Victor through it, can steer work already in flight — answer a blocker,
redirect it, feed it context. And the reverse channel must be as ambient as the
forward one: mail to an agent never rots in an inbox no one watches. The agent
learns of it the moment it lands, and Victor sees pending mail in the sidebar
where he already looks — not buried in a dashboard he never opens.

## North-star principles

- **Event-driven where we can; attn-triggered where we can't.** Claude chiefs
  get true push (Monitor) — they learn of meaningful change *when it happens*.
  Other agents (codex, etc.) can only poll. Even there, avoid a dumb timer:
  attn nudges the agent to poll — injecting via pty once the chief has been idle
  a few minutes — so the check fires at a sensible moment, not on a busy loop.
- **The loop runs both ways.** Observing is half a loop; the chief must also be
  able to *steer* a running agent. The reverse channel (the dispatch mailbox)
  gets the same event-driven, ambient treatment as the forward one. Delivery by
  situation: a **Claude** agent watches its own inbox (a Monitor) and
  self-delivers — no human in the loop. An agent that **can't self-monitor**
  (codex) falls back to attn — an **on-agent overlay** Victor clicks when he's
  driving it, and an **automatic idle-wake** (attn pty-nudges it to check the
  inbox after a few minutes idle) when no one is. A **left-sidebar pending-mail
  badge** flags the session either way. Never dashboard-bound. Keep the existing
  boundary: the daemon delivers durable mail and triggers a *read*; it never
  streams message content into the PTY.
- **attn owns the signal; the chief stays dumb.** A first-class
  `attn dispatch watch` defines what an "event" *is*. The chief just arms it and
  reacts. One source of truth for doneness — reused verbatim by the poll
  fallback, so "done" is never defined twice.
- **Signal, not noise.** Wake the chief for terminal completion, genuine
  blockers, and failures — *never* routine tool-approval prompts. And silence
  must never equal success: every terminal state emits, including crashes.
- **Distilled by default, drill-in on demand.** The chief ingests the concise
  report + actionables; it pulls the full transcript only when a decision needs
  it. Awareness must make the chief *more* useful, not drown its context.
- **Awareness lives in two places.** Live context for immediate help; the
  durable journal so a *future* chief — post-compaction, next day, another
  machine — still holds the thread. The chief's job is coherence across threads
  and across time, and context windows are neither.
- **Default-on, overridable.** The chief arms monitoring by default, but Victor
  can always say "leave it for the dashboard" and the chief steps back.
- **Awareness serves Victor, not itself — and stops short of deciding for him.**
  The point of knowing is to be *ready*: synthesize across agents, catch
  conflicts, prepare the next step, and surface the one thing that actually needs
  him. But the chief informs and tees up; it does not decide. It acts on its own
  only on the small, obvious, reversible things — answering a trivial,
  low-consequence blocker, say — and anything with real consequence it hands to
  Victor and waits. Inform-and-prepare by default; act only at the margins. A
  confident-but-wrong steer is worse than waiting.

## Scope & non-goals

**In scope.** The chief monitoring the agents *it* delegated; ingesting their
outputs into both live context and the durable journal; the `dispatch watch`
command and its event definition; the Monitor-based path for Claude chiefs and
the polling fallback for other agents. And the **reverse channel**: fixing the
dispatch mailbox so steering reaches a running agent promptly — agents watching
their own inbox, plus a sidebar pending-mail badge and an on-agent overlay for
Victor.

**Non-goals.**
- *Not* a general pub/sub between arbitrary sessions. Built on general
  primitives, but the vision is specifically the **chief's** awareness of, and
  reach into, its delegations.
- *Not* a new dashboard or screen. The signal lives in the chief's cognition and
  small ambient affordances (a sidebar badge, an on-agent overlay) — not a
  separate surface you have to go visit.
- *Not* auto-acting on agent output. Awareness enables proactivity, but
  irreversible or outward-facing actions stay gated behind Victor.
- *Not* a replacement for Victor steering agents directly. The badge and overlay
  give him ambient reach; he can still drive any agent himself whenever he wants.
- *Not* a rewrite of the mailbox — its durable, ack-tracked model stays; only
  delivery and ergonomics change.
- *Not* the chief's notebook inbox — that's a separate channel (the chief's own
  inbox, stored as a notebook note), out of scope here.

## Big rocks (the arc)

Coarse and expected to evolve — not a task tracker.

**Forward — observe (agent → chief):**
- [~] **`attn dispatch watch <id>`** — blocking command, one line per meaningful
  event, exits on terminal. Owns the signal definition (done / blocker /
  failure; excludes routine approvals; covers crash states). In flight: PR #394
  (shared classifier in `internal/dispatch`, reuses `list_dispatches`, no new
  protocol surface) — ready to merge.
- [x] **Reliable doneness-on-close** — a close without a structured terminal
  report is now the neutral terminal `ended` (silence is neither success nor
  failure), so an unreported success no longer cries a false `[failed]`. attn does
  *not* infer completion on a clean close — doneness stays agent-claimed, never
  guessed. The one close it *does* assert is a crash: the daemon captures the
  delegated session's last attn-classified state the moment its process exits
  (before the idle-clobber/removal erases it) and surfaces a close that was cut
  off mid-flight — `working` / `launching` / `pending_approval` — as `failed`,
  while a clean rest (`idle` / `waiting_input`) or an unconfirmed close (`unknown`
  / unstamped) stays neutral `ended`. So a real crash is visible, a real success
  is never overwritten, and the safe direction (never a false `done`) holds. The
  neutral `ended` landed with the #394 alignment; crash-visibility-on-close
  followed. Also resolves the re-arming caveat below.
- [ ] **Delegation-time injection** — at delegation, attn instructs the chief to
  arm a persistent, non-blocking Monitor on the new dispatch. Scoped to the
  dispatch id, self-retiring on terminal, default-on but overridable.
- [ ] **Output-into-context** — the event carries the *distilled* report
  (`concise_summary` / `structured_report` / `actionable`); the chief drills
  into the full transcript on demand. The *large-output* branch already ships:
  `attn dispatch handoff` lets a delegated agent stash a big artifact in the
  Notebook and report just the reference, so the drill-in target can be a durable
  note, not only the transcript ([plan](../plans/2026-06-22-dispatch-notebook-handoff.md)).
- [ ] **Durable capture** — the chief journals the outcome so awareness survives
  compaction and session restarts.
- [ ] **Non-Claude poll path** — agents that can't be pushed to (codex, etc.)
  poll `dispatch status` (same doneness semantics, stop-on-terminal). attn makes
  the poll quasi-event-driven by pty-injecting a "go check" nudge once the chief
  has been idle a few minutes, rather than leaving it on a timer.

**Reverse — steer (chief → agent):**
- [~] **Agents watch their own inbox** — delegated Claude agents arm a Monitor
  on `dispatch inbox` and self-deliver mail on arrival. Brief-level, no attn
  change; now a standing line in every delegation brief.
- [ ] **Auto-wake on idle** — for agents that can't self-monitor (codex), attn
  pty-nudges them to check the inbox after a few minutes idle. The *unattended*
  fallback — mirror of the idle-nudge on the forward side.
- [ ] **On-agent mailbox overlay** — a top-right overlay on an agent that has
  pending mail, for when Victor is *driving* that agent; click fires the
  inbox-doorbell PTY injection to it (the *attended* manual path; message
  content stays out of the PTY).
- [ ] **Pending-mail sidebar badge** — a per-session left-sidebar badge when an
  agent has unread mail (sibling of the chief / delegated-from-chief badges).
- [ ] **Fix mailbox delivery, kill limbo** — mail reaches a live agent promptly
  instead of waiting for a lifecycle boundary or a dashboard click; mail
  lifecycle ties to the dispatch so nothing strands when it closes. Durable /
  ack model unchanged.

**On screen:**
- [~] **"Delegated from chief" sidebar badge** — already in flight (PR #392); the
  visual sibling the pending-mail badge follows.

## Open questions

- **Push vs hold.** When does an ingested output make the chief proactively ping
  Victor vs simply hold it until he asks? Where's the threshold?
- **Multi-delegation synthesis.** When several agents finish close together, how
  does the chief batch them into one coherent update instead of N pings?
- **Ownership of "meaningful."** Is the event definition wholly attn-core, or is
  some of "what matters" agent-defined? (Leaning attn-core for one source of
  truth.)
- **Re-arming across sessions.** If the chief compacts or restarts while a
  monitor is armed, how is the watch re-established from journal / dispatch
  state so a thread isn't silently dropped? (The old failure mode here — re-arming
  a watch on an already-finished, already-closed agent reading `failed` for a
  success — is resolved: a clean close now reads neutral `ended`, and only a real
  mid-flight crash reads `failed`. See *reliable doneness-on-close* above.)
- **Idle-nudge reliability.** Does the pty "go check" nudge for non-Claude
  chiefs land reliably (idle detection, injection timing), or do some agents
  still need a self-driven timer as backstop?
- **Ack ergonomics.** With self-delivery, how much of the read → mark-read → ack
  ceremony survives? What's the minimum that still tracks "delivered / acted on"
  without making mail a chore?
