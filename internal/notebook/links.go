package notebook

import "regexp"

// rootAbsoluteLinkRE matches markdown links whose target is a root-absolute
// notebook path, e.g. [a decision](/memory/decisions/foo.md). External
// (http://…), relative (foo.md), and anchor-only (#section) targets are
// intentionally not matched: the Notebook linking convention is root-absolute
// markdown only — no [[wikilinks]] — so the targets resolve without an external
// resolver and survive moves.
var rootAbsoluteLinkRE = regexp.MustCompile(`\[[^\]]*\]\((/[^)\s]+)\)`)

// Links extracts the root-absolute link targets from markdown body text, in
// first-seen order with duplicates removed. Any #anchor suffix is preserved on
// the returned target.
func Links(body string) []string {
	matches := rootAbsoluteLinkRE.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		target := m[1]
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}
