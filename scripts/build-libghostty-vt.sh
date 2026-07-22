#!/bin/bash
# Build the NATIVE libghostty-vt static library + headers used by the worker's
# server-authoritative terminal restore path (internal/ghosttyvt).
#
# Source of truth for third_party/ghostty-vt/ (gitignored). Reads its OWN pin
# (ghostty-vt-native.pin) — separate from the frontend WASM build's pin, because
# the Terminal C API this project needs does not exist at the WASM pin. See the
# pin file and docs/plans/2026-07-22-server-authoritative-terminal.md.
#
# Requires zig 0.16.x (installed via asdf alongside the WASM build's 0.15.x).
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/.." && pwd)"
pin_file="$repo_dir/ghostty-vt-native.pin"
patch_file="$repo_dir/ghostty-vt-native.patch"
output_dir="$repo_dir/third_party/ghostty-vt"

# The pin file may carry leading comment lines; take the first non-comment,
# non-blank line as the commit.
ghostty_commit="$(grep -vE '^\s*(#|$)' "$pin_file" | head -n1 | tr -d '[:space:]')"
if [[ -z "$ghostty_commit" ]]; then
  echo "error: no commit found in $pin_file" >&2
  exit 1
fi

zig="${ZIG:-$(command -v zig)}"
zig_version="$("$zig" version)"
case "$zig_version" in
  0.16.*) ;;
  *) echo "error: need zig 0.16.x, found $zig_version (asdf: 'asdf local zig 0.16.0')" >&2; exit 1 ;;
esac

sdkroot="${SDKROOT:-$(xcrun --show-sdk-path)}"
workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-libghostty-vt.XXXXXX")"

cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

echo "==> cloning ghostty @ $ghostty_commit"
git init -q "$workdir/ghostty"
git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
git -C "$workdir/ghostty" fetch -q --depth=1 origin "$ghostty_commit"
git -C "$workdir/ghostty" checkout -q --detach FETCH_HEAD

# Carried patches (e.g. exposing the primary-vs-alt ScreenFormatter through the
# C API). Applied only when present so the build works before the patch lands.
if [[ -f "$patch_file" ]]; then
  echo "==> applying $patch_file"
  git -C "$workdir/ghostty" apply "$patch_file"
fi

echo "==> zig build -Demit-lib-vt=true (zig $zig_version)"
pushd "$workdir/ghostty" >/dev/null
env SDKROOT="$sdkroot" "$zig" build \
  -Demit-lib-vt=true \
  -Dtarget=aarch64-macos \
  -Doptimize=ReleaseFast \
  --summary none \
  --prefix "$workdir/prefix"
popd >/dev/null

echo "==> installing to $output_dir"
rm -rf "$output_dir"
mkdir -p "$output_dir/lib" "$output_dir/include"
cp "$workdir/prefix/lib/libghostty-vt.a" "$output_dir/lib/"
cp -R "$workdir/prefix/include/ghostty" "$output_dir/include/"

echo "$ghostty_commit" > "$output_dir/.built-commit"
chmod -R u+w "$output_dir"
shasum -a 256 "$output_dir/lib/libghostty-vt.a"
echo "==> done: $output_dir"
