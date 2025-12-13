package attention

import (
	"sort"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Aggregator combines multiple attention sources into a unified view.
type Aggregator struct {
	mutedRepos map[string]bool
}

// NewAggregator creates an Aggregator with the given muted repos.
func NewAggregator(repos []protocol.RepoState) *Aggregator {
	mutedRepos := make(map[string]bool)
	for _, r := range repos {
		if r.Muted {
			mutedRepos[r.Repo] = true
		}
	}
	return &Aggregator{mutedRepos: mutedRepos}
}

// Result holds the aggregated attention data.
type Result struct {
	// All items needing attention (not muted), sorted by Since (oldest first)
	Items []Item

	// Counts by kind
	SessionCount int
	PRCount      int

	// Total items needing attention
	TotalCount int
}

// Aggregate combines sessions and PRs into a unified attention result.
func (a *Aggregator) Aggregate(sessions []protocol.Session, prs []protocol.PR) Result {
	var items []Item

	// Process sessions
	for i := range sessions {
		adapter := SessionAdapter{Session: &sessions[i]}
		if adapter.NeedsAttention() {
			items = append(items, FromSource(adapter))
		}
	}

	// Process PRs
	for i := range prs {
		adapter := PRAdapter{
			PR:        &prs[i],
			RepoMuted: a.mutedRepos[prs[i].Repo],
		}
		if adapter.NeedsAttention() {
			items = append(items, FromSource(adapter))
		}
	}

	// Sort by Since (oldest first - they've been waiting longest)
	sort.Slice(items, func(i, j int) bool {
		return items[i].Since.Before(items[j].Since)
	})

	// Count by kind
	sessionCount := 0
	prCount := 0
	for _, item := range items {
		switch item.Kind {
		case "session":
			sessionCount++
		case "pr":
			prCount++
		}
	}

	return Result{
		Items:        items,
		SessionCount: sessionCount,
		PRCount:      prCount,
		TotalCount:   len(items),
	}
}

// FilterByKind returns only items of the specified kind.
func (r Result) FilterByKind(kind string) []Item {
	var filtered []Item
	for _, item := range r.Items {
		if item.Kind == kind {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// Sessions returns only session items.
func (r Result) Sessions() []Item {
	return r.FilterByKind("session")
}

// PRs returns only PR items.
func (r Result) PRs() []Item {
	return r.FilterByKind("pr")
}
