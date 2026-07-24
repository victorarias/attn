package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) automationRun(ctx context.Context, definitionID, requestID, input string) (*store.AutomationRun, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	if input == "" {
		input = "{}"
	}
	if !json.Valid([]byte(input)) {
		return nil, fmt.Errorf("input_json must be valid JSON")
	}
	def, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil || def == nil {
		if err == nil {
			err = fmt.Errorf("automation %q not found", definitionID)
		}
		return nil, err
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return nil, err
	}
	if spec.Trigger.Type != "manual" {
		return nil, fmt.Errorf("automation %q is provider-driven and cannot be run manually yet", definitionID)
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		return nil, err
	}
	subjectKey := ""
	canonicalInput := input
	if spec.Location.Type == "repository_worktree" {
		pr, err := automation.ParsePullRequestInput(json.RawMessage(input))
		if err != nil {
			return nil, err
		}
		canonical, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}
		canonicalInput = string(canonical)
		subjectKey = pr.SubjectKey()
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	ids := newAutomationRunReservation()
	run, _, err := d.store.ClaimManualAutomationRun(definitionID, requestID, subjectKey, canonicalInput, def.Revision, string(snapshotJSON), time.Now(), ids)
	if err != nil {
		return nil, err
	}
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	run, err = d.store.GetAutomationRun(run.ID)
	if err != nil {
		return nil, err
	}
	// A run now exists for this definition (freshly claimed, or the idempotent
	// dedup of an already-claimed one) whether or not delivery below succeeds;
	// broadcast so a WS client watching this definition's runs sees it appear
	// without waiting on the delivery outcome.
	d.broadcastAutomationsChanged(definitionID)
	if run.State != store.AutomationRunStatePending {
		return run, nil
	}
	if err := d.deliverAutomationRun(ctx, run); err != nil {
		return d.handleAutomationDeliveryError(run, err)
	}
	return d.store.GetAutomationRun(run.ID)
}
func newAutomationRunReservation() store.AutomationRunReservation {
	runID := uuid.NewString()
	return store.AutomationRunReservation{RunID: runID, OccurrenceID: uuid.NewString(), TicketID: "auto-" + strings.ReplaceAll(runID[:18], "-", ""), SessionID: uuid.NewString(), WorkspaceID: "workspace-" + uuid.NewString(), PaneID: "pane-" + uuid.NewString()}
}
func (d *Daemon) automationObservationLock(definitionID, subjectKey string, cycle int) *sync.Mutex {
	key := fmt.Sprintf("%s\x00%s\x00%d", definitionID, subjectKey, cycle)
	d.automationObservationMu.Lock()
	defer d.automationObservationMu.Unlock()
	if d.automationObservationLocks == nil {
		d.automationObservationLocks = make(map[string]*sync.Mutex)
	}
	lock := d.automationObservationLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		d.automationObservationLocks[key] = lock
	}
	return lock
}

// handleAutomationCommand is the unix-socket transport for the automations
// surface: one command set shared with WS (see ws_automations.go), each
// dispatched to the same action function in automations_actions.go that WS
// uses, so the two transports can never drift in what a given action returns.
// The socket is synchronous, so a context.Background()-derived ctx is fine —
// there is no client-side timeout race to bound here the way WS's
// wsAutomationMutationTimeoutDuration does.
func (d *Daemon) handleAutomationCommand(conn net.Conn, cmd string, msg any) {
	ctx := context.Background()
	var result any
	switch cmd {
	case protocol.CmdAutomationApply:
		result = d.actionAutomationApply(ctx, msg.(*protocol.AutomationApplyMessage))
	case protocol.CmdAutomationValidate:
		result = d.actionAutomationValidate(msg.(*protocol.AutomationValidateMessage))
	case protocol.CmdAutomationDefinitionsGet:
		result = d.actionAutomationDefinitionsGet(msg.(*protocol.AutomationDefinitionsGetMessage))
	case protocol.CmdAutomationDefinitionGet:
		result = d.actionAutomationDefinitionGet(msg.(*protocol.AutomationDefinitionGetMessage))
	case protocol.CmdAutomationRun:
		result = d.actionAutomationRun(ctx, msg.(*protocol.AutomationRunMessage))
	case protocol.CmdAutomationRunsGet:
		result = d.actionAutomationRunsGet(msg.(*protocol.AutomationRunsGetMessage))
	case protocol.CmdAutomationSetEnabled:
		result = d.actionAutomationSetEnabled(ctx, msg.(*protocol.AutomationSetEnabledMessage))
	case protocol.CmdAutomationDelete:
		result = d.actionAutomationDelete(ctx, msg.(*protocol.AutomationDeleteMessage))
	case protocol.CmdAutomationCleanup:
		result = d.actionAutomationCleanup(ctx, msg.(*protocol.AutomationCleanupMessage))
	}
	_ = json.NewEncoder(conn).Encode(result)
}
