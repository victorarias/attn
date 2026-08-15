#!/bin/bash
# Ensure app/vendor/ghostty-vt/ghostty-vt.wasm (gitignored) is present and is
# the module the lock names. Runs ahead of every frontend dev/build/test.
#
# DOWNLOAD-AND-VERIFY, FAIL CLOSED: the binary is ghostty-org's own ReleaseFast
# build, mirrored onto our rolling release keyed by the shared ghostty-vt.pin.
# Nothing here builds anything — there is no zig in the frontend toolchain. A
# missing or mismatched module stops the build rather than letting a stale
# terminal core reach a bundle.
#
# Maintainers move the pin and re-mirror with `make publish-ghostty-vt-wasm`.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/scripts/lib/ghostty-vt-wasm.sh"

want_sha="$(vtw_lock_field sha256)"
lock_pin="$(vtw_lock_field pin)"
pin="$(vtw_commit)"

if [[ -z "$want_sha" || -z "$lock_pin" ]]; then
  echo "error: $vtw_lock_file is missing pin/sha256" >&2
  echo "       regenerate with: make publish-ghostty-vt-wasm" >&2
  exit 1
fi

# The pin moved but nobody mirrored the new commit's module. Downloading would
# 404 and, worse, a stale local file would silently pass its own sha check.
if [[ "$pin" != "$lock_pin" ]]; then
  echo "error: ghostty-vt.pin ($pin) does not match ghostty-vt-wasm.lock ($lock_pin)" >&2
  echo "       the browser and the worker must parse VT with one implementation." >&2
  echo "       mirror the new pin with: make publish-ghostty-vt-wasm" >&2
  exit 1
fi

if [[ -f "$vtw_output" && "$(vtw_sha256 "$vtw_output")" == "$want_sha" ]]; then
  echo "ghostty-vt.wasm verified (pin ${pin:0:12})"
  exit 0
fi

asset="$(vtw_asset_name)"
url="https://github.com/${VTW_REPO}/releases/download/${VTW_RELEASE_TAG}/${asset}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/attn-vtw-dl.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading ghostty-vt.wasm (pin ${pin:0:12})"
echo "    $url"
if ! curl -fL --retry 3 --retry-delay 1 -o "$tmp/$asset" "$url"; then
  echo "error: could not download $asset" >&2
  echo "       if you just moved ghostty-vt.pin, mirror it first:" >&2
  echo "       make publish-ghostty-vt-wasm" >&2
  exit 1
fi

got_sha="$(vtw_sha256 "$tmp/$asset")"
if [[ "$got_sha" != "$want_sha" ]]; then
  echo "error: sha256 mismatch for $asset" >&2
  echo "       locked: $want_sha" >&2
  echo "       actual: $got_sha" >&2
  exit 1
fi

mkdir -p "$(dirname "$vtw_output")"
cp "$tmp/$asset" "$vtw_output"
chmod 0644 "$vtw_output"
echo "ghostty-vt.wasm verified (pin ${pin:0:12})"
