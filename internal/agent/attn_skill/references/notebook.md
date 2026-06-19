# Notebook

Load this reference when you read or maintain the attn Notebook — the durable,
profile-wide markdown store — especially when your session is the chief of staff.
The Notebook outlives any single workspace; per-workspace shared context (see the
workspace-context reference) does not.

The Notebook is plain markdown on disk, and you maintain it by **editing the files
directly with native tools** (Read/Write/Edit, plus `ls`/`grep` over the tree).
There is no `attn notebook` CLI. The notebook root is given to you in your
operating guidance (the chief-of-staff launch injection); it is
`~/attn-notebook` by default (per-profile variants append the profile name). Paths
below are written relative to that `<root>`.

## Orient First

Load what is already known before adding anything — read the index files:

    <root>/index.md             # the bundle guide
    <root>/knowledge/index.md   # the knowledge-base nav

Then browse the relevant subtree (`ls`/`grep` under `<root>/knowledge/...` or
`<root>/journal/`).

## Two Layers

- **The journal** — `<root>/journal/<date>.md`, dated entries: the durable,
  curated, cross-workspace log of what was done in attn, kept for the user's
  recall and reviews (not a raw dump — raw machine inputs stay in the raw tier,
  never the journal). Entries carry `type: journal`.
- **The knowledge base** — `<root>/knowledge/`, distilled, timeless notes worth
  keeping: decisions, gotchas, domain knowledge that outlived a single PR. It is
  organized **PARA-style**: `projects/` (bounded efforts, roughly one per
  workspace/epic), `areas/` (ongoing responsibilities and subsystems),
  `resources/` (reference material), `archive/` (inactive items). As a project
  finishes, promote its durable knowledge up into `areas/`.

When a `projects/<slug>/` folder corresponds to a workspace, stamp its `index.md`
frontmatter with `resource: attn:workspace/<id>`. That link lets the keeper file
the folder under `archive/` automatically when the workspace is removed — so
promote anything durable into `areas/` before then, since archived notes drop out
of the active view.

The knowledge base is not a task tracker. Capture what is *known*, not what is
*to do*.

## Append A Journal Entry

Open today's file (`<root>/journal/<YYYY-MM-DD>.md`), creating it if absent, and
append your entry. A new file carries OKF frontmatter:

    ---
    type: journal
    title: 2026-06-18
    ---

Then add a short, dated, prose entry. Backfill a specific day by editing that
day's file directly.

## Write Or Edit A Knowledge Note

Create a file under the right PARA directory, e.g.
`<root>/knowledge/areas/notebook.md`, with OKF frontmatter and a tight body:

    ---
    type: note
    title: Notebook is filesystem-canonical
    summary: The .md files on disk are the source of truth.
    sources:
      - /journal/2026-06-18.md
    ---

    The Notebook's markdown files are canonical; edits are plain file writes.

To edit, just Read then Edit the file — ordinary on-disk edits. The daemon's
filesystem watcher notices your change and refreshes any open in-app browser.

## Rules That Make The Knowledge Base Trustworthy

- **Grounding is required.** Every durable knowledge note must carry resolvable
  `sources:` in its frontmatter — journal anchors, `dispatch:<id>`, or URLs. Do
  not author a note from paraphrase alone.
- **Frontmatter is OKF.** `type:` is the one required field — an open,
  author-chosen string (e.g. `note`), not a fixed enum. `title`, `summary`,
  `tags`, `created`, `updated`, and `sources` are recommended. Unknown keys are
  preserved untouched. The reserved files `index.md` and `log.md` carry no
  frontmatter.
- **Links are root-absolute markdown:** `[label](/knowledge/areas/foo.md)`, not
  `[[wikilinks]]`. Keep the relationship kind (supersedes, relates-to) in prose.
- **Be concise.** Notes are read by future agents under a token budget; prefer a
  tight summary plus a `sources:` pointer over a transcript.

## As Chief Of Staff

The Notebook is your home. When you are promoted to the role mid-session, attn
points you at `<root>/index.md` — read it to orient. Read `<root>/knowledge/index.md`,
record durable decisions in the knowledge base as you make them, and keep the
day's journal current with your cross-workspace view. The keeper already narrates
each workspace's own work into the journal, so write at a chief-of-staff
altitude — what moved across workspaces, what you delegated and decided — not a
per-workspace play-by-play. You remain profile-wide — you may `"$ATTN_WRAPPER_PATH"
workspace context show --session <id>` for a specific workspace you step into, but
that is opt-in.
