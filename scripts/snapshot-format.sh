#!/usr/bin/env bash
# Prints the snapshot format tag: the identity of the terminal-snapshot wire
# format this build encodes and decodes.
#
# A pty-worker outlives an install, so a session opened after an upgrade can be
# served a snapshot the running app cannot read. The tag is what lets the app
# recognize that and fall back to a snapshot-less attach instead of decoding
# garbage. See docs/plans/2026-08-16-snapshot-format-skew.md.
#
# Derived, never typed: the inputs are the two artifacts that decide the format
# — the native encoder's archive and the browser decoder's module — so
# republishing either moves the tag with no discipline required. The pin alone
# is not enough: the native lock key changed for the snapshot API while
# ghostty-vt.pin stayed put, which is the skew that produced the incident.
#
# Bump SALT when attn's own encode/decode changes the bytes without moving
# either lock.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SALT=1

hash_stdin() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 | awk '{print $NF}'
    return
  fi
  echo "No SHA-256 implementation found (tried shasum, sha256sum, openssl)" >&2
  exit 1
}

inputs=("${ROOT_DIR}/ghostty-vt-native.lock" "${ROOT_DIR}/ghostty-vt-wasm.lock")
for path in "${inputs[@]}"; do
  if [[ ! -f "${path}" ]]; then
    echo "snapshot-format: missing ${path#"${ROOT_DIR}/"} — the tag cannot be derived from an incomplete checkout" >&2
    exit 1
  fi
done

digest="$({ printf 'salt=%s\n' "${SALT}"; cat "${inputs[@]}"; } | hash_stdin)"
printf '%s\n' "${digest:0:12}"
