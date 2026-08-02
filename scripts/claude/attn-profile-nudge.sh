#!/usr/bin/env bash
# Claude Code PostToolUse hook: after a PR is created or merged from this
# worktree, remind the agent to clean up any throwaway attn profile it installed
# from here.
#
# Why this exists: a profile is cheap to create and invisible once made. Its
# daemon and pty-workers keep running long after the branch that needed them is
# merged, and nothing ever reaps them — an idle profile costs ~40MB for the
# daemon plus ~15MB per worker, indefinitely. The agent that made it is the only
# one that knows whether it is still needed, and the PR is the moment it knows.
#
# The hook is silent unless this worktree actually owns a profile with something
# running, so agents that never installed one never see it.
#
# stdin:  Claude Code hook payload (JSON)
# stdout: hook JSON with additionalContext, or nothing
# Always exits 0 — a cleanup reminder must never fail a PR.
set -uo pipefail

payload="$(cat)"

# This runs on every Bash tool call, and the overwhelming majority are not PR
# milestones. Reject those on a single grep over the raw payload before spawning
# anything — the precise match on the parsed command happens below.
printf '%s' "$payload" | grep -qE 'gh[^"]*pr|stack[^"]*merge' || exit 0

# jq is the only hard dependency; without it, stay silent rather than guess.
command -v jq >/dev/null 2>&1 || exit 0

tool_name="$(printf '%s' "$payload" | jq -r '.tool_name // empty')"
[ "$tool_name" = "Bash" ] || exit 0

command_line="$(printf '%s' "$payload" | jq -r '.tool_input.command // empty')"
[ -n "$command_line" ] || exit 0

# Match PR creation and merge, via gh or the repo's stack CLI. Deliberately
# narrow: `gh pr view`, `gh pr checks`, and friends are not "done" signals.
if ! printf '%s' "$command_line" | grep -Eq '(^|[;&|[:space:]])(gh[[:space:]]+pr[[:space:]]+(create|merge)|stack[[:space:]]+merge)([[:space:]]|$)'; then
  exit 0
fi

# A failed command is not a milestone. Tested explicitly rather than with `//`,
# which treats a literal false as empty and would read a failure as a success.
failed="$(printf '%s' "$payload" | jq -r '
  if (.tool_response | type) == "object" and
     (.tool_response.success == false or .tool_response.is_error == true)
  then "yes" else "no" end')"
[ "$failed" = "no" ] || exit 0

worktree="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
[ -n "$worktree" ] || exit 0

# Prefer the freshly built binary in the worktree — a repo-local hook should
# reflect the branch's own CLI — then fall back to whatever is installed.
attn_bin=""
for candidate in "$worktree/attn" "$HOME/Applications/attn.app/Contents/MacOS/attn"; do
  if [ -x "$candidate" ]; then
    attn_bin="$candidate"
    break
  fi
done
[ -n "$attn_bin" ] || exit 0

listing="$("$attn_bin" profile list --json 2>/dev/null)" || exit 0
[ -n "$listing" ] || exit 0

# Attribute profiles to this worktree, and keep only those with something left
# to clean. Recorded origin is exact; the heuristic covers profiles installed
# before origin recording existed, by looking for a daemon or a session whose
# own cwd sits inside this worktree.
summary="$(printf '%s' "$listing" | jq -r --arg wt "$worktree" '
  [ .profiles[]
    | select(.profile != "")
    | select(.origin != null and .origin.worktree == $wt)
    | select(.hasData or .hasApp)
  ]
  | map("  - \(.label): "
      + (if .daemonRunning then "daemon running" else "daemon stopped" end)
      + (if .liveWorkers > 0 then ", \(.liveWorkers) pty-worker(s)" else "" end)
      + (if .origin.branch != null and .origin.branch != "" then " [installed from \(.origin.branch)]" else "" end))
  | join("\n")
')"

if [ -z "$summary" ]; then
  # No profile recorded against this worktree. Fall back to the heuristic for
  # profiles that predate origin recording: a daemon running out of this
  # worktree's own binary, or a registered session whose cwd is inside it.
  heuristic=""
  for datadir in "$HOME"/.attn-*; do
    [ -d "$datadir" ] || continue
    [ -f "$datadir/origin.json" ] && continue
    if ls "$datadir"/workers/*/registry/*.json >/dev/null 2>&1; then
      if grep -lF "\"cwd\": \"$worktree\"" "$datadir"/workers/*/registry/*.json >/dev/null 2>&1; then
        heuristic="$heuristic
  - ${datadir##*/.attn-}: has a session whose cwd is this worktree (origin not recorded)"
      fi
    fi
  done
  summary="$(printf '%s' "$heuristic" | sed '/^$/d')"
fi

[ -n "$summary" ] || exit 0

verb="created"
printf '%s' "$command_line" | grep -Eq 'merge' && verb="merged"

context="You just $verb a PR from this worktree, and it still owns attn profile(s):

$summary

If you are done with the profile, clean it up now:

  attn profile clean <name>

That reaps its pty-workers, stops its daemon, quits its app, and removes the app bundle and data dir. Skip this if you still need the profile (CI fixes, live verification, follow-up work) — but do not leave it running after the work is done: nothing else ever reaps it.

Do not clean the default (production) profile."

jq -n --arg ctx "$context" '{
  hookSpecificOutput: {
    hookEventName: "PostToolUse",
    additionalContext: $ctx
  }
}'
exit 0
