package daemon

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// workflowEngineSink is a destination the daemon can push a workflow control
// frame to. The engine runs in a SEPARATE process (the `attn workflow run` CLI)
// and connects over the unix socket, so the production sink wraps that
// net.Conn. A *wsClient sink (UI-driven engine, future) and a fake sink (tests)
// also satisfy this interface, mirroring the reviewLoopCancel registry where a
// single registry serves every transport.
type workflowEngineSink interface {
	sendWorkflowControl(msg interface{}) error
}

// connWorkflowEngineSink adapts the engine's request net.Conn into a sink by
// json-encoding control frames back onto the same connection. This is the
// SOCKET upsert path: the engine that registered a run is the one we relay a
// cancel to.
type connWorkflowEngineSink struct {
	conn net.Conn
}

func (s connWorkflowEngineSink) sendWorkflowControl(msg interface{}) error {
	return json.NewEncoder(s.conn).Encode(msg)
}

// wsWorkflowEngineSink adapts a *wsClient into a sink (a UI-hosted engine).
type wsWorkflowEngineSink struct {
	daemon *Daemon
	client *wsClient
}

func (s wsWorkflowEngineSink) sendWorkflowControl(msg interface{}) error {
	s.daemon.sendToClient(s.client, msg)
	return nil
}

// registerWorkflowEngine records the sink that owns a run so a later cancel can
// reach the engine process. Lazy-inits the map so a directly-constructed
// &Daemon{store: ...} test daemon does not panic. Mirrors
// registerReviewLoopCancel.
func (d *Daemon) registerWorkflowEngine(runID string, sink workflowEngineSink) {
	if d == nil || strings.TrimSpace(runID) == "" || sink == nil {
		return
	}
	d.workflowEngineMu.Lock()
	defer d.workflowEngineMu.Unlock()
	if d.workflowEngineConn == nil {
		d.workflowEngineConn = make(map[string]workflowEngineSink)
	}
	d.workflowEngineConn[runID] = sink
}

// unregisterWorkflowEngine drops a run's engine sink (engine exited / run done).
func (d *Daemon) unregisterWorkflowEngine(runID string) {
	if d == nil {
		return
	}
	d.workflowEngineMu.Lock()
	defer d.workflowEngineMu.Unlock()
	delete(d.workflowEngineConn, runID)
}

// relayWorkflowCancel looks up the engine sink for a run and pushes a cancel
// control frame. It reuses WorkflowRunCancelMessage as the on-wire control frame
// so the engine decodes the same shape it would receive directly. Returns
// whether a sink existed (relayed). Mirrors cancelReviewLoopExecution.
func (d *Daemon) relayWorkflowCancel(runID string) bool {
	if d == nil {
		return false
	}
	d.workflowEngineMu.Lock()
	sink := d.workflowEngineConn[runID]
	d.workflowEngineMu.Unlock()
	if sink == nil {
		return false
	}
	control := protocol.WorkflowRunCancelMessage{
		Cmd:   protocol.CmdWorkflowRunCancel,
		RunID: runID,
	}
	if err := sink.sendWorkflowControl(control); err != nil {
		d.logf("workflow cancel relay failed for run %s: %v", runID, err)
	}
	return true
}
