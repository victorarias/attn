#!/usr/bin/env bash
set -euo pipefail

# PR changelog gate. Every PR must either add a fragment under changelog.d/ or
# modify CHANGELOG.md directly (the release compilation PR does the latter;
# hand-fixes to changelog copy also pass). release/* branches are the version
# bump opened by scripts/release.sh and are exempt.
#
# usage: changelog-gate.sh <base-ref> [head-branch]
#
# Runs in CI (.github/workflows/ci.yml, job "Changelog") and locally:
#   ./scripts/changelog-gate.sh main
#
# See docs/making-a-release.md.

BASE_REF="${1:?usage: changelog-gate.sh <base-ref> [head-branch]}"
HEAD_BRANCH="${2:-$(git branch --show-current)}"

cd "$(git rev-parse --show-toplevel)"

if [[ "$HEAD_BRANCH" == release/* ]]; then
  echo "changelog gate: release branch ${HEAD_BRANCH}, skipping"
  exit 0
fi

# Diff against the merge-base with the base branch; prefer origin/<base> when
# it exists (CI checks out with full history).
RANGE="${BASE_REF}...HEAD"
if git rev-parse -q --verify "origin/${BASE_REF}" >/dev/null; then
  RANGE="origin/${BASE_REF}...HEAD"
fi

# Committed additions, plus staged/untracked ones so the gate is honest when
# run locally before committing (CI only ever sees the committed set).
added_fragment="$(
  git diff --diff-filter=A --name-only "$RANGE" -- 'changelog.d/*.yaml'
  git diff --diff-filter=A --name-only --cached -- 'changelog.d/*.yaml'
  git ls-files --others --exclude-standard -- 'changelog.d/*.yaml'
)"
touched_changelog="$(
  git diff --name-only "$RANGE" -- CHANGELOG.md
  git diff --name-only HEAD -- CHANGELOG.md
)"

if [[ -z "$added_fragment" && -z "$touched_changelog" ]]; then
  cat >&2 <<EOF
changelog gate: this branch neither adds a changelog fragment nor touches
CHANGELOG.md.

Add one YAML fragment under changelog.d/ describing what changed (use
kind: internal for changes with no user-visible behavior). Format and
examples: docs/making-a-release.md
EOF
  exit 1
fi

if [[ -n "$added_fragment" ]]; then
  echo "changelog gate: fragment(s) added:"
  echo "$added_fragment" | sed 's/^/  /'
else
  echo "changelog gate: CHANGELOG.md modified directly"
fi

echo "changelog gate: validating changelog.d/"
go run ./cmd/changelog-check
echo "changelog gate: OK"
