#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
daemon_package="github.com/victorarias/attn/internal/daemon"
shard_count="${ATTN_GO_TEST_SHARDS:-5}"
package_parallelism="${ATTN_GO_TEST_PACKAGE_PARALLELISM:-4}"
test_gomaxprocs="${ATTN_GO_TEST_GOMAXPROCS:-${GOMAXPROCS:-3}}"
test_timeout="${ATTN_GO_TEST_TIMEOUT:-90s}"

if ! [[ "$shard_count" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_SHARDS must be a positive integer, got: $shard_count" >&2
  exit 2
fi
if ! [[ "$package_parallelism" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_PACKAGE_PARALLELISM must be a positive integer, got: $package_parallelism" >&2
  exit 2
fi
if ! [[ "$test_gomaxprocs" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_GOMAXPROCS must be a positive integer, got: $test_gomaxprocs" >&2
  exit 2
fi
export GOMAXPROCS="$test_gomaxprocs"

go_bin="$(command -v go)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-go-test.XXXXXX")"
# shellcheck disable=SC2329 # invoked indirectly by trap
cleanup() {
  rm -rf "$test_root"
}
trap cleanup EXIT

# Scope the entire test process tree away from production ~/.attn. Package
# TestMain functions add their own isolation, but the runner must also protect
# packages and subprocesses before any package-level setup executes. Keep the
# outer path stable so Go can reuse successful test results; TestMain still
# replaces it with a fresh per-process directory whenever tests actually run.
runner_hash="$(shasum -a 256 "$root/scripts/test-go.sh" | awk '{ print $1 }')"
cache_namespace="$(printf '%s\n%s\n%s\n' "$root" "$($go_bin env GOVERSION)" "$runner_hash" | shasum -a 256 | awk '{ print $1 }')"
test_cache_root="${TMPDIR:-/tmp}/attn-go-test-cache/$cache_namespace"
mkdir -p "$test_cache_root/data"
export ATTN_DATA_DIR="$test_cache_root/data"
unset ATTN_DB_PATH ATTN_SOCKET_PATH ATTN_CONFIG_PATH ATTN_PLUGIN_DIR

# Tests should exercise Git itself, not an environment-specific command wrapper.
# macOS is attn's supported platform, and /usr/bin/git provides a stable direct
# executable. Other platforms keep their resolved Git unless explicitly pinned.
test_git="${ATTN_TEST_GIT:-}"
if [ -z "$test_git" ]; then
  if [ "$(uname -s)" = "Darwin" ] && [ -x /usr/bin/git ]; then
    test_git=/usr/bin/git
  else
    test_git="$(command -v git)"
  fi
fi
if [ ! -x "$test_git" ]; then
  echo "test Git is not executable: $test_git" >&2
  exit 2
fi
test_git_hash="$(shasum -a 256 "$test_git" | awk '{ print $1 }')"
test_git_key="$(printf '%s\n%s\n' "$test_git" "$test_git_hash" | shasum -a 256 | awk '{ print $1 }')"
test_bin_dir="$test_cache_root/git/$test_git_key"
mkdir -p "$test_bin_dir"
if [ ! -e "$test_bin_dir/git" ]; then
  ln -s "$test_git" "$test_bin_dir/git" 2>/dev/null || true
fi
if [ ! -L "$test_bin_dir/git" ] || [ "$(readlink "$test_bin_dir/git")" != "$test_git" ]; then
  echo "cached test Git does not point to $test_git: $test_bin_dir/git" >&2
  exit 2
fi
export PATH="$test_bin_dir:$PATH"

short_mode=false
for arg in "$@"; do
  if [ "$arg" = "-short" ] || [ "$arg" = "-short=true" ]; then
    short_mode=true
  fi
done

# The daemon's process-level E2E tests need the real attn executable. Build it
# once for all shards instead of independently inside each test. Normalize an
# explicitly supplied binary the same way. The content-addressed path stays
# stable for unchanged code and changes whenever the executable changes, so
# daemon result-cache hits cannot hide a new binary at an unchanged source path.
if [ "$short_mode" = false ]; then
  source_e2e_bin="${ATTN_E2E_BIN:-}"
  if [ -z "$source_e2e_bin" ]; then
    source_e2e_bin="$test_root/attn"
    "$go_bin" build -o "$source_e2e_bin" ./cmd/attn
  elif [ ! -x "$source_e2e_bin" ]; then
    echo "ATTN_E2E_BIN is not executable: $source_e2e_bin" >&2
    exit 2
  fi
  e2e_hash="$(shasum -a 256 "$source_e2e_bin" | awk '{ print $1 }')"
  e2e_cache_dir="$test_cache_root/e2e/$e2e_hash"
  mkdir -p "$e2e_cache_dir"
  cached_e2e_hash=""
  if [ -f "$e2e_cache_dir/attn" ]; then
    cached_e2e_hash="$(shasum -a 256 "$e2e_cache_dir/attn" | awk '{ print $1 }')"
  fi
  if [ "$cached_e2e_hash" != "$e2e_hash" ]; then
    cp "$source_e2e_bin" "$e2e_cache_dir/attn.$$.tmp"
    chmod +x "$e2e_cache_dir/attn.$$.tmp"
    mv -f "$e2e_cache_dir/attn.$$.tmp" "$e2e_cache_dir/attn"
  fi
  export ATTN_E2E_BIN="$e2e_cache_dir/attn"
fi

packages_file="$test_root/packages"
"$go_bin" list ./... | awk -v daemon="$daemon_package" '$0 != daemon' >"$packages_file"

tests_file="$test_root/daemon-tests"
"$go_bin" test -list '^Test' ./internal/daemon | awk '/^Test/ { print }' >"$tests_file"
if [ ! -s "$tests_file" ]; then
  echo "no daemon tests discovered" >&2
  exit 1
fi

pids=()
names=()
logs=()

run_job() {
  local name="$1"
  shift
  local log="$test_root/$name.log"
  (cd "$root" && "$@") >"$log" 2>&1 &
  pids+=("$!")
  names+=("$name")
  logs+=("$log")
}

packages=()
while IFS= read -r package; do
  packages+=("$package")
done <"$packages_file"
run_job packages "$go_bin" test -timeout "$test_timeout" -p "$package_parallelism" "$@" "${packages[@]}"

shard=0
while [ "$shard" -lt "$shard_count" ]; do
  regex="$(awk -v count="$shard_count" -v target="$shard" '
    (NR - 1) % count == target {
      if (tests != "") tests = tests "|"
      tests = tests $0
    }
    END { print "^(" tests ")$" }
  ' "$tests_file")"
  run_job "daemon-$((shard + 1))" "$go_bin" test -timeout "$test_timeout" "$@" -run "$regex" ./internal/daemon
  shard=$((shard + 1))
done

failed=0
index=0
while [ "$index" -lt "${#pids[@]}" ]; do
  if wait "${pids[$index]}"; then
    cat "${logs[$index]}"
  else
    cat "${logs[$index]}" >&2
    echo "Go test job failed: ${names[$index]}" >&2
    failed=1
  fi
  index=$((index + 1))
done

exit "$failed"
