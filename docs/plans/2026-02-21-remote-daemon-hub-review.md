# Remote Daemon Hub Plan — Review

Reviewed: 2026-02-21  
Reviewer: o3 (codex scout, 3 passes) + cross-verified against codebase

---

## CRITICAL

### 1. No compiled-in version string exists anywhere

**Location:** Plan §A.1, Bootstrap Flow step 2, Auto-update  
**Issue:** The entire bootstrap and auto-update flow depends on `attn --version`, but:
- No `--version` subcommand exists in `cmd/attn/main.go`
- No `-ldflags "-X main.version=..."` in `Makefile`, `Formula/attn.rb`, or `.github/workflows/release.yml`
- The plan lists this as prep refactor §1 ("trivial") but it's a hard prerequisite for Phase A and B — nothing works without it

**Fix:** This must be the very first thing implemented. Add `var version = "dev"` in main.go, `-ldflags` in Makefile/formula/workflow, and `attn --version` subcommand. Not trivial prep — it's a gating dependency.

### 2. Release workflow produces only macOS/arm64 — adding Linux is not "two lines"

**Location:** Plan §A.4, Prep refactor §2  
**Issue:** The current `.github/workflows/release.yml` is tightly coupled to the Tauri build:
- It uses `tauri-apps/tauri-action@v0` which creates the GitHub release
- The Go binary is built only as `darwin/arm64` and placed in `app/src-tauri/binaries/`
- Linux binaries need to be built as separate artifacts and uploaded to the same release
- The release is created by the Tauri action, so linux builds must either run in the same job (after Tauri) or in a separate job that waits for the release to exist
- CGO may be needed for SQLite on Linux (the plan doesn't mention this) — needs `CGO_ENABLED=0` with a pure-Go SQLite driver, or cross-compilation toolchain

**Fix:** Add a separate job (`linux-binaries`) that runs on `ubuntu-latest`, builds `GOOS=linux GOARCH=amd64` and `GOOS=linux GOARCH=arm64`, and uploads via `gh release upload`. Must handle the race with Tauri release creation (use `needs: tauri` or retry loop). Check if the Go SQLite dependency requires CGO.

### 3. `sessions_updated` event sends full session list — hub merge undefined

**Location:** Plan §C (Session visibility), not addressed  
**Issue:** `broadcastSessionsUpdated()` in `daemon.go:2617` sends `d.store.List("")` — the entire session list from the store. When the hub receives this from a remote daemon, it contains only that remote's sessions. But the hub needs to merge them with local sessions and other remote sessions before broadcasting to the UI. The plan says "merges remote sessions" but doesn't specify:
- Where merged sessions are stored (hub's own store? in-memory map?)
- How `sessions_updated` events from remotes are re-broadcast (full list from all endpoints? delta?)
- What happens when two remotes have sessions with the same ID (UUID collision unlikely but possible)

**Fix:** Add a section to the plan specifying the hub's session merge strategy. Recommendation: hub maintains an in-memory `map[endpointID][]Session`, and on any remote `sessions_updated`, rebuilds the full merged list and re-broadcasts to UI clients. Hub's store only persists endpoint config, not remote sessions.

---

## IMPORTANT

### 4. Command scoping audit conflates WS and unix socket commands

**Location:** Plan §8 (Command scoping audit table)  
**Issue:** The scoping table lists `register`, `state`, `stop`, `todos`, `heartbeat` as commands the hub needs to scope. But these come via the unix socket handler (`daemon.go:handleConnection`), NOT via WebSocket. The WS handler has 62 case arms; the unix socket handler has 21. They are separate code paths with different command sets. The hub, as a WS client to remotes, will only see WS commands — it will never need to route unix socket commands.

Commands that are unix-socket-only: `register`, `state`, `stop`, `todos`, `heartbeat`, `query` (the hooks IPC ones). These need no hub routing.

**Fix:** Split the scoping table into "WS commands (hub must route)" and "Unix socket commands (local only, not relevant to hub)". Remove unix-socket-only commands from hub routing considerations.

### 5. `ParseMessage` hard-rejects unknown commands — breaks forward compatibility

**Location:** Plan §11 (Protocol version compatibility)  
**Issue:** `ParseMessage()` in `constants.go` returns `errors.New("unknown command: " + peek.Cmd)` for any command it doesn't recognize. If a newer remote daemon sends a command/event the hub doesn't know about, parsing fails. This directly contradicts the plan's major.minor compatibility model where "minor versions are additive-only (new optional fields, new commands that unknown receivers can ignore)."

**Fix:** Two options: (a) Change `ParseMessage` to return the raw JSON + command string for unknown commands (graceful degradation), or (b) acknowledge that exact version matching is needed and drop the major.minor model. Option (a) is better for the hub use case.

### 6. `sendInitialState` triggers `fetchAllPRDetails()` — wasteful for hub connections

**Location:** Plan §B, not addressed  
**Issue:** When the hub connects as a WS client to a remote daemon, `sendInitialState()` in `websocket.go:294` fires `go d.fetchAllPRDetails()`. This triggers GitHub API calls on the remote machine for every hub reconnect. The hub handles PRs centrally — remote PR fetching is wasteful and may hit rate limits.

**Fix:** Either (a) add a "hub client" flag in the WS handshake that suppresses PR fetching, or (b) accept the waste for v1 since PR polling is idempotent and the extra fetch is just one burst. Document the decision either way.

### 7. `initial_state` sends full PR/repo/author data to hub — unnecessary

**Location:** Plan §B  
**Issue:** `sendInitialState` includes `Prs`, `Repos`, `Authors`, `Settings`, `Warnings` — all irrelevant for hub connections (PRs are hub-local, settings are per-daemon). This wastes bandwidth and creates confusion about authority.

**Fix:** For v1, the hub simply ignores these fields from remote `initial_state`. Document this. For v2, consider a lighter handshake for hub↔remote connections.

### 8. Hub must filter remote broadcast events

**Location:** Not addressed in plan  
**Issue:** Remote daemons broadcast events like `prs_updated`, `repos_updated`, `authors_updated`, `settings_updated` to all WS clients — including the hub. The hub must NOT forward these to the UI (PRs/settings are hub-local). But the plan doesn't specify which events to filter vs forward.

**Fix:** Add an event filtering table (similar to command scoping) specifying which events the hub forwards to UI vs ignores:
- Forward: `session_registered`, `session_state_changed`, `session_removed`, `pty_output`, `pty_desync`, attach/spawn results
- Ignore: `prs_updated`, `repos_updated`, `authors_updated`, `settings_updated`, `warnings_updated`

### 9. No SSH failure handling specified

**Location:** Plan §B (Bootstrap Flow)  
**Issue:** The bootstrap flow assumes SSH "just works." Missing edge cases:
- SSH key not configured / passphrase-protected key (how does a Go daemon handle interactive key prompts?)
- Host key verification (`StrictHostKeyChecking`) — first connection to unknown host
- 2FA / jump hosts / ProxyCommand configurations
- SSH connection timeout
- Permission denied (wrong user, key not authorized)
- Remote machine is not Linux (plan assumes `uname -sm` returns `Linux x86_64` but what about FreeBSD, macOS remotes?)

**Fix:** Add error handling section. At minimum: (a) use `-o BatchMode=yes -o StrictHostKeyChecking=accept-new` for non-interactive SSH, (b) set connection timeout, (c) report specific error messages to UI via `endpoint_status_changed` event with `error` status and descriptive `status_message`, (d) document that SSH agent must be running for key auth.

### 10. `ws-relay` local TCP proxy component not specified

**Location:** Plan "The ws-relay Subcommand" section  
**Issue:** The plan shows the remote-side `ws-relay` (~30 lines, raw TCP bridge) but only mentions the hub-side local TCP proxy in passing. This proxy is a critical component: it bridges the SSH process's stdin/stdout to a local TCP listener so nhooyr.io/websocket can `Dial()` a standard URL. It needs:
- Spawn SSH process, get stdin/stdout pipes
- Start local TCP listener on random port
- Accept one connection, bridge it bidirectionally to SSH stdin/stdout
- Handle SSH process death → close TCP connection
- Handle TCP connection close → kill SSH process
- Clean up on hub shutdown

This is ~60-80 lines of careful code with goroutine lifecycle management, not a trivial detail.

**Fix:** Add the local proxy to the plan as a named component (`internal/hub/transport.go`). Spec the lifecycle: SSH process management, bidirectional bridging, error propagation, cleanup.

### 11. Plan says "86 cases" in the switch — actual count is 62

**Location:** Prep refactor §0  
**Issue:** The plan says "86 cases across ~430 lines" but `handleClientMessage` has 62 `case protocol.Cmd*` arms (some with duplicate entries for different code paths). The unix socket handler in `daemon.go` has 21 additional cases, but those are separate. The numbers are misleading.

**Fix:** Correct to "62 case arms in handleClientMessage" in the plan.

### 12. Contradiction: Locked Decision §9 vs §11

**Location:** Decisions Locked §9 says "exact match, auto-update on mismatch." Architecture Decision §11 proposes major.minor versioning with a compatibility window.  
**Issue:** These contradict each other. If we always auto-update to exact match, why do we need a compatibility window? And if we have a compatibility window, why auto-update on any mismatch?

**Fix:** Resolve by picking one: Either (a) exact match with auto-update (simpler, the plan's primary thrust) and drop §11, or (b) major.minor with auto-update only on major mismatch. Recommendation: exact match + auto-update for v1 (simplest). Drop major.minor to Deferred Decisions.

---

## MINOR

### 13. `daemon --status` subcommand underspecified

**Location:** Plan §A.3  
**Issue:** "Prints running/stopped + version" but doesn't say how it detects if the daemon is running. Options: check PID file, try connecting to unix socket, check the WS port. The daemon already has a `/health` HTTP endpoint — could reuse that.

**Fix:** Specify: connect to unix socket, send a `query` command, report result. Or check PID file at `~/.attn/daemon.pid` if it exists.

### 14. Remote daemon port conflict

**Location:** Bootstrap Flow step 4  
**Issue:** Bootstrap runs `nohup attn daemon &` but doesn't handle the case where port 9849 is already in use (another user's daemon, or a different process). The daemon would fail to start silently since stdout/stderr are redirected to /dev/null.

**Fix:** Check `attn daemon --status` output after starting. If it reports failure, surface the error. Consider allowing port configuration via env var on remote.

### 15. `git_status` subscription re-establishment after reconnect

**Location:** Not addressed  
**Issue:** If the UI has an active `subscribe_git_status` for a remote session and the SSH connection drops and reconnects, the subscription is lost on the remote daemon (new WS client). The hub needs to re-send `subscribe_git_status` for any active subscriptions after reconnect.

**Fix:** Hub must track active git_status subscriptions per endpoint and re-establish them on reconnect.

### 16. `session_visualized` timing across network

**Location:** Not addressed  
**Issue:** `session_visualized` is sent when the user focuses a session in the UI. For remote sessions, this must be forwarded to the remote daemon to trigger deferred classification (the "needs_review_after_long_run" flag). Network latency means the 5-second visualization window may behave differently.

**Fix:** Minor — just forward the command. Note that the 5s timer starts on the remote when it receives the command, which is fine.

### 17. `nohup attn daemon &` may not work reliably

**Location:** Bootstrap Flow step 4  
**Issue:** `nohup cmd </dev/null &>/dev/null &` over SSH can be unreliable. The SSH session may not properly detach the background process on all systems. Some SSH implementations wait for all file descriptors to close.

**Fix:** Use `nohup attn daemon </dev/null >~/.attn/daemon.log 2>&1 &` (redirect to log file, not /dev/null) and add `disown` or use `setsid`. Or better: add a `attn daemon --background` flag that forks itself.

### 18. Endpoint capabilities detection: `agents_available`

**Location:** Plan §B, EndpointCapabilities model  
**Issue:** `agents_available` is listed as `string[]` (e.g., `["claude", "codex"]`) but the remote daemon's `initial_state` sends this as `settings.claude_available = "true"/"false"`. The hub needs to parse these settings into the capabilities struct. This is minor but unspecified.

**Fix:** Document that hub extracts agent availability from `settings.claude_available`, `settings.codex_available`, `settings.copilot_available` in the remote's `initial_state` settings map.

### 19. `inject_test_pr` and `inject_test_session` scoping

**Location:** Not in scoping table  
**Issue:** These test commands exist on both WS and unix socket. For hub routing, they should probably be hub-local (inject into local store only).

**Fix:** Add to scoping table as hub-local.

---

## SUGGESTIONS

### 20. Add SSH multiplexing for efficiency

Multiple SSH commands during bootstrap (uname, which, scp, daemon start, ws-relay) could reuse a single SSH connection via `ControlMaster`. This avoids repeated key exchange and speeds up bootstrap significantly.

### 21. Add a dry-run / test-connection command

Before full bootstrap, let users test SSH connectivity: `test_endpoint { ssh_target }` → tries `ssh <target> 'echo ok'` and reports result. Useful for diagnosing auth/firewall issues without committing to a full install.

### 22. Consider remote log access

When debugging remote daemon issues, having `ssh <target> 'tail -f ~/.attn/daemon.log'` accessible from the UI would be valuable. Could be a Phase D or E feature.

### 23. Deferred Decision §5 contradicts Phase D

Deferred Decisions lists "Remote session spawning UI (which endpoint to target)" but Phase D §3 says "Spawn session UI allows selecting target endpoint." These should be consistent — remove from Deferred if it's in Phase D.

### 24. Consider parallel endpoint bootstrap on daemon start

If multiple endpoints are configured, bootstrapping them serially could take 30+ seconds (SSH handshake + install + start per endpoint). Bootstrap in parallel goroutines.

### 25. Binary cache location

The plan mentions `~/.attn/remotes/binaries/` for caching linux binaries. This should be documented in the plan's Bootstrap Flow section and cleaned up periodically (only keep current version).
