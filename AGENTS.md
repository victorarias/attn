# AGENTS.md

Repository guidance for coding agents working in this repo.

## Build And Test

```bash
# source/dev install
make install
make install-daemon

# app
make build-app

# tests
make test
make test-frontend
make test-e2e
make test-harness
make test-all
go test ./internal/store -run TestList
```

Use `make install` as the default source/dev install path; it rebuilds and installs the app bundle and ensures the bundled daemon is running. Use `make install-daemon` when only daemon/runtime code changed and you want the faster sidecar-only loop.

Frontend-only shortcuts:

```bash
cd app
pnpm run dev
pnpm test
pnpm run e2e
```

## Core Rules

- Do not add tests that only restate compile-time guarantees.
- Do not copy production code into tests.
- Maintain `CHANGELOG.md` for user-visible changes and include it in the same commit when appropriate.
- Diagnose root cause before fixing symptoms.
- Do not remove requested functionality without explicit approval.

## Packaged-App Harness

- Real packaged-app scenarios are single-tenant. Never run them in parallel.
- Treat failures from parallel packaged-app runs as invalid harness usage, not product evidence.
- Prefer `pnpm --dir app run real-app:serial-matrix` for multiple packaged-app scenarios.
- If you need one scenario, run exactly one packaged-app scenario at a time and wait for it to finish.
- Rebuild first when packaged-app evidence matters, or you may be testing an older installed app.

## Debugging And Logging

Verbose daemon logging:

```bash
DEBUG=debug attn -s test
```

- daemon log: `~/.attn/daemon.log`
- use `d.logf(...)` in daemon code and pass `LogFunc` into subpackages when needed
- do not use `log.Printf()` for daemon logging because stderr is discarded in background mode
- use prefixed `console.log/warn/error` in frontend code and inspect via Tauri DevTools

### Frontend Instrumentation (disk-based)

For hard-to-reproduce UI bugs (viewport shifts, race conditions, layout glitches), prefer **disk-based instrumentation** over `console.log`. This lets the agent read the log file directly without needing browser DevTools access.

Pattern: use Tauri's `writeTextFile` to append JSONL to `$APPLOCALDATA/debug/<name>.jsonl`. See `app/src/utils/paneRuntimeDebug.ts` and `app/src/utils/terminalRuntimeLog.ts` for the canonical pattern.

Quick approach for temporary investigation:

```typescript
// app/src/utils/viewportDebug.ts (example)
import { isTauri } from '@tauri-apps/api/core';

let chain: Promise<void> = Promise.resolve();

export function vpLog(event: string, details?: Record<string, unknown>) {
  const entry = { at: new Date().toISOString(), event, ...details };
  chain = chain.catch(() => {}).then(async () => {
    if (!isTauri()) return;
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir('debug', { baseDir: BaseDirectory.AppLocalData, recursive: true });
    await writeTextFile('debug/<name>.jsonl', JSON.stringify(entry) + '\n', {
      baseDir: BaseDirectory.AppLocalData, append: true, create: true,
    });
  });
}
```

Read the log: `cat ~/Library/Application\ Support/com.attn.manager/debug/<name>.jsonl`

Rules:
- Always write to disk, not just console — agents can't read DevTools
- Use JSONL format (one JSON object per line) so the file is easy to parse
- Remove temporary instrumentation files once the bug is resolved

## Architecture Snapshot

- `cmd/attn`: CLI wrapper that launches agents, registers sessions, and wires hooks/settings
- `internal/hooks`: Claude hook generation and state/todo reporting
- `internal/daemon`: session lifecycle, PTY management, git/github operations, websocket updates
- `internal/store`: SQLite-backed state with in-memory cache
- `internal/classifier`: stop-time WAITING/DONE classification
- `internal/transcript`: last-assistant-message extraction from JSONL transcripts
- app: Tauri frontend over `ws://localhost:9849`

Session states:

- `launching`: session opened, waiting for first authoritative runtime signal
- `working`: actively running
- `pending_approval`: blocked on approval
- `waiting_input`: stopped and needs user direction
- `idle`: done
- `unknown`: state could not be determined reliably
- `needs_review_after_long_run`: 5+ minute run held for user review before final classification

## Critical Patterns

### 1. Protocol Versioning

When changing commands, events, or protocol message shapes:

1. update `internal/protocol/schema/main.tsp` if needed
2. run `make generate-types`
3. update `internal/protocol/constants.go`
4. increment `ProtocolVersion` in `internal/protocol/constants.go` for protocol changes
5. run `make install`

Why: the daemon survives app rebuilds. Old daemon plus new app creates silent breakage unless protocol versioning forces a reconnect failure.

Generated files you do not hand-edit:

- `internal/protocol/generated.go`
- `app/src/types/generated.ts`

### 2. Async WebSocket Actions

For operations that can fail, do not use optimistic fire-and-forget UI.

Use the request/result pattern:

- daemon handles the command and emits a `*_result` event
- protocol defines success and error fields
- frontend returns a `Promise`, tracks pending actions, and resolves or rejects on the result event

Canonical example: `sendPRAction` in `app/src/hooks/useDaemonSocket.ts`.

### 3. Classifier Timestamp Protection

Classifier work is asynchronous and can be slow. Capture the timestamp before classification starts and update state via `UpdateStateWithTimestamp()` so stale classifier results cannot overwrite fresher runtime state.

### 4. Review Comment Fan-Out

When changing `ReviewComment`, update all consumers:

- TypeSpec schema
- store
- daemon websocket handlers
- frontend hooks and UI
- reviewer MCP tools in `internal/reviewer/mcp/tools.go`
- reviewer filtering logic

Easy miss: reviewer-agent MCP tools do not read through the websocket protocol.

### 5. Pointer/Value Conversions

Store APIs often use pointer slices while protocol responses use value slices. Prefer helpers in `internal/protocol/helpers.go` such as `Ptr`, `Deref`, `SessionsToValues`, and `PRsToValues`.

### 6. Terminal Focus Ownership

When switching sessions, do not blindly refocus the main terminal. If the selected session has an active utility terminal, keep focus there.

Why:

- `Cmd+T` and utility workflows depend on keyboard input reaching the utility PTY
- delayed `mainTerminal.focus()` can steal focus after utility mount
- failure mode: cursor looks active but typing goes to the wrong PTY or disappears

Implementation rule:

- in `app/src/App.tsx`, selection should `fit()` main terminal but only `focus()` it when utility is not open or active
- in `app/src/components/UtilityTerminalPanel/index.tsx`, prefer `xterm.focus()` before fallback focus with retry

Verification:

- run `app/e2e/utility-terminal-realpty.spec.ts` with `VITE_MOCK_PTY=0 VITE_FORCE_REAL_PTY=1`
- confirm `Cmd+T` typing works without extra click
- confirm switch-away and switch-back still types into utility without extra click

### 7. PTY Geometry And Replay

Before changing PTY attach/replay, terminal resize, mobile keyboard viewport handling, or daemon-served terminal rendering, preserve these rules:

- PTY geometry has one authority at a time: the most recently active interactive client
- `pty_resize` is authoritative; `attach_session` replay is provisional context only
- do not use replay as a generic redraw repair tool
- do not treat local `fit()` or viewport churn as proof the PTY is correct
- do not let replayed historical terminal queries produce fresh live PTY input
- for mobile or viewport bugs, keep structured instrumentation and prefer transition evidence over screenshots alone

## Communication

- unix socket IPC: `~/.attn/attn.sock`
- websocket: `ws://localhost:9849`
- websocket clients have a 256-message buffer; sustained frontend over-send can drop messages or disconnect slow clients

## Changelog

`CHANGELOG.md` uses dated sections, not versions.

When updating it:

- write for users, not maintainers
- summarize behavior changes, not implementation details
- group related work into a small number of bullets
- update it after meaningful features, fixes, removals, or commits

## When Something Is Broken

1. Diagnose why before proposing a fix.
2. Fix the root cause instead of removing behavior to hide the symptom.
3. If refactoring, list the behaviors that must survive and verify them.
4. Do not remove user-requested functionality without approval.

Ask: am I fixing the problem or avoiding it?
