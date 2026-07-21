package daemon

import "github.com/victorarias/attn/internal/protocol"

// broadcastAutomationsChanged emits an id-only automations_changed event for
// the given definition IDs. Automation definitions/runs change at low
// frequency and the event carries only opaque IDs (canonical state lives in
// SQLite; clients re-read via automation_definitions_get / automation_runs_get
// on receipt), so unlike the workflow run broadcaster (workflow_broadcast.go)
// this emits directly on every mutation/transition rather than through a
// coalescing loop. No-op for an empty id list so callers can pass through a
// possibly-empty definitionID without a guard.
func (d *Daemon) broadcastAutomationsChanged(definitionIDs ...string) {
	if d == nil || len(definitionIDs) == 0 {
		return
	}
	msg := &protocol.AutomationsChangedMessage{
		Event:         protocol.EventAutomationsChanged,
		DefinitionIds: definitionIDs,
	}
	if d.automationsBroadcastHook != nil {
		d.automationsBroadcastHook(msg)
	}
	if d.wsHub != nil {
		d.wsHub.BroadcastValue(msg)
	}
}
