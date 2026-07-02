# Design: Orphaned-ticket reconciliation (dead session, non-terminal ticket)

Status: **implemented in this PR**. The core design was ratified by Victor +
chief of staff on 2026-07-01; sections marked **[ratified]** restate those
decisions with codebase grounding. The four questions originally fenced
**[Open — Victor]** were decided by Victor on 2026-07-01 (see "Decided
questions" below) and the implementation ships alongside this document. Where
codebase reality bends the ratified framing, the divergence is called out
explicitly rather than silently absorbed.

## Invariant and problem

**Invariant to enforce: no non-terminal ticket without a live owning session.**

Delegated agents sometimes stop without driving their ticket to a terminal
column: they leave it in Working or In Review and Victor closes the session. The
board then lies — the column is driven purely by agent self-report
(`handleSetTicketStatus`, `internal/daemon/ticket_status.go:42`), while session
liveness is a fact attn already knows. Death while the ticket is non-terminal is
always an anomaly: In Review is a legitimate resting state, but only while the
owning agent is alive; if the session dies there, the agent either stopped early
or forgot to complete.

## What the codebase does today (grounding)

### The two session-death seams

Session death is observed at two disjoint choke points, both already wired to
tickets:

- **Process death** → `handlePTYExit` (`internal/daemon/daemon.go:1354`), fed by
  the worker backend's exit events (`ptybackend.ExitInfo`,
  `internal/ptybackend/backend.go:122`; fired from `worker.go:2122` on a worker
  `EventExit`, `worker.go:1900` when the 30s-unreachable health poller synthesizes
  `Signal:"worker_unreachable"`, and `worker.go:1956` legacy poll). It captures
  the **pre-clobber** session state, calls
  `captureTicketCrashState(id, state)` (daemon.go:1391-1396), then clobbers the
  session to `idle` (daemon.go:1398).
- **User close / teardown** → `unregisterSession` (daemon.go:1469; called from
  `ws_session.go:26` `handleUnregisterWS`, workspace teardown, pane close,
  delegate cleanup) and `removeReapedSession` (daemon.go:1486; startup reap).
  Both route through `dropSessionRecord` (daemon.go:1500), which re-runs
  `captureTicketCrashState` as a backstop and then **deletes the session row**
  (`store.Remove`, `internal/store/store.go:264`). There is no persisted
  "ended" session state: close/teardown/reap paths delete the row, while a
  spontaneous process exit leaves the row behind clobbered to `idle` with no
  live runtime (daemon.go:1398).

Note the user-close path usually fires *both* seams: `terminateSession`'s `Kill`
(daemon.go:1457) blocks until the child is dead, so `handlePTYExit` fires first
with the pre-clobber state, then `dropSessionRecord` runs as first-writer-wins
backstop. Any death-hook we add at this seam must tolerate double-fire.

### Crash capture and the deliberate gap

`captureTicketCrashState` (`internal/daemon/ticket_crash.go:37`) is the one
attn-authored ticket transition today: if the pre-clobber state was **mid-flight**
(`launching | working | pending_approval` — `isMidFlightCrashState`,
ticket_crash.go:16), it stamps the session's active non-terminal ticket
(`ActiveTicketForSession`, `internal/store/tickets.go:353`) to `crashed`
(terminal) with a fixed comment, then `notifyTicketObservers` +
`broadcastTicketsUpdated`. **Neutral ends** (`idle | waiting_input | unknown`)
deliberately "leave the ticket wherever the agent last reported it"
(ticket_crash.go:13-15).

That neutral-end path is exactly the orphan gap: agent stops early at idle with
the ticket still Working, or dies/closes while parked In Review → nothing
happens, the board lies forever. Mid-flight deaths are already surfaced
(bluntly) as Crashed.

### Ticket model facts this design leans on

- Statuses (`internal/store/tickets.go:25-43`): `todo, working, blocked,
  in_review, done, failed, crashed`; terminal = `done|failed|crashed`
  (`IsTerminal`, tickets.go:71). Terminal stamps `closed_at` and enters the
  30-day TTL sweep (`SweepExpiredTickets`, tickets.go:737; terminal statuses
  hardcoded at :748).
- Binding: `ticket.assignee == session id` for delegated work
  (`delegate_ticket.go:40` sets it at delegation; `attn ticket take` reassigns
  via `AssignTicket`, `ticket_take.go` → tickets.go:568). `assignee` may also be
  `"you"` (human) or `""` (unbound todo) — `TicketAuthorYou`, tickets.go:57.
  **A session can own several non-terminal tickets** (take + delegation);
  `ActiveTicketForSession` returns only the newest (tickets.go:353-371,
  `ORDER BY created_at DESC`, first non-terminal) — a pre-existing limitation
  `captureTicketCrashState` shares.
- The ticket outlives the session row and carries what resume needs: `cwd`,
  `last_agent_id`, and `resume_session_id` (mirrored on every resume-id
  persist precisely because the session row dies — `persistResumeSessionID`,
  daemon.go:2182; consumed at spawn, `ws_pty.go:499`).
- All ticket writes are serialized by the store's process-wide mutex
  (`internal/store/store.go:22`) and use `UPDATE ... WHERE id = ?` +
  `RowsAffected` checks (tickets.go:833). There is **no existing
  compare-and-set primitive on tickets** and no flags/metadata column; the flag
  needs a migration (next free version: **60**; latest is 59 at
  `internal/store/sqlite.go:540`; single-column `ALTER TABLE` precedent:
  migration 57, sqlite.go:527).
- Comments: `AddTicketComment(id, author, comment, now)` (tickets.go:503) writes
  the display thread + a `commented` event transactionally. Author identity
  `TicketAuthorAttn = "attn"` (tickets.go:50) exists for exactly this: attn
  authoring on its own behalf, never an observer, accrues no cursors.
- Inbox: participants = assignee ∪ non-comment event authors ∪ subscribers
  (`TicketParticipants`, `internal/store/ticket_events.go:236`). The chief is a
  participant of every delegated ticket because it authored the `created` event.
  `notifyTicketObservers` (`internal/daemon/ticket_notify.go:34`) fans out to
  live participants; self-monitors drain via `attn ticket inbox --watch`, others
  get the fixed doorbell (`ticketNudgePrompt`, ticket_notify.go:14) through the
  pausable countdown (`nudge_countdown.go`).

### Where the ratified framing meets codebase reality

1. **"No auto-transition anywhere" vs the shipped crash stamp.** The ratified
   rule (§6 below) forbids machine column moves — but
   `captureTicketCrashState` already auto-moves mid-flight deaths to terminal
   `crashed`, and that behavior shipped deliberately (work-tracker slice 3c;
   see `docs/plans/2026-06-26-work-tracker.md`). This design does **not**
   silently repeal it: reconciliation covers the neutral-end deaths crash
   capture deliberately leaves alone, and the no-auto-transition rule governs
   the new path. Whether the crash stamp itself should eventually be softened
   into a reconciliation verdict is listed under open questions.
2. **There is no "Crashed column".** On the board, `failed`/`crashed` fold into
   a "Closed" sub-lane under Done (`app/src/components/TicketBoardPanel.tsx:37-40`,
   grouping at :111); columns are Todo · Working · Blocked · In Review · Done
   (:24-34). "Reuse the Crashed column" in the brainstorm therefore really means
   "reuse the `crashed` status", which is terminal — see open question 1.
3. **There is no session tombstone.** Closed sessions are deleted;
   spontaneously-exited ones linger as `idle` rows without a runtime. The
   sweep's "session dead" test is therefore "no session row for `assignee`, or
   a row with no live worker" (see §8).
4. **The daemon's existing Claude classifier is SDK-based, not `claude -p`.**
   `ClassifyWithClaude` (`internal/classifier/classifier.go:426`) uses the agent
   SDK (`WithMaxTurns(2)`, `WithOutputFormat`, `WithPersistSession(false)`); the
   raw-CLI precedent with env-allowlisting and bounded output is the headless
   runner (`internal/agent/headless.go:42`). Neither passes `--max-budget-usd`
   today; the runner is the right base and needs a small extension (§3).
5. **Codex resume ids are unreliable for transcript lookup.**
   `(*Codex).ResumeSessionIDFromStopTranscriptPath` returns `""`
   (`internal/agent/codex.go:425`), so `ticket.resume_session_id` is only
   populated for codex when the session was itself spawned as a resume
   (codex.go:421). Codex transcript discovery is cwd+time-based
   (`FindCodexTranscript`, `internal/transcript/discovery.go:15`) and its
   freshness window degrades after death — §3c resolves this.

## Design overview

```text
session ends (either seam)
  └─ reconcileTicketsOnSessionEnd(sessionID, preClobberState)      [extends ticket_crash.go]
       ├─ mid-flight state → captureTicketCrashState (unchanged: stamp crashed)
       └─ for EACH remaining non-terminal bound ticket:
            1. capture classifier inputs synchronously (brief, column, agent,
               cwd, transcript path, close context) — the session row may be
               deleted moments later
            2. CAS-claim the ticket's reconciled_at flag  ── lost the race → stop
            3. go: spawn capped headless `claude -p` verdict classifier
            4. verdict (or rule-7 failure note) → attn-authored ticket comment
            5. re-check ticket: went terminal meanwhile → drop verdict silently
            6. notifyTicketObservers + broadcastTicketsUpdated → chief's inbox

periodic sweep (backstop, same liveness authority)
  └─ every sweepInterval: non-terminal tickets with session-id assignees,
     not yet flagged, owner dead ≥ grace → same reconciliation path (steps 1–6,
     with sweep-time transcript fallbacks); also repairs claims that died
     before their verdict comment landed
```

## 1. Death-hook **[ratified]**

Extend `internal/daemon/ticket_crash.go` into the single session-end ticket
seam: rename the entry to `reconcileTicketsOnSessionEnd(sessionID, state)` and
keep both existing call sites unchanged (`handlePTYExit` daemon.go:1391-1396,
pre-clobber; `dropSessionRecord` daemon.go:1500-1508, backstop). Behavior:

- Mid-flight pre-clobber state → existing crash stamp, untouched — **and**
  (decided by Victor, 2026-07-01) the same ticket then continues down the
  reconciliation path, so a crashed ticket gets a classifier verdict comment
  too. The verdict's went-terminal drop rule treats the just-stamped `crashed`
  as the expected status so the stamp does not suppress its own verdict.
- Enumerate **all** non-terminal tickets with `assignee ==
  sessionID` (new store query beside `ActiveTicketForSession` — a
  `ListTickets`-shaped variant without the newest-only cut; fixing the
  newest-only limitation for the crash stamp too is a free adjacent win) and
  run the reconciliation path per ticket.
- All classifier inputs are captured **synchronously at the seam** while the
  session row still exists (`dropSessionRecord` deletes it immediately after);
  the classifier itself runs in a goroutine and must never re-read the session.
- Double-fire (exit seam then drop seam) is expected; the flag CAS (§2)
  dedupes.
- Close-context framing: the seam knows whether this was a spontaneous process
  exit or a user/teardown-initiated close (`markForcedStopClassification` is set
  by `terminateSession`, daemon.go:1451 — peek, don't consume, or thread a
  reason through from `unregisterSession`). Included in the classifier prompt
  and the verdict comment ("closed by user while In Review" reads differently
  from "process died while idle"), not used for any gating.

## 2. Flag-as-lock **[ratified mechanism; placement resolved here]**

**Placement: a new `reconciled_at TEXT NOT NULL DEFAULT ''` column on
`tickets`** (migration 60, RFC3339 UTC like every other ticket timestamp —
`formatTicketTime`, tickets.go:930). Empty = never machine-reconciled. The
timestamp doubles as provenance ("this verdict was reconciliation, not
self-report") and as the dedupe lock between death-hook and sweep.

**Primitive: a true set-if-unset, new but trivial under the existing model:**

```go
// ClaimTicketReconciliation atomically claims the machine-reconciliation flag.
// Returns false when the flag was already set (another path owns this verdict).
func (s *Store) ClaimTicketReconciliation(id string, now time.Time) (bool, error)
// UPDATE tickets SET reconciled_at = ?, updated_at = ? WHERE id = ? AND reconciled_at = ''
// claimed := RowsAffected == 1
```

The store mutex (`store.go:22`) already serializes writers in-process; the
`WHERE reconciled_at = ''` guard makes the claim correct even across daemon
restarts and between the hook and sweep paths. Claim happens **before** the
classifier spawns (flag-as-lock, ratified §2).

**Re-arm.** A claimed flag means "this death was judged once". If the owning
session comes back — ticket resume respawns against the stored
`resume_session_id` (`ws_pty.go:499`) or the ticket is reassigned — the flag
must clear so a *future* death is judged again. Clear points:
`AssignTicket` (tickets.go:568) and the spawn-success path when the spawned
session id matches a flagged ticket's assignee (`ws_pty.go` around :692-704).
Rejected alternative: clear on any non-attn status change — self-reports can
race the classifier goroutine and re-arm a claim that is mid-flight.

**Rejected placements:**

- *New `TicketEventKind` ("reconciled") as the flag.* No schema change, but
  "not yet flagged" becomes an event-log scan, the kind fans out into the
  generated protocol enum (TypeSpec + `make generate-types` + the quicktype
  identical-enum merge trap), and events are a notification substrate — a lock
  is not a notification.
- *A new `orphaned` ticket status.* A machine column move — violates the
  ratified no-auto-transition rule and would force a terminal/non-terminal call
  the design deliberately leaves to Victor.
- *Daemon-memory dedupe map.* Lost on restart; the hook/sweep race would
  reopen exactly when reconciliation is most likely to double-fire (restart
  reap).

## 3. Classifier **[ratified shape; integration resolved here]**

### 3a. One classifier CLI, always `claude -p` **[ratified]**

The classifier is always Claude Code headless, regardless of which CLI the dead
agent ran — a transcript is just a file, and an agentic reader handles Codex
rollout JSONL fine. Rationale (from the ratified brainstorm, verified against
Codex CLI 0.142.3): Codex `exec` has no dollar cap, no max-turns, no timeout;
its token cap (`features.rollout_budget`) is unstable/under-development. Claude
Code has `--max-budget-usd`, `--max-turns`, `--json-schema`, and
`--output-format json` (which reports `total_cost_usd` for spend logging).

**Rejected alternative — per-CLI classifiers with per-CLI caps** (codex judging
codex transcripts): keeps each transcript in its "native" reader, but doubles
the prompt/parse/failure surface and, decisively, cannot enforce the runaway
backstop on the codex side at all. The caps are the safety property; the
native-format benefit is nil for an agentic reader with file tools.

### 3b. Invocation

Shaped like (ratified): `claude -p --model sonnet --max-turns 15
--max-budget-usd 0.50 --json-schema <verdict schema> --output-format json`.

Integration: extend the existing headless machinery rather than hand-rolling a
new exec path. `runHeadlessCommand` (`internal/agent/headless.go:42`) already
provides the env **allowlist** (`headlessEnvironment("claude")`, headless.go:89
— parent-session `CLAUDE_CODE_SESSION_ID` etc. are dropped by omission, plus
`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`), a 1 MiB bounded output, and stderr →
diagnostic mapping (`classifyHeadlessFailure`, headless.go:147). Concretely:

- Add optional `MaxTurns int`, `MaxBudgetUSD string`, and `OutputSchema
  json.RawMessage` fields to `HeadlessTaskRequest`
  (`internal/agent/driver.go:302`); `claudeHeadlessArgs`
  (`internal/agent/claude.go:270`) appends `--max-turns` / `--max-budget-usd` /
  `--json-schema` when set. The native path already passes `--permission-mode
  dontAsk --output-format json` and `--allowedTools`.
- Tools: `Read,Grep,Glob` only (read-only subset of `claudeNativeDefaultTools`,
  claude.go:143). `WorkDir`: a scratch temp dir (keeper pattern,
  `workspace_keeper.go:448`); the transcript is addressed by absolute path.
- Executable/model resolution mirrors the keeper: configured setting →
  `driver.ResolveExecutable` → `exec.LookPath` (workspace_keeper.go:461-463).
  Model `sonnet` (env-overridable, mirroring `ATTN_CLAUDE_CLASSIFIER_MODEL`,
  classifier.go:427).
- Wall-clock cap: `context.WithTimeout` 5 minutes (keeper precedent,
  `defaultKeeperCompactTimeout`, workspace_keeper.go:29). The dollar/turn caps
  are the runaway backstop, not the primary control — cost scales with
  ambiguity because the prompt instructs early exit (§3d).
- Spend logging: parse `total_cost_usd` / `num_turns` from the
  `--output-format json` envelope (`parseClaudeFinalText`, claude.go:311, needs
  a sibling that surfaces the envelope fields) → `d.logf`.
- Concurrency: a small semaphore (2 concurrent runs) — workspace teardown can
  kill several delegated sessions at once; without a cap that is N parallel
  sonnet processes.

New daemon file `internal/daemon/ticket_reconcile.go` owns: input capture
struct, claim, spawn, verdict parse, comment render, terminal re-check,
notify/broadcast — patterned on the keeper executor
(`executeKeeperCompact`, workspace_keeper.go:448) for the spawn half and on
`captureTicketCrashState` for the ticket half.

**Rejected alternative — the SDK path** (`ClassifyWithClaude`,
classifier.go:426): structured output and turn caps, yes, but no
budget-cap equivalent to `--max-budget-usd`, and the ratified cap set is
CLI-flag-shaped. The headless runner also already solves env hygiene, which the
SDK path would need re-verifying.

### 3c. Transcript-path resolution **[resolved]**

Two moments, two strategies:

- **At the death-hook (the common case):** the session row is alive; resolve
  exactly as `resolveTranscriptPathForSession` does (daemon.go:2411) via the
  driver `TranscriptFinder` (driver.go:436). Claude: prefer
  `GetTicketResumeSessionID(assignee)` (tickets.go:622 — the latest
  claude-native id after resumes, mirrored by `persistResumeSessionID`,
  daemon.go:2182) and fall back to the attn session id;
  `FindClaudeTranscript` (discovery.go:325) matches `<id>.jsonl` under
  `~/.claude/projects/…`. Codex: `FindCodexTranscript(session.Directory,
  startedAt)` (discovery.go:15) — at death time the rollout file was written
  moments ago, so the 5-minute mod-time window and newest-match logic hold.
- **At the sweep (session row gone):** Claude is unaffected (ids live on the
  ticket). Codex: call `FindCodexTranscript(ticket.Cwd, ticket.CreatedAt)` —
  the ticket's creation time is the delegation/spawn moment, and an earlier
  anchor only widens the acceptance window. Risk: a newer same-cwd rollout
  (another codex session in the same directory) wins the newest-match; accepted
  for a backstop path — worst case the classifier reads the wrong transcript
  and reports low confidence, or resolution fails and rule 7 applies
  ("could not locate the session transcript").

Rejected alternative: persist the resolved transcript path on the ticket at
death time (another column, and the death-hook already covers the common case;
the sweep exists precisely for the daemon-was-down window where nothing got to
persist anything).

### 3d. Prompt contract **[ratified]**

Inputs: transcript **path** (not contents), ticket title + description, current
column, agent type (claude/codex — tells the reader which JSONL dialect to
expect), close context (§1). Instructions: the ticket description is the
definition of done — never judge the tail alone; read backwards from the tail
in chunks (`Read` with offset/limit) and stop as soon as a verdict is
supportable; report the evidence turn. Cost scales with ambiguity, not
transcript size.

## 4. Verdict schema **[resolved]**

Passed via `--json-schema`; enforced server-side so parse failures are cap/rule-7
territory, not silent misreads:

```json
{
  "type": "object",
  "properties": {
    "assessment": {
      "type": "string",
      "enum": ["done", "partial", "interrupted", "blocked_unreported"]
    },
    "confidence": { "type": "string", "enum": ["high", "medium", "low"] },
    "whats_left": {
      "type": "string",
      "description": "One line. Empty string when assessment is done."
    },
    "evidence": {
      "type": "string",
      "description": "Pointer to the supporting turn(s): position/timestamp plus a short quote."
    }
  },
  "required": ["assessment", "confidence", "whats_left", "evidence"],
  "additionalProperties": false
}
```

- `done` — brief satisfied; the agent finished but never reported terminal.
- `partial` — real progress, identifiable remainder.
- `interrupted` — cut off mid-work; remainder is "most of it" or unclear.
- `blocked_unreported` — stopped on a blocker it never surfaced as `needs_input`.

Confidence is an enum, not a float: it shapes the chief's framing (ratified §6),
and three levels are what a human framing decision actually consumes. A `verdict
unclear` case is deliberately *not* in the enum — "could not determine" is the
rule-7 failure comment, keeping "the model judged X" and "the machinery failed
to judge" distinguishable forever.

## 5. Verdict delivery **[ratified]**

The verdict lands as a ticket comment via `AddTicketComment` authored as
`TicketAuthorAttn` (tickets.go:503, :50) — durable, survives chief restart,
visible in `attn ticket list`/`get`. Render:

```
🩺 Reconciliation verdict — session s-abc123 ended (closed by user) while this
ticket was In Review.
Assessment: partial (confidence: medium)
What's left: e2e spec for the copy path was written but never run.
Evidence: last assistant turn (~14:32): "tests pass locally except e2e, which…"
```

Then the existing fan-out: `notifyTicketObservers(ticketID)`
(ticket_notify.go:34) + `broadcastTicketsUpdated()` (ticket_board.go:72). The
chief is a participant (it authored `created` at delegation) and self-monitors
via `attn ticket inbox --watch`; non-self-monitor chiefs get the standard
countdown doorbell. No new event kind, no new protocol message for delivery.

Edge worth naming: a ticket with no chief in its participant set (created by
`attn ticket new` from the user, taken by an agent that later died) may have no
live participant to notify — the comment still lands and the board badge (§6)
is the durable surface.

## 6. No auto-transition; board representation **[ratified rule; representation decided]**

Neither the daemon nor the classifier moves the column on the reconciliation
path. The chief presents verdict + evidence; Victor decides (close /
re-delegate / take over / drop). Confidence shapes framing, never action.
(Reality note §"meets reality" 1: the pre-existing mid-flight crash stamp is
the standing exception and stays.)

**Decided (Victor, 2026-07-01): a distinct orphan badge — with the condition
that it is genuinely visible on the card face.** Surface `reconciled_at`
(plus, implicitly, "assignee not live") as a red "orphaned" pill rendered
*above the card title* in the ticket's *current* column, plus the same badge
next to the status in `TicketDetailPanel` (tooltip carries the stamp time).
Reusing `crashed` was rejected because it would (a) be a machine transition to
a **terminal** status — literally the thing rule 6 forbids — (b) start the
30-day TTL clock (tickets.go:748) on work whose output may be sitting ready
for review, and (c) collapse the retry-vs-review framing. The user response
differs by case — crash → "retry"; closed-while-In-Review → "output ready,
just review" — and conflating them loses that. Cost paid: `reconciled_at`
joined the protocol `Ticket` model (TypeSpec `main.tsp` → `make
generate-types` → `ProtocolVersion` 139→140) and both panels grew the badge.
The comment-only alternative (zero protocol churn) was rejected because the
board keeps lying to anyone not reading comments, which is the problem
statement. Victor also noted the chief will usually handle the verdict comment
on wake-up anyway — the badge is the durable board-level truth for the cases
where no chief is watching (§5 edge).

## 7. Cap-hit is not a verdict **[ratified]**

Classifier exec error, timeout, budget/turn cap-hit, schema-invalid output, or
unresolvable transcript → still flag (already claimed) + attn comment:

```
🩺 Reconciliation could not determine the outcome — needs a human look.
(reason: budget cap hit after 15 turns / transcript not found / …)
```

Reconciliation failure must surface, not vanish. One shot per claim — no
automatic retry (a retry loop against a pathological transcript is exactly the
runaway the caps exist to prevent); the re-arm rules (§2) govern when a fresh
death gets a fresh verdict.

## 8. Sweep backstop **[ratified]**

A periodic daemon loop patterned on `monitorBranches` (daemon.go:3486 — ticker
+ `d.done` select). Recommended cadence: **every 5 minutes, grace 15 minutes**
(rationale: daemon startup recovery including deferred worker reconciliation
completes within ~30s — `deferredRecoveryMaxAttempts=3 × 10s`, daemon.go:1182 —
and a quick close-then-resume flow lives in minutes; 15 min clears both with
margin while still surfacing an orphan within the same working session).
Tunable (decided by Victor, 2026-07-01): every knob is an env override
(`ATTN_TICKET_RECONCILE_MODEL` / `_MAX_TURNS` / `_MAX_BUDGET_USD` / `_TIMEOUT`
/ `_SWEEP_INTERVAL` / `_GRACE`) over the recommended defaults — not a settings
surface.

Per pass:

1. `ListTickets` (tickets.go:308) filtered to non-terminal, `assignee ∉ {"",
   "you"}`, `reconciled_at == ''`.
2. **Liveness — same authority as the death-hook, not its own heuristic
   [ratified].** The death-hook's source is the worker backend's exit events;
   the sweep asks the same backend the standing form of the same question:
   dead ⇔ `store.Get(assignee) == nil` (rows are deleted on death — §"meets
   reality" 3) **or** the row exists but the backend has no live runtime
   (`SessionIDs` membership / `SessionLikelyAlive`,
   `internal/ptybackend/backend.go:192` — the probes the startup reap already
   trusts, daemon.go:1139-1177). A `Recoverable` row without a live worker
   counts as dead: resumable is not alive, and the invariant is about live
   ownership.
3. Grace: in-memory `map[ticketID]firstSeenDead`; reconcile once dead ≥ grace.
   Restart resets the clock — acceptable for a backstop (delays, never loses:
   the ticket is still non-terminal + dead next pass). Rejected: persisting
   first-seen-dead (a column for a timer).
4. Claim via the same CAS; its only direct write is the claim [ratified]. Then
   the same classifier path with sweep-time transcript fallbacks (§3c).
5. **Claim-crash repair:** the claim and the verdict comment are not atomic —
   a daemon death between them leaves a claimed ticket with no verdict, which
   would otherwise vanish silently (violating rule 7). The sweep detects
   flagged, non-terminal tickets older than grace with no attn-authored
   reconciliation comment (scan `GetTicket().Activity` for the marker prefix)
   and posts the rule-7 comment. No re-classify.
6. Per-pass claim cap (e.g. 3): on first deploy the initial pass may find a
   backlog of historical orphans; a cap turns that into a trickle instead of a
   burst of concurrent sonnet spend.
7. After the classifier returns, re-fetch the ticket; gone terminal meanwhile →
   drop the verdict silently [ratified] (log only, flag stays as provenance
   that reconciliation ran).

The sweep exists for what the death-hook structurally cannot cover: tickets
orphaned before this feature ships; a daemon death mid-seam (row already
removed, flag not yet claimed); claim-crash repair (step 5); and any session-end
path a future change forgets to route through the seam. Note that a plain
daemon-down death is *not* in this list — the row survives the outage and
startup recovery's reap routes through `dropSessionRecord` → the death-hook
seam (daemon.go:1139-1177).

## Decisions (rationale + rejected alternative)

| Decision | Rationale | Rejected |
|---|---|---|
| Reconciliation covers **all** session deaths: mid-flight keeps the crash stamp and also gets a verdict; neutral ends get a verdict (decided by Victor, 2026-07-01) | The gap is precisely the complement of `captureTicketCrashState`; repealing shipped crash behavior is a separate product call; the verdict is cheap once the runner exists and "interrupted at X" beats a bare Crashed | Replace the crash stamp with reconciliation (strict rule-6 reading — board loses immediate crash truth); verdicts for neutral ends only (a bare Crashed says nothing about what was lost) |
| Single classifier CLI: always `claude -p` | Only CLI with dollar+turn caps and schema-enforced JSON output; transcript is just a file for an agentic reader | Per-CLI classifiers — no enforceable caps on codex, double prompt/parse surface (§3a) |
| Flag = `reconciled_at` column + `ClaimTicketReconciliation` CAS | Provenance + lock in one durable cell; `RowsAffected` under the store mutex is a true set-if-unset | Event-kind-as-flag (log scan + protocol enum churn); `orphaned` status (machine transition); in-memory dedupe (restart hole) |
| Verdict = ticket comment authored `attn` | Durable, chief-restart-proof, already fans out to participants; `TicketAuthorAttn` exists for exactly this | New event kind / protocol message (delivery machinery already exists); comment-as-flag (breaks flag-before-spawn ordering) |
| Distinct orphan badge, not `crashed` (decided by Victor, 2026-07-01 — conditional on card-face visibility) | `crashed` is terminal ⇒ TTL + rule-6 violation + loses retry-vs-review framing | Reuse `crashed`; no UI surface (§6) |
| Raw-exec headless runner, extended | Env allowlist, bounded output, failure mapping already solved; caps are CLI flags | SDK path (no budget cap; env hygiene unproven there) |
| One shot per claim; failures comment, never retry | Rule 7 + runaway protection | Auto-retry on cap-hit (the pathological case retries forever) |
| Grace via in-memory first-seen-dead | Backstop may delay, must not lose; restart merely delays | Persisted death timestamps (schema for a timer) |

## Decided questions (Victor, 2026-07-01)

All four questions originally left open were decided in review of this
document; the implementation in this PR reflects them.

1. **Board representation of the orphan anomaly → distinct badge, conditional
   on visibility.** "I'm fine with the badge but only if it would be visible"
   — so the pill sits on the card face above the title, not buried in the meta
   row (§6). `reconciled_at` therefore entered the protocol `Ticket` model
   (ProtocolVersion 139→140). Victor also observed the chief usually handles
   the verdict comment immediately on wake-up; the badge covers the no-chief
   and chief-missed cases.
2. **Mid-flight crashes also get a classifier verdict comment → yes.** The
   Crashed stamp stays for the immediate board truth; the verdict adds what a
   bare Crashed cannot — what was in flight and what's left (§1).
3. **Tunables → yes, tunable.** The recommended defaults (sonnet / 15 turns /
   $0.50 / 5-min timeout; sweep 5 min / grace 15 min / per-pass cap 3 /
   concurrency 2) shipped as env overrides `ATTN_TICKET_RECONCILE_*` (§3b,
   §8).
4. **Re-arm surface → as recommended.** Clear the flag on `AssignTicket` and
   on assignee-session respawn/register; never on self-reported status changes
   (§2).

## Non-goals **[ratified]**

- Idle-but-alive nudging (explicitly dropped by Victor).
- Auto-closing or any autonomous chief action; no machine column moves on the
  reconciliation path.

## Implementation map (as shipped in this PR)

1. **Store:** migration 60 (`reconciled_at`, `columnExists`-guarded for
   idempotent re-runs); `ClaimTicketReconciliation` /
   `ClearTicketReconciliationForAssignee`; `ActiveTicketsForSession`
   (all-non-terminal-for-assignee). (`internal/store/sqlite.go`,
   `internal/store/tickets.go`)
2. **Seam:** `ticket_crash.go` reduced to the crash stamp;
   `reconcileTicketsOnSessionEnd` in the new
   `internal/daemon/ticket_reconcile.go` owns synchronous input capture and
   claim→classify→comment; both call sites unchanged in shape.
3. **Runner:** `HeadlessTaskRequest{MaxTurns, MaxBudgetUSD, OutputSchema}` +
   `claudeHeadlessArgs` flags; result envelope parsing surfaces
   `structured_output` / `total_cost_usd` / `num_turns` (single-object and
   stream-array shapes). (`internal/agent/driver.go`, `claude.go`)
4. **Sweep:** ticker loop + conservative liveness + in-memory grace map +
   per-pass claim cap + claim-crash repair.
   (`internal/daemon/ticket_reconcile.go`)
5. **Protocol + UI:** `Ticket.reconciled_at` on the wire (ProtocolVersion
   139→140); card-face orphan badge in `TicketBoardPanel.tsx` and a matching
   badge in `TicketDetailPanel.tsx` (`isTicketOrphaned`,
   `app/src/utils/ticketOrphan.ts`).
6. **Tests:** store CAS (claim twice, re-claim after clear, AssignTicket
   re-arm); seam double-fire dedupe; fake-classifier daemon tests via an
   injectable exec (nil in test constructors, real `claude -p` wired only in
   production `New()`); sweep grace/liveness matrix; transcript resolution
   fallbacks; rule-7 comment on failure classes; verdict-drop when the ticket
   went terminal mid-classify; frontend badge visibility matrix.

## Verification of this doc

Every integration point above names a real code location at current `main`
(`80c62f6b`); the death seams, crash capture, ticket store surface, notify
fan-out, headless runner, and transcript discovery were each read directly
rather than assumed. Ratified decisions are restated with their rationale and
the rejected alternative; the four originally-open calls are recorded under
"Decided questions" with Victor's 2026-07-01 answers.
