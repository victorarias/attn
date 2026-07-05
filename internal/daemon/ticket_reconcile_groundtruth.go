package daemon

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

// Annotate-only ground-truth cross-check for reconciliation verdicts. The
// classifier's verdict is produced from a stale, pre-sliced transcript and
// can contradict reality (e.g. it can say a PR merge is "pending" hours
// after the PR actually merged). This never mutates the verdict itself: it
// extracts PR references from the verdict's free text and appends
// "Ground-truth check" lines to the posted comment when a referenced PR is
// positively known to be merged or closed. Two sources feed that knowledge:
//
//  1. The daemon's own tracked-PR rows (deterministic, no network). Note the
//     store only tracks OPEN PRs today (see groundTruthTerminalStates), so
//     this leg is forward-compatible rather than load-bearing.
//  2. For referenced PRs ABSENT from the tracked open set — the common shape
//     of the real bug, since merged/closed PRs vanish from the poller's
//     `is:open` sweep — up to groundTruthMaxLookups targeted single-request
//     GitHub lookups resolve the definitive state.
//
// Everything degrades to silence: no cwd, no origin remote, no GitHub client
// for the host, lookup errors, or a still-open PR all produce no annotation,
// and nothing here can fail the reconcile.

// groundTruthMaxLines caps how many Ground-truth check lines can be appended
// to a single reconciliation comment.
const groundTruthMaxLines = 5

// groundTruthMaxLookups caps the targeted GitHub lookups per reconcile: a
// verdict is short free text, so more than a few PR references is noise, and
// each lookup is a real API request.
const groundTruthMaxLookups = 3

// groundTruthLookupTimeout bounds the TOTAL added latency of the lookup leg;
// the executor's context is wrapped with it so a slow or wedged GitHub call
// cannot stall the verdict comment.
const groundTruthLookupTimeout = 10 * time.Second

// prStateFetcher resolves a PR's definitive lifecycle state. Production wires
// github.Client.FetchPRState for the repo's host; tests substitute a fake via
// the Daemon.ticketReconcilePRFetch seam.
type prStateFetcher func(repo string, number int) (state string, merged bool, title string, err error)

// groundTruthMaxPRNumber is a garbage guard: PR numbers extracted from free
// text above this are treated as noise (e.g. a misparsed line number or hash)
// rather than a real PR reference.
const groundTruthMaxPRNumber = 100000

// groundTruthCaps records which best-effort limits the ground-truth
// cross-check hit while building annotation lines. It drives a single debug
// log line in reconcileGroundTruth; it never changes what gets annotated.
type groundTruthCaps struct {
	lineCap   bool // groundTruthMaxLines reached
	lookupCap bool // groundTruthMaxLookups reached
	timeout   bool // lookup budget (ctx) expired
}

func (c groundTruthCaps) any() bool { return c.lineCap || c.lookupCap || c.timeout }

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
// every verdict that references one. Nothing writes "merged"/"closed" rows
// today — the production path for finished PRs is the untracked-ref GitHub
// lookup (groundTruthUntrackedLines) — but this leg starts firing with no
// further changes if the poller is ever extended to persist terminal states.
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
func reconcileGroundTruthLines(refs []int, repoSlug string, prs []*protocol.PR) (lines []string, lineCap bool) {
	if repoSlug == "" || len(prs) == 0 || len(refs) == 0 {
		return nil, false
	}

	byNumber := make(map[int]*protocol.PR, len(prs))
	for _, pr := range prs {
		if pr == nil {
			continue
		}
		byNumber[pr.Number] = pr
	}

	for _, n := range refs {
		if len(lines) >= groundTruthMaxLines {
			lineCap = true
			break
		}
		pr, ok := byNumber[n]
		if !ok || pr == nil {
			continue // untracked: silent
		}
		if !groundTruthTerminalStates[strings.ToLower(pr.State)] {
			continue // not a confirmed-terminal state: silent
		}
		lines = append(lines, groundTruthLine(n, pr.State, pr.Title))
	}
	return lines, lineCap
}

func groundTruthLine(number int, state, title string) string {
	return fmt.Sprintf(
		"Ground-truth check: PR #%d is %s (%q) — the verdict text may be stale on this point.",
		number, state, title)
}

// groundTruthUntrackedLines resolves referenced PRs that are ABSENT from the
// tracked open set via targeted GitHub lookups. Because the store only tracks
// open PRs, absence is the expected signature of a merged/closed PR — but it
// can also mean "never tracked", so each candidate gets one definitive
// lookup, capped at groundTruthMaxLookups. Merged or closed results produce a
// line; open results, lookup errors, or a nil fetcher produce silence.
func groundTruthUntrackedLines(ctx context.Context, refs []int, tracked map[int]bool, repoSlug string, fetch prStateFetcher) (lines []string, caps groundTruthCaps) {
	if fetch == nil || repoSlug == "" || len(refs) == 0 {
		return nil, groundTruthCaps{}
	}

	lookups := 0
	for _, n := range refs {
		if tracked[n] {
			continue // tracked rows are the deterministic leg's business
		}
		if len(lines) >= groundTruthMaxLines {
			caps.lineCap = true
			break
		}
		if lookups >= groundTruthMaxLookups {
			caps.lookupCap = true
			break
		}
		if ctx.Err() != nil {
			caps.timeout = true
			break // overall lookup budget spent
		}
		lookups++
		state, merged, title, err := fetchPRStateCtx(ctx, fetch, repoSlug, n)
		if err != nil {
			continue // silent: no positive knowledge
		}
		switch {
		case merged:
			lines = append(lines, groundTruthLine(n, "merged", title))
		case strings.EqualFold(state, "closed"):
			lines = append(lines, groundTruthLine(n, "closed", title))
		}
	}
	return lines, caps
}

// fetchPRStateCtx runs fetch under ctx: github.Client has no context plumbing
// (its HTTP client owns a 30s per-request timeout), so the call runs in a
// goroutine and the result is abandoned if ctx expires first — the goroutine
// then finishes harmlessly against its own HTTP timeout.
func fetchPRStateCtx(ctx context.Context, fetch prStateFetcher, repo string, number int) (state string, merged bool, title string, err error) {
	type result struct {
		state  string
		merged bool
		title  string
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		s, m, t, e := fetch(repo, number)
		ch <- result{s, m, t, e}
	}()
	select {
	case r := <-ch:
		return r.state, r.merged, r.title, r.err
	case <-ctx.Done():
		return "", false, "", ctx.Err()
	}
}

// reconcileGroundTruth assembles the full cross-check for a verdict: resolve
// the ticket cwd's origin into host + owner/name slug, extract PR references
// from the verdict's free text, annotate from tracked rows (deterministic),
// then resolve untracked references through the host's GitHub client (capped,
// time-bounded). Returns the lines to append to the comment; empty on any
// missing prerequisite.
func (d *Daemon) reconcileGroundTruth(ctx context.Context, verdict *ticketReconcileVerdict, cwd string) []string {
	if verdict == nil {
		return nil
	}
	host, repoSlug := git.OriginHostOwnerRepo(cwd)
	if repoSlug == "" {
		return nil
	}
	refs := extractPRRefs(verdict.WhatsLeft + "\n" + verdict.Evidence)
	if len(refs) == 0 {
		return nil
	}

	prs := d.store.ListPRsByRepo(repoSlug)
	lines, trackedLineCap := reconcileGroundTruthLines(refs, repoSlug, prs)

	tracked := make(map[int]bool, len(prs))
	for _, pr := range prs {
		if pr != nil {
			tracked[pr.Number] = true
		}
	}

	fetch := d.ticketReconcilePRFetch
	if fetch == nil && d.githubAvailable() {
		if client, ok := d.ghRegistry.Get(host); ok {
			fetch = client.FetchPRState
		}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, groundTruthLookupTimeout)
	defer cancel()
	untracked, caps := groundTruthUntrackedLines(lookupCtx, refs, tracked, repoSlug, fetch)
	lines = append(lines, untracked...)
	if trackedLineCap {
		caps.lineCap = true
	}

	if len(lines) > groundTruthMaxLines {
		lines = lines[:groundTruthMaxLines]
		caps.lineCap = true
	}
	if caps.any() {
		d.logf("ticket reconcile ground-truth %s: annotation cap reached (lineCap=%t lookupCap=%t timeout=%t; refs=%d lines=%d)",
			repoSlug, caps.lineCap, caps.lookupCap, caps.timeout, len(refs), len(lines))
	}
	return lines
}
