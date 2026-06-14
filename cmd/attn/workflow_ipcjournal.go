package main

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workflow"
)

// workflowClient is the narrow daemon surface the IPC journal (and the run
// orchestration) depend on. The real *client.Client satisfies it; tests inject a
// fake. Keeping it an interface keeps cmd/attn unit-testable without a daemon.
type workflowClient interface {
	WorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error)
	WorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error)
	WorkflowRunGet(runID string) (*protocol.WorkflowRun, error)
	WorkflowRunList(sessionID string) ([]protocol.WorkflowRun, error)
	WorkflowRunCancel(runID string) (*protocol.WorkflowRun, error)
}

// ipcJournal is the engine's Journal backed by the daemon over the unix socket.
// It mirrors DurableJournal's design — an in-memory write-through mirror for the
// hot Lookup/Entries read paths — but the durable store lives in the daemon, so
// every Append/Upsert is mirrored locally AND proxied as a workflow_call_upsert.
// The mirror is seeded at construction from the daemon's view of the run so a
// fresh process can resume a prior run by replaying the journaled prefix.
type ipcJournal struct {
	client  workflowClient
	runID   string
	mirror  *workflow.MemJournal
	lastErr error
}

var _ workflow.Journal = (*ipcJournal)(nil)

// NewIPCJournal builds an ipcJournal for runID, seeding its in-memory mirror from
// the daemon's persisted agent calls (in durable append order). A get error or an
// absent run leaves the mirror empty — a Run from scratch. The seed is what makes
// --resume work: a new journal for a prior runID rebuilds the prior prefix.
func NewIPCJournal(c workflowClient, runID string) *ipcJournal {
	j := &ipcJournal{
		client: c,
		runID:  runID,
		mirror: workflow.NewMemJournal(),
	}
	run, err := c.WorkflowRunGet(runID)
	if err != nil {
		j.lastErr = err
		return j
	}
	if run != nil {
		for i := range run.AgentCalls {
			// Upsert preserves order and is duplicate-safe.
			j.mirror.Upsert(entryFromCall(run.AgentCalls[i]))
		}
	}
	return j
}

// Lookup returns the mirrored entry at ordinal (no network). Matches MemJournal.
func (j *ipcJournal) Lookup(ordinal string) (workflow.JournalEntry, bool) {
	return j.mirror.Lookup(ordinal)
}

// Append records a freshly-executed live call, enforcing the one-entry-per-ordinal
// invariant against the mirror first (identical error to MemJournal.Append), then
// proxying a workflow_call_upsert to the daemon. A proxy failure is captured in
// lastErr (so a flaky socket does not crash the run) but the mirror is still
// updated — the mirror stays authoritative for in-run reads regardless.
func (j *ipcJournal) Append(e workflow.JournalEntry) error {
	if _, exists := j.mirror.Lookup(e.Ordinal); exists {
		return j.mirror.Append(e) // returns the duplicate-ordinal error
	}
	// Mirror invariant already checked above, so Append cannot fail here.
	_ = j.mirror.Append(e)
	j.proxy(e)
	return nil
}

// Upsert records a live call, overwriting any stale entry at the same ordinal (the
// divergence-overwrite path on resume). The Journal interface gives Upsert no
// error return, so a proxy failure is captured in lastErr rather than dropped.
func (j *ipcJournal) Upsert(e workflow.JournalEntry) {
	j.mirror.Upsert(e)
	j.proxy(e)
}

// Entries returns all mirrored entries in append order (no network).
func (j *ipcJournal) Entries() []workflow.JournalEntry {
	return j.mirror.Entries()
}

// Err returns the first proxy/seed error observed, mirroring DurableJournal.Err.
func (j *ipcJournal) Err() error {
	return j.lastErr
}

// proxy sends a single agent call to the daemon, tolerating (recording) errors.
func (j *ipcJournal) proxy(e workflow.JournalEntry) {
	call := callFromEntry(j.runID, e)
	if _, err := j.client.WorkflowCallUpsert(j.runID, &call); err != nil {
		j.lastErr = err
	}
}

// callFromEntry maps a JournalEntry to the protocol WorkflowAgentCall the daemon
// persists. Only the six JournalEntry fields are round-tripped; the richer columns
// (label, phase, model, harness) are owned by out-of-band write paths and left nil.
func callFromEntry(runID string, e workflow.JournalEntry) protocol.WorkflowAgentCall {
	return protocol.WorkflowAgentCall{
		RunID:      runID,
		Ordinal:    e.Ordinal,
		PromptHash: ptrIfNonEmpty(e.PromptHash),
		SchemaHash: ptrIfNonEmpty(e.SchemaHash),
		ResultJson: rawResultToPtr(e.Result),
		Status:     protocol.WorkflowAgentCallStatus(callStatusOrOk(e.Status)),
		Error:      ptrIfNonEmpty(e.Err),
	}
}

// entryFromCall maps a persisted WorkflowAgentCall back to a JournalEntry. The
// round-trip is lossless for the six JournalEntry fields, the only correctness
// requirement: IsCacheHit needs Ordinal+PromptHash+SchemaHash and replay uses
// Result/Status.
func entryFromCall(call protocol.WorkflowAgentCall) workflow.JournalEntry {
	return workflow.JournalEntry{
		Ordinal:    call.Ordinal,
		PromptHash: protocol.Deref(call.PromptHash),
		SchemaHash: protocol.Deref(call.SchemaHash),
		Result:     ptrToRawResult(call.ResultJson),
		Status:     string(call.Status),
		Err:        protocol.Deref(call.Error),
	}
}

// callStatusOrOk normalizes a (possibly empty) engine status into a persisted
// agent-call status. The engine writes "ok" | "skipped" | "errored"; an empty
// status defaults to "ok".
func callStatusOrOk(status string) string {
	switch status {
	case string(protocol.WorkflowAgentCallStatusOk),
		string(protocol.WorkflowAgentCallStatusErrored),
		string(protocol.WorkflowAgentCallStatusSkipped),
		string(protocol.WorkflowAgentCallStatusRunning):
		return status
	default:
		return string(protocol.WorkflowAgentCallStatusOk)
	}
}
