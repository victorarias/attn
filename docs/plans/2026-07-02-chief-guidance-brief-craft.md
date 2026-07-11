# Plan: Chief guidance pass — brief contract, facts-vs-forks, push-vs-hold, report contract

## Goal

A single prose pass across the chief's always-on guidance (`hooks.ChiefGuidance`),
the delegated-agent prompt (`delegatedTicketPrompt`), and the skill references, so
the chief writes briefs that stand alone, fetches facts without deciding design,
knows when to interrupt Victor vs. batch, and delegated agents land terminal
reports Victor can act on. **Guidance only — no new machinery.** Six edits, every
added line earning its place: prompt bloat dilutes the whole prompt (Decision 5 of
[2026-06-30-delegated-leaf-not-chief.md](2026-06-30-delegated-leaf-not-chief.md)),
so this plan carries a hard line budget (~26 net new prose lines, cap 30 — see the
budget table). Why now: the awareness loop shipped
([vision](../vision/chief-delegation-awareness.md)); what's left underdefined is
the chief's *judgment* — brief quality, the design-fork boundary, and the vision's
open "push vs. hold" / "multi-delegation synthesis" items, which this pass settles
at the guidance level.

## Architecture Map

```text
Chief launch (claude --append-system-prompt / codex developer_instructions)
  cmd/attn launch -> unix socket notebook_guide (internal/daemon/notebook.go
                     handleNotebookGuide) -> hooks.ChiefGuidance(root)      [Tier 1, always-on]
    today:  watch trigger ("Right after delegating, arm a harness Monitor…"),
            come-back boundary, coordinator-not-doer, blast radius
    target: + brief contract (4 elements, thin)          (a)
            + scouting license / fork-naming rule        (b)
            + push-vs-hold + consolidation + summary     (c)
            + re-brief rule                              (d)
            watch trigger reworded: overridable default  (g)

Chief delegates -> internal/daemon/delegate.go delegate()
  trackedByChief: brief -> delegatedTicketPrompt(brief) -> withLeafIdentity(...)
    today:  status-report commands + confirm-before-complete
    target: + three-field terminal-report contract       (e)

Depth on demand (Tier 2, lazy): internal/agent/attn_skill (//go:embed,
installed by the daemon with orphan pruning — merge ≠ shipped, rebuild required)
  SKILL.md front door                       — unchanged
  references/tickets.md                     — + operational re-brief line   (d/f)
  references/delegation.md                  — checklist -> 4-element contract,
                                              + fork-naming instruction     (f)
  references/delegated-agent.md             — + fold-the-fork-outcome rule,
                                              + terminal-report example     (f)

Tests (all substring assertions on composed prose):
  internal/hooks/hooks_test.go  TestChiefGuidance         — one string per new clause
  internal/daemon/delegate_test.go
    TestChiefOfStaffDelegateBindsTicketAndPrompt          — the three report fields
  internal/agent/attn_skill_test.go  assertAttnSkillTree  — per-reference strings
    (shared by TestEnsureAttnClaudeSkillInstalled / …CodexSkillInstalled — one edit
     covers both installs)
```

## Surfaces & line budget

Count net new prose lines against this table when done (`git diff --stat` sanity):

| edit | surface | content | budget |
|---|---|---|---|
| a+b | `hooks.ChiefGuidance` | brief contract + load trigger + scouting/fork rule | 6 |
| c | `hooks.ChiefGuidance` | push-vs-hold, consolidation, re-engagement summary | 4 |
| d | `hooks.ChiefGuidance` | re-brief rule | 2 |
| g | `hooks.ChiefGuidance` | watch trigger → overridable default | 1 |
| e | `delegatedTicketPrompt` | three-field terminal-report contract | 3 |
| f | `references/delegated-agent.md` | fold-fork-outcome rule + report example | 5 |
| f | `references/tickets.md` | operational re-brief line | 2 |
| f | `references/delegation.md` | checklist tighten (in place) + fork line | 3 |

## Boundaries

- **Guidance only.** No protocol change, no store change, no daemon logic, no
  frontend, no ProtocolVersion bump, no migration. Nothing here gates anything:
  the board still informs, attn still authors only `crashed`
  (`internal/daemon/ticket_crash.go`), doorbells still trigger a read and never
  carry content.
- **Two-tier split preserved** (the A2 shape from
  [2026-06-28-delegated-ticket-awareness.md](2026-06-28-delegated-ticket-awareness.md)):
  guidance that matters *at delegate time* is always-on in `ChiefGuidance` /
  `delegatedTicketPrompt`; the craft depth stays in the lazily-loaded skill
  references. Do not move depth up or contracts down.
- **Seams, not overlap.** The deterministic "state of the world" card is machinery
  owned by [2026-07-02-dashboard-state-card.md](2026-07-02-dashboard-state-card.md)
  (it names `hooks.ChiefGuidance` as *this* plan's seam for the narrative WHY —
  the cross-reference is already two-way). `attn ticket show` is owned by
  [2026-07-02-ticket-show.md](2026-07-02-ticket-show.md) — do **not** document it
  in any reference here. Two later plans deliberately touch surfaces this plan
  edits, AFTER it lands:
  [2026-07-02-ticket-list-context.md](2026-07-02-ticket-list-context.md) appends
  one coordinated clause (branch = collision signal) to the same "Delegation
  hands work off" bullet — outside this plan's line budget, keeping its asserted
  substrings byte-identical — and
  [2026-07-02-ticket-attach-ergonomics.md](2026-07-02-ticket-attach-ergonomics.md)
  changes `delegatedTicketPrompt`'s signature (`brief, attachments`), appends a
  handover-files block after edit (e)'s contract, and adds a "Handover files"
  subsection to `references/delegation.md`. Both are compatible; the later plans
  rebase and keep this plan's substring assertions green.
- **Assertion-string trap:** `ChiefGuidance` embeds `TicketAwarenessGuidance()`
  verbatim as `%[3]s`, which already contains `what "done" looks like`, `how it is
  verified`, and `scope`. Every new `TestChiefGuidance` substring must be unique to
  the new clauses (verified unique below), or the test passes vacuously.
- **Reality constraint on re-briefs:** agents cannot edit a ticket's description.
  `ticket_edit_description` is a websocket-only app command
  (`internal/daemon/ticket_actions.go handleTicketEditDescription`, authored as
  `"you"`); the agent socket (`daemon.go` dispatch) has no equivalent and the CLI
  has no verb. Guidance wording must stay truthful to that (see Decisions).

## Implementation Steps

- [ ] **(a+b) Brief contract + scouting license in `hooks.ChiefGuidance`**
      (`internal/hooks/hooks.go`). Insert one new bullet *before* the existing
      "Delegation hands work off" bullet (craft precedes delegating). Mirror the
      four section bullets of `references/tickets.md` ("The description is the
      brief"), compressed to one sentence each. Draft:

      > - The brief is the contract. Every delegation brief carries four things:
      >   the outcome as a stop condition, just-enough context, a verification
      >   contract (how completion is proven), and scope/autonomy bounds (what is
      >   deferred; what is a real blocker vs. the worker's call). Before
      >   delegating non-trivial work, load the attn skill's tickets reference for
      >   the full craft. When the brief needs anchors — paths, names, what the
      >   code currently does — you may run a native subagent to fetch them: do it
      >   when anchors are missing, not as a ritual, and never to resolve design.
      >   An open design fork gets the opposite treatment: do not settle it and do
      >   not pre-align it with the user yourself — name it in the brief as an
      >   explicit open question and instruct the delegate to align with the user
      >   before building past it.

- [ ] **(c) Push-vs-hold in `hooks.ChiefGuidance`.** Extend the "When a watched
      ticket comes back" bullet (keep its existing asserted strings intact:
      "awareness and upkeep", "the agent's claim, not as confirmed", "review on
      the merits"). Draft addition:

      > Push to the user immediately only when they are the bottleneck — blocked,
      > needs input, failed, crashed, or a thread gone quiet; hold the rest
      > (progress notes, completions, in-review the user hasn't said they are
      > waiting on) and fold them into your next natural update. Several tickets
      > moving in one turn is one consolidated update ordered by what needs the
      > user first, never a ping per ticket. When the user re-engages after a gap,
      > lead with the one-picture summary — the dashboard already shows the rows;
      > you add the why and the recommended next step.

- [ ] **(d) Re-brief rule in `hooks.ChiefGuidance`.** One sentence in the same
      come-back bullet, right after the steering context. Draft:

      > When your answer to a blocker is really a missing piece of the brief, say
      > so: mark the comment as a re-brief so the ticket's description — the
      > durable brief a resume or reassignment will read — gets updated instead of
      > the fix living only in the thread.

- [ ] **(g) Watch trigger → overridable default** in the "Delegation hands work
      off" bullet. Keep the asserted strings "arm a harness Monitor" and
      "attn ticket inbox --watch" byte-identical; append:

      > (a default, not a hard rule — skip it if the user asks; the daemon
      > doorbells you if unread ticket activity sits unwatched).

      The shared countdown claim is real: `internal/daemon/ticket_notify.go`
      doorbells every non-approval chief unless an optional watch already consumed
      the queue. While executing, also mark the "Open product question for Victor"
      line in `docs/plans/2026-06-28-delegated-ticket-awareness.md` resolved with
      a pointer to this plan.

- [ ] **(e) Terminal-report contract in `delegatedTicketPrompt`**
      (`internal/daemon/delegate.go`). Insert between the status-command list and
      the "Closing a ticket is the user's call" paragraph. Exactly three fields —
      keep each assertable phrase on a single source line so `strings.Contains`
      doesn't straddle a wrap. Draft:

      > A ready_for_review or failed report must carry three things in its
      > comment: where the artifact is (branch, PR, or paths), how to verify it,
      > and any deviations from the brief.

- [ ] **(f) Skill reference tightenings** (`internal/agent/attn_skill/references/`):
      - `delegated-agent.md`: in the reporting section, add the fold-the-fork
        rule — "When alignment with the user settles a design fork, fold the
        outcome into the ticket: a comment always; when it changes the brief, flag
        it as a re-brief so the description absorbs it (description edits are
        currently the user's, from the ticket panel)." Also restate the
        three-field terminal contract in one line matching (e)'s wording, and
        rewrite the `ready_for_review` example comment to model it (artifact
        location + verify command + deviation).
      - `tickets.md`: in "Durable description vs live steering", add the
        operational line — "When an answer to a blocker is really a missing piece
        of the brief, it belongs in the description, not just the thread — mark
        the comment as a re-brief so the durable contract gets updated."
      - `delegation.md`: tighten the Brief Workflow numbered checklist *in place*
        to the same four-element contract as (a) (it already has objective /
        context / constraints / deliverable; fold in the verification contract and
        rename to stop-condition + autonomy-bounds wording), and add the
        fork-naming instruction: "Name any open design fork in the brief as an
        explicit open question and tell the delegate to align with the user before
        building past it — do not settle it yourself." Keep every string
        `assertAttnSkillTree` already asserts. Do not mention `ticket show`.

- [ ] **Tests.** One assertion per new clause, mirroring the existing
      watch-trigger assertions in `TestChiefGuidance`
      (`internal/hooks/hooks_test.go`):
      - `TestChiefGuidance` add: `"stop condition"`, `"verification contract"`,
        `"load the attn skill's tickets reference"`, `"never to resolve design"`,
        `"align with the user before building past it"`, `"bottleneck"`,
        `"one consolidated update"`, `"re-brief"`,
        `"a default, not a hard rule"`. (All verified absent from today's
        `ChiefGuidance` + embedded `TicketAwarenessGuidance`.)
      - `TestChiefOfStaffDelegateBindsTicketAndPrompt`
        (`internal/daemon/delegate_test.go`) add: `"where the artifact is"`,
        `"how to verify it"`, `"deviations from the brief"`.
      - `assertAttnSkillTree` (`internal/agent/attn_skill_test.go`) add — to the
        delegated-agent block: `"fold the outcome into the ticket"`,
        `"deviations from the brief"`; to the tickets block: `"re-brief"`; to the
        delegation block: `"align with the user before building past it"`.
      No other tests: prose changes with no runtime surface beyond these strings.

- [ ] **CHANGELOG.md** — one user-facing entry: the chief now writes
      contract-style briefs, names design forks instead of deciding them, batches
      non-urgent ticket updates into one consolidated report, and delegated agents
      close out with artifact / verification / deviations.

## Verification

```bash
go build ./... && go vet ./...
go test ./internal/hooks/...
go test ./internal/agent/...
go test ./internal/daemon -run 'TestDelegate|TestChiefOfStaffDelegate|TestOrdinaryDelegation|TestDelegatedFromChief'
gofmt -l internal/hooks internal/daemon internal/agent
```

- Scope the daemon run with `-run` — the pre-existing `TestGitStatusScheduler`
  data race aborts a bare `go test ./internal/daemon -race`.
- Line-budget self-check: net new prose lines ≤ 30 across all six edits.
- Live smoke (optional, non-prod is pre-authorized): `make dev`, promote a chief
  in `attn-dev`, and confirm the composed system prompt carries the new clauses —
  `ChiefGuidance` is served by the *daemon* (`notebook_guide`) and the skill files
  ship via daemon `//go:embed`, so none of this is visible until the daemon is
  rebuilt and the chief relaunched/reloaded.

## Decisions

All settled with Victor — do not re-litigate:

1. **Guidance only, six edits, hard line budget.** Prompt bloat dilutes — heavy
   repetition trains the model to skim past absolutes (Decision 5,
   [2026-06-30-delegated-leaf-not-chief.md](2026-06-30-delegated-leaf-not-chief.md)).
   The budget table above is part of the contract.
2. **The brief contract is always-on, thin, in `ChiefGuidance`** — with a
   load-trigger to the tickets reference for depth. Precedent: lazy-load-miss is a
   root cause; guidance that matters at delegate time cannot depend on a lazy
   skill load (the A2 decision,
   [2026-06-28-delegated-ticket-awareness.md](2026-06-28-delegated-ticket-awareness.md)).
   The four elements duplicate ~15 words of `tickets.md` — accepted; prompts don't
   reliably follow cross-file references.
3. **Scouting license is narrow: anchors yes, design no.** The chief MAY run a
   native subagent to fetch anchors — paths, names, what the code currently does —
   when the brief needs them; not always, and never to resolve design. Design
   ambiguity gets the opposite treatment: the chief does not resolve it and does
   not pre-align with Victor; it names the fork in the brief as an explicit open
   question and instructs the delegate to align with Victor before building past
   it. The risk being avoided (Victor's words): the chief doing too much, making
   design decisions for him, or getting stuck designing with him.
4. **Push-vs-hold** (settles the vision's open "Push vs. hold" and
   "Multi-delegation synthesis" items at the guidance level): push immediately
   when Victor is the bottleneck — blocked, needs_input, failed, crashed,
   went-quiet; hold and batch the rest — progress notes, completions, in_review
   unless Victor said he is waiting. Several tickets moving in one turn become ONE
   consolidated update ordered by what needs Victor. On re-engagement after a
   gap, lead with the one-picture summary; the deterministic card is machinery
   owned by the dashboard-state-card plan — the chief adds the WHY.
5. **Terminal-report contract is exactly three fields** — artifact location,
   how to verify, deviations from the brief. Do not overdo it (Victor).
6. **"Arm a Monitor" is an overridable default, not a hard rule** — the daemon
   backstop makes a missing watch safe. This settles the open product question
   recorded in 2026-06-28-delegated-ticket-awareness.md (the Claude-obeyed-human /
   codex-obeyed-prompt finding).
7. **Re-brief rule adapted to code reality.** The settled intent is "edit the
   description too — it is the durable brief," but no agent surface can edit a
   description today (`ticket_edit_description` is app-websocket-only). The
   guidance therefore instructs marking the comment as a re-brief so the
   description gets updated, and the CLI verb is a named follow-up — the wording
   must not tell an agent to run a command that doesn't exist.

## Open Questions / Follow-ups

- **`attn ticket edit --description <text>` agent verb** — the machinery that
  makes the re-brief rule fully operational for the chief (and a leaf folding a
  settled fork into its own brief). Small PR mirroring the `attn ticket comment`
  slice (protocol cmd + agent-socket handler reusing `store.EditTicketDescription`
  + CLI); needs a ProtocolVersion bump (CLAUDE.md critical pattern #1). Tighten
  the guidance wording from "mark the comment as a re-brief" to "edit the
  description" when it lands.
- **Went-quiet detection** is machinery owned by the sibling
  [2026-07-02-ticket-went-quiet.md](2026-07-02-ticket-went-quiet.md) plan: an
  attn-authored non-status `went_quiet` activity event lands in the chief's inbox
  like any other move, so the push-class went-quiet signal needs no further
  guidance once it ships. Only until that plan lands does the signal depend on
  the chief noticing staleness itself (e.g. board timestamps via
  `attn ticket list`).
- The chief-side narrative summary and the dashboard card should be checked
  against each other once both land — same seam, two layers (rows vs. why).
