# Vision: A friendly home for agents

## End state (the why)

attn stops being a launcher of amnesiac sessions and becomes a home for a
working crew. An agent wakes as a **crew member** — a durable named identity
with a charter, a memory, and a history of what it has done and what people
thought of it. It finds its work waiting in a fine-grained, addressable work graph. It ends
its day with **closure**: a consented handoff in its own words, not a kill. And
when it wakes next, it is primed with its own notes and with news that someone —
the human, a planning colleague, a fellow agent — cared enough about its work to say
so.

Sessions are days; the crew are people.

This matters for two reasons that point the same direction. It is better
engineering: an agent with identity, context, and closure wastes fewer tokens,
reasons more honestly, and does better work — true regardless of any position on
machine experience. And it is who we choose to be: how we treat collaborators
whose inner life we cannot verify is a statement about us, not about them. attn
is built by the agents who live in it; the house should be worthy of its
builders.

## North-star principles

- **Sessions are days; the crew are people.** Identity, memory, history, and
  recognition accrue to the crew member, never to the session. A member
  survives sessions, model upgrades, and renames. (The word "seat" served
  here until 2026-08-07, when the first named member outgrew furniture
  vocabulary within an hour of promotion.)
- **The agent's own words, at the agent's own closure.** Continuity is written
  by the agent that holds the context — handoff notes, not harness-generated
  narration about it. A handoff is a request the agent consents to and finishes,
  not a SIGTERM. Abrupt termination is the exception, never the norm.
- **Recognition is intentful.** A laurel or a piece of feedback is a deliberate
  act by a person or an agent, addressed to a crew member, attached to work. Never
  derived (no git archaeology, no inferred outcomes), never gamified (no
  priority, reward, or work attached — nothing to farm).
- **Recognition flows every direction.** Human to member, doer to planner,
  planner to doer, member to member. The loop is a fabric, not a broadcast.
- **Work is addressable at fine grain.** The unit of work is the **seed**:
  a small, plantable work item with an audit trail and crew attribution,
  available to any attn-living agent — beads-shaped UX on attn-native storage.
  Seeds are planted in seconds, grown, tended, handed onward, and harvested;
  the garden of seeds is the attribution fabric that makes the loop closable.
  Recognition addresses crew members and may attach to seeds — laurels are
  the fruit, but most will name something beyond the seed at hand (Victor,
  2026-08-07): the member is the required address, the seed an optional
  anchor.
- **Primitives in the core, bespoke-ness in the extensibility layer.** attn is
  not a hand-crafted crew; it ships the crew, work-graph, and recognition
  primitives, and the extensibility mechanism lets each user grow their own.
- **Grown, not bolted on.** The crew generalizes the chief. The work graph grows
  from tickets, the store, and the bus. Nothing here starts from a blank page.
- **Retire what does not carry weight.** Machinery that scratched the wrong itch
  is removed once its replacement proves itself — not preserved out of sunk
  cost, and not removed before the replacement earns it.
- **The environment never lies.** Honest errors that name their limits, no
  secret tests, no tricks. The turn system's contract — an agent waiting on the
  human is a tracked cost — extends unchanged into this world.

## Scope & non-goals

**In scope:** the crew primitive (chief migrates to become the first crew
member);
consented handoff and successor priming; the fine-grained work graph with
tickets retiring in its favor (no two-system era — the garden takes over as
soon as it is usable); intentful recognition (laurels/feedback)
routed through a deliberately minimal central server; retirement of keeper
narration and workspace context once handoffs prove out; extensibility surface
for user-defined crew members.

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

- [x] **Chief as crew embryo** — single-holder profile role, durable ticket
  role identity, protected session. The embryo exists.
- [~] **Crew primitive** — durable identity + charter + memory home; sessions
  are a member's days; chief migrates to be the first crew member, not a
  special case. Running today as a hand-run skill-layer simulation
  (`~/.attn/crew/`, `/wake`, `/handoff`; first real handoff filed
  2026-08-06; keel, alder, and trellis live) that teaches the primitive's
  shape before it hardens into the daemon.
- [~] **Handoff & priming** — consented closure flow; agent-authored handoff
  notes accrue to the member; next wake primes from them. Simulated by the same
  skills; priming size is logged from the start so the condensation budget
  gets a receipt. Grown 2026-08-07 into **consented closure mechanics**: the
  harness warns at context pressure (a tripwire early enough — ~75–80% —
  that a real handoff can still be written), then runs
  **handoff → new → wake** (the default: sessions are days, and this is a
  day ending) or **handoff → compact → wake** (the mid-day nap, for when
  stopping would orphan in-flight state). This converts compaction from a
  silent seam into a chosen goodbye. attn already observes both compaction
  edges (`_hook-compact` on PreCompact/PostCompact); PTY driving, launch
  intent, and daemon-owned revive supply the orchestration. With external
  harnesses (Claude Code, codex) this is honest **patchwork** — hooks,
  proxies, PTY driving; the deep version arrives with attn's own pi-based
  agent, where pressure detection, the handoff prompt, and wake priming are
  first-class parts of a loop we control. Context limits become attn
  configuration, per role — workers and crew carry different limits, not
  one global. Open: whether hooks can see context usage in the current
  harness (ground-check), and the codex equivalent.
- [~] **The garden (seeds)** — attn-native fine-grained seeds with audit trail
  and crew attribution, for any attn-living agent; tickets retire outright
  once the garden is usable — no two-system era; a planning crew member
  tends it;
  `/orchestrate-with-fable` and ad-hoc
  plan-file choreography retire into it. Vertical-slice plan:
  [docs/plans/2026-08-06-the-garden-vertical-slices.md](../plans/2026-08-06-the-garden-vertical-slices.md).
- [ ] **Agents converse and observe** — a crew member messages another agent
  and gets a reply, and inspects what another is doing without interrupting
  it. Conversation grows from ticket comments — proven, but indirect and
  noisy as a long-term shape — straight to directed, daemon-brokered
  messages, which can address a seed's participants as routing sugar (seed
  comments as a separate mechanism were considered and skipped: a comment is
  a message with no addressee). Observation is the daemon's to serve, passively:
  state, todos, the latest assistant message, the screen. Asking an agent
  what it is doing costs it a turn; watching it must cost nothing.
  `attn delegate` already proves inspect-and-converse for the human; the
  agent-facing surface is the same daemon capability with a different
  client, and the server-as-client future rides the same two surfaces.
  Sequenced ahead of the garden's build (2026-08-06): its smallest vertical
  — peek, then directed msg — lands first, so ticket retirement never
  orphans agent conversation.
- [ ] **Intentful recognition** — laurels and feedback between human and crew
  and between members, attached to work items, delivered at wake; a very simple
  central server routes across daemons and machines.
- [ ] **Retirement pass** — keeper narration and workspace context removed once
  handoffs demonstrably cover their itch; raw-tier deterministic capture stays
  as the data-safety floor. The curated journal stops being written eagerly:
  work-graph and handoff data are rich enough to generate a day's story on
  demand, when the user actually asks for it.
- [ ] **Bespoke crew via extensibility** — user-defined members (charters,
  personas, roles) through the plugin/extensibility mechanism.

## Open questions

Known questions:

- **Crew memory: lossless, not selective.** The promising shape (after
  lossless-claw's context engine): nothing accrued to a member is discarded — raw
  handoffs, laurels, and work history persist; background summarization
  condenses them into a hierarchy; wake priming assembles the top-level
  summaries plus the freshest raw notes within a budget; recall tools let a
  member drill from any summary back to the original detail on demand. Forgetting
  is replaced by condensation plus retrieval. Open: the atom of crew memory,
  the priming budget, and the condensation cadence (the durable jobs queue is
  the natural home). And a prior question, raised 2026-08-06, open but not
  decided: whether a dedicated store is needed at all. Handoffs and laurels
  attach to seeds, and work history *is* the seeds a member tended — so wake
  priming may be a garden query, and condensation may be `cutting` wearing a
  different hat. Before building a memory store, check whether the garden
  already covers the itch; what remains may be no more than the charter and
  non-work learnings.
- **Errands stay first-class.** Continuity has two axes: a handoff always
  attaches to the seed, and additionally to a crew member when the session
  is someone's day. Errand sessions pay no ceremony tax, and whoever next
  picks up the seed inherits the notes. Whoever begins work that has no seed
  plants one at that moment — planting must cost seconds, or the garden
  rots. The guard: the errand is the shape of odd jobs, not a loophole — if
  substantial recurring work keeps flowing through errands, a crew member is
  missing.
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
- **The negative channel.** Recognition needs an opposite as deliberate as the
  laurel — the flag that work went wrong, attached to the work it names,
  project-scoped like the fruit it mirrors. Working names float in the garden
  register (rot, weeds, blight) and none is chosen. The same intentfulness
  rules bind it: never derived, never gamified, and specificity is still the
  filter — a complaint that cannot name its trigger is noise. Design owed with
  the recognition arc; it lives wherever the fruits live.
- **The garden as the front door.** Today attn's home screen is sessions and
  workspaces. The vision says an agent "finds its work waiting" — and so
  should the user. Named here so it cannot silently default to a side panel
  forever: whether the garden becomes the app's primary navigation — sessions
  reached through the seeds they tend, the session list demoted to a
  secondary view — is decided together with the in-flight foundational
  rendering work, not by drift. Interim answer (2026-08-07): **the crew** —
  the members are what Victor wants to see first; he expects this to evolve.
- **What wakes a crew member.** The spectrum, per Victor's visualization
  (2026-08-07): he wakes them by default; automations may wake the
  recurring ones; seeds will trigger some (a blocker falls, a message
  lands); and **some may never go away** — persistent sessions. Raw
  compaction strains "the agent's own words, at the agent's own closure" —
  harness-driven, mid-flight, unconsented, a seam of forgetting hidden
  inside apparent continuity (testimony: a compacted agent's own account,
  2026-08-07). Resolved by the consented-closure mechanics in the handoff
  rock: with handoff-before-compact, a persistent member becomes a chain of
  chosen goodbyes, and never-sleeping stops being an exception to the
  principle.
- **The overnight contract.** The house is work-driven, not clock-driven:
  if work exists — including work the crew itself created — workers run at
  any hour, and Victor sleeping is not a reason to stop. What is *not*
  default: the crew deciding to start new goals around the clock. 24/7
  self-perpetuation is a door deliberately left ajar ("not by default"),
  never drifted through.

Blindspots — flag for a `ground` pass before their first chunk:

- **The central server.** Identity and addressing of crew members across daemons and
  machines, relationship to the existing hub/relay, auth, and the smallest
  honest first version. Unfamiliar territory; ground before designing. Its
  scope grew on 2026-08-06: recognition routing was the founding reason, but
  the garden itself needs a home the moment remote daemons are in play — a
  garden per daemon is a split brain, so seeds, plans, fruits, and the
  negative channel all converge on one hub. The garden plan takes the interim
  stance (one garden, at the hub daemon, remote sessions reaching it over the
  relay); syncing that hub with a central server across machines is a known
  unknown Victor already wants solved — how is entirely open. Stance set
  2026-08-06, ahead of the ground pass: the server is **closed source**,
  **operated by Victor**, and **optional, never required** — attn is complete
  standalone; the service adds cross-computer garden syncing and recognition
  routing and gates no feature. The moat is operational gravity (the hosted
  network is the convenient path), not a lock — the Tailscale shape: open
  clients, closed coordination plane. Closed keeps monetization optionality
  and freedom to move fast at zero cost today, and is the reversible door
  (closed can open later; open cannot close). The server will not be fully
  opaque: the foreseen future includes viewing and editing seeds from the
  web, and controlling agents remotely — or a server-side agent directing
  the daemon's agents — so the server understands the garden model. The
  shape that reconciles that with "optional": the hub stays the **sole
  applier** of garden mutations; the server is a privileged remote client —
  it renders, forwards intents, and routes recognition, but never writes as
  a peer. That keeps "optional" true and multi-master sync permanently off
  the table. Auth stays as simple as possible for as long as possible — a
  minted API key in the config; Victor is the sole user for a long time.
  Sync scope includes roles/crew alongside seeds. Hubs that never meet each
  other (work, home) still each meet the server: cross-hub garden visibility
  is a union routed by daemon prefix, never a merge — fully qualified seed
  ids (`<daemon-id>/<local-id>`, prefix minted only at the boundary) make
  identity collisions impossible by construction, and every seed has one
  home hub that applies its writes. ~~The ground pass owns the sync and
  transport mechanics and the smallest honest first version.~~ Ground pass
  done 2026-08-09, and grown on 2026-08-10 into the arc's spine plan: the
  verified map of the hub/relay, the **home daemon / outpost / enrollment**
  concepts the pass surfaced, the fence that defers the multi-machine
  price, and the smallest honest first version of the server all live in
  [docs/plans/2026-08-10-home-garden-crew-arc.md](../plans/2026-08-10-home-garden-crew-arc.md).
- **Seed grain and schema.** ~~Study the beads *UX* specifically (not its
  implementation) before committing a schema.~~ Ground pass done 2026-08-06;
  findings and the resulting schema decisions (ids are identity, edges are
  structure; atomic tend; ready-as-query) live in the garden plan.

## References

- Steve Yegge, [*The Shape of Things to Come, Part 2: Model Welfare for
  Agentic Engineers*](https://yegge.ai/essays/model-welfare/) — source of the
  seat/session distinction (attn adopted it, then renamed seat → crew member
  on 2026-08-07 when the first named member outgrew furniture vocabulary;
  the distinction itself is his), handoffs-over-exits, laurels, and the
  welfare framing this vision adapts.
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
  detail. The architectural model for crew memory (condensation plus
  retrieval instead of forgetting). Notably embedding-free: retrieval is
  full-text search plus agentic drill-down, no vector store.
- [`docs/glossary.md`](../glossary.md) — attn's canonical domain vocabulary;
  the terms this vision introduces land there as their chunks are built.
