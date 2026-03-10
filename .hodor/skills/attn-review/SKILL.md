---
name: attn-review
description: Review guidance for attn pull requests. Focus on correctness, protocol safety, generated files, session-state behavior, and cross-daemon/app compatibility.
---

## Review priorities
- Prioritize correctness, regressions, state-machine bugs, protocol mismatches, and workflow breakage.
- Ignore style nits unless they hide a real maintenance or behavior issue.
- Treat automated review findings as advisory, not merge blockers by default.

## Protocol and compatibility
- Any protocol change must bump `ProtocolVersion` in `internal/protocol/constants.go`.
- Watch for app/daemon compatibility regressions, especially daemon background-process upgrade paths.
- Prefer additive protocol changes when possible.

## Generated files
- Never hand-edit generated protocol files.
- If `internal/protocol/schema/main.tsp` changes, expect regenerated outputs and consistency across Go + TypeScript consumers.

## Review-comment and PR features
- When review-comment data structures change, verify all consumers are updated: store, daemon handlers, frontend hooks/UI, and reviewer/MCP paths.
- Check GitHub/PR workflows for auth assumptions, fork safety, and advisory-vs-blocking behavior.

## Session and PTY behavior
- Be extra careful around session state transitions (`launching`, `working`, `pending_approval`, `waiting_input`, `idle`, `unknown`).
- Watch for reconnect, recovery, classifier races, stale timestamps, and full-screen PTY restore regressions.
- For terminal or renderer changes, prefer deterministic behavior/harness coverage over brittle snapshot expectations.

## Logging and diagnostics
- Daemon code should use daemon-wired logging rather than stray stderr logging.
- Favor actionable diagnostics when a failure can leave the app in a degraded but still-running state.
