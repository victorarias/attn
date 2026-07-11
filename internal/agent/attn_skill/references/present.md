# Authoring a Present review

Present is an agent-triggered guided review that opens inside attn's own
window — not a GitHub PR flow. The artifact you write is a small YAML
manifest that names an explicit **frame**: a repo path plus a `base` and
`head` git ref. Any set of changes fits: a feature branch against `main`, a
docs-only diff, an incident hotfix against the commit before it. There is no
PR to resolve and nothing to fetch from GitHub.

Use this whenever you want a human to review a change you made — in the
current session or a past one — with more structure than "here's the diff."

## Name the reader's job first

Before writing a single note, decide what you want the reviewer to walk away
able to do. Most of the time this is implicit and doesn't need stating: a
PR-shaped review or a branch-vs-main review defaults to "decide this is
correct and safe to merge." Don't write that mission out — it's the default
and stating it is noise.

Override the default when the shape demands something else:

- A risky migration or an invariant-changing PR: the mission is "understand
  the failure modes and confirm the safeguards," and the tour should spend
  its annotations proving those safeguards, not walking the happy path.
- A mechanical rename or a dependency bump: the mission is "confirm nothing
  else changed" — two stops and a long skip list, not a tour.
- Anything that isn't PR-shaped at all (an incident diff, a design doc's
  actual code, a spike you want sanity-checked before continuing): state the
  mission explicitly in the summary, in one sentence, because the reader
  can't infer "decide to merge" from a manifest that isn't a merge decision.

## The manifest

Default filename `.present.yml` in the repo `attn present` runs from
(`--manifest <path>` overrides it). Manifest schema is v1, `kind: changes`.
Unknown fields are rejected at parse time — don't invent fields that "seem
like they should exist."

````yaml
version: 1
kind: changes
title: Reconcile slice classifier
frame:
  repo: /Users/victor/projects/victor/attn      # must be absolute
  base: main
  head: feat/reconcile-slice-classifier          # base/head pinned to SHAs when opened

summary: |
  Reshapes reconcile into a deterministic transcript slice plus a single
  tool-less Haiku call. Mission: confirm the slice boundary can't leak future
  turns into the classifier's view.

  ```mermaid
  flowchart LR
    Transcript --> Slicer[Deterministic slice]
    Slicer --> Classifier[Haiku, no tools]
    Classifier --> Decision
  ```

files:
  - path: internal/reconcile/slice.go
    note: >
      The slice boundary. Everything downstream trusts that this never
      includes a turn that started after the reconcile point.
    annotations:
      - anchor: "func SliceTranscript"
        note: Cuts on tool-result boundaries, not turn boundaries — a mid-turn tool call can't split the slice.
      - anchor: "cutoffIndex"
        thread:
          - "This is the only place cutoffIndex is computed; everything else takes it as given."
          - >
            Went back and forth on cutting by wall-clock timestamp instead —
            dropped it because transcript timestamps aren't monotonic across
            a resumed session, and this boundary has to be exact.

  - path: internal/reconcile/classify.go
    note: Tool-less single call. `--allowedTools ""` is load-bearing here — see the annotation.
    annotations:
      - start: 40
        end: 52
        note: The whole prompt. No system tools reach this call by design; keep it that way if you touch this block.

skip:
  - internal/reconcile/testdata/golden.json
````

### Schema reference

- `version: 1`, `kind: changes`, `title` — all required.
- `frame.repo` (required, absolute path), `frame.base` / `frame.head`
  (required git refs — branch names, tags, or SHAs; attn pins both to SHAs
  the moment the presentation opens, so the reader always sees a stable
  diff even if the branch moves later).
- `summary` — optional, markdown, rendered as stop 0.
- `files[]` — optional, in reading order. Each has `path` (relative, no
  duplicates, can't escape the repo), an optional `note` (markdown), and
  optional `annotations`.
- `skip[]` — optional, relative paths, must not also appear in `files`.
- `annotations[]` on a file entry — exactly one anchor form and exactly one
  body form, no more, no less:
  - anchor form: `anchor: "substring"` (min 3 chars, first match on the
    head-side file wins), or `line: N` (1-based, head side), or `start: N` +
    `end: M` (inclusive range).
  - body form: `note: "..."` (a single comment) or `thread: [...]` (an
    ordered list of comment strings, no empty entries).
  - Two line/range annotations in the same file whose ranges overlap are
    rejected at parse time — don't stack them.
- There is no `view:` field and no `{author, body}` thread form — those are
  jaunt-only. Present always diffs (no whole-file mode yet) and every
  annotation comment is authored by you, the agent, under one byline.

### What the reader does with files you didn't name

The rail groups cards into three sections: **Tour** (your `files`, in your
order), **Other** (files that changed between the pinned SHAs but that you
didn't call out — attn computes this automatically and appends them as
plain diff cards, no note, no annotations), and **Skipped** (your `skip`
list, dimmed, always last). Forgetting a file is safe: it lands in Other,
not lost, and doesn't cost you anything more than the reviewer working
through it without your commentary. So don't feel obliged to name every
file — name the ones that carry the design.

### YAML authoring: `>` for prose, `|` for structure

This bites often enough to deserve its own callout. For any `note:` or
`thread:` item that runs more than one line, use a **folded** block (`>`):

```yaml
note: >
  Went back and forth on retry-on-conflict — dropped it in the end because
  it breaks idempotency when the caller's a webhook, and here it almost
  always is.
```

- **`>` (folded)** — prose. Wrap freely at ~80 cols for readability; the
  reader sees one paragraph. Use this for almost every multi-line `note:`,
  `summary:`, and thread item. A blank line still breaks paragraphs.
- **`|` (literal)** — the linebreaks are meaningful: a mermaid block, a
  one-bullet-per-line list, ASCII art. Rare, but required for mermaid — see
  below.
- **A plain quoted string** — a short note that fits on one line. Simplest;
  no block scalar needed.

### Markdown is rendered

`summary:`, file `note:`, and annotation/thread bodies all render as
GitHub-flavored markdown. Use it where it earns its keep:

- **Lists** for genuine enumerations — one-per-line bullets read better than
  comma-separated prose.
- **Inline `code`** for symbol names, flags, constants — always backtick
  them.
- **Fenced code blocks** sparingly; the diff is right there.
- **Tables** for actual matrices, not fake-tabled prose.
- **Bold** for the one word in a paragraph the reader must not miss.

Headings inside notes are usually wrong — the file card already has
structure. Reach for one only inside a long `summary:` that earns sections.

### Mermaid diagrams

Fenced `mermaid` blocks render in the summary, file notes, and
annotation/thread bodies. A diagram is one of the highest-leverage things
you can put in a tour: the reader absorbs the *shape* of a change — what's
new, how it connects — in two seconds, before reading a single note.

**Default to a diagram in the summary** for any change that introduces or
rearranges connections between components — most non-trivial changes do.
Per-file diagrams are rarer, worth it when one file introduces a non-trivial
sub-shape (a state machine, a multi-branch router, a pipeline).

Pick the type to match the change:

| change shape | diagram |
|---|---|
| architecture / data flow | `flowchart LR` or `flowchart TD` (the default) |
| enum-driven aggregate or explicit state machine | `stateDiagram-v2` |
| timing/ordering across services is the point | `sequenceDiagram` |
| a new type hierarchy or interface lattice | `classDiagram` |

Skip the diagram when the change is small and linear (one file, one
concept, nothing new connects to anything), or when the diagram would just
relabel the file list with no extra structure — decoration, not signal.

````yaml
summary: |
  Splits the resolver into a domain core and a Postgres adapter.

  ```mermaid
  flowchart LR
    HTTP --> Service --> Resolver[Resolver core] --> Adapter[Postgres adapter]
  ```
````

Note the outer `|` — the diagram's linebreaks are meaningful; folded `>`
would collapse them and mermaid would refuse to render.

Two attn-specific caveats jaunt doesn't have: `attn present validate` does
**not** parse mermaid syntax today, so double-check your diagram source
yourself before opening. And a broken diagram doesn't fail the presentation
— the reader just sees an inline error plus the raw source as fallback, so a
syntax mistake degrades gracefully but still looks sloppy. Check it anyway.

## Voice

The tour is pedagogic — it walks the reviewer through *your* mental model so
they arrive where you already are. Picture the reader: smart, motivated,
landing cold. They haven't read the plan doc. They don't know which choices
were contested. They want to leave with the mental model you have, without
doing the archaeology you did to build it. The bar is "did I leave the
reader with the right mental model," not "did I describe the diff
accurately."

Five rules, in order:

1. **Teach, don't list.** "Enums first — the rest of the system keys off
   these states" teaches. "Adds Status and Priority enums" lists. The diff
   already lists.
2. **Be concise — not by compressing meaning.** A note that takes 5 lines to
   say what 2 would is padded; cut it. But if a file genuinely needs four
   sentences to bridge "I see what changed" to "I see why it had to be this
   way," write four sentences. The yardstick is the reader, not a word
   count.
3. **Assume a smart reader.** No hand-holding on standard patterns. Explain
   what's non-obvious — the invariant, the tradeoff, the constraint you bowed
   to — and trust the reviewer on the rest.
4. **Carry the concept; let tags trail.** A plan-doc reference (`INV-5`,
   `DT-2`) is a pointer, not a stand-in for the idea. Lead with the concept,
   then add the tag as a footnote: "First-writer-wins is enforced here
   (INV-5)," not "Implements INV-5."
5. **Sound like a friendly engineer, not a textbook.** Loose register,
   contractions, first person where it helps ("I almost went the other way
   here"). Avoid stiff openers ("This module provides…"), smugness, and cold
   one-line pronouncements.

A good tour feels like a sharp colleague walking you through the codebase at
a whiteboard. A bad tour feels like a compliance checklist.

One non-goal, stated once so it doesn't creep back in: a tour is not a
lesson plan. There's no comprehension check, no curriculum, no quiz. You're
handing the reviewer a mental model to use immediately, not testing whether
they retained it.

## Workflow

### 0. How did you get here?

- **Same session that produced the change** — you already have the diff,
  the decisions, and the plan doc in context. Don't re-explain what was
  already discussed and decided; the reviewer wasn't there for that
  conversation, but the tour's job is to hand them your mental model, not a
  transcript of your working session. Spend your annotations — especially
  `thread:` — on what emerged *during implementation* that isn't visible
  from the plan: the thing that turned out harder than expected, the
  alternative you tried and abandoned, the edge case you found by testing.
  This is the highest-leverage move available to a same-session author, and
  only you know which choices were actually contested.

- **Fresh session, or reviewing someone else's change** — you know nothing
  yet. Never author from parametric memory of what a diff "probably" does.
  Read the actual artifacts:
  - Diff the full range (`git diff <base>..<head>` in the frame's repo) and
    read all of it before writing a single note.
  - For every file you plan to annotate, read its head-side content in
    full (`git show <head>:<path>`) — an annotation requires knowing exactly
    where the invariant or decision lives, not just that the file changed.
  - If a plan or design doc exists for the change, read it in full; it
    usually carries the *why* — constraints, rejected alternatives, open
    questions — that belongs in your summary or file notes.

### 1. Understand the shape

Count files, group by layer (domain / ports / services / adapters / tests /
migrations / generated). This shapes everything downstream.

Small, mechanical changes (a rename, a dependency bump, formatting) usually
don't need a tour at all — the mission override above covers this case: two
stops and a skip list, or skip the tour entirely and just note that in the
session. Small but consequential changes (a new invariant, a subtle race
fix) are exactly where a short tour with 1–2 annotations earns its keep.
Large changes (30+ files) are where a tour earns its keep the most — budget
your reading; you don't have to read every file, that's what the reviewer
is for, but you do have to read the ones you tour.

### 2. Decide the reading order

Default shape is **domain-outward** — concepts, then behavior, then wiring:

1. Plan/design doc (if one exists) — ground truth for invariants and
   decisions
2. Domain model / aggregates / enums
3. Inbound + outbound ports / contracts
4. Service / use-case layer — the behavior
5. Service-layer tests — the behavior's executable spec
6. Persistence adapter / migration
7. Integration tests (real DB / real dependency)
8. Realtime / eventing / orchestration if touched
9. Wiring (HTTP, gRPC, CLI, protocol)
10. End-to-end tests

Adapt for the shape of the change. A frontend-heavy change reads types →
domain modules → components → hooks → route wiring → component tests. An
infra change reads migrations → config → handlers.

### 3. Skip discipline

Put generated files, lock files, and huge fixtures in `skip` rather than
leaving them to fall into Other — they still show up (dimmed, at the end),
but naming them tells the reviewer you looked and they're inert. You don't
need to enumerate every non-tour file this way; only the ones that would
otherwise look like an oversight if the reviewer stumbled on them
unexplained. Everything else you didn't mention lands in Other automatically
— that's the safety net, not a place to actively curate.

### 4. Write the notes

Each note answers "what should the reviewer pay attention to here, and
why?" — not "what changed" (the diff already shows that). Good notes
reference invariants, the tradeoff behind a shape, why this file matters
now, or a cross-reference telling the reader where to look next.

Length scales with conceptual lift, not a target line count: a thin or
mechanical file gets one line or none. A file with a moderate idea gets 2–3
lines. The file the change hinges on — the service, the aggregate, the plan
— can run 4–8 lines if the lift genuinely warrants it; past that, ask
whether the load belongs in the summary instead. A note that restates the
diff is padding — cut it.

### 5. Add annotations

Reserve `annotations` for the files that carry the design — the ones where
you'd otherwise say "scroll down to line 40, see that function?" in person.
1–5 per consequential file; more than that usually means the file note
should carry more of the weight instead.

An annotation is a pin, not a transcript — the reader already sees the
anchored line rendered inline above your comment. Add what the line doesn't
carry: why it matters, the invariant it enforces, the constraint that forced
the shape. If the line already says everything, don't pin it.

Each annotation teaches exactly one concept — one invariant, one tradeoff,
one decision. If a stop needs two ideas, that's two pins, not one crowded
one; push a second idea into the file note instead if it doesn't warrant its
own line.

Prefer `anchor` over `line`/`start`+`end` — anchors survive edits between
when you write the manifest and when the presentation opens. Pick anchors
that are distinctive: a function signature, a specific constant, a heading
with enough surrounding text that it can't collide. **Verify every anchor**
before committing to the manifest — the resolver takes the *first* substring
match in the head-side file, silently, with no way for the reader to know
it pinned the wrong line. If there's any chance of ambiguity (two functions
sharing a prefix, a heading repeated across sections), lengthen the anchor
until it's unambiguous. `attn present validate` re-checks every anchor
against the actual pinned head content and will flag anything that fails to
resolve or resolves ambiguously — run it, don't guess.

Use `thread:` instead of `note:` when a decision is likely to draw a "why
not X?" — the first entry states the point, the next pre-empts the
counter-argument with the rejected alternative and the constraint that
forced your hand:

```yaml
- anchor: "func (s *Service) Resolve"
  thread:
    - "First-writer-wins lives here. The CAS is what actually enforces it."
    - >
      Went back and forth on retry-on-conflict — dropped it because it
      breaks idempotency when the caller's a webhook, and here it almost
      always is.
```

Both entries render under your byline (there's no distinct reviewer voice in
a thread until the reviewer replies) — that's expected, not a limitation to
work around. This is the highest-leverage annotation form for same-session
authors, since you're the only one who knows which alternatives were
rejected and why.

### 6. Write the summary

3–5 lines. Cover: what the change is, in one phrase; the reading strategy in
shorthand ("domain-outward: model → service → persistence → wiring"); the
mission, if it's not the "decide to merge" default; anything that would
surprise the reviewer (e.g. "generated files are skipped at the bottom").
Include a diagram by default per the mermaid guidance above.

## Validate, open, feedback, rounds

```bash
attn present validate --manifest .present.yml
```

Parses and schema-validates locally, no daemon call. If the manifest has any
annotations, it also resolves the frame's refs to SHAs and checks every
anchor against the actual head-side content — the same check the daemon
does at open time. Errors (unresolvable anchor, line past EOF, schema
violations) fail the command; warnings (ambiguous anchor match) print but
don't. Iterate until clean before opening — a broken anchor or a schema
mistake should never reach the reviewer's screen.

```bash
attn present --manifest .present.yml --wait
```

Validates again, hands the manifest to the daemon, which pins `base`/`head`
to SHAs and opens the presentation — a banner notice plus a Present window
chip appear in the app. **When you are Codex, always include `--wait` and keep
this foreground tool call open while the reviewer works.** It blocks
synchronously until the round is submitted or the presentation is closed,
then returns the outcome on stdout. Submitted feedback is printed as markdown,
so use that command result as the review handback and continue the same turn
from it.

To inspect feedback from an older or non-waiting presentation explicitly:

```bash
attn present feedback <presentation-id> [--round <n>] [--json]
```

Prints the round's feedback as markdown (defaults to the latest round).

**Adding a round.** If the reviewer asked for changes, make them, then open
a new round on the same presentation:

```bash
attn present --manifest .present.yml --presentation <presentation-id> --wait
```

Author round N+1 as the **delta** since the last submitted round — what
changed, what feedback you addressed — not a re-tour of the whole change
from scratch. The reviewer already walked the base; don't make them re-walk
it.

## Edge cases

- **No plan or design doc:** the first tour entry is the domain model
  itself.
- **Pure test change:** reading order is the test file, then the helpers it
  exercises.
- **Pure mechanical refactor (rename, dependency bump, formatting):** a tour
  is usually low value — skip it, or write a two-stop tour and lean on
  `skip` for the mechanical churn, per the mission override above.
- **Generated files dominate the diff:** skip everything generated; say so
  in the summary so the reviewer doesn't wonder why the file count and the
  tour length don't match.
