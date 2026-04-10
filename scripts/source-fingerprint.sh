#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

output_mode="text"
field="fingerprint"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)
      output_mode="json"
      shift
      ;;
    --field)
      field="${2:-}"
      if [[ -z "${field}" ]]; then
        echo "--field requires a value" >&2
        exit 1
      fi
      shift 2
      ;;
    --help|-h)
      cat <<'EOF'
Usage:
  scripts/source-fingerprint.sh
  scripts/source-fingerprint.sh --field fingerprint
  scripts/source-fingerprint.sh --json

Fields:
  fingerprint
  commit
  short_commit
  dirty
EOF
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

hash_file() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$path" | awk '{print $NF}'
    return
  fi
  echo "No SHA-256 implementation found (tried shasum, sha256sum, openssl)" >&2
  exit 1
}

emit_result() {
  local fingerprint="$1"
  local commit="$2"
  local short_commit="$3"
  local dirty="$4"

  if [[ "${output_mode}" == "json" ]]; then
    printf '{"fingerprint":"%s","commit":"%s","short_commit":"%s","dirty":%s}\n' \
      "${fingerprint}" "${commit}" "${short_commit}" "${dirty}"
    return
  fi

  case "${field}" in
    fingerprint)
      printf '%s\n' "${fingerprint}"
      ;;
    commit)
      printf '%s\n' "${commit}"
      ;;
    short_commit)
      printf '%s\n' "${short_commit}"
      ;;
    dirty)
      printf '%s\n' "${dirty}"
      ;;
    *)
      echo "Unknown field: ${field}" >&2
      exit 1
      ;;
  esac
}

if ! git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  emit_result "unknown" "unknown" "unknown" "false"
  exit 0
fi

commit="$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'unknown')"
short_commit="$(git -C "${ROOT_DIR}" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"

dirty="false"
if ! git -C "${ROOT_DIR}" diff --quiet --ignore-submodules --; then
  dirty="true"
elif ! git -C "${ROOT_DIR}" diff --cached --quiet --ignore-submodules --; then
  dirty="true"
elif [[ -n "$(git -C "${ROOT_DIR}" ls-files --others --exclude-standard)" ]]; then
  dirty="true"
fi

if [[ "${dirty}" == "false" ]]; then
  emit_result "git:${commit}" "${commit}" "${short_commit}" "${dirty}"
  exit 0
fi

tmp_payload="$(mktemp "${TMPDIR:-/tmp}/attn-source-fingerprint.XXXXXX")"
cleanup() {
  rm -f "${tmp_payload}"
}
trap cleanup EXIT

while IFS= read -r -d '' relative_path; do
  absolute_path="${ROOT_DIR}/${relative_path}"
  printf '%s\0' "${relative_path}" >>"${tmp_payload}"
  if [[ -L "${absolute_path}" ]]; then
    printf '__SYMLINK__%s' "$(readlink "${absolute_path}")" >>"${tmp_payload}"
  elif [[ -f "${absolute_path}" ]]; then
    cat "${absolute_path}" >>"${tmp_payload}"
  else
    printf '__MISSING__' >>"${tmp_payload}"
  fi
  printf '\0' >>"${tmp_payload}"
done < <(git -C "${ROOT_DIR}" ls-files -z --cached --others --exclude-standard)

tree_hash="$(hash_file "${tmp_payload}")"
emit_result "tree:${tree_hash}" "${commit}" "${short_commit}" "${dirty}"
