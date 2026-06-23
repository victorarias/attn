# Self-report trigger loop — chief↔delegated-agent awareness

> Status: alignment captured 2026-06-23 (v1, iterable). Implementation map below is
> a stub — hand to plan-doc-write before building.

## Why / Alignment

**Intent.** The chief should be *aware* of its delegated agents and able to *steer*
them — bidirectional comms — **without babysitting**. Today's PR #399 delegation
drowned the chief in noise because it watched the wrong thing: process/daemon state,
which flickers (working↔waiting_input) and misfires "ended" on a turn-boundary rest.
The fix is to trigger only on meaningful, **self-reported** signals.

**The model (aligned 2026-06-23):**
- The delegated agent **self-reports only at terminal or blocked** — never progress,
  never process state. No hooks, no daemon-state inference, no auto-capture —
  self-report only.
- The chief **arms one watch per delegation** — intentionally. The watch *is* the
  bidirectional channel; it's the point of the vision. Watching is correct; *what* it
  watched was the bug.
- **The trigger mechanism fires on exactly two things:** (forward) the agent's
  self-reported terminal/blocked, and (reverse) **mailbox items** (chief→agent
  messages). Both first-class. Nothing else triggers.
- Between triggers: **silence.** The chief does not watch process/daemon state by
  default — on-demand only if it chooses to.
- **The loop:** agent self-reports blocked → chief's watch fires (carrying the
  report) → chief sends a mailbox item (steer) → the agent's watch fires on that
  mailbox item → agent reads & acts. No manual wake. This is what finally retires the
  clunky pull-based mailbox.
- **The chief's response discipline (on a `blocked` report):** the chief does *not*
  reflexively auto-reply to unblock. It steers back only when it is **sure** of the
  answer or the decision is **low-consequence**; otherwise it **escalates to Victor
  and waits**. The chief usually lacks the full context, and a confident-but-wrong
  steer is worse than waiting. Auto-reply is the exception, not the default.
- **Ambient UI:** an on-agent overlay + a sidebar pending-mail badge surface mailbox
  items visually, so a human sees pending mail without opening the dashboard.

## Aligned on
- Watch the **self-report**, not the process state.
- Altitude: **guidance + the minimal attn watch fix** — the trigger fires on
  self-report + mailbox items, not daemon state.
- Direction: build the **full bidirectional loop** this chunk.
- Reverse scope: core loop **+ ambient UI** (on-agent overlay + sidebar badge).
- **Mailbox items are part of the trigger mechanism** — first-class, alongside
  self-reports.

## In scope (this chunk)
- attn: the watch/trigger fires on **self-reported terminal/blocked + mailbox items**
  (replaces daemon-state keying — the source of today's misfires).
- attn: a reliable agent **self-report affordance** for terminal/blocked (verify what
  exists today; build the gap — today's agent sat at `waiting_input` with an empty
  report).
- attn: **reverse delivery** — a mailbox item triggers the recipient agent's watch
  (no manual wake) + ambient UI (on-agent overlay + sidebar pending-mail badge).
- Guidance:
  - chief delegation instructions — arm the watch, and how.
  - agent brief convention — self-report only at terminal/blocked; arm a watch that
    fires on mailbox items.
  - chief behavior — don't watch process state, don't spelunk transcripts, don't
    narrate intermediate ticks; re-engage on the trigger. On a `blocked` report,
    only auto-steer when **sure or low-consequence** — otherwise escalate to Victor
    and wait.

## Deferred
- Non-Claude (codex) idle **auto-wake** — the unattended reverse fallback.
- **Crash-without-report gap:** an agent that dies without self-reporting → chief sees
  silence ("still working"). Accepted for now (chief peeks on-demand); re-decide
  later. This is what doneness-on-close (#397) addressed — deprioritized under
  self-report-only, not free.
- daemon-push re-invocation, stop-hooks, any auto-inference of state — explicitly
  rejected for now.
- **Watch timeout on long idle (follow-up PR):** the chief-side `watch` may time out
  if the agent is idle a very long time with no self-report — a backstop for the
  crash-without-report gap above. Careful framing: the timeout **escalates to Victor**
  (surfaces "this agent went quiet"); it does NOT trigger the chief to auto-poke the
  agent. Pairs with the response discipline — silence is escalated, not auto-resolved.

## Related / dependency
- **"watching ≠ working"** (chief *displayed* state): an armed watch must not display
  the chief as busy, or this model reintroduces the busy-look. In-flight investigation
  (dispatch 76e5d022) + the chief-state rock. Gating-adjacent.

## Vision
[chief-delegation-awareness](../vision/chief-delegation-awareness.md). Advances:
forward-observe (self-report watch), reverse-steer (mailbox trigger + overlay +
badge), and sharpens the **signal definition** — trigger = self-report + mailbox,
NOT daemon state.

## Implementation map

> Status: design decided 2026-06-23 (Understand phase complete — 6-reader subsystem
> map). One PR, committed in logical steps. **No ProtocolVersion bump** — every change
> reuses existing protocol surfaces.

### Design decisions (grounded in this doc's model)
1. **Forward trigger = remove daemon-state from `dispatch.Classify`.** Steps 1-3
   (done/failed/review) and 5-6 (decision-request / needs_input) are *already*
   self-report-keyed — keep them. Delete steps **4 (status=closed→ended), 7
   (status=idle→ended), 8 (status=waiting_input→blocker)** — these are the
   daemon-state inference that flickers and misfires. The #399 "empty report at
   waiting_input" bug *is* step 8. After removal the watch fires **only** on a
   self-reported terminal (done/failed/review) or blocked (decision-request /
   needs_input / blocker). No opt-in `--peek` flag: on-demand state peeking already
   exists via `dispatch status` / `dispatch list`.
2. **Accepted gap (per Deferred): a watch on an agent that closes/crashes _without_ a
   terminal self-report now hangs silently.** This is correct under the model —
   silence ≠ success, and a quiet hung watch does NOT peg the chief green (see #5).
   The happy path is unaffected: a well-behaved agent files `--done`/`--failed`/
   `--review` and the watch exits on that *self-report* (steps 1-3), before/around
   close. The deferred **watch-timeout→escalate-to-Victor** is the future backstop.
   Mitigated now by the reliable self-report affordance (#3) + mandatory-terminal
   guidance (#6). The `dispatch_gone`/`not_found` neutral-terminal (record actually
   deleted from the list) stays — that's definitive record removal, not state flicker.
3. **Self-report affordance = convenience flags on `dispatch report`** that synthesize
   the structured `DispatchReport` over the *existing* `ReportDispatchEnvelope` (no
   protocol change; the enums already exist): `--done` (work_state=completed,
   report_type=completion), `--failed` (failed/failure), `--review` (ready_for_review,
   report_type=handoff), `--blocked` (needs_input, report_type=blocker) with optional
   `--question`/`--recommendation`/`--consequence` → a pending `DispatchDecisionRequest`.
   `--message`/`--file` supply the human summary (synthesized if a state flag is given
   alone). Mutually exclusive with `--coordination-file` (power-user escape hatch
   stays). Net effect: a freeform `--message` with no state flag is a **silent note**
   (stored, visible on-demand, classifies as `KindNone`) — "never report progress" is
   enforced by the trigger, not by forbidding notes.
4. **Reverse trigger = the agent self-monitors its own inbox** (a quiet `Monitor` on
   `dispatch inbox --unread`, reusing the existing command — dogfooded by this very
   agent). This is **guidance**, not new daemon code: the agent's own watch fires on
   the mailbox item, no manual wake. The daemon **auto-doorbell** (push on send) is the
   *codex* fallback and is **deferred**. The existing manual "Wake agent" button /
   `wake_dispatch_agent` stays as the attended path the on-agent overlay click fires.
5. **"watching ≠ working" needs no code.** Confirmed by the state-inference reader: an
   armed loop reads `working` only via (a) Stop payload `background_tasks` status=running
   or (b) the live PTY animated-status-frame detector. A **quiet** watch produces
   neither at rest, so it settles to idle (~6s, via the PTY detector emitting idle off
   the settled prompt). Only a *noisy* watch that re-prompts pegs green. We make the
   watch quiet by construction (guidance) → the busy-look dissolves. No state-inference
   patch in this PR.
6. **Guidance has one authoritative home: the bundled skill** at
   `internal/agent/attn_skill/references/` (Go-embedded, installed to `~/.claude` and
   `~/.agents`; never hand-edit the installed copies). `~/exo/AGENTS.md` is the chief
   *persona* doc — no delegation mechanics — and is **not** touched. No `~/exo` skill
   mirror exists.

### Steps (PR-sized, sequenced)
1. **Forward trigger (Go core).** `internal/dispatch/classify.go`: remove steps 4/7/8,
   rewrite the package doc to the self-report-only model. Rewrite the affected
   `classify_test.go` / `watch_test.go` cases (closed/idle/waiting_input). Prune the
   now-dead `defaultSummary` reasons in `watch.go`. _Self-contained; merges green alone._
2. **Self-report affordance (CLI).** `cmd/attn/main.go` `parseDispatchReportArgs`: add
   the state flags + synthesis, update `writeDispatchHelp`. Tests in `main_test.go`.
   No client/daemon signature change (reuses `ReportDispatchEnvelope`).
3. **Launch prompt + guidance.** Rewrite `chiefOfStaffDispatchPrompt`
   (`chief_of_staff_dispatch.go`) and `references/chief-of-staff.md` +
   `references/delegation.md`: chief arms one `dispatch watch`; agent self-reports
   only at terminal/blocked via the flags and arms a quiet inbox watch; chief
   response-discipline (don't watch process state / don't spelunk / escalate-unless-
   sure). Reconcile `delegation.md:18` ("does not require you to monitor").
4. **Reverse ambient UI (frontend).** `PendingMailBadge` next to
   `DelegatedFromChiefBadge` in `Sidebar.tsx` (active + muted + collapsed rail);
   derive per-session unread in `App.tsx` `enrichedLocalSessions` from
   `chiefOfStaffDispatches`; on-agent top-right overlay in
   `SessionTerminalWorkspace/index.tsx` `renderPaneSurface` (pointer-events scoped —
   respect Focus Ownership / PTY geometry), click → `sendWakeDispatchAgent`. Frontend
   tests. Uses existing `unread_message_count` — no protocol change.
5. **Changelog + plan-doc progress.** One user-facing `CHANGELOG.md` entry.

### Out of this PR (per Deferred)
Codex idle auto-wake / daemon auto-doorbell; crash-without-report handling; watch
timeout→escalate; any daemon-push / stop-hook / state auto-inference.

## Progress
- [x] Understand — 6-reader subsystem map (watch, state, self-report, mailbox, UI, guidance).
- [x] Design — decisions above; implementation map filled.
- [ ] Step 1 — forward trigger (Classify).
- [ ] Step 2 — self-report affordance (CLI flags).
- [ ] Step 3 — launch prompt + guidance.
- [ ] Step 4 — reverse ambient UI (badge + overlay).
- [ ] Step 5 — changelog + progress.
- [ ] Adversarial review + CI green + figgyster.
