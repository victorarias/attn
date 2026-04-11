# App-Owned Daemon And Install Surface

## Status

Draft plan describing the intended direction for daemon ownership, install surface simplification, and rollout.

## Why

The desktop app should be the primary product surface. Today the codebase still carries assumptions that the user may have installed or launched `attn` separately, and some startup logic still treats daemon liveness as sufficient even when the live daemon belongs to an older install path or build.

That creates the wrong ownership model:

- the app can silently reuse a stale daemon
- startup logic becomes path-heuristic-heavy
- upgrade behavior depends on old local state
- user-facing docs and errors leak internal/runtime details
- the CLI-only install path keeps shaping the architecture even though the app should own its runtime

The intended direction is simpler:

- users launch `attn.app`
- the app owns the daemon lifecycle
- the app uses its bundled sibling binary by default
- CLI-only installs are no longer a supported user install surface
- development and debugging can still override runtime paths, but only as an explicit dev flow

## Vision

### Product Surface

The supported end-user install surface is the desktop app.

- Homebrew cask, DMG installs, and source-installed app builds should converge on the same daemon ownership model.
- The bundled `attn` binary remains part of the app/runtime implementation and may still be used by agents and developer workflows.
- The project should stop treating the standalone `attn` CLI as something app users are expected to install or launch manually.
- The Homebrew formula / CLI-only install path should be removed as a supported user installation method in the next version.

### Daemon Ownership

The app should own daemon startup, replacement, and readiness checks.

- App startup should not search broadly for candidate `attn` binaries.
- App startup should resolve exactly one binary by default: the bundled sibling binary next to the app executable.
- A single explicit override may exist for development/debugging only.
- A live socket is not enough to accept a daemon. The running daemon must match the intended app-owned runtime identity.

### Identity Model

For normal app installs, daemon identity should be based on source fingerprint.

- `source_fingerprint` is the canonical match key for normal app/runtime ownership.
- Older daemons that do not expose fingerprint should be treated as old/unknown and replaced.
- Separate protocol matching should not be the normal identity mechanism once fingerprint is available.
- Development mode can intentionally point the app at a different daemon binary without repackaging the app, but that must be explicit and ergonomic, aiming a seamless devxp.

### Readiness

Daemon readiness should require both:

- socket live
- successful `/health`

Socket-only readiness is not sufficient.

## Target Architecture

### Bundled Runtime Default

The app should resolve its daemon binary using this default rule:

1. Explicit dev/debug override if present.
2. Otherwise, the bundled sibling binary next to the app executable.
3. Otherwise, fail clearly.

`~/.local/bin/attn` should not be part of normal desktop app startup resolution.

### Single Ensure Entry Point

The long-term startup model should be:

1. App resolves its bundled runtime binary.
2. App asks that binary to ensure the correct daemon is running.
3. App connects only after ensure succeeds.

Proposed command shape:

```bash
attn daemon ensure --expected-protocol <protocol>
```

The command should:

1. Detect whether a daemon is already live.
2. Inspect the live daemon over stable surfaces.
3. Compare the running daemon identity against the invoking binary.
4. Reuse the daemon if it matches.
5. Replace the daemon if it does not match or cannot be trusted.
6. Wait until socket + `/health` are both good before returning success.

The new binary owns this reconciliation. Older daemons do not need to cooperate beyond being killable.

### Older-Version Upgrade Behavior

Upgrading across versions should be one-way and owned by the newly installed binary.

- The new bundled binary must be able to replace an older live daemon.
- If the older daemon exposes usable health metadata, compare against it.
- If the older daemon does not expose fingerprint, or health is unreadable, treat it as old/unknown and replace it.
- Daemons are safe to restart. Session continuity should not block replacement.

### Development Override

Development must remain seamless.

- Rebuilding only the daemon should remain a normal fast dev loop.
- Devs should not need to remember long commands or manually wire environment variables.
- The development override should be reachable through a documented dev setting and/or simple `make` target.
- This override exists only for development/debugging and should not shape the normal production startup path.

## Rollout Plan

### Phase 1: Land The App-Owned Path

The next release should:

- make the app-owned runtime path the primary startup path
- move toward bundled-binary ownership by default
- remove CLI-only install support from user-facing distribution/docs
- keep a temporary fallback for unknown old state

### Phase 2: Temporary Fallback

The first release with the new model should keep a clearly isolated temporary fallback path.

Fallback requirements:

- it should be visibly marked temporary in code
- it should be simple and blunt, not compatibility-rich
- it should prefer recovering a working app over preserving unusual old local state
- it may kill the old daemon process and reset daemon socket/pid state if needed
- it should not clear worker sockets/registries

The fallback exists only to prevent users from getting stranded during the transition from older installs.

### Phase 3: Remove Temporary Fallback

After the new model has baked for a short period, remove the fallback entirely.

- The fallback should not become permanent architecture.
- The code should make that temporary status obvious.
- Removal should be a deliberate follow-up cleanup, not an indefinite “later”.

## Install Surface Simplification

### User Install Surface

The next version should stop supporting CLI-only installs as a user path.

- Remove the Homebrew formula / CLI-only install method from supported user guidance.
- Remove CLI-only installation support as a product path.
- Keep the app installs as the supported distribution surfaces.

### Developer Install Surface

Development workflows should stay supported and become simpler.

- `make install` should become the default local development install path and install the full app bundle plus bundled daemon/runtime.
- `make install-daemon` should be the explicit fast path for daemon-only iteration when the app bundle itself does not need rebuilding.
- The `make` surface should be simplified overall so local development does not require remembering multiple overlapping install modes.

`make install` may still be a correct instruction for development/source installs. If it is not currently the right default, it should be changed until it is.

## User-Facing Guidance

App-facing UX should stop leaking CLI-oriented recovery instructions.

- Do not steer app users toward installing or invoking the Homebrew formula / standalone CLI.
- Do not frame the CLI as part of the normal app flow.
- Keep the `attn` binary available internally and for developer workflows.
- Review app-facing errors, onboarding, and recovery messaging so they match the app-owned model.

This does not require removing the `attn` binary itself. It requires stopping the product from treating it as a normal user-facing surface.

## Success Criteria

The work is successful when:

- the desktop app always uses its bundled sibling binary by default
- a newly installed app reliably replaces older daemons without relying on user CLI installs
- startup accepts a daemon only after socket + `/health`
- fingerprint is the canonical normal-mode identity check
- older daemons without fingerprint are replaced automatically
- CLI-only install support is removed from the user-facing install story
- development overrides remain easy and intentional
- the temporary fallback is isolated, obvious, and later removed
- app-facing docs and errors reflect app ownership rather than CLI ownership

## Cleanup Checklist

When the architecture is fully in place, remove or simplify:

- Tauri-side daemon reconciliation logic that duplicates CLI/runtime ownership
- normal app startup paths that search multiple possible daemon binaries
- user-facing references to the Homebrew formula / CLI-only install path
- overlapping or confusing `make install*` developer commands
- the temporary fallback path introduced for the migration

## Non-Goals

This plan does not require:

- removing the bundled `attn` binary
- removing the CLI binary from the repository entirely
- renaming the internal runtime binary
- clearing worker socket/registry state as part of the migration fallback

## Open Implementation Notes

These are implementation notes, not unresolved product questions:

- The runtime may still need an explicit development/debug override surface.
- The app and the runtime should share one source of truth for daemon reconciliation over time.
- The long-term goal is to minimize path heuristics and runtime duplication at app startup.
