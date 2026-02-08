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
6. Updates `Formula/attn.rb` SHA/version for the new tag and pushes that commit.

The GitHub release workflow (`.github/workflows/release.yml`) builds and publishes macOS artifacts and uploads `attn_aarch64.dmg` for the Homebrew cask.
The cask itself stays `version :latest` and does not need per-release edits.

## Optional Fast Path

```bash
./scripts/release.sh v0.1.1 --skip-tests
```

Use only if CI or prior local verification already covered tests.

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
3. Verify install/upgrade:
   - `brew upgrade victorarias/attn/attn`
   - `brew upgrade --cask victorarias/attn/attn`
