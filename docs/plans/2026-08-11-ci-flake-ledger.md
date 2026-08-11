# Plan: close the active CI flake ledger

## Goal

Remove the root causes behind the active entries in issue #796 without weakening
the scenarios they protect. Treat failures on branches that predate an existing
fix as historical evidence rather than changing current code again.

## Findings

- `agent pane stays painted after opening a shell split` failed by reading the
  last trace entry after a healthy draw; that entry can be a no-op paint with
  `quads: null`. Current main already fixes this in `487adce1` by reading
  `lastRealDraw`. The Aug 9 occurrences came from branches without that commit.
- `agent stays painted when split races a chunked redraw` and `hovering a chooser
  row does not change the Enter target` did not reach their scenarios. Both
  failed in `app/e2e/fixtures.ts` because a live child process had not created
  its Unix socket before the fixture's fixed five-second deadline. The same
  fixture failure has also been attributed to session-restore and state-color
  tests.
- `TestDaemon_PrunesSessionsWithoutLivePTYOnStart` queries after the Unix socket
  appears, but startup PTY recovery deliberately continues in a goroutine. The
  test sometimes observes the seeded session before recovery prunes it.

## Architecture Map

```text
E2E today:
test -> spawn daemon -> poll socket path + fixed deadline -> scenario

E2E target:
test -> spawn daemon -> watch socket directory
                    \-> observe child exit
                       -> socket exists: scenario
                       -> child exits: fail with process/log evidence

Go test today:
Start -> socket/listener ready -> recovery goroutine -> prune seeded session
 test ---------------------------> query (races recovery)

Go test target:
Start -> socket/listener ready -> recovery goroutine -> close recovery signal
 test --------------------------------------------------> query
```

## Boundaries

- The E2E fixture owns process readiness. Individual scenarios must not grow
  retries or larger time budgets to compensate for daemon startup.
- `Daemon` owns startup recovery state and its completion signal. Tests wait on
  that real lifecycle edge rather than sleeping or polling.
- Terminal paint assertions and RepoOptions keyboard behavior stay unchanged;
  the observed failures provide no evidence that either product behavior is
  wrong on current main.

## Implementation Steps

- [x] Replace the E2E socket interval and fixed startup deadline with a
      race-safe filesystem watch plus child-exit handling for both daemon
      launchers; retain actionable stdout, stderr, and daemon-log diagnostics.
- [x] Add a waitable daemon recovery completion signal and change the shared Go
      test helper to wait on it.
- [x] Update `TestDaemon_PrunesSessionsWithoutLivePTYOnStart` to wait for
      recovery rather than sleeping for startup and querying mid-recovery.
- [x] Add focused tests for readiness success/child-exit cleanup and recovery
      signaling where the seam can be tested without wall-clock timing.
- [x] Run the targeted Go test under repetition and race detection, the exact
      E2E scenarios that received the false attribution, frontend
      tests/typecheck, and the relevant broader suites.
- [x] Add one internal changelog fragment for the flake fixes.

## Decisions

- Do not modify the current split-paint test for the dominant ledger entry; its
  fix is already on main and the later failures executed older test code.
- Fix the shared E2E launcher once. Raising its deadline would leave an
  unmeasured limit and preserve false attribution to whichever scenario happens
  to start next.
- Keep startup recovery asynchronous in production. Registration during that
  window is intentional and protected by the recovery cutoff; only tests that
  assert post-recovery state need the completion edge.
- Scope both E2E daemon launchers with `ATTN_DATA_DIR` as well as their explicit
  database and socket paths. Startup diagnostics now come from the same isolated
  directory instead of risking inherited profile paths.

## Verification

- `go test ./internal/daemon`: passed.
- Recovery signal and startup-prune tests: 25 consecutive passes and clean under
  the race detector.
- `pnpm --dir app exec tsc --noEmit`: passed.
- `pnpm --dir app test`: 243 files passed; 2,714 tests passed and 15 skipped.
- Readiness seam E2E: both socket-creation and child-exit cases passed.
- Exact ledger scenarios passed: chunked split redraw and LocationPicker
  hover/Enter targeting.
