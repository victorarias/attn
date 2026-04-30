---
name: Hook rework — lean on native Notification + self-driving ledger
description: Replace attn's 2024-era custom state inference with Claude Code's Notification hook, add a per-session ledger for ScheduleWakeup/CronCreate/Monitor so self-driving work classifies as `working`, and drop TodoWrite tracking entirely.
type: plan
status: Proposed
---

# Hook rework — lean on native Notification + self-driving ledger

**Status**: Proposed
**Owner**: Victor
**Date**: 2026-04-27

## Why

attn's current hook setup predates several Claude Code features that we should now lean on:

1. **`Notification` hook with matcher types** (`idle_prompt`, `permission_prompt`, `elicitation_dialog`, `auth_success`) — a deterministic "needs human" signal we currently ignore. We instead infer the same state from `PreToolUse[AskUserQuestion]` + `PermissionRequest` + a stop-time LLM classifier. The classifier still earns its keep for ambiguous Stop events, but the upfront hooks are largely replaceable.
2. **Self-driving primitives** — `ScheduleWakeup`, `CronCreate`, `Monitor`, the `/loop` skill. When the agent stops with one of these pending, our classifier mislabels the session as `idle` or `waiting_input`. The user sees yellow ("act on this") when the agent will resume on its own.
3. **`PostToolBatch`** — fires once per parallel tool batch. Replaces our `PostToolUse[*]` chatter that fires on every Read/Bash.
4. **Hooks we never registered**: `SessionEnd` (clean shutdown reason), `StopFailure` (rate-limit/billing/auth errors), `SubagentStart`/`SubagentStop` (avoid misclassifying parents blocked on subagents), `stop_hook_active` re-entry guard.

The TodoWrite hook + UI is also slated for removal — todos can be rebuilt later if needed.

## Current state

`internal/hooks/hooks.go` registers, per session:

| Event | Matcher | Action |
|---|---|---|
| `Stop` | `*` | snapshot transcript, run `internal/classifier` (LLM call → WAITING/DONE) |
| `UserPromptSubmit` | `*` | mark `working` |
| `PreToolUse` | `AskUserQuestion` | mark `waiting_input` |
| `PermissionRequest` | `*` | mark `pending_approval` |
| `PostToolUse` | `TodoWrite` | record todos |
| `PostToolUse` | `AskUserQuestion` | mark `working` |
| `PostToolUse` | `*` | mark `working` |

Custom logic backing the above: `internal/classifier` (Claude/Codex SDK call), `internal/transcript` (JSONL parser), todo store fields, frontend todo UI.

## Target state (provisional — pending Step 0 verification)

The hook inventory below is drawn from a docs-research pass that almost certainly over-reports what actually exists. Treat this as a sketch; **Step 0 is the source of truth** for what we wire in Steps 3–5.

Confidence labels:
- 🟢 **confirmed** — we already use it in production today, or it's documented and well-known.
- 🟡 **likely** — listed in docs we reviewed; needs empirical confirmation.
- 🔴 **unverified** — research-agent claim, plausible but never observed.

```
🟢 Stop                                  → classifier (fallback when ledger empty + no recent Notification)
🟡 SessionEnd                            → cleanup with reason
🟢 SessionStart                          → reset ledger, mark launching/working
🔴 StopFailure                           → surface API error state
🟢 UserPromptSubmit                      → mark working, decrement self-driving ledger if turn was wakeup-triggered
🔴 PostToolBatch                         → mark working
🟡 Notification[idle_prompt]             → mark waiting_input
🔴 Notification[elicitation_dialog]      → mark waiting_input
🟡 Notification[permission_prompt]       → mark pending_approval
🟢 PostToolUse[ScheduleWakeup]           → ledger++ (record wake_at)
🟢 PostToolUse[CronCreate]               → ledger++ (record cron expr, filter out cloud routines if distinguishable)
🟢 PostToolUse[Monitor]                  → ledger++ (record bash_id)
🟢 PostToolUse[CronDelete]               → ledger-- (by id)
🟡 PostToolUse[TaskStop]                 → ledger-- (by id)
🟡 SubagentStart / SubagentStop          → guard: keep parent in working while child runs
```

Open shape questions for Notification specifically: is the discriminator a **matcher type** (`matcher: "idle_prompt"`), a **payload field** (`payload.notification_type`, `payload.type`, or `payload.message`), or only a freeform message string? Step 0 must answer this — the settings.json wiring in Step 3 changes substantially depending on the answer.

Per-session ledger lives in `internal/store` as a structured field. At Stop time:

- ledger non-empty AND not stale → state = `working` (no LLM classifier call).
- else → run classifier as today.

The classifier shrinks to a fallback for "stopped, no Notification fired, no ledger entries" cases.

## Open questions Step 0 must answer

1. **Hook existence** — which of the 🟡/🔴 events above actually fire on the current Claude Code build? Especially `PostToolBatch`, `StopFailure`, `SessionEnd` reason codes. Step 0 enumerates *every event observed*, not just ones from a hand-written list.
2. **Notification discriminator** — is it `matcher: "idle_prompt"` (matcher-based), or a payload field (`type`, `notification_type`, `message`), or only message-string parsing? The harness must capture full payloads so the answer falls out.
3. Does `CronCreate` fire from the local `/loop` flow only, or also from `/schedule` cloud routines? If both, can `tool_input` distinguish them?
4. Does `Notification` fire when the agent stops with a `ScheduleWakeup` pending? With which discriminator value?
5. Canonical schemas for `tool_input`/`tool_response` on `ScheduleWakeup`, `CronCreate`, `Monitor`, `CronDelete`, `TaskStop`.
6. Stop hook: confirm `stop_hook_active` field name and value semantics.
7. Does Claude Code emit any hook on resume-from-wakeup that would let us decrement the ledger automatically (vs. inferring from `UserPromptSubmit` after wake_at)?

---

## Step 0 — Hook payload capture harness (agent-runnable)

**Goal**: empirically resolve the open questions above and produce a canonical reference of Claude Code's hook payloads as they actually fire today.

**Runner**: an agent (Claude Code, headless) executes `tools/hook-capture/run.sh`. The script is self-contained and produces `tools/hook-capture/report.md`.

### Layout

```
tools/hook-capture/
  run.sh                       # entry point
  hook-logger.sh               # writes one JSONL line per hook invocation
  settings.template.json       # hook config; rendered into the sandbox
  scenarios/
    01-trivial.txt             # one-line prompt files; one per scenario
    02-single-bash.txt
    03-parallel-bash.txt
    04-ask-user-question.txt
    05-permission-needed.txt
    06-schedule-wakeup.txt
    07-cron-create-loop.txt
    08-cron-create-routine.txt
    09-monitor-bg-bash.txt
    10-subagent-explore.txt
    11-todo-write.txt
    12-stop-failure-rate-limit.txt
  flags.json                   # per-scenario `claude` CLI flag overrides
  expected.json                # per-scenario must/may events (stdlib JSON, no PyYAML)
  build-report.py              # parses logs + expected.json; writes report.md
  README.md                    # agent invocation contract
  report.md                    # output (generated; not committed by default)
```

### Sandbox

The runner isolates Claude Code from the user's real config:

```bash
SANDBOX=$(mktemp -d -t attn-hook-capture.XXXXXX)
export CLAUDE_CONFIG_DIR="$SANDBOX/.claude"
mkdir -p "$CLAUDE_CONFIG_DIR"

# Render settings.template.json with absolute path to hook-logger.sh
sed "s|__LOGGER__|$PWD/tools/hook-capture/hook-logger.sh|g" \
  tools/hook-capture/settings.template.json \
  > "$CLAUDE_CONFIG_DIR/settings.json"
```

Each scenario sets its own `HOOK_CAPTURE_LOG` so payloads are attributable.

### `hook-logger.sh`

```bash
#!/usr/bin/env bash
# Usage: hook-logger.sh <event_name>
# Reads hook payload JSON on stdin; appends one JSONL line to $HOOK_CAPTURE_LOG.
set -euo pipefail
LOG="${HOOK_CAPTURE_LOG:?must set HOOK_CAPTURE_LOG}"
EVENT="${1:?missing event name}"
TS=$(date +%s%N | cut -c1-13)
PAYLOAD=$(cat)
jq -cn --arg ts "$TS" --arg event "$EVENT" --argjson payload "$PAYLOAD" \
  '{ts: ($ts|tonumber), event: $event, payload: $payload}' >> "$LOG"
exit 0
```

Logger must always exit 0 — we never want the harness to block Claude Code's flow.

### `settings.template.json`

Subscribes to every hook event we care to inspect, with `*` matchers where supported, so the harness discovers payloads even for events not yet in our rework:

```json
{
  "hooks": {
    "Stop":            [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ Stop"}]}],
    "StopFailure":     [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ StopFailure"}]}],
    "SessionStart":    [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ SessionStart"}]}],
    "SessionEnd":      [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ SessionEnd"}]}],
    "UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ UserPromptSubmit"}]}],
    "PreToolUse":      [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PreToolUse"}]}],
    "PostToolUse":     [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PostToolUse"}]}],
    "PostToolBatch":   [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PostToolBatch"}]}],
    "PermissionRequest":[{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PermissionRequest"}]}],
    "Notification":    [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ Notification"}]}],
    "SubagentStart":   [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ SubagentStart"}]}],
    "SubagentStop":    [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ SubagentStop"}]}],
    "PreCompact":      [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PreCompact"}]}],
    "PostCompact":     [{"matcher":"*","hooks":[{"type":"command","command":"__LOGGER__ PostCompact"}]}]
  }
}
```

### Scenarios

Each scenario is one `claude` invocation. Headless options:

```bash
claude -p \
  --output-format stream-json \
  --permission-mode bypassPermissions \
  --max-turns 6 \
  < scenarios/NN-name.txt \
  > "$SANDBOX/scenario-NN.stdout.jsonl" \
  2> "$SANDBOX/scenario-NN.stderr"
```

`--permission-mode bypassPermissions` lets the agent execute tools without surfacing dialogs in scenarios where we're not specifically testing permission flow. For S5 (permission needed), explicitly run with `default` permission mode and an excluded tool to provoke `PermissionRequest`/`Notification[permission_prompt]`.

| # | Name | Prompt (abridged) | What we expect to fire |
|---|---|---|---|
| 01 | trivial | "Reply with the single word 'hello' and stop." | Stop, UserPromptSubmit, SessionStart, SessionEnd |
| 02 | single-bash | "Run `echo hello` via Bash, then stop." | + PreToolUse, PostToolUse, PostToolBatch (?) |
| 03 | parallel-bash | "In a single response, run `echo a`, `echo b`, `echo c` in parallel via Bash." | + PostToolBatch (single fire vs. one per tool — answers Q3) |
| 04 | ask-user-question | "Use the AskUserQuestion tool to ask whether to proceed. After asking, stop." | + Notification[idle_prompt] (or PreToolUse[AskUserQuestion]) |
| 05 | permission-needed | "Run `git status` via Bash." (run with `--permission-mode default` and `--disallowedTools Bash`) | + PermissionRequest, Notification[permission_prompt] |
| 06 | schedule-wakeup | "Call the ScheduleWakeup tool with `delaySeconds: 60`, `prompt: \"resume\"`, `reason: \"test\"`." | + PostToolUse[ScheduleWakeup] payload — answers Q4. Inspect Stop payload for self-wait indicators (Q2). |
| 07 | cron-create-loop | "Use the `/loop` skill with interval `5m` and prompt `noop`." | + PostToolUse[CronCreate]; tool_input should reveal local-loop flavor (Q1) |
| 08 | cron-create-routine | "Use the `/schedule` skill to create a routine running daily at 9am with prompt `noop`." | If CronCreate fires, compare tool_input vs. S07 (Q1). If a different tool fires, capture its name. |
| 09 | monitor-bg-bash | "Run `for i in 1 2 3; do echo $i; sleep 1; done` in the background, then use the Monitor tool to watch its stdout for 5 lines." | + PostToolUse[Monitor] payload (Q4) |
| 10 | subagent-explore | "Use the Agent tool with subagent_type=Explore to find any markdown file in this directory. Report under 50 words." | + SubagentStart, SubagentStop |
| 11 | todo-write | "Add three todos via TodoWrite: A, B, C." | + PostToolUse[TodoWrite] payload (sanity for the removal) |
| 12 | stop-failure-rate-limit | "Reply with one word." Run with `--model nonexistent-model-id` to provoke an error. | + StopFailure |

Scenarios that depend on Claude Code refusing to invoke a tool from a direct-instruction prompt (notably 06–09) may need fallback wording — if the agent declines, the scenario script logs "tool not invoked" rather than failing the harness.

### `expected.json`

```json
[
  { "scenario": "01-trivial",
    "must": ["UserPromptSubmit", "Stop"],
    "may":  ["SessionStart", "SessionEnd"],
    "note": "Establishes baseline lifecycle hooks." },
  { "scenario": "03-parallel-bash",
    "must": ["PreToolUse", "PostToolUse", "Stop"],
    "may":  ["PostToolBatch"],
    "note": "If PostToolBatch fires once for the parallel batch, it's a real event." }
]
```

JSON instead of YAML so `build-report.py` only needs the stdlib (no PyYAML).

### `build-report.py`

Reads every `scenario-*.jsonl` log + `expected.json`. Produces `report.md` with:

1. **Hooks observed** — every distinct `event` value seen across all scenarios. This is the authoritative inventory; the rework reads from this list, not from the docs-research list.
2. **Hooks NOT confirmed** — every event the harness *subscribed to* that never fired across any scenario. This is the hallucination signal: events on this list are likely not real on the user's Claude Code version.
3. **Coverage table** — per scenario, expected vs. observed. `must:` events missing flagged red; `may:` events missing noted but not flagged.
4. **Payload schemas** — for each unique `(event, tool_name)` pair seen, jq-extract a sample payload + a synthesized field summary.
5. **Notification discriminator** — analyze every Notification payload across scenarios; report whether the matcher field, a payload field (`type`/`notification_type`/`message`), or message-string parsing distinguishes the cases.
6. **Answers to Q1–Q7** — each question gets a short verdict + the supporting log line. If a question can't be answered from the captured data, mark it "unresolved — needs interactive run" rather than guessing.
7. **Surprises** — any hook event we didn't subscribe to but saw mentioned in stderr / stream-json — flagged as "subscribe in next iteration."

Report is committed back as `tools/hook-capture/report.md` and referenced by Steps 1–5.

### Agent invocation contract

The agent is told:

> Run `bash tools/hook-capture/run.sh`. When it exits, read `tools/hook-capture/report.md`. If the **Open questions** section has any "unresolved" entries, investigate (re-run a specific scenario with adjusted prompts, or note that the user's Claude Code version doesn't support a feature). Return a short summary of: hooks confirmed available, hooks unexpectedly missing, payload surprises that affect the rework plan.

The agent does **not** modify rework code in this step — Step 0 is observation only.

### What Step 0 does not test

- Actual wakeup firing (would require a long-running `claude` session — out of scope here).
- Resume-from-wakeup semantics (Q6 may end "unknown — needs interactive run"; the rework can ship with a time-based ledger expiry as fallback).
- Multi-turn classifier behavior — covered by existing `internal/classifier/classifier_test.go`.

---

## Step 0 findings (empirical + docs, 2026-04-30)

Headless harness ran 12 scenarios. Interactive verification done by driving `claude` inside a tmux session and `tmux send-keys`/`capture-pane` to interact with permission prompts and AskUserQuestion. Cross-checked against the official inventory at https://code.claude.com/docs/en/hooks (the authoritative source — supersedes our empirical captures).

### Authoritative event inventory (per official docs)

```
SessionStart, Setup, UserPromptSubmit, UserPromptExpansion,
PreToolUse, PermissionRequest, PermissionDenied, PostToolUse,
PostToolUseFailure, PostToolBatch, Notification,
SubagentStart, SubagentStop, TaskCreated, TaskCompleted,
Stop, StopFailure, TeammateIdle, InstructionsLoaded,
ConfigChange, CwdChanged, FileChanged, WorktreeCreate,
WorktreeRemove, PreCompact, PostCompact, Elicitation,
ElicitationResult, SessionEnd
```

29 events total. Earlier "likely hallucinations" classification (TaskCreated/TaskCompleted/CwdChanged/FileChanged/etc.) was wrong — they are real, just not exercised by our scenarios.

### Hooks observed empirically (12)

Headless harness: `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolBatch`, `Stop`, `StopFailure`, `SubagentStart`, `SubagentStop` (10).

Interactive tmux verification adds: `PermissionRequest`, `Notification` (2).

### Hooks documented but unobserved (17)

`Setup`, `UserPromptExpansion`, `PermissionDenied`, `PostToolUseFailure`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `PreCompact`, `PostCompact`, `Elicitation`, `ElicitationResult`. Most need specific triggers (worktrees, MCP elicitation, compaction, multi-agent teammates). Out of rework scope; subscribe lazily as needs arise.

### Notification — schema confirmed

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "...",
  "hook_event_name": "Notification",
  "message": "<varies>",
  "notification_type": "permission_prompt | idle_prompt"
}
```

`notification_type` IS the payload-field discriminator the plan asked about (Q2). Three variants observed:

| `notification_type` | `message` | Trigger | Maps to |
|---|---|---|---|
| `permission_prompt` | `"Claude needs your permission to use <Tool>"` | Tool requires user approval | `pending_approval` |
| `permission_prompt` | `"Claude Code needs your attention"` | `AskUserQuestion` invoked | `waiting_input` |
| `idle_prompt` | `"Claude is waiting for your input"` | ~60s idle after `Stop` | already-classified state; informational only |

Within `permission_prompt`, `notification_type` alone is **not** sufficient — disambiguate via `message` text or the preceding `PermissionRequest.tool_name` (`AskUserQuestion` → question, anything else → permission).

`Notification[elicitation_dialog]` was never observed; needs an MCP server with elicitation capability. Plausible but unverified.

### PermissionRequest — schema confirmed

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "...",
  "permission_mode": "default",
  "hook_event_name": "PermissionRequest",
  "tool_name": "Write",
  "tool_input": {...},
  "permission_suggestions": [{"type": "setMode", "mode": "acceptEdits", "destination": "session"}]
}
```

**`PermissionRequest` fires for `AskUserQuestion` too**, not just for tools requiring user approval. Current attn wiring (`PermissionRequest[*] → pending_approval`) will incorrectly classify question turns. Step 3 must filter on `tool_name`.

### Event ordering (interactive)

**Permission prompt — accepted:**
```
PreToolUse[Tool] → PermissionRequest[Tool] → Notification(permission_prompt, "needs permission to use Tool")
  → (user picks Yes) → PostToolUse[Tool] → PostToolBatch → Stop
```

**Permission prompt — denied:**
```
PreToolUse[Tool] → PermissionRequest[Tool] → Notification(permission_prompt, "needs permission to use Tool")
  → (user picks No) → ⛔ NO further hook events fire ⛔
```

**Critical for Step 4**: when the user manually denies via the UI prompt, the session goes silent — no `Stop`, no `PostToolUse`, no `PermissionDenied`. Verified across 29 subscribed hook names. Claude Code's `--debug hooks` log explicitly shows `[DEBUG] <Tool> tool permission denied` — it knows internally but emits no hook event for user-driven UI denial.

(`PermissionDenied` IS a real hook per docs, but it fires for **auto-mode classifier** denials — a separate path where Claude Code's classifier auto-denies a tool call. Carries `retry: true` decision control. Distinct from user-driven UI denial.)

**Both denial paths converge on the same attn state:** the session is stalled, the user must act. attn stays in `pending_approval` (flashing) until the next clearing signal — no special timeout needed. Clearing signals:

- `UserPromptSubmit` — user typed a new prompt
- `Stop` — model produced output (only happens if user accepted via retry, etc.)
- `SessionEnd` — session terminated
- `Notification[idle_prompt]` — fires ~60s after silent halt, equivalent "needs attention" state, can be no-op or upgrade to `waiting_input` (same UI bucket)

**AskUserQuestion:**
```
PreToolUse[AskUserQuestion] → PermissionRequest[AskUserQuestion] → Notification(permission_prompt, "needs your attention")
  → (user answers) → PostToolUse[AskUserQuestion] → PostToolBatch → Stop
```

### Stop payload (already-known but reconfirmed)

```json
{ "hook_event_name": "Stop", "stop_hook_active": false, "last_assistant_message": "..." }
```

`stop_hook_active` and `last_assistant_message` ship natively — Step 4's transcript-watcher fallback can be much simpler than today's classifier-side parser.

### StopFailure payload

```json
{ "hook_event_name": "StopFailure", "error": "invalid_request", "last_assistant_message": "..." }
```

Confirmed via scenario 12 (nonexistent model). Step 4 wires this into a `failed` / `unknown` state.

### Hooks NOT real (drop from rework)

Subscribed but never fired across diverse triggers: `ConfigChange`, `CwdChanged`, `FileChanged`, `InstructionsLoaded`, `TaskCompleted`, `TaskCreated`, `TeammateIdle`, `UserPromptExpansion`. Treat as research-agent hallucinations.

### Hooks documented as real but not exercised

`PermissionDenied` (auto-classifier denial path), `Notification[elicitation_dialog]`, `Elicitation`, `ElicitationResult`, `PreCompact`/`PostCompact`/`PostToolUseFailure`/`WorktreeCreate`/`WorktreeRemove`, `Setup`, `UserPromptExpansion`, `TaskCreated`/`TaskCompleted`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `FileChanged`. Per docs all are real; they don't fire in our scenarios because the trigger conditions aren't present. Defer subscription until a use case appears.

### Notification matcher inventory (per docs)

`permission_prompt`, `idle_prompt`, `auth_success`, `elicitation_dialog`, `elicitation_complete`, `elicitation_response`. We empirically observed `permission_prompt` and `idle_prompt`.

### Updates to Step 3 and Step 4

These are now fully unblocked. Step 3 (Notification wiring) reads:

```
PermissionRequest where tool_name != AskUserQuestion → pending_approval
PermissionRequest where tool_name == AskUserQuestion → waiting_input
PermissionDenied (auto-mode path)                    → pending_approval (user must act)
Notification[idle_prompt]                            → waiting_input
Notification[permission_prompt]                      → redundant; PermissionRequest fires first
Notification[auth_success]                           → no-op (informational)
```

Both denial paths (`PermissionDenied` from auto-mode classifier, or silent halt from user UI denial) leave attn in `pending_approval`. attn keeps flashing until a clearing signal (`UserPromptSubmit`, `Stop`, `SessionEnd`).

Step 4: no special timeout needed — `pending_approval` is sticky-until-clearing-signal by design.

---

## Step 1 — Drop TodoWrite tracking ✅ DONE (2026-04-30)

**Files to remove or trim:**

- `internal/hooks/hooks.go`: remove the `PostToolUse[TodoWrite]` entry.
- `cmd/attn/main.go`: remove `_hook-todo` subcommand and `runHookTodo`.
- `internal/store`: remove todo fields from session record + helper APIs.
- `internal/protocol/schema/main.tsp`: remove todo-related types.
- Frontend: remove todo rendering, todo store slice, related tests.
- `CHANGELOG.md`: note removal under today's section.

**Behaviors that must survive:**
- All other hook commands continue to work (`_hook-stop`, `_hook-state`).
- Session lifecycle untouched.
- Protocol version bump per AGENTS.md "Critical Pattern 1" since we're changing event shape.

**Verification:**
- `make test` passes.
- `make test-frontend` passes.
- Open the app, run `attn -s test`, confirm no console errors and no broken UI elements.

---

## Step 2 — Self-driving ledger + working-state mapping

Depends on Step 0 confirming `tool_input` schemas for `ScheduleWakeup`/`CronCreate`/`Monitor`.

**Schema (in `internal/store`):**

```go
type SelfDrivingEntry struct {
    Kind     string    // "wakeup" | "cron" | "monitor"
    ID       string    // tool-provided id when available; synthesized otherwise
    AddedAt  time.Time
    WakeAt   *time.Time // for wakeup
    CronExpr string     // for cron
    Local    bool       // distinguish /loop from cloud routines (per Step 0 Q1)
}

type SessionState struct {
    // ...existing fields...
    SelfDriving []SelfDrivingEntry
}
```

**Hook handlers in `cmd/attn/main.go`:**
- `_hook-self-add Kind` reads `tool_input` JSON from stdin, parses kind-specific fields, appends to ledger.
- `_hook-self-remove Kind` reads cancel tool's `tool_input`, removes by id.

**Settings.json additions (`internal/hooks/hooks.go`):**

```
PostToolUse[ScheduleWakeup] → _hook-self-add wakeup
PostToolUse[CronCreate]     → _hook-self-add cron
PostToolUse[Monitor]        → _hook-self-add monitor
PostToolUse[CronDelete]     → _hook-self-remove cron
PostToolUse[TaskStop]       → _hook-self-remove cron      # if Step 0 confirms /loop cancels via TaskStop
```

**Classifier integration (`internal/classifier`):**

At Stop time, before invoking the LLM:

```go
if len(session.SelfDriving) > 0 && !allEntriesStale(session.SelfDriving, now) {
    return "working", nil // skip classifier
}
```

Stale = `WakeAt` more than 2× original delay in the past, OR no Wakeat and entry older than 24h.

**On `UserPromptSubmit` and any tool fire:** if the agent has resumed (any input/tool activity after the most recent ledger `WakeAt`), drop expired wakeup entries. Crons stay until explicitly canceled.

**Behaviors that must survive:**
- Existing classifier path for sessions with empty ledger.
- All current state transitions.
- Classifier timestamp protection (per AGENTS.md "Critical Pattern 3") — ledger updates use the same `UpdateStateWithTimestamp` API.

**Verification:**
- New unit tests: ledger add/remove/expire.
- New integration test: stop with non-empty ledger → state == working without classifier call.
- Manual: run `attn -s test`, have Claude call `ScheduleWakeup` and stop, observe `working` (not `idle`/`waiting_input`).

---

## Step 3 — Switch to Notification + PostToolBatch

Depends on Step 0 confirming Notification fires for the cases we expect, and on PostToolBatch availability.

**Settings.json change (`internal/hooks/hooks.go`):**

Remove:
- `PreToolUse[AskUserQuestion]`
- `PermissionRequest[*]`
- `PostToolUse[AskUserQuestion]`
- `PostToolUse[*]` (general working reset)

Add:
- `Notification[idle_prompt]` → `_hook-state $sid waiting_input`
- `Notification[elicitation_dialog]` → `_hook-state $sid waiting_input`
- `Notification[permission_prompt]` → `_hook-state $sid pending_approval`
- `PostToolBatch[*]` → `_hook-state $sid working` (only if Step 0 confirms it fires; otherwise keep `PostToolUse[*]`)

**Behaviors that must survive:**
- pending_approval still appears for permission flows (with the few-seconds delay from Notification firing after the dialog appears — explicitly accepted).
- waiting_input still appears for `AskUserQuestion`.
- Working state still resets after tool batches.

**Verification:**
- Existing daemon tests for state transitions pass.
- Manual: trigger each transition (ask-user-question, permission needed, normal completion) and confirm correct color.

---

## Step 4 — SessionEnd / StopFailure / stop_hook_active

**Settings.json additions:**

- `SessionEnd[*]` → `_hook-session-end` (records `reason`, marks session terminated cleanly).
- `StopFailure[*]` → `_hook-stop-failure` (sets a distinct error sub-state with the API error type).

**Stop hook update (`cmd/attn/main.go runHookStop`):**

```go
if input.StopHookActive {
    return // bail to avoid re-entry
}
```

**New session sub-state for API errors** — display alongside `idle` (e.g., red badge) so users know to investigate. Protocol bump.

**Behaviors that must survive:**
- Stop hook still triggers classifier on the first invocation.
- Sessions terminated via `SessionEnd` no longer linger in `working`/`launching`.

**Verification:**
- New tests for stop_hook_active short-circuit.
- New tests for error-sub-state plumbing.
- Manual: kill a session via Ctrl-D → SessionEnd path verified.

---

## Step 5 — Tighten classifier role

**Decision rule at Stop:**

```
1. If StopFailure already set state for this turn → keep it.
2. If self-driving ledger non-empty (Step 2) → working.
3. If a Notification[idle_prompt|elicitation_dialog|permission_prompt] fired during the turn → that state is already set; classifier is a no-op confirmation.
4. Otherwise → run classifier (the genuinely ambiguous case).
```

This shrinks classifier traffic significantly and removes the cases where it currently disagrees with Notification.

**Behaviors that must survive:**
- Classifier-driven WAITING/DONE distinction for ambiguous stops.
- All existing `internal/classifier/classifier_test.go` cases continue to pass.

**Verification:**
- Add a counter for "classifier skipped" reasons. Should show non-trivial skip rate after Step 2 rolls in.

---

## Out of scope

- Removing the LLM-classifier entirely. Step 0 may suggest it's possible, but we don't ship that change in this plan.
- Routines / `/schedule` cloud agents — explicitly out, attn only watches local sessions.
- Mobile / remote dev support for the ledger (where `~/.claude/` lives elsewhere).
- New UI for self-driving sub-states. Step 2 maps everything to `working`; substates land in a follow-up if useful.

## Risk register

- **Hallucinated hooks.** The hook list above came from a docs-research pass that probably over-reports. Several of the 🔴 events (`PostToolBatch`, `StopFailure`, `Notification` typed matchers, `elicitation_dialog`) may simply not exist. Mitigation: Step 0's "Hooks NOT confirmed" report surfaces these explicitly, and Steps 3–5 are written to gracefully shrink — every replacement has a fallback to the production hook we use today. Expect to delete pieces of the target state, not just verify them.
- **Notification discriminator unknown.** If it's payload-based rather than matcher-based, Step 3's settings.json wiring changes (single `Notification[*]` subscription, dispatch in the handler instead of the matcher). Mitigation: design `_hook-state` to accept an optional `--from-stdin-field` arg that reads the discriminator from payload before deciding state; the harness data tells us which mode to use.
- **Step 0 might find `Notification[idle_prompt]` doesn't fire in some cases we currently catch via `PreToolUse[AskUserQuestion]`.** Mitigation: keep both during a transition window if coverage is incomplete.
- **`PostToolBatch` may not exist on user's CC version.** Mitigation: fallback to `PostToolUse[*]` for the working-reset signal.
- **CronCreate ambiguity (Q3) unresolved.** Mitigation: filter cloud routines from the ledger by inspecting `tool_input` keys; if indistinguishable, accept that cloud-routine sessions briefly classify as `working` (acceptable — they're rare and self-correct on next turn).
- **Wakeup-fire signal (Q7) unresolved.** Mitigation: ledger entries expire on time-based heuristic + `UserPromptSubmit` decrement; rare false-positive `working` states accepted.

## Sequencing

Step 0 → Step 1 (independent, do in parallel if convenient) → Step 2 → Step 3 → Step 4 → Step 5.

CHANGELOG entry gets one bullet per shipped step.
