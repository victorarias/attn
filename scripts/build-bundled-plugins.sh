#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stage_root="${1:-${repo_root}/app/src-tauri/bundled-plugins}"

# `bun build --compile` on macOS arm64 leaves stale bytes past the end of its
# ad-hoc code signature (upstream bug: oven-sh/bun#32159, fix pending in
# oven-sh/bun#32162 as of bun 1.3.14). Those trailing bytes make every later
# `codesign --force --sign ...` of the binary (e.g. tauri's bundler re-signing
# app resources) fail strict validation with "main executable failed strict
# validation", even though the binary itself runs fine. Truncate the file to
# the Mach-O's declared LC_CODE_SIGNATURE end so downstream codesign has a
# clean base to re-sign from.
fix_bun_compile_codesign() {
  local bin_path="$1"
  [[ "$(uname -s)" == "Darwin" ]] || return 0
  command -v otool >/dev/null 2>&1 || return 0

  local sig_offset sig_size sig_end actual_size
  read -r sig_offset sig_size < <(otool -l "${bin_path}" | awk '
    /cmd LC_CODE_SIGNATURE/ { found=1; next }
    found && /dataoff/ { off=$2 }
    found && /datasize/ { size=$2; print off, size; exit }
  ')
  [[ -n "${sig_offset:-}" && -n "${sig_size:-}" ]] || return 0

  sig_end=$((sig_offset + sig_size))
  actual_size="$(stat -f %z "${bin_path}")"
  if (( actual_size > sig_end )); then
    echo "  fixing bun codesign trailer on ${bin_path} (${actual_size} -> ${sig_end} bytes)"
    truncate -s "${sig_end}" "${bin_path}"
  fi
}

stage_plugin() {
  local name="$1" description="$2"
  local source_dir="${repo_root}/plugins/${name}"
  local stage_dir="${stage_root}/${name}"

  local package_version manifest_version runtime_version
  package_version="$(cd "${source_dir}" && bun -e 'console.log(require("./package.json").version)')"
  manifest_version="$(awk -F '"' '/^version = / { print $2; exit }' "${source_dir}/attn-plugin.toml")"
  runtime_version="$(awk -F '"' '/^const pluginVersion = / { print $2; exit }' "${source_dir}/src/index.ts")"
  if [[ -z "${package_version}" || "${package_version}" != "${manifest_version}" || "${package_version}" != "${runtime_version}" ]]; then
    echo "${name} version mismatch: package=${package_version:-missing} manifest=${manifest_version:-missing} runtime=${runtime_version:-missing}" >&2
    exit 1
  fi

  rm -rf "${stage_dir}"
  mkdir -p "${stage_dir}/bin"
  bun build "${source_dir}/src/index.ts" --compile --minify --outfile "${stage_dir}/bin/${name}"
  chmod 0755 "${stage_dir}/bin/${name}"
  fix_bun_compile_codesign "${stage_dir}/bin/${name}"
  if [[ "${name}" == "attn-opencode" ]]; then
    bun build "${source_dir}/src/guidance-plugin.ts" --target=bun --format=esm --minify --outfile "${stage_dir}/guidance-plugin.js"
  fi
  cp "${source_dir}/README.md" "${stage_dir}/README.md"
  cat >"${stage_dir}/attn-plugin.toml" <<EOF
name = "${name}"
version = "${package_version}"
attn_api_version = 4
description = "${description}"

[plugin]
kind = "executable"
path = "bin/${name}"
EOF

  echo "Staged bundled ${name} ${package_version} at ${stage_dir}"
}

stage_plugin attn-opencode "Server-backed OpenCode driver for attn"
stage_plugin attn-pi "pi driver for attn"
