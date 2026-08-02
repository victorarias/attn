#!/usr/bin/env bash
set -euo pipefail

# Compile pending changelog fragments into a dated CHANGELOG.md section.
#
# Collects changelog.d/*.yaml (the raw facts each PR shipped), asks claude to
# write the user-facing section per the style rules below, inserts it at the
# top of CHANGELOG.md, and deletes the consumed fragments. The result is left
# staged for review: read the section, fix the copy where it misses, then
# commit and open a PR. See docs/making-a-release.md.
#
# usage: compile-changelog.sh [--dry-run]
#   --dry-run  print the prompt that would be sent and exit without changes

DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

cd "$(git rev-parse --show-toplevel)"

shopt -s nullglob
fragments=(changelog.d/*.yaml)
shopt -u nullglob

if (( ${#fragments[@]} == 0 )); then
  echo "no pending fragments in changelog.d/; nothing to compile"
  exit 0
fi

if [[ "$DRY_RUN" -eq 0 ]] && ! command -v claude >/dev/null 2>&1; then
  echo "error: claude CLI is required (https://claude.ai/code)" >&2
  exit 1
fi

echo "validating ${#fragments[@]} fragment(s)..."
go run ./cmd/changelog-check

TODAY="$(date +%Y-%m-%d)"

# Each fragment plus the subject of the commit that added it — after a squash
# merge the subject carries the PR number, which the writer can use for context.
FACTS=""
for f in "${fragments[@]}"; do
  subject="$(git log --diff-filter=A --format=%s -1 -- "$f" 2>/dev/null || true)"
  [[ -z "$subject" ]] && subject="(uncommitted)"
  FACTS+="--- fragment: ${f}
--- introduced by: ${subject}
$(cat "$f")

"
done

# If the top section already carries today's date (a second compile in one
# day), hand it to the writer to fold the new facts into.
TOP_DATE="$(grep -m1 -o '^## \[[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}\]' CHANGELOG.md | tr -d '#[] ' || true)"
EXISTING_SECTION=""
REPLACE_TOP=0
if [[ "$TOP_DATE" == "$TODAY" ]]; then
  REPLACE_TOP=1
  EXISTING_SECTION="$(awk '/^## \[/{n++} n==1 && !/^---$/' CHANGELOG.md)"
fi

PROMPT="You are writing the changelog for attn, a macOS app that orchestrates
coding agents. Below are raw changelog fragments, one per merged PR, written by
the engineers who shipped each change. Compile them into one dated CHANGELOG.md
section.

Style rules — these matter more than fidelity to the fragment wording:
- Write for the app's user, not the codebase. Lead every bullet with a bold
  sentence stating the change in terms of what the user experiences, then
  explain in plain prose. Clarity beats technical correctness.
- Category headers in this order, only when non-empty: ### Added, ### Changed,
  ### Fixed, ### Removed. kind maps to the header; kind: internal fragments are
  context for you but get no bullet.
- Merge related fragments into one bullet when they tell one story.
- Drop what is below the noise floor for a user reading release notes.
- Wrap lines at roughly 80 columns.

Match the voice of these examples from the existing changelog:

### Fixed
- **⌘J jumps to the agent that has waited longest.** Jump-to-waiting used to
  pick the first waiting session in sidebar workspace order, which made its
  target feel arbitrary. It now follows the queue's own order — the turn owed
  longest, the same row the \"Your turn\" band lists first.

Output format: ONLY the markdown section, starting with the line
## [${TODAY}]
and nothing after the last bullet. No preamble, no code fences.
If no fragment merits a user-facing bullet, output exactly: EMPTY

Fragments:

${FACTS}"

if [[ "$REPLACE_TOP" -eq 1 ]]; then
  PROMPT+="

CHANGELOG.md already has a section for ${TODAY}. Fold its bullets and the new
fragments into one merged section (rewriting bullets is fine):

${EXISTING_SECTION}"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "--- prompt (dry run, no changes made) ---"
  echo "$PROMPT"
  exit 0
fi

echo "writing section with claude..."
SECTION="$(claude --strict-mcp-config -p "$PROMPT" < /dev/null)"

if [[ "$SECTION" == "EMPTY" ]]; then
  echo "no user-facing changes in pending fragments; removing them without a new section"
  rm -f "${fragments[@]}"
  git add -A changelog.d
  echo "done — review with 'git status', then commit"
  exit 0
fi

if [[ "$SECTION" != "## [${TODAY}]"* ]]; then
  echo "error: unexpected writer output (expected a section starting with '## [${TODAY}]'):" >&2
  printf '%s\n' "$SECTION" >&2
  exit 1
fi

SECTION="$SECTION" REPLACE_TOP="$REPLACE_TOP" python3 - <<'PY'
import os, re

section = os.environ["SECTION"].strip()
replace_top = os.environ["REPLACE_TOP"] == "1"

with open("CHANGELOG.md") as fh:
    content = fh.read()

m = re.search(r"(?m)^## \[", content)
if not m:
    raise SystemExit("CHANGELOG.md has no dated sections; refusing to guess")

head, rest = content[: m.start()], content[m.start() :]
if replace_top:
    # Drop the existing top section up to (and including) its trailing --- line.
    parts = rest.split("\n---\n", 1)
    rest = parts[1].lstrip("\n") if len(parts) > 1 else ""

with open("CHANGELOG.md", "w") as fh:
    fh.write(head + section + "\n\n---\n\n" + rest)
PY

rm -f "${fragments[@]}"
git add -A changelog.d CHANGELOG.md

echo
echo "compiled ${#fragments[@]} fragment(s) into CHANGELOG.md section [${TODAY}]"
echo "next: review the section (git diff --cached), fix copy where it misses,"
echo "commit, and open a PR"
