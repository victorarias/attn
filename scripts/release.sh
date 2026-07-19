#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
usage: $0 <version-tag> [--skip-tests] [--dry-run]
example: $0 v0.1.1

Branch protection blocks direct pushes to main, so the release lands through a
pull request: this script opens a release/<tag> branch, waits for the required
CI checks to pass, merges the PR, then tags the merge commit and pushes the tag
to trigger .github/workflows/release.yml.
EOF
}

# Poll the PR's status checks until they all complete. Succeeds only when at
# least one check exists and none are pending; fails fast on any failure.
wait_for_checks() {
  local pr="$1"
  local timeout="${RELEASE_CHECK_TIMEOUT:-1800}"
  local interval=15
  local waited=0

  echo "Waiting for CI checks on ${pr} (timeout ${timeout}s)..."
  while :; do
    local summary total pending failed
    summary="$(gh pr view "$pr" --json statusCheckRollup -q \
      '.statusCheckRollup as $c | "\($c|length) \([$c[]|select(.status!="COMPLETED")]|length) \([$c[]|select((.conclusion//"")|test("FAILURE|CANCELLED|TIMED_OUT|STARTUP_FAILURE|ACTION_REQUIRED"))]|length)"' \
      2>/dev/null || echo "0 0 0")"
    read -r total pending failed <<<"$summary"
    [[ "$total" =~ ^[0-9]+$ ]] || total=0
    [[ "$pending" =~ ^[0-9]+$ ]] || pending=0
    [[ "$failed" =~ ^[0-9]+$ ]] || failed=0

    if (( failed > 0 )); then
      echo "error: CI checks failed:"
      gh pr checks "$pr" || true
      return 1
    fi

    if (( total > 0 && pending == 0 )); then
      echo "All required CI checks passed."
      return 0
    fi

    if (( waited >= timeout )); then
      echo "error: timed out after ${timeout}s waiting for CI checks"
      gh pr checks "$pr" || true
      return 1
    fi

    echo "  checks: ${total} total, ${pending} pending (waited ${waited}s)"
    sleep "$interval"
    waited=$(( waited + interval ))
  done
}

if [[ $# -lt 1 ]]; then
  usage
  exit 1
fi

VERSION_TAG="$1"
shift
SKIP_TESTS=0
DRY_RUN=0

if [[ ! "$VERSION_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: version tag must look like v1.2.3"
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-tests)
      SKIP_TESTS=1
      ;;
    --dry-run)
      DRY_RUN=1
      ;;
    *)
      usage
      exit 1
      ;;
  esac
  shift
done

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh (GitHub CLI) is required for the PR-based release flow"
  echo "install: https://cli.github.com/"
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "error: gh is not authenticated; run 'gh auth login'"
  exit 1
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "error: git working tree is not clean"
  echo "commit or stash your changes before running release"
  exit 1
fi

if git rev-parse -q --verify "refs/tags/${VERSION_TAG}" >/dev/null; then
  echo "error: tag ${VERSION_TAG} already exists locally"
  exit 1
fi

if git ls-remote --exit-code --tags origin "${VERSION_TAG}" >/dev/null 2>&1; then
  echo "error: tag ${VERSION_TAG} already exists on origin"
  exit 1
fi

RELEASE_BRANCH="release/${VERSION_TAG}"

if git rev-parse -q --verify "refs/heads/${RELEASE_BRANCH}" >/dev/null; then
  echo "error: branch ${RELEASE_BRANCH} already exists locally"
  exit 1
fi

if git ls-remote --exit-code --heads origin "${RELEASE_BRANCH}" >/dev/null 2>&1; then
  echo "error: branch ${RELEASE_BRANCH} already exists on origin"
  exit 1
fi

VERSION="${VERSION_TAG#v}"
CURRENT_BRANCH="$(git branch --show-current)"

if [[ "$CURRENT_BRANCH" != "main" ]]; then
  echo "error: release script must be run from main (current: ${CURRENT_BRANCH})"
  exit 1
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  cat <<EOF
Dry run for ${VERSION_TAG}
- Would update versions to ${VERSION}
- Would refresh lockfiles
- Would run validation: $([[ "$SKIP_TESTS" -eq 1 ]] && echo "no (skip-tests)" || echo "yes")
- Would create branch ${RELEASE_BRANCH} with the release commit and push it
- Would open a PR to main and wait for required CI checks to pass
- Would merge the PR, then tag ${VERSION_TAG} on the merge commit and push the tag
EOF
  exit 0
fi

echo "Updating app versions to ${VERSION}..."
perl -0pi -e 's/"version": "\d+\.\d+\.\d+"/"version": "'"${VERSION}"'"/' app/package.json
perl -0pi -e 's/"version": "\d+\.\d+\.\d+"/"version": "'"${VERSION}"'"/' app/src-tauri/tauri.conf.json
perl -0pi -e 's/^version = "\d+\.\d+\.\d+"/version = "'"${VERSION}"'"/m' app/src-tauri/Cargo.toml

echo "Refreshing lockfiles..."
(cd app && pnpm install --frozen-lockfile)
(cd app/src-tauri && cargo check -q)

if [[ "$SKIP_TESTS" -eq 0 ]]; then
  echo "Running validation..."
  ./scripts/test-go.sh
  (cd app && pnpm run build)
  (cd app && pnpm test)
fi

echo "Creating release branch ${RELEASE_BRANCH}..."
git switch -c "${RELEASE_BRANCH}"

echo "Committing release version changes..."
git add app/package.json app/pnpm-lock.yaml app/src-tauri/tauri.conf.json app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -m "release: ${VERSION_TAG}"

echo "Pushing ${RELEASE_BRANCH}..."
git push -u origin "${RELEASE_BRANCH}"

echo "Opening release PR..."
PR_URL="$(gh pr create --base main --head "${RELEASE_BRANCH}" \
  --title "release: ${VERSION_TAG}" \
  --body "Automated version bump for the **${VERSION_TAG}** release, generated by \`scripts/release.sh\`. Once CI is green this PR is merged automatically, then \`${VERSION_TAG}\` is tagged on the merge commit to trigger the release build.")"
echo "Opened ${PR_URL}"

if ! wait_for_checks "${PR_URL}"; then
  echo "error: aborting release; ${PR_URL} is left open for inspection"
  echo "the tag ${VERSION_TAG} was NOT created or pushed"
  exit 1
fi

echo "Merging ${PR_URL}..."
gh pr merge "${PR_URL}" --merge --delete-branch

echo "Updating local main..."
git checkout main
git pull --ff-only origin main

echo "Tagging ${VERSION_TAG} on $(git rev-parse --short HEAD)..."
git tag -a "${VERSION_TAG}" -m "${VERSION_TAG}"
git push origin "${VERSION_TAG}"

echo "Release automation complete for ${VERSION_TAG}"
echo "Monitor workflow: .github/workflows/release.yml"
