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

// Annotate-only ground-truth cross-check for reconciliation verdicts: it never
// mutates the verdict, only appends "Ground-truth check" lines when a PR the
// verdict references is positively known merged/closed. Two sources: the
// daemon's tracked-PR rows (deterministic), and capped GitHub lookups for refs
// absent from the tracked open set — the common shape, since finished PRs
// vanish from the `is:open` sweep. Everything degrades to silence; nothing here
// can fail the reconcile.

// groundTruthMaxLines caps Ground-truth check lines per reconciliation comment.
const groundTruthMaxLines = 5

// groundTruthMaxLookups caps the targeted GitHub lookups per reconcile; each is
// a real API request.
const groundTruthMaxLookups = 3

// groundTruthLookupTimeout bounds the TOTAL added latency of the lookup leg so
// a wedged GitHub call cannot stall the verdict comment.
const groundTruthLookupTimeout = 10 * time.Second

// prStateFetcher resolves a PR's definitive lifecycle state; tests substitute a
// fake via the Daemon.ticketReconcilePRFetch seam.
type prStateFetcher func(repo string, number int) (state string, merged bool, title string, err error)

// groundTruthMaxPRNumber is a garbage guard: numbers above this are treated as
// noise (a misparsed line number or hash), not a PR reference.
const groundTruthMaxPRNumber = 100000

// groundTruthCaps records which best-effort limits were hit; it drives one log
// line and never changes what gets annotated.
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

// extractPRRefs scans text for PR references (#123, "PR 123", pull URLs),
// deduped, in first-seen order; numbers past groundTruthMaxPRNumber are dropped.
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
	// Sort by source position so "first-seen order" is stable across patterns.
	sort.Slice(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })
	for _, m := range matches {
		add(m.num)
	}
	return refs
}

// groundTruthTerminalStates must stay a positive allowlist, never a "not open"
// blacklist: the prs table holds only `is:open` results and its State field
// carries attn's workflow annotation ("waiting"), not GitHub lifecycle, so a
// blacklist would call every tracked still-open PR finished. Nothing writes
// merged/closed rows today; this leg starts firing if the poller ever does.
var groundTruthTerminalStates = map[string]bool{
	"merged": true,
	"closed": true,
}

// reconcileGroundTruthLines cross-checks refs against the tracked-PR rows,
// emitting one line per PR whose State positively confirms merged/closed;
// silence otherwise. Capped at groundTruthMaxLines.
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

// groundTruthUntrackedLines resolves refs ABSENT from the tracked open set —
// absence is the expected signature of a finished PR, but can also mean "never
// tracked", so each candidate gets one definitive lookup, capped. Merged/closed
// produce a line; open results, lookup errors, or a nil fetcher produce silence.
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

// fetchPRStateCtx runs fetch under ctx: github.Client has no context plumbing,
// so the call runs in a goroutine and is abandoned if ctx expires first — it
// then finishes harmlessly against its own 30s HTTP timeout.
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

// reconcileGroundTruth assembles the full cross-check for a verdict and returns
// the lines to append to the comment; empty on any missing prerequisite.
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
