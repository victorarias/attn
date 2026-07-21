package daemon

import (
	"context"
	"encoding/json"
	"errors"
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

type automationActionResult struct {
	Event   string          `json:"event"`
	Action  string          `json:"action"`
	Success bool            `json:"success"`
	Error   *string         `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// automationCleanupResult is automation_cleanup's socket/CLI data payload —
// mirrors AutomationActionResultMessage's cleaned/kept_dirty/kept_active WS
// fields.
type automationCleanupResult struct {
	Cleaned    []string `json:"cleaned"`
	KeptDirty  []string `json:"kept_dirty"`
	KeptActive []string `json:"kept_active"`
}

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
	if run.State != "pending" {
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
func (d *Daemon) handleAutomationCommand(conn net.Conn, cmd string, msg any) {
	var data any
	var err error
	switch cmd {
	case protocol.CmdAutomationApply:
		m := msg.(*protocol.AutomationApplyMessage)
		data, err = d.automationApply(m.DefinitionYaml)
	case protocol.CmdAutomationValidate:
		m := msg.(*protocol.AutomationValidateMessage)
		_, _, err = d.validateAutomationSpec(m.DefinitionYaml)
	case protocol.CmdAutomationList:
		data, err = d.store.ListAutomationDefinitions()
	case protocol.CmdAutomationShow:
		m := msg.(*protocol.AutomationShowMessage)
		data, err = d.store.GetAutomationDefinition(m.DefinitionID)
	case protocol.CmdAutomationRun:
		m := msg.(*protocol.AutomationRunMessage)
		if strings.TrimSpace(protocol.Deref(m.PRURL)) != "" && strings.TrimSpace(protocol.Deref(m.InputJson)) != "" {
			err = errors.New("pr_url and input_json are mutually exclusive")
			break
		}
		if strings.TrimSpace(protocol.Deref(m.PRURL)) != "" {
			data, err = d.automationRunPullRequest(context.Background(), m.DefinitionID, m.RequestID, protocol.Deref(m.PRURL))
		} else {
			data, err = d.automationRun(context.Background(), m.DefinitionID, m.RequestID, protocol.Deref(m.InputJson))
		}
	case protocol.CmdAutomationRunList:
		m := msg.(*protocol.AutomationRunListMessage)
		data, err = d.store.ListAutomationRuns(m.DefinitionID)
	case protocol.CmdAutomationSetEnabled:
		m := msg.(*protocol.AutomationSetEnabledMessage)
		data, err = d.automationSetEnabled(context.Background(), m.DefinitionID, m.Enabled)
	case protocol.CmdAutomationDelete:
		m := msg.(*protocol.AutomationDeleteMessage)
		err = d.automationDelete(context.Background(), m.DefinitionID)
	case protocol.CmdAutomationCleanup:
		m := msg.(*protocol.AutomationCleanupMessage)
		var cleaned, keptDirty, keptActive []string
		cleaned, keptDirty, keptActive, err = d.automationCleanup(context.Background(), m.DefinitionID)
		if err == nil {
			data = automationCleanupResult{Cleaned: cleaned, KeptDirty: keptDirty, KeptActive: keptActive}
		}
	}
	result := automationActionResult{Event: protocol.EventAutomationActionResult, Action: cmd, Success: err == nil}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else if data != nil {
		// Guarded on nil rather than marshalling unconditionally: an action with
		// no payload (validate) leaves data as a nil `any`, and json.Marshal of
		// that yields the 4-byte literal `null`, not an empty slice — so
		// `json:"data,omitempty"` would not drop the field and the wire would
		// carry "data":null. json.RawMessage implements Unmarshaler and is
		// invoked even for JSON null, so the client would decode a non-nil
		// 4-byte Data and every "did I get a payload?" check downstream would
		// answer yes. Leaving Data nil here is what lets omitempty do its job.
		result.Data, _ = json.Marshal(data)
	}
	_ = json.NewEncoder(conn).Encode(result)
}
