package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) startDelegation(msg *protocol.DelegateMessage) (*protocol.DelegationOperation, error) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		// Compatibility for older websocket clients. The CLI always supplies and
		// prints a stable key, which is the recoverable retry contract.
		requestID = uuid.NewString()
	}
	if strings.HasPrefix(requestID, "op-") {
		return nil, fmt.Errorf("request_id uses reserved operation prefix op-")
	}
	msg.RequestID = requestID
	msg.Cmd = protocol.CmdDelegate
	encoded, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode delegation request: %w", err)
	}
	chiefSessionID := ""
	if currentChief := d.chiefOfStaffSessionID(); currentChief == strings.TrimSpace(msg.SourceSessionID) {
		chiefSessionID = currentChief
	}
	record, claimed, err := d.store.ClaimDelegationOperation(requestID, "op-"+uuid.NewString(), uuid.NewString(), chiefSessionID, string(encoded), time.Now())
	if err != nil {
		return nil, err
	}
	if claimed || record.Operation.State == protocol.DelegationOperationStateAccepted || record.Operation.State == protocol.DelegationOperationStatePreparing {
		go d.runDelegationOperation(record.Operation.OperationID)
	}
	return &record.Operation, nil
}

func (d *Daemon) runDelegationOperation(id string) {
	if !d.beginDelegationRun(id) {
		return
	}
	defer d.endDelegationRun(id)
	record, err := d.store.GetDelegationOperation(id)
	if err != nil {
		d.logf("delegate operation %s disappeared: %v", id, err)
		return
	}
	if record.Operation.State == protocol.DelegationOperationStateCompleted || record.Operation.State == protocol.DelegationOperationStateFailed {
		return
	}
	_ = d.store.UpdateDelegationOperation(id, protocol.DelegationOperationStatePreparing,
		"validating delegation request", "", "", "", nil, nil, time.Now())
	var msg protocol.DelegateMessage
	if err := json.Unmarshal([]byte(record.RequestJSON), &msg); err != nil {
		d.finishDelegationFailure(id, fmt.Errorf("decode accepted delegation request: %w", err))
		return
	}
	result, launchErr := d.delegateOperation(&msg, id, record.Operation.SessionID, protocol.Deref(record.Operation.WorktreePath), record.WorktreeOwned, record.WorktreeToken, record.ChiefSessionID)
	if launchErr != nil {
		d.finishDelegationFailure(id, launchErr)
		return
	}
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateCompleted,
		"delegation ready", result.WorkspaceID, "", result, nil)
}

func (d *Daemon) finishDelegationFailure(id string, err error) {
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateFailed,
		"delegation failed", "", "", nil, err)
}

// Terminal persistence is part of completing the launch operation, not
// best-effort bookkeeping. If SQLite temporarily rejects the write after
// externally visible side effects, keep one finisher alive so callers cannot be
// stranded polling a permanently preparing record. Daemon restart is the other
// recovery boundary: pending records are resumed from the durable journal.
func (d *Daemon) persistDelegationTerminal(id string, state protocol.DelegationOperationState, progress, workspaceID, worktreePath string, result *protocol.DelegateResult, operationErr error) {
	delay := 100 * time.Millisecond
	for {
		if err := d.store.UpdateDelegationOperation(id, state, progress, workspaceID, "", worktreePath, result, operationErr, time.Now()); err == nil {
			return
		} else {
			d.logf("persist terminal delegation operation %s: %v", id, err)
		}
		select {
		case <-d.done:
			return
		case <-time.After(delay):
			if delay < 5*time.Second {
				delay *= 2
			}
		}
	}
}

func (d *Daemon) beginDelegationRun(id string) bool {
	d.delegationMu.Lock()
	defer d.delegationMu.Unlock()
	if d.delegationRunning == nil {
		d.delegationRunning = make(map[string]bool)
	}
	if d.delegationRunning[id] {
		return false
	}
	d.delegationRunning[id] = true
	return true
}

func (d *Daemon) endDelegationRun(id string) {
	d.delegationMu.Lock()
	delete(d.delegationRunning, id)
	d.delegationMu.Unlock()
}

func (d *Daemon) delegationOperation(id string) (*protocol.DelegationOperation, error) {
	record, err := d.store.GetDelegationOperation(strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("delegation operation not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	return &record.Operation, nil
}

func (d *Daemon) resumePendingDelegations() {
	records, err := d.store.PendingDelegationOperations()
	if err != nil {
		d.logf("load pending delegation operations: %v", err)
		return
	}
	for i := range records {
		go d.runDelegationOperation(records[i].Operation.OperationID)
	}
}

func (d *Daemon) handleDelegateStatusWS(client *wsClient, msg *protocol.DelegateStatusMessage) {
	operation, err := d.delegationOperation(msg.ID)
	response := protocol.DelegationOperationMessage{
		Event:     protocol.EventDelegationOperation,
		Success:   err == nil,
		Operation: operation,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
