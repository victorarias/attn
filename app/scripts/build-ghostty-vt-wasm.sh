#!/bin/bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
app_dir="$(cd "$script_dir/.." && pwd)"
output="$app_dir/vendor/ghostty-vt/ghostty-vt.wasm"
compat_patch="$app_dir/vendor/ghostty-vt/ghostty-web-v0.4.0-compat.patch"

ghostty_commit="29d4aba03337d7e9d6a0c357969e8924240b84dd"
ghostty_web_commit="9e4e126d89ac3537d2b2ebec075849851566de9f"
zig="${ZIG:-/opt/homebrew/bin/zig}"
sdkroot="${SDKROOT:-$(xcrun --show-sdk-path)}"
workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-ghostty-vt.XXXXXX")"

cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

git init "$workdir/ghostty"
git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
git -C "$workdir/ghostty" fetch --depth=1 origin "$ghostty_commit"
git -C "$workdir/ghostty" checkout --detach FETCH_HEAD

git init "$workdir/ghostty-web"
git -C "$workdir/ghostty-web" remote add origin https://github.com/coder/ghostty-web.git
git -C "$workdir/ghostty-web" fetch --depth=1 origin "$ghostty_web_commit"
git -C "$workdir/ghostty-web" checkout --detach FETCH_HEAD

git -C "$workdir/ghostty-web" show \
  "$ghostty_web_commit:patches/ghostty-wasm-api.patch" \
  | git -C "$workdir/ghostty" apply -
git -C "$workdir/ghostty" apply "$compat_patch"

pushd "$workdir/ghostty" >/dev/null
env SDKROOT="$sdkroot" "$zig" build lib-vt \
  -Dtarget=wasm32-freestanding \
  -Doptimize=ReleaseSmall \
  --summary none \
  --prefix "$workdir/prefix"
popd >/dev/null

cp "$workdir/prefix/bin/ghostty-vt.wasm" "$output"
chmod 0644 "$output"
shasum -a 256 "$output"
