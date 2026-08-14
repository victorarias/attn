# Plan: the garden-era epic

Written 2026-08-14 from the rulings of that day's alignment round. This
doc encodes decisions already made; the forks below are closed, and each
carries its ruling date. The epic is the end of the two-capture-system
era: slice 6 of [the garden slices plan](2026-08-06-the-garden-vertical-slices.md)
plus ticket retirement, on one epic branch Victor lives on before it
merges.

## Goal

A plot is read and reviewed the way markdown plans are today — rendered,
annotated, feedback flowing to the tending agent — and tickets retire on
the same branch, because the garden now does everything delegations used
tickets for. The proof is lived, not tested: Victor runs days of real
work from the epic branch without touching `attn ticket`, then the epic
merges to main (his explicit OK — an epic merge is on the wait list).

## Why now

- Garden slice 5 shipped (#892): plots, capture, dispatch — the
  2026-08-06 "usable" bar for retirement. Messaging (peek + msg) is
  live. Both retirement preconditions hold.
- The annot-seams refactor (#900) tidied exactly the files slice 6
  builds on: one daemon draft handler, shared correlation in
  `daemonPendingRequests.ts`, shared `QuickLabelPicker` and
  `useAnnotationSend`. Slice 6 touches only the markdown annotation
  stack; nothing else moves.

## The shape

Ruling A (2026-08-14): the panel drills read-only; the tile is the
annotated reader; `attn open` makes a tile.

```text
GardenPanel                       # read-only
  seed row -> drill-down          # rendered body + log, no annotations
           -> "open as tile"      # hands off to the reader

workspace tile                    # the one annotated reading surface
  attn open <seed-id>             # same verb that docks a markdown file
    -> tile whose document source is the seed
       MarkdownReader             # body renders as the plan page
         annotations/             # existing stack, second source
       log ledger beneath         # children + notes, updates live
```

One annotation stack, two document sources. The reader learns "a seed"
beside "a file"; anchoring, highlight rendering, and payload formatting
stay exactly where the annot-seams audit ruled them.

## The house URI

Ruling B (2026-08-14): `attn://seed/<id>` is the house URI — document
identity for tiles and the annotation draft key. The draft keying
generalizes from `<op>:<workspaceId>:<path>`
(`app/src/hooks/daemonMarkdownAnnotationEvents.ts`) to `<op>:<uri>`,
where a file document's URI form carries workspace and path and a seed's
is `attn://seed/<id>`.

Wherever the daemon *acts* — submit routing, draft persistence, log
writes — the message carries typed source/destination fields (seed id,
session id), never recovered by parsing the URI string. The URI is
identity and keying; typed fields are authority.

## Live re-anchor

Ruling C (2026-08-14): a seed's body is a living document — the tender
edits it while Victor reads. On a body edit event the open reader
re-anchors its annotations live: no reload, no scroll jump, no orphaned
highlight. (The anchoring machinery already exists under
`MarkdownReader/anchoring/`; what is new is driving it from a body-edit
event instead of a file reload.)

## Submit

Ruling D (2026-08-14): the annotation composer on a seed gets a
split-button submit. Primary sends to the tender's session (the existing
annotation→agent path); the caret menu carries "Note on seed" (a log
note, so feedback on unattended work is never lost). On an untended seed
the note action flips to primary — there is nobody to send to, and the
button says so by its shape.

## Ticket retirement

The path is encoded at the ["Tickets retire" entry](2026-08-06-the-garden-vertical-slices.md)
(ruled 2026-08-14). In epic terms:

- A delegation binds a seed: the brief is the seed's body, the delegate
  session its tender. Status reports become log notes; steering goes
  over agent-msg.
- `attn ticket` verbs become loud signposts to their garden equivalents
  — each prints where the capability went and exits nonzero. The
  signposts stay indefinitely (ruled 2026-08-14): their audience is
  mostly agents running on stale memories and guidance, a signpost is
  what lets them self-correct, and a few inert lines of code cost
  nothing. No removal is scheduled; a later cleanup may delete them
  once long silent.
- Conversion (ruled 2026-08-14): unbound backlog todos bulk-convert to
  seeds at cutover — they are inert, nothing reports to them. In-flight
  delegations finish on their tickets; new dispatches start on seeds.
  The board drains itself within days, and "done tickets stay readable"
  covers the tail.
- Done tickets stay readable forever, never migrated.
- The outpost leg stays gated on the uplink per
  [the arc plan](2026-08-10-home-garden-crew-arc.md).

**The landmine this epic must design around:** delegation reporting IS
tickets today — briefs, status reports, artifacts, and the dispatch
skill's contract all land on the ticket. The garden takes that weight
*before* the ticket verbs go signpost, or the first delegation on the
epic branch has nowhere to report. That ordering is the epic's internal
sequencing rule.

### Artifacts

Ruled 2026-08-14: artifacts are part of the seed, and the association
is a typed `attach` entry in its log. Storage does not move — the
canonical-artifact lifecycle
([2026-07-18](2026-07-18-canonical-plan-artifact-lifecycle.md)) keeps
deciding *where documents live* (committed plans in git behind a
reference, untracked staging files promoted into the Notebook); the
seed records only the association, the way tickets already model an
attach as an activity event with the artifact list derived from those
events.

- An `attach` log note carries a typed reference — notebook doc id,
  repository path + repo, or plain URL — never a string the daemon
  parses meaning from.
- The seed's "current artifacts" is a projection over the log (attach
  minus detach), rendered as a small set in the panel drill and the
  tile, not buried in the timeline.
- `detach` is the way out.
- At ticket conversion, a ticket's main attached plan becomes the
  seed's body; its other artifacts become `attach` entries.

## Implementation steps

- [ ] Open `epic/garden-era` from main once #900 (annot-seams) merges.
- [ ] Seed document source in the reader + `attn open <seed-id>` +
      panel read-only drill (ruling A).
- [ ] `attn://seed/<id>` URI, draft-key generalization, typed
      daemon fields (ruling B).
- [ ] Live re-anchor on body edits (ruling C).
- [ ] Split-button submit with the untended flip (ruling D).
- [ ] Delegation reporting on seeds: bind at dispatch, log-note status,
      typed `attach`/`detach` with the artifact projection, agent-msg
      steering — the weight transfer.
- [ ] Ticket verbs → permanent signposts; bulk-convert backlog todos;
      done tickets readable.
- [ ] Victor lives on the branch for days without tickets.
- [ ] Epic merge to main on his explicit OK.

## Decisions

- Panel stays read-only; one annotated surface, the tile (A). Two
  annotation-capable surfaces would mean two draft owners for one seed.
- URI for identity, typed fields for authority (B) — the daemon never
  parses meaning out of a string a client composed.
- Retirement rides this epic rather than waiting for a separate pass —
  living without tickets is the only honest test of the garden carrying
  the weight, and a lived epic branch is the cheapest place to run it.
- Artifacts associate through the log, not a schema field or child
  seeds (rejected: child seeds conflate documents with units of work;
  a dedicated attachments collection duplicates what the log plus a
  projection already express). Storage stays with the canonical
  lifecycle; the seed holds associations only.
