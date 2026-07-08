# Plan: Present — reader UX gap map (jaunt parity)

## Why / Alignment

Victor's direction (2026-07-07): map all UX gaps between Present's change
reader and jaunt's reader, then chip away in small PRs. Parent vision:
[docs/vision/present.md](../vision/present.md) — pedagogy over inventory;
cognition-friendly UX is a non-negotiable. Source for the jaunt side: a full
reader-UX survey of `~/projects/victor/jaunt` (citations below are into that
repo's `web/src/`).

Victor's priorities, verbatim buckets:

- **Must**: tour scroll, progress model, keyboard model, rail anatomy.
- **Critical**: author annotations, file grouping for big PRs, diagrams.
- **Simplified vs jaunt (deliberate)**: submit is just approve / submit
  feedback / close; no agent-channel view (vision non-goal — conversation
  stays in the session terminal); keep attn's current visual identity but
  polish it.
- **Good but minor (later)**: mid-review round submits, what's-new since a
  submit, collapse-on-review, deleted-line context.

## Gap map

### Must-have

1. **Tour scroll** — one continuously scrolling document instead of
   click-a-file-see-a-diff: summary card as stop 0 (summary markdown +
   warnings callout + reading-order hint + files/± meta strip), one diff card
   per file in reading order with its note as a callout, an "end of tour"
   footer. jaunt: `App.tsx:338-390`, `SummaryCard.tsx`, `FileCard.tsx`.
2. **Progress model** — per-file `reviewed` flag: `R` toggles it, advancing
   with `J` marks the file being left, reviewed cards collapse, progress bar +
   N/M counts in the rail header, coverage list in the submit dialog ("not yet
   walked: a.ts, b.ts" — advisory, never a blocker). Durable per
   (presentation, round): localStorage first, daemon-side only if a real need
   appears. jaunt: `hooks/useDraft.ts:63-150`,
   `hooks/useTourNavigation.ts:115-163`.
3. **Keyboard model** — `J`/`K` next/prev stop, `R` reviewed, `N`/`P`
   annotation hops, `S`/`⌘Enter` submit, `←`/`→` collapse/expand; single-letter
   keys gated while typing; every actionable button wears its key as an inline
   kbd badge (no separate legend). jaunt: `App.tsx:182-245`.
4. **Rail anatomy** — the rail becomes a table of contents synced to scroll:
   zero-padded index, path, +/− stats, note dot, annotation count,
   active/reviewed styling, pinned "00 · summary" row. jaunt:
   `Sidebar.tsx:54-176`.

### Critical

5. **Author annotations** — manifest-carried line-pinned annotations/threads
   rendered inline under their lines, in the same visual language as
   `DiffCommentThread`; a reviewer reply becomes a comment on the round. Needs
   manifest schema + store + protocol + reader work. This is the vision's
   "full tour guidance" rock. jaunt: `Thread.tsx`, `DiffView.tsx:38-65`.
6. **File grouping** — Tour / Other / Skipped groups so big PRs stay
   navigable. "Other" = files changed between the pinned SHAs but not named in
   the manifest; the daemon computes the changed-file list at round load
   (protocol addition to `get_presentation_round`). jaunt:
   `Sidebar.tsx:178-192`.
7. **Diagrams** — mermaid rendering in summary, file notes, and annotation
   bodies. The vision's "rendering palette" rock (the shared markdown renderer
   also pays the ticket-board rendering debt).

### Simplified vs jaunt (deliberate)

- **Submit** = three actions: Approve / Submit feedback / Close. No GitHub
  target toggle, no three-verdict picker, no end-review checkbox. Approve
  semantics (protocol + `attn present feedback` output wording) shipped in
  slice 8.
- **No agent channel / transcript view** — vision non-goal.
- **Visual identity**: keep attn's; polish density/spacing/states. No jaunt
  retheme (its terminal-green mono aesthetic stays jaunt's).

### Good but minor (later)

- Mid-review submits that keep reviewed marks and ship only new feedback
  (jaunt `useDraft.ts:133-150`).
- What's-new-since-round view (vision rock "rounds & drift" — jaunt does not
  have this either).
- Collapse-on-review persistence across reload.
- Deleted-line context in whole-file views (jaunt `lib/hunkOverlay.ts`).

## Authoring guidance (the other half)

The reader UX above is half the product; the other half is the discipline
agents follow when authoring a presentation. Source: jaunt's authoring skill
(`~/projects/victor/jaunt/skill/SKILL.md`, 418 lines) — the donor product's
crown jewel. Captured here so the port doesn't get lost behind the reader work.

**Ports nearly verbatim:**

- The five voice rules, in order: teach don't list; be concise without
  compressing meaning; assume a smart reader; carry the concept and let
  plan-doc tags (`INV-5`, `DT-2`) trail as footnotes; sound like a friendly
  engineer. "A good tour feels like a sharp colleague walking you through the
  codebase at a whiteboard. A bad tour feels like a compliance checklist."
- Reading-order doctrine: domain-outward default (plan doc → domain model →
  ports → service → service tests → persistence → integration tests →
  realtime/orchestration → wiring → e2e), adapted per PR shape; always-skip
  discipline for generated files.
- Annotation hygiene: an annotation is a pin, not a transcript — it adds the
  *why* the line doesn't carry; 1–5 per consequential file; prefer distinctive
  substring anchors over line numbers; **verify every anchor** (first-substring
  match resolves silently to the wrong line if ambiguous); annotation bodies
  stay 1–2 lines, longer context belongs in the file-level note.
- `thread:` for contested decisions — first comment states the point,
  follow-ups pre-empt the likely "why not X?" with the rejected alternative
  and the constraint that forced the choice. Highest-leverage form for
  same-session authors, who know which choices were contested.
- Mermaid **by default** in the summary whenever the change touches how
  components connect (flowchart/stateDiagram/sequenceDiagram/classDiagram by
  change type); skip only when the change is small and linear or the diagram
  would just relabel the file list.
- Note length scales with the reader's conceptual lift, not a word count; a
  note that restates the diff is padding, a note that teaches the constraint
  or rejected alternative is the point.
- Validate before handing to the reviewer — broken anchors/diagrams must not
  reach the reviewer's screen.

**Changes for attn:**

- The artifact is the Present manifest with an explicit repo/base/head frame —
  no GitHub PR resolution (jaunt's PR-ref section drops entirely).
- jaunt's server/sentinel/launch machinery (its steps 9–11: `LISTENING`,
  `FEEDBACK_READY`/`AGENT_ASK_READY` sentinels, hand-off vs wait-and-act,
  re-launch loop) is replaced wholesale by `attn present` + the doorbell +
  `attn present feedback` — attn's session model already covers the loop.
- `jaunt validate`'s error classes (paths not in the diff, unresolvable or
  ambiguous anchors, files∩skip overlap, mermaid syntax errors, per-field
  reporting) become `attn present validate` (or validation inside
  `attn present`) once anchors/annotations exist in the manifest.
- Distribution: embed the skill in the daemon (like the delegation skill's
  `//go:embed`) so agents get it without per-repo setup — merge ≠ shipped,
  rebuild the daemon.

**Additions from reviewing mattpocock's `teach` skill (2026-07-08):** one set of
principles for every presentation kind — not a separate rulebook per artifact.

- **Mission-first.** The skill's opening move: name the reader's job for this
  presentation (merge decision, absorb a design, choose between options,
  understand an incident) and structure the stops around that job. PRs get a
  strong default mission — "decide this is right and safe to merge" — plus
  guidance on overriding it (a risky migration tours the danger; a mechanical
  rename is two stops and a skip list). Non-PR presentations must state the
  mission explicitly; there is no default.
- **Teach the delta (zone of proximal development).** Calibrate to what the
  reader already holds. A same-session author knows what was already discussed
  and decided — don't re-teach the agreed design; spend annotations on what
  emerged during implementation (surprises, local decisions, places the plan
  bent). Rounds are pure delta: round N+1 presents drift since the last
  submit, not the whole tour again.
- **Stop budget / one concept per stop.** A tour is a sequence of small
  comprehension wins, not exhaustive coverage — working memory is the
  constrained resource. Sharpens jaunt's "teach, don't list" for big PRs.
- **Never present from parametric memory.** Re-read the actual diff/artifacts
  before authoring; verify every anchor. The *why* behind the existing
  validate-before-handoff rule.
- **Explicit non-goal:** the goal is comprehension sufficient to decide, not
  retention. None of the curriculum apparatus (quizzes, retrieval practice,
  spacing, learning records) applies — Present is a one-shot guided read
  ending in a decision.

**Sequencing:** an interim level-0/1 version (voice rules, reading order,
notes, summary, skip) is useful immediately — manifest v0 already supports
everything it prescribes. The full port (annotations, `thread:`, mermaid,
validate) waits on slices 5–6.

## Architecture (slice 1 target)

Key discovery: `@pierre/diffs` ships **CodeView**
(`dist/react/CodeView.d.ts`) — a multi-file virtualized scroll coordinator:
controlled `items` (one `CodeViewDiffItem` per file), sticky file headers,
`renderCustomHeader`/`renderAnnotation`/`renderGutterUtility` shared with the
single-file API `DiffView` already uses for comment threads, a
`scrollTo({item/line})` handle, and an `onScroll` callback. The tour scroll is
therefore a **port of DiffView's comment machinery onto CodeView**, not a
hand-rolled stack of DiffViews.

```text
Current:
PresentRoot
  -> rail (click selects one file)
  -> main pane -> one DiffView (own scroller)

Target:
PresentRoot
  -> header (title, round strip)                     [exists]
  -> body grid: rail | tour
     rail = table of contents                        [upgrade, slice 3]
     tour = <PresentTour>                            [new, slice 1]
        SummaryCard (stop 0)
        <CodeView items=[one diff item per file]>    (owns the scroll)
        end-of-tour footer
  -> DriveBar footer (prev/next, counter, submit)    [slice 2]
```

State: per-anchor comment drafts stay as shipped in PR #502, additionally
keyed by file via `CodeViewLineSelection {id, range}`. Reviewed marks (slice
2): `Record<path, bool>` in localStorage keyed
`present.reviewed.<presentationId>.<roundId>`.

## Implementation Steps (slices, one small PR each)

- [x] 1. **Tour scroll on CodeView**: `PresentTour` component, summary card,
      per-file sticky headers with note callouts, comment machinery ported at
      full parity with today's per-file DiffView (multi-draft, hover `+`,
      Escape LIFO, scroll-pin equivalent), rail click scrolls to the file.
- [x] 2. **Progress + keyboard + DriveBar**: J/K/R, reviewed collapse,
      kbd badges, localStorage persistence, coverage in submit dialog.
- [x] 3. **Rail anatomy**: ± stats, note/annotation dots, pinned summary row,
      scroll-synced active state.
- [x] 4. **Grouping**: daemon changed-files list → Other group (protocol).
- [ ] 5. **Annotations**: manifest schema + store/protocol + inline threads
      (expect multiple PRs).
    - [x] 5a. manifest schema + resolution + protocol
    - [x] 5b. reader: inline threads, reply-as-comment, N/P, rail counts
- [x] 6. **Diagrams**: mermaid in the shared markdown path.
- [x] 7. **Authoring skill**: interim level-0/1 version (voice, order, notes,
      skip) can land any time; full port (annotations, `thread:`, mermaid,
      `attn present validate`) after slices 5–6; ships via daemon embed.
- [x] 8. **Submit**: approve / submit feedback / close (protocol + CLI).
- [ ] 9. **Polish pass** + the minor items above.

## Decisions

- Tour scroll builds on `@pierre/diffs` CodeView, not stacked DiffViews —
  cross-file virtualization is native and the annotation API is shared.
- The layout pivot lands before progress/keyboard so they are built on the
  final skeleton, not retrofitted.
- Reviewed marks persist in localStorage per round first; daemon persistence
  deferred until a cross-window/machine need shows.
- Submit simplification (approve/feedback/close) is a deliberate divergence
  from jaunt's GitHub-oriented dialog.

## Open Questions

- Where the summary card lives relative to CodeView's owned scroller (leading
  child inside the scroll container vs a collapsible card above it) — resolve
  in slice 1 by checking what CodeView's container tolerates.
- Scroll-pin: does CodeView's anchor logic have the same cold-window
  autonomous-scroll bug fixed for the single-file Virtualizer in PR #502?
  Verify on a cold present-window load during slice 1.
- Per-file ± stats source: **resolved in slice 3** — the daemon returns stats
  in the round payload (lean daemon), not client-computed from lazily fetched
  diffs.
- Approve semantics on round/session state — **resolved in slice 8**: Approve
  submits the current draft round with `verdict: "approved"` (comments still
  allowed alongside — approve-with-nits) and, in the same transaction, flips
  the presentation's `status` from `open` to `approved`; handback fires as
  usual. Submit feedback is the existing path with `verdict: "feedback"` and
  leaves `status` at `open`. Close skips round submission and handback
  entirely — drafts are discarded client-side — and just moves `status` to
  `closed`; closing a presentation that isn't `open` (already closed or
  approved) is an error. No interplay with session `pending_approval`. Opening
  a new round reopens an approved or closed presentation back to `open`,
  since a new round is a new ask for review.
