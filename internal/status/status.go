package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/claude-manager/internal/protocol"
)

const maxLabels = 3

// Format formats sessions for tmux status bar (backwards compatible)
func Format(sessions []*protocol.Session) string {
	return FormatWithPRs(sessions, nil)
}

// FormatWithPRs formats sessions and PRs for status bar
func FormatWithPRs(sessions []*protocol.Session, prs []*protocol.PR) string {
	// Filter to waiting sessions (non-muted)
	var waitingSessions []*protocol.Session
	for _, s := range sessions {
		if s.State == protocol.StateWaiting && !s.Muted {
			waitingSessions = append(waitingSessions, s)
		}
	}

	// Filter to waiting PRs (non-muted)
	var waitingPRs []*protocol.PR
	for _, pr := range prs {
		if pr.State == protocol.StateWaiting && !pr.Muted {
			waitingPRs = append(waitingPRs, pr)
		}
	}

	// If nothing waiting, return "✓ all clear"
	if len(waitingSessions) == 0 && len(waitingPRs) == 0 {
		return "✓ all clear"
	}

	var parts []string

	// Sessions part
	if len(waitingSessions) > 0 {
		// Sort by StateSince (oldest first)
		sort.Slice(waitingSessions, func(i, j int) bool {
			return waitingSessions[i].StateSince.Before(waitingSessions[j].StateSince)
		})

		// Format labels (max 3)
		var labels []string
		for i, s := range waitingSessions {
			if i >= maxLabels {
				break
			}
			labels = append(labels, s.Label)
		}

		labelStr := strings.Join(labels, ", ")
		if len(waitingSessions) > maxLabels {
			labelStr += ", ..."
		}
		parts = append(parts, fmt.Sprintf("%d waiting: %s", len(waitingSessions), labelStr))
	}

	// PRs part
	if len(waitingPRs) > 0 {
		// Sort by ID
		sort.Slice(waitingPRs, func(i, j int) bool {
			return waitingPRs[i].ID < waitingPRs[j].ID
		})

		// Format PR numbers (max 3, show just #number for brevity)
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

// FormatWithPRsAndRepos formats status with repo-aware PR display
func FormatWithPRsAndRepos(sessions []*protocol.Session, prs []*protocol.PR, repos []*protocol.RepoState) string {
	// Build muted repos set
	mutedRepos := make(map[string]bool)
	for _, r := range repos {
		if r.Muted {
			mutedRepos[r.Repo] = true
		}
	}

	// Count sessions
	sessionWaiting := 0
	for _, s := range sessions {
		if s.State == protocol.StateWaiting && !s.Muted {
			sessionWaiting++
		}
	}

	// Group PRs by repo, excluding muted
	repoCount := make(map[string]int)
	prWaiting := 0
	for _, pr := range prs {
		if pr.Muted || mutedRepos[pr.Repo] {
			continue
		}
		if pr.State == protocol.StateWaiting {
			prWaiting++
			repoCount[pr.Repo]++
		}
	}

	if sessionWaiting == 0 && prWaiting == 0 {
		return "✓ all clear"
	}

	var parts []string

	// Sessions part (bold red in tmux)
	if sessionWaiting > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=red,bold]%d sessions#[default]", sessionWaiting))
	}

	// PRs part
	if prWaiting > 0 {
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
			prPart = fmt.Sprintf("%d PRs in %d repos", prWaiting, len(repoCount))
		}
		parts = append(parts, prPart)
	}

	return "● " + strings.Join(parts, " | ")
}
