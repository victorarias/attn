package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/claude-manager/internal/protocol"
)

const maxLabels = 2

// Format formats sessions for tmux status bar
func Format(sessions []*protocol.Session) string {
	// Filter to waiting only
	var waiting []*protocol.Session
	for _, s := range sessions {
		if s.State == protocol.StateWaiting {
			waiting = append(waiting, s)
		}
	}

	if len(waiting) == 0 {
		return ""
	}

	// Sort by StateSince (oldest first)
	sort.Slice(waiting, func(i, j int) bool {
		return waiting[i].StateSince.Before(waiting[j].StateSince)
	})

	// Format labels
	var labels []string
	for i, s := range waiting {
		if i >= maxLabels {
			break
		}
		labels = append(labels, s.Label)
	}

	labelStr := strings.Join(labels, ", ")
	if len(waiting) > maxLabels {
		labelStr += "..."
	}

	return fmt.Sprintf("%d waiting: %s", len(waiting), labelStr)
}
