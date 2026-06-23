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
_Stub — hand to plan-doc-write to decompose into PR-sized steps with sequencing._
