# Nudge delivery review remediation

## Why

Ticket delivery must remain runtime- and role-neutral without risking an
approval response. Done means every state transition uses the shared
`pending_approval` safety rule, a countdown cannot append Enter after approval
begins, and the real-app and direct-doorbell tests prove the intended surface.

## Aligned on

- Plugin-owned sessions are ordinary nudge targets, so their authoritative state
  reports must reconcile deferred unread activity just like built-in runtimes.
- The approval boundary applies through the whole doorbell write, not only at
  countdown-fire time.
- Optional Claude watching is one valid consumer outcome, never a harness
  precondition or delivery gate.

## In scope / deferred

In scope: the five review findings on PR #531, their focused tests, and stale
behavioral wording. Deferred: redesigning nudge UX, countdown timing, or inbox
semantics beyond what is required to make the existing contract safe and
observable.

## Vision

Advances [chief delegation awareness](../vision/chief-delegation-awareness.md):
the daemon provides a bounded doorbell while durable ticket state remains the
source of truth.
