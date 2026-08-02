# attn profiles

A **profile** fully isolates an attn install: its own data directory, socket,
websocket port, macOS app bundle, and bundle identifier. Profiles let several
agents run attn — and its tests — side by side without colliding.

**One knob: `ATTN_PROFILE`.** Set it once in a shell and every entrypoint (CLI,
daemon, frontend e2e, real-app harness, build) targets the same isolated world.
You never set per-entrypoint profile variables by hand. `profile-env` first
clears inherited explicit routing overrides (`ATTN_SOCKET_PATH`, `ATTN_DB_PATH`,
`ATTN_CONFIG_PATH`, `ATTN_WS_PORT`, and `ATTN_PLUGIN_DIR`) so the selected profile
actually wins, including inside an attn-managed session.

```
attn profile-env agent7 | source     # fish:  set ATTN_PROFILE=agent7
eval "$(attn profile-env agent7)"     # bash/zsh
attn profile-env --unset | source     # back to the default profile
```

## See where you are

```
attn profile            # status of the active profile
attn profile list       # every profile with data and/or an installed app
```

`attn profile status` prints the resolved data dir, socket, port, bundle id,
app path, and e2e ports, and whether the daemon socket / app are present. Any
non-default `attn` command also prints a one-line `[attn profile=… socket=…
port=…]` banner so you can never lose track of which world you are touching.

`ATTN_DATA_DIR`, when set, overrides every profile-derived data dir outright
(highest precedence, above `ATTN_PROFILE`) — the knob test suites and
harnesses use to scope themselves to an explicit temp dir instead of ever
touching a real profile's data. See
[docs/plans/2026-07-18-db-loss-mitigation.md](plans/2026-07-18-db-loss-mitigation.md).
It is a per-process scoping override, not a profile. `attn profile resolve`
and `attn profile clean` deliberately keep reporting and operating on the
profile's canonical directories and do not honor it, so under `ATTN_DATA_DIR`
the live process's runtime paths differ from `resolve`'s output by design.

## The single authority: `attn profile resolve`

Every resource a profile maps to is derived in exactly one place
(`internal/config`) and surfaced by `resolve`. Tooling (the Makefile, the e2e
harness, the real-app harness) reads `resolve` instead of re-deriving — so the
mapping can never drift between entrypoints.

```
attn profile resolve --json                 # full resolution as JSON
attn profile resolve --field wsPort          # one value (scripting)
attn profile resolve --profile dev --field bundleId
```

Resolved keys: `profile, label, dataDir, socket, dbPath, wsPort, bundleId,
appName, appPath, deepLinkScheme, e2eDaemonPort, e2eVitePort`.

## What maps to what

| Resource | default | dev | named (e.g. `agent7`) |
|---|---|---|---|
| Data dir | `~/.attn` | `~/.attn-dev` | `~/.attn-agent7` |
| WS port | 9849 | 29849 | hash → `[20000,29848]` |
| Bundle id | `com.attn.manager` | `…​.dev` | `…​.agent7` |
| App | `attn.app` | `attn-dev.app` | `attn-agent7.app` |
| Deep-link scheme | `attn` | `attn-dev` | `attn-agent7` |
| e2e daemon port | 19849 | hash → `[30000,30999]` | hash → `[30000,30999]` |
| e2e Vite port | 1421 | hash → `[31000,31999]` | hash → `[31000,31999]` |

Port bands are disjoint by construction: a throwaway e2e daemon never collides
with a *real* daemon of the same profile. Names match `[a-z0-9][a-z0-9-]{0,15}`.

## Running tests under a profile

| Suite | Command | Isolation |
|---|---|---|
| Go unit/integration | `make test` | already parallel-safe (each test uses `t.TempDir()` + explicit `ATTN_*` env; never touches `~/.attn`) |
| Frontend e2e | `make test-e2e` | derives this profile's e2e daemon + Vite ports; the per-run daemon kill is scoped to that port |
| Real-app scenarios | `pnpm --dir app run real-app:…` | targets the active profile's daemon/app via `attn profile resolve`; `ATTN_HARNESS_PROFILE` overrides; default is the dev sibling, never prod |

So two agents in separate worktrees with distinct `ATTN_PROFILE` values can run
the Go and (soon) e2e suites concurrently with no cross-talk.

## Safety model — hard to point the wrong profile at the wrong place

- **Prod is sacred.** Bare `make`, `make install`, `make install-daemon` build
  the prod bundle, so they refuse at parse time if `ATTN_PROFILE` is set. Build
  a profile's own app with `make install PROFILE=<name>` (or `make dev` for the
  dev sibling, which works from any shell).
- **Build matches your shell.** `make install PROFILE=<name>` refuses when
  `<name>` differs from your shell's `ATTN_PROFILE`, so you can't build agent8's
  app while you think you're agent7. (`make dev` is exempt — it always targets
  the dev sibling on purpose.)
- **The packaged app is bound to its build profile.** A profile's `.app` pins
  `ATTN_PROFILE`/`ATTN_WS_PORT` and strips inherited routing env at launch, so
  it can never reach another profile's daemon.
- **Daemon isolation is enforced.** The daemon refuses to start if its socket
  root and database would straddle two profiles, and restarts when the running
  daemon's profile no longer matches the caller's.
- **Single authority.** Because every entrypoint derives from `attn profile
  resolve`, there is no hand-synced per-entrypoint mapping to get wrong.

## Lifecycle

| Step | How |
|---|---|
| Pick | `attn profile-env <name> \| source` |
| Inspect | `attn profile` / `attn profile list` / `attn profile list --json` |
| Build + install app | `make install PROFILE=<name>` (opens it: `make run PROFILE=<name>`) |
| Sign | uniform stable identity via `scripts/macos-codesign-identity.sh`; macOS grants persist per bundle id |
| Clean | `attn profile clean <name>` — reap pty-workers, stop daemon, quit app, remove data dir + app, forget the bundle |

### Cleaning up after yourself

A profile costs nothing to create and is invisible once made: its daemon (~40MB)
and each pty-worker (~15MB) keep running long after the branch that needed them
is merged, and nothing ever reaps them on its own. Clean a throwaway profile as
soon as the work is done.

**Workers are reaped before the data dir goes.** Stopping a daemon deliberately
leaves its workers running — they are built to outlive a restart and be
re-adopted — so `clean` shuts them down explicitly first. It talks to each worker
over the authenticated control socket recorded in the profile's worker registry,
which is precise by construction: it cannot hit an unrelated process that
inherited a recycled PID. A worker whose socket is unreachable is signalled only
if its argv still identifies it as that registry entry's worker; anything less
certain is left running and reported by PID, e.g.

```
  workers  3 registered (2 removed, 1 unidentified)
           ! session 8f9fb98d: pid 19891 could not be confirmed as its worker
             (dial unix …: connect: connection refused); left running — check it
             with `ps -p 19891` and kill it yourself if it is stale
```

Removing the data dir before reaping is what strands a worker permanently: the
registry it would be found through goes with the dir, so no daemon can ever adopt
it again.

### Provenance

`make install PROFILE=<name>` and `make install-daemon PROFILE=<name>` record the
worktree they ran from in `<data-dir>/origin.json`. Install is the only moment
that link is known for certain — the build fingerprint identifies source
*content*, not the checkout — and without it a throwaway profile is
indistinguishable from a long-lived one once the agent that made it is gone.

`attn profile list` shows the origin worktree; `attn profile list --json` adds
the branch, whether the daemon is up, and how many workers are live. Cleaning a
profile removes its origin record along with everything else, so provenance never
outlives the profile.

That record is what powers the repository's PR-milestone cleanup hook
(`scripts/claude/attn-profile-nudge.sh`, wired in `.claude/settings.json`): after
an agent creates or merges a PR, it reminds the agent to clean any profile
installed from that worktree, and stays silent otherwise. Record an origin by
hand with `attn profile set-origin <name> [--worktree <dir>]` if you created a
profile outside `make install`.

## Rollout status

The model above is fully implemented: the `internal/config` authority and
`attn profile status|resolve|list|clean`; Go-test parallel-safety; profile-aware
**frontend e2e** (derives its daemon + Vite ports from the active profile and
scopes its teardown kill to its own port) and **real-app harness** (honors
`ATTN_PROFILE` with an `ATTN_HARNESS_PROFILE` override, resolves every resource
via `attn profile resolve`, never targets prod by omission). Every non-empty
profile (the `dev` sibling or any named profile) exposes the **UI automation
layer** — the app writes a `ui-automation.json` manifest and serves the
localhost+token bridge the harness drives. Production (the empty-profile bundle)
stays off unless an operator opts in with `ATTN_AUTOMATION=1` (see
`profile::automation_enabled`). The model also covers the
**per-profile app build** (`make install PROFILE=<name>` — bundle metadata
generated from `attn profile tauri-config`, the authority's port and bundle id
baked into the binary so a profiled app can never reach another profile's
daemon); and **`attn profile clean`** for teardown. See
`docs/plans/2026-06-13-parallel-profiles.md` for the design history.
