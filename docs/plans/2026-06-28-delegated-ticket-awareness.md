# Plan: Close the delegated-ticket awareness gap (chief watch + daemon backstop)

## Goal

When the chief delegates, it currently goes silent until Victor prompts it: a
delegated ticket's status change pushes **nothing** into the chief session, so
completions/blocks/crashes sit unseen. Fix it with two composing mechanisms, in
**one PR**:

- **A — Guidance (in-band, always-on):** tell the Claude chief to arm a harness
  Monitor on `attn ticket inbox` right after delegating, with the
  awareness/upkeep-vs-action boundary. Delivered via the **system prompt**
  (`ChiefGuidance`), not the lazily-loaded skill — lazy-load-miss is the root
  cause. Consolidate the chief guidance out of the skill (decision: **A2**).
  Add **`attn ticket inbox --watch`** so the Monitor command is trivial and
  robust (silent unless changed) instead of a hand-rolled jq loop.
- **B — Daemon backstop (enforced):** an **idle** self-monitor with unread ticket
  activity gets the PTY doorbell instead of today's no-op, covering the case
  where no Monitor was armed (forgot / crashed / lagging). Agent-general, not
  chief-specific. **Deferred, not event-synchronous** (see the timing trap below).

They compose because `attn ticket inbox` **consumes** (advances the per-identity
cursor): A's `--watch` drains the chief's unread within its poll interval, so B's
deferred re-check sees nothing unread and **self-suppresses** — the doorbell only
fires when nothing is watching.

### The timing trap B must avoid

`notifyTicketObservers` runs **synchronously the instant an event lands** (called
from `ticket_status.go:81`, `ticket_actions.go:42`, `ticket_crash.go:62`,
`ticket_attach.go:81`), while a chief's `--watch` Monitor drains on its **poll
interval (~3s), later**. So at the notify instant `unread > 0` essentially always
— the event just landed. A naive "idle self-monitor with unread → doorbell now"
fires on **every** delegated event for a chief that *did* arm the watch: it gets
the `--watch` push **and** a spurious PTY doorbell telling it to run `attn ticket
inbox` that `--watch` then drains to empty. That is exactly the redundant
interruption the original `DeliveryWatch` no-op exists to prevent.

Fix: the self-monitor backstop is **deferred**. When the daemon sees an idle
self-monitor with unread, it schedules a debounced per-session re-check after a
grace delay (≥ the `--watch` poll interval + margin) and doorbells **only if the
session is still idle and still has unread** then. A live watch drains within its
interval → re-check sees 0 → no doorbell; an absent/dead watch leaves it unread →
backstop fires a few seconds late (fine for a backstop). `ticketnotify.Notify`
stays pure and unchanged — B is entirely a daemon-layer addition on top of the
existing `DeliveryWatch` decision.

## Why the split (root cause)

`ChiefGuidance` (`internal/hooks/hooks.go:130`) is injected unconditionally at
launch — always in context. The skill references (`chief-of-staff.md`,
`delegation.md`) are lazy-loaded on the agent's own judgment, so the watch
trigger can be absent exactly when it's needed. Today there is *no* watch
guidance anywhere to be missed — `delegation.md:18` affirmatively says delegation
"does not require you to monitor the new agent," and `ChiefGuidance:139` says the
chief's "turn is done." The chief was correctly following guidance that told it
the opposite.

On the daemon side, `ticketnotify.Notify` short-circuits any `HasSelfMonitor`
observer to `DeliveryWatch` (no-op) **before** the idle check
(`internal/ticketnotify/notify.go:158`), on the false assumption that a live
Monitor is draining the queue — but nothing tells the chief to arm one.

## Architecture Map

```text
Current — delegated ticket changes status:
  delegated agent: attn ticket status <s>
    -> store appends ticket event
      -> daemon.notifyTicketObservers(ticketID)        ticket_notify.go
        -> for each participant: Notify(obs, idle, ...)  ticketnotify/notify.go
            unread==0           -> DeliveryNone
            HasSelfMonitor      -> DeliveryWatch  (NOTHING injected) <-- chief falls here, silently
            idle & !selfmon     -> DeliveryNudge  (PTY doorbell)     <-- only codex
            busy & !selfmon     -> DeliveryDeferred
  chief: receives nothing; only sees it by manually running `attn ticket inbox`.

Target — A (push while busy) + B (deferred idle backstop):
  chief (at delegate time, per ChiefGuidance):
    Monitor: while running `attn ticket inbox --watch`   <-- harness push, content allowed (own queue)
      -> CLI poll loop -> ticket_inbox (CONSUMES cursor) -> prints new events, silent otherwise
  daemon.notifyTicketObservers (SYNC, on event-land) -> Notify(obs, idle, ...):  ticketnotify/notify.go (UNCHANGED)
      unread==0            -> DeliveryNone
      HasSelfMonitor       -> DeliveryWatch      (no synchronous injection — as today)
      idle & !selfmon      -> DeliveryNudge       (immediate doorbell; codex, no competing watch)
      busy & !selfmon      -> DeliveryDeferred
  daemon layer adds (B):  on DeliveryWatch && idle -> scheduleTicketBackstop(session)   ticket_notify.go
    debounced timer (grace >= --watch interval) -> ticketBackstopFire(session):
      still idle && Unread(obs) > 0  -> typeDoorbell        (no live watch drained it -> backstop)
      else                           -> no-op               (watch drained it, or went busy -> self-suppress)
```

## Data Model / Interfaces

No store/schema change. No protocol-version bump for `--watch` (it reuses
`ticket_inbox`). `ticketnotify.Notify` is **unchanged** — B lives entirely in the
daemon layer as a deferred re-check; the only new CLI surface is `--watch`.

```go
// B: daemon-side deferred backstop (internal/daemon/ticket_notify.go).
// notifyTicketSession already runs Notify on event-land AND on idle-transition
// (notifyTicketSessionWentIdle). Add: when the decision is DeliveryWatch for an
// idle session, don't trust the no-op — schedule a debounced re-check.
func (d *Daemon) notifyTicketSession(sessionID, now) {
    // ... existing: obs, idle, delivery := Notify(...) ...
    if delivery == ticketnotify.DeliveryWatch && idle {
        d.scheduleTicketBackstop(sessionID)   // debounced per session; resets timer
    }
}

const ticketBackstopGrace = ...  // >= ticketWatchInterval + margin; document the coupling

func (d *Daemon) ticketBackstopFire(sessionID) {
    s := d.store.Get(sessionID); if s == nil || !isIdleForNudge(s.State) { return }
    obs := d.ticketObserverForSession(sessionID)
    if n, _ := ticketnotify.Unread(d.store, obs); n == 0 { return }  // a --watch drained it -> suppress
    d.typeDoorbell(sessionID, ticketNudgePrompt)                     // still unread past grace -> backstop
}
```

```text
# A: attn ticket inbox --watch  (cmd/attn/main.go ~:885, the "ticket inbox" flagset)
# A long-running command for the chief's harness Monitor. Reuses the consuming
# ticket_inbox command; the daemon cursor dedups, so the client tracks nothing.
loop:
  resp = client.TicketInbox(sourceSessionID)   # internal/client/client.go:284 (CONSUMES)
  if resp.bundles not empty:
      print one concise line per event: "<ticketID> <from>→<to>  <comment-snippet>"
  sleep(ticketWatchInterval)                    # const, e.g. 3s; document the constant
# Silent when empty -> the harness Monitor only notifies the chief on real change.
```

## Boundaries

- `ChiefGuidance` (system prompt) **owns** the chief's delegate→watch→boundary
  behavior after A2. It is always-on and authoritative.
- `attn ticket inbox --watch` is a thin CLI poll loop over the existing consuming
  `ticket_inbox` command — it owns no dedup state (the daemon cursor does) and is
  a **different transport** from B's daemon doorbell; they never overlap.
- `ticketnotify.Notify` stays pure and **unchanged** (no session/Monitor/timer
  knowledge); its `DeliveryWatch` no-op is correct as-is. B is owned entirely by
  the **daemon** layer (`ticket_notify.go`), which holds the session state, the
  Nudger, and the timer — it layers a deferred re-check on top of `DeliveryWatch`.
- The skill keeps only the **delegated-agent** ticket-reporting protocol; chief
  coordination guidance no longer lives there.

## Implementation Steps

### A — Guidance (A2 consolidation)
- [x] `internal/hooks/hooks.go`: replace the single delegation bullet
  (`ChiefGuidance` ~`:139`) with the two bullets below (mechanic + boundary).
  Drop the dead pointer at `:146` to read `load the attn skill's notebook
  reference` (no more chief-of-staff ref).
- [x] `internal/agent/attn_skill/references/chief-of-staff.md`: delete the
  "As Chief of Staff" section (`:6-50`, ported into the system prompt). The
  remaining "As a Delegated Agent" half is worker-facing → rename the file to
  `delegated-agent.md` (or fold into `tickets.md`).
- [x] `internal/agent/attn_skill/SKILL.md:27-28`: reword the route to
  worker-facing "Report your work state as a delegated agent (tickets)" pointing
  at the renamed reference.
- [x] `internal/agent/attn_skill_test.go`: update the embedded-file-list
  assertion that names `references/chief-of-staff.md` to the renamed file.
- [x] Verify the `chief-of-staff` mentions in `references/notebook.md` are
  role-prose ("chief-of-staff altitude"), not a file link — confirm there's no
  `[...](chief-of-staff.md)` to repoint. (Grep `rg chief-of-staff
  internal/agent/attn_skill` before/after.)
- [x] `internal/agent/attn_skill/references/delegation.md:18`: delete "does not…
  require you to monitor the new agent" (now false). Keep the native-subagent
  boundary and the mechanics (brief/placement/worktree).
- [x] `internal/hooks/hooks_test.go`: update the `:335` pointer assertion
  ("notebook and chief-of-staff references" → "notebook reference"); add a
  `TestChiefGuidance` assertion that the Monitor / `attn ticket inbox` trigger is
  present.

Proposed `ChiefGuidance` bullets:

```
- Delegation hands work off — it doesn't block you. When you delegate, attn opens a
  tracked ticket bound to that session and moves it across a board (Working, Blocked,
  In Review, Done, Failed, Crashed) as the agent self-reports. Right after delegating,
  arm a harness Monitor running `attn ticket inbox --watch` so a ticket's state-changes
  push to you the moment they land instead of you polling the board. Record the
  delegation in the journal, report back to Victor, and your turn is done until a
  watched ticket pushes or Victor re-engages you.
- When a watched ticket comes back — ready for review, blocked, needs input, failed, or
  crashed — your job is awareness and upkeep, not independent action. Surface to Victor
  what the agent reported, against what the brief asked and where the artifact landed,
  with a recommended next step, and keep the journal and board current. Present a
  technical status as the agent's claim, not confirmed: you don't validate specialist
  work (code, designs, implementations) or drive recovery — reviewing it and deciding to
  re-delegate, take over, or drop the thread are Victor's calls. The exception is a
  deliverable that is itself prose — a doc, report, or knowledge note — which is yours to
  review on the merits (think Alfred: he proofreads the correspondence, he doesn't sign
  off on the rebuilt engine). Act on your own only on the small and reversible — answer a
  trivial blocker, nudge a stuck agent once — and never leave a thread parked.
```

### A — `attn ticket inbox --watch`
- [x] `cmd/attn/main.go` (~`:885`, ticket inbox flagset): add `--watch` (+
  optional `--interval`). When set, run the poll loop above against
  `internal/client/client.go`'s `TicketInbox`; print a concise line per new
  event; silent when empty; exit cleanly on SIGINT/SIGTERM (the harness stops the
  Monitor on session end). Document the interval constant.
- [x] Confirm `--watch` scopes to the caller via `ATTN_SESSION_ID`
  (`source_session_id` is already required by `handleTicketInbox`).

### B — Daemon backstop (daemon-layer only; `ticketnotify` untouched)
- [x] `internal/daemon/ticket_notify.go`: in `notifyTicketSession`, when the
  decision is `DeliveryWatch` and the session is idle, call
  `scheduleTicketBackstop(sessionID)`. Add `scheduleTicketBackstop` (debounced
  per-session timer, reset on each call) + `ticketBackstopFire` (doorbell iff
  still idle and still unread). Add the `ticketBackstopGrace` constant and
  document its coupling to `--watch`'s interval.
- [x] Reuse the daemon's existing debounce/timer machinery (precedent:
  `notebook_narration.go` / the workspace-keeper debounced runner) and its test
  clock, rather than a raw `time.AfterFunc`, so the timer is testable.
- [x] `internal/daemon/ticket_notify_test.go`: KEEP
  `TestNotifyDoesNotNudgeClaudeObserver` (`:77`) — the **synchronous** path still
  must not doorbell a self-monitor. ADD a test that drives the clock past the
  grace and asserts: (a) an idle self-monitor with persistent unread **is**
  doorbelled by the backstop; (b) if the unread is consumed (simulating `--watch`)
  before the grace, the backstop **does not** fire. Codex immediate-nudge and
  busy-defer tests are unchanged.

### Cross-cutting
- [x] `CHANGELOG.md`: one user-facing bullet — chief now gets pushed updates when
  delegated tickets change state.
- [x] Scrub the stale "arm a Monitor on your dispatch inbox" convention from
  delegation briefs/docs (the dispatch mailbox was retired); the inbox is the
  ticket inbox. (Memory note also.)

## Real-agent verification (harness benchmark)

A and B are verified against REAL agents via
`app/scripts/real-app-harness/scenario-chief-ticket-watch.mjs` on the isolated
**uat** profile (create-as-chief → human delegation prompt → observe; never coached,
never told it is a test). Two benchmark-support configs (below) let the chief run
unattended on a known model. Status:

- [x] **A — Opus, watch path:** Opus 4.8 chief delegates via `attn delegate`, arms
  `attn ticket inbox --watch` in a harness Monitor ("1 monitor", "Allowed by auto
  mode"), and reacts to the worker's `ready_for_review`. The full intended loop.
- [x] **B — Opus, no-Monitor backstop** (`--no-watch` mode): a chief told NOT to arm
  a Monitor delegates, goes idle with no watch, and the daemon doorbells it ("📋 New
  ticket activity…") after the grace; the chief reacts. Verdict
  `backstop-nudged-and-reacted`. (Also confirms the doorbell's type→Enter submits.)
- [x] **Model dependence (finding, accepted):** Sonnet 4.6 did the audit ITSELF
  inline (no delegation) on the same guidance/prompt. The chief is expected to run on
  an Opus-class model; not chasing Sonnet. (Effort confound: attn does not pin effort,
  so Sonnet ran high / Opus xhigh.)
- [x] **Codex — nudge path:** codex chief (gpt-5.5 low) delegates via `attn delegate
  --brief-file …`, and the daemon's DIRECT `DeliveryNudge` doorbell ("📋 New ticket
  activity…") reaches it the instant it is idle with unread; codex genuinely reacts
  ("Ticket activity came in. I'll read the inbox…"). Validate codex with the idle-first
  flow (`--no-watch`), NOT the normal path: the normal path fires the event ~2s after
  delegation while codex is still busy → the nudge defers and the loose react heuristic
  false-matches codex's own delegation narration. codex's nudge is idle-gated, so the
  event must be fired after codex goes idle.
- [x] **Finding — system-prompt vs human conflict (observed once each; confounded):** told
  "don't set up any watch", Claude/Opus(xhigh) OBEYED the human (idle, no Monitor → backstop
  path); codex(low) OBEYED the ChiefGuidance and armed `attn ticket inbox --watch` anyway,
  flagging the conflict ("despite your preference not to watch, the chief-of-staff policy
  required arming…"). n=1 per agent and effort differs (xhigh vs low), so this could be
  capability/effort rather than a stable agent trait — don't over-read it. codex still also
  gets the daemon DIRECT nudge (the daemon's capability model treats codex as non-self-monitor
  regardless), so it is double-covered. **Open product question for Victor:** should "arm a
  Monitor" be a hard ChiefGuidance rule or a human-overridable default?

## Benchmark-support configs (folded into this PR)

Two daemon settings thread setting → `SpawnOptions` → worker env → `agent.SpawnOpts`
→ driver, mirroring the `workflows_enabled` pattern (the worker backend is production,
so the env hop is what runs). Both default off/empty; yolo overrides auto-approve.

- **`auto_approve_enabled`** (global bool) → Claude `--permission-mode auto`; Codex
  `-c approval_policy="on-request" -c approvals_reviewer="auto_review"`. Lets the chief
  run unattended without stalling on approval gates. Env hop `ATTN_AUTO_APPROVE`.
  - [x] setting + validation (ws_settings.go); ws_pty + reload read; worker.go env; cmd/attn read; SpawnOpts field; claude `--permission-mode auto`; codex `approval_policy`+`approvals_reviewer`
  - [x] codex combo confirmed vs [official docs](https://developers.openai.com/codex/concepts/sandboxing/auto-review): `approvals_reviewer = "auto_review"` requires `approval_policy = "on-request"` (or a granular interactive policy) — exactly the pairing we wire; it routes escalations to a reviewer agent (the reviewer can still deny). Keys are NOT surfaced in `codex --help`/`review --help`/`doctor`, so CLI introspection can't find them — trust the docs (`approval_policy` is separately visible via `codex doctor` → "approval policy OnRequest"). Benchmark: codex ran unattended through the scenario; the reviewer didn't visibly fire in panes (nothing needed approval under the sandbox) — expected, config is correct.
  - [ ] SettingsModal UI toggle + unit tests
- **`chief_model_<agent>`** (per-agent string, chief-gated) → `--model <alias>` (e.g.
  `opus`). Empty = agent default. Env hop `ATTN_CHIEF_MODEL`; read only for chief
  launches (`chiefLaunchModel`).
  - [x] setting + validation; ws_pty chief-gated read; worker.go env; cmd/attn read; SpawnOpts field; claude + codex `--model`
  - [ ] SettingsModal UI toggle (per-agent) + unit tests
- [ ] **CHANGELOG** entry covering both new settings
- Real-app gotcha: the source-fingerprint guard compares the app bundle's baked
  fingerprint (`get_state.appBuild`) against the working tree, so Go/frontend edits
  need a full `make install PROFILE=uat`; harness scripts are excluded (edit + re-run
  free).

## Decisions

- **A2 over A3 (consolidate into the system prompt).** Token cost isn't the
  tiebreaker — the prompt is cached, and a missed lazy-loaded trigger costs more
  than always-on tokens; the chief loads the chief reference on most turns
  anyway. One always-on home, no drift, simpler skill. The full boundary moves
  up; the skill keeps only the delegated-agent reporting half.
- **Add `attn ticket inbox --watch`.** A2 deletes the natural home for the
  silent-unless-changed Monitor recipe; `--watch` makes the guidance trivial
  ("arm a Monitor running `attn ticket inbox --watch`") and robust, instead of
  the chief hand-rolling a jq diff loop.
- **B is deferred, not event-synchronous — and daemon-only.** `notifyTicketObservers`
  fires the instant an event lands, before `--watch` polls, so a synchronous
  "idle self-monitor → doorbell" would double-fire on *every* event for a
  watching chief. Instead the daemon schedules a debounced re-check after a grace
  ≥ the `--watch` interval and doorbells only if *still* unread. The daemon can't
  detect whether a Monitor is armed, so it infers it from persistence: a live
  watch drains within its interval → re-check sees 0 → suppress; no watch → still
  unread → backstop fires. `ticketnotify.Notify` stays pure/unchanged; the
  `DeliveryWatch` no-op is still correct synchronously.
- **The grace and the `--watch` interval are coupled constants.** Grace must
  exceed the poll interval (+ round-trip margin) or a live watch won't have
  drained in time and the backstop double-fires. Document both where they're
  defined.
- **`--watch` reuses `ticket_inbox` (no protocol bump).** Client-side poll loop;
  the daemon cursor does the dedup, so the client tracks no state.
- **Backstop uses a real `time.AfterFunc` + grace override, not a fake clock.** The
  daemon has no injectable clock on this path, so the timer fires on wall-clock. A
  `*time.Timer` identity guard (`ticketBackstopFire(sessionID, self)`) makes a fire
  that lost a reschedule race bail, which is what keeps the debounce to one doorbell;
  a `ready` channel publishes the timer handle before the closure reads it (the
  AfterFunc captures the very variable being assigned, a real data race otherwise —
  caught under `-race`). Tests pick the determinism they need: a 1h grace + manual
  `ticketBackstopFire` for the debounce/stale-timer invariants, a tiny grace + poll
  for the end-to-end "a scheduled timer actually fires" path.
- **The headline backstop test models the chief, not just any self-monitor.** The
  reported bug is *agent reports → idle Claude chief never notices*. The harness
  delegation source spawns as a shell, so `makeSelfMonitor` flips the chief's stored
  agent to Claude and `callSetTicketStatus` (the delegate reporting ready-for-review)
  drives the real producer — proving the chief gets backstopped, while the reporting
  agent is never doorbelled about its own change. Generality (any delegated Claude,
  via a human comment) is covered by sibling tests; the mechanism keys on
  `HasSelfMonitor`, not on being the chief.
- **`attn ticket inbox --watch` reports a daemon outage once per outage, not every
  poll.** A wrapping Monitor treats each printed line as fresh activity, so repeating
  an unchanged error would nudge the chief every interval. The poll loop lives in a
  testable `watchTicketInbox` (injected fetch + writers) so the dedup is covered
  without a daemon, signals, or a real ticker.

## Verification

- `go test ./internal/ticketnotify/... ./internal/daemon/... ./internal/hooks/...
  ./internal/agent/...` green; `go build ./...` clean. The backstop test drives
  the daemon's test clock past `ticketBackstopGrace` (don't rely on wall-clock).
- Manual on the **dev** install (`make dev`): chief delegates → arms
  `attn ticket inbox --watch` Monitor → delegated agent reports
  `ready_for_review` → chief receives a pushed line while still working, and
  **no** stray doorbell after the grace (watch drained → backstop suppresses).
  Separately, a chief with **no** Monitor armed, left idle, gets the B doorbell a
  few seconds after a ticket changes.

## Follow-ups

- **A4 (deferred):** have `attn delegate` emit a one-line "arm a Monitor" reminder
  on success — contextual and agent-general, but overlaps this PR's surfaces;
  revisit only if guidance proves insufficient.
- Consider a true daemon push for `--watch` (event subscription) if poll latency
  is ever felt; the poll loop is the pragmatic v1.
