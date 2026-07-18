# Plan: attn.db Data-Loss Mitigation

## Why

On 2026-07-18 a `go test ./internal/daemon` run destroyed the production
`~/.attn/attn.db`: daemon tests resolve `config.DataDir()` straight to the real
`~/.attn`, and a test wrote a sentinel file there. Nothing was recoverable —
no backups existed, no Time Machine / APFS snapshots. Workspaces and settings
were salvaged from the live daemon's memory, but tickets, session registry,
review comments, and recent locations (the connective core) were gone, so in
practice the state was a total loss.

Two mitigations, in order: make loss recoverable (backups), then make this
class of loss impossible to cause from the test suite (tooling).

## 1. Periodic Backups With Rotation

**Status: core merged (#578, `a1860213`).** The daemon snapshots the DB via
`VACUUM INTO` into `<data-dir>/backups/`:

- at daemon startup and every 6h — `attn-<timestamp>.db`, **keep last 12**
  (~3 days), oldest pruned
- before any pending schema migration — `attn-premigration-<ver>-<ts>.db`
- backups refuse the in-memory fallback store (would snapshot garbage and
  rotate away real copies exactly when the durable DB is broken)
- a failed backup logs and never blocks startup

Reaches prod on the next prod install; until then the running daemon has no
safety net.

Follow-ups (rotation awareness — no infinite copies anywhere):

- [x] Cap pre-migration snapshots too (#582). They are exempt from the
      keep-12 prune today, which is unbounded over time. Prune to the last
      **5** pre-migration snapshots (they only accrue one per migration, but
      "exempt" must not mean "immortal").
- [x] Restore path (#582): `attn db restore <backup|latest>` — stop-daemon-safe
      copy of a snapshot back into place (refuses while a daemon holds the
      DB), so recovery is one command instead of an incident script.
- [x] Surfacing (#582): expose last-successful-backup age (settings/status
      payload). A backup loop that silently fails for weeks is a safety net
      that isn't there; the app can show a warning when the newest snapshot
      is stale (> 24h).

pid-lock hardening follow-ups (orphan unlink-race regression, restore-lock
error classification) landed via #586/#587.

## 2. Tooling: Tests Can Never Touch The Real Data Dir

Root cause: `config.DataDir()` is env-derived at call time (`$HOME/.attn`),
and nothing in the test harness redirects it. Any test (or transitively, any
helper) that writes under `DataDir()` writes to production. The current state
— three individually-guarded tests with a `t.Setenv("HOME", ...)` prologue —
is convention, not enforcement.

We deliberately do NOT touch `HOME`: redirecting a global env var that every
tool interprets is exactly the kind of blast-radius lever a mistaken agent or
partially-applied redirect can turn into real damage. Instead, an explicit
attn-only override:

```text
Layer 1 (explicit test scoping)       Layer 2 (global backstop)
ATTN_DATA_DIR env var, highest         under `go test`, config.DataDir()
  precedence in config resolution;     panics unless ATTN_DATA_DIR is set —
  TestMain sets it to a per-run        no path heuristics, no HOME
  temp dir                             inspection, just "tests must scope"
```

- [x] **`ATTN_DATA_DIR` override in `config`**: highest-precedence data-dir
      source (above `ATTN_PROFILE` derivation). Nothing else in the
      environment changes — HOME is never read differently, no other tool's
      dotfiles are affected, and destructive test cleanup only ever sees the
      explicit temp path.
- [x] **TestMain scoping** in `internal/daemon` (and any other package whose
      tests can reach `config.DataDir()` — audit `internal/store`,
      `internal/hooks`, `cmd/attn`): `config.ScopeTestEnvironment(<temp
      dir>)` in `TestMain` before `m.Run()`, so every test in the package is
      scoped by default instead of opt-in. Empirically (added the backstop,
      ran `go test ./...`, fixed every panic) this landed in
      `internal/daemon` (extended the existing `TestMain`),
      `internal/daemonctl`, and `internal/client`; `internal/store`,
      `internal/hooks`, and `cmd/attn` never reach the guarded chokepoint in
      tests and needed no changes.
- [x] **Closed the per-path override escape door** (figgyster review on
      PR #584): `DBPath`, `SocketPath`, `PluginDir`, and the config-file path
      each check their own env var (`ATTN_DB_PATH`, `ATTN_SOCKET_PATH`,
      `ATTN_PLUGIN_DIR`, `ATTN_CONFIG_PATH`) before ever reaching the
      `ATTN_DATA_DIR`-scoped `attnDir()` chokepoint, so setting
      `ATTN_DATA_DIR` alone in `TestMain` did not bound them — a shell with
      an inherited `ATTN_DB_PATH` pointed at the real database would still
      leak into tests. `config.ScopeTestEnvironment(dataDir)` sets
      `ATTN_DATA_DIR` and unconditionally clears all four overrides in one
      call; every `TestMain` above uses it instead of a raw
      `os.Setenv("ATTN_DATA_DIR", ...)`. Test-only: panics outside
      `testing.Testing()`. Proven by a subprocess regression
      (`TestScopeTestEnvironment_SanitizesInheritedOverrides`) that seeds
      hostile `ATTN_DB_PATH`/`ATTN_SOCKET_PATH`/`ATTN_CONFIG_PATH` in the
      child's inherited env and asserts the child's `TestMain`-scoped
      `DBPath()`/`SocketPath()` still resolve inside the fresh
      `ATTN_DATA_DIR`, not the hostile values.
- [x] **Backstop in `config`**: using `testing.Testing()` (Go ≥1.21),
      `DataDir()` panics if called under `go test` without `ATTN_DATA_DIR`
      set. A panic is correct here: it converts silent prod damage into an
      immediate, attributable test failure, and it catches every future
      package that forgets Layer 1 — including code that doesn't exist yet.
      The rule is a simple presence check, not a path comparison, so it can't
      be fooled by symlinks or unusual HOMEs. (Also had to make `config`'s
      `init()`-time config-file load lazy — it ran before any package's
      `TestMain` could set `ATTN_DATA_DIR`, so the backstop fired on package
      load for every test binary that imports `config`, not just ones that
      call `DataDir()`.)
- [x] Regression proof: one test per layer — `TestAttnDir_DerivedPathsAllInheritDataDir`
      proves `ATTN_DATA_DIR` wins even with `ATTN_PROFILE` set and that every
      derived path (socket/DB/log) inherits it; `TestDataDir_PanicsWithoutATTNDataDirUnderTest`
      proves the backstop panic fires, via a subprocess that re-execs the test
      binary with `ATTN_DATA_DIR` unset (same pattern as os/exec crash tests).

## Decisions

- **No HOME manipulation, ever** (Victor, 2026-07-18): test scoping goes
  through the attn-specific `ATTN_DATA_DIR` override, never by redirecting
  `HOME`. A global env redirect is a blast-radius lever — a partially-applied
  or mistaken redirect combined with destructive cleanup could hit the real
  home directory.
- Backstop lives in `config` (the single source of path truth), not in
  `store.OpenDB` — the incident wrote a raw file without going through the
  store, so guarding the store would not have caught it.
- `testing.Testing()` over env-sniffing heuristics: it is the stdlib's
  purpose-built signal and free of false positives in production binaries.
- Pre-migration snapshot cap of 5 rather than folding them into the keep-12
  pool: a bad migration may only be noticed days later; the newest rotating
  snapshots would already be post-migration by then.

## Follow-ups

- Consider an opt-in `attn db backup now` CLI verb for manual pre-surgery
  snapshots (cheap once BackupNow exists).
