#!/usr/bin/env bash
#
# Harvest failing tests from GitHub Actions CI history and rank them by how
# often they fail, separating flakes from genuine breakage.
#
# A failure is evidence of flakiness when the same commit is known to pass:
# either a later attempt of the same run succeeded (the strongest signal, since
# nothing about the code changed), or another run on the same SHA succeeded.
# A failure on a SHA that never passed is reported as unresolved, which usually
# means a real break that a follow-up commit fixed.
#
# Job logs and per-attempt job lists are immutable once a run finishes, so both
# are cached on disk. Re-running the report over the same window is cheap; only
# newly finished runs cost API calls.
#
# Usage:
#   scripts/ci-flake-report.sh [--limit N] [--format table|jsonl|summary|markdown]
#                              [--repo OWNER/REPO] [--workflow FILE]
#                              [--branch NAME] [--since YYYY-MM-DD]
#                              [--cache DIR] [--no-cache]
#
# Formats:
#   table    ranked flake ledger plus unresolved failures (default)
#   summary  one line per test, machine-readable TSV, flakes only
#   jsonl    one JSON object per individual failure occurrence
#   markdown tracking-issue body, consumed by the flake-ledger workflow

set -euo pipefail

repo="${ATTN_FLAKE_REPO:-victorarias/attn}"
workflow="ci.yml"
limit=200
format="table"
branch=""
since=""
cache="${ATTN_FLAKE_CACHE:-${TMPDIR:-/tmp}/attn-ci-flakes}"
use_cache=true
dormant_after=60

while [ $# -gt 0 ]; do
  case "$1" in
    --limit) limit="$2"; shift 2 ;;
    --format) format="$2"; shift 2 ;;
    --repo) repo="$2"; shift 2 ;;
    --workflow) workflow="$2"; shift 2 ;;
    --branch) branch="$2"; shift 2 ;;
    --since) since="$2"; shift 2 ;;
    --dormant-after) dormant_after="$2"; shift 2 ;;
    --cache) cache="$2"; shift 2 ;;
    --no-cache) use_cache=false; shift ;;
    -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$format" in
  table|jsonl|summary|markdown) ;;
  *) echo "unknown format: $format (want table, jsonl, summary, or markdown)" >&2; exit 2 ;;
esac

for tool in gh jq awk; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done

mkdir -p "$cache/logs" "$cache/jobs"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-flake.XXXXXX")"
# shellcheck disable=SC2329 # invoked indirectly by trap
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

# ---------------------------------------------------------------- run history

runs="$work/runs.json"
run_args=(--workflow "$workflow" --limit "$limit" --repo "$repo"
  --json databaseId,headSha,headBranch,conclusion,createdAt,attempt,event,status)
[ -n "$branch" ] && run_args+=(--branch "$branch")
gh run list "${run_args[@]}" >"$runs"

if [ -n "$since" ]; then
  jq --arg since "$since" '[.[] | select(.createdAt >= $since)]' "$runs" >"$work/runs.filtered"
  mv "$work/runs.filtered" "$runs"
fi

# Only completed runs carry a trustworthy verdict; in-flight ones are skipped so
# a run that is still red-but-running is not mistaken for a finished failure.
jq '[.[] | select(.status == "completed")]' "$runs" >"$work/runs.completed"
mv "$work/runs.completed" "$runs"

total_runs="$(jq 'length' "$runs")"
[ "$total_runs" -eq 0 ] && { echo "no completed runs found" >&2; exit 1; }

# ------------------------------------------------------------- job discovery

# Job lists for a finished attempt never change, so they cache by (run,attempt).
fetch_jobs() {
  local run_id="$1" attempt="$2" dest="$cache/jobs/$run_id-$attempt.json"
  if [ "$use_cache" = true ] && [ -s "$dest" ]; then
    cat "$dest"
    return 0
  fi
  local tmp="$dest.$$.tmp"
  if gh api "repos/$repo/actions/runs/$run_id/attempts/$attempt/jobs?per_page=100" >"$tmp" 2>/dev/null; then
    mv -f "$tmp" "$dest"
    cat "$dest"
  else
    rm -f "$tmp"
    echo '{"jobs":[]}'
  fi
}

fetch_log() {
  local job_id="$1" dest="$cache/logs/$job_id.log"
  if [ "$use_cache" = true ] && [ -s "$dest" ]; then
    cat "$dest"
    return 0
  fi
  local tmp="$dest.$$.tmp"
  if gh api "repos/$repo/actions/jobs/$job_id/logs" >"$tmp" 2>/dev/null; then
    mv -f "$tmp" "$dest"
    cat "$dest"
  else
    rm -f "$tmp"
    return 0
  fi
}

# ------------------------------------------------------------ log extraction

# Emits "kind<TAB>name" per failing test. Strips the Actions timestamp prefix and
# ANSI colour first so the matchers see the raw runner output.
extract_failures() {
  sed -E 's/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z //; s/\x1b\[[0-9;]*[a-zA-Z]//g' \
    | awk -F'\t' '
      # Go: collect top-level test names, then attribute them to the package
      # named by the trailing FAIL line. go test buffers a package block
      # together even under -p, so the pending list belongs to that package.
      /^--- FAIL: / {
        split($0, a, " ")
        pending[++np] = a[3]
        next
      }
      /^FAIL\tgithub\.com\// {
        for (i = 1; i <= np; i++) print "go\t" $2 "\t" pending[i]
        np = 0
        next
      }
      # A test binary killed by the -timeout guard never prints --- FAIL.
      /^panic: test timed out after / {
        timedout = 1
        next
      }
      # Playwright: drop the line:col so a key survives edits above the test.
      /^ *✘ / {
        line = $0
        sub(/^ *✘ +[0-9]+ +/, "", line)
        sub(/ \([0-9.]+m?s\) *$/, "", line)
        sub(/\.spec\.ts:[0-9]+:[0-9]+ ›/, ".spec.ts ›", line)
        if (line != "") print "playwright\t-\t" line
        next
      }
      # Vitest prints a FAIL line per failing test with its full suite path.
      /^ *FAIL +[^ ]+\.(test|spec)\.(ts|tsx|js|jsx) *>/ {
        line = $0
        sub(/^ *FAIL +/, "", line)
        if (line != "") print "vitest\t-\t" line
        next
      }
      # Non-test breakage: report it, but never count it as a flake candidate.
      /^Go files are not formatted/ { build = "gofmt" }
      /^# github\.com\// { if (build == "") build = "compile" }
      /^error TS[0-9]+/ { if (build == "") build = "typescript" }
      END {
        for (i = 1; i <= np; i++) print "go\tunknown\t" pending[i]
        if (timedout && np == 0) print "go\t-\tTEST-TIMEOUT"
        if (build != "") print "build\t-\t" build
      }
    '
}

# --------------------------------------------------------------- harvest loop

failures="$work/failures.jsonl"
: >"$failures"
scanned_jobs=0

while IFS=$'\t' read -r run_id sha branch_name created attempt conclusion; do
  # Attempts before the latest one exist only because the run was re-run, and
  # every job in them that failed is a failure the same commit later retried.
  # Scanning them is what surfaces flakes that a rerun already papered over.
  last_failing_attempt="$attempt"
  [ "$conclusion" = "success" ] && last_failing_attempt=$((attempt - 1))

  n=1
  while [ "$n" -le "$attempt" ]; do
    if [ "$n" -gt "$last_failing_attempt" ]; then break; fi
    jobs_json="$(fetch_jobs "$run_id" "$n")"
    attempt_conclusion="failed"
    [ "$n" -eq "$attempt" ] && attempt_conclusion="$conclusion"

    while IFS=$'\t' read -r job_id job_name; do
      [ -z "${job_id:-}" ] && continue
      scanned_jobs=$((scanned_jobs + 1))
      log="$work/job.log"
      fetch_log "$job_id" >"$log" || continue
      [ -s "$log" ] || continue

      while IFS=$'\t' read -r kind pkg test; do
        [ -z "${test:-}" ] && continue
        jq -nc \
          --arg run "$run_id" --arg attempt "$n" --arg sha "$sha" \
          --arg branch "$branch_name" --arg created "$created" \
          --arg job "$job_name" --arg kind "$kind" --arg pkg "$pkg" \
          --arg test "$test" --arg run_conclusion "$conclusion" \
          --arg attempt_conclusion "$attempt_conclusion" --arg attempts "$attempt" \
          '{run: $run, attempt: ($attempt|tonumber), attempts: ($attempts|tonumber),
            sha: $sha, branch: $branch, created: $created, job: $job,
            kind: $kind, package: $pkg, test: $test,
            run_conclusion: $run_conclusion, attempt_conclusion: $attempt_conclusion}' \
          >>"$failures"
      done < <(extract_failures <"$log")
    done < <(jq -r '.jobs[] | select(.conclusion == "failure") | [(.id|tostring), .name] | @tsv' <<<"$jobs_json")

    n=$((n + 1))
  done
done < <(jq -r '.[] | [(.databaseId|tostring), .headSha, .headBranch, .createdAt, (.attempt|tostring), .conclusion] | @tsv' "$runs")

# ------------------------------------------------------------- classification

# A commit is known-good when any completed run on it succeeded. Combined with
# the per-attempt record above this yields the verdict for each failure:
#   rerun-green  a later attempt of the SAME run passed — same code, so a flake
#   sha-green    another run on the same commit passed — flake
#   unresolved   the commit never passed — treat as real breakage until shown otherwise
jq -r '[.[] | select(.conclusion == "success") | .headSha] | unique' "$runs" >"$work/green.json"

classified="$work/classified.jsonl"
jq -c --slurpfile green "$work/green.json" '
  . as $f
  | .verdict = (
      if ($f.attempt < $f.attempts and $f.run_conclusion == "success") then "rerun-green"
      elif ($green[0] | index($f.sha)) then "sha-green"
      else "unresolved" end)
' "$failures" >"$classified"

if [ "$format" = "jsonl" ]; then
  cat "$classified"
  exit 0
fi

# --------------------------------------------------------------- aggregation

agg="$work/agg.json"
# Elapsed CI runs, not elapsed days, decide whether a flake is still live. A
# test that flaked 12 times and has since survived 150 runs untouched has been
# fixed; four quiet days over a weekend prove nothing either way.
jq -r 'map(.createdAt) | sort' "$runs" >"$work/created.json"

jq -s --slurpfile created "$work/created.json" '
  group_by([.kind, .package, .test])
  | map(
    ([.[] | .created] | max) as $last
    | {
      runs_since: ([$created[0][] | select(. > $last)] | length),
      kind: .[0].kind,
      package: .[0].package,
      test: .[0].test,
      job: .[0].job,
      total: length,
      flakes: [.[] | select(.verdict != "unresolved")] | length,
      unresolved: [.[] | select(.verdict == "unresolved")] | length,
      rerun_green: [.[] | select(.verdict == "rerun-green")] | length,
      shas: ([.[] | .sha] | unique | length),
      branches: ([.[] | .branch] | unique),
      last_seen: ([.[] | .created] | max),
      runs: ([.[] | .run] | unique)
    })
  | sort_by(-.flakes, -.total)
' "$classified" >"$agg"

if [ "$format" = "summary" ]; then
  jq -r '.[] | select(.flakes > 0)
    | [.kind, .package, .test, .flakes, .total, .shas, (.branches|length), .last_seen, .runs_since]
    | @tsv' "$agg"
  exit 0
fi

# ------------------------------------------------------------------ markdown

# Rendered into a tracking issue by .github/workflows/flake-ledger.yml. The
# leading marker comment is how that workflow finds the section it owns.
if [ "$format" = "markdown" ]; then
  md_window_start="$(jq -r 'map(.createdAt) | min' "$runs")"
  md_window_end="$(jq -r 'map(.createdAt) | max' "$runs")"
  md_failed="$(jq '[.[] | select(.conclusion != "success")] | length' "$runs")"
  md_rerun="$(jq '[.[] | select(.attempt > 1)] | length' "$runs")"

  md_table() {
    local filter="$1"
    printf '| Flakes | Reruns | Commits | Branches | Runs since | Last seen | Test |\n'
    printf '| ---: | ---: | ---: | ---: | ---: | --- | --- |\n'
    jq -r --argjson dormant "$dormant_after" ".[] | select(.flakes > 0) | select($filter)
      | [(.flakes|tostring), (.rerun_green|tostring), (.shas|tostring),
         ((.branches|length)|tostring), (.runs_since|tostring), (.last_seen[0:10]),
         (if .package == \"-\" or .package == \"\" then \"\" else (.package | sub(\"^github[.]com/[^/]+/[^/]+/\"; \"\") + \".\") end) + .test]
      | @tsv" "$agg" \
      | awk -F'\t' '{ printf "| %s | %s | %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $5, $6, $7 }'
  }

  printf '<!-- ci-flake-ledger -->\n'
  printf '# CI flake ledger\n\n'
  printf 'Generated by `scripts/ci-flake-report.sh` from the last %s runs of `%s` (%s .. %s).\n\n' \
    "$total_runs" "$workflow" "${md_window_start:0:10}" "${md_window_end:0:10}"
  printf -- '- %s runs completed, %s red on their final attempt, %s re-run at least once.\n' \
    "$total_runs" "$md_failed" "$md_rerun"
  printf -- '- A failure counts as a **flake** when the same commit is known to pass: a later attempt of the same run succeeded, or another run on that commit succeeded.\n'
  printf -- '- **Runs since** is how many CI runs have completed since the last occurrence. Past %s with no recurrence, an entry is treated as dormant.\n\n' "$dormant_after"

  printf '## Active\n\n'
  if [ "$(jq --argjson dormant "$dormant_after" '[.[] | select(.flakes > 0 and .runs_since < $dormant)] | length' "$agg")" -eq 0 ]; then
    printf 'None. \n\n'
  else
    md_table '.runs_since < $dormant'
    printf '\n'
  fi

  if [ "$(jq --argjson dormant "$dormant_after" '[.[] | select(.flakes > 0 and .runs_since >= $dormant)] | length' "$agg")" -gt 0 ]; then
    printf '## Dormant\n\nNo recurrence in the %s+ runs since; a fix most likely landed.\n\n' "$dormant_after"
    md_table '.runs_since >= $dormant'
    printf '\n'
  fi

  if [ "$(jq '[.[] | select(.flakes == 0 and .unresolved > 0)] | length' "$agg")" -gt 0 ]; then
    printf '## Unresolved\n\nThe commit never passed, so these are most likely real breakage rather than flakes.\n\n'
    printf '| Failures | Commits | Last seen | Test |\n| ---: | ---: | --- | --- |\n'
    jq -r '.[] | select(.flakes == 0 and .unresolved > 0)
      | [(.unresolved|tostring), (.shas|tostring), (.last_seen[0:10]),
         (if .package == "-" or .package == "" then "" else (.package | sub("^github[.]com/[^/]+/[^/]+/"; "") + ".") end) + .test]
      | @tsv' "$agg" \
      | awk -F'\t' '{ printf "| %s | %s | %s | %s |\n", $1, $2, $3, $4 }'
    printf '\n'
  fi
  exit 0
fi

# -------------------------------------------------------------------- report

window_start="$(jq -r 'map(.createdAt) | min' "$runs")"
window_end="$(jq -r 'map(.createdAt) | max' "$runs")"
failed_runs="$(jq '[.[] | select(.conclusion != "success")] | length' "$runs")"
rerun_runs="$(jq '[.[] | select(.attempt > 1)] | length' "$runs")"

printf 'CI flake report — %s (%s)\n' "$repo" "$workflow"
printf 'window   %s .. %s\n' "${window_start:0:10}" "${window_end:0:10}"
printf 'runs     %s completed, %s red on final attempt, %s re-run at least once\n' \
  "$total_runs" "$failed_runs" "$rerun_runs"
printf 'scanned  %s failed jobs\n\n' "$scanned_jobs"

flaky_total="$(jq '[.[] | select(.flakes > 0)] | length' "$agg")"
if [ "$flaky_total" -eq 0 ]; then
  printf 'No flakes found in this window.\n\n'
else
  # A long window mixes flakes that are still biting with ones a past fix
  # already killed, and raw totals rank the dead ones first because they had
  # more time to accumulate. Splitting on last-seen keeps the top of the report
  # actionable; the dormant list stays visible so a regression is noticeable.
  # A long window mixes flakes that are still biting with ones a past fix
  # already killed, and raw totals rank the dead ones first because they had
  # more time to accumulate. The split is on CI runs elapsed since the last
  # occurrence, not on wall-clock: a test that has since survived $dormant_after
  # runs is treated as fixed. Dormant entries stay listed so a regression shows.
  print_flake_table() {
    local filter="$1"
    printf '%-6s %-5s %-5s %-5s %-6s %-11s  %s\n' 'FLAKE' 'RERUN' 'SHAS' 'BRCH' 'SINCE' 'LAST SEEN' 'TEST'
    jq -r --argjson dormant "$dormant_after" ".[] | select(.flakes > 0) | select($filter)
      | [(.flakes|tostring), (.rerun_green|tostring), (.shas|tostring),
         ((.branches|length)|tostring), (.runs_since|tostring), (.last_seen[0:10]),
         (if .package == \"-\" or .package == \"\" then \"\" else (.package | sub(\"^github[.]com/[^/]+/[^/]+/\"; \"\") + \".\") end) + .test]
      | @tsv" "$agg" \
      | awk -F'\t' '{ printf "%-6s %-5s %-5s %-5s %-6s %-11s  %s\n", $1, $2, $3, $4, $5, $6, $7 }'
  }

  active_total="$(jq --argjson dormant "$dormant_after" '[.[] | select(.flakes > 0 and .runs_since < $dormant)] | length' "$agg")"
  dormant_total="$(jq --argjson dormant "$dormant_after" '[.[] | select(.flakes > 0 and .runs_since >= $dormant)] | length' "$agg")"

  printf 'ACTIVE FLAKES — failed on a known-good commit within the last %s CI runs\n' "$dormant_after"
  if [ "$active_total" -eq 0 ]; then
    printf '  (none)\n'
  else
    print_flake_table '.runs_since < $dormant'
  fi
  printf '\n'

  if [ "$dormant_total" -gt 0 ]; then
    printf 'DORMANT — no recurrence in the %s+ CI runs since; a fix most likely landed\n' "$dormant_after"
    print_flake_table '.runs_since >= $dormant'
    printf '\n'
  fi
fi

unresolved_total="$(jq '[.[] | select(.flakes == 0 and .unresolved > 0)] | length' "$agg")"
if [ "$unresolved_total" -gt 0 ]; then
  printf 'UNRESOLVED — commit never passed; likely real breakage, verify before dismissing\n'
  printf '%-6s %-5s  %s\n' 'FAILS' 'SHAS' 'TEST'
  jq -r '.[] | select(.flakes == 0 and .unresolved > 0)
    | [(.unresolved|tostring), (.shas|tostring),
       (if .package == "-" or .package == "" then "" else (.package | sub("^github\\.com/[^/]+/[^/]+/"; "") + ".") end) + .test]
    | @tsv' "$agg" \
    | awk -F'\t' '{ printf "%-6s %-5s  %s\n", $1, $2, $3 }'
  printf '\n'
fi

printf 'Cache: %s (delete to force a full refetch)\n' "$cache"
