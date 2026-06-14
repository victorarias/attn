package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
)

// WS wrappers for the durable workflow engine, mirroring ws_review.go. The
// read-only UI mainly reads (get/list) and cancels; upsert/call_upsert variants
// exist for symmetry and testability. Each delegates to the shared core in
// workflow_run.go and replies with a WorkflowActionResultMessage via
// sendToClient.

func (d *Daemon) sendWorkflowActionResultWS(client *wsClient, action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) {
	d.sendToClient(client, buildWorkflowActionResult(action, run, runs, runID, err))
}

func (d *Daemon) handleWorkflowRunUpsertWS(client *wsClient, msg *protocol.WorkflowRunUpsertMessage) {
	d.registerWorkflowEngine(msg.Run.RunID, wsWorkflowEngineSink{daemon: d, client: client})
	run, err := d.applyWorkflowRunUpsert(&msg.Run)
	d.sendWorkflowActionResultWS(client, "upsert", run, nil, msg.Run.RunID, err)
}

func (d *Daemon) handleWorkflowCallUpsertWS(client *wsClient, msg *protocol.WorkflowCallUpsertMessage) {
	d.registerWorkflowEngine(msg.RunID, wsWorkflowEngineSink{daemon: d, client: client})
	run, err := d.applyWorkflowCallUpsert(msg.RunID, &msg.Call)
	d.sendWorkflowActionResultWS(client, "call_upsert", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunGetWS(client *wsClient, msg *protocol.WorkflowRunGetMessage) {
	run, err := d.getWorkflowRunHydrated(msg.RunID)
	d.sendWorkflowActionResultWS(client, "get", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunListWS(client *wsClient, msg *protocol.WorkflowRunListMessage) {
	runs, err := d.listWorkflowRunsHydrated(protocol.Deref(msg.SessionID))
	d.sendWorkflowActionResultWS(client, "list", nil, runs, "", err)
}

func (d *Daemon) handleWorkflowRunCancelWS(client *wsClient, msg *protocol.WorkflowRunCancelMessage) {
	run, _, err := d.cancelWorkflowRun(msg.RunID)
	d.sendWorkflowActionResultWS(client, "cancel", run, nil, msg.RunID, err)
}
