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
