# Plan: Session activity

## Why / Alignment

The dashboard says what state each agent is in, but not what it is *doing*. A
row reading `working` for twenty minutes carries no information; the user opens
the session to find out, which is the friction attn exists to remove.

**Activity** is one short present-tense line per session saying what the agent is
up to right now — "running the frontend test suite", "waiting on approval to
delete migrations/0042.sql". It is generated from the session's own transcript by
a configurable headless agent, refreshed when the session actually moves, and off
by default.

Activity is deliberately *not* summarization. The Notebook already owns
summarizing (`summarize_session`, `narrate_workspace`): durable, curated,
backward-looking, written for the user to read later. Activity is the opposite on
every axis — ephemeral, present-tense, regenerated constantly, glanced at rather
than read. Reusing "summary" for it would collide with a shipped concept and
mislead the next reader.

In scope: the activity generator, its trigger and throttle, per-session storage
and broadcast, the settings surface (enabled / agent / model / effort), the CLI
surface, and the dashboard rendering. Deferred: activity for remote-endpoint
sessions, activity history, and any use of activity as a routing or attention
input.

## Architecture Map

```text
app clients ──> set_client_presence {dashboard_visible} (on change + heartbeat)
                              │
                              v
                  presence tier = max across clients
                  watching(120s) / present(300s) / away(STOP)
                              │
    transcript cursor moved ──┤ throttled at the tier's interval
    turn opened            ───┤ bypasses the interval, never bypasses `away`
                              v
                    enqueueSessionActivity(sessionID)
                      UniqueKey "session_activity:<id>"
                              v
                        jobs queue (internal/jobs)
                              v
                        executeSessionActivity
                          -> transcript.ReadEventPage(path, agent, cursor, max)
                          -> render bounded event window + session state
                          -> HeadlessTaskProvider.RunHeadlessTask (tool-less,
                             no schema; claude|codex per activity.config)
                          -> sanitize to one line
                          -> store.UpdateSessionActivity(id, line, at, cursor)
                          -> publishFact(FactSessionActivityChanged, id)
                              v
                        wireProjections -> session snapshot push
                              v
                        dashboard row / sidebar
```

Reuses, without new machinery: `internal/agent`'s headless spawn seam
(`HeadlessTaskRequest` already carries `ReasoningEffort`, `MaxTurns`,
`MaxBudgetUSD`, `OutputSchema`), `internal/transcript`'s cursor-based event
reader, and `internal/jobs`' `UniqueKey` coalescing.

## Data Model / Interfaces

Session gains three persisted columns (migration; current max version in a real
DB is 92 — re-verify before numbering):

```go
Activity       string    // the line itself; empty when never generated
ActivityAt     time.Time // when it was generated — lets the UI age out a stale line
ActivityCursor string    // transcript cursor the line was generated through
```

Protocol (`internal/protocol/schema/main.tsp`, then `make generate-types`,
`ProtocolVersion++`, and the third lockstep spot in `useDaemonSocket.ts`):

```typescript
activity?: string;     // One-line present-tense description of what the agent is doing now.
                       // Generated from the transcript; absent when the feature is off or
                       // nothing has been generated yet.
activity_at?: string;  // ISO timestamp the line was generated. Present whenever activity is.
```

Settings, following the `model_capture.*` prefix pattern:

- `activity.enabled` — boolean, effective default **`false`**.
- `activity.config` — JSON `{"agent","model","effort"}`. **No default agent.**
  Blank means unconfigured, and enabling the feature without an agent selected is
  an error the user must see, not a silent fallback to Claude: the two agents
  differ enough in speed, price, and account that picking one for the user would
  be choosing how their money is spent. The UI must require a selection before
  the toggle takes, and the daemon must report the misconfiguration rather than
  guess.

  Per-agent defaults once an agent is chosen:

  | agent | model | effort |
  |---|---|---|
  | `claude` | `claude-haiku-4-5` | unset (measured inert on this model) |
  | `codex` | `gpt-5.6-luna` | `low` |

  Validation is a near-copy of `parseNotebookNarrationConfig`: driver installed,
  implements `HeadlessTaskProvider`, executable resolves on PATH.

- `activity.intervals` — JSON `{"watching","present"}` in seconds, default
  `{"watching":120,"present":300}`. One setting rather than three because they
  are a single policy; `away` is absent by design, since "stop" is not a rate.
- `activity.presence_idle_seconds` — integer, default `90`. How long after the
  last input in the app the `present` tier survives. **Unmeasured** — a guess
  pending a receipt. It is a safe one because `away` is self-healing (see
  below), so erring short costs a few seconds of latency on return and erring
  long costs runs nobody sees.

### Agent choice is measured, not a preference

Both agents run the same tool-less prompt through the same seam. Same 10-entry
corpus, same prompt, no output schema, two concurrent:

| agent / model | effort | p50 | p95 | in tok | out tok | $/run |
|---|---|---|---|---|---|---|
| claude / claude-haiku-4-5 | — | 11.7s | 19.4s | 46,745 | ~700–990 | $0.0108–0.0165 |
| codex / gpt-5.6-luna | `none` | 6.2s | 9.4s | 13,477 | 15 | $0.0027 |
| codex / gpt-5.6-luna | `low` | **4.8s** | **5.7s** | 13,477 | 15–53 | $0.0027 |
| codex / gpt-5.6-luna | `medium` | 4.6s | 11.1s | 13,479 | 40–66 | $0.0027 |

**Codex is ~2x faster and ~5x cheaper.** The gap is not model quality, it is what
each CLI does around the model. For one 90-character answer Claude Code bills a
46,745-token prefix and generates ~700–990 output tokens — of which ~600 is
extended thinking that cannot be turned off. Codex bills 13,477 and generates
15–66.

Line quality is deliberately **not** compared here. Both models are treated as
good enough; the choice between them is speed and price. The harness's pass rate
cannot support a quality claim anyway, for two reasons worth knowing before
anyone quotes one:

- The corpus is 27 `working` entries against 1 `idle`, so `state_consistency` —
  the only check that separates a good line from a fluent wrong one — is
  exercised once per benchmark run. Grow the blocked-state entries before
  trusting it.
- `state_consistency` matches a vocabulary list, so it reads tense by accident.
  "Built the harness; found a cost error" is correct for `idle` and fails;
  "Running the test suite" is wrong for `idle` and fails for the same reason. The
  rule it is trying to express is about tense and ongoing-ness, which a word list
  cannot say.

Effort is nearly free to choose on Luna: cost is flat at $0.0027 across all three
because input dominates completely (reasoning contributes 0–50 tokens against
13,477 of prefix), and the latency spread is inside the noise. **`low` is the
default** on the strength of its p95 — it produced the tightest tail while still
reasoning on the inputs that warranted it.

Luna pricing used above: $0.20/1M input, $1.20/1M output, $0.02/1M cached input.
Codex reports no cost in its result envelope, so `$/run` is computed from the
token counts it does report. Prompt caching fired on **1 of 12** probe runs
(9,984 cached, $0.00098) — real, but too rare to budget for, since each session's
window differs and every run is a fresh `--ephemeral` process.

## The trigger

Generation is gated by **what the user is doing**, not by what the agents are
doing. An activity line is glanced at; a line nobody can see has no value, so
generating one is pure waste. Three presence tiers, derived by the daemon from
what clients report:

| tier | the user is | interval |
|---|---|---|
| `watching` | the app is visible and showing the dashboard | 120s |
| `present` | input in the app within `presence_idle_seconds`, dashboard not showing | 300s |
| `away` | neither | **nothing is generated** |

`away` is a hard stop, not a slow rate. The app closed, the app in the
background, the user gone from the keyboard — all produce zero runs and zero
spend, indefinitely.

Two preconditions apply inside an active tier, and both are what keep the count
far below "sessions × rate":

- **The transcript cursor must have moved.** A session that has not written
  since its last line has nothing new to say. Blocked and finished sessions cost
  nothing even while the dashboard is open, because their line is still true.
- **A turn opening bypasses the interval.** Reaching a state that wants the user
  is the moment the line matters most, and `internal/attention` already owns that
  concept. It breaks through the interval — it does **not** break through `away`.

### Why `away` can be a hard stop

Because leaving `away` is always an action, and that action restores the tier.
There is no path back to the dashboard that does not make the app visible. So the
staleness `away` creates is self-healing: the moment it would matter, generation
has already resumed.

The cost is latency on return — the last line shows with its `ActivityAt` age
while a fresh one is generated. That is honest, and it is why the seam's latency
matters (measured: ~6s p50 on Codex, ~12s on Claude).

An earlier draft kept state changes generating while unobserved. Measurement
killed it: real transitions run at **13 per session-hour** (p50 7.1/session, p95
29.8, from 48.7 observed session-hours of capture data), against 30/hr for the
`watching` tier. Unobserved breakthroughs would have cost nearly half of
continuous generation while producing lines for an empty room.

### What clients report, and why it is a heartbeat

The frontend is the only place that knows window visibility, which view is
rendered, and when input last happened. It sends presence; the daemon derives the
tier and owns the intervals — the frontend must not decide policy.

Presence is sent on change **and** as a periodic heartbeat, and the daemon expires
a client that goes quiet. That ordering matters: a latched boolean leaks. A client
that crashes, is force-quit, or loses its socket mid-`watching` would otherwise
pin the daemon at 120s generation forever, with nobody looking. An expiring
heartbeat fails to `away`, which is the safe direction.

The daemon takes the **highest tier across all connected clients** — two windows
open, one showing the dashboard, means `watching`.

Not persisted. On daemon restart no client has reported yet, so the tier is
`away` and nothing generates until an app connects and says otherwise.

### Known gap: subject changes within `working`

An agent that finishes the migration and starts on tests never leaves `working`,
so nothing breaks through, and the line can be stale for up to one interval. This
ships unsolved on purpose — it may not be noticeable at 120s, and the cheap fix
is available if it is: break through on a deterministic local signal (the tool
name or its target file changed against the last run's), which needs no model to
detect. Do not build that until the interval is observed to be a problem.

### Deferred: remote sessions

A remote-endpoint session's transcript lives on the remote daemon, and its
presence signal originates in an app connected to the hub. Making the tier
propagate hub → remote is real work and is out of scope here; remote sessions
generate no activity until it is done.

## Why ReadEventPage and not ExtractConversationSlice

`session_title.go` is the closest existing precedent and it uses
`transcript.ExtractConversationSlice`. Activity must **not** copy that choice.
`ConversationSlice` documents itself as carrying "no tool-call/activity trace" —
it keeps the human's brief, re-scoping turns, and the agent's last prose turns,
because it answers *what is this session about*. That is the right reduction for
a title and the wrong one for activity, where the tool calls are the whole story.

`ReadEventPage(path, agent, cursor, maxEvents)` keeps `tool_call` / `tool_result`
kinds and pages **strictly forward from a byte offset**, so a refresh seeks to
the stored cursor and reads only what was appended. Measured: a full read of the
largest live transcript (32.3 MB, 5,681 events) takes 1.37s; the delta read
replaces that with a seek plus the appended bytes.

The window the model sees is therefore "everything since the last line", which is
literally the last few moments — self-bounding at roughly the tier interval
worth of events during steady work, and much smaller when a state change breaks
through.

### Thinking is the signal, and today it is discarded

`internal/transcript` extracts assistant `text` blocks and drops everything else;
it contains no reference to thinking or reasoning. That throws away most of the
prose. In one real transcript: **1,890 thinking blocks against 864 text blocks.**

Measured across 604 active 3-minute windows:

| | count | share |
|---|---|---|
| windows with no assistant text | 101 | 17% |
| ...of those, containing thinking | 77 | **76% rescued** |
| truly prose-less (no text, no thinking) | 24 | **4%** |

Without thinking, roughly one window in five is pure tool traffic with nothing
stating intent. With it, that falls to one in twenty-five. Thinking is also the
richer signal — median 368 chars against text's 140 — because it is the agent
narrating what it is trying to do, which is exactly what an activity line is.

So `internal/transcript` gains a `thinking` event kind:

- **Claude**: `thinking` content blocks.
- **Codex**: `agent_reasoning` payloads, whose `text` is already close to the
  target (a real sample reads `**Preparing workspace and researching latest
  CVEs**`). Availability varies by rollout — of three recent sessions one carried
  40 `agent_reasoning` entries and two carried none, holding only `reasoning`
  with `encrypted_content` and an empty summary. Treat it as a bonus signal, not
  a dependency.

This is a change to a shared package. Verify that existing `Event` consumers
tolerate an unknown kind rather than assuming they do, and apply `RedactText` —
thinking is the agent's unfiltered reasoning and it is about to leave the machine.

### Bounds

Both must be visible when hit, never silent:

- `maxEvents` and a total char budget cap a burst. When the page comes back not
  `AtEnd`, the run uses the newest events and reports the truncation the way
  `RecentAssistantMessagesReport` does rather than presenting a silently short
  window.
- Per-event clipping: thinking 600, assistant 400, user 300, tool_call 200,
  tool_result 150. Thinking is clipped hardest in absolute terms because it is
  the longest content type (p95 3,520 chars, max 20,443).

**Tool results are kept, including non-error ones.** Dropping them would shrink
the p95 window by ~40%, but the saving is a few dollars a month against a 200K
context — there was never a budget to defend — and non-error results carry real
signal: "42 passed, 3 failed", a grep that found nothing, a build that succeeded.
The per-event clip is enough to stop one giant result crowding out the window.

Measured window sizes with thinking included and tool results kept, across 617
active windows:

| | p50 | p95 | p99 | max |
|---|---|---|---|---|
| events | 12 | 46 | 54 | 64 |
| rendered chars | 2,777 | 9,307 | 11,207 | 12,408 |
| ≈ tokens | 694 | 2,326 | 2,801 | 3,102 |

Tripwires set generously past the observed max, so only something broken touches
them: **200 events, 32,000 chars**.

### The previous line is the anchor

The delta alone can be near-empty exactly when the line matters most. A
turn-opening breakthrough fires within seconds of the flip, so little or nothing
has been appended — and on `pending_approval` the pending `tool_call` is *never
written to the transcript at all* (measured: the last event predated the state by
2.0 minutes). The most important line would otherwise have the least input.

So the run is fed the **previous activity line** alongside the delta. It costs
zero extra reads, which is decisive: every other anchor (`ExtractLastAssistant-
Message`, `ReadRecentAssistantMessages`) full-scans from byte 0 via
`readJSONLLines` and would reintroduce the 1.37s scan on every refresh. Only
`ReadEventPage` seeks.

It also buys continuity for free — "still running the test suite" becomes "tests
pass, fixing lint" — and makes the breakthrough case work: previous line + new
state yields *"blocked approving a file delete — was running the test suite"*
even with an empty delta.

The risk is **echo**: a line fed back to itself can keep a stale subject alive
indefinitely and look plausible the whole time. The prompt must state that the
delta is authoritative and the previous line is only context. The trigger design
already guarantees a run never happens with both an empty delta and an unchanged
state, and cold start plus cursor re-seed both clear it. This is a "working but
wrong" failure mode, so it goes on the live-verification list explicitly rather
than being assumed away.

Two cursor failure modes need explicit handling, because both are normal:

- **Cold start** (no stored cursor). Do not scan a 32 MB transcript to produce a
  first line. Seed the cursor at head and let the next real movement generate the
  first line.
- **`ErrCursorMismatch`** (the transcript was rewritten — Claude compaction does
  this). Re-seed at head, log it, and skip one generation rather than failing the
  job repeatedly against a cursor that will never validate.

## Cost

**The window is not what this costs.** Measured through the real headless seam
(`claude -p`, tool-less, `--max-turns 2`) with default arguments, a run costs
**$0.016–$0.022** — and a minimal control prompt ("Say the word: hello") costs
$0.0217, *more* than a full activity run. The result envelope says why:

```
input_tokens:    10        <- the actual prompt
cache_creation:  9,453     <- Claude Code's own system prompt
cache_read:      17,857    <- ...and its tool definitions
output_tokens:   81
```

Roughly 27K tokens of fixed harness overhead per invocation, paid whether the
prompt is 10 tokens or 3,000. Against that, the whole measured window is noise:

| | tokens in | marginal cost | actual cost via `claude -p` |
|---|---|---|---|
| p50 window | 694 | $0.0008 | ~$0.016 |
| p95 window | 2,326 | $0.0024 | ~$0.018 |
| control (10 tokens) | 10 | $0.00001 | $0.0217 |

**Shrinking the window is therefore the wrong lever.** Every clip budget above,
tuned together, moves ~$0.0007 of a ~$0.021 run. The clip budgets exist to keep
one giant tool result from crowding out the window, not to save money. The cost
is the harness prefix and the generated output; the sections below take each in
turn.

### Trimming the prefix, measured

The prefix is partly reducible, and what remains can be made to cache. Measured
through the driver's own argv, sequentially, on a real corpus window (1,497-char
system half, 457-char user half), three runs each:

| | prefix tokens | rewritten per run | steady cost | latency |
|---|---|---|---|---|
| default headless args | 46,745 | 5,821 | $0.0210 | ~10s |
| `--system-prompt <the invariant half>` | 33,955 | ~50 | **$0.0100** | ~17s |

Two effects, and the second is the larger one. Replacing the CLI's own system
prompt removes ~12.8K tokens outright; it also removes the *volatile* sections
inside it (cwd, date, git state), which were invalidating the cache suffix on
every single run — `cache_creation` falls from 5,821 to ~50 and the prefix becomes
almost pure cache read. That is also why
`--exclude-dynamic-system-prompt-sections` measured as doing nothing on its own:
`--system-prompt` already subsumes it.

So **~$0.010/run steady, down from ~$0.021.** The prompt splits at the `{{USER}}`
marker: the invariant half (role, state glossary, output contract) goes to
`--system-prompt`, the per-run half (state, previous line, window) stays the user
turn.

This needs one additive field on `HeadlessTaskRequest`, which the Claude driver
did not have (it exposed `--append-system-prompt` for interactive launches only):
`SystemPrompt` → `--system-prompt`. It benefits every bounded headless duty.

### Rejected: stripping the tool definitions

`DisableTools` makes the native tools uncallable but still ships their
definitions in the billed prefix. `--disallowedTools "*"` does remove them, and
it is dramatic — the control prefix falls to **2,309 tokens**. It is also
unusable: it disables `StructuredOutput`, the tool the CLI uses to deliver a
`--json-schema` answer. The run reasons its way to a correct line, reports *"I
don't have permission to write the dashboard line"*, returns no structured output
and exits non-zero. Reproduced twice; the whole benchmark went to 0% pass on it.

An enumerated disallow-list avoids that (it can omit `StructuredOutput`) and
measured 23,021 against 37,018 prefix tokens with a schema in play. It is still
rejected: the list has to track the CLI's tool set forever, and when it drifts it
fails the same way — silently, by losing the answer. An outdated list also buys
almost nothing (a stale 5-name list measured 16,988 against 2,309 for `"*"`), so
the maintenance is load-bearing rather than optional. Cost saving whose failure
mode is losing the answer is not worth having.

Two further findings from the same sweep:

- **`--bare` is unusable** unless an API key is in the environment: it skips
  credential resolution and the run dies with `Not logged in`. The driver already
  gates it correctly (`claudeHasBareModeAuthentication`).
- **`--disallowedTools` is variadic** and will swallow a trailing positional
  prompt, after which the CLI rejects the run for having no input.

### Output tokens are the latency, and on Claude they are not ours

A run's wall time is almost entirely generation. Startup is ~1.2s; the rest is
the model emitting ~930 output tokens to deliver a 20-token answer at ~65 tok/s.
Dumping the stream shows exactly where they go:

| block | size | what it is |
|---|---|---|
| thinking | 1,715 chars | deliberating about the task |
| **text** | **67 chars** | **the answer, already correct** |
| thinking | 568 chars | deliberating about how to call a tool |
| tool_use | — | `StructuredOutput`, repeating the same answer |

Two conclusions, one actionable and one structural.

**Drop the output schema.** The answer is the final text. Asking for it as a
schema-validated object bought a second reasoning turn to restate what turn one
already said: 14.2s/$0.0089 with the schema against 10.6s/$0.0059 without, same
input. It also costs nothing on Codex, whose tool-free path has no schema support
at all, so removing it makes both agents answer the same way.

**It does not fix the thinking, and nothing will.** Without the schema the run
collapses to one turn and loses the duplicate answer, but the primary thinking
block survives intact — a no-schema run still emits **~700–990 output tokens**,
composed of one ~2,400-char thinking block and a 68-char answer. `--effort` is
measured inert on claude-haiku-4-5: none, low, medium and high all land between
862 and 1,047 output tokens on identical input. A terser prompt does not help
either (9.3s against 10.6s). ~9s is the floor for this seam.

This is the whole reason the agent choice matters. Codex on the same task emits
15 output tokens with `reasoning_output_tokens: 0`, because there `--effort` is
live and `none` means none.

### Cost scales with attention, not with uptime

The presence tiers are the cost control, and they are a better one than any
interval because `away` multiplies by zero. A month is roughly:

    runs ≈ sessions_moving × (watching_hours × 30 + present_hours × 12)

"Sessions moving" is the load-bearing term and it is much smaller than "sessions
open": a session only generates when its transcript cursor advanced, so blocked,
finished, and idle sessions contribute nothing even with the dashboard open.

A working month — 1h/day watching the dashboard, 4h/day present, 3 sessions
actually moving, 22 days — is ~5,100 runs: **~$14 on Codex/Luna, ~$85 on
Claude/Haiku.** Turning the app off, or walking away, costs nothing at all.

Both intervals are configurable, so the cheap end is reachable without a code
change, and the feature is off until someone turns it on.

## Boundaries

- The generator is daemon-owned. The frontend renders `activity` and never
  derives or requests one.
- The activity agent is configured globally and independently of the session's
  own agent. Unlike auto-titling (which uses the session's agent), a Codex
  session's activity may be written by Claude Haiku — this is a dashboard
  utility, not something the session should pay for or influence.
- The run is **tool-less and bounded**: no filesystem access, `MaxTurns` 1, an
  `OutputSchema` pinning `{"activity": string}`, and a `MaxBudgetUSD` tripwire.
  It reads a rendered prompt and returns a string.
- Only sessions with a transcript and a `HeadlessTaskProvider`-capable agent
  participate. Shells and satellites have no transcript and are skipped.
- Activity is display-only. It must not feed attention routing, the queue, or
  state classification — attn already decides those deterministically, and making
  a model line load-bearing would put an LLM in the path of the product's
  most-trusted behavior.

## The harness

Prompt quality is the whole product here and it cannot be unit-tested. `cmd/activity-bench`
is the experiment loop: freeze real windows as a corpus, run a matrix of
prompt × agent × model × effort against it, and report cost, latency, and what
each variant actually wrote.

It runs the **real** code path — `internal/transcript` for the window and
`internal/agent`'s `HeadlessTaskProvider` for the run — so what is benchmarked is
what ships. A Python reimplementation would benchmark a fiction.

```bash
activity-bench corpus   # freeze windows from real transcripts -> local fixtures
activity-bench run      # matrix over prompts x models x efforts
activity-bench report   # comparison table + side-by-side lines
```

- **Corpus.** Sampled from real transcripts, stratified by state and by whether
  the window has prose, so the set is not all easy cases. `RedactText` applied.
  Written to a gitignored local directory — real windows carry source code and
  conversations and are not committed, the same call the model-capture plan made.
- **Prompts are files**, loaded at runtime from `prompts/activity/*.md`, so
  iterating a prompt needs no rebuild.
- **Deterministic checks** catch the failure modes already observed without an
  LLM judge: line length, no preamble or quotes or trailing period, and
  state-consistency — if the state says blocked, a line describing active work is
  a failure. That last one is the exact defect the spike produced before state
  was in the prompt.

  Treat its pass rate as a smoke test, not a score. It matches a vocabulary list,
  so it cannot tell past tense from present ("Built the harness" fails for `idle`
  though it is correct), and the corpus gives it one blocked entry to fire on.
  Both are worth fixing before the harness is used to choose between prompts.
- **Report** shows per-variant pass rate, p50/p95 latency, cost per run, and the
  generated lines side by side. Eyeballing stays part of the loop; the checks
  exist to make regressions loud, not to replace judgment.

Beyond prompt iteration, the harness is where the open numbers get settled: the
tier intervals, per-kind clip budgets, and whether a cheaper model holds up.

## Implementation Steps

- [x] `thinking` event kind in `internal/transcript` (Claude `thinking` blocks,
      Codex `agent_reasoning`), with `RedactText` applied.
- [x] `HeadlessTaskRequest.SystemPrompt` -> Claude `--system-prompt`; folded into
      the prompt on Codex, which has no such flag. `--effort` wired on the Claude
      headless path, where it was never passed.
- [x] `cmd/activity-bench`: corpus, run, report, deterministic checks.
- [ ] Migration + store columns + `UpdateSessionActivity`; protocol fields,
      `make generate-types`, `ProtocolVersion++`, `useDaemonSocket.ts`.
- [ ] **Presence.** `set_client_presence` command; per-connection state cleared
      on disconnect and expired on heartbeat silence; tier reduction across
      clients; the tier readable for diagnosis.
- [ ] **Frontend presence reporting.** Window visibility + dashboard route +
      input recency, sent on change and on a heartbeat.
- [ ] `activity.*` settings with validators (near-copy of
      `parseNotebookNarrationConfig` / `validateNotebookNarrationSetting`),
      including the no-default-agent rule: enabling without a selected agent is a
      reported error, never a silent fallback.
- [ ] The job kind, executor, prompt builder, and one-line sanitizer.
- [ ] Tier-driven trigger: cursor movement throttled at the tier interval, turn
      opening bypassing the interval, `away` suppressing everything; the
      `FactSessionActivityChanged` fact and its `wireProjections` entry.
- [ ] Cold-start and cursor-mismatch re-seeding.
- [ ] Settings UI: toggle, required agent choice, model, effort, both intervals.
- [ ] Dashboard rendering, ageing out a line well past its interval rather than
      showing a stale line as current.
- [ ] CLI: `activity` in `attn list --json`; a way to clear a wrong line; a way
      to see the current presence tier, since a feature that silently stops
      needs a way to tell "off" from "away".
- [ ] Tests: the interval suppresses cursor movement inside the window and fires
      on the first movement after it; a turn opening fires inside the window;
      `away` suppresses both; a client that stops heartbeating expires to `away`;
      the tier is the max across clients; cursor advance; cursor-mismatch
      re-seed; truncation reporting; settings validation including the missing
      agent; and a projection test so the broadcast is not a direct hub write.
- [ ] Glossary entries for **activity** and **presence tier**, drawing the line
      against the Notebook's journal and summaries. Changelog fragment.
- [ ] Live verification on a throwaway profile: enable with each agent, watch
      lines appear while the dashboard is visible, confirm hiding the dashboard
      slows generation and leaving the app stops it entirely, confirm returning
      resumes, disable.

## Decisions

- **Name it activity, not summary.** The Notebook owns summarizing; the collision
  would mislead every future reader. Activity is present-tense and disposable.
- **Off by default, and the agent is chosen explicitly.** It spends money per
  session per refresh and sends transcript excerpts to a model. There is no
  default agent: Claude and Codex differ enough in speed, price, and account that
  picking one would be choosing how the user's money is spent.
- **Generation follows the user, not the agents.** Three presence tiers —
  `watching` 120s, `present` 300s, `away` stop. A line nobody can see has no
  value, and `away` multiplies the bill by zero. This replaces an earlier
  always-on throttle whose cost scaled with agent uptime.
- **`away` is a hard stop, including for turn openings.** Safe because leaving
  `away` is always an action that restores the tier, so the staleness is
  self-healing. Measurement forced this: real state transitions run at 13 per
  session-hour against 30/hr for `watching`, so generating through `away` would
  have cost nearly half of always-on for an empty room.
- **Presence is a heartbeat, not a latch.** A latched boolean pins generation
  forever when a client dies mid-`watching`. Expiry fails to `away`, which is the
  safe direction.
- **No output schema.** The answer is the final text. The schema bought a second
  reasoning turn restating turn one: 14.2s/$0.0089 against 10.6s/$0.0059. It also
  unifies the two agents, since Codex's tool-free path has no schema support.
- **Cursor movement is still a precondition inside every tier.** A session that
  has not written has nothing new to say, and its existing line is still true.

## Follow-ups

- Activity for remote-endpoint sessions, once the transcript path question there
  is settled.
- Consider a local small model instead of a hosted one. No local runtime is
  installed today (no ollama / llama-cli / mlx_lm), so this is unproven — the
  `model-captures` corpus exists partly to evaluate it.
- The `model-captures` corpus retains a narrower use this feature does not need:
  reading the screen for a session with no transcript (an agent running a TUI, or
  a CLI attn has no reader for).
