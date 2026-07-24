#!/bin/bash
# Ensure the NATIVE libghostty-vt static library + headers exist under
# third_party/ghostty-vt/<goos>_<goarch>/ (gitignored), used by the worker's
# server-authoritative terminal restore path (internal/ghosttyvt).
#
# DOWNLOAD-FIRST: ordinary contributors never build from source. The default
# path fetches the prebuilt asset for the TARGET platform (keyed by pin+patch)
# from the repo's rolling release and verifies it against ghostty-vt-native.lock.
# A source build (zig 0.16.x, see scripts/lib/libghostty-vt.sh) runs only when
# there is no published asset for the current key — i.e. you have edited
# ghostty-vt-native.pin/.patch — or when ATTN_VT_FROM_SOURCE=1 forces it.
# Maintainers publish new assets with `make publish-native-vt`.
#
# The target platform defaults to the effective Go target; the Makefile passes
# GHOSTTY_VT_GOOS/GOARCH when cross-building a Linux daemon from a Mac.
#
# See the pin/lock files and docs/plans/2026-07-22-server-authoritative-terminal.md.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/libghostty-vt.sh"

platform="$(vt_default_platform)"
output_dir="$(vt_output_dir_for "$platform")"
lib="$(vt_lib_for "$platform")"
key="$(vt_key)"

# Already current? (make usually guards this via mtime, but stay idempotent.)
if [[ -f "$lib" && "$(cat "$output_dir/.built-key" 2>/dev/null)" == "$key" ]]; then
  exit 0
fi

build_from_source() {
  vt_build_from_source "$platform"
  echo "==> done (source build): $output_dir"
  shasum -a 256 "$lib"
}

if [[ "${ATTN_VT_FROM_SOURCE:-}" == "1" ]]; then
  echo "==> ATTN_VT_FROM_SOURCE=1: building $platform from source"
  build_from_source
  exit 0
fi

lock_key="$(vt_lock_field key)"
lock_sha="$(vt_lock_field "sha256_${platform}")"

# A locally-edited pin/patch has no published asset yet (its key won't match the
# committed lock). Skip the guaranteed-404 and build from source directly.
if [[ -z "$lock_key" || "$key" != "$lock_key" ]]; then
  echo "==> no published asset for key ${key:0:12} (pin/patch edited locally?); building $platform from source"
  build_from_source
  exit 0
fi

# The key matches but this platform was never published (partial lock): nothing
# to verify against, so build it from source rather than trust an unpinned blob.
if [[ -z "$lock_sha" ]]; then
  echo "==> key ${key:0:12} has no published $platform asset (lock missing sha256_${platform}); building from source"
  build_from_source
  exit 0
fi

asset="$(vt_asset_name_for "$platform")"
url="https://github.com/${VT_REPO}/releases/download/${VT_RELEASE_TAG}/${asset}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/attn-vt-dl.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading prebuilt libghostty-vt ($platform, key ${key:0:12})"
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

echo "==> verified; extracting into $output_dir"
rm -rf "$output_dir"
mkdir -p "$vt_repo_dir/third_party"
tar -xzf "$tmp/$asset" -C "$vt_repo_dir/third_party"
printf '%s' "$key" > "$output_dir/.built-key"
echo "==> done (prebuilt): $output_dir"
shasum -a 256 "$lib"
