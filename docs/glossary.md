# Glossary

Canonical domain language for attn. When code, prompts, plans, or guidance name
these concepts, use these words. The goal is one shared vocabulary so every agent
(and human) working on attn means the same thing.

This file is the source of truth for the terms below. If an implementation detail
drifts from a definition here, the definition wins — fix the code or fix this file
in the same change, deliberately.

---

## Workspace context

The per-workspace **editorial overlay**: `context.md`, one per workspace, managed
by `internal/store/workspace_context.go`. It is the workspace's own agents' (and
the user's) running statement of what currently matters here — Area, Current
Picture, Decisions, Constraints.

- **Authored by the workspace's agents and humans.** Agents provide editorial
  information into the workspace context as they work. This is the one place a
  working agent is expected to contribute durable shared state.
- **Ephemeral, not durable.** It is compacted on a size threshold and **erased**
  when the workspace is removed (`DELETE FROM workspace_contexts`). It was never
  meant to outlive the work.
- **Salience, not truth.** It marks where to point attention; it is the agents'
  unverified claim of what was important, not a record of what actually happened.

It is SQLite-canonical (the coordination layer), distinct from the Notebook, which
is filesystem-canonical.

## The Notebook

attn's durable, **profile-wide, filesystem-canonical** markdown layer — a dated
journal plus a PARA knowledge base. The
`.md` files on disk are the source of truth (unlike the workspace context, which is
SQLite-canonical), and the Notebook outlives any single session, workspace, or PR.

The write paths are the daemon (over the WebSocket protocol) and native file edits
on disk — both land in the same filesystem-canonical tree. It holds **the journal
and the knowledge base** — the journal a dated log, the knowledge base distilled,
timeless knowledge — alongside the machine-internal raw tier the keeper reads from.

## The journal

The durable, **curated, cross-workspace log of what was done in attn** —
`journal/<date>.md` in the Notebook. The user's lasting record for recall and
performance review: decisions made, things built and shipped, hard-won fixes,
dead-ends, what was learned. Importance is not recurrence; the most valuable
entries are singular.

Who writes the journal:

| Writer | What they contribute |
|--------|----------------------|
| **The keeper** | per-workspace narratives of the work done in each workspace |
| **The chief of staff** | a cross-workspace, chief-of-staff-altitude log (what moved across workspaces, what was delegated and decided) |
| **The human** | direct edits — corrections, additions, curation |

Other agents *can* write to the journal, but we do not ask them to and do not nudge
them. In practice they do not, and that is fine: automated capture (the keeper) is
what keeps the journal good. We do not try to prevent other writes — that is not
enforceable at this abstraction level — we simply say nothing about it.

The journal is **curated**: nothing machine-raw lands in it. Raw machine inputs
live in the raw tier and are consumed by the keeper, never pasted into the journal.

## Knowledge base

The Notebook's **distilled, durable knowledge subtree** — `knowledge/` — holding
notes worth keeping beyond a single PR: decisions, gotchas, and domain knowledge. It
is organized PARA-style into `knowledge/projects/`, `knowledge/areas/`,
`knowledge/resources/`, and `knowledge/archive/`, and indexed by `knowledge/index.md`
(each PARA dir also carries its own `index.md`). A note carries OKF frontmatter with a
`type` field (`type: note`), an open vocabulary rather than a closed set the store
validates.

Maintained by the **chief of staff** and the **user**, edited directly as files (over
the daemon/WS write path or as native file edits on disk) — there is no closed-kind
gate.

Distinct from the journal along one axis: the journal is a **dated log of what
happened**; a knowledge note is a **timeless statement of what is known**. The
knowledge base is not a task tracker — capture what is *known*, not what is *to do*.
Chief-authored knowledge is **grounded** with resolvable `sources:` (journal
anchors or URLs) rather than written from paraphrase alone; the
user's own notes in the same space are theirs to keep however they like.

## Note title

A note's title is its **first `# H1` heading** — the single canonical title. attn
does **not** read a frontmatter `title:` field (`Document.Title()` parses the body's
first level-1 ATX heading, skipping fenced code). When a note has no `# H1`, callers
fall back to the note's **filename**, which is its stable address (links point at the
path, so the filename is an ID, not a competing title). Frontmatter carries
*properties* (`type`, `summary`, `tags`, `sources`, dates) and is rendered as a
properties card in the editor — never a title. Journals (`# <date>`) and the chief
inbox (`# Chief inbox`) follow the same rule; they carry their title as the body H1
and write no frontmatter `title:`.

## The keeper

The single automated entity that **tends each workspace**. One persona, two duties:

1. **Keeps the workspace context tidy** — compacts/prunes `context.md` when it
   grows past threshold.
2. **Narrates the workspace's work into the journal** — turns the workspace's
   sessions into the curated per-workspace
   journal narrative. On a workspace's final removal pass it also files that
   workspace's linked `knowledge/projects/<slug>/` folder (the one whose `index.md`
   carries `resource: attn:workspace/<id>`) under `knowledge/archive/` — a
   mechanical tidy-up that keeps the active `projects/` view focused; the chief
   keeps the higher-judgment promotion into `areas/`.

These two duties are **causally coupled**, which is why they are one entity: the
keeper can safely prune `context.md` *because* it has already preserved the story
in the journal. "Nothing is lost when the overlay is compacted or erased" is the
keeper's own promise to keep, not an implicit contract spread across separate
actors.

The keeper is realized as task kinds on the durable runner (`internal/tasks`):
`compact_context` (tidy duty), and `summarize_session` + `narrate_workspace`
(narrate duty). These are internal **mechanisms**, not separate personas — there
is no separate "janitor," "narrator," or "summarizer" in the domain model, only the
keeper performing its duties. (The narrate duty runs as a strong-tier agent reading
per-session digests produced by a cheaper summarize step; both are the keeper.)

## The chief of staff

The cross-workspace operator. The Notebook — not any single workspace's context —
is its durable home. The chief **journals**, from a chief-of-staff altitude: the
state of work across workspaces, what it delegated, what was decided — **not** a
step-by-step of what individual agents are doing inside a workspace.

The chief is **keeper-aware**: because the keeper already narrates each workspace's
own work into the journal, the chief does not duplicate per-workspace play-by-play.
It writes the cross-workspace layer the keeper cannot see.

## Turn, and the queue

A **turn** is the user owing an agent their attention. It **opens** when a session
reaches a state that wants the user, and it **closes** only when the user settles
it — steering, approving, snoozing, or pressing settle. Nothing else ever closes
one; in particular, looking at an agent is not acting on it. `turn_owed` is derived
at broadcast from the persisted `turn_opened_at`/`turn_settled_at` stamps and is
never stored. The predicates live in `internal/attention`.

The **queue** is the sidebar arrangement built on turns (queue mode; off by
default). Its standing order is the chief's anchored slot, the turns you owe
(oldest first), the settled rest, pinned agents, pinned workspaces, muted.

**Pinned** — the user saying "I'll come to this one myself". A **pinned agent**
(`sessions.pinned_at`) leaves the queue on its own and leaves its siblings in it; a
**pinned workspace** takes the whole workspace. Pinning is not settling: the turn
goes on accruing underneath, so unpinning surfaces whatever is outstanding at its
true age.

**Satellite** — a shell split out of an agent, recorded at spawn as
`sessions.parent_session_id`. It gets no sidebar row of its own: you reach it by
going to its agent, where it is a pane. Splitting a shell out of a shell inherits
the same agent, so the link always names an agent and never another shell.
Attachment is judged at read — the parent must still exist and still be in the same
workspace — so a satellite whose agent closed, or whose pane moved, gets its own
row back with no cascade. A satellite with no live parent is an **orphan**, and
orphans keep their rows.

## Ticket

The chief delegates a unit of work to a sub-agent, and that work is tracked as a
**ticket** bound to the delegated session (the session is the ticket's assignee).
The agent reports its own **work state** — in progress, needs input, ready for
review, completed, or failed — which moves the ticket across the board
(Todo · Working · Blocked · In Review · Done). Comments, status changes, and
artifact attachments accumulate on the ticket's activity thread. Current artifacts
are the files in the ticket's Notebook directory, and the chief watches
progress from the ticket view and board rather than polling the agent.

## The raw tier

Machine-internal capture under `.attn/raw/`, the keeper's **input**, never
user-facing and never part of the curated journal:

- `sessions/<wsID>/<sessionID>.md` — per-session digests (the summarize step's
  output), nested under the owning workspace; a session with no workspace lands in
  the reserved `sessions/_solo/<sessionID>.md` bucket.
- `context-snapshots/<wsID>.md` — the `context.md` snapshot taken synchronously at
  workspace removal (the deterministic data-safety floor, so the editorial overlay
  is never lost before the keeper can narrate it).

The raw tier is physically unreachable through the user-facing notebook APIs
(`CleanPath` rejects dotdir segments). Capture into it is deterministic and always
happens; the keeper's narration is best-effort on top of it, so nothing is lost if
narration never runs.

## The document store

attn's **document store** is where an extension keeps its own data. Three names
locate every record:

- **Namespace** — `owner/name`, e.g. `ext/approval-gate`. A namespace is granted
  to exactly one author and is the isolation boundary: nothing an extension does
  can read or write another namespace, and two namespaces may use the same
  collection name without meeting.
- **Collection** — a named set of documents inside a namespace, e.g. `requests`.
- **Document** — one JSON object with a caller-chosen **id**, unique within its
  collection. The body is stored byte for byte and nothing ever rewrites it, so
  an author never writes a migration.

Every document also carries a **revision**: a count of the writes to it, starting
at 1, reported by every read and by every write. Handing a revision back as a
write's **expectation** makes that write conditional — it lands only if the
document is still at that revision, and is otherwise refused with both revisions
named. That is what makes a read-modify-write safe between writers who cannot see
each other; without an expectation a write always lands, which is what a caller
setting a value rather than editing one wants. Expecting revision 0 means
expecting no document at all, and is how a write becomes create-only.

A collection carries a **declaration**: the fields it promises are queryable,
each with a type. Declaring a field does not constrain what a document may
contain — undeclared keys are stored and returned untouched — it only says what a
query may name, and it is what the store indexes that field by. The type decides
how stored values compare, so a body holding `"5"` in a `number` field sorts as
the number 5. `created_at` and `updated_at` are always queryable and are never
declared.

Physically a collection is its own table and a declared field an indexed column
computed from the body, so a declaration is built rather than merely recorded —
without any document being rewritten. A **live query** therefore ends, with an
error saying which, when its collection is removed or redeclared without a field
it uses: the collection can no longer answer the question that was asked.

A **query** is one JSON object: namespace, collection, filters, sort, limit, and
an optional **after cursor** — the id of the last document of the previous page.
The cursor is part of the query rather than a filter a caller writes, because the
visible order is (sort field, id) and a filter can only constrain one of those:
with documents tied on the sort field, `sort > value` skips the tied ones and
`sort >= value` returns the anchor again.

A **live query** is that same query left open. Every delivery is the whole
current result set, so a subscriber renders what it is handed and never
accumulates state across deliveries; the daemon re-runs the query when a write to
the collection says the answer may have moved. A skipped delivery is not a lost
update — the next one supersedes it.

## Conversation session

A **conversation session** is an attn session whose agent runs headless in a
process attn spawns, instead of a terminal program driven through a PTY. It is a
session in every other respect — it has a workspace, a pane, a state, turns, and
a ticket binding — so nothing that reasons about sessions has to know which kind
it is looking at.

What differs is the surface. A PTY session's surface is a byte stream and a
terminal grid; a conversation session's is an **envelope** stream going out and
**verbs** coming in. There is no grid, no scrollback, and no attach.

The process running the agent is its **host**. The daemon owns a host's lifetime
exactly as it owns a PTY worker's: it signals the host to tear down, and kills
its process group as the backstop, so nothing the agent started outlives the
session.

An **envelope** is one message from a host: a session id, a monotonic sequence
number, a `kind`, and a body. Kinds fall in two families, and the split is what
lets an agent's own vocabulary grow without the daemon changing:

- **Declarations** are what the daemon understands and acts on — `session_ready`,
  `run_started`, `run_settled`. These are the host telling attn something true
  about the session.
- **Renderings** are what the app draws — `message_start`, `message_delta`,
  `message_end`, `queue_update`. The daemon forwards them opaquely and holds no
  opinion about them.

Every declaration carries the attn state it puts the session in, so the daemon
reads state off the host rather than inferring it from the kind. That is what
makes a conversation session move through `working` and `idle` like any other
agent, and it is the path a future `pending_approval` travels on too.

A **run** is one prompt and everything the agent does in response to it, from
`run_started` to `run_settled`. A run is what a turn is opened and settled
around, the same way a PTY agent's stop is.

Three verbs put a message in front of the agent, and which one you use is a
statement about *when* it should be read:

- a **prompt** opens a run, and is what the composer sends when nothing is
  running;
- a **steer** cuts in at the agent's next turn boundary — the interruption, and
  what every attn doorbell (a ticket nudge, a chief nudge, a Present notice)
  becomes for a conversation session, since there is no PTY to type into;
- a **follow-up** waits for the run to finish and is read before it settles, so
  the agent never stops with one unread.

A steer or a follow-up sent to a session with no run open starts one. That is
what makes a doorbell safe to ring at any moment: nothing is dropped for having
arrived at the wrong time. What has been sent and not yet read is the session's
**queue**, which the host reports as it fills and drains — queued, then seen.

An agent becomes a conversation agent by its plugin driver registering the
`conversation` capability. Everything else about launching it — argv, env, cwd —
comes back from the same `driver.spawn` call a PTY-backed agent uses.
