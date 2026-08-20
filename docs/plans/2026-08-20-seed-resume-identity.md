# Plan: Seed-owned resume identity

## Goal

A seed can reopen an agent conversation even when attn has no dispatch record for it. The identity is set atomically at plant time or later, remains visible on seed reads, and can be cleared.

## Architecture Map

```diff
 attn seed plant / set-resume
   -> seed_plant / seed_set_resume
     -> garden.Seed document
+      {resume_session_id, resume_cwd, resume_agent}

 app Resume button
   -> seed_resume
     -> resumeSeed
-      -> dispatch {session, cwd, agent, resume}
+      -> dispatch {session, cwd, agent, resume}
+         OR seed-owned resume identity
       -> handleSpawnSession
         -> agent driver (claude / copilot)
```

Tests drive the daemon handlers with the real document store and a fake PTY backend. Live verification uses an isolated profile and real dead Claude and Copilot conversations.

## Data Model / Interfaces

```text
Seed {
  resume_session_id?: agent-native conversation id
  resume_cwd?:        launch directory
  resume_agent?:      configured agent name
}

seed plant "title" --resume-session-id ID --cwd DIR --agent NAME
seed set-resume SEED --resume-session-id ID --cwd DIR --agent NAME
seed set-resume SEED --clear
```

The three values are one identity: callers either supply all three or clear all three. A seed without a tender uses its agent-native resume id as the new attn session id, matching the old ticket fallback's ability to mint/recover a container without a surviving assignment.

## Implementation Steps

- [x] Add the seed fields, wire messages, protocol bump, and generated types.
- [x] Add atomic plant/set/clear CLI and daemon paths with storage tests.
- [x] Fall back to seed identity in `resumeSeed`; preserve dispatch precedence and loud refusals.
- [x] Cover Claude and Copilot fallback, missing identity, and missing cwd path.
- [x] Run targeted/full tests and Linux build checks.
- [x] Verify CLI-set and app-button resumes in an isolated profile.
- [ ] Open the PR into `main`, request figgyster, and wait for green CI.

## Decisions

- Dispatch remains authoritative when present. Seed fields are a fallback, so existing delegated sessions keep identical behavior.
- `set-resume` is separate from `edit`: markdown editing and launch identity are different mutations, and neither should accidentally clear the other.
- `--clear` is the way out; partial identities are refused before a write.

## Follow-ups

- Copilot's fork-on-resume session-state behavior remains unchanged.
- Replanting crashed ticket backlog remains separate work (`s-wj755q`).
