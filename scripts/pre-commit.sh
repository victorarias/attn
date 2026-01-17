#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"

header() {
  printf '\n[pre-commit] %s\n' "$1"
}

header "Go format"
go_files=$(git ls-files '*.go')
if [ -n "$go_files" ]; then
  fmt_out=$(gofmt -l $go_files)
  if [ -n "$fmt_out" ]; then
    echo "gofmt required for:"
    echo "$fmt_out"
    echo "Run: gofmt -w <files>"
    exit 1
  fi
fi

header "Go tests"
(go test ./...)

header "Go build"
(go build -o /tmp/attn-precommit-build ./cmd/attn)
rm -f /tmp/attn-precommit-build

header "Frontend tests"
(cd "$root/app" && pnpm run test)

header "Frontend build"
(cd "$root/app" && pnpm run build)

header "E2E"
(cd "$root/app" && pnpm run e2e)

header "Rust format"
if ! command -v pkg-config >/dev/null 2>&1; then
  echo "pkg-config not found. Install it to run Rust checks."
  echo "Debian/Ubuntu: sudo apt-get install -y pkg-config"
  echo "macOS: brew install pkgconf"
  exit 1
fi

(cd "$root/app/src-tauri" && cargo fmt -- --check)

header "Rust tests"
(cd "$root/app/src-tauri" && cargo test)
