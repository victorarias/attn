# Context-full handoff for crew members

Slice 4 of the crew-birth superdraft. A member whose context nears full is
asked to hand off — the same presence-decided turnover `attn handoff` already
is — instead of drifting into its harness's auto-compact.

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
full context costs the day and only gets worse. It asks the same thing of a
session that auto-sleep does — that a prompt typed there would be read — and
not what the heartbeat asks, because closing IS an answer to whatever the
member was waiting for.

Delivery is the doorbell every other crew nudge uses. The prompt names both
numbers, because an agent can act on "at X of Y" and cannot act on a silent
compact.

It fires once per session. The condition holds for as long as the session
lives, so without that the ask would repeat every minute and a member mid-letter
would read the second copy as a different instruction. It re-arms only when the
context is observed back under budget — meaning the harness compacted anyway
and the session has room again. A new session is armed by construction.

## What is not covered

- Harnesses other than Claude Code and Codex report no usage at all, so their
  members are never asked. Stated in `SupportsContextOccupancy`.
- Occupancy is memory-only, so a daemon restart is blind to a session until its
  next turn. One turn, and the reading is absolute when it arrives.
