#!/bin/bash
# Ensure the NATIVE libghostty-vt static library + headers exist under
# third_party/ghostty-vt/ (gitignored), used by the worker's server-authoritative
# terminal restore path (internal/ghosttyvt).
#
# DOWNLOAD-FIRST: ordinary contributors never build from source. The default
# path fetches a prebuilt asset (keyed by pin+patch) from the repo's rolling
# release and verifies it against ghostty-vt-native.lock. A source build (zig
# 0.16.x, see scripts/lib/libghostty-vt.sh) runs only when there is no published
# asset for the current key — i.e. you have edited ghostty-vt-native.pin/.patch —
# or when ATTN_VT_FROM_SOURCE=1 forces it. Maintainers publish new assets with
# `make publish-native-vt` (scripts/publish-libghostty-vt.sh).
#
# See the pin/lock files and docs/plans/2026-07-22-server-authoritative-terminal.md.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/libghostty-vt.sh"

key="$(vt_key)"

# Already current? (make usually guards this via mtime, but stay idempotent.)
if [[ -f "$vt_lib" && "$(cat "$vt_output_dir/.built-key" 2>/dev/null)" == "$key" ]]; then
  exit 0
fi

build_from_source() {
  vt_build_from_source
  echo "==> done (source build): $vt_output_dir"
  shasum -a 256 "$vt_lib"
}

if [[ "${ATTN_VT_FROM_SOURCE:-}" == "1" ]]; then
  echo "==> ATTN_VT_FROM_SOURCE=1: building from source"
  build_from_source
  exit 0
fi

lock_key="$(vt_lock_field key)"
lock_sha="$(vt_lock_field sha256)"

# A locally-edited pin/patch has no published asset yet (its key won't match the
# committed lock). Skip the guaranteed-404 and build from source directly.
if [[ -z "$lock_key" || "$key" != "$lock_key" ]]; then
  echo "==> no published asset for key ${key:0:12} (pin/patch edited locally?); building from source"
  build_from_source
  exit 0
fi

asset="$(vt_asset_name)"
url="https://github.com/${VT_REPO}/releases/download/${VT_RELEASE_TAG}/${asset}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/attn-vt-dl.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading prebuilt libghostty-vt (key ${key:0:12})"
echo "    $url"
if ! curl -fL --retry 3 --retry-delay 1 -o "$tmp/$asset" "$url"; then
  echo "warning: download failed; falling back to source build" >&2
  build_from_source
  exit 0
fi

# Fail closed on a corrupt/tampered/stale download, then fall back to source.
if ! echo "${lock_sha}  $tmp/$asset" | shasum -a 256 -c - >/dev/null 2>&1; then
  echo "warning: sha256 mismatch for $asset (expected $lock_sha); falling back to source build" >&2
  build_from_source
  exit 0
fi

echo "==> verified; extracting into $vt_output_dir"
rm -rf "$vt_output_dir"
mkdir -p "$vt_repo_dir/third_party"
tar -xzf "$tmp/$asset" -C "$vt_repo_dir/third_party"
printf '%s' "$key" > "$vt_output_dir/.built-key"
echo "==> done (prebuilt): $vt_output_dir"
shasum -a 256 "$vt_lib"
