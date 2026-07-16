# OpenCode bundled distribution

## Goal

Ship the first-party OpenCode adapter inside every attn app bundle while keeping
it inert until the user explicitly installs it for that profile. Installation
must not copy the artifact or require Bun, and uninstall must preserve plugin
data and refuse to strand an active delegated run.

## Architecture Map

```text
Build:
plugins/attn-opencode/src/index.ts
  -> Bun standalone compile
    -> staged Resources/plugins/attn-opencode/{manifest,README,bin}
      -> sign nested executable -> sign app

Runtime:
bundled root + profile user-plugin root + installed_bundled_plugins setting
  -> merged catalog (bundled Available/Installed, user Installed)
    -> supervisor launches only user plugins + opted-in bundled plugins
      -> bun source entrypoint OR direct executable entrypoint

Actions:
Settings / CLI
  -> install_bundled_plugin(name)
    -> reject user collision -> persist allowlist -> start immediately
  -> uninstall_plugin(name)
    -> reject active owned runs -> stop/unregister -> update allowlist
    -> bundled row returns to Available; user row/files are removed

Tests:
temp bundled/user roots + in-memory store + fake supervisor launcher
  -> catalog/lifecycle/protocol tests
packaged non-production profiles
  -> bundle/signature/inert-by-default/profile-scope/live OpenCode flow
```

## Data Model / Interfaces

```text
Manifest.Plugin
  kind: "bun" | "executable"  // legacy entrypoint implies bun
  path: relative path

PluginCatalogItem
  manifest, availability(bundled|user), installationState

SettingInstalledBundledPlugins
  JSON string array; empty/missing means no bundled opt-ins

PluginInfo (daemon -> UI/CLI)
  availability, installation_state, runtime_state
  can_install, can_uninstall
  existing health/supervisor diagnostics
```

## Boundaries

- `internal/plugins` validates both manifest forms and discovers each root; it
  does not decide profile installation state.
- The daemon owns catalog merging, the durable bundled allowlist, collision and
  active-run guards, and supervisor lifecycle.
- The supervisor owns process execution and switches only on the validated
  manifest entrypoint kind.
- Build scripts own compilation, staging, nested signing, and package
  consistency. Bundled resources remain read-only at runtime.
- Settings owns only request-local installing/uninstalling state.

## Execution

- [x] Generalize manifest validation and process launch for Bun and executable
  entrypoints with compatibility tests.
- [x] Add bundled-root resolution, merged catalog discovery, installed allowlist,
  collision handling, and startup filtering.
- [x] Add atomic install/uninstall actions, active-run guard, and user-plugin
  compatibility.
- [x] Extend TypeSpec/generated types, CLI parity, Settings controls, and bump
  the daemon protocol.
- [x] Compile/stage/sign the OpenCode executable in app builds and verify
  version consistency and source fingerprints.
- [x] Update documentation and changelog.
- [x] Run automated, packaging, and isolated multi-profile live-app verification.
- [ ] Open the PR, address Figgyster review, and merge only after current-head
  approval and green checks.

## Decisions

- Bundled describes artifact availability, never automatic installation.
- Profile opt-in is an allowlist in the existing settings store; the executable
  always runs from the app resource directory.
- Preserve the current `entrypoint` TOML shorthand as Bun source while accepting
  explicit `kind`/`path` for packaged executables.
- Same-name user installs block bundled installation; no implementation swaps
  happen implicitly.
- Bundled uninstall is state-only and preserves private plugin data.

## Verification

- `go test ./...`
- `pnpm --dir app test` (1,768 tests)
- `pnpm --dir app exec tsc --noEmit`
- `bun test --cwd plugins/attn-opencode` (64 tests, 179 assertions)
- Shell syntax checks for the app, bundled-plugin, and source-fingerprint build
  scripts.
- `cargo test` (25 tests), including validation of the tracked Tauri resource
  root used before generated bundled artifacts exist in a clean checkout.
- Built and signed isolated `bundle-a` and `bundle-b` app profiles. A fresh
  `bundle-b` listed OpenCode as Available and stopped without launching it.
- In `bundle-a`, installed OpenCode from the signed app resource, completed a
  real OpenCode 1.17.18 session (`BUNDLED_OK`), reloaded and resumed the same
  native session (`RESUME_OK`), rejected uninstall during the active run, then
  closed and uninstalled it while preserving its run registry and app resource.
- Reinstalled the `bundle-a` app while OpenCode was opted in and verified that
  the profile retained its installation choice and reconnected the plugin.

## Follow-ups

- Remote/third-party catalogs and destructive plugin-data removal remain
  separate work.
