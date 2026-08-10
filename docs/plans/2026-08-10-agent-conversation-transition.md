# Plan: Agent conversation transition

## Goal

Keep one attn session correctly bound when its running agent starts a new native
conversation, beginning with Codex `/new`. The agent's `SessionStart` hook is the
authoritative observation: no terminal parsing, filesystem inference, or
pre-session fallback.

An attn session stays the same session. Its **agent conversation** changes, and
transcript-scoped state moves with that binding.

## Architecture Map

```text
Current:
SessionStart {native session id}
  -> set_session_resume_id
    -> overwrite sessions.resume_session_id + ticket mirror
      -> old transcript watcher and activity cursor remain live

Target:
SessionStart {native session id, transcript path}
  -> observeAgentConversation(attn session id, native id, exact path)
    -> same native id: no-op
    -> changed native id:
       1. commit canonical binding + ticket mirror + activity reset
       2. reset transcript-scoped in-memory state
       3. publish session.conversation.changed
            -> wire projection re-pushes the session
            -> watcher reaction binds to the exact new transcript

Restart:
persisted native id
  -> transcript finder resolves the exact conversation
    -> watcher reconstructs without event replay
```

## Data Model / Interfaces

The existing `sessions.resume_session_id` remains the canonical native
conversation id. The ticket copy remains the durable resume target after the
session row is removed.

```go
type AgentConversationObservation struct {
    SessionID     string // stable attn session id
    NativeID      string // provider-owned conversation id
    TranscriptPath string // exact SessionStart path; runtime hint
}

// Store transaction. `changed` is false for repeated observations.
TransitionSessionConversation(sessionID, nativeID string) (changed bool, err error)
```

The transaction updates the session binding, mirrors the ticket binding, and
clears `activity`, `activity_at`, and `activity_cursor`. The transcript path is
not a new source of truth: the hook-provided path binds the live watcher, while
restart recovery resolves the persisted native id through the agent's transcript
finder.

Bus fact:

```text
name:    session.conversation.changed
subject: stable attn session id
payload: exact transcript path (transient runtime hint)
```

`set_session_resume_id` gains optional `transcript_path`; this is a protocol
shape change and requires generated Go/TypeScript types plus a protocol bump.

## Boundaries

- `SessionStart` observes the provider fact. It does not decide whether the
  observation is a transition.
- The daemon owns comparison, orchestration, and publication after commit.
- The store owns the atomic durable invariants.
- The transcript watcher is the only long-lived runtime consumer that must
  rebind. Point-in-time consumers continue reading the canonical id on demand.
- `wireProjections` only pushes committed state to clients; it performs no
  mutation and publishes no further facts.

## Implementation Steps

- [x] Route `SessionStart`'s native id and transcript path through one daemon
  conversation-observation operation; repeated observations are no-ops.
- [x] Add the atomic store transition and cover binding, ticket mirror, and
  activity reset.
- [x] Publish `session.conversation.changed` after commit and project the
  refreshed session to clients.
- [x] Rebind the transcript watcher to the exact new transcript and make daemon
  restart resolution prefer the persisted native id.
- [x] Remove native-id syncing from later hooks so `SessionStart` is the single
  transition signal.
- [x] Add regression coverage for `/new`: old rollout still exists, new binding
  wins, activity/cursor reset, watcher moves, reload resumes the new id.
- [x] Define **agent conversation** in the glossary, add a changelog fragment,
  regenerate protocol types, and bump the protocol version.
- [x] Run focused Go/frontend tests, then verify the transition in an isolated
  packaged app profile.

## Decisions

- This is event-driven state propagation, not event sourcing. SQLite remains
  canonical and runtime state is reconstructable after restart.
- Correctness invariants commit together; they are not eventually consistent
  subscribers.
- The fact is provider-neutral because successive native conversations are an
  attn session concept, even though Codex `/new` exposed the missing transition.
- A changed native id is the transition identity; no separate generation counter
  is needed.
- The existing transport command keeps its name. First-class semantics live in
  the daemon transition and domain fact, avoiding an unrelated public-wire rename.

## Verification

- Store, client, command, and full daemon Go test suites pass.
- Frontend typechecking and all frontend tests pass.
- Protocol generation completed and protocol version 222 passed packaged-app
  preflight.
- The real Codex packaged-app scenario observed `/new` move one attn session
  from native id `A` to `B`, bind the watcher to `B` while `A` still existed,
  and preserve `B` after reload.
