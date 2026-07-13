# Plan: Codex nudge submission

## Why / Alignment

An attn nudge is a submitted doorbell message, not composer text. This chunk
restores that contract for Codex while preserving the recently added approval
fence, countdown deduplication, busy-session delivery policy, and user-input
guards.

In scope: the shared doorbell transport, focused daemon coverage, the packaged
Codex nudge scenario, and a user-facing changelog entry. Deferred: redesigning
nudge eligibility, countdown timing, composer introspection, or agent-specific
plugin contracts.

## Architecture Map

```text
Current:
ticket event -> countdown / deliver-now -> typeDoorbell
  -> one PTY write: prompt + CR
    -> Codex treats the burst as composer input; no turn starts

Target:
ticket event -> countdown / deliver-now -> typeDoorbell (approval fence held)
  -> one PTY write: bracketed-paste(prompt) + CR
    -> explicit paste terminator separates prompt from Enter semantically
    -> Codex submits one user message and starts a turn

Tests:
daemon fake backend -> assert atomic framed write + state fence
packaged attn-dev.app -> real Codex -> assert working transition + settled prompt
```

## Boundaries

- `typeDoorbell` owns one atomic prompt-and-submit write under `doorbellMu`.
- `applyStateAndSyncNudge` remains the authority that prevents an approval state
  from landing inside that transaction.
- Countdown, unread, selection, and recent-keystroke policy remain unchanged.
- The real-app scenario must distinguish “text appeared” from “Codex started a
  turn.”

## Implementation Steps

- [x] Reproduce the stranded-composer regression on signed current `main` with a
  real Codex session.
- [x] Restore distinct prompt and Enter semantics inside one approval-fenced PTY
  write.
- [x] Update daemon tests to assert atomic framing, non-interleaving, and no
  duplicate submission.
- [x] Strengthen the packaged-app scenario to require a real Codex turn and an
  empty composer after settling.
- [x] Run focused/static/full relevant checks and live verification.
- [x] Run the repository review workflow and prepare a review-ready handoff.

## Decisions

- Keep the fix in the existing shared doorbell primitive: the regression was
  introduced there, and the previous separated-write behavior was shared across
  runtimes.
- Preserve the atomic state fence and use explicit bracketed-paste framing to
  separate the composer text from Enter without a timing window.
- Treat the current harness pass as a regression in the assertion: pane text alone
  proves injection, not submission.
