# Parallel Profiles — one coherent, safe, discoverable profile model

Branch: `feat/parallel-profiles` → `main` (independent of the knowledge-base epic).
Status: implemented. See the **Rollout status** section of [docs/profiles.md](../profiles.md)
for what shipped; this document is retained as the design history.

## Goal

Let N agents (each in its own worktree) run the **full** attn test surface — Go,
frontend e2e, and packaged real-app scenarios — **in parallel**, each fully isolated,
by picking **one** profile. And make that whole lifecycle (pick · build · run · sign ·
clean) **streamlined, obvious, and safe**: it must be hard to point the wrong profile at
the wrong entrypoint, and trivial to see which profile you are on.

## What already works (do not rebuild)

The Go layer is already generic over `ATTN_PROFILE`: `internal/config/config.go` resolves
any name matching `^[a-z0-9][a-z0-9-]{0,15}$` into an isolated data dir
(`~/.attn-<profile>`), socket, DB, and a stable FNV-derived WS port in `[20000,29848]`
(`derivedProfilePort`). `ValidateDaemonIsolation` (config.go:267) and the daemonctl
profile-match restart (ensure.go:102) already guard against cross-profile contamination,
and `config_test.go:290` proves an arbitrary profile gets a non-colliding port. `go test
./...` is already parallel-safe (tests isolate via `t.TempDir()` + explicit `ATTN_*`
env, never touching `~/.attn`). **So "custom profiles" exist at the daemon layer today.**

## The actual problems

1. **Three uncoordinated profile knobs.** Runtime reads `ATTN_PROFILE`
   (config.go); the real-app harness reads a *separate* `ATTN_HARNESS_PROFILE` defaulting
   to `dev` (harnessProfile.mjs:22); the packaged app bakes a *third*, compile-time
   `ATTN_BUILD_PROFILE` (profile.rs:18). e2e reads **none** of them — it hardcodes ports.
   Picking a profile therefore means setting up to three different variables correctly,
   and getting it wrong silently targets the wrong daemon/app.
2. **Hand-synced derivation, four places.** Bundle id / app name / port / deep-link
   scheme are duplicated as literal `dev`/prod branches in `config.go`, `profile.rs`,
   `harnessProfile.mjs`, and the two `tauri*.conf.json` files — each carrying a "keep in
   sync with …" comment. Drift here is the concrete cross-entrypoint hazard.
3. **e2e is single-tenant by port, not by state.** Each e2e test already spawns its
   daemon in a fresh `/tmp` dir (fixtures.ts:188) — state is isolated. But the daemon port
   (`19849`), Vite port (`1421`, `strictPort`), and the teardown `pkill -f
   ATTN_WS_PORT=19849` (fixtures.ts:131) are global literals, so a second agent collides
   and agent A's teardown kills agent B's daemon.
4. **Only two app bundles can exist.** `bundle_identifier()` (profile.rs:112) and the
   Makefile (`APP_BUNDLE*`, `build_tauri_app` literal productName, the `--config
   tauri.dev.conf.json` overlay) hardcode exactly `com.attn.manager` / `.dev`. macOS allows
   one running instance per bundle id, so two agents cannot each drive a packaged app.

## Design principles

- **One knob: `ATTN_PROFILE`.** It is the single identity for every entrypoint. The other
  two knobs become *derived/internal*: `ATTN_BUILD_PROFILE` is set by the Makefile from the
  chosen profile and baked into the bundle (it must be compile-time); `ATTN_HARNESS_PROFILE`
  becomes an explicit override that defaults from `ATTN_PROFILE`.
- **One authority: `attn profile resolve`.** A Go command prints, for a profile, every
  resolved resource (data dir, socket, db, ws port, bundle id, app name + path, deep-link
  scheme, e2e ports) as JSON or a single `--field`. The Makefile, harness, e2e, and Rust
  build all *derive* from it instead of re-encoding the mapping. This deletes the
  four-place hand-sync — the root of the cross-entrypoint risk.
- **The packaged app is immutably bound to its build profile.** `apply_build_profile_env`
  (profile.rs:54) already pins `ATTN_PROFILE`/`ATTN_WS_PORT` and strips inherited routing
  env at startup, so `attn-agent7.app` can only ever be agent7. Keep and generalize this.
- **Safe defaults; prod is sacred.** Empty `ATTN_PROFILE` never causes a *test/harness* to
  touch prod (harness defaults to a named/dev profile, never prod-by-omission). Prod
  install/build refuses when `ATTN_PROFILE` is set (existing Makefile guard).
- **Discoverability is a feature, not a doc footnote.** A single status command and a
  per-command banner make the active profile and its resolved targets impossible to miss.

## The authority command (discoverability centerpiece)

```
attn profile resolve [--profile NAME] [--json | --field KEY]
    # NAME defaults to $ATTN_PROFILE. Prints every resolved resource.
attn profile status        # active profile + resources + health
    #   profile, dataDir, socket, wsPort, bundleId, appPath
    #   daemon: running? pid? port reachable?
    #   app:    installed? version vs source?
attn profile list          # every profile with a data dir and/or installed app
attn profile clean NAME    # stop daemon · rm data dir · rm app bundle · LaunchServices forget
                           # (refuses default/prod without an explicit flag)
```

`resolve` is the contract; `status`/`list`/`clean` are the human surface. Each new Go
helper (`BundleIdentifierForProfile`, `AppNameForProfile`, `AppPathForProfile`,
`DeepLinkSchemeForProfile`, `E2EDaemonPortForProfile`, `E2EVitePortForProfile`) lives once
in `internal/config` and is what `resolve` reports.

## Entrypoint matrix (after)

| Entrypoint | Picks profile via | Derives resources from |
|---|---|---|
| CLI / daemon | `ATTN_PROFILE` (today) | `config.*` (today) |
| Go unit/integration | n/a — `t.TempDir()` + explicit env (today) | n/a |
| Frontend e2e | `ATTN_PROFILE` (new); empty ⇒ today's 19849/1421 | `attn profile resolve` e2e ports |
| Real-app harness | `ATTN_PROFILE` (new); `ATTN_HARNESS_PROFILE` overrides; empty ⇒ safe default, never prod | `attn profile resolve` |
| `make` build/install | explicit `PROFILE=` arg | `attn profile resolve` (id, name, port, scheme) |
| Packaged app (runtime) | baked `ATTN_BUILD_PROFILE` ⇒ pins `ATTN_PROFILE` | `config.*` |

## Safety guardrails (enumerated)

1. **Prod-sacred (existing, keep):** `make {run,install,install-daemon}` errors at parse
   time if `ATTN_PROFILE` is set.
2. **Build/runtime consistency (new):** `make install PROFILE=X` refuses if `ATTN_PROFILE`
   is set and ≠ X. You cannot build agent7's app while your shell says you are agent8.
3. **Immutable bundle binding (existing, keep):** a profile app strips inherited
   socket/db/port env and pins its own at launch.
4. **Daemon isolation + match (existing, keep):** `ValidateDaemonIsolation` +
   daemonctl profile-match restart.
5. **Harness prod guard (existing, keep):** `assertProductionRunAllowed`; default target is
   never prod.
6. **e2e port bands are disjoint (new):** e2e daemon/Vite ports live in `[30000,30999]` /
   `[31000,31999]`, disjoint from prod 9849, dev 29849, the real-profile band
   `[20000,29848]`, and 1420/1421 — so an e2e throwaway daemon never collides with a
   *real* daemon of the same profile. Teardown `pkill` is scoped to the run's own port.
7. **Single authority (new):** every entrypoint derives from `attn profile resolve`, so the
   four-place mapping can no longer drift.

## Discoverability (enumerated)

- `attn profile status` — one command shows where you are and what it points at.
- Per-command banner `[attn profile=X socket=… port=…]` already prints for non-default
  profiles (banner.go:16); extend it to e2e/harness startup lines.
- Every Makefile install echoes `>>> Installing <profile>: <app> (port=…)`.
- One authoritative reference doc: `docs/profiles.md` (the recipe + lifecycle table +
  safety model), cross-linked from `AGENTS.md` and `app/scripts/real-app-harness/CLAUDE.md`.

## Lifecycle (the streamlined story)

| Step | Command |
|---|---|
| Pick | `attn profile-env agent7 \| source` (sets `ATTN_PROFILE=agent7`) |
| See | `attn profile status` |
| Go tests | `make test` (profile-agnostic; already parallel-safe) |
| e2e | `make test-e2e` (derives agent7 ports automatically) |
| Build/install app | `make install PROFILE=agent7` → `attn-agent7.app` / `com.attn.manager.agent7` |
| Real-app scenarios | `pnpm --dir app run real-app:…` (honors `ATTN_PROFILE`) |
| Restart daemon | auto on profile mismatch; `attn -s` / `daemon ensure` under the profile |
| Sign | uniform stable identity (`scripts/macos-codesign-identity.sh`); grants persist per bundle |
| Clean | `attn profile clean agent7` |

## Port bands

| Purpose | Default profile | Named profile |
|---|---|---|
| Real daemon WS | 9849 (prod) / 29849 (dev) | `derivedProfilePort` ∈ `[20000,29848]` |
| e2e throwaway daemon | 19849 | `30000 + fnv(name)%1000` |
| e2e Vite | 1421 | `31000 + fnv(name)%1000` |

Collisions between two named profiles are possible but rare; `strictPort` makes them fail
loudly rather than cross-wire.

## Phased PRs (small, all → main)

1. **Profile authority + discoverability (Go only, additive).** `internal/config`
   helpers (bundle id / app name / app path / deep-link scheme / e2e ports) + `attn profile
   resolve|status|list`. Tests. Land `docs/profiles.md` skeleton. *Low risk.*
2. **e2e honors the profile.** `playwright.config.ts` + `fixtures.ts` derive ports from the
   authority (empty ⇒ unchanged); scope the teardown kill. Concurrent two-daemon isolation
   integration test. AGENTS.md recipe. *Low risk.*
3. **Harness honors the one knob.** `harnessProfile.mjs` resolves via the authority;
   `ATTN_HARNESS_PROFILE` becomes an override defaulting from `ATTN_PROFILE`; arbitrary
   profiles supported; prod guard intact. Update real-app-harness CLAUDE.md. *Medium.*
4. **Parameterized per-profile build/install.** Makefile `PROFILE=` targets; generate the
   Tauri `--config` overlay (identifier/productName/scheme) from the authority instead of a
   committed per-profile file; Rust `bundle_identifier`/`default_port_for_build_profile`/
   deep-link derive for arbitrary profiles (or consume baked values); build/runtime
   consistency guard. *Large, code-signing-sensitive — likely split (Rust+config, then
   Makefile, then harness wiring).*
5. **Lifecycle: `attn profile clean` + orphan reaping.** Stop daemon, remove data dir + app
   bundle, LaunchServices forget; guards for default/prod. *Medium.*
6. **Docs polish.** Finalize `docs/profiles.md` as the obvious reference; weave into
   AGENTS.md. (Docs land incrementally with each PR; this closes gaps.)

## Open questions / risks

- **Per-bundle macOS grants.** Each new bundle id is a fresh app to macOS, so accessibility/
  automation grants are per-profile (one-time per agent). Stable signing makes them persist
  across rebuilds; we cannot share grants across distinct bundle ids. Acceptable for test
  agents — document it.
- **Deep-link scheme per profile.** Two apps registering `attn` would make macOS URL
  routing ambiguous, so each profile bundle should register `attn-<profile>`. Confirm no
  consumer assumes exactly `attn`/`attn-dev`.
- **LaunchServices proliferation.** Many `com.attn.manager.<agent>` registrations on a dev
  machine; `attn profile clean` must `lsregister -u` to keep it tidy.
- **Tauri 2 config overlay.** Prefer inline `--config '{json}'` (merged) over generating
  committed files; verify Tauri 2 merges identifier + productName + deep-link from inline.
- **e2e profile var.** Keying e2e off `ATTN_PROFILE` (vs a separate `ATTN_E2E_PROFILE`)
  chosen for one-knob simplicity; the throwaway daemon uses a temp dir so the profile is
  only a port namespace there.
