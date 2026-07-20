package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// WS wrappers for the automations surface: list definitions, list one
// definition's runs, enable/disable, delete, cleanup, and run-now. Canonical state
// stays in SQLite; every handler here replies with a compact
// AutomationActionResultMessage and mutations also broadcast
// automations_changed (automations_broadcast.go) so other clients re-read.
//
// This is a distinct wire shape from the unix-socket automation_action_result
// used by the CLI/agent path (automations.go's automationActionResult /
// internal/client's AutomationResult, which carry a generic `data` payload) —
// see the AutomationActionResultMessage doc comment in main.tsp for why the
// two are not merged.
//
// Mutations here (set_enabled, delete, run) can block behind d.automationMu
// while it is held for an in-flight automation delivery (clone/fetch, agent
// spawn), which can take tens of seconds — the frontend's wrappers use a 30s
// timeout to match. The mutations resolve that race in one of two ways:
//
//   - set_enabled and delete both abort, rather than mutating, once
//     wsAutomationMutationTimeoutDuration's daemon-side deadline (25s, strictly
//     inside the client's 30s) passes while still waiting on automationMu — see
//     automationSetEnabled and automationDelete. So the client's timer can
//     never fire before one of these either lands or is provably abandoned; a
//     late store flip after a reported timeout is not possible.
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

// automationRunSummaryListCap bounds automation_runs_get: a defensive cap
// against an unbounded WS payload for a long-lived definition, not a
// UI-driven pagination contract.
const automationRunSummaryListCap = 100

func (d *Daemon) handleAutomationDefinitionsGetWS(client *wsClient, msg *protocol.AutomationDefinitionsGetMessage) {
	definitions, err := d.store.ListAutomationDefinitions()
	result := protocol.AutomationActionResultMessage{
		Event:     protocol.EventAutomationActionResult,
		Action:    "definitions_get",
		RequestID: msg.RequestID,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Definitions = make([]protocol.AutomationDefinitionSummary, len(definitions))
		for i := range definitions {
			result.Definitions[i] = d.buildAutomationDefinitionSummary(definitions[i])
		}
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationRunsGetWS(client *wsClient, msg *protocol.AutomationRunsGetMessage) {
	runs, err := d.store.ListAutomationRunsWithOccurrenceKeys(msg.DefinitionID, automationRunSummaryListCap+1)
	result := protocol.AutomationActionResultMessage{
		Event:     protocol.EventAutomationActionResult,
		Action:    "runs_get",
		RequestID: msg.RequestID,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		if len(runs) > automationRunSummaryListCap {
			runs = runs[:automationRunSummaryListCap]
			result.Truncated = protocol.Ptr(true)
		}
		result.Runs = make([]protocol.AutomationRunSummary, len(runs))
		for i := range runs {
			result.Runs[i] = automationRunSummary(runs[i])
		}
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationSetEnabledWS(client *wsClient, msg *protocol.AutomationSetEnabledMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		definition, err := d.automationSetEnabled(ctx, msg.DefinitionID, msg.Enabled)
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "set_enabled",
			RequestID: msg.RequestID,
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Definitions = []protocol.AutomationDefinitionSummary{d.buildAutomationDefinitionSummary(*definition)}
		}
		d.sendToClient(client, result)
	}()
}

// handleAutomationDeleteWS is the WS counterpart of the unix-socket
// CmdAutomationDelete path (automations.go's handleAutomationCommand):
// soft-delete a definition. Unlike set_enabled's result, there is no
// updated definition summary to return — a deleted definition drops out of
// automation_definitions_get/automations_changed listings entirely, so
// clients learn of the removal from the broadcast automationDelete already
// sends, not from this result's payload.
func (d *Daemon) handleAutomationDeleteWS(client *wsClient, msg *protocol.AutomationDeleteMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		err := d.automationDelete(ctx, msg.DefinitionID)
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "delete",
			RequestID: msg.RequestID,
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		}
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
		cleaned, keptDirty, keptActive, err := d.automationCleanup(ctx, msg.DefinitionID)
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "cleanup",
			RequestID: msg.RequestID,
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Cleaned = cleaned
			result.KeptDirty = keptDirty
			result.KeptActive = keptActive
		}
		d.sendToClient(client, result)
	}()
}

// handleAutomationRunWS is the WS counterpart of the unix-socket
// CmdAutomationRun path (automations.go's handleAutomationCommand): run-now,
// manual trigger only. A manual-trigger rejection (e.g. a provider-driven
// definition) surfaces as success=false with the error text, matching the
// socket path's existing behavior — it is not a transport-level failure.
func (d *Daemon) handleAutomationRunWS(client *wsClient, msg *protocol.AutomationRunMessage) {
	go func() {
		var run *store.AutomationRun
		var err error
		prURL := strings.TrimSpace(protocol.Deref(msg.PRURL))
		inputJSON := strings.TrimSpace(protocol.Deref(msg.InputJson))
		switch {
		case prURL != "" && inputJSON != "":
			err = errors.New("pr_url and input_json are mutually exclusive")
		case prURL != "":
			run, err = d.automationRunPullRequest(context.Background(), msg.DefinitionID, msg.RequestID, prURL)
		default:
			run, err = d.automationRun(context.Background(), msg.DefinitionID, msg.RequestID, protocol.Deref(msg.InputJson))
		}
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "run",
			RequestID: protocol.Ptr(msg.RequestID),
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.RunID = protocol.Ptr(run.ID)
			result.TicketID = protocol.Ptr(run.TicketID)
			result.SessionID = protocol.Ptr(run.SessionID)
		}
		d.sendToClient(client, result)
	}()
}

// handleAutomationApplyWS is the WS counterpart of the unix-socket
// CmdAutomationApply path, used by the app editor's Save. Unlike the socket
// path's automationApply (last-writer-wins), this goes through
// automationApplyWithGuards so expected_id/expected_revision (D4/D5 in the
// design) can refuse an id change or a stale save — see that function's doc
// comment for why both checks are safe against a concurrent apply. On
// success the result carries the saved definition's summary (including its
// new revision) so the editor can update its expected_revision for the next
// save without a round trip through automation_definitions_get.
func (d *Daemon) handleAutomationApplyWS(client *wsClient, msg *protocol.AutomationApplyMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		definition, err := d.automationApplyWithGuards(ctx, msg.DefinitionYaml, protocol.Deref(msg.ExpectedID), int(protocol.Deref(msg.ExpectedRevision)))
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "apply",
			RequestID: msg.RequestID,
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Definitions = []protocol.AutomationDefinitionSummary{d.buildAutomationDefinitionSummary(*definition)}
			result.SpecYaml = protocol.Ptr(msg.DefinitionYaml)
			result.Revision = protocol.Ptr(definition.Revision)
		}
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
		_, _, err := d.validateAutomationSpec(msg.DefinitionYaml)
		result := protocol.AutomationActionResultMessage{
			Event:     protocol.EventAutomationActionResult,
			Action:    "validate",
			RequestID: msg.RequestID,
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		}
		d.sendToClient(client, result)
	}()
}

// handleAutomationDefinitionGetWS backs the editor's load path: definition_id
// "" returns the starter template at revision 0 (new-definition case, D7 in
// the design), so create and edit share one frontend code path. A non-empty
// id resolves definition_yaml via automationDefinitionYAML's legacy-row
// fallback and returns the stored revision, for the editor to echo back as
// expected_revision on save.
func (d *Daemon) handleAutomationDefinitionGetWS(client *wsClient, msg *protocol.AutomationDefinitionGetMessage) {
	result := protocol.AutomationActionResultMessage{
		Event:     protocol.EventAutomationActionResult,
		Action:    "definition_get",
		RequestID: msg.RequestID,
	}
	if msg.DefinitionID == "" {
		template, err := automation.StarterTemplateYAML()
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.sendToClient(client, result)
			return
		}
		result.Success = true
		result.SpecYaml = protocol.Ptr(string(template))
		result.Revision = protocol.Ptr(0)
		d.sendToClient(client, result)
		return
	}
	definition, err := d.store.GetAutomationDefinition(msg.DefinitionID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	if definition == nil {
		result.Error = protocol.Ptr("automation definition not found")
		d.sendToClient(client, result)
		return
	}
	specYAML, err := automationDefinitionYAML(*definition)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.SpecYaml = protocol.Ptr(specYAML)
	result.Revision = protocol.Ptr(definition.Revision)
	d.sendToClient(client, result)
}

// automationDefinitionYAML resolves def's definition_yaml: the stored
// spec_yaml when present, else automation.MarshalDefinitionYAML rendered
// from spec_json for a row written before migration 75 added spec_yaml (see
// MarshalDefinitionYAML's doc comment).
func automationDefinitionYAML(def store.AutomationDefinition) (string, error) {
	if def.SpecYAML != "" {
		return def.SpecYAML, nil
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return "", fmt.Errorf("parse stored definition %s: %w", def.ID, err)
	}
	rendered, err := automation.MarshalDefinitionYAML(spec)
	if err != nil {
		return "", fmt.Errorf("render definition %s: %w", def.ID, err)
	}
	return string(rendered), nil
}

// buildAutomationDefinitionSummary extracts the compact WS fields from a
// definition's SpecJSON. An unmarshal failure (should not happen for a
// definition that passed automationApply's validation, but is not assumed)
// degrades to an id/name/enabled-only summary rather than dropping the
// definition from the list.
func (d *Daemon) buildAutomationDefinitionSummary(def store.AutomationDefinition) protocol.AutomationDefinitionSummary {
	summary := protocol.AutomationDefinitionSummary{
		ID:        def.ID,
		Name:      def.Name,
		Enabled:   def.Enabled,
		Revision:  def.Revision,
		UpdatedAt: string(protocol.NewTimestamp(def.UpdatedAt)),
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		d.logf("automation definition summary parse %s: %v", def.ID, err)
		return summary
	}
	summary.TriggerType = spec.Trigger.Type
	if spec.Trigger.Schedule != nil {
		summary.ScheduleCron = protocol.Ptr(spec.Trigger.Schedule.Cron)
		summary.ScheduleTimeZone = protocol.Ptr(spec.Trigger.Schedule.TimeZone)
	}
	summary.Continuity = protocol.Ptr(spec.Policy.Continuity)
	summary.CatchUp = protocol.Ptr(spec.Policy.CatchUp)
	return summary
}

func automationRunSummary(run store.AutomationRunWithOccurrenceKey) protocol.AutomationRunSummary {
	summary := protocol.AutomationRunSummary{
		ID:                 run.ID,
		DefinitionID:       run.DefinitionID,
		DefinitionRevision: run.DefinitionRevision,
		State:              run.State,
		TicketID:           protocol.Ptr(run.TicketID),
		SessionID:          protocol.Ptr(run.SessionID),
		WorkspaceID:        protocol.Ptr(run.WorkspaceID),
		PaneID:             protocol.Ptr(run.PaneID),
		CreatedAt:          string(protocol.NewTimestamp(run.CreatedAt)),
		UpdatedAt:          string(protocol.NewTimestamp(run.UpdatedAt)),
		OccurrenceKey:      protocol.Ptr(run.OccurrenceKey),
	}
	if run.LastError != "" {
		summary.LastError = protocol.Ptr(run.LastError)
	}
	if run.DeliveredAt != nil {
		summary.DeliveredAt = protocol.Ptr(string(protocol.NewTimestamp(*run.DeliveredAt)))
	}
	return summary
}
