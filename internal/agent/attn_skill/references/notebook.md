# Notebook

Load this reference when you read or write the attn Notebook — the durable,
profile-wide markdown memory — especially when your session is the chief of
staff. The Notebook outlives any single workspace; per-workspace shared context
(see the workspace-context reference) does not.

The daemon owns every attn write. Never edit notebook files directly on disk:
go through `"$ATTN_WRAPPER_PATH" notebook …` so writes stay atomic, serialized,
and observable.

## Orient First

Print the operating rules and your notebook root:

    "$ATTN_WRAPPER_PATH" notebook guide

Load what is already known before adding anything:

    "$ATTN_WRAPPER_PATH" notebook show /memory/index.md
    "$ATTN_WRAPPER_PATH" notebook list memory

Create the notebook once if it does not exist yet (idempotent):

    "$ATTN_WRAPPER_PATH" notebook init

## Two Kinds Of Notes

- `journal` — dated, append-only entries: the raw record of what happened.
- `memory` — distilled, durable notes worth keeping: decisions, gotchas, domain
  knowledge that outlived a single PR. They live under `memory/decisions/`,
  `memory/gotchas/`, and `memory/domain/`.

Memory is not a task tracker. Capture what is *known*, not what is *to do*.

## Append A Journal Entry

    "$ATTN_WRAPPER_PATH" notebook journal append --text "Shipped PR 318; chief now reads the notebook at launch."

Defaults to today; pass `--date YYYY-MM-DD` to backfill a specific day.

## Write Or Edit A Durable Note

Content comes from `--file` or stdin. To create a note:

    "$ATTN_WRAPPER_PATH" notebook memory write --path /memory/decisions/notebook-canonical.md --file /tmp/note.md

To edit safely, read first to get the current hash, then pass it as
`--base-hash`. The write applies only if the file is unchanged on disk;
otherwise it reports a conflict and you re-read and retry:

    "$ATTN_WRAPPER_PATH" notebook show /memory/decisions/foo.md
    # edit, then:
    "$ATTN_WRAPPER_PATH" notebook memory write --path /memory/decisions/foo.md --base-hash <hash> --file /tmp/foo.md

## Rules That Make Memory Trustworthy

- **Grounding is required.** Every durable `memory` note must carry resolvable
  `sources:` in its frontmatter — journal anchors, `dispatch:<id>`, or URLs. Do
  not author memory from paraphrase alone.
- **Frontmatter:** `kind:` is the one required field (`journal` or `memory`).
  `title`, `summary`, `tags`, `created`, `updated`, and `sources` are
  recommended. Unknown keys are preserved untouched.
- **Links are root-absolute markdown:** `[label](/memory/decisions/foo.md)`, not
  `[[wikilinks]]`. Keep the relationship kind (supersedes, relates-to) in prose.
- **Be concise.** Notes are read by future agents under a token budget; prefer a
  tight summary plus a `sources:` pointer over a transcript.

## As Chief Of Staff

The Notebook is your home. When you are promoted to the role mid-session, attn
prompts you to run `notebook guide`; follow it. Read `memory/index.md` to orient,
record durable decisions there as you make them, and keep the day's journal
current. You remain profile-wide — you may `attn workspace context show
--session <id>` for a specific workspace you step into, but that is opt-in.
