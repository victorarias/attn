# Session instructions first slice

## Why

Allow an attn agent to ask one free-form question about a Codex session and
receive a concise, evidence-backed answer without introducing an authorization
ledger or workflow-specific policy.

## Aligned on

- The scoped implementation spec is the build contract. Native Codex
  `user_message` and `agent_message` records are the v1 evidence corpus.
- The daemon must resolve the stored native Codex resume ID to one transcript
  snapshot, fingerprint it before parsing, and validate all displayed excerpts
  against that snapshot.
- `Unclear` is a successful answer; transcript, model, and validation failures
  return no verdict and a stable non-zero failure.
- Assistant turns may supply context but cannot independently support a claim
  about the user's instructions.

## In scope / deferred

This slice adds the read-only CLI, Unix-socket command, Codex projection,
Luna low/medium retry, exact evidence validation, and focused tests. Claude,
settings, retrieval for long conversations, and any durable provenance or
authorization model are deferred.
