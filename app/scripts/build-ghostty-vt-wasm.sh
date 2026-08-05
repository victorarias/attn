#!/bin/bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
app_dir="$(cd "$script_dir/.." && pwd)"
repo_dir="$(cd "$app_dir/.." && pwd)"
output="$app_dir/vendor/ghostty-vt/ghostty-vt.wasm"
compat_patch="$app_dir/vendor/ghostty-vt/ghostty-web-v0.4.0-compat.patch"
compat_source="$app_dir/vendor/ghostty-vt/ghostty-web-v0.4.0-compat.zig"
lock_script="$script_dir/ghostty-vt-wasm-lock.sh"
pin_file="$repo_dir/ghostty-vt.pin"

ghostty_commit="$(grep -vE '^\s*(#|$)' "$pin_file" | head -n1 | tr -d '[:space:]')"
if [[ -z "$ghostty_commit" ]]; then
  echo "error: no commit found in $pin_file" >&2
  exit 1
fi

zig="${ZIG:-}"
if [[ -z "$zig" ]] && command -v asdf >/dev/null 2>&1; then
  zig="$(asdf which zig 2>/dev/null || true)"
fi
if [[ -z "$zig" ]]; then
  zig="$(command -v zig || true)"
fi
if [[ -z "$zig" ]]; then
  echo "error: zig not found; need zig 0.16.x" >&2
  exit 1
fi
case "$("$zig" version)" in
  0.16.*) ;;
  *) echo "error: need zig 0.16.x, found $("$zig" version)" >&2; exit 1 ;;
esac
zig_version="$("$zig" version)"

workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-ghostty-vt.XXXXXX")"

cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

git init -q "$workdir/ghostty"
git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
git -C "$workdir/ghostty" fetch -q --depth=1 origin "$ghostty_commit"
git -C "$workdir/ghostty" checkout -q --detach FETCH_HEAD

git -C "$workdir/ghostty" apply "$compat_patch"
cp "$compat_source" "$workdir/ghostty/src/terminal/c/wasm_compat.zig"

pushd "$workdir/ghostty" >/dev/null
"$zig" build \
  -Demit-lib-vt=true \
  -Dtarget=wasm32-freestanding \
  -Doptimize=ReleaseSmall \
  --summary none \
  --prefix "$workdir/prefix"
popd >/dev/null

cp "$workdir/prefix/bin/ghostty-vt.wasm" "$output"
chmod 0644 "$output"
bash "$lock_script" write "$zig_version"
bash "$lock_script" verify
