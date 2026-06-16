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

Other agents *can* write to the journal (the `attn notebook journal append` CLI
stays available), but we do not ask them to and do not nudge them. In practice they
do not, and that is fine: automated capture (the keeper) is what keeps the journal
good. We do not try to prevent other writes — that is not enforceable at this
abstraction level — we simply say nothing about it.

The journal is **curated**: nothing machine-raw lands in it. Raw machine inputs
live in the raw tier and are consumed by the keeper, never pasted into the journal.

## The keeper

The single automated entity that **tends each workspace**. One persona, two duties:

1. **Keeps the workspace context tidy** — compacts/prunes `context.md` when it
   grows past threshold.
2. **Narrates the workspace's work into the journal** — turns the workspace's
   sessions (and delegated dispatch outcomes) into the curated per-workspace
   journal narrative.

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

## Dispatch / dispatch report

The chief delegates a unit of work to a sub-agent — a **dispatch**. The sub-agent
reports its outcome back to the chief as a structured **dispatch report** (a summary
plus a decision). A terminal dispatch report is captured deterministically into the
raw tier (`.attn/raw/dispatches/<id>.md`) and persisted in SQLite. The keeper reads
those raw dispatch outcomes and weaves the terminal ones into the workspace
narrative; the chief may also reference them in its cross-workspace journaling.

## The raw tier

Machine-internal capture under `.attn/raw/`, the keeper's **input**, never
user-facing and never part of the curated journal:

- `sessions/<sessionID>.md` — per-session digests (the summarize step's output).
- `dispatches/<dispatchID>.md` — captured dispatch outcomes.
- `context-snapshots/<wsID>.md` — the `context.md` snapshot taken synchronously at
  workspace removal (the deterministic data-safety floor, so the editorial overlay
  is never lost before the keeper can narrate it).

The raw tier is physically unreachable through the user-facing notebook APIs
(`CleanPath` rejects dotdir segments). Capture into it is deterministic and always
happens; the keeper's narration is best-effort on top of it, so nothing is lost if
narration never runs.
