package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

// WS wrappers for the automations surface; each delegates to the action
// function the unix-socket transport uses (automations_actions.go).
//
// Mutations can block behind automationMu for tens of seconds during a
// delivery. set_enabled/delete/apply abort at the daemon-side 25s deadline
// (strictly inside the client's 30s timeout), so no store flip lands after a
// reported timeout; run-now instead claims durably first
// (ClaimManualAutomationRun is idempotent per request_id) so a retry dedups.

func (d *Daemon) handleAutomationDefinitionsGetWS(client *wsClient, msg *protocol.AutomationDefinitionsGetMessage) {
	result := d.actionAutomationDefinitionsGet(msg)
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationRunsGetWS(client *wsClient, msg *protocol.AutomationRunsGetMessage) {
	result := d.actionAutomationRunsGet(msg)
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationSetEnabledWS(client *wsClient, msg *protocol.AutomationSetEnabledMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationSetEnabled(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationDeleteWS soft-deletes a definition; clients learn of the
// removal from automationDelete's broadcast, not this result's payload.
func (d *Daemon) handleAutomationDeleteWS(client *wsClient, msg *protocol.AutomationDeleteMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationDelete(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationCleanupWS reclaims worktree disk space for a definition's
// terminal runs.
func (d *Daemon) handleAutomationCleanupWS(client *wsClient, msg *protocol.AutomationCleanupMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationCleanup(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationRunWS is run-now. A manual-trigger rejection surfaces as
// success=false with the error text, not a transport-level failure.
func (d *Daemon) handleAutomationRunWS(client *wsClient, msg *protocol.AutomationRunMessage) {
	go func() {
		result := d.actionAutomationRun(context.Background(), msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationApplyWS backs the app editor's Save. The app always sends
// expected_id/expected_revision, which is what enforces the guards on this
// path but not on the socket/CLI path (see actionAutomationApply).
func (d *Daemon) handleAutomationApplyWS(client *wsClient, msg *protocol.AutomationApplyMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationApply(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationValidateWS is validate-without-apply for the editor. It runs
// on its own goroutine even though it takes no automationMu: validation shells
// out to git per location override, and the dispatcher calls handlers inline
// on the client's read loop — a slow or hung path would stall every other
// message from that client.
func (d *Daemon) handleAutomationValidateWS(client *wsClient, msg *protocol.AutomationValidateMessage) {
	go func() {
		result := d.actionAutomationValidate(msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationDefinitionGetWS backs the editor's load path: definition_id
// "" returns the starter template at revision 0 (new-definition case), so
// create and edit share one frontend code path.
func (d *Daemon) handleAutomationDefinitionGetWS(client *wsClient, msg *protocol.AutomationDefinitionGetMessage) {
	result := d.actionAutomationDefinitionGet(msg)
	d.sendToClient(client, result)
}
