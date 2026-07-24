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
#
# PER-PLATFORM: the library links into the daemon on darwin/arm64 AND the two
# Linux tuples (the daemon runs headless on Linux; only the Tauri app is
# Mac-only). Every prebuilt asset, output dir, and lock sha256 is keyed by
# <goos>_<goarch>; the source identity `key` (sha256 of pin+patch) is shared
# across platforms because the same source produces every target.

# This file lives at <repo>/scripts/lib/libghostty-vt.sh; repo root is two up.
vt_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
vt_repo_dir="$(cd "$vt_lib_dir/../.." && pwd)"
vt_pin_file="$vt_repo_dir/ghostty-vt-native.pin"
vt_patch_file="$vt_repo_dir/ghostty-vt-native.patch"
vt_lock_file="$vt_repo_dir/ghostty-vt-native.lock"

# Every native target that links the real cgo library. Keep in lockstep with the
# widened //go:build constraint in internal/ghosttyvt and the per-tuple #cgo
# directives in ghosttyvt.go. `publish` builds+uploads all of these.
VT_PLATFORMS=(darwin_arm64 linux_amd64 linux_arm64)

# The single rolling GitHub release that accumulates keyed prebuilt assets.
# Old keys stay downloadable for anyone on an older commit. Never collides with
# the app's version releases (attn vX.Y.Z).
VT_RELEASE_TAG="native-vt-prebuilts"
VT_REPO="victorarias/attn"

# The platform (goos_goarch) a consumer build targets. Honors the explicit
# GHOSTTY_VT_GOOS/GOARCH the Makefile passes when cross-building; otherwise the
# effective Go target (`go env` already folds in any GOOS/GOARCH in the env).
vt_default_platform() {
  local goos goarch
  goos="${GHOSTTY_VT_GOOS:-$(go env GOOS)}"
  goarch="${GHOSTTY_VT_GOARCH:-$(go env GOARCH)}"
  printf '%s_%s' "$goos" "$goarch"
}

vt_platform_goos()   { printf '%s' "${1%%_*}"; }
vt_platform_goarch() { printf '%s' "${1##*_}"; }

# zig -Dtarget / `zig cc -target` triple for a platform tuple. Linux tuples use
# zig's bundled libc (no SDK); darwin needs the macOS SDK via SDKROOT.
vt_zig_target() {
  case "$1" in
    darwin_arm64) printf 'aarch64-macos' ;;
    darwin_amd64) printf 'x86_64-macos' ;;
    linux_amd64)  printf 'x86_64-linux-gnu' ;;
    linux_arm64)  printf 'aarch64-linux-gnu' ;;
    *) echo "error: unsupported native VT platform '$1'" >&2; return 1 ;;
  esac
}

# Per-platform install locations. The archive+headers for each target live in
# their own subdir so multiple tuples can coexist in one checkout (e.g. a Mac
# hub cross-building a Linux daemon). The #cgo directives point at these paths.
vt_output_dir_for() { printf '%s/third_party/ghostty-vt/%s' "$vt_repo_dir" "$1"; }
vt_lib_for()        { printf '%s/lib/libghostty-vt.a' "$(vt_output_dir_for "$1")"; }

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

# Source identity of the produced artifacts: sha256 over the pin commit AND the
# carried patch. Both determine the bytes, so the key changes when either moves —
# which is how a locally-edited pin/patch is detected as "no published asset yet"
# and routed to a source build. Platform-independent: the same source builds
# every target, so one key covers all tuples; the asset filename adds the tuple.
vt_key() {
  {
    vt_commit
    printf '\n'
    [[ -f "$vt_patch_file" ]] && cat "$vt_patch_file"
  } | shasum -a 256 | cut -d' ' -f1
}

# Asset filename for a platform: libghostty-vt-<key>-<goos>_<goarch>.tar.gz.
vt_asset_name_for() {
  printf 'libghostty-vt-%s-%s.tar.gz' "$(vt_key)" "$1"
}

# Read a `key=value` field from the lock file; empty if absent. Per-platform
# sha256 fields are named sha256_<goos>_<goarch>.
vt_lock_field() {
  local field="$1"
  [[ -f "$vt_lock_file" ]] || return 0
  grep -E "^${field}=" "$vt_lock_file" | head -n1 | cut -d= -f2- | tr -d '[:space:]'
}

# Build the archive + headers for one platform from pinned source into its
# per-platform output dir. Requires zig 0.16.x and network access to clone
# ghostty. This is the maintainer path and the consumer fallback; ordinary
# contributors never reach it (they download a prebuilt).
vt_build_from_source() {
  local platform="${1:-$(vt_default_platform)}"
  local goos ztarget outdir sdkroot workdir zig zig_version
  goos="$(vt_platform_goos "$platform")"
  ztarget="$(vt_zig_target "$platform")"
  outdir="$(vt_output_dir_for "$platform")"

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

  # Only the macOS target needs the platform SDK; Linux targets use zig's
  # bundled libc headers and cross-build from any host.
  sdkroot=""
  if [[ "$goos" == "darwin" ]]; then
    sdkroot="${SDKROOT:-$(xcrun --show-sdk-path)}"
  fi

  workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-libghostty-vt.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$workdir'" RETURN

  local commit
  commit="$(vt_commit)"
  echo "==> [$platform] cloning ghostty @ $commit"
  git init -q "$workdir/ghostty"
  git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
  git -C "$workdir/ghostty" fetch -q --depth=1 origin "$commit"
  git -C "$workdir/ghostty" checkout -q --detach FETCH_HEAD

  # Carried patches (e.g. exposing the primary-vs-alt ScreenFormatter through the
  # C API). Applied only when present so the build works before the patch lands.
  if [[ -f "$vt_patch_file" ]]; then
    echo "==> [$platform] applying $vt_patch_file"
    git -C "$workdir/ghostty" apply "$vt_patch_file"
  fi

  echo "==> [$platform] zig build -Demit-lib-vt=true -Dtarget=$ztarget (zig $zig_version)"
  (
    cd "$workdir/ghostty"
    env ${sdkroot:+SDKROOT="$sdkroot"} "$zig" build \
      -Demit-lib-vt=true \
      -Dtarget="$ztarget" \
      -Doptimize=ReleaseFast \
      --summary none \
      --prefix "$workdir/prefix"
  )

  echo "==> [$platform] installing to $outdir"
  rm -rf "$outdir"
  mkdir -p "$outdir/lib" "$outdir/include"
  cp "$workdir/prefix/lib/libghostty-vt.a" "$outdir/lib/"
  cp -R "$workdir/prefix/include/ghostty" "$outdir/include/"
  # Ghostty is MIT-licensed; carry its LICENSE so redistributed prebuilts keep
  # attribution.
  [[ -f "$workdir/ghostty/LICENSE" ]] && cp "$workdir/ghostty/LICENSE" "$outdir/LICENSE"

  vt_key > "$outdir/.built-key"
  chmod -R u+w "$outdir"
}

# Pack one platform's installed archive + headers + LICENSE into a tarball whose
# members are rooted at ghostty-vt/<platform>/ (so a consumer extracts it
# straight into third_party/).
vt_pack() {
  local dest="$1" platform="$2"
  local outdir
  outdir="$(vt_output_dir_for "$platform")"
  tar -C "$vt_repo_dir/third_party" -czf "$dest" \
    "ghostty-vt/$platform/lib" "ghostty-vt/$platform/include" \
    $([[ -f "$outdir/LICENSE" ]] && echo "ghostty-vt/$platform/LICENSE")
}
