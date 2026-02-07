# Tasks

## DB migration failure can silently drop persistence

- Status: `open`
- Priority: `high`
- Area: `daemon/store`, `tauri UX`

### Problem

Daemon startup can fail to open `~/.attn/attn.db` during migrations and silently fall back to in-memory storage.

Observed log:

`Failed to open DB at /Users/.../.attn/attn.db: migration 20 (add host to prs and migrate ids): duplicate column name: host (using in-memory)`

### User impact

- Sessions are not persisted across daemon restarts.
- After app close/reopen, previously running sessions may not appear restored.
- UI does not clearly explain that persistence is degraded.

### Context captured from this branch

- Migration `20` was made idempotent to avoid one known duplicate-column failure mode.
- Reconnect behavior and stale socket handling were improved separately.
- Even with this fix, broader migration failure handling/UX still needs dedicated follow-up.

### Follow-up options for next PR

1. Harden migration execution:
- Make all non-trivial migrations idempotent or preflight-check columns/indexes.
- Add migration failure classification (`recoverable` vs `fatal`).

2. Improve user-facing guidance:
- Emit a daemon warning event when persistence falls back to in-memory.
- Show a persistent banner in app with remediation steps.
- Include exact DB path and failure message in UI.

3. Add a recovery workflow:
- Add a `doctor`/`repair` command (or equivalent maintenance action) to validate and repair schema metadata safely.
- Optionally backup and recreate DB when schema is unrecoverable.

4. Define strict policy:
- Decide whether daemon should refuse startup on DB migration failure (instead of silent degraded mode), or continue in degraded mode with explicit warning + guidance.

### Acceptance criteria (next PR)

- User can clearly see when daemon is running without durable persistence.
- Guidance includes concrete recovery steps.
- Migration failure path is covered by automated tests.
- Session restoration behavior is deterministic after app restart.
