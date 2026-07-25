# Plan: near-perfect agent state detection

## Goal

Make session colors correct essentially all the time for Claude Code and Codex, so
that a **settled / unsettled** attention mode can be built on top of them. Today's
detection is right most of the time but has three reproducible failure classes:

1. **Stuck states.** A state can persist forever with no live evidence behind it.
2. **Background work.** A Claude turn that yields with `attn wait-pr` (or any
   background Bash/Workflow) outstanding is governed by one binary rule.
3. **Guardian flash.** Codex auto-approve routes every approval to its
   `auto_review` guardian LLM, but attn renders the guardian's latency as a
   user-facing `pending_approval` (yellow) for a second or two on every Bash call.

Phases 0–3 are about the colors themselves and must stand on their own: they are
worth shipping even if no attention mode ever exists.

**Attention mode sits on top and is opt-in.** It is a working mode the user
enables, off by default at release — a different projection of the same resolved
state, never a second source of truth. The end state, with it enabled, is an agent
that unsettles itself when it is stuck, done-and-stale, or has a question, and
stays quiet otherwise. With it disabled, nothing about the sidebar changes except
that the colors are right.

## Why the current design produces these failures

Every source calls `applyState` directly, and every source is **edge-triggered**.
There is no level signal, no evidence TTL, and no periodic re-evaluation.

```text
Current (last-writer-wins, all edges):

  claude/codex hooks ──┐
  stop classifier ─────┤
  transcript watcher ──┼──> applyState(sessionID, state, cause) ──> store ──> broadcast
  pty screen scrape ───┤        (cause selects effects only;
  plugin driver ───────┘         no arbitration between sources)
  process exit ────────┘
```

`pending_approval` is set by an edge (`PermissionRequest` hook) and cleared by a
different edge (`PostToolUse`, or `approvalResolver` seeing the prompt leave the
screen). Drop either edge — hook spawn fails, the agent emits no output, the TUI
renders an approval shape `isPendingApproval()` does not match — and the state is
stuck with nothing in the daemon that will ever revisit it.

`internal/pty/state_detector.go` is the only fallback and it is keyword matching
over ANSI-stripped screen tails (`"allow"` + `"confirm"` + `"y/n"` …). It
false-positives on assistant prose and false-negatives on any TUI change.

## The unused signals (verified on live PTYs, 2026-07-25)

Raw PTY captures of `claude` 2.1.220 and `codex`, driven through a real pty:

```text
claude:  ESC ] 0 ; ⠐ Run background sleep command BEL   <- braille spinner = turn RUNNING
         ESC ] 0 ; ✳ Run background sleep command BEL   <- U+2733 = turn NOT running

codex:   ESC ] 0 ; ⠸ attn--fix-state-detec... BEL       <- spinner prefix = busy
         ESC ] 0 ; attn--fix-state-detec... BEL         <- bare cwd = idle
```

- **OSC 0 title glyph is a level, refreshed ~1Hz, emitted by the harness itself.**
  Braille block (U+2800–U+28FF) = running; `✳` (claude) / no glyph (codex) = not
  running. A level that stops arriving cannot get stuck — though the spike below
  shows it goes briefly silent mid-tool, which is why it corroborates rather than
  leads.
- **OSC 777 is not a signal claude emits.** An earlier draft of this plan listed
  it as claude's explicit settle event. It is not: no OSC 777 appears in any of
  the nine captures, and claude 2.1.220 offers only `iterm2`, `iterm2_with_bell`,
  and `terminal_bell` notification channels. Claude's settle event is the
  **`Notification` hook**, below. Corrected while implementing phase 1a, after
  building and then deleting the OSC 777 reader.
- The title also carries a live turn summary ("Run background sleep command") —
  a free sidebar label with no LLM call.

attn parses OSC 133 in `internal/pty/blockfeed.go` and ignored OSC 0
entirely. Unwired hooks: Claude **`Notification`** (fires on permission-needed and
on 60s-idle-waiting), `SessionEnd`, `SubagentStop`; Codex's `notify` program
(`agent-turn-complete`).

## Spike results (2026-07-25)

Throwaway spike in `internal/pty/spike_state_replay_test.go` and
`internal/pty/spike_resolver_test.go` (both skip unless `ATTN_SPIKE_CAPTURES` is
set). Nine scripted `claude`/`codex` sessions were driven through a real PTY with
byte-level timestamps, while a parallel hook log recorded what the harness itself
believed — independent ground truth. Detectors were replayed against it at 200ms
resolution. Codex hook truth needed attn's own trusted-hash overrides, emitted by
`internal/hooks/spike_codex_overrides_test.go`.

Ground truth comes from hooks, so any hook-driven detector scores well by
construction. Two things had to be corrected before the numbers meant anything:
hooks were rebased onto the stream clock by anchoring the first `UserPromptSubmit`
to the scripted keystroke (`SessionStart` fires seconds late and varies), and a
`PermissionRequest` under an automated reviewer was relabelled as tool execution
rather than a user-facing approval — the guardian resolves it in milliseconds and
there is no hook at the moment it does.

| capture | current (screen scrape) | OSC heartbeat only |
|---|---|---|
| `run_approval` (auto mode) | 85.6%, worst wrong streak 13.2s | 95.3%, 3.0s |
| `run_approval2` (real 27.5s approval) | 15.7%, **43.4s** | 67.0%, 27.6s |
| `run_bg` (background task) | 71.4%, 11.4s | 93.6%, 3.0s |
| `run_quiettool` (38s background) | 71.5%, 15.2s | 93.4%, 2.6s |
| `run_claude_fg` (7.4s foreground tool) | 73.4%, 21.8s | 95.1%, 2.4s |
| `run_codex_fg` (35.1s foreground tool) | **1.0%**, 40.2s | **99.0%**, 0.4s |
| `run_falsepos` (prose only) | 16.0%, 13.6s | 81.5%, 2.6s |
| `run_interrupt` | 36.9%, 15.4s | 91.0%, 2.2s |

What the numbers mean:

- **The heartbeat is real.** Claude emits a spinner frame every 0.97s (max
  observed gap 0.97s); codex every 0.102s (max gap 0.15s). It tracks harness truth
  to within ~100ms: the spinner appears **+0.02s to +0.08s** after
  `UserPromptSubmit`, and the idle glyph lands within **+0.11s** of `Stop`.
- **A long foreground tool breaks a heartbeat-only design.** When Claude starts a
  blocking foreground Bash call it sets the title to ✳ and **emits nothing for
  3.5s** before the spinner resumes. (Codex does not: through a 35.1s blocking
  `sleep`, it emitted 300 consecutive busy frames, max gap 0.125s.) That produces a
  false *settled* mid-turn, and the TTL sweep shows no value escapes it:

  | TTL | correct | worst wrong streak | mean late-settle | **worst false-settle** |
  |---|---|---|---|---|
  | 0 (idle glyph as an edge) | 76.4% | 40.0s | 0.05s | 40.0s |
  | 0.5s | 92.2% | 3.8s | 0.30s | 3.8s |
  | 1.0s | **97.8%** | 3.4s | 0.70s | 3.4s |
  | 2.0s | 96.1% | 2.4s | 1.70s | 2.4s |
  | 3.0s | 94.5% | 3.0s | 2.70s | 1.4s |
  | 4.0s | 93.0% | 4.0s | 3.70s | 0.4s |
  | 5.0s | 91.4% | 5.0s | 4.70s | 0.2s |

  Killing the false settle needs TTL ≥ 4s, which costs 3.7s of late settle and 5pp
  of accuracy. **There is no good TTL, so the heartbeat is not a detector on its
  own.** TTL=0 is also ruled out independently: the title blips idle mid-turn even
  without a tool (✳ at 14.25s, spinner again at 14.28s).
- **Title hijack does not break it.** Injecting a competing `ESC]0;victor@mac: ~`
  every 2s, and a continuous `ESC]0;htop` repaint for 30s, moved accuracy by
  +0.2pp (93.8% → 94.0%). The resolver keys on *the presence of spinner frames*,
  not on the last title's content, so a subprocess overwriting the title is simply
  ignored while the agent keeps painting.
- **The current detector's failures reproduce exactly as reported.** On
  `run_approval2` it claimed `working` for **43.4 continuous seconds** after the
  turn had stopped — the stuck-green bug, on tape. On `run_falsepos` it emitted
  `pending_approval` for 8.2s with no approval anywhere in the session; the trigger
  was **the user's own typed prompt** echoed in the input box, containing the words
  "Do you want to proceed? 1. Yes 2. Yes, and don't ask again". Typing about
  approvals, pasting one, or reviewing code that renders one turns a session
  yellow today. These states reach the store: `handlePTYState` applies them as
  `liveSignal` with no arbitration against the hooks they contradict.
- **The OSC signal cannot see approvals, and should not.** During an approval the
  title goes idle — the agent genuinely is not running. `run_approval2`'s 27.6s
  worst streak *is* the approval window. Approvals stay hook-owned. The valuable
  part is the other edge: the spinner resumes **+0.05s** after the approval is
  granted, which is a far better "approval resolved" signal than today's
  `approvalResolver` waiting for the prompt to vanish from the screen.
- **Background work confirmed.** In `run_quiettool` the title went idle at 16.0s
  (0.11s after `Stop`) and then emitted **nothing at all for 38 seconds** while
  the background task ran, resuming 0.05s after the auto-resume. The heartbeat
  correctly reports "not thinking"; green during that window must come from the
  Stop payload's `background_tasks`, as planned.

- **What the heartbeat is actually for: bounding stuck states.** Since hook-derived
  truth makes a hook-driven detector score 100% by construction, the honest
  experiment is the failure mode that bites in production — a **lost hook** (spawn
  failure, socket error, an event the agent never fires). Dropping hooks at
  increasing rates, averaged over 20 seeds per capture
  (`spike_resolver_test.go`, TTL 1.0s, staleAfter 4.0s):

  | hooks dropped | hooks only | hooks + heartbeat |
  |---|---|---|
  | 0% | 100.0%, worst streak 0.0s | 98.5%, worst **1.0s** |
  | 5% | 98.3%, worst **60.0s** | 98.3%, worst **3.8s** |
  | 10% | 94.9%, worst 60.2s | 97.2%, worst 27.6s |
  | 25% | 84.6%, worst 60.2s | 95.9%, worst 27.6s |
  | 50% | 70.9%, worst 60.2s | 93.1%, worst 43.4s |

  One dropped `Stop` strands a hooks-only design green **for the rest of the
  session** (60s = the remainder of the capture). Adding the heartbeat cuts that
  stuck window to 3.8s — a 16x reduction — at a cost of 1.5pp of steady-state
  accuracy when hooks are perfect. That trade is the whole point: the heartbeat
  does not make the common case more accurate, it makes the bad case bounded.
  The hybrid's residual 27.6s/43.4s streaks are approval windows, where a dropped
  `PermissionRequest` leaves nothing to recover from.

Two incidental findings worth keeping:

- Victor's global `~/.claude/settings.json` sets `defaultMode: auto`, so Claude
  already runs a permission classifier on every session attn launches.
  `ReviewerInLoop` therefore cannot be inferred from attn's launch flags alone —
  it must account for the resolved permission mode. Measured guardian round trip:
  `PermissionRequest` → `PostToolUse` in **90ms** (the Claude analogue of the Codex
  flash).
- Claude's `Notification` hook fires **6s after** `PermissionRequest` for
  permission, and exactly **60s after** `Stop` when idle. It is a useful
  corroborator, not a low-latency trigger.

## Target architecture: evidence table + pure resolver

Sources stop commanding state. They record observations; a pure function resolves
them; a tick re-runs the resolver so evidence expires.

```text
Target:

  hook brackets (turn/tool open) ──> recordEvidence(level)   ┐
  approval edge ──────────────────> recordEvidence(edge)     │
  OSC 0 title heartbeat ──────────> recordEvidence(level)    ├──> sessionEvidence
  Notification hook ──────────────> recordEvidence(edge)     │      (per session)
  stop classifier ────────────────> recordEvidence(edge)     │
  process exit ───────────────────> recordEvidence(level)    ┘          │
                                                                        v
                          tick (1s) ──────────────> resolve(evidence, now) -> state
                                                                        │
                                                           dwellGate(state, policy)
                                                                        │
                                                                        v
                                                       applyState(cause: resolver)
```

`applyState` stays the single store door; the resolver becomes its only live-signal
caller. Cause-specific paths that are already ordered and authoritative (plugin
driver CAS, startup recovery, process exit) keep their existing routes.

**The spike inverted the original hierarchy.** This plan first had the heartbeat as
the primary level with hooks explaining *why* a turn stopped. The measurements say
the reverse: hook brackets are the level (they are the only signal that survives a
3.5s mid-tool title silence), and the heartbeat's job is to bound how long a
bracket may lie.

```text
resolve(evidence, now):
  if process exited                              -> idle (terminal)
  if heartbeat fresh (now - lastBusy <= TTL)     -> working   # recovers a LOST turn-open hook
  if approval edge open and not ReviewerInLoop   -> pending_approval
  if pendingCron                                 -> scheduled
  if turnOpen or toolOpen:                       # bracket says work is outstanding
      if heartbeat silent > staleAfter           -> settled   # closing hook LOST: un-stick
      else                                       -> working
  if backgroundWork                              -> working   (settled; Victor's call)
  if settle edge + classifier verdict            -> waiting_input | idle
  if no movement > stuckAfter                    -> unknown, reason=stuck
  otherwise                                      -> unknown
```

Two clauses do the real work and neither exists today: `heartbeat fresh` rescues a
lost turn-open hook, and `heartbeat silent > staleAfter` closes a bracket whose
closing hook never arrived. Measured together, they take the worst stuck window
from "the rest of the session" to under 4 seconds. Starting values: TTL 1.0s,
`staleAfter` 4.0s.

Screen scraping is **deleted for claude and codex**, not demoted to a fallback. It
scored 1.0% on a codex foreground tool and manufactures false `pending_approval`
from ordinary text; keeping it as a tiebreaker would reintroduce both. Agents with
no harness signals (copilot) keep it until they have some.

## Data model

```go
// internal/sessionstate (new package, pure, no daemon imports)

type Source string
const (
    SourceHarnessEvent Source = "harness_event"   // hooks, Notification
    SourceHeartbeat    Source = "heartbeat"       // OSC 0 title glyph
    SourceClassifier   Source = "classifier"      // stop-time LLM
    SourceBracket      Source = "hook_bracket"    // turn/tool open-close, PRIMARY level
    SourceScreen       Source = "screen"          // pty scrape — copilot only, deleted for claude/codex
    SourceProcess      Source = "process"         // exit / no pty
)

// One recorded observation. Level observations are refreshed continuously and
// expire; edge observations are one-shot facts that stay until superseded.
type Observation struct {
    Source     Source
    Claim      Claim      // busy | settled | approval_pending | needs_input | exited | ...
    Detail     string     // turn summary, notify message — diagnostics + sidebar label
    ObservedAt time.Time
}

// Everything the resolver may read for one session. Owned by the daemon,
// mutated only through record(); the resolver never touches the store.
type Evidence struct {
    Heartbeat        *Observation // level, TTL 1.5s claude / 0.5s codex (measured)
    LastHarnessEvent *Observation // edge
    LastClassifier   *Observation // edge
    TurnOpen         bool         // UserPromptSubmit seen, no Stop yet  (level)
    ToolOpen         bool         // PreToolUse seen, no PostToolUse yet  (level)
    Screen           *Observation // level; copilot only
    Process          *Observation // level, no TTL
    BackgroundWork   bool         // from Stop payload background_tasks
    PendingCron      bool         // from Stop payload session_crons
    ReviewerInLoop   bool         // codex auto_review / claude --permission-mode auto
    LastMovement     time.Time    // any evidence change; drives stuck detection
}

type Resolution struct {
    State  protocol.SessionState
    Reason string     // why this state — surfaced by `attn state explain`
    Unsettles bool    // derived attention projection
}

func Resolve(e Evidence, now time.Time) Resolution   // pure, table-tested
```

Resolution order (first match wins):

See the `resolve` pseudocode above for the ordered rules. The property that
matters: every clause that can hold a state green depends on evidence that either
refreshes (heartbeat, bracket) or expires, and the resolver re-runs on a tick — so
no state can outlive its evidence. That is the structural fix for the stuck class,
and the hook-drop table above is the measurement of it.

## Dwell policy

A transition *into* an attention-demanding state must hold before it is published.
Keyed on **who is being asked first**:

```go
func dwellFor(state protocol.SessionState, e Evidence) time.Duration {
    if state == protocol.SessionStatePendingApproval && e.ReviewerInLoop {
        return guardianDwell // 60s; cancelled by the next PreToolUse/PostToolUse
    }
    return 0                 // no reviewer: the user IS the reviewer, publish now
}
```

`ReviewerInLoop` is known at launch: `internal/agent/codex.go` passes
`approvals_reviewer="auto_review"`, `internal/agent/claude.go` passes
`--permission-mode auto`. It must be persisted with the session so the daemon can
read it at resolve time.

This removes the Codex guardian flash without delaying a genuine approval request
by a single millisecond.

## Attention projection (settled / unsettled) — opt-in layer

A **projection of the resolved state**, not a new state and not a second
resolver. It is gated behind a user-enabled working mode (off by default at
release); with the mode off, `Resolution.Unsettles` is computed but nothing in
the UI consumes it. Nothing in phases 0–3 may depend on the mode being on.

- **Unsettled**: `waiting_input`, post-dwell `pending_approval`, `unknown` with
  reason `stuck`, crashed/failed, and `idle` that has gone unread past
  `idleStaleAfter` (generalizes today's `needs_review_after_long_run`).
- **Settled**: `working` (including background-work and Monitor-armed turns),
  `scheduled`, `recoverable`, fresh `idle`.

## Boundaries

- `internal/pty` owns byte-level extraction only: the OSC 0 title is
  parsed where OSC 133 already is (`blockfeed.go` / `boundary.go`) and surfaced
  through the existing `s.onState` channel (`internal/pty/session.go:360`),
  widened from `string` to a typed observation. It must not know about
  `protocol.SessionState` or dwell.
- `internal/sessionstate` owns `Resolve` and `dwellFor`. Pure, no daemon or store
  imports, table-tested against recorded evidence traces.
- `internal/daemon` owns the evidence table, the tick, and the single call into
  `applyState`. It must not re-implement resolution rules inline.
- `internal/classifier` keeps its charter unchanged (LLM only, no keyword
  heuristics) and now answers a narrower question: given a settled turn, is it
  `idle` or `waiting_input`.

## Refactoring opportunities and tech debt

Two lists. The first are **enabling refactors**: doing them first makes the fix a
small diff instead of a large one, and each is behavior-preserving on its own. The
second is debt that the fix should clear on its way past, so we do not leave two
generations of state logic side by side.

### Enabling refactors (do before the behavior change)

1. **Widen the PTY→daemon state channel from a bare string.**
   `session.onState func(state string)` (`internal/pty/session.go:136`, wired at
   `internal/pty/manager.go:350`) is the only way anything the PTY sees reaches the
   daemon, and it carries a state name with no source, no observation time, and no
   reason. The evidence table needs all three. Widen it to a small
   `Observation{Source, Claim, Detail, At}` — mechanically, no behavior change —
   and every later phase becomes additive. Skipping this forces the heartbeat to
   masquerade as a `working` string, which is exactly the flattening that caused
   the current mess.

2. **Let the agent driver own its detectors instead of `manager.go` switching on
   the agent name.** The per-agent fact is encoded twice today: `HasStateDetector`
   / `HasApprovalResolver` booleans in `internal/agent/{claude,codex,copilot}.go`,
   plus a `switch agent { case "copilot": ... case "claude": ... }` in
   `internal/pty/manager.go:338-345`. Adding an OSC heartbeat detector means
   editing both again, and the two can silently disagree (a driver with the
   capability set but no case in the switch gets nothing, no error). Have the
   driver return its observers; delete the booleans and the switch.

3. **Collapse the two identical screen detectors.** `copilotStateDetector` and
   `claudeWorkingDetector` (`internal/pty/state_detector.go:42-60`) have the same
   three fields, duplicate the 2000-char tail trim, and duplicate the
   working-pulse debounce. One `screenDetector` with an injected classify function
   makes the claude path a single deletion later instead of a careful unpick of a
   560-line file.

4. **Move the Stop-hook state decision out of the CLI into the daemon.**
   `nonTerminalStopState` (`cmd/attn/main.go:114-139`) decides `working` /
   `scheduled` / classify inside the hook binary — and to do it, `runHookStop`
   makes a socket round-trip *to the daemon* to ask whether the session is chief of
   staff (`sessionIsChiefOfStaff`, `cmd/attn/main.go:3222`), then sends the answer
   back. The daemon already has every input. This one is load-bearing, not
   cosmetic: the resolver cannot see `BackgroundTasks` or `SessionCrons` as
   evidence because the CLI collapses them into a state string before they cross
   the socket. The hook should report the facts; the resolver decides.

5. **Split `classifySessionState` into a pure decision plus an IO shell.**
   `internal/daemon/daemon.go:2510-2640` is ~130 lines with six `applyState` exits
   interleaved with capability gates, todo counting, transcript path resolution,
   file IO, and a 30-second LLM call. It is already a resolver, written as control
   flow, so the decision can only be tested by standing up a daemon. Extracting
   the pure part is how Phase 2's `Resolve` absorbs it rather than sitting beside
   it as a seventh writer.

### Debt to clear while passing through

- **`ShouldApplyPTYState` is a distributed veto that exists only because there is
  no arbitration, and should be demolished, not ported.** Three drivers each
  implement a transition filter (`claude.go:584`, `codex.go:484`,
  `copilot.go:104`) whose comments say so outright — Claude's guards `scheduled`
  because the detector "would otherwise silently knock the session out"; Codex's
  allows exactly one transition and rejects all others. That is last-writer-wins
  being patched per agent at the point of impact. Once the resolver owns
  precedence, this interface has no reason to exist. Call it out explicitly so a
  future reader does not preserve it out of caution.
- **Codex's entire PTY path is one workaround that the heartbeat removes.** Codex
  has `HasStateDetector: false` but `HasApprovalResolver: true` — the only thing
  it scrapes for is the approval prompt leaving the screen, because no hook fires
  when a permission request is approved. The spinner resuming 0.05s later is a
  better signal from the same stream. Deleting `approval_resolver.go`'s 750ms
  absence debounce also deletes Codex's `ShouldApplyPTYState`, its capability
  flag, and its share of `state_detector.go`.
- **`defaultStateHeuristics.requestPhrases` matches ordinary English.** `"can
  you"`, `"could you"`, `"do you want"` (`internal/pty/state_detector.go:26-38`)
  fire on the user's own prompt text echoed in the pane — the measured 16.0%
  capture. Goes away with the claude scraper; worth naming so nobody tries to
  tune the list instead.
- **The todo fast-path is stale-evidence logic of the exact kind we are
  removing.** `pendingCount > 0 → waiting_input` (`daemon.go:2540-2549`) reads an
  untimestamped level (the todo list) at a settle edge and wins over everything
  after it. A session with a forgotten pending todo reads `waiting_input` forever.
  It should become one evidence row with a TTL, not a fast path.
- **`internal/pty` re-declares the state enum.** `stateWorking` / `stateIdle` /
  `statePendingApproval` / `stateWaitingInput` as local string consts
  (`state_detector.go:9-14`) shadow `protocol.State*`. Two vocabularies for one
  enum, and a typo in either is a silent no-op rather than a compile error. Use
  the protocol constants.
- **State handling has no home.** It is spread across `handlePTYState`,
  `classifySessionState`, startup recovery, and process exit inside a 3879-line
  `daemon.go`, plus the CLI hook paths in a 3480-line `main.go`. Phase 2's
  `internal/sessionstate` should be where the decision lives, with `daemon.go`
  keeping only the wiring.

### Found while implementing (not yet decided)

Logged as encountered so Victor can decide whether to clean them; nothing here is
being fixed as part of the current step.

- **The same state stream is deduped twice, with different rules.**
  `internal/ptyworker/runtime.go:191` drops an observation whose claim equals the
  previous one (`changed := previousState != state`), then
  `shouldForwardStateLocked` (`internal/ptybackend/worker.go:166`) dedupes again
  with a `working`-pulse exemption the first layer does not have. Two suppressors
  in series over one stream, neither aware of the other.
- **The working keepalive pulse is a dead letter on the default backend.** The
  outer suppressor wins: `Runtime.run` broadcasts `stateChangedEvent` only
  `if changed`, so the detector's 1.2s `workingPulseInterval` re-emit never
  crosses the worker→daemon wire, and the backend's own 2s
  `shouldForwardStateLocked` window only runs on the info-poll path, which is
  gated behind `if !legacyLifecycle { continue }`. Confirmed live on profile
  `statedet` (2026-07-25): eleven continuous seconds of claude working produced
  exactly one `state=working` line in `daemon.log`. This matters beyond tidiness —
  the pulse is the liveness signal the resolver's TTL design depends on, so the
  resolver phase must either un-gate it or grow its own heartbeat.
- **The copilot screen heuristics look stale against copilot 1.0.73.**
  `classifyCopilotScreen` keys off `defaultStateHeuristics` — prompt markers
  ` › ` / ` > ` / `❯ ` and status markers "context left" / "for shortcuts". The
  current copilot CLI renders none of them (its composer is a `┃`-bordered box),
  and a live copilot session on `statedet` produced no `source=screen` claim at
  all. Not confirmed against a working turn — this org's policy blocks copilot
  turns on that machine — so the finding is "the markers are absent from the
  rendered frames", not "the detector is proven dead". Worth a deliberate check
  before the resolver starts weighing copilot screen evidence.
- **That dedup is why `approvalResolver` re-emits `pending_approval` it did not
  observe.** Its own type comment (`internal/pty/approval_resolver.go:13-20`)
  explains it: the onset hook bypasses the worker, so without a redundant
  re-emission the later `working` clear "would look unchanged to the worker and
  never be forwarded". A source-blind dedup is being worked around by fabricating
  an observation. Once observations carry a source, dedup should be per-source or
  gone — and the fabricated re-emit deleted with it.
- **The worker fabricates `working` when it does not know its state, and it does
  so for every session.** `internal/ptyworker/runtime.go` defaults an empty cached
  state to `"working"` when a new watcher subscribes. Confirmed live on profile
  `statedet` (2026-07-25): the *first* observation the daemon logs for a fresh
  session is `state=working source=worker_info detail="watch subscribe replay"` —
  including for `agent=shell`, which has no detector at all and therefore cannot
  have been observed working. Inventing green out of ignorance is precisely the
  stuck-green failure class; it should report `unknown` or send nothing.
  Cosmetically invisible today because a spawn is green anyway, but it means every
  session's evidence table would open with a false row.
- **`TestDaemon_StopCommand_PendingTodos_SetsWaitingInput` races the reaper.**
  It waits for async classification with a bare `time.Sleep(200ms)`
  (`internal/daemon/daemon_test.go:4837`) on a session that has no PTY, so under
  the sharded suite's load the recovery/reaping loop can demote it to
  `recoverable` first and the assertion reads that instead of `waiting_input`.
  Seen once in a 5-shard `make test` run, not reproducible standalone
  (`-count 3` green). Pre-existing and orthogonal to observations — but it is the
  same underlying design problem as the product bug: two writers with no
  arbitration, resolved by whoever gets there first.
- **`handlePTYState` discards the observation time.** The store timestamps the
  transition with its own clock, so an observation delayed in the worker→daemon
  hop is recorded as if it just happened. Harmless today; it stops being harmless
  once TTLs are measured against `At`.

## Implementation steps

### Phase 0 — instrumentation (ship first, no behavior change)

- [x] Per-session ring buffer of state observations in `internal/statetrace`
      (`Source, Claim, Detail, Cause, Outcome, Reason, ObservedAt, RecordedAt`),
      capped at 256, in memory, dropped with the session record. Every recorded
      observation is also mirrored to `daemon.log` as a `state trace:` line, so a
      session already gone still leaves its trace in the log.
- [x] `attn state explain <session>` (`--json`) — replays the ring over the new
      `state_explain` command (protocol 190). Makes "sometimes it gets stuck"
      falsifiable.
- [x] Record from every existing source *without* changing arbitration. The
      recording is deliberately wider than the applied transitions: `Outcome`
      separates `applied` from `vetoed` (rejected before `applyState` — a driver's
      `ShouldApplyPTYState`, a plugin-owned session, an unknown session),
      `discarded` (the store's own commit rule refused it), and `skipped` (the
      source looked and had nothing to claim). A stuck color is precisely a
      session with no applied observations, so the rejected ones are the whole
      point. `sessionStateChange` grew an optional `origin{source, detail,
      observedAt}` so several sources sharing one cause (every trusted PTY
      observation is a `liveSignal`) stay distinguishable.
- [x] Enabling refactor 1: widen `session.onState` to carry
      `Observation{Source, Claim, Detail, At}`. Behavior-preserving; every later
      phase is additive on top of it. Landed as `internal/pty/observation.go` plus
      additive `state_source`/`state_detail`/`state_observed_at` fields on the
      worker RPC's `EventStateChanged` (no RPC bump — an older worker omits them
      and the daemon reads `SourceUnknown` observed on arrival). `handlePTYState`
      now logs source/detail/observed-at. Live-verified on profile `statedet` with
      a real Claude session (`real-app:scenario-tr402-local-claude`): states still
      flow and now arrive attributed.

### Phase 1 — wire the harness signals

- [x] **Phase 1a (PR #669).** Parse OSC 0/2 in the worker; classify the glyph
      prefix (braille block = busy, `✳`/bare = not busy) per agent; expose title
      text as `Detail`. Read-only `oscScanner` in `internal/pty/oscscan.go` — the
      OSC 133 segmenter could not be reused because it strips markers from the
      stream and is corpus-locked to a frontend parity fixture. Per-agent behavior
      is a driver capability (`Capabilities.HarnessSignals`), matching the
      `ScreenDetector` kind pattern. An unchanged level re-emits at most once a
      second, and the phase 0 ring collapses consecutive identical observations
      into a repeat count, so a long turn cannot flush the ring.
- [x] ~~Parse OSC 777 `notify`~~ — **dropped**: claude does not emit it (see
      "The unused signals" above). Built, found unwitnessable on a live PTY, and
      deleted rather than shipped as untested-in-production code.
- [x] **Phase 1b (PR #670).** Claude `Notification` hook registered in
      `internal/hooks/hooks.go`, routed to a new `_hook-notification` command.
      It carries `notification_type` — `permission_prompt` when the agent is
      blocked on approval, `idle_prompt` after 60s of waiting — which the plan
      did not know about; the typed field is the claim and the message is only
      the detail, so nothing parses an English sentence. Recorded as evidence
      (`hook_notify`), never applied: at ~6s of latency the session may already
      have moved on.
- [x] ~~Persist `ReviewerInLoop` on the session at spawn.~~ **Changed**: the
      resolved permission mode rides along on the state hook
      (`StateMessage.permission_mode`) and is recorded as a `reviewer` level.
      Spawn flags are not authoritative — a user's global agent settings can put
      a guardian in the loop for a session attn launched without asking for one,
      and the mode changes mid-session on Shift+Tab. Claude reports the field on
      every post-prompt hook; Codex reports none, and an absent mode records
      nothing rather than a fake "unknown" claim. Phase 2 derives
      `Evidence.ReviewerInLoop` from this level plus the codex spawn flag.
- [x] Enabling refactors 2 and 3: driver-named observers (`Capabilities.
      ScreenDetector` kind replaces `HasStateDetector` plus the agent-name switch
      in `manager.go`), and one `screenDetector` + `screenPolicy` in place of the
      two near-identical structs. The per-agent *meaning* stays in
      `classifyClaudeScreen` / `classifyCopilotScreen`; only the mechanics (tail,
      claim suppression, working pulse) are shared. `STATE_DETECTOR=0` still
      disables; `=1` keeps the driver's kind, which is what it already did in every
      reachable case.
- [x] Enabling refactor 4: the Stop hook reports `background_task_statuses` /
      `pending_session_crons` as facts on `StopMessage` (protocol 189);
      `nonTerminalStopState` moved to `internal/daemon/stop_terminality.go` and the
      chief-of-staff lookup is now an in-process `d.isChiefOfStaffSession` call
      instead of a socket round-trip from the hook back to the daemon.
- [x] Feed the OSC signals into the Phase 0 ring only; still no arbitration
      change (phase 1a). `pty.Source.EvidenceOnly()` routes them past
      `applyState` entirely — their claims are "busy"/"not_busy", not protocol
      state names — and past the worker/backend state dedup, which would
      otherwise swallow a repeated level.
- [ ] Feed the hook signals into the ring the same way (phase 1b), then compare
      traces against the baseline.

### Phase 2 — resolver

- [ ] New `internal/sessionstate` with `Evidence`, `Resolve`, table tests built
      from Phase 1 traces.
- [x] Enabling refactor 5 (first half): `classifySessionState`'s rules extracted
      to `internal/daemon/classify_decision.go` as three pure functions
      (`classifyPreTranscript`, `classifyPostTranscript`, `classifyVerdict`)
      returning a `classifyDecision`; the shell performs the IO between them and
      owns the single store write. Six `applyState` exits collapsed to one. Folding
      these rules into `Resolve` itself is Phase 2 work — this makes them movable.
- [ ] Daemon evidence table + 1s tick; route live signals through the resolver
      into `applyState` with a new `resolverObservation` cause.
- [ ] Gate `internal/pty/state_detector.go` to copilot only; delete the keyword
      lists and `approval_resolver.go`'s screen-absence debounce for claude/codex.
- [ ] Delete `ShouldApplyPTYState` and all three driver implementations — the
      resolver now owns precedence. Verify each vetoed transition the comments
      describe is covered by a `Resolve` table test first (Claude's `scheduled`
      park, Codex's approval-clear, Copilot's two-state whitelist).
- [ ] Replace `internal/pty`'s shadow state consts with `protocol.State*`.

### Phase 3 — policy

- [ ] Dwell gate + `guardianDwell`.
- [ ] Stuck detection (`stuckAfter`, reason surfaced in the UI).
- [ ] `idleStaleAfter` staleness marking, folding in
      `needs_review_after_long_run`. Marked in phase 3; only *consumed* as an
      unsettle trigger by phase 4.

Phases 0–3 are shippable on their own and are judged on one question: are the
colors right?

### Phase 4 — attention mode (opt-in working mode)

**Not planned yet — deliberately.** Victor's call, 2026-07-25: phases 0–3 are the
groundwork and are judged on colors alone, and phase 4 needs his full vision
written down before it is worth designing. The only constraint phases 0–3 must
respect: the projection is derived from `Resolve`'s output, so nothing below phase
4 may introduce a second notion of "needs attention". Sketch of what exists today
so far (`Resolution.Unsettles` on the wire, a setting, sidebar grouping) is
placeholder only; do not implement against it.

## Decisions

- **A yielded turn with background work stays `working`/green** rather than
  getting its own state. Victor's call, 2026-07-25: it keeps the state set and the
  protocol unchanged, and under the attention projection it is settled either way,
  so the extra state would buy only a color distinction. Revisit if a long
  `attn wait-pr` reads as misleadingly active in practice.
- **Done is settled, but goes stale.** An unread idle result unsettles past
  `idleStaleAfter` so work Victor drove is never silently forgotten. Strict
  "always settled" was rejected for that reason.
- **Attention mode is a projection behind a user setting, not a state.** Victor's
  call, 2026-07-25: it ships as an optional working mode on top of correct colors,
  off by default. Keeping it a pure projection of `Resolve` means the two can never
  disagree, and phases 0–3 deliver value with the mode permanently off.
- **Instrumentation ships before any behavior change.** The failure modes are
  intermittent; without a replayable per-session trace there is no way to prove a
  stuck case is gone rather than merely rarer.
- **The dwell is keyed on the reviewer, not on a global timer.** Delaying all
  `pending_approval` transitions would slow real approval requests; gating only on
  `ReviewerInLoop` targets exactly the guardian case.
- **The heartbeat TTL is per-agent, not global.** Measured frame intervals differ
  by 10x (claude 0.97s, codex 0.102s). A single global TTL either flickers on
  claude or settles needlessly late on codex.
- **The heartbeat, not the screen, clears an approval.** The spinner resumes 0.05s
  after an approval is granted. `approvalResolver`'s screen-absence debounce is
  replaced by "approval edge outstanding AND heartbeat went busy again", which is
  the stuck-yellow fix.
- **Hook brackets are the level; the heartbeat bounds how long a bracket may
  lie.** Reversed after the spike. The TTL sweep falsified heartbeat-as-detector:
  no single TTL gives both a fast settle and no false-settle, because claude's
  title goes silent 3.5s mid-tool. `turnOpen`/`toolOpen` carry "work is
  outstanding"; the heartbeat's job is `staleAfter` — a bracket open with a silent
  heartbeat means a closing hook was lost, so settle anyway.
- **The heartbeat is still authoritative for *whether* a turn runs, never for
  *why* it stopped.** A fresh heartbeat outranks everything but process exit
  (it recovers a lost `UserPromptSubmit`); the settle-reason rules only run when
  it is quiet.
- **The heartbeat buys stuck-bounding, not accuracy.** With hooks perfect it
  *costs* 1.5pp. At 5% hook loss it cuts the worst stuck streak from 60.0s to
  3.8s. That trade is the whole point — the reported bug is stuck colors, not
  average error.
- **Approvals depend on hooks, with no fallback.** Victor's call, 2026-07-25. The
  heartbeat structurally cannot corroborate an approval (the agent genuinely is
  not running), which is why the hybrid's residual 27.6s/43.4s worst streaks are
  all approval windows. Rather than build a fallback that would have to guess, we
  accept `PermissionRequest` + `Notification` as the approval source of truth. A
  dropped approval hook is bounded by the settle rules, not by a scraper.
- **Screen scraping is deleted for claude and codex, not demoted to a fallback.**
  It scores 1.0% on a 35s foreground tool and invents `pending_approval` from
  Victor's own prose. A fallback that wrong would poison the evidence table
  whenever it was consulted. Copilot keeps it until it has harness signals.

## Open questions

- ~~Does Claude emit OSC 777 for permission prompts, or only for turn settle?~~
  Answered while implementing phase 1a: it emits no OSC 777 at all. The
  `Notification` hook is the only settle event, at ~6s.
- Codex has no notification OSC. Its `notify` program config
  (`agent-turn-complete`) is the analogue; confirm it fires for approval requests
  too, or accept hooks-only for Codex settle detection.
- Heartbeat TTL was measured on an idle machine (claude 0.97s frame interval,
  codex 0.102s). A busy PTY under backpressure may batch chunks and stretch the
  gap. The per-agent TTLs above carry ~55% and ~5x margin respectively; confirm
  under a loaded matrix run before locking them in.

## Follow-ups

- Delete the three spike files once Phase 0 instrumentation replaces them as the
  way to get traces: `internal/pty/spike_state_replay_test.go`,
  `internal/pty/spike_resolver_test.go`,
  `internal/hooks/spike_codex_overrides_test.go`. They are committed so the
  measurements above stay reproducible, and all three skip unless their env var is
  set. `spike_codex_overrides_test.go` is the one worth keeping longer: it reaches
  into the production trusted-hash computation to wire arbitrary codex hooks, which
  is how any future codex hook test gets ground truth.

- Use the OSC 0 turn summary as a live sidebar label.
- Copilot CLI is out of scope here; it keeps the screen-scrape path until it has
  its own harness signals.
