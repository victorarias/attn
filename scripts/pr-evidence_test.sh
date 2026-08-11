#!/usr/bin/env bash
# Tests for `pr-evidence.sh publish`.
#
# The publish path is what an agent scripts against, so its exit code and its
# emitted markdown are the contract; the temp clone it makes along the way is
# nobody's business but its own, which is exactly why a leak goes unnoticed.
# Run: bash scripts/pr-evidence_test.sh
#
# The `record` half stays uncovered: screencapture and swift need a real
# window server.
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/pr-evidence.sh"

pass=0
fail=0

check() {
  local label="$1" condition="$2"
  if [[ "$condition" == "ok" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $label"
    echo "  $condition"
  fi
}

# One sandbox per run: a bare repo standing in for the evidence repo, fake `gh`
# and `ffmpeg` ahead of the real ones on PATH, and a private TMPDIR so a leaked
# temp dir is countable rather than lost among the machine's.
setup_sandbox() {
  sandbox="$(mktemp -d "${TMPDIR:-/tmp}/pr-evidence-test.XXXXXX")"
  mkdir -p "$sandbox/bin" "$sandbox/tmp"

  # -c identity everywhere: CI checkouts have no user.name/user.email of their
  # own, and a commit without one fails before any assertion runs.
  git init -q --bare "$sandbox/remote.git"
  git init -q "$sandbox/seed"
  : > "$sandbox/seed/README.md"
  git -C "$sandbox/seed" add README.md
  git -C "$sandbox/seed" -c user.name=test -c user.email=test@example.com commit -q -m "seed"
  git -C "$sandbox/seed" push -q "$sandbox/remote.git" HEAD:refs/heads/main

  cat > "$sandbox/bin/gh" <<EOF
#!/usr/bin/env bash
[ "\$1" = "repo" ] && [ "\$2" = "clone" ] || exit 1
git clone -q "$sandbox/remote.git" "\$4"
EOF

  # The real one renders a gif; all this one has to be is a file with bytes.
  cat > "$sandbox/bin/ffmpeg" <<'EOF'
#!/usr/bin/env bash
out=""
for arg in "$@"; do out="$arg"; done
printf 'GIF89a-fake' > "$out"
EOF

  chmod +x "$sandbox/bin/gh" "$sandbox/bin/ffmpeg"
  printf 'not really an mp4' > "$sandbox/clip.mp4"
}

run_publish() {
  env PATH="$sandbox/bin:$PATH" \
    TMPDIR="$sandbox/tmp" \
    ATTN_PR_EVIDENCE_REPO="owner/evidence" \
    GIT_AUTHOR_NAME="test" GIT_AUTHOR_EMAIL="test@example.com" \
    GIT_COMMITTER_NAME="test" GIT_COMMITTER_EMAIL="test@example.com" \
    bash "$script" publish "$@" 2>"$sandbox/stderr.txt"
}

leftover_temp_dirs() {
  find "$sandbox/tmp" -maxdepth 1 -name 'pr-evidence.*' 2>/dev/null | wc -l | tr -d ' '
}

# --- a publish that works reports that it worked -----------------------------
setup_sandbox
output="$(run_publish --dir some-branch "$sandbox/clip.mp4")"
status=$?

check "publish exits 0" \
  "$([[ $status -eq 0 ]] && echo ok || echo "exit=$status stderr=$(cat "$sandbox/stderr.txt")")"

check "publish emits the gif embed" \
  "$([[ "$output" == *"![clip]("*"/some-branch/clip.gif)"* ]] && echo ok || echo "output=$output")"

check "publish emits the mp4 link" \
  "$([[ "$output" == *"/some-branch/clip.mp4)"* ]] && echo ok || echo "output=$output")"

pushed="$(git -C "$sandbox/remote.git" ls-tree -r --name-only HEAD | sort | tr '\n' ' ')"
check "both files reach the evidence repo" \
  "$([[ "$pushed" == *"some-branch/clip.gif"* && "$pushed" == *"some-branch/clip.mp4"* ]] && echo ok || echo "tree=$pushed")"

sha="$(git -C "$sandbox/remote.git" rev-parse HEAD)"
check "the markdown points at the commit it just pushed" \
  "$([[ "$output" == *"$sha"* ]] && echo ok || echo "sha=$sha output=$output")"

# The regression: the EXIT trap used to read a `local` that was already out of
# scope, so it died under `set -u` — publish exited 1 after succeeding, and the
# clone stayed on disk.
check "the temp clone is cleaned up" \
  "$([[ "$(leftover_temp_dirs)" == "0" ]] && echo ok || echo "left $(leftover_temp_dirs) behind")"

rm -rf "$sandbox"

# --- a publish that cannot work says why -------------------------------------
setup_sandbox
output="$(run_publish --dir some-branch "$sandbox/missing.mp4")"
status=$?

check "a missing file exits non-zero" \
  "$([[ $status -ne 0 ]] && echo ok || echo "exit=$status")"

check "a missing file is named in the error" \
  "$(grep -q "no such file: $sandbox/missing.mp4" "$sandbox/stderr.txt" && echo ok || echo "stderr=$(cat "$sandbox/stderr.txt")")"

rm -rf "$sandbox"

echo "pr-evidence: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
