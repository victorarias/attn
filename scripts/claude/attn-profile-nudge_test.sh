#!/usr/bin/env bash
# Tests for the PR-milestone profile-cleanup nudge hook.
#
# The hook's whole value is that it stays silent unless there is something to
# clean, so most of these assert silence. Run: bash scripts/claude/attn-profile-nudge_test.sh
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
hook="$script_dir/attn-profile-nudge.sh"

pass=0
fail=0

# Each case runs the hook against a fake worktree, a fake `attn` whose
# `profile list --json` returns a canned listing, and a synthetic hook payload.
run_hook() {
  local listing="$1" command_line="$2" tool_name="${3:-Bash}" success="${4:-true}" legacy="${5:-}"
  local sandbox
  sandbox="$(mktemp -d)"

  git -C "$sandbox" init -q 2>/dev/null
  cat >"$sandbox/attn" <<EOF
#!/usr/bin/env bash
if [ "\$1" = "profile" ] && [ "\$2" = "list" ]; then
  cat <<'LISTING'
$listing
LISTING
  exit 0
fi
exit 1
EOF
  chmod +x "$sandbox/attn"

  local worktree
  worktree="$(cd "$sandbox" && git rev-parse --show-toplevel)"
  local payload
  payload="$(jq -n \
    --arg tool "$tool_name" --arg cmd "$command_line" --argjson ok "$success" \
    '{tool_name: $tool, tool_input: {command: $cmd}, tool_response: {success: $ok}}')"

  # Rewrite the listing's origin to the sandbox worktree so attribution matches.
  local resolved
  resolved="$(printf '%s' "$payload" | sed "s|__WORKTREE__|$worktree|g")"
  local listing_resolved
  listing_resolved="$(printf '%s' "$listing" | sed "s|__WORKTREE__|$worktree|g")"
  cat >"$sandbox/attn" <<EOF
#!/usr/bin/env bash
if [ "\$1" = "profile" ] && [ "\$2" = "list" ]; then
  cat <<'LISTING'
$listing_resolved
LISTING
  exit 0
fi
exit 1
EOF
  chmod +x "$sandbox/attn"

  # A profile that predates origin recording: a data dir under HOME with a
  # registered session whose cwd is this worktree, and no origin.json.
  if [ -n "$legacy" ]; then
    local regdir="$sandbox/.attn-$legacy/workers/d-1/registry"
    mkdir -p "$regdir"
    printf '{\n  "session_id": "s1",\n  "cwd": "%s"\n}\n' "$worktree" >"$regdir/s1.json"
    if [ "$legacy" = "attributed" ]; then
      printf '{"worktree":"/elsewhere"}\n' >"$sandbox/.attn-$legacy/origin.json"
    fi
  fi

  (cd "$sandbox" && printf '%s' "$resolved" | HOME="$sandbox" bash "$hook" 2>/dev/null)
  local rc=$?
  rm -rf "$sandbox"
  return $rc
}

assert_silent() {
  local name="$1" out
  shift
  out="$(run_hook "$@")"
  if [ -z "$out" ]; then
    pass=$((pass + 1))
    printf '  ok   %s\n' "$name"
  else
    fail=$((fail + 1))
    printf '  FAIL %s — expected no output, got: %s\n' "$name" "$out"
  fi
}

assert_nudges() {
  local name="$1" expect="$2" out
  shift 2
  out="$(run_hook "$@")"
  if printf '%s' "$out" | jq -e '.hookSpecificOutput.additionalContext' >/dev/null 2>&1 &&
    printf '%s' "$out" | grep -qF "$expect"; then
    pass=$((pass + 1))
    printf '  ok   %s\n' "$name"
  else
    fail=$((fail + 1))
    printf '  FAIL %s — expected a nudge containing %q, got: %s\n' "$name" "$expect" "$out"
  fi
}

owned_listing='{"profiles":[
  {"profile":"","label":"default","hasData":true,"hasApp":true,"daemonRunning":true,"liveWorkers":9},
  {"profile":"wily1","label":"wily1","hasData":true,"hasApp":true,"daemonRunning":true,"liveWorkers":2,
   "origin":{"worktree":"__WORKTREE__","branch":"wily-raccoon","recordedAt":"2026-08-02T00:00:00Z"}}
]}'

foreign_listing='{"profiles":[
  {"profile":"other","label":"other","hasData":true,"hasApp":true,"daemonRunning":true,"liveWorkers":3,
   "origin":{"worktree":"/somewhere/else","branch":"other","recordedAt":"2026-08-02T00:00:00Z"}}
]}'

no_profiles_listing='{"profiles":[
  {"profile":"","label":"default","hasData":true,"hasApp":true,"daemonRunning":true,"liveWorkers":9}
]}'

gone_listing='{"profiles":[
  {"profile":"wily1","label":"wily1","hasData":false,"hasApp":false,"daemonRunning":false,"liveWorkers":0,
   "origin":{"worktree":"__WORKTREE__","branch":"wily-raccoon","recordedAt":"2026-08-02T00:00:00Z"}}
]}'

echo "attn-profile-nudge:"

# Fires on the milestones, naming the profile and what is still running.
assert_nudges "nudges on gh pr create"  "wily1" "$owned_listing" "gh pr create --fill"
assert_nudges "nudges on gh pr merge"   "wily1" "$owned_listing" "gh pr merge 123 --squash"
assert_nudges "nudges on stack merge"   "wily1" "$owned_listing" "stack merge"
assert_nudges "reports live workers"    "2 pty-worker(s)" "$owned_listing" "gh pr create"
assert_nudges "says merged on a merge"  "merged a PR"     "$owned_listing" "gh pr merge 1"
assert_nudges "says created on create"  "created a PR"    "$owned_listing" "gh pr create"
assert_nudges "matches mid-pipeline"    "wily1" "$owned_listing" "cd /tmp && gh pr create --fill"

# Silence is the default. Each of these would be noise.
assert_silent "silent for other tools"        "$owned_listing" "gh pr create" "Read"
assert_silent "silent for non-milestone gh"   "$owned_listing" "gh pr view 123"
assert_silent "silent for gh pr checks"       "$owned_listing" "gh pr checks"
assert_silent "silent for pr wait-ready"      "$owned_listing" "attn pr wait-ready 123"
assert_silent "silent when command failed"    "$owned_listing" "gh pr create" "Bash" "false"
assert_silent "silent when profile is foreign" "$foreign_listing" "gh pr create"
assert_silent "silent with no owned profile"  "$no_profiles_listing" "gh pr create"
assert_silent "silent when nothing installed" "$gone_listing" "gh pr create"
assert_silent "never targets default profile" "$no_profiles_listing" "gh pr merge 1"
assert_silent "silent on unrelated command"   "$owned_listing" "git commit -m 'gh pr create'"

# Heuristic fallback: profiles created before origin recording existed are still
# attributed, via a registered session whose cwd is this worktree.
assert_nudges "heuristic finds legacy profile" "legacy" \
  "$no_profiles_listing" "gh pr create" "Bash" "true" "legacy"
assert_nudges "heuristic says origin missing"  "origin not recorded" \
  "$no_profiles_listing" "gh pr create" "Bash" "true" "legacy"
# A profile that already carries an origin pointing elsewhere is someone else's;
# the heuristic must not claim it just because a session once ran here.
assert_silent "heuristic skips attributed profile" \
  "$no_profiles_listing" "gh pr create" "Bash" "true" "attributed"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
