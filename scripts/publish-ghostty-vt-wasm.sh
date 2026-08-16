#!/bin/bash
# Mirror ghostty-org's prebuilt ghostty-vt.wasm for the pinned commit onto our
# rolling release, then rewrite ghostty-vt-wasm.lock. Run this whenever you move
# the shared ghostty-vt.pin.
#
#   make publish-ghostty-vt-wasm     # or: ./scripts/publish-ghostty-vt-wasm.sh
#
# Upstream publishes the module on its "tip" release, which is rebuilt on every
# commit to main — the asset for our pin stops being downloadable the moment
# main moves. So this fetches it, checks it really is the pinned commit's build,
# republishes it under a pin-keyed name we control, and locks its sha256. COMMIT
# the updated lock; that is what makes every checkout re-fetch.
#
# THE PIN MUST BE UPSTREAM'S CURRENT TIP when you run this. Mirroring an older
# commit is not possible — the bytes are gone. Bump the pin to the commit tip
# was built from and mirror in the same sitting.
#
# Requires: `gh` authenticated with write access to the repo.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ghostty-vt-wasm.sh"

command -v gh >/dev/null || { echo "error: gh CLI required to publish" >&2; exit 1; }

pin="$(vtw_commit)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/attn-vtw-pub.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> checking upstream $VTW_UPSTREAM_TAG was built from $pin"
tip_commit="$(gh release view "$VTW_UPSTREAM_TAG" --repo "$VTW_UPSTREAM_REPO" \
  --json targetCommitish -q .targetCommitish)"
if [[ "$tip_commit" != "$pin" ]]; then
  echo "error: upstream $VTW_UPSTREAM_TAG is built from $tip_commit, not the pinned $pin" >&2
  echo "       upstream keeps no per-commit wasm assets, so the pinned commit's" >&2
  echo "       module cannot be recovered. Move ghostty-vt.pin to $tip_commit" >&2
  echo "       (and re-mirror the native archives for it) or wait for a tip you want." >&2
  exit 1
fi

echo "==> downloading $VTW_UPSTREAM_ASSET from $VTW_UPSTREAM_REPO@$VTW_UPSTREAM_TAG"
gh release download "$VTW_UPSTREAM_TAG" --repo "$VTW_UPSTREAM_REPO" \
  --pattern "$VTW_UPSTREAM_ASSET" --dir "$tmp" --clobber

asset="$(vtw_asset_name)"
mv "$tmp/$VTW_UPSTREAM_ASSET" "$tmp/$asset"

if ! gh release view "$VTW_RELEASE_TAG" --repo "$VTW_REPO" >/dev/null 2>&1; then
  echo "==> creating release $VTW_RELEASE_TAG"
  gh release create "$VTW_RELEASE_TAG" --repo "$VTW_REPO" \
    --title "Prebuilt libghostty-vt" \
    --notes "Prebuilt libghostty-vt artifacts. Do not delete assets — older commits still reference them."
fi

echo "==> uploading $asset to $VTW_RELEASE_TAG"
gh release upload "$VTW_RELEASE_TAG" "$tmp/$asset" --repo "$VTW_REPO" --clobber

mkdir -p "$(dirname "$vtw_output")"
cp "$tmp/$asset" "$vtw_output"
chmod 0644 "$vtw_output"

echo "==> writing $vtw_lock_file"
vtw_write_lock

echo
echo "mirrored ghostty-vt.wasm for pin $pin"
echo "  sha256 $(vtw_sha256 "$vtw_output")"
echo "  commit ghostty-vt-wasm.lock to make every checkout re-fetch."
