#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${ATTN_VERSION:-}" ]]; then
  printf '%s\n' "${ATTN_VERSION}"
  exit 0
fi

PACKAGE_JSON="${ROOT_DIR}/app/package.json"
if [[ ! -f "${PACKAGE_JSON}" ]]; then
  echo "app/package.json not found" >&2
  exit 1
fi

VERSION="$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)".*$/\1/p' "${PACKAGE_JSON}" | head -n1)"
if [[ -z "${VERSION}" ]]; then
  echo "could not determine version from app/package.json" >&2
  exit 1
fi

printf '%s\n' "${VERSION}"
