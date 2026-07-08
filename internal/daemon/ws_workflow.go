package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
)

// WS wrappers for the durable workflow engine. The UI is
// read-only over the WebSocket: it reads (get/list) and cancels. Run/call mutation
// is owned by the engine process over the unix socket (see workflow_run.go, which
// also gates starts on the workflows_enabled master switch), so there is no WS
// upsert path. Each handler delegates to the shared core in workflow_run.go and
// replies with a WorkflowActionResultMessage via sendToClient.

func (d *Daemon) sendWorkflowActionResultWS(client *wsClient, action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) {
	d.sendToClient(client, buildWorkflowActionResult(action, run, runs, runID, err))
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
