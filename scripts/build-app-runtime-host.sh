#!/usr/bin/env bash
set -euo pipefail

# Builds attn's shared app runtime host — the Bun sidecar every installed app's
# handlers execute in — as a `bun build --compile` standalone binary.
#
# Compiling rather than shipping source is what makes the runtime reachable at
# all: bun lives on a developer PATH that `pathutil.EnsureGUIPath()` does not
# reconstruct, so a daemon launched by the macOS app cannot find it. The Bun
# runtime is embedded in the executable, so running apps needs no toolchain on the
# user's machine; bun is required only by `attn app apply`, which builds CLI-side.
#
# Usage: build-app-runtime-host.sh [stage_dir] [bun_target]
#
#   stage_dir   where to place the binary. Defaults to the Tauri resource dir, so
#               a plain run stages it for the next app build.
#   bun_target  a `bun build --compile --target=` triple. Empty means the host
#               platform. Cross targets (bun-linux-x64, bun-linux-arm64) exist so
#               the runtime is not silently darwin-only: the daemon runs on Linux
#               remotes.
#
# The Mach-O trailer fixups are the same ones the bundled-plugin build carries
# (oven-sh/bun#32159) and are Darwin-only by construction — a Linux-target binary
# has no code signature to strip.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stage_dir="${1:-${repo_root}/app/src-tauri/app-runtime}"
bun_target="${2:-}"

# Resolved against the caller's directory before anything else, because the
# compile below runs from apphost/ and bun reads --outfile relative to its own
# cwd. A relative stage_dir would be created where the caller meant and written
# one directory deeper, leaving the build reporting success over an empty tree.
case "${stage_dir}" in
  /*) ;;
  *) stage_dir="$(pwd)/${stage_dir}" ;;
esac

# BinaryName is the same string internal/daemon looks for. Changing it here alone
# produces a daemon that cannot find its runtime.
binary_name="attn-app-runtime"
source_dir="${repo_root}/apphost"

minimum_bun_version="1.3.14"
if ! command -v bun >/dev/null 2>&1; then
  echo "bun is not on PATH; the app runtime host is a bun --compile binary" >&2
  exit 1
fi
bun_version="$(bun --version)"
if [[ "$(printf '%s\n' "${minimum_bun_version}" "${bun_version}" | sort -V | head -1)" != "${minimum_bun_version}" ]]; then
  echo "bun ${bun_version} is too old to produce signable --compile binaries; need >= ${minimum_bun_version}" >&2
  exit 1
fi

# Strips the ad-hoc signature bun's linker leaves, plus any bytes past the
# Mach-O's declared LC_CODE_SIGNATURE end. Without it every later
# `codesign --force --sign` of this binary fails strict validation.
remove_bun_linker_signature() {
  local executable="$1"
  if [[ "$(uname -s)" != "Darwin" ]]; then
    return
  fi
  command -v otool >/dev/null 2>&1 || return 0

  local signature_end file_size
  signature_end="$(otool -l "${executable}" | awk '
    $1 == "cmd" && $2 == "LC_CODE_SIGNATURE" { in_signature = 1; next }
    in_signature && $1 == "dataoff" { dataoff = $2; next }
    in_signature && $1 == "datasize" { print dataoff + $2; exit }
  ')"
  if [[ -z "${signature_end}" ]]; then
    return
  fi
  file_size="$(stat -f '%z' "${executable}")"
  if (( file_size > signature_end )); then
    truncate -s "${signature_end}" "${executable}"
  fi
  codesign --remove-signature "${executable}"
}

echo "building the app runtime host${bun_target:+ for ${bun_target}}"
rm -rf "${stage_dir}"
mkdir -p "${stage_dir}"

compile_args=("${source_dir}/src/index.ts" --compile --minify --outfile "${stage_dir}/${binary_name}")
if [[ -n "${bun_target}" ]]; then
  compile_args+=(--target="${bun_target}")
fi
(cd "${source_dir}" && bun build "${compile_args[@]}")

# bun leaves these beside the output; they are not part of the artifact.
find "${stage_dir}" -maxdepth 1 -name '*.bun-build' -delete

# A cross-compiled binary is not this host's Mach-O, so both the trailer fixup and
# signing apply only to a native build.
#
# Signing here, rather than leaving it to the app bundle's signing pass as the
# bundled-plugin build does, is what makes the artifact runnable from a checkout:
# stripping bun's ad-hoc signature leaves a binary macOS refuses to execute at all
# (SIGKILL, no diagnostic), and the daemon resolves this binary beside itself when
# it is not running from a .app. The bundle pass re-signs with --force afterwards,
# so signing twice costs nothing.
if [[ -z "${bun_target}" ]]; then
  remove_bun_linker_signature "${stage_dir}/${binary_name}"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    identity="${MACOS_CODESIGN_IDENTITY:-}"
    if [[ -z "${identity}" ]]; then
      identity="$(bash "${repo_root}/scripts/macos-codesign-identity.sh" find)"
    fi
    codesign --force --sign "${identity:--}" "${stage_dir}/${binary_name}"
  fi
fi
chmod 0755 "${stage_dir}/${binary_name}"

echo "  ${stage_dir}/${binary_name}"
