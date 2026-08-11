#!/usr/bin/env bash
set -euo pipefail

# PR evidence recordings: capture an app window to mp4, publish mp4+gif to the
# evidence repo, and emit the markdown to paste into a PR description.
#
#   pr-evidence.sh record  --profile <name> [--seconds N] [--out FILE]
#                          (or --app <owner> for a non-attn window)
#   pr-evidence.sh publish [--dir <name>] [--fps N] [--width N] FILE.mp4 ...
#
# GitHub inline-renders a GIF referenced from a public repo's raw URL, but a
# repo-hosted mp4 (raw, blob, or release asset) is only ever a click-through
# link, and <video> tags are stripped. Receipts, one section per embed form:
# https://github.com/victorarias/attn-pr-evidence/issues/1. Hence the pair:
# GIF for the inline preview, mp4 as the full-quality master.

EVIDENCE_REPO="${ATTN_PR_EVIDENCE_REPO:-victorarias/attn-pr-evidence}"

usage() {
  sed -n '4,9p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "pr-evidence: $*" >&2
  exit 1
}

# Prints "<window-id> <width>" for the largest on-screen layer-0 window owned
# by $1; with WINID_LIST=1, prints every attn* owner instead (for the error
# path). CGWindowID is not reachable from AppleScript, so this goes through a
# throwaway swift script.
window_id_for_app() {
  local owner="$1"
  local swift_src
  swift_src="$(mktemp -t pr-evidence-winid).swift"
  cat > "$swift_src" <<'EOF'
import CoreGraphics
import Foundation
let opts: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
guard let list = CGWindowListCopyWindowInfo(opts, kCGNullWindowID) as? [[String: Any]] else { exit(1) }
let target = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : ""
var best: (id: Int, area: Int, width: Int) = (0, 0, 0)
var owners = Set<String>()
for w in list {
    guard (w[kCGWindowLayer as String] as? Int ?? -1) == 0 else { continue }
    let owner = w[kCGWindowOwnerName as String] as? String ?? ""
    if owner.hasPrefix("attn") { owners.insert(owner) }
    guard owner == target, let num = w[kCGWindowNumber as String] as? Int,
          let bounds = w[kCGWindowBounds as String] as? [String: Any],
          let width = bounds["Width"] as? Int, let height = bounds["Height"] as? Int
    else { continue }
    if width * height > best.area { best = (num, width * height, width) }
}
if ProcessInfo.processInfo.environment["WINID_LIST"] != nil {
    print(owners.sorted().joined(separator: " "))
    exit(0)
}
guard best.id != 0 else { exit(3) }
print("\(best.id) \(best.width)")
EOF
  local status=0
  swift "$swift_src" "$owner" || status=$?
  rm -f "$swift_src" "${swift_src%.swift}"
  return "$status"
}

cmd_record() {
  # 20s default: busy terminal content converts at ~320KB/s of gif at the
  # publish defaults (receipt: 6s live attn window -> 1.9MB), so 20s stays
  # well under GitHub's 10MB inline-render limit; 30s flirts with it.
  local app="" profile="" seconds=20 out=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --profile) profile="$2"; shift 2 ;;
      --app) app="$2"; shift 2 ;;
      --seconds) seconds="$2"; shift 2 ;;
      --out) out="$2"; shift 2 ;;
      *) die "record: unknown argument $1" ;;
    esac
  done
  # Record the profile under verification (attn-<profile>.app owns a window
  # named after itself; bare "prod" is attn.app). ATTN_PROFILE only exists in
  # the shell that eval'd profile-env, so it is a fallback, never the story.
  if [[ -z "$app" ]]; then
    profile="${profile:-${ATTN_PROFILE:-}}"
    [[ -n "$profile" ]] || die "which window? pass --profile <name> (the profile you are verifying on) or --app <owner>"
    if [[ "$profile" == "prod" ]]; then app="attn"; else app="attn-$profile"; fi
  fi
  [[ -n "$out" ]] || out="evidence-$(date +%Y%m%d-%H%M%S).mp4"

  local id_and_width
  if ! id_and_width="$(window_id_for_app "$app")"; then
    local candidates
    candidates="$(WINID_LIST=1 window_id_for_app "$app")"
    die "no on-screen window owned by \"$app\"; attn-family owners on screen: ${candidates:-none}. Pass --app <owner>."
  fi
  local win_id="${id_and_width%% *}"

  echo "recording window $win_id of $app for ${seconds}s -> $out"
  screencapture -x -v -V "$seconds" -l "$win_id" "$out"
  [[ -s "$out" ]] || die "screencapture produced no file (screen-recording permission?)"
  echo "recorded $out ($(du -h "$out" | cut -f1 | tr -d ' '))"
}

cmd_publish() {
  local dir="" fps=10 width=960
  local files=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --dir) dir="$2"; shift 2 ;;
      --fps) fps="$2"; shift 2 ;;
      --width) width="$2"; shift 2 ;;
      -*) die "publish: unknown argument $1" ;;
      *) files+=("$1"); shift ;;
    esac
  done
  [[ ${#files[@]} -gt 0 ]] || usage
  for f in "${files[@]}"; do
    [[ -f "$f" ]] || die "no such file: $f"
  done
  if [[ -z "$dir" ]]; then
    dir="$(git branch --show-current 2>/dev/null || true)"
    [[ -n "$dir" ]] || die "not on a git branch; pass --dir <name>"
  fi
  dir="${dir//\//-}"

  local clone
  clone="$(mktemp -d -t pr-evidence)"
  trap 'rm -rf "$clone"' EXIT
  gh repo clone "$EVIDENCE_REPO" "$clone" -- --depth 1 --quiet

  mkdir -p "$clone/$dir"
  local names=()
  for f in "${files[@]}"; do
    local base gif
    base="$(basename "$f")"
    gif="${base%.*}.gif"
    cp "$f" "$clone/$dir/$base"
    # Halve retina pixels, cap width, 10fps, per-clip palette. Receipt: a 3s
    # 3644x2370 attn window clip -> 362KB gif at these defaults.
    ffmpeg -loglevel error -y -i "$f" \
      -vf "fps=$fps,scale='min($width,iw/2)':-2:flags=lanczos,split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=dither=bayer:bayer_scale=4" \
      "$clone/$dir/$gif"
    local gif_bytes
    gif_bytes="$(stat -f %z "$clone/$dir/$gif")"
    if (( gif_bytes > 10485760 )); then
      echo "warning: $gif is $((gif_bytes / 1048576))MB; GitHub inline-renders images only under 10MB — shorten the clip or lower --fps/--width" >&2
    fi
    names+=("$base")
  done

  git -C "$clone" add "$dir"
  git -C "$clone" commit --quiet -m "evidence: $dir"
  # Another agent may publish concurrently; rebase once and retry.
  git -C "$clone" push --quiet || { git -C "$clone" pull --rebase --quiet && git -C "$clone" push --quiet; } || die "push to $EVIDENCE_REPO failed"
  local sha
  sha="$(git -C "$clone" rev-parse HEAD)"

  echo
  echo "published to $EVIDENCE_REPO@${sha:0:10}/$dir — paste into the PR description:"
  echo
  for base in "${names[@]}"; do
    local raw="https://raw.githubusercontent.com/$EVIDENCE_REPO/$sha/$dir"
    echo "![${base%.*}]($raw/${base%.*}.gif)"
    echo
    echo "[Full-quality recording (mp4)]($raw/$base)"
    echo
  done
}

case "${1:-}" in
  record) shift; cmd_record "$@" ;;
  publish) shift; cmd_publish "$@" ;;
  *) usage ;;
esac
