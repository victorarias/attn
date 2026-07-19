#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stage_root="${1:-${repo_root}/app/src-tauri/bundled-plugins}"

# bun < 1.3.14 emits --compile binaries whose embedded JS payload can overflow
# the __BUN segment past the code-signature extent; codesign then refuses to
# sign them ("main executable failed strict validation").
minimum_bun_version="1.3.14"
bun_version="$(bun --version)"
if [[ "$(printf '%s\n' "${minimum_bun_version}" "${bun_version}" | sort -V | head -1)" != "${minimum_bun_version}" ]]; then
  echo "bun ${bun_version} is too old to produce signable --compile binaries; need >= ${minimum_bun_version}" >&2
  exit 1
fi

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
  if [[ "${name}" == "attn-opencode" ]]; then
    bun build "${source_dir}/src/guidance-plugin.ts" --target=bun --format=esm --minify --outfile "${stage_dir}/guidance-plugin.js"
  fi
  if [[ "${name}" == "attn-pi" ]]; then
    # The suite runs inside pi's node runtime; pi resolves
    # @earendil-works/pi-coding-agent as a virtual module at load time, so it
    # must stay an external import.
    bun build "${source_dir}/suite/index.ts" --target=node --format=esm --minify \
      --external "@earendil-works/pi-coding-agent" --outfile "${stage_dir}/suite.js"
  fi
  cp "${source_dir}/README.md" "${stage_dir}/README.md"
  cat >"${stage_dir}/attn-plugin.toml" <<EOF
name = "${name}"
version = "${package_version}"
attn_api_version = 5
description = "${description}"

[plugin]
kind = "executable"
path = "bin/${name}"
EOF

  echo "Staged bundled ${name} ${package_version} at ${stage_dir}"
}

stage_plugin attn-opencode "Server-backed OpenCode driver for attn"
stage_plugin attn-pi "pi driver for attn"
