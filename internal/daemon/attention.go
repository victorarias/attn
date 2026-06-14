package daemon

import (
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
)

// aggregateAttention is the daemon's single production caller of
// attention.Aggregate. It reads the live read-models the aggregator needs —
// sessions, PRs, repo/author mute state, and workflow runs — straight from the
// store, converts them to the value slices the aggregator consumes, and derives
// the unified needs-attention view.
//
// The daemon (not internal/attention) owns every store read here, so the
// attention package stays a pure derivation over protocol values and never
// imports internal/store. Workflow runs flow in via the hydrated list so a
// finished run (completed/failed) surfaces in the same aggregate as a waiting
// session or PR — the parity-with-native completion signal the workflow engine
// design calls for. A still-running or canceled run contributes nothing (see
// WorkflowRunAdapter.NeedsAttention).
func (d *Daemon) aggregateAttention() attention.Result {
	if d == nil || d.store == nil {
		return attention.Result{}
	}

	sessions := protocol.SessionsToValues(d.store.List(""))
	prs := protocol.PRsToValues(d.store.ListPRs(""))
	repos := protocol.RepoStatesToValues(d.store.ListRepoStates())
	authors := protocol.AuthorStatesToValues(d.store.ListAuthorStates())

	var workflowRuns []protocol.WorkflowRun
	if runs, err := d.listWorkflowRunsHydrated(""); err != nil {
		d.logf("attention: list workflow runs failed: %v", err)
	} else {
		workflowRuns = make([]protocol.WorkflowRun, 0, len(runs))
		for _, run := range runs {
			if run != nil {
				workflowRuns = append(workflowRuns, *run)
			}
		}
	}

	agg := attention.NewAggregator(repos, authors)
	return agg.Aggregate(sessions, prs, workflowRuns)
}
