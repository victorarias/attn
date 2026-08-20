# Context-full handoff for crew members

Slice 4 of the crew-birth superdraft. A member whose context nears full is
asked to hand off — the turnover `attn handoff` already is — instead of
drifting into its harness's auto-compact.

A compact is not a nap. Compaction leaves harness narration as the member's
memory of the day, where a letter written by the member should be. On
2026-08-19 this fired for real: Trellis auto-compacted mid-day and the morning
survives only as a summary.

## The gate: what the daemon can actually read

The superdraft left one open question — what a daemon can know about a live
session's context usage, per agent. Answered by measurement, not by design.

- **Claude Code.** Assistant records in the session JSONL carry a `usage`
  block. Context occupancy is `input_tokens + cache_read_input_tokens +
  cache_creation_input_tokens` — the prompt the model was handed, which is
  exactly what the harness compacts. When a message made several requests the
  block carries `iterations`; the LAST one is the context the session is
  carrying. The transcript never states the context window.
- **Codex.** `event_msg` / `token_count` carries
  `info.last_token_usage.input_tokens` (already inclusive of the cached
  prefix, so it is the whole prompt) and, for free,
  `info.model_context_window`.
- **Everything else.** Nothing. `transcript.SupportsUsage` was already
  `claude | codex`, and `SupportsContextOccupancy` tracks it deliberately:
  both answers come off the same provider records. A member on another harness
  is never asked on this ground — no signal is faked, and the seam to add one
  is that single switch.

No new plumbing was needed. `internal/daemon/transcript_watcher.go` already
follows every live session's transcript at 500ms, and `internal/transcript`
already parses both formats for session cost. Occupancy is a second, different
quantity off the same records: cost SUMS a message's iterations because each
was billed; occupancy is the LAST one, because the earlier requests carried the
same context.

## Receipts

**The signal tracks the thing we want to beat.** Trellis's auto-compact
(session `c8badd0a`, fable-5, Claude Code 2.1.235) recorded
`compactMetadata.preTokens = 267,749`. The last occupancy attn could have read
from that transcript before the boundary was 266,998 — 0.3% below the
compaction point.

**The harness threshold cannot be predicted.** Measured 2026-08-19 over every
Claude Code transcript on Victor's machine: 265 real auto-compactions between
2026-08-05 and 2026-08-19, Claude Code 2.1.214 through 2.1.235, across opus-5,
fable-5 and opus-4-8.

| Claude Code | compaction point |
| --- | --- |
| ≤ 2.1.215 | ~186k (lowest observed 185,578) |
| ≥ 2.1.216 | ~267k |

It is not a fixed fraction of anything and it MOVED with a patch release. So
the design stays safely under the lowest ever seen rather than trying to
predict the next one.

**Closing costs little.** Across the five prompted handoffs in the record, the
tokens between the ask and the `attn handoff` call were 312, 363, 1,099, 7,525
and 16,916.

## The numbers

- `crewContextHandoffMargin = 25,000` — room for the member to close. Covers
  the worst observed letter by 1.5x.
- `crewContextBudgetDefault = 160,000` — how much context a day gets.
  `185,578 − 25,000 = 160,578`, rounded down. It is also comfortably under the
  200,000-token context window of the smallest model attn launches, so the
  budget can never sit above a member's ceiling.
- The budget is lowered to `window − margin` whenever a window IS known — Codex
  states one on every `token_count`, and a session launched with a
  context-window cap has one attn set itself. A member whose window is smaller
  than the budget would otherwise never be asked at all, which is the exact
  failure this exists to prevent.

Both are settings: `crew.context_handoff_tokens` and
`crew.context_handoff_enabled`.

## Shape

The decision joins the crew lifecycle policy (`internal/crew/lifecycle.go`) as
a third action beside the heartbeat and auto-sleep, and is read BEFORE cache
pressure and presence: a cache lapse costs a re-write and can be waited out, a
full context costs the day and only gets worse. It asks less of the session it
acts on than the heartbeat does — only that a prompt typed there would be read
— because closing IS an answer to whatever the member was waiting for, while a
heartbeat is an answer to nothing.

Delivery is the doorbell every other crew nudge uses. The prompt names both
numbers, because an agent can act on "at X of Y" and cannot act on a silent
compact.

It fires once per session. The condition holds for as long as the session
lives, so without that the ask would repeat every minute and a member mid-letter
would read the second copy as a different instruction. It re-arms only when the
context is observed back under budget — meaning the harness compacted anyway
and the session has room again. A new session is armed by construction.

## Continuity: the successor has to carry the work

The two closures attn asks for are not the same closure. Auto-sleep ends a day
nobody is watching, so a successor waking to stand around is exactly what it is
trying to avoid. A context-full close says nothing about whether the work is
done — the member ran out of room, not out of things to do — and the presence
rule would end it there, parking whatever was in flight until the user came
back.

So the ask names `--nap`, which already exists and means "start the next day
even if the user is away". Not a new handoff kind, and the costs are not
comparable: a successor nobody needed is one priming (~$0.15 of context), and
work parked overnight is the day.

The letter is asked for in resume shape for the same reason — what you were in
the middle of, exactly where it stands, and the first concrete thing to do —
because a successor that has to ask where things stand is a compact with extra
steps. Measured before the change: a member woken behind a plain context-full
handoff read its letter and answered "nothing to pick up, standing by".

Measured after, on a member appending one number per tool call to a file: it was
asked mid-count, wrote a letter naming "count.txt currently ends at 35" and
"First concrete step: run `echo 36 >> count.txt`", and its successor woke and
carried the file to 51 with no gap and no repeat at the seam. Nobody typed a
number, and nobody was asked what to do next.

## Mid-turn: reading and asking

Occupancy is read continuously — the transcript watcher follows every live
session at 500ms and is not gated on state — so a session's context is known
while it is working. Delivery used to be the gap: the crew tick stayed off a
session mid-turn, so a member could climb from comfortable to compacted inside
a single turn without ever being asked.

Measured over the same corpus, 286 auto-compactions:

| | count |
| --- | --- |
| the session went idle at or above 160,000 before compacting | 279 (97.6%) |
| the whole climb finished inside one turn | 7 (2.4%) |

For the 279, the runway from the first idle moment above budget to the
compaction was at least 117s (p50 33 minutes), so the 60s tick was never the
constraint. The 7 climbed from a last-idle occupancy of 33k-138k, the worst
burning 159,674 tokens without the session going idle once.

So the context ask types mid-turn, and it is the only one of the three that
does: a heartbeat has nothing to say to a session whose cache is being read as
we speak, and an absence keeps until the turn ends.

## The doorbell's screen guard

Typing into a working session is what made that safe to want, and unsafe to do
naively. A session's state is only as fresh as its last classification, and
claude's question tool parks on a selector without firing the permission hook
that would make attn call it `pending_approval` — so the state reads `working`
while the screen waits for a keypress, and a bracketed paste followed by Enter
answers it.

So the doorbell reads the authoritative viewport before it types, and again
before it presses Enter, and holds off when the screen is waiting on a
keypress. Every nudge gets this, not only the crew ones.

Receipt, over 44,724 viewports captured from live sessions between 2026-08-03
and 2026-08-13: 47 of 40,130 `working` screens were selectors, and 6 of 2,217
`idle` ones — the second group being the case the old comment already admitted
and nothing covered. All 25 distinct selector footers in that corpus say "to
select" or "Esc to cancel", so the rule reads those words rather than glyphs:
claude changed which glyphs it animates with inside one minor version and the
prose did not move. "to confirm" is deliberately not in the pattern; it appears
in assistant prose. The footer is read 8 lines up, where 6, 8 and 12 all find
the same 47 screens and 20 starts matching prose.

A refusal is not a failure: the caller is told the target is holding, and every
path that queues a message rather than erroring asks one predicate
(`doorbellDeferred`). A backend that cannot render — an older worker, a session
with no frame yet — delivers exactly as before, because failing closed on a
missing capability would turn a snapshot outage into a silent nudge outage.

## What is not covered

- Harnesses other than Claude Code and Codex report no usage at all, so their
  members are never asked. Stated in `SupportsContextOccupancy`.
- Occupancy is memory-only, so a daemon restart is blind to a session until its
  next turn. One turn, and the reading is absolute when it arrives.
- The screen guard is only as good as the words it reads. A harness that
  invents a selector saying neither "to select" nor "Esc to cancel" is not
  covered, and the corpus behind the pattern is claude-shaped: 44,344 of its
  44,724 viewports are claude. Codex approvals reach `pending_approval`, which
  the doorbell already refuses.
