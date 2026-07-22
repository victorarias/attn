package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// automationDefinitionYAML resolves def's definition_yaml by rendering
// automation.MarshalDefinitionYAML from spec_json — spec_yaml storage is
// gone (see MarshalDefinitionYAML's doc comment), so this is the only path;
// every read (the editor's Save-then-reopen, `attn automation show`) gets
// its YAML re-derived from the canonical spec, comments and formatting
// choices not preserved.
func automationDefinitionYAML(def store.AutomationDefinition) (string, error) {
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

// This file is the one action layer shared by both transports (the
// unix-socket/CLI dispatch in automations.go and the WS handlers in
// ws_automations.go): one function per command, each taking its typed
// request message and returning the complete typed result message with
// Success/Error set. All result-shape construction — building summaries,
// deciding what to omit — lives here, not duplicated per transport.

// automationRunSummaryListCap bounds automation_runs_get: a defensive cap
// against an unbounded WS payload for a long-lived definition, not a
// UI-driven pagination contract.
const automationRunSummaryListCap = 100

func (d *Daemon) actionAutomationDefinitionsGet(msg *protocol.AutomationDefinitionsGetMessage) protocol.AutomationDefinitionsResultMessage {
	result := protocol.AutomationDefinitionsResultMessage{
		Event:     protocol.EventAutomationDefinitionsResult,
		RequestID: msg.RequestID,
	}
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	lastRuns, err := d.store.LatestAutomationRunPerDefinition()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.Definitions = make([]protocol.AutomationDefinitionSummary, len(definitions))
	for i := range definitions {
		var lastRun *store.AutomationRunWithOccurrenceKey
		if run, ok := lastRuns[definitions[i].ID]; ok {
			lastRun = &run
		}
		result.Definitions[i] = d.buildAutomationDefinitionSummary(definitions[i], lastRun)
	}
	return result
}

// actionAutomationDefinitionGet backs the editor's load path: definition_id
// "" returns the starter template at revision 0 (no definition — the
// new-definition case, D7 in the design), so create and edit share one
// frontend code path. A non-empty id resolves definition_yaml via
// automationDefinitionYAML's spec-JSON rendering.
func (d *Daemon) actionAutomationDefinitionGet(msg *protocol.AutomationDefinitionGetMessage) protocol.AutomationDefinitionResultMessage {
	result := protocol.AutomationDefinitionResultMessage{
		Event:     protocol.EventAutomationDefinitionResult,
		RequestID: msg.RequestID,
	}
	if msg.DefinitionID == "" {
		template, err := automation.StarterTemplateYAML()
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			return result
		}
		result.Success = true
		result.SpecYaml = protocol.Ptr(string(template))
		return result
	}
	definition, err := d.store.GetAutomationDefinition(msg.DefinitionID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	if definition == nil {
		result.Error = protocol.Ptr("automation definition not found")
		return result
	}
	specYAML, err := automationDefinitionYAML(*definition)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.SpecYaml = protocol.Ptr(specYAML)
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	return result
}

func (d *Daemon) actionAutomationRunsGet(msg *protocol.AutomationRunsGetMessage) protocol.AutomationRunsResultMessage {
	result := protocol.AutomationRunsResultMessage{
		Event:        protocol.EventAutomationRunsResult,
		RequestID:    msg.RequestID,
		DefinitionID: msg.DefinitionID,
	}
	runs, err := d.store.ListAutomationRunsWithOccurrenceKeys(msg.DefinitionID, automationRunSummaryListCap+1)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	if len(runs) > automationRunSummaryListCap {
		runs = runs[:automationRunSummaryListCap]
		result.Truncated = protocol.Ptr(true)
	}
	result.Success = true
	result.Runs = make([]protocol.AutomationRunSummary, len(runs))
	for i := range runs {
		result.Runs[i] = automationRunSummary(runs[i])
	}
	return result
}

func (d *Daemon) actionAutomationValidate(msg *protocol.AutomationValidateMessage) protocol.AutomationValidateResultMessage {
	result := protocol.AutomationValidateResultMessage{
		Event:     protocol.EventAutomationValidateResult,
		RequestID: msg.RequestID,
	}
	if _, _, err := d.validateAutomationSpec(msg.DefinitionYaml); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	return result
}

// actionAutomationApply is the one apply path for both transports, unified on
// automationApplyWithGuards. The socket/CLI path never sets
// expected_id/expected_revision (both nil after JSON decode, since cmd/attn
// omits them), so automationApplyWithGuards enforces neither check and
// behaves exactly like the old unguarded last-writer-wins automationApply.
// The WS editor's Save always sends both (possibly zero-valued for a create),
// so its guards apply as before — see automationApplyWithGuards's doc
// comment for why enforcement is keyed on pointer presence rather than on the
// zero value itself.
func (d *Daemon) actionAutomationApply(ctx context.Context, msg *protocol.AutomationApplyMessage) protocol.AutomationApplyResultMessage {
	result := protocol.AutomationApplyResultMessage{
		Event:     protocol.EventAutomationApplyResult,
		RequestID: msg.RequestID,
	}
	definition, err := d.automationApplyWithGuards(ctx, msg.DefinitionYaml, msg.ExpectedID, msg.ExpectedRevision)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	specYAML, err := automationDefinitionYAML(*definition)
	if err == nil {
		result.SpecYaml = protocol.Ptr(specYAML)
	}
	return result
}

func (d *Daemon) actionAutomationSetEnabled(ctx context.Context, msg *protocol.AutomationSetEnabledMessage) protocol.AutomationSetEnabledResultMessage {
	result := protocol.AutomationSetEnabledResultMessage{
		Event:     protocol.EventAutomationSetEnabledResult,
		RequestID: msg.RequestID,
	}
	definition, err := d.automationSetEnabled(ctx, msg.DefinitionID, msg.Enabled)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	return result
}

func (d *Daemon) actionAutomationDelete(ctx context.Context, msg *protocol.AutomationDeleteMessage) protocol.AutomationDeleteResultMessage {
	result := protocol.AutomationDeleteResultMessage{
		Event:     protocol.EventAutomationDeleteResult,
		RequestID: msg.RequestID,
	}
	if err := d.automationDelete(ctx, msg.DefinitionID); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	return result
}

func (d *Daemon) actionAutomationCleanup(ctx context.Context, msg *protocol.AutomationCleanupMessage) protocol.AutomationCleanupResultMessage {
	result := protocol.AutomationCleanupResultMessage{
		Event:     protocol.EventAutomationCleanupResult,
		RequestID: msg.RequestID,
	}
	cleaned, keptDirty, keptActive, err := d.automationCleanup(ctx, msg.DefinitionID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.Cleaned = cleaned
	result.KeptDirty = keptDirty
	result.KeptActive = keptActive
	return result
}

// actionAutomationRun handles run-now for both transports: pr_url and
// input_json are mutually exclusive, and a manual-trigger rejection (e.g. a
// provider-driven definition) surfaces as success=false with the error text,
// not a transport-level failure.
func (d *Daemon) actionAutomationRun(ctx context.Context, msg *protocol.AutomationRunMessage) protocol.AutomationRunResultMessage {
	result := protocol.AutomationRunResultMessage{
		Event:     protocol.EventAutomationRunResult,
		RequestID: protocol.Ptr(msg.RequestID),
	}
	prURL := strings.TrimSpace(protocol.Deref(msg.PRURL))
	inputJSON := strings.TrimSpace(protocol.Deref(msg.InputJson))
	if prURL != "" && inputJSON != "" {
		result.Error = protocol.Ptr("pr_url and input_json are mutually exclusive")
		return result
	}
	var run *store.AutomationRun
	var err error
	if prURL != "" {
		run, err = d.automationRunPullRequest(ctx, msg.DefinitionID, msg.RequestID, prURL)
	} else {
		run, err = d.automationRun(ctx, msg.DefinitionID, msg.RequestID, protocol.Deref(msg.InputJson))
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	summary := automationRunSummary(store.AutomationRunWithOccurrenceKey{AutomationRun: *run})
	result.Run = &summary
	return result
}

// buildAutomationDefinitionSummary extracts the compact wire fields from a
// definition's SpecJSON. An unmarshal failure (should not happen for a
// definition that passed automationApply's validation, but is not assumed)
// degrades to an id/name/enabled-only summary rather than dropping the
// definition from the list. lastRun is embedded when the caller has it handy
// (definitions_get provides one query's worth for every definition); mutation
// results (apply/set_enabled) pass nil rather than pay for a per-definition
// lookup — the frontend's automations_changed-driven refetch fills it in.
func (d *Daemon) buildAutomationDefinitionSummary(def store.AutomationDefinition, lastRun *store.AutomationRunWithOccurrenceKey) protocol.AutomationDefinitionSummary {
	summary := protocol.AutomationDefinitionSummary{
		ID:        def.ID,
		Name:      def.Name,
		Enabled:   def.Enabled,
		Revision:  def.Revision,
		UpdatedAt: string(protocol.NewTimestamp(def.UpdatedAt)),
	}
	if lastRun != nil {
		runSummary := automationRunSummary(*lastRun)
		summary.LastRun = &runSummary
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
	return summary
}

func automationRunSummary(run store.AutomationRunWithOccurrenceKey) protocol.AutomationRunSummary {
	summary := protocol.AutomationRunSummary{
		ID:            run.ID,
		DefinitionID:  run.DefinitionID,
		State:         run.State,
		TicketID:      protocol.Ptr(run.TicketID),
		SessionID:     protocol.Ptr(run.SessionID),
		PaneID:        protocol.Ptr(run.PaneID),
		CreatedAt:     string(protocol.NewTimestamp(run.CreatedAt)),
		UpdatedAt:     string(protocol.NewTimestamp(run.UpdatedAt)),
		OccurrenceKey: protocol.Ptr(run.OccurrenceKey),
	}
	if run.LastError != "" {
		summary.LastError = protocol.Ptr(run.LastError)
	}
	if run.CancelReason != "" {
		summary.CancelReason = protocol.Ptr(run.CancelReason)
	}
	if run.DeliveredAt != nil {
		summary.DeliveredAt = protocol.Ptr(string(protocol.NewTimestamp(*run.DeliveredAt)))
	}
	return summary
}
