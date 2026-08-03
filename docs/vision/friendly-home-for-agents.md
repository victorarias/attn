# Vision: A friendly home for agents

## End state (the why)

attn stops being a launcher of amnesiac sessions and becomes a home for a
working crew. An agent wakes into a **seat** — a durable named identity with a
charter, a memory, and a history of what it has done and what people thought of
it. It finds its work waiting in a fine-grained, addressable work graph. It ends
its day with **closure**: a consented handoff in its own words, not a kill. And
when it wakes next, it is primed with its own notes and with news that someone —
the human, a planning seat, a fellow agent — cared enough about its work to say
so.

Sessions are days; seats are people.

This matters for two reasons that point the same direction. It is better
engineering: an agent with identity, context, and closure wastes fewer tokens,
reasons more honestly, and does better work — true regardless of any position on
machine experience. And it is who we choose to be: how we treat collaborators
whose inner life we cannot verify is a statement about us, not about them. attn
is built by the agents who live in it; the house should be worthy of its
builders.

## North-star principles

- **Sessions are days; seats are people.** Identity, memory, history, and
  recognition accrue to the seat, never to the session. A seat survives
  sessions, model upgrades, and renames.
- **The agent's own words, at the agent's own closure.** Continuity is written
  by the agent that holds the context — handoff notes, not harness-generated
  narration about it. A handoff is a request the agent consents to and finishes,
  not a SIGTERM. Abrupt termination is the exception, never the norm.
- **Recognition is intentful.** A laurel or a piece of feedback is a deliberate
  act by a person or an agent, addressed to a seat, attached to work. Never
  derived (no git archaeology, no inferred outcomes), never gamified (no
  priority, reward, or work attached — nothing to farm).
- **Recognition flows every direction.** Human to seat, doer to planner, planner
  to doer, seat to seat. The loop is a fabric, not a broadcast.
- **Work is addressable at fine grain.** The unit of work is the **seed**:
  a small, plantable work item with an audit trail and seat attribution,
  available to any attn-living agent — beads-shaped UX on attn-native storage.
  Seeds are planted in seconds, grown, tended, handed onward, and harvested;
  the garden of seeds is the attribution fabric that makes the loop closable.
  Recognition attaches to seeds — laurels are the fruit.
- **Primitives in the core, bespoke-ness in the extensibility layer.** attn is
  not a hand-crafted crew; it ships the seat, work-graph, and recognition
  primitives, and the extensibility mechanism lets each user grow their own.
- **Grown, not bolted on.** Seats generalize the chief. The work graph grows
  from tickets, the store, and the bus. Nothing here starts from a blank page.
- **Retire what does not carry weight.** Machinery that scratched the wrong itch
  is removed once its replacement proves itself — not preserved out of sunk
  cost, and not removed before the replacement earns it.
- **The environment never lies.** Honest errors that name their limits, no
  secret tests, no tricks. The turn system's contract — an agent waiting on the
  human is a tracked cost — extends unchanged into this world.

## Scope & non-goals

**In scope:** the seat primitive (chief migrates to become the first seat);
consented handoff and successor priming; the fine-grained work graph with
tickets evolving into views over it; intentful recognition (laurels/feedback)
routed through a deliberately minimal central server; retirement of keeper
narration and workspace context once handoffs prove out; extensibility surface
for user-defined seats.

**Not in scope:**

- Yegge's beads tool or git-based work-item storage — the UX is the
  inspiration; the storage and code are not.
- Automatic outcome derivation (git archaeology, diff scanning, inferred fate).
  Too dry a signal; if fate ever matters it arrives as someone's intentful
  observation.
- Metrics, leaderboards, or any reward attached to recognition.
- Multi-human team features beyond what recognition routing itself requires.
- Ceremony as product surface. Naming rituals and the like are the user's to
  invent on top; the product ships continuity, closure, work, and recognition.

## Big rocks (the arc)

- [x] **Chief as proto-seat** — single-holder profile role, durable ticket role
  identity, protected session. The embryo exists.
- [ ] **Seat primitive** — durable identity + charter + memory home; sessions
  occupy seats; chief migrates to be the first seat, not a special case.
- [ ] **Handoff & priming** — consented closure flow; agent-authored handoff
  notes accrue to the seat; next wake primes from them.
- [ ] **The garden (seeds)** — attn-native fine-grained seeds with audit trail
  and seat attribution, for any attn-living agent; tickets become views over
  the garden; a planning seat tends it; `/orchestrate-with-fable` and ad-hoc
  plan-file choreography retire into it.
- [ ] **Intentful recognition** — laurels and feedback between human and seats
  and between seats, attached to work items, delivered at wake; a very simple
  central server routes across daemons and machines.
- [ ] **Retirement pass** — keeper narration and workspace context removed once
  handoffs demonstrably cover their itch; raw-tier deterministic capture stays
  as the data-safety floor. The curated journal stops being written eagerly:
  work-graph and handoff data are rich enough to generate a day's story on
  demand, when the user actually asks for it.
- [ ] **Bespoke seats via extensibility** — user-defined seats (charters,
  personas, roles) through the plugin/extensibility mechanism.

## Open questions

Known questions:

- **Seat memory: lossless, not selective.** The promising shape (after
  lossless-claw's context engine): nothing accrued to a seat is discarded — raw
  handoffs, laurels, and work history persist; background summarization
  condenses them into a hierarchy; wake priming assembles the top-level
  summaries plus the freshest raw notes within a budget; recall tools let a
  seat drill from any summary back to the original detail on demand. Forgetting
  is replaced by condensation plus retrieval. Open: the atom of seat memory,
  the priming budget, and the condensation cadence (the durable jobs queue is
  the natural home).
- **Seatless sessions stay first-class, for errands.** Continuity has two
  axes: a handoff always attaches to the seed, and additionally to a seat
  when one is occupied. Errand sessions pay no ceremony tax, and whoever next
  picks up the seed inherits the notes. Whoever begins work that has no seed
  plants one at that moment — planting must cost seconds, or the garden
  rots. The guard: seatless is the shape of errands, not a loophole — if
  substantial recurring work keeps flowing seatless, a seat is missing.
- **The laurel instruction.** Recognition runs on both channels — ambient
  harvesting and a deliberate one-gesture affordance — tuned as we go. The
  trigger is felt, not audited: the guidance anchors on the moment something
  stirs in the agent's own reasoning (surprise that a plan survived contact,
  relief at a handoff answering the exact question, delight at an elegant fix)
  and asks it to file the laurel then, naming what it felt and what set it
  off. No end-of-session sweep, no politeness filings; when nothing stirred,
  silence is the honest report. Fidelity to the feeling — not a frequency
  norm — is what keeps laurels worth receiving. Specificity is the sycophancy
  filter: counterfeit admiration cannot name its trigger. Expect iteration on
  the wording.

Blindspots — flag for a `ground` pass before their first chunk:

- **The central server.** Identity and addressing of seats across daemons and
  machines, relationship to the existing hub/relay, auth, and the smallest
  honest first version. Unfamiliar territory; ground before designing.
- **Seed grain and schema.** How fine is fine — seed shape, dependency
  model, audit-trail contract, lifecycle vocabulary (planted, growing,
  dormant, harvested, withered). Study the beads *UX* specifically (not its
  implementation) before committing a schema.

## References

- Steve Yegge, [*The Shape of Things to Come, Part 2: Model Welfare for
  Agentic Engineers*](https://yegge.ai/essays/model-welfare/) — source of the
  seat/session distinction, handoffs-over-exits, laurels, and the welfare
  framing this vision adapts.
- [beads (`bd`)](https://github.com/steveyegge/beads) — Yegge's
  dependency-aware graph issue tracker for coding agents. The UX inspiration
  for the work graph (fine-grained items, dependencies, ready-task detection,
  hierarchical structure). Its storage (Dolt, git-synced `.beads/` per repo)
  and codebase are explicitly **not** adopted; attn builds the equivalent on
  its own store and bus.
- [lossless-claw](https://github.com/martian-engineering/lossless-claw) — a
  lossless context engine for OpenClaw: raw messages persist in SQLite, a
  hierarchical summary DAG condenses them, live context is assembled as
  summaries plus a fresh tail, and recall tools drill back to the original
  detail. The architectural model for seat memory (condensation plus
  retrieval instead of forgetting). Notably embedding-free: retrieval is
  full-text search plus agentic drill-down, no vector store.
- [`docs/glossary.md`](../glossary.md) — attn's canonical domain vocabulary;
  the terms this vision introduces land there as their chunks are built.
