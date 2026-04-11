# Release Guide

This guide is for maintainers publishing `attn` releases.

## One-Command Release

From a clean `main` branch:

```bash
./scripts/release.sh v0.1.1
```

or:

```bash
make release VERSION_TAG=v0.1.1
```

What it automates:

1. Bumps app version in:
   - `app/package.json`
   - `app/src-tauri/tauri.conf.json`
   - `app/src-tauri/Cargo.toml`
2. Refreshes lockfiles.
3. Runs validation (unless `--skip-tests`).
4. Creates release commit + annotated tag.
5. Pushes `main` and tag.

The GitHub release workflow (`.github/workflows/release.yml`) builds and publishes the macOS app artifacts, uploads `attn_aarch64.dmg` for the Homebrew cask, and attaches standalone Linux daemon binaries for `amd64` and `arm64`.
The cask itself stays `version :latest` and does not need per-release edits.
The macOS release job now requires these GitHub Actions secrets for Developer ID signing and notarization:

- `APPLE_CERTIFICATE`
- `APPLE_CERTIFICATE_PASSWORD`
- `KEYCHAIN_PASSWORD`
- `APPLE_API_ISSUER`
- `APPLE_API_KEY`
- `APPLE_API_KEY_P8`

## Optional Fast Path

```bash
./scripts/release.sh v0.1.1 --skip-tests
```

Use only if CI or prior local verification already covered tests.

## Branch Preflight

To verify the Linux release builds on GitHub-hosted runners without publishing a release, use the `Release Preflight` workflow.

It runs in branch/PR context and has no release side effects:

1. Builds the Linux daemon on:
   - `ubuntu-24.04` (`amd64`)
   - `ubuntu-24.04-arm` (`arm64`)
2. Verifies the compiled-in version via `attn --version`
3. Uploads the binaries as workflow artifacts instead of GitHub release assets

Suggested use:

1. Push your branch.
2. Open a PR against `main` to trigger `Release Preflight` against the branch with no release side effects.
3. Confirm both artifacts are produced:
   - `attn-linux-amd64`
   - `attn-linux-arm64`
4. Only then merge changes to `.github/workflows/release.yml`.

## Preconditions

- Working tree must be clean.
- Current branch must be `main`.
- Tag must not already exist locally or on `origin`.
- `origin` must be writable (push access).

## After Release

1. Confirm GitHub Actions release job succeeded.
2. Confirm release assets include:
   - versioned DMG
   - `attn_aarch64.dmg`
   - `attn-linux-amd64`
   - `attn-linux-arm64`
3. Verify install/upgrade:
   - `brew upgrade --cask victorarias/attn/attn`

## Re-run an Existing Tag

If the workflow changes after a tag already exists, rerun the release workflow manually:

1. Merge the workflow change to `main`.
2. Open the `Release` workflow in GitHub Actions.
3. Run `workflow_dispatch` with the existing tag, for example `v0.4.0`.
