#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
usage: $0 <version-tag> [--skip-tests] [--dry-run]
example: $0 v0.1.1
EOF
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
- Would commit release changes
- Would create and push tag ${VERSION_TAG}
- Would update Formula/attn.rb for ${VERSION_TAG}
- Would commit and push formula update
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
  go test ./...
  (cd app && pnpm run build)
  (cd app && pnpm test)
fi

echo "Committing release version changes..."
git add app/package.json app/pnpm-lock.yaml app/src-tauri/tauri.conf.json app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -m "release: ${VERSION_TAG}"

echo "Creating and pushing tag ${VERSION_TAG}..."
git tag -a "${VERSION_TAG}" -m "${VERSION_TAG}"
git push origin main
git push origin "${VERSION_TAG}"

echo "Waiting for GitHub source tarball..."
for _ in {1..30}; do
  if ./scripts/update-homebrew-formula.sh "${VERSION_TAG}" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

./scripts/update-homebrew-formula.sh "${VERSION_TAG}"
git add Formula/attn.rb
git commit -m "homebrew: update formula for ${VERSION_TAG}"
git push origin main

echo "Release automation complete for ${VERSION_TAG}"
echo "Monitor workflow: .github/workflows/release.yml"
