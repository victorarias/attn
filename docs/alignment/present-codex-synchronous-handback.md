# Synchronous Present handback for Codex

## Why

Codex currently ends its turn after opening a Present review, so feedback has
to wake the session through a nudge. That wake-up is intentionally suppressed
while the user is watching the session, which makes the normal Present workflow
stall. Done means Codex keeps the Present command in the foreground and receives
the review outcome as that command's result.

## Aligned on

- The built-in Present guidance tells Codex to run `attn present --wait` for
  every round and treat its stdout as the review handback.
- The packaged-app Present scenario exercises the same synchronous lifecycle:
  start the waiting CLI, submit the round, then assert that the CLI returns the
  submitted feedback.
- Closing a presentation remains a terminal result of the waiting command.

## In scope / deferred

This chunk changes the embedded agent guidance and the existing real-app
Present harness. It does not change nudge delivery, submission semantics, or
the optional non-waiting CLI surface used by humans and other integrations.

## Vision

This advances the rounds loop in [the Present vision](../vision/present.md): the
reviewer hands a round back and the presenting agent can respond in the same
live workflow.
