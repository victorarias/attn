package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
)

const maxLabels = 3

// Format formats sessions for tmux status bar (backwards compatible)
func Format(sessions []protocol.Session) string {
	return FormatWithPRs(sessions, nil)
}

// FormatWithPRs formats sessions and PRs for status bar
func FormatWithPRs(sessions []protocol.Session, prs []protocol.PR) string {
	// Use attention aggregator to get items needing attention
	agg := attention.NewAggregator(nil, nil) // No repo/author muting in this variant
	result := agg.Aggregate(sessions, prs)

	if result.TotalCount == 0 {
		return "✓ all clear"
	}

	var parts []string

	// Sessions part
	sessionItems := result.Sessions()
	if len(sessionItems) > 0 {
		// Items are already sorted by Since (oldest first)
		var labels []string
		for i, item := range sessionItems {
			if i >= maxLabels {
				break
			}
			labels = append(labels, item.Label)
		}

		labelStr := strings.Join(labels, ", ")
		if len(sessionItems) > maxLabels {
			labelStr += ", ..."
		}
		parts = append(parts, fmt.Sprintf("%d waiting: %s", len(sessionItems), labelStr))
	}

	// PRs part - need original PR data for Number field
	if result.PRCount > 0 {
		// Filter PRs that need attention to get Number
		var waitingPRs []protocol.PR
		for _, pr := range prs {
			if pr.State == protocol.PRStateWaiting && !pr.Muted {
				waitingPRs = append(waitingPRs, pr)
			}
		}

		// Sort by ID for consistent display
		sort.Slice(waitingPRs, func(i, j int) bool {
			return waitingPRs[i].ID < waitingPRs[j].ID
		})

		var labels []string
		for i, pr := range waitingPRs {
			if i >= maxLabels {
				break
			}
			labels = append(labels, fmt.Sprintf("#%d", pr.Number))
		}

		labelStr := strings.Join(labels, ", ")
		if len(waitingPRs) > maxLabels {
			labelStr += ", ..."
		}
		parts = append(parts, fmt.Sprintf("%d PR: %s", len(waitingPRs), labelStr))
	}

	return strings.Join(parts, " | ")
}

// FormatWithPRsAndRepos formats status with repo and author-aware PR display
func FormatWithPRsAndRepos(sessions []protocol.Session, prs []protocol.PR, repos []protocol.RepoState, authors []protocol.AuthorState) string {
	// Use attention aggregator with repo and author muting
	agg := attention.NewAggregator(repos, authors)
	result := agg.Aggregate(sessions, prs)

	if result.TotalCount == 0 {
		return "✓ all clear"
	}

	var parts []string

	// Sessions part (bold red in tmux)
	if result.SessionCount > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=red,bold]%d sessions#[default]", result.SessionCount))
	}

	// PRs part - group by repo for display
	if result.PRCount > 0 {
		// Build muted repos and authors sets for filtering
		mutedRepos := make(map[string]bool)
		for _, r := range repos {
			if r.Muted {
				mutedRepos[r.Repo] = true
			}
		}
		mutedAuthors := make(map[string]bool)
		for _, a := range authors {
			if a.Muted {
				mutedAuthors[a.Author] = true
			}
		}

		// Count PRs by repo
		repoCount := make(map[string]int)
		for _, pr := range prs {
			if pr.Muted || mutedRepos[pr.Repo] || mutedAuthors[pr.Author] {
				continue
			}
			if pr.State == protocol.PRStateWaiting {
				repoCount[pr.Repo]++
			}
		}

		var prPart string
		if len(repoCount) <= 2 {
			// Show repo names
			var repoParts []string
			var repoNames []string
			for r := range repoCount {
				repoNames = append(repoNames, r)
			}
			sort.Strings(repoNames)
			for _, r := range repoNames {
				short := r
				if idx := strings.LastIndex(r, "/"); idx >= 0 {
					short = r[idx+1:]
				}
				repoParts = append(repoParts, fmt.Sprintf("%s(%d)", short, repoCount[r]))
			}
			prPart = strings.Join(repoParts, " ")
		} else {
			prPart = fmt.Sprintf("%d PRs in %d repos", result.PRCount, len(repoCount))
		}
		parts = append(parts, prPart)
	}

	return "● " + strings.Join(parts, " | ")
}
