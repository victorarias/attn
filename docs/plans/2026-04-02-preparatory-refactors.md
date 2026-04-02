# Preparatory Refactors Plan

Date: 2026-04-02
Status: In Progress
Owner: daemon/frontend

## Summary

This document isolates the preparatory refactorings into standalone work items.

Each item should be implemented as a clean PR.

## Implementation Status

- Done: 0. Extract command routing in websocket.go
- Done: 1. Add compiled-in version string
- Pending: 2. Add linux targets to release workflow
- Done: 3. Add `endpoint_id` to Session model
- Done: 4. Abstract the frontend WS URL
- Dropped: 5. Introduce major.minor protocol versioning
- Done: 6. Auto-restart local daemon on protocol version mismatch

## Refactorings

### 0. Extract command routing in websocket.go (done)

The `handleClientMessage` switch in `websocket.go` has 62 case arms across ~430 lines, with many handlers implemented inline. This makes it hard to reason about command routing and command handling by scope.

**Approach: Annotated switch + domain files + scope metadata (Option C)**

1. **Thin the switch** — Every case becomes a 1-line delegation to a method in a domain-specific file. This is already the pattern for `branch.go`, `stash.go`, and `worktree.go` — just finish the job for the remaining domains.

2. **Create domain files** — Move inline handler implementations out of `websocket.go`:

   | New file | Commands moved | Approx cases |
   |---|---|---|
   | `ws_pr.go` | approve, merge, mute_pr, mute_repo, mute_author, refresh, fetch_details, pr_visited, inject_test_pr, collapse, query_prs/repos/authors | ~13 |
   | `ws_session.go` | session_visualized, clear_sessions, clear_warnings, unregister, mute, get_recent_locations, inject_test_session | ~7 |
   | `ws_settings.go` | get_settings, set_setting + all validate* funcs | ~2 cases + 120 lines |
   | `ws_pty.go` | spawn, attach, detach, input, resize, kill + forwardPTYStreamEvents | ~6 cases + 140 lines |
   | `ws_git.go` | subscribe/unsubscribe git_status, get_file_diff, get_branch_diff_files + sendGitStatusUpdate | ~4 cases + 140 lines |
   | `ws_review.go` | review_state, mark_file_viewed, add/update/resolve/wontfix/delete comment, get_comments, start/cancel review | ~10 cases + 180 lines |

   Already done: `branch.go` (~5 cases), `stash.go` (~5 cases), `worktree.go` (~4 cases).

   Result: `websocket.go` shrinks from 1774 to ~400 lines (switch routing table + WS infrastructure).

3. **Add `command_meta.go`** — A scope registry that classifies every command:

   ```go
   type CommandScope int
   const (
       ScopeSession  CommandScope = iota  // forward to owning endpoint
       ScopeEndpoint                       // forward to target endpoint
       ScopeHubLocal                       // handle on hub only
       ScopeHubMerge                       // merge results from all endpoints
   )

   var CommandMeta = map[string]CommandScope{
       protocol.CmdPtyInput:      ScopeSession,
       protocol.CmdClearSessions: ScopeHubLocal,
       protocol.CmdQueryPRs:      ScopeHubLocal,
       // ... all 60+ commands classified
   }
   ```

   This provides a single place to reason about command scope: `scope := CommandMeta[cmd]`.

**Exit gate:** All existing tests pass. `websocket.go` contains only WS infrastructure and the thin switch. Every command has a scope classification in `command_meta.go`.

### 1. Add compiled-in version string (done)

**This is a hard prerequisite for binary bootstrap and auto-update flows.** Nothing in those flows works without it.

Currently no version string exists anywhere: no `-ldflags`, no `--version` subcommand, not in Makefile, Formula, or release workflow.

Changes needed:
- Add `var version = "dev"` in `cmd/attn/main.go`
- Add `attn --version` subcommand that prints the version
- Add `-ldflags "-X main.version=$(VERSION)"` to Makefile `build` and `install` targets
- Update `Formula/attn.rb` to pass ldflags: `system "go", "build", *std_go_args(ldflags: "-X main.version=#{version}"), "./cmd/attn"`
- Update `.github/workflows/release.yml` to include ldflags in Go build step
- Version is derived from the git tag in CI, from `package.json` or Makefile var locally

### 2. Add linux targets to release workflow

The current release workflow only produces `darwin/arm64` and is tightly coupled to the Tauri build action (which creates the GitHub release). Adding Linux requires careful coordination:

1. Add a `linux-binaries` job that runs on `ubuntu-latest` (or in the existing `tauri` job after the Tauri action creates the release)
2. Build `GOOS=linux GOARCH=amd64` and `GOOS=linux GOARCH=arm64` with ldflags
3. Upload binaries via `gh release upload $TAG attn-linux-amd64 attn-linux-arm64 --clobber`
4. **CGO consideration:** Check if the SQLite dependency requires CGO. If so, either use `CGO_ENABLED=0` with a pure-Go SQLite driver, or set up cross-compilation toolchain. Test that the resulting binary runs correctly.
5. Name binaries consistently: `attn-linux-amd64`, `attn-linux-arm64`

### 3. Add `endpoint_id` to Session model (done)

Additive field in TypeSpec, regenerate. Frontend ignores it until used. Store can persist it.

### 4. Abstract the frontend WS URL (done)

Currently hardcoded to `ws://127.0.0.1:${port}/ws`. Make it configurable via settings or an endpoint profile.

### 5. ~~Introduce major.minor protocol versioning~~ (DROPPED)

No longer needed. Exact version matching plus automatic update removes the compatibility window that major.minor versioning was meant to solve.

### 6. Auto-restart local daemon on protocol version mismatch (done)

**Problem:** When a user upgrades the app (or runs `make install`), the old daemon process keeps running in the background. The app connects, receives `initial_state` with the old protocol version, detects mismatch, shows a red error banner ("New daemon version available. Restart when ready..."), opens the circuit breaker, and dead-ends. The user must manually restart the daemon. This generates support requests and is a bad UX for something the app can handle itself.

**Current behavior:**
1. App connects to daemon via WebSocket.
2. Receives `initial_state` with `protocol_version`.
3. Detects mismatch (daemon version < client version).
4. Shows red banner, opens circuit breaker — dead end requiring manual intervention.

**Desired behavior:**
1. App connects, detects old daemon.
2. App calls `restart_daemon` Tauri command.
3. Old daemon is killed, new daemon started.
4. App reconnects automatically.

**Implemented adjustment:** Before restarting, Tauri resolves the daemon binary it would launch and runs `attn --protocol-version`. If that reported protocol does not match the app's expected protocol, restart fails closed and the existing mismatch banner remains. This avoids restart churn when the resolved binary is itself stale.

**Changes — Rust (`app/src-tauri/src/lib.rs`):**
1. Add `restart_daemon` Tauri command:
   - Read PID from `~/.attn/attn.pid`.
   - Send SIGTERM to the old process (with safety checks: not self, not parent).
   - Wait for socket to go away (up to 5s polling).
   - Resolve the daemon binary first and preflight it with `attn --protocol-version`.
   - Start new daemon using existing binary resolution logic only if the protocol matches the app.
2. Extract shared helpers (`resolve_daemon_binary`, `spawn_daemon`, `resolve_prefer_local`) from `start_daemon` to avoid duplication.

**Changes — Frontend (`app/src/hooks/useDaemonSocket.ts`):**
1. When version mismatch detected and `daemonVersion < clientVersion`:
   - Log that auto-restart is happening.
   - Call `invoke('restart_daemon', { prefer_local, expected_protocol })` instead of showing error banner.
   - Close WebSocket but do NOT open circuit breaker — let normal reconnection logic handle it.
   - Show a transient info message ("Restarting daemon...") instead of the red error banner.
2. If `restart_daemon` fails, fall back to the current error banner behavior.
3. The case where `daemonVersion >= clientVersion` (app is old) still shows the existing error banner — app can't fix itself.

**Why this matters:**
This validates the kill→wait→start→reconnect cycle and removes the manual restart step from the common "upgraded app, old daemon" case.

**Exit gate:** Upgrade the app while a daemon is running. App auto-restarts the daemon and reconnects without user intervention. No red banner for the common "upgraded app, old daemon" case.
