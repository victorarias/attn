# Installed plugin supervision

## Goal

Keep installed plugins available across process exits and lost daemon
connections, make recovery visible in Settings, and let a restarted OpenCode
plugin resume monitoring attn-owned native sessions without relaunching their
TUIs.

## Architecture Map

```text
Current:
daemon discovery / install
  -> start bun once
    -> Wait removes process record

Target:
daemon discovery / install
  -> pluginSupervisor.Ensure(manifest)
    -> generation-tagged process
      -> hello(generation) -> connected + stability timer
      -> exit / 5s disconnect grace
        -> bounded backoff -> next generation

driver.register
  -> store.ListAgentDriverRuns(plugin)
  -> active_runs response
    -> OpenCode registry intersection
      -> orphan/dead cleanup
      -> surviving HTTP/SSE monitors restart in place

Tests:
fake clock + fake launcher/handles
  -> deterministic lifecycle transitions
fake daemon RPC + fake OpenCode server
  -> ownership reconciliation + recovered reports
```

## Data Model / Interfaces

```text
ManagedPlugin (daemon-owned, in memory)
  manifest, desired, phase, generation, process
  restartAttempt, connectedAt, nextRestartAt
  lastExit, restart/grace/stability cancellation

PluginInfo (daemon -> Settings)
  runtime_phase, restart_attempt, next_restart_at, last_exit

plugin hello (plugin -> daemon)
  name, version, attn_api_version, generation

driver.register result (daemon -> plugin)
  ok, active_runs[{session_id, run_id, metadata}]
```

## Boundaries

- `pluginSupervisor` owns process desired state, generation checks, timers, and
  exit diagnostics; it does not know about OpenCode or plugin drivers.
- The daemon supplies process environment and translates connection lifecycle
  into supervisor notifications.
- The store is authoritative for active plugin-run ownership. OpenCode's private
  registry contains the credentials and sequence needed to reconnect, but may
  not resurrect a run absent from the store result.
- OpenCode recovery reuses the existing monitor path and never spawns a TUI or
  selects a different native conversation.

## Execution

- [x] Add the injectable supervisor with restart, backoff, stability, stop, and
  disconnect-grace coverage.
- [x] Route daemon discovery, install, remove, shutdown, and plugin connections
  through desired-state and generation-aware supervision.
- [x] Expose supervisor snapshots in the protocol and Settings; regenerate types
  and increment the daemon protocol.
- [x] Return authoritative active runs from `driver.register`; increment the
  plugin API and update compatibility fixtures.
- [x] Add OpenCode registry listing, ownership reconciliation, and monitor-only
  recovery with focused adapter coverage.
- [x] Update operational docs and changelog.
- [x] Run full automated checks and the isolated packaged-app crash/recovery
  scenario before opening the PR.
- [ ] Open the PR, address Figgyster review, and merge only after approval.

## Decisions

- Put the supervisor behind launcher and clock interfaces so lifecycle tests do
  not sleep or spawn real processes.
- Include `generation` in plugin hello under the bumped API. Process identity
  cannot be inferred safely from plugin name after a restart.
- Reset restart attempts only after 60 seconds connected, not merely alive.
- Treat OpenCode recovery as monitor reconstruction from surviving private
  records; spawning or resuming a PTY remains daemon/worker-owned.

## Verification

- `go test ./...`
- Focused supervisor tests under `go test -race`
- OpenCode plugin: 63 tests, 175 assertions
- Plugin SDK: 9 tests, 16 assertions
- Settings: 44 tests
- `make dev` packaged and installed the isolated `attn-dev` app.
- Live `attn-dev`: OpenCode completed a turn, the confirmed dev plugin process
  was terminated, the supervisor started a new process, and the plugin returned
  healthy. A second turn completed in the same visible TUI with the same attn
  run ID and native OpenCode session ID; its report sequence advanced from 8 to
  13. The production plugin process was identified separately and left intact.

## Follow-ups

- Add a manual “restart now” control only if backoff diagnostics show a need.
- Consider health-triggered restarts only after measuring hung-process false
  positives.
