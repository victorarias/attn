package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

// WS wrappers for the automations surface: list definitions, list one
// definition's runs, enable/disable, delete, cleanup, run-now, apply, and
// validate. Every handler here delegates to the same action function in
// automations_actions.go that the unix-socket transport uses
// (handleAutomationCommand in automations.go), then d.sendToClient's the
// per-action typed result — the two transports can never drift in what a
// given action returns.
//
// Mutations here (set_enabled, delete, run, apply) can block behind
// d.automationMu while it is held for an in-flight automation delivery
// (clone/fetch, agent spawn), which can take tens of seconds — the
// frontend's wrappers use a 30s timeout to match. The mutations resolve that
// race in one of two ways:
//
//   - set_enabled, delete, and apply all abort, rather than mutating, once
//     wsAutomationMutationTimeoutDuration's daemon-side deadline (25s, strictly
//     inside the client's 30s) passes while still waiting on automationMu — see
//     automationSetEnabled, automationDelete, and automationApplyLocked. So the
//     client's timer can never fire before one of these either lands or is
//     provably abandoned; a late store flip after a reported timeout is not
//     possible.
//   - run-now cannot use the same trick: automationRun durably claims the run
//     via ClaimManualAutomationRun before waiting on automationMu, so an
//     abandoned wait still leaves a pending run that will eventually deliver.
//     Instead the client is expected to reuse the same request_id across a
//     retry of the same click (ClaimManualAutomationRun is idempotent per
//     request_id), so a retry after a client-side timeout dedups onto the
//     original claim rather than creating a second one.
//   - cleanup does not participate in this race at all: it never takes
//     automationMu (see handleAutomationCleanupWS), so it has nothing to
//     abort ahead of — the WS timeout there is only a defensive bound.

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

// handleAutomationDeleteWS is the WS counterpart of the unix-socket
// CmdAutomationDelete path: soft-delete a definition. Unlike set_enabled's
// result, there is no updated definition summary to return — a deleted
// definition drops out of automation_definitions_get/automations_changed
// listings entirely, so clients learn of the removal from the broadcast
// automationDelete already sends, not from this result's payload.
func (d *Daemon) handleAutomationDeleteWS(client *wsClient, msg *protocol.AutomationDeleteMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationDelete(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationCleanupWS is the WS counterpart of the unix-socket
// CmdAutomationCleanup path: reclaim worktree disk space for every terminal
// run of a definition right now. Unlike set_enabled/delete, cleanup never
// blocks on automationMu (automationCleanup doesn't take it — it's disk-only
// and doesn't race an in-flight delivery's row mutations), so there is no
// abort-before-mutate deadline race to guard against; the timeout here is
// just a defensive bound on how long a large worktree scan may run.
func (d *Daemon) handleAutomationCleanupWS(client *wsClient, msg *protocol.AutomationCleanupMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationCleanup(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationRunWS is the WS counterpart of the unix-socket
// CmdAutomationRun path: run-now. A manual-trigger rejection (e.g. a
// provider-driven definition) surfaces as success=false with the error text,
// matching the socket path's behavior — it is not a transport-level failure.
func (d *Daemon) handleAutomationRunWS(client *wsClient, msg *protocol.AutomationRunMessage) {
	go func() {
		result := d.actionAutomationRun(context.Background(), msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationApplyWS is the WS counterpart of the unix-socket
// CmdAutomationApply path, used by the app editor's Save. The app always
// sends expected_id/expected_revision (see actionAutomationApply's doc
// comment on why that's what makes them enforced here but not on the
// socket/CLI path).
func (d *Daemon) handleAutomationApplyWS(client *wsClient, msg *protocol.AutomationApplyMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationApply(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationValidateWS is the WS counterpart of the unix-socket
// CmdAutomationValidate path: validate-without-apply, so the editor can show
// an error before Save.
//
// It takes no automationMu: validateAutomationSpec never mutates the store,
// so it cannot contend with an in-flight apply/delete/set_enabled the way the
// mutation handlers above can. It does still run on its own goroutine,
// because "does not touch the store" is not the same as "is fast": each
// location override is checked with git.ValidateLocalClone, which stats the
// path and shells out to git twice. The dispatcher calls handlers inline on
// the client's read loop, so validating synchronously would stall every other
// message from that client behind those subprocesses — and a path on an
// unresponsive mount would stall them indefinitely.
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
