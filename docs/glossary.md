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

## Ticket

The chief delegates a unit of work to a sub-agent, and that work is tracked as a
**ticket** bound to the delegated session (the session is the ticket's assignee).
The agent reports its own **work state** — in progress, needs input, ready for
review, completed, or failed — which moves the ticket across the board
(Todo · Working · Blocked · In Review · Done). Comments, status changes, and
handover attachments accumulate on the ticket's activity thread, and the chief
watches progress from the ticket view and board rather than polling the agent.

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
