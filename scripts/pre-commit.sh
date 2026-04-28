#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"

header() {
  printf '\n[pre-commit] %s\n' "$1"
}

changed_files="$(git diff --cached --name-only --diff-filter=ACMR)"
unset GIT_INDEX_FILE

if [ -z "$changed_files" ]; then
  header "No staged files"
  exit 0
fi

has_changed() {
  local pattern="$1"
  printf '%s\n' "$changed_files" | grep -Eq "$pattern"
}

has_go_files() {
  printf '%s\n' "$changed_files" | grep -Eq '\.go$'
}

has_frontend_files() {
  printf '%s\n' "$changed_files" | grep -E '^app/' | grep -Ev '^app/src-tauri/' | grep -Eq '.'
}

changed_matching() {
  local pattern="$1"
  printf '%s\n' "$changed_files" | grep -E "$pattern" || true
}

configure_pkg_config() {
  if ! command -v pkg-config >/dev/null 2>&1 && command -v pkgconf >/dev/null 2>&1; then
    export PKG_CONFIG="$(command -v pkgconf)"
  fi

  if [ -z "${PKG_CONFIG_PATH:-}" ] && [ -d "$HOME/.nix-profile/lib/pkgconfig" ]; then
    export PKG_CONFIG_PATH="$HOME/.nix-profile/lib/pkgconfig:$HOME/.nix-profile/share/pkgconfig"
  fi
}

ensure_no_unstaged_changes() {
  local dirty=()
  local file
  for file in "$@"; do
    if ! git diff --quiet -- "$file"; then
      dirty+=("$file")
    fi
  done

  if [ "${#dirty[@]}" -gt 0 ]; then
    echo "Cannot auto-format partially staged files:"
    printf '  %s\n' "${dirty[@]}"
    echo "Stage or stash the unstaged edits, then commit again."
    exit 1
  fi
}

format_go_files() {
  local go_files=()
  mapfile -t go_files < <(changed_matching '\.go$')
  if [ "${#go_files[@]}" -eq 0 ]; then
    return
  fi

  ensure_no_unstaged_changes "${go_files[@]}"
  gofmt -w "${go_files[@]}"
  git add -- "${go_files[@]}"
}

format_rust_files() {
  local pattern="$1"
  local rust_files=()
  mapfile -t rust_files < <(changed_matching "$pattern")
  if [ "${#rust_files[@]}" -eq 0 ]; then
    return
  fi

  ensure_no_unstaged_changes "${rust_files[@]}"
  rustfmt --edition 2021 --config skip_children=true "${rust_files[@]}"
  git add -- "${rust_files[@]}"
}

tmp_bin=""
cleanup_tmp_bin() {
  if [ -n "$tmp_bin" ]; then
    rm -f "$tmp_bin"
  fi
}
trap cleanup_tmp_bin EXIT

ensure_e2e_binary() {
  if [ -n "${ATTN_E2E_BIN:-}" ] || [ -x "$root/attn" ]; then
    return
  fi

  header "Go build for E2E"
  tmp_bin="$(mktemp -t attn-precommit.XXXXXX)"
  make -C "$root" build OUTPUT="$tmp_bin"
  export ATTN_E2E_BIN="$tmp_bin"
}

run_daemon_checks=false
run_frontend_checks=false
run_tauri_checks=false
run_native_checks=false
run_shell_checks=false

if has_go_files; then
  run_daemon_checks=true
fi

if has_changed '(^|/)[^/]+\.sh$|^\.githooks/pre-commit$'; then
  run_shell_checks=true
fi

if has_changed '^(cmd|internal|test|scripts/source-fingerprint\.sh|go\.mod|go\.sum|Makefile)(/|$)'; then
  run_daemon_checks=true
fi

if has_changed '^internal/protocol/schema/'; then
  run_daemon_checks=true
  run_frontend_checks=true
  run_native_checks=true
fi

if has_changed '^app/src-tauri/'; then
  run_daemon_checks=true
  run_tauri_checks=true
fi

if has_frontend_files; then
  run_frontend_checks=true
fi

if has_changed '^(native-ui/|app/scripts/real-app-harness/scenario-native-canvas\.mjs$)'; then
  run_daemon_checks=true
  run_native_checks=true
fi

if has_changed '^(\.githooks/pre-commit|scripts/pre-commit\.sh|AGENTS\.md)$'; then
  run_daemon_checks=true
fi

if [ "$run_daemon_checks" = true ]; then
  header "Go format"
  format_go_files

  header "Go tests"
  (cd "$root" && go test ./...)

  header "Go build"
  tmp_bin="$(mktemp -t attn-precommit.XXXXXX)"
  make -C "$root" build OUTPUT="$tmp_bin"
  if [ -z "${ATTN_E2E_BIN:-}" ]; then
    export ATTN_E2E_BIN="$tmp_bin"
  fi

  header "Tauri daemon binary"
  target_triple="$(rustc -vV | awk '/host:/ {print $2}')"
  tauri_bin_dir="$root/app/src-tauri/binaries"
  tauri_bin_path="$tauri_bin_dir/attn-$target_triple"
  mkdir -p "$tauri_bin_dir"
  if [ ! -f "$tauri_bin_path" ]; then
    cp "$tmp_bin" "$tauri_bin_path"
  fi
fi

if [ "$run_shell_checks" = true ]; then
  header "Shell syntax"
  shell_files=()
  mapfile -t shell_files < <(changed_matching '(^|/)[^/]+\.sh$|^\.githooks/pre-commit$')
  if [ "${#shell_files[@]}" -gt 0 ]; then
    bash -n "${shell_files[@]}"
  fi
fi

if [ "$run_frontend_checks" = true ]; then
  if [[ "${NODE_OPTIONS:-}" != *"--localstorage-file="* ]]; then
    export NODE_OPTIONS="${NODE_OPTIONS:+$NODE_OPTIONS }--localstorage-file=/tmp/attn-vitest-localstorage.json"
  fi

  header "Frontend tests"
  (cd "$root/app" && pnpm run test)

  header "Frontend build"
  (cd "$root/app" && pnpm run build)

  ensure_e2e_binary

  header "E2E"
  (cd "$root/app" && pnpm run e2e)
fi

if [ "$run_tauri_checks" = true ]; then
  configure_pkg_config

  header "Tauri Rust format"
  format_rust_files '^app/src-tauri/.*\.rs$'

  header "Tauri Rust lint"
  (cd "$root/app/src-tauri" && cargo clippy --all-targets -- -D warnings)

  header "Tauri Rust tests"
  (cd "$root/app/src-tauri" && cargo test)
fi

if [ "$run_native_checks" = true ]; then
  header "Native Rust format"
  format_rust_files '^native-ui/.*\.rs$'

  header "Native Rust lint"
  (cd "$root/native-ui" && cargo clippy --workspace --all-targets -- -D warnings)

  header "Native Rust tests"
  (cd "$root/native-ui" && cargo test --workspace)
fi

if [ "$run_daemon_checks" != true ] &&
  [ "$run_frontend_checks" != true ] &&
  [ "$run_tauri_checks" != true ] &&
  [ "$run_native_checks" != true ] &&
  [ "$run_shell_checks" != true ]; then
  header "No matching test bucket"
fi
