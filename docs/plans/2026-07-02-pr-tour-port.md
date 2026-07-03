# Plan: PR tours in attn (jaunt port) — direction plan

> **SUPERSEDED (2026-07-04)** by [docs/vision/present.md](../vision/present.md).
> The design pivoted: instead of overlaying a tour on the existing diff panel,
> **Present** replaces that panel with an agent-triggered presentation surface
> (own window, rounds, doc annotation, teaching). The jaunt inventory below
> remains accurate and useful; the decisions and slices do not govern anything.

## Goal

Port jaunt into attn: a delegated agent authors a **guided PR tour** — an ordered
reading of the change with per-file notes and line-pinned annotations — and Victor
plays it inside attn's existing diff viewer instead of a separate local web app.
Today the review handoff is flat: the agent reports `ready_for_review`, maybe
attaches a report, and Victor opens the diff panel cold — the reading order, the
contested decisions, and the "start here" are all in the agent's head. jaunt
(`/Users/victor/projects/victor/jaunt`) already solved the authoring side (the
`.jaunt-guide.yml` contract + the authoring skill); attn already owns the diff
side (branch diff, viewed marks, inline review comments). This plan joins them:
same guide file, played over `DiffDetailPanel`, with replies flowing into the
review-comment machinery attn already has — so the tour informs the review, and
the feedback loop stays in attn instead of jaunt's feedback-file dance.

**Explicitly NOT first-wave.** The cheap textual precursor — the
where/verify/deviations report contract for delegate handoffs — ships first via
the chief-guidance-brief-craft plan (same wave as
[ticket-show](2026-07-02-ticket-show.md) /
[dashboard-state-card](2026-07-02-dashboard-state-card.md), which reference it).
This plan is the full investment, scheduled after that contract has proven what a
good handoff report contains. Direction-level: coarser than sibling plans; each
slice gets its own alignment pass before build.

## What jaunt actually is (verified inventory)

- **The artifact**: `.jaunt-guide.yml` at the PR repo's root. Shape (parser:
  `normalizeTour` in jaunt `src/tour.ts`):
  - `version: 1`
  - `summary` — markdown (+ mermaid fenced blocks) reading strategy
  - `files:` — **ordered** entries `{ path, note?, view?: diff|content,
    annotations?: [...] }`
  - each annotation has **exactly one** locator — `anchor: "substr"` (first
    substring match in the post-PR file), `line: N`, or `start: N, end: M` — and
    **exactly one** body — `note: str` or `thread: [str | {author, body}]`
    (thread = pre-empted pushback; default author `"agent"`)
  - `skip:` — paths rendered dimmed at the bottom
- **Consumption**: the CLI fetches the PR via `gh`, then `applyTour` (same file)
  reorders the payload tour → others → skipped, attaches `tourNote`/`tourGroup`
  per file, and resolves annotations to `{lineStart, lineEnd, comments[]}` via
  `resolveAnnotations` — anchors resolve against post-PR file **content**;
  misses degrade to warnings, never errors. `view: content` renders the full
  post-PR file (for all-add plan docs); falls back to diff when content is
  unavailable.
- **Authoring**: `skill/SKILL.md` is a complete authoring discipline — reading
  order heuristics (domain-outward), voice rules (teach don't list), anchor
  hygiene (verify first-match), `thread:` for contested decisions, and `jaunt
  validate` as the contract (schema + paths-in-PR + anchors resolve).
- **Feedback loop** (NOT ported): submit sentinels, `~/.jaunt/*.feedback.md`,
  the agent-reply file. attn replaces all of it with review comments — see
  Boundaries.

## What attn already has (verified inventory)

- **Review identity**: `reviews` table keyed `(repo_path, branch)` —
  `GetOrCreateReview` in `internal/store/review.go`. `pr_number` exists but
  nothing writes it today. Reviews carry viewed-file marks and `ReviewComment`s.
- **ReviewComment**: `{id, review_id, filepath, line_start, line_end, content,
  author("user"|"agent"), resolved…}`. Line convention (load-bearing):
  anchored at `line_start`; a **negative `line_end`** encodes the
  original/deleted side (`app/src/utils/reviewComment.ts`). Fan-out on any shape
  change: TypeSpec → store → `internal/daemon/ws_review.go` → frontend hooks/UI
  (AGENTS.md critical pattern #4).
- **Diff data**: `handleGetBranchDiffFiles` (base defaults to
  `origin/<DefaultBranch>`) and `handleGetFileDiff` →
  `coordinator().FileDiff(dir, path, baseRef, staged)` returning
  original+modified file contents (`internal/daemon/ws_git.go`). attn's frame is
  the **local branch diff**, not a GitHub PR object.
- **Frontend**: `DiffDetailPanel.tsx` orchestrates (file tree via `buildTree`,
  controlled `selectedFilePath`/`onSelectFilePath` owned by `App.tsx`, viewed
  marks, `expandedContext === -1` = full-file mode); `DiffView.tsx` wraps
  `@pierre/diffs` and already renders review comments as native
  `lineAnnotations` grouped per (side, anchor line), plus a top-banner thread
  for comments whose anchor line isn't rendered.
- **Packaged evidence**: `pnpm --dir app run real-app:scenario-diff-review`
  asserts diff-panel state via the `diff_get_state` automation action
  (`app/scripts/real-app-harness/scenario-diff-review.mjs`).
- **Artifact transport**: `attn ticket attach --file <path> [--note]` →
  `handleTicketAttach` (`internal/daemon/ticket_attach.go`) copies into
  `.attn/tickets/<id>/`, records a `TicketAttachment` + `attachment_added`
  event, and notifies observers. `TicketDetailPanel.tsx` lists attachments
  (name + note; no open affordance yet).
- `gopkg.in/yaml.v3` is already a direct Go dependency.

## Architecture Map

```text
Current (two disjoint worlds):
  jaunt: agent writes .jaunt-guide.yml -> jaunt CLI (gh fetch, applyTour) -> local web app
  attn:  agent reports ready_for_review -> Victor opens DiffDetailPanel cold
           -> get_branch_diff_files / get_file_diff -> DiffView (+ ReviewComments)

Target:
  delegated agent (guidance: skill reference, ported from jaunt skill/SKILL.md)
    -> writes .jaunt-guide.yml in its worktree root  (same file jaunt consumes)
    -> attn tour validate                            (ported contract checks)
    -> attn ticket attach --file .jaunt-guide.yml    (and/or commits it — open Q)
         -> attachment_added event -> existing notify fan-out (no new doorbell)

  Victor opens the diff panel (or "Play tour" from the ticket)
    -> new protocol cmd get_review_tour(repo_path, branch [, guide_path])
         -> daemon: internal/tour.Load + Normalize   (port of normalizeTour)
         -> returns the *unresolved* guide (stops with anchor|line|range)
    -> DiffDetailPanel tour mode:
         file list ordered tour -> others -> skip(dimmed)   (port of applyTour ordering)
         stop notes resolved against fetchDiff's modified content at render time
           (port of resolveAnnotations; misses degrade to inline warnings)
         next/prev stop drives onSelectFilePath + scroll-to-line
    -> reply on a stop => plain ReviewComment via existing addComment
         (feedback loop = attn's review comments, not jaunt's feedback file)

Tests:
  internal/tour: golden .jaunt-guide.yml fixtures -> parse/normalize/anchor-resolve
  daemon: get_review_tour handler test (mirror handleGetReviewState tests)
  app: DiffDetailPanel.test.tsx pattern (createMockDaemon + exact-call-count)
  packaged: scenario-diff-review sibling asserting tour state via diff_get_state
```

## Data Model / Interfaces

The **guide file is the artifact and the source of truth** — no new SQLite
tables in the first slice. attn parses the same YAML jaunt does; player progress
(current stop) is ephemeral frontend state; durable "seen" continues to ride the
existing viewed-files machinery.

```go
// internal/tour — faithful port of jaunt src/tour.ts normalizeTour semantics
type Guide struct {
    Version int          // only 1 accepted
    Summary string       // markdown
    Files   []FileEntry  // ordered
    Skip    []string
}
type FileEntry struct {
    Path        string
    Note        string
    View        string       // "diff" | "content"
    Annotations []Annotation
}
type Annotation struct {           // exactly one locator, like jaunt
    Anchor string                  // first-substring-match, resolved later
    Line   int                     // or exact line
    Start, End int                 // or inclusive range
    Comments []Comment             // note => one; thread => many
}
type Comment struct{ Author, Body string }  // author defaults to "agent"
```

Protocol (pattern #1: edit `internal/protocol/schema/main.tsp`, `rm -rf
tsp-output`, `make generate-types`, update constants, bump ProtocolVersion;
tsc-check for the quicktype enum-merge gotcha):

```text
cmd  get_review_tour   { repo_path, branch, guide_path? }
evt  review_tour_result { success, tour?: ReviewTour, error? }
ReviewTour mirrors Guide verbatim — stops carry their UNRESOLVED locator;
the frontend resolves anchors against the modified content it already fetched.
```

Anchor resolution lives **client-side at render time** (a small pure util
mirroring `resolveAnnotations`), because `fetchDiff` already hands the frontend
the post-diff file content — resolving at ingest would bake in a snapshot that
rots as the branch moves, and jaunt's own semantics (warn-and-drop on miss) are
exactly the right drift behavior.

## Boundaries

- `internal/tour` owns parsing, normalization, and validation of the guide. It
  never touches `gh`, the store, or the daemon — pure file → struct.
- The daemon translates guide → protocol and locates the file (repo root or an
  explicit path, e.g. a ticket attachment's stored path). It does not resolve
  anchors.
- **The tour is a read-only overlay.** It never writes `ReviewComment`s, never
  marks files viewed, never gates anything (the board informs, never gates —
  same spine). Replying to a stop creates an ordinary user-authored
  `ReviewComment` through the existing `addComment` path; from there the
  existing machinery (resolve, send-to-Claude) applies untouched.
- Ticket plumbing is transport only: a tour arriving as an attachment rides the
  existing `attachment_added` event and observer notify — **no new doorbell**,
  and attn still authors only the `crashed` ticket status.
- jaunt's feedback loop (sentinels, feedback/reply files) is explicitly not
  ported; attn's review comments are the reply channel.

## Implementation Steps

- [ ] **Slice 1 — guide model + ingestion (Go).** New package `internal/tour`:
      `Load(path)` + normalize with jaunt's exact validation rules (version==1;
      exactly-one locator; note xor thread; non-empty bodies) using
      `gopkg.in/yaml.v3`; `Validate` additionally checks paths against a
      changed-file list and anchors against on-disk file content (port of
      `jaunt validate`'s error/warning split). Protocol: `get_review_tour` cmd
      + result (pattern #1, ProtocolVersion bump). Daemon handler in
      `internal/daemon/ws_review.go` mirroring `handleGetReviewState`'s
      shape (result struct, error-ptr, `sendToClient`). CLI: `attn tour
      validate [path]` in `cmd/attn/main.go` (mirror `runTicket` subcommand
      dispatch + `parseTicketAttachArgs`-style flag parsing). Tests: golden
      fixtures in `internal/tour` (valid guide, every rejection case jaunt's
      normalizer throws, anchor hit/miss/ambiguity); daemon handler test beside
      the existing ws_review tests.
- [ ] **Slice 2 — agent authoring path.** New skill reference
      `internal/agent/attn_skill/references/pr-tour.md`: a condensed port of
      jaunt `skill/SKILL.md` (reading-order heuristics, voice rules, anchor
      hygiene, thread-for-contested-decisions), retargeted — write the guide at
      the worktree root, run `attn tour validate`, then `attn ticket attach
      --file .jaunt-guide.yml --note "PR tour"`. Wire the new reference into the
      skill front-door (`internal/agent/attn_skill/SKILL.md`) and mention the
      affordance in `references/delegated-agent.md`'s handoff section.
      `delegatedTicketPrompt` (`internal/daemon/delegate.go`) stays untouched —
      depth lives in the reference, per the thin-always-on prompting principle.
      Ship only after the chief-guidance-brief-craft report contract has landed,
      so tour authoring extends that contract instead of competing with it.
- [ ] **Slice 3 — player UI over the diff viewer.** `DiffDetailPanel.tsx` tour
      mode: on open, request `get_review_tour` (new socket fn in
      `useDaemonSocket.ts`, request/result promise pattern per `sendPRAction`;
      remember the App.tsx four-step prop plumbing gotcha). When a tour exists:
      order the file list tour → others → skip-dimmed (extend `buildTree`
      input), render the file-level note as a banner above the diff, resolve
      stop anchors against the fetched modified content (new
      `app/src/utils/tourStops.ts`, port of `resolveAnnotations`) and feed them
      into `DiffView`'s existing native `lineAnnotations` grouping as a
      read-only annotation kind alongside comments; `view: content` maps to the
      existing `expandedContext === -1` full-file mode. Player bar: stop i/N +
      next/prev driving `onSelectFilePath` + scroll-to-line; shortcuts go
      through `useShortcut.ts` and must be checked against the macOS default-
      menu accelerators (AGENTS.md pattern #8). Reply-on-stop opens the
      existing comment draft at the stop's line. Tests:
      `DiffDetailPanel.test.tsx` pattern (createMockDaemon, exact call counts,
      out-of-order responses); a `DiffView` harness case if annotation
      rendering needs real shadow-DOM (per `app/CLAUDE.md` harness guidance);
      packaged sibling of `scenario-diff-review.mjs` with a fixture guide,
      asserting order + stop navigation via `diff_get_state` (single-tenant,
      serial). CHANGELOG entry.
- [ ] **Slice 4 (stretch) — ticket integration polish.** "Play tour" affordance
      on `TicketDetailPanel.tsx` when an attachment is a `.jaunt-guide.yml`
      (opens the diff panel with `guide_path` pointed at the attachment's
      stored path); board/dashboard surfacing deferred.

## Verification

- `go test ./internal/tour ./internal/daemon ./internal/store` (scope daemon
  runs with `-run` if the pre-existing GitStatusScheduler race bites).
- `pnpm --dir app test DiffDetailPanel` and `pnpm --dir app test DiffView`.
- `make generate-types` after the TypeSpec change (rm -rf tsp-output first;
  tsc-check the generated output).
- Packaged: `make dev`, then `pnpm --dir app run real-app:scenario-diff-review`
  and the new tour scenario — one packaged scenario at a time.
- End-to-end by hand: delegate a real change from the chief, have the agent
  author + attach a guide, play it in attn-dev, and open the same worktree's
  guide in jaunt's web app to confirm one-artifact-two-surfaces.

## Decisions

(settled with Victor — not up for re-litigation)

- **Not first-wave.** The textual where/verify/deviations report contract ships
  first via the chief-guidance-brief-craft plan; this plan is the full
  investment and follows it.
- **Artifact format stays COMPATIBLE with `.jaunt-guide.yml` v1.** Agents write
  ONE guide consumable by either surface — jaunt's web app or attn's player.
  attn's parser is a faithful port of `normalizeTour`/`resolveAnnotations`
  semantics (including warn-don't-fail anchor misses); attn does not fork the
  schema. If attn ever needs more, it extends v1 with optional fields jaunt
  ignores — never breaks it.
- **Shape: a tour = ordered stops (file, range, note)** — structurally
  ReviewComment-adjacent plus a sequence index — with a player affordance
  (next/prev stop) over the existing diff viewer, not a new surface.
- **The tour travels on the ticket and/or the PR; final call is Victor's**
  (see Open Questions for the trade-off).

(implementation decisions made here)

- **File is the source of truth; no tour tables in slice 1.** The guide is
  agent-authored YAML with an external consumer (jaunt); mirroring it into
  SQLite creates a second copy that can disagree. Player progress is ephemeral;
  durable review state stays on the existing reviews/viewed-files machinery.
- **Anchors resolve client-side at render time** against the modified content
  `fetchDiff` already delivers — drift degrades to jaunt-style warnings instead
  of silently pinning stale lines.
- **Tour stops are NOT ReviewComments.** They render through the same
  `DiffView` annotation slots but stay a read-only overlay; replies mint real
  ReviewComments. This keeps the pattern-#4 fan-out untouched in slices 1–3.

## Open Questions

- **Transport — ticket attachment vs PR/repo file (Victor's call).**
  *Ticket attachment*: travels with the delegation, durable in
  `.attn/tickets/<id>/`, arrival notifies via `attachment_added`; but invisible
  to jaunt (which looks for the repo-root file) and useless for tours on work
  with no ticket. *Repo-root file (possibly committed to the PR)*:
  jaunt-compatible by construction and works ticket-less; but dirties the
  branch/PR unless gitignored, and a committed guide rots as review rounds
  land. *Both* (write at root, also attach): maximally compatible, mild
  duplication. Leaning: both, with the repo-root copy gitignored by default.
- **Tour anchored to a PR vs the local branch diff.** attn's diff frame is
  branch-vs-`origin/<default>` (`handleGetBranchDiffFiles`); jaunt's is the
  GitHub PR at `headSha`. For worktree-delegated work these coincide, but a
  guide authored against a PR head can disagree with a moved local branch. Does
  the player ever need PR-pinned content (via `reviews.pr_number`, today unset),
  or is "the tour describes the branch as it stands" the contract?
- **Diff drift under the guide.** Warn-and-drop (jaunt semantics) is the floor.
  Do we want a freshness signal — e.g. compare guide mtime/attachment time
  against branch head — telling Victor "this tour predates N newer commits"?
- **`ReviewComment.order` field vs separate tour storage** — dissolved to "no
  persistence" by the file-as-source-of-truth decision, but revisit if tours
  later need server-side state (multi-client stop sync, chief-readable tour
  progress).
- **How much of `view: content` / markdown / mermaid to honor in v1 of the
  player.** Full-file mode exists (`expandedContext === -1`); markdown-in-notes
  needs a renderer in `DiffView` annotation slots; mermaid is likely a
  follow-up. jaunt remains the fallback surface for guides that lean on it.

## Follow-ups

- Chief awareness of tours: the chief could mention "a tour is attached" when
  reporting `ready_for_review` — guidance-only, belongs to the
  chief-guidance-brief-craft lineage.
- Tour summary (+ mermaid) rendering as a proper opening card, possibly reusing
  the notebook markdown surface.
- Sunset question for jaunt's own web app once the attn player covers Victor's
  actual review flow — keep jaunt as the ticket-less/external-repo surface.
