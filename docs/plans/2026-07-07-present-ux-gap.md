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
  semantics need protocol + `attn present feedback` output wording (slice 7).
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

- [ ] 1. **Tour scroll on CodeView**: `PresentTour` component, summary card,
      per-file sticky headers with note callouts, comment machinery ported at
      full parity with today's per-file DiffView (multi-draft, hover `+`,
      Escape LIFO, scroll-pin equivalent), rail click scrolls to the file.
- [ ] 2. **Progress + keyboard + DriveBar**: J/K/R, reviewed collapse,
      kbd badges, localStorage persistence, coverage in submit dialog.
- [ ] 3. **Rail anatomy**: ± stats, note/annotation dots, pinned summary row,
      scroll-synced active state.
- [ ] 4. **Grouping**: daemon changed-files list → Other group (protocol).
- [ ] 5. **Annotations**: manifest schema + store/protocol + inline threads
      (expect multiple PRs).
- [ ] 6. **Diagrams**: mermaid in the shared markdown path.
- [ ] 7. **Submit**: approve / submit feedback / close (protocol + CLI).
- [ ] 8. **Polish pass** + the minor items above.

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
- Per-file ± stats source: daemon returns stats in the round payload vs client
  computes from lazily fetched diffs — decide in slice 3 (lean daemon).
- Approve semantics on round/session state — decide in slice 7.
