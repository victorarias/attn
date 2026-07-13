# Chief awareness wave — umbrella

> North star: `docs/vision/chief-delegation-awareness.md` — read that for the *why*.
> This doc coordinates the 2026-07-02 plan set the way
> [2026-06-26-work-tracker.md](2026-06-26-work-tracker.md) coordinated the ticket
> slices: one table, explicit waves, and a seam registry so parallel executors do
> not trespass on each other's surfaces.

## Goal

This wave advances the chief-delegation-awareness vision on all three of its
fronts at once: **chiefs write better briefs** (guidance pass + a real-agent
benchmark that makes brief quality a tested property), **the orchestration
mechanism closes its open gaps** (non-consuming drill-in read, reliable ticket
resume, the went-quiet floor, the shipped two-way attachment path,
collision-awareness data), and
**attn reduces Victor's cognitive load** (waiting-on-you badge, return-digest
dashboard card, one-click ticket access on the pane — with richer ticket content
and guided PR tours staged behind usage evidence). Spine, as always: **awareness,
not autonomy** — every surface here informs, none gates; attn still authors
exactly one status (`crashed`); doorbells stay content-free.

## The plans

> Legend: ⬜ not started · 🟡 in progress · ✅ merged · ⛔ not pursuing.

| plan | scope (one line) | dependencies / seams | wave | status |
|---|---|---|---|---|
| [waiting-on-you-counter](2026-07-02-waiting-on-you-counter.md) | Badge on the sidebar board button (blocked ∪ in_review) + combined board filter; zero protocol | none; **owns `app/src/utils/waitingOnYou.ts`** and the `openBoardSurface(initialFilter?)` signature change | 1 | ⬜ |
| [chief-guidance-brief-craft](2026-07-02-chief-guidance-brief-craft.md) | Guidance-only pass: brief contract, scouting license, push-vs-hold, terminal-report contract, Monitor-as-default (~26-line budget) | none; **owns `hooks.ChiefGuidance` + `delegatedTicketPrompt`**; unblocks benchmark, list-context, pr-tour-port | 1 | ⬜ |
| [ticket-show](2026-07-02-ticket-show.md) | `attn ticket show <id>` — global, non-consuming full-record read over the agent socket (protocol + CLI) | none; stays off `hooks.go` (brief-craft owns it) | 1 | ⬜ |
| [ticket-resume-fix](2026-07-02-ticket-resume-fix.md) | Root-cause fix for "Session spawn arguments were not prepared": daemon-owned `ticket_resume` replaces the racy frontend resume orchestration | none; replaces the `onResume` implementation pane-overlay treats as opaque; deletes `app/src/utils/ticketResume.ts` | 1 | ⬜ |
| [ticket-pane-overlay](2026-07-02-ticket-pane-overlay.md) | Bound-ticket chip on the agent pane header toggling the editable `TicketDetailPanel` as a pane overlay; frontend-only | none; **the access-pattern experiment gating ticket-rich-content** | 1 | ⬜ |
| [dashboard-state-card](2026-07-02-dashboard-state-card.md) | Deterministic return-digest card on the home dashboard (Waiting on you / In flight / Closed today) + `handleRevealTicketDetail` | imports `waitingOnYouTickets` (counter — STOP if absent); inherits `openBoardSurface`'s new signature (closure required); ships `sessionStateForTicket` seam unwired | 2 | ⬜ |
| [brief-quality-benchmark](2026-07-02-brief-quality-benchmark.md) | Real-agent harness benchmark scoring a minted brief on four deterministic checks; records chief model + effort | hard-depends on **merged** brief-craft guidance text (marker-phrase gate) | 2 | ⬜ |
| [ticket-attach-ergonomics](2026-07-02-ticket-attach-ergonomics.md) | By-id attach shipped through the newer filesystem-backed design; delegate-time attach declined as unnecessary coupling | superseded by [ticket handover through Notebook files](2026-07-09-design-artifact-handover.md) | — | ⛔ |
| [ticket-list-context](2026-07-02-ticket-list-context.md) | Durable `tickets.branch` captured at mint; branch/cwd on `ticket list` and board cards (data exposure only) | after brief-craft (one coordinated `ChiefGuidance` clause, outside its budget); board-card line coexists with went-quiet's chip | 2 | ⬜ |
| [ticket-went-quiet](2026-07-02-ticket-went-quiet.md) | The went-quiet floor: board quiet chip (pure join, owns `app/src/utils/ticketQuiet.ts`) + daemon-authored `went_quiet` activity event | independent; lands after counter's board edits to minimize rebases; **slice 3 wires dashboard's `sessionStateForTicket`** | 2 | ⬜ |
| [ticket-rich-content](2026-07-02-ticket-rich-content.md) | Markdown, attachment previews, sandboxed HTML — three usage-gated slices inside `TicketDetailPanel` | gated on the pane-overlay experiment showing tickets get read in-app (soak time, not just code precedence) | 3 | ⬜ |
| [pr-tour-port](2026-07-02-pr-tour-port.md) | Guided PR tours (jaunt `.jaunt-guide.yml` port) played over the diff viewer; direction-level plan | after brief-craft's report contract proves out; each slice gets its own alignment pass | 3 | ⬜ |

## Waves

### Wave 1 — dependency-free, unblocking

`waiting-on-you-counter`, `chief-guidance-brief-craft`, `ticket-show`,
`ticket-resume-fix`, `ticket-pane-overlay`.

All five are dependency-free per their own docs, and three of them unblock
others: the counter owns `app/src/utils/waitingOnYou.ts` (dashboard-state-card
STOPs without it); brief-craft owns `hooks.ChiefGuidance` + `delegatedTicketPrompt`
(the benchmark hard-depends on its merged text, pr-tour-port defers to its report
contract, and landing it first de-conflicts ticket-list-context); ticket-show
declares itself same-wave as brief-craft
and each explicitly stays off the other's surfaces. ticket-resume-fix is an
independent root-cause bug fix. ticket-pane-overlay is frontend-only and
independent — and it is the access-pattern experiment gating ticket-rich-content,
so starting it earliest maximizes gate evidence. Intra-wave contention is
mergeable but real: counter / resume-fix / overlay all touch `App.tsx` in
different regions; overlay and resume-fix both touch `useUiAutomationBridge.ts`;
show and brief-craft both touch `references/tickets.md` in different sections —
second lander rebases.

### Wave 2 — blocked on or contended with wave 1

`dashboard-state-card`, `brief-quality-benchmark`, `ticket-list-context`,
`ticket-went-quiet`.

dashboard-state-card imports `waitingOnYouTickets` from the counter's module (its
own doc says STOP if absent) and inherits the counter's changed `openBoardSurface`
signature (the closure at the App call site is mandatory — MouseEvent trap).
brief-quality-benchmark declares "lands after chief-guidance-brief-craft" and
picks its guidance-gate marker phrase from the **merged** text.
ticket-list-context lands after brief-craft so its one-clause
`ChiefGuidance` addition is coordinated rather than colliding. ticket-went-quiet
is technically independent but is the heaviest `TicketBoardPanel`/`BoardCard`
toucher — landing after the counter's filter edits minimizes rebases, its
`relativeTime` move (with re-export) is transparent to the dashboard's imports,
and its slice 3 is the designated home for wiring the dashboard's
`sessionStateForTicket` seam. Intra-wave: went-quiet and list-context both edit
`BoardCard` (chip vs location line) — distinct lines, second lander rebases.

### Wave 3 — explicitly deferred by their own docs

`ticket-rich-content`, `pr-tour-port`.

ticket-rich-content is usage-gated on the ticket-pane-overlay experiment (slice
(a) ships once the overlay shows tickets get read in-app), with further gates
between its own slices — it needs wave-1 soak time, not just code precedence.
pr-tour-port says "explicitly NOT first-wave": the cheap where/verify/deviations
report contract ships first via chief-guidance-brief-craft and must prove what a
good handoff report contains; the plan is direction-level and each slice gets its
own alignment pass before build.

## Seam registry

Ownership rules for surfaces more than one plan touches. Executing agents: build
on the owner's artifact; never redefine, fork, or "restore" it.

| seam | owner | everyone else |
|---|---|---|
| `app/src/utils/waitingOnYou.ts` (`waitingOnYouTickets`, `isWaitingOnYou`, `WAITING_ON_YOU_STATUSES`) | waiting-on-you-counter | dashboard-state-card **imports** it; nobody re-derives the blocked ∪ in_review union (not even `applyFilter`) |
| `openBoardSurface(initialFilter?: BoardFilter)` signature | waiting-on-you-counter | any caller passing it as a callback prop wraps it in a closure — a bare reference leaks the React `MouseEvent` into `initialFilter` (dashboard-state-card's call site and card test pin this) |
| `hooks.ChiefGuidance` (incl. embedded `TicketAwarenessGuidance`) | chief-guidance-brief-craft — all wave-1 edits | ticket-list-context appends ONE coordinated clause **after** it lands (outside the line budget, asserted substrings byte-identical); ticket-show / went-quiet stay off `hooks.go` entirely (skill references only) |
| `delegatedTicketPrompt` (`internal/daemon/delegate.go`) | chief-guidance-brief-craft (report contract) | later edits keep `TestChiefOfStaffDelegateBindsTicketAndPrompt`'s substring assertions green |
| `internal/agent/attn_skill/references/tickets.md` | no single owner — four plans edit different sections (show: drill-in read; brief-craft: re-brief line; list-context: branch column + collision signal; went-quiet: inbox `went_quiet` line) | second lander rebases; keep every string `assertAttnSkillTree` asserts |
| `internal/agent/attn_skill/references/delegation.md` | brief-craft (checklist tighten + fork line) | later edits preserve that guidance |
| `app/src/utils/ticketQuiet.ts` (`assigneeSessionState`, `ticketQuietChip`, the `relativeTime` move w/ re-export) | ticket-went-quiet | dashboard-state-card ships the `sessionStateForTicket` prop **unwired**; went-quiet slice 3 wires it at the `<Dashboard>` call site (moves to whichever PR lands second) |
| `handleResumeTicket` / the `onResume` callback | ticket-resume-fix (daemon `ticket_resume`; deletes `app/src/utils/ticketResume.ts`) | ticket-pane-overlay treats `onResume` as an opaque App callback; nobody restores `planTicketResume` |
| `TicketDetailPanel` render contract (optional handlers = editable) | unchanged by everyone | pane-overlay adds a third host; rich-content lands content upgrades inside the panel so every host inherits them — no per-host forks |
| `app/src/utils/tickets.ts` (`boundTicketForSession`) | ticket-pane-overlay | the automation bridge reuses it; single home for the session→ticket rule |
| `ProtocolVersion` | nobody — four near-term plans bump it (show, resume-fix, list-context, went-quiet; later rich-content b/c and pr-tour slice 1) | never pin the number in a doc or PR; rebase-and-re-bump is mechanical; do not batch bumps by assuming a final number |

## Rule of play

This umbrella tracks **plan-level status only** (the table's status column).
Each PR **updates its own plan doc's row/checkboxes** as part of the change —
the per-plan docs stay the source of truth for slice progress, exactly as the
work-tracker umbrella ran its slices. When a plan fully merges, flip its row
here to ✅ in the same PR that completes it.
