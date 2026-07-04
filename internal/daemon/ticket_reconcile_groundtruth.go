package daemon

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Deterministic, annotate-only ground-truth cross-check for reconciliation
// verdicts. The classifier's verdict is produced from a stale, pre-sliced
// transcript and can contradict state attn already tracks (e.g. it can say a
// PR merge is "pending" hours after the PR actually merged). This never
// mutates the verdict itself: it only extracts PR references from the
// verdict's free text and, when the daemon's own tracked-PR store positively
// confirms one of those PRs is no longer open, appends a "Ground-truth check"
// line to the posted comment. It makes no network calls and degrades to
// silence on any ambiguity (untracked PR number, unresolved repo, etc.).

// groundTruthMaxLines caps how many Ground-truth check lines can be appended
// to a single reconciliation comment.
const groundTruthMaxLines = 5

// groundTruthMaxPRNumber is a garbage guard: PR numbers extracted from free
// text above this are treated as noise (e.g. a misparsed line number or hash)
// rather than a real PR reference.
const groundTruthMaxPRNumber = 100000

var (
	prHashRefPattern   = regexp.MustCompile(`#(\d+)`)
	prWordRefPattern   = regexp.MustCompile(`(?i)\bPR\s+(\d+)\b`)
	prGitHubURLPattern = regexp.MustCompile(`(?i)github\.com/[\w.-]+/[\w.-]+/pull/(\d+)`)
)

// extractPRRefs scans text for PR number references (#123, "PR 123", and
// github.com/.../pull/123 URLs), dedupes them, and returns them in first-seen
// order. Numbers above groundTruthMaxPRNumber are dropped as garbage.
func extractPRRefs(text string) []int {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var refs []int
	seen := make(map[int]bool)
	add := func(numStr string) {
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 || n > groundTruthMaxPRNumber {
			return
		}
		if seen[n] {
			return
		}
		seen[n] = true
		refs = append(refs, n)
	}

	// Match all three patterns over the full text and then sort matches by
	// position so "first-seen order" is stable across pattern types.
	type match struct {
		pos int
		num string
	}
	var matches []match
	for _, pat := range []*regexp.Regexp{prHashRefPattern, prWordRefPattern, prGitHubURLPattern} {
		for _, loc := range pat.FindAllStringSubmatchIndex(text, -1) {
			matches = append(matches, match{pos: loc[0], num: text[loc[2]:loc[3]]})
		}
	}
	// Sort by position in the source text so "first-seen order" is stable
	// across pattern types (all three patterns are matched independently
	// above).
	sort.Slice(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })
	for _, m := range matches {
		add(m.num)
	}
	return refs
}

// groundTruthTerminalStates are the PR.State values that positively confirm a
// PR is finished (no longer open on GitHub). This is a positive allowlist,
// not a "not open" blacklist: `internal/store`'s `prs` table is populated
// exclusively from `is:open` GitHub search queries and is fully replaced
// (delete + reinsert) on every poll, so a currently-tracked PR's `State`
// field never holds a GitHub open/closed/merged value at all — it holds
// attn's own workflow annotation (today, only protocol.PRStateWaiting =
// "waiting", meaning "needs attention"). A "not open" blacklist would treat
// every tracked (i.e. actually still-open) PR as finished and misfire on
// every verdict that references one. An allowlist stays silent unless a PR
// row explicitly says "merged" or "closed" — which nothing in this codebase
// writes today, so in practice this cross-check does not yet fire against
// live data. It is forward-compatible: if the poller is ever extended to
// persist terminal GitHub states, this starts firing correctly with no
// further changes here.
var groundTruthTerminalStates = map[string]bool{
	"merged": true,
	"closed": true,
}

// reconcileGroundTruthLines cross-checks refs against prs (the daemon's own
// tracked-PR rows for repoSlug) and returns one "Ground-truth check" line per
// referenced PR whose State positively confirms it is merged or closed. PRs
// that are still open, in some other non-terminal state, or whose number
// isn't tracked at all, produce no line — silence is the default; this only
// fires when attn positively knows the referenced PR is finished. Capped at
// groundTruthMaxLines lines.
func reconcileGroundTruthLines(refs []int, repoSlug string, prs []*protocol.PR) []string {
	if repoSlug == "" || len(prs) == 0 || len(refs) == 0 {
		return nil
	}

	byNumber := make(map[int]*protocol.PR, len(prs))
	for _, pr := range prs {
		if pr == nil {
			continue
		}
		byNumber[pr.Number] = pr
	}

	var lines []string
	for _, n := range refs {
		if len(lines) >= groundTruthMaxLines {
			break
		}
		pr, ok := byNumber[n]
		if !ok || pr == nil {
			continue // untracked: silent
		}
		if !groundTruthTerminalStates[strings.ToLower(pr.State)] {
			continue // not a confirmed-terminal state: silent
		}
		lines = append(lines, fmt.Sprintf(
			"Ground-truth check: PR #%d is %s (%q) — the verdict text may be stale on this point.",
			n, pr.State, pr.Title))
	}
	return lines
}
