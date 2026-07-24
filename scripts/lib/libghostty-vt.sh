#!/bin/bash
# Shared helpers for the NATIVE libghostty-vt static library used by the
# worker's server-authoritative terminal restore path (internal/ghosttyvt).
#
# Sourced by scripts/build-libghostty-vt.sh (download-first consumer path) and
# scripts/publish-libghostty-vt.sh (maintainer build+upload path). See the pin
# file, the lock file, and docs/plans/2026-07-22-server-authoritative-terminal.md.
#
# This file only defines functions and read-only vars; it performs no work and
# is safe to source. Callers must set -euo pipefail themselves.

# This file lives at <repo>/scripts/lib/libghostty-vt.sh; repo root is two up.
vt_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
vt_repo_dir="$(cd "$vt_lib_dir/../.." && pwd)"
vt_pin_file="$vt_repo_dir/ghostty-vt-native.pin"
vt_patch_file="$vt_repo_dir/ghostty-vt-native.patch"
vt_lock_file="$vt_repo_dir/ghostty-vt-native.lock"
vt_output_dir="$vt_repo_dir/third_party/ghostty-vt"
vt_lib="$vt_output_dir/lib/libghostty-vt.a"

# The single rolling GitHub release that accumulates keyed prebuilt assets.
# Old keys stay downloadable for anyone on an older commit. Never collides with
# the app's version releases (attn vX.Y.Z).
VT_RELEASE_TAG="native-vt-prebuilts"
VT_REPO="victorarias/attn"

# The ghostty commit from the pin file (first non-comment, non-blank line).
vt_commit() {
  local commit
  commit="$(grep -vE '^\s*(#|$)' "$vt_pin_file" | head -n1 | tr -d '[:space:]')"
  if [[ -z "$commit" ]]; then
    echo "error: no commit found in $vt_pin_file" >&2
    return 1
  fi
  printf '%s' "$commit"
}

# Identity of the produced artifact: sha256 over the pin commit AND the carried
# patch. Both determine the bytes, so the key changes when either moves — which
# is how a locally-edited pin/patch is detected as "no published asset yet" and
# routed to a source build. The asset filename embeds this key.
vt_key() {
  {
    vt_commit
    printf '\n'
    [[ -f "$vt_patch_file" ]] && cat "$vt_patch_file"
  } | shasum -a 256 | cut -d' ' -f1
}

vt_asset_name() {
  printf 'libghostty-vt-%s.tar.gz' "$(vt_key)"
}

# Read a `key=value` field from the lock file; empty if absent.
vt_lock_field() {
  local field="$1"
  [[ -f "$vt_lock_file" ]] || return 0
  grep -E "^${field}=" "$vt_lock_file" | head -n1 | cut -d= -f2- | tr -d '[:space:]'
}

# Build the archive + headers from pinned source into $vt_output_dir. Requires
# zig 0.16.x and network access to clone ghostty. This is the maintainer path
# and the consumer fallback; ordinary contributors never reach it.
vt_build_from_source() {
  local commit sdkroot workdir zig zig_version
  commit="$(vt_commit)"

  zig="${ZIG:-$(command -v zig || true)}"
  if [[ -z "$zig" ]]; then
    echo "error: zig not found on PATH; need zig 0.16.x to build libghostty-vt from source" >&2
    echo "       (consumers normally download a prebuilt asset — this path only runs when" >&2
    echo "        you have changed ghostty-vt-native.pin/.patch or forced ATTN_VT_FROM_SOURCE=1)" >&2
    return 1
  fi
  zig_version="$("$zig" version)"
  case "$zig_version" in
    0.16.*) ;;
    *) echo "error: need zig 0.16.x, found $zig_version (asdf: 'asdf local zig 0.16.0')" >&2; return 1 ;;
  esac

  sdkroot="${SDKROOT:-$(xcrun --show-sdk-path)}"
  workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-libghostty-vt.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$workdir'" RETURN

  echo "==> cloning ghostty @ $commit"
  git init -q "$workdir/ghostty"
  git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
  git -C "$workdir/ghostty" fetch -q --depth=1 origin "$commit"
  git -C "$workdir/ghostty" checkout -q --detach FETCH_HEAD

  # Carried patches (e.g. exposing the primary-vs-alt ScreenFormatter through the
  # C API). Applied only when present so the build works before the patch lands.
  if [[ -f "$vt_patch_file" ]]; then
    echo "==> applying $vt_patch_file"
    git -C "$workdir/ghostty" apply "$vt_patch_file"
  fi

  echo "==> zig build -Demit-lib-vt=true (zig $zig_version)"
  (
    cd "$workdir/ghostty"
    env SDKROOT="$sdkroot" "$zig" build \
      -Demit-lib-vt=true \
      -Dtarget=aarch64-macos \
      -Doptimize=ReleaseFast \
      --summary none \
      --prefix "$workdir/prefix"
  )

  echo "==> installing to $vt_output_dir"
  rm -rf "$vt_output_dir"
  mkdir -p "$vt_output_dir/lib" "$vt_output_dir/include"
  cp "$workdir/prefix/lib/libghostty-vt.a" "$vt_output_dir/lib/"
  cp -R "$workdir/prefix/include/ghostty" "$vt_output_dir/include/"
  # Ghostty is MIT-licensed; carry its LICENSE so redistributed prebuilts keep
  # attribution.
  [[ -f "$workdir/ghostty/LICENSE" ]] && cp "$workdir/ghostty/LICENSE" "$vt_output_dir/LICENSE"

  vt_key > "$vt_output_dir/.built-key"
  chmod -R u+w "$vt_output_dir"
}

# Pack the installed archive + headers + LICENSE into a tarball whose members are
# rooted at ghostty-vt/ (so a consumer extracts it straight into third_party/).
vt_pack() {
  local dest="$1"
  tar -C "$vt_repo_dir/third_party" -czf "$dest" \
    ghostty-vt/lib ghostty-vt/include \
    $([[ -f "$vt_output_dir/LICENSE" ]] && echo ghostty-vt/LICENSE)
}
