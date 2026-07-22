# attn agent guide

macOS only. Do not add Linux or Windows compatibility unless requested.

## Commands

```bash
# isolated non-production install (pre-authorized)
make dev                    # build/install/open attn-dev.app; ensure dev daemon
make install-daemon-dev     # replace/re-sign daemon sidecar only
make install PROFILE=<name> # build/install named isolated profile

# production (Victor's explicit approval required)
make                        # build/install/open ~/Applications/attn.app
make install
make install-daemon

# build and test
make build-app
make test
make test-frontend
make test-e2e
make test-harness           # Go + frontend + e2e
make test-all               # Go + frontend
go test ./internal/store -run TestList

# frontend-only loop
pnpm --dir app run dev
pnpm --dir app test
pnpm --dir app run e2e
```

Run full app builds/installs outside the sandbox: code signing needs the macOS
keychain; sandboxed identity lookup can cause ad-hoc signing and lose persistent
permissions.

## Profiles and live verification

- Non-production builds, installs, launches, and restarts are pre-authorized.
- Production `make`, `make install`, and `make install-daemon` require Victor's
  explicit approval.
- Install the cheapest tier that covers the change:
  - Go-only change (`cmd/attn`, `internal/**`) → `make install-daemon-dev`, or
    `make install-daemon PROFILE=<name>`. Replaces and re-signs the sidecar and
    restarts the daemon; no Tauri/Rust/frontend build.
  - Anything under `app/` (frontend, `src-tauri`, plugins), a protocol change
    (`generated.ts` moves with `generated.go`), or bundle metadata → `make dev`,
    or `make install PROFILE=<name>`.
  Escalate to the full build when unsure, or when a daemon-only install does not
  show the change.
- Named profile: select it with `eval "$(./attn profile-env <name>)"`, then run
  `make install PROFILE=<name>`. The shell's `ATTN_PROFILE` must match.
- `profile-env` clears inherited routing overrides. Verify the emitted
  `[attn profile=…]` banner before acting.
- Inspect with `attn profile`, `attn profile list`, or
  `attn profile resolve --json`; remove with `attn profile clean <name>`.
- Full model and per-agent recipe: [docs/profiles.md](docs/profiles.md).

Every non-trivial PR needs live verification from the branch in a running
non-production app/daemon. Exempt only:

- trivial docs/comments/renames/log strings; or
- a pure isolated change fully covered by unit tests, with no daemon lifecycle,
  protocol, PTY, background-runner, timing, or UI surface. State the reason.

Daemon lifecycle, protocol, PTY, background-runner, and UI changes always need
live verification. If the environment cannot run a non-production app, stop and
ask; do not merge on automated tests alone.

Before live verification, run the selected profile's bundled preflight:

```bash
profile_app="$(./attn profile resolve --field appPath)"
"$profile_app/Contents/MacOS/attn" preflight
# mirror pinned launch settings when applicable:
"$profile_app/Contents/MacOS/attn" preflight \
  --agent codex --model <model> --effort high --json
```

Use the bundled CLI, not an unrelated `attn` on `PATH`. Preflight is diagnostic;
fix reported tool/path/routing/daemon/protocol failures before treating scenario
output as product evidence.

To wait on a GitHub PR, run `attn pr wait-ready <pr> --repo <owner/repo>
--reviewer <login>` once; do not poll checks, reviews, and comments separately.
It returns on the first actionable update and reports which one by exit code:
`0` approved, `1` checks failed, `3` changes requested, `4` new human comment,
`124` timeout. Bot comments are ignored; comments already present when the wait
starts are the baseline.

### Packaged-app harness

- Single-tenant: never run packaged-app scenarios in parallel.
- Multiple scenarios: `pnpm --dir app run real-app:serial-matrix`.
- Rebuild before evidence-sensitive runs.
- Harness uses active `ATTN_PROFILE`, otherwise `dev`;
  `ATTN_HARNESS_PROFILE` overrides it.
- Production requires both `ATTN_HARNESS_PROFILE=` and `--run-against-prod`.
- On failure, inspect captured pane text and native screenshots before diagnosis.
- Remote scenarios target the local OrbStack VM (`attn-remote@orb`); provision with `pnpm --dir app run real-app:provision-remote`.

## Test safety

Tests must never resolve `config.DataDir()` or derived paths to production
`~/.attn`.

- Scope with `ATTN_DATA_DIR`; never redirect `HOME`.
- Any package reaching config paths must define `TestMain`, create one temp dir,
  and call `config.ScopeTestEnvironment(dir)` before `m.Run()`.
- Do not replace that call with raw `os.Setenv`: the helper also clears inherited
  `ATTN_DB_PATH`, `ATTN_SOCKET_PATH`, `ATTN_CONFIG_PATH`, and `ATTN_PLUGIN_DIR`.
- Individual tests may add `t.Setenv("ATTN_DATA_DIR", t.TempDir())`.
- Under `go test`, missing `ATTN_DATA_DIR` intentionally panics.

See [docs/plans/2026-07-18-db-loss-mitigation.md](docs/plans/2026-07-18-db-loss-mitigation.md).

## Architecture

- `cmd/attn`: CLI, agent launch, session registration, hooks/settings
- `internal/hooks`: Claude hooks and state/todo reporting
- `internal/daemon`: lifecycle, PTY orchestration, git/GitHub, WebSocket
- `internal/pty`: PTY, read loop, replay, terminal-query responses
- `internal/ptybackend`: `worker` (default) / `embedded` selector
- `internal/ptyworker`: per-session process; production PTYs run here through
  `internal/pty`, not inside the daemon
- `internal/store`: SQLite plus in-memory cache
- `internal/classifier`: stop-time state classification
- `internal/transcript`: assistant-message extraction from JSONL
- `app`: Tauri frontend; WebSocket `ws://localhost:9849`

States: `launching`, `working`, `pending_approval`, `waiting_input`, `idle`,
`unknown`. `needs_review_after_long_run` is a separate flag: a 5+ minute run
defers final classification until viewed, then clears the flag.

IPC: `~/.attn/attn.sock`. WebSocket clients buffer 256 messages; sustained
over-send may drop messages or disconnect slow clients.

## Cross-cutting contracts

### Protocol

For command/event/message-shape changes:

1. edit `internal/protocol/schema/main.tsp`;
2. run `make generate-types`;
3. update `internal/protocol/constants.go` and increment `ProtocolVersion`;
4. verify with a non-production install.

Never hand-edit generated `internal/protocol/generated.go` or
`app/src/types/generated.ts`. The daemon survives app rebuilds; version skew
must fail explicitly.

### WebSocket and state

- Fallible async UI actions use request/result: daemon emits `*_result`; frontend
  returns a `Promise` and resolves/rejects it. See `sendPRAction`.
- Persisted daemon state transitions go through `applyState` in
  `internal/daemon/session_state.go`; never write state directly to the store.
- Capture classifier observation time before async classification and pass it via
  `classifierObservation`; reject stale results.
- Changing `ReviewComment` requires schema, `internal/store/review.go`,
  `internal/daemon/ws_review.go`, frontend hooks, and UI updates.
- Prefer `internal/protocol/helpers.go` pointer/value helpers (`Ptr`, `Deref`,
  `SessionsToValues`, `PRsToValues`).

### Terminal

- The latest active interactive client owns PTY geometry.
- `pty_resize` is authoritative; attach replay is provisional context.
- Do not use replay as redraw repair or infer PTY correctness from local `fit()`.
- Replayed terminal queries must not generate fresh PTY input.
- The daemon/worker alone answers CPR, DA1, and OSC 10/11/12; frontend strips
  model replies and sends theme changes via `set_terminal_theme`.
- Session switching must retain utility-terminal focus. `App.tsx` may fit the
  main terminal but focuses it only when utility is inactive;
  `SessionTerminalWorkspace` prefers the active `GhosttyTerminal` handle.
- Manually verify `Cmd+T` typing and switch-away/back utility focus.

### macOS shortcuts

Packaged-app default menu accelerators can consume shortcuts before DOM keydown.

- Cmd+C: handle the DOM `copy` event (`GhosttyTerminal`), not keydown alone;
  verify with `real-app:scenario-terminal-block-copy`.
- Check every new shortcut against `Menu::default` accelerators.
- In `app/src-tauri/src/lib.rs`, remove a conflicting predefined menu item so
  the WebView resolver handles rebindings.
- Use `dispatch_native_shortcut` only when a visible/relabeled native menu item
  is required; it hardcodes the action.

## Diagnostics

- Daemon: `~/.attn/daemon.log` (profile data-dir equivalent for non-prod).
- Worker PTY: `<data-dir>/workers/<daemon-instance>/log/<session>.log`;
  `pty.Session` logs are here, not in `daemon.log`.
- Debug daemon: quit the app first (it respawns without `DEBUG`), then run
  `DEBUG=debug attn daemon ensure` for the selected profile.
- Daemon code: use `d.logf(...)` / injected `LogFunc`; background stderr drops
  `log.Printf()`.
- Frontend: use prefixed console logging and Tauri DevTools.
- Hard-to-reproduce UI bugs: prefer disk JSONL under
  `$APPLOCALDATA/debug/<name>.jsonl`; follow `terminalDiagnosticsLog.ts` or
  `terminalLinkHitTestLog.ts`; remove temporary instrumentation after the fix.

## Change discipline

- Diagnose root cause. Do not remove requested behavior without explicit
  approval. For refactors, list and verify behaviors that must survive.
- Do not copy production code into tests or test compile-time guarantees.
- Update `CHANGELOG.md` only for meaningful user-visible changes. Use dated
  sections; describe the PR's net behavior in concise user-facing bullets.
