package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// This file holds the shared core for the durable workflow engine's daemon-side
// IPC. The engine itself runs in the `attn workflow run` CLI process; the daemon
// only persists (via the S-store CRUD), coalesced-broadcasts run updates to the
// read-only UI, serves get/list, and relays cancel to the engine process. Both
// the socket dispatch (engine + CLI) and the WS dispatch (UI) delegate here, the
// same split review-loop uses (handle*ReviewLoop / handle*ReviewLoopWS over core
// funcs).

// --- protocol <-> store row conversions -------------------------------------

func workflowRunProtoToRow(run *protocol.WorkflowRun) *store.WorkflowRunRow {
	if run == nil {
		return nil
	}
	return &store.WorkflowRunRow{
		RunID:       run.RunID,
		ScriptPath:  run.ScriptPath,
		ScriptHash:  run.ScriptHash,
		ArgsJSON:    run.ArgsJson,
		SessionID:   run.SessionID,
		WorkspaceID: run.WorkspaceID,
		Status:      string(run.Status),
		Phase:       run.Phase,
		Harness:     run.Harness,
		ResultJSON:  run.ResultJson,
		LastError:   run.LastError,
		Resumable:   run.Resumable,
		CreatedAt:   run.CreatedAt,
		UpdatedAt:   run.UpdatedAt,
		CompletedAt: run.CompletedAt,
	}
}

func workflowRunRowToProto(row *store.WorkflowRunRow) *protocol.WorkflowRun {
	if row == nil {
		return nil
	}
	return &protocol.WorkflowRun{
		RunID:       row.RunID,
		ScriptPath:  row.ScriptPath,
		ScriptHash:  row.ScriptHash,
		ArgsJson:    row.ArgsJSON,
		SessionID:   row.SessionID,
		WorkspaceID: row.WorkspaceID,
		Status:      protocol.WorkflowRunStatus(row.Status),
		Phase:       row.Phase,
		Harness:     row.Harness,
		ResultJson:  row.ResultJSON,
		LastError:   row.LastError,
		Resumable:   row.Resumable,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CompletedAt: row.CompletedAt,
	}
}

func workflowCallProtoToRow(call *protocol.WorkflowAgentCall) *store.WorkflowAgentCallRow {
	if call == nil {
		return nil
	}
	return &store.WorkflowAgentCallRow{
		RunID:           call.RunID,
		Ordinal:         call.Ordinal,
		Label:           call.Label,
		Phase:           call.Phase,
		PromptHash:      call.PromptHash,
		SchemaHash:      call.SchemaHash,
		ResolvedModel:   call.ResolvedModel,
		ResolvedHarness: call.ResolvedHarness,
		AgentType:       call.AgentType,
		ResultJSON:      call.ResultJson,
		Status:          string(call.Status),
		Error:           call.Error,
		ResultPath:      call.ResultPath,
		StartedAt:       call.StartedAt,
		CompletedAt:     call.CompletedAt,
	}
}

func workflowCallRowToProto(row *store.WorkflowAgentCallRow) protocol.WorkflowAgentCall {
	if row == nil {
		return protocol.WorkflowAgentCall{}
	}
	return protocol.WorkflowAgentCall{
		RunID:           row.RunID,
		Ordinal:         row.Ordinal,
		Label:           row.Label,
		Phase:           row.Phase,
		PromptHash:      row.PromptHash,
		SchemaHash:      row.SchemaHash,
		ResolvedModel:   row.ResolvedModel,
		ResolvedHarness: row.ResolvedHarness,
		AgentType:       row.AgentType,
		ResultJson:      row.ResultJSON,
		Status:          protocol.WorkflowAgentCallStatus(row.Status),
		Error:           row.Error,
		ResultPath:      row.ResultPath,
		StartedAt:       row.StartedAt,
		CompletedAt:     row.CompletedAt,
	}
}

// --- shared core ------------------------------------------------------------

// getWorkflowRunHydrated loads a run plus its journaled agent calls and returns
// the protocol shape with AgentCalls populated in durable append order. Returns
// (nil, nil) when the run is absent.
func (d *Daemon) getWorkflowRunHydrated(runID string) (*protocol.WorkflowRun, error) {
	row, err := d.store.GetWorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	run := workflowRunRowToProto(row)

	calls, err := d.store.ListWorkflowAgentCalls(runID)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 {
		run.AgentCalls = make([]protocol.WorkflowAgentCall, 0, len(calls))
		for _, call := range calls {
			run.AgentCalls = append(run.AgentCalls, workflowCallRowToProto(call))
		}
	}
	return run, nil
}

// listWorkflowRunsHydrated returns runs for a session (empty sessionID = all),
// newest-first as the store orders them. Agent calls are intentionally OMITTED
// from list entries: the list view is a summary surface, and hydrating every
// run's full journal would be O(runs * calls) for a screen that only needs the
// run header. Callers that need the journal fetch a single run via
// getWorkflowRunHydrated.
func (d *Daemon) listWorkflowRunsHydrated(sessionID string) ([]*protocol.WorkflowRun, error) {
	rows, err := d.store.ListWorkflowRuns(sessionID)
	if err != nil {
		return nil, err
	}
	runs := make([]*protocol.WorkflowRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, workflowRunRowToProto(row))
	}
	return runs, nil
}

// applyWorkflowRunUpsert persists the run row plus every embedded AgentCall,
// re-hydrates, marks the run dirty for a coalesced broadcast, and returns the
// hydrated run.
func (d *Daemon) applyWorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error) {
	if run == nil {
		return nil, nil
	}
	if err := d.store.UpsertWorkflowRun(workflowRunProtoToRow(run)); err != nil {
		return nil, err
	}
	for i := range run.AgentCalls {
		call := run.AgentCalls[i]
		if call.RunID == "" {
			call.RunID = run.RunID
		}
		if err := d.store.UpsertWorkflowAgentCall(workflowCallProtoToRow(&call)); err != nil {
			return nil, err
		}
	}
	hydrated, err := d.getWorkflowRunHydrated(run.RunID)
	if err != nil {
		return nil, err
	}
	d.markWorkflowRunDirty(run.RunID)
	return hydrated, nil
}

// applyWorkflowCallUpsert persists a single agent call (ON CONFLICT(run_id,
// ordinal) updates in place), re-hydrates the owning run, marks it dirty, and
// returns the hydrated run.
func (d *Daemon) applyWorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error) {
	if call == nil {
		return d.getWorkflowRunHydrated(runID)
	}
	row := workflowCallProtoToRow(call)
	if row.RunID == "" {
		row.RunID = runID
	}
	if err := d.store.UpsertWorkflowAgentCall(row); err != nil {
		return nil, err
	}
	hydrated, err := d.getWorkflowRunHydrated(runID)
	if err != nil {
		return nil, err
	}
	d.markWorkflowRunDirty(runID)
	return hydrated, nil
}

// cancelWorkflowRun marks a run canceled, persists it, relays a cancel control
// frame to the registered engine sink, and re-broadcasts. Returns (nil, false,
// nil) when the run is absent. The bool reports whether an engine sink was found
// to relay to (the engine may have already exited).
func (d *Daemon) cancelWorkflowRun(runID string) (*protocol.WorkflowRun, bool, error) {
	row, err := d.store.GetWorkflowRun(runID)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		return nil, false, nil
	}

	now := string(protocol.TimestampNow())
	row.Status = string(protocol.WorkflowRunStatusCanceled)
	row.UpdatedAt = now
	row.CompletedAt = protocol.Ptr(now)
	if err := d.store.UpsertWorkflowRun(row); err != nil {
		return nil, false, err
	}

	relayed := d.relayWorkflowCancel(runID)

	hydrated, err := d.getWorkflowRunHydrated(runID)
	if err != nil {
		return nil, relayed, err
	}
	d.markWorkflowRunDirty(runID)
	return hydrated, relayed, nil
}

// --- socket (engine + CLI) handlers -----------------------------------------
//
// Thin wrappers over the core, mirroring review-loop's handle*ReviewLoop. The
// engine connects over the unix socket, so upsert/call_upsert register the
// requesting net.Conn as the run's engine sink for a later cancel relay. Replies
// use WorkflowActionResultMessage (which carries run/runs) because protocol
// Response has no workflow field; that keeps one shared reply shape across both
// transports.

func (d *Daemon) sendWorkflowActionResult(conn net.Conn, action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) {
	result := buildWorkflowActionResult(action, run, runs, runID, err)
	_ = json.NewEncoder(conn).Encode(result)
}

func buildWorkflowActionResult(action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) *protocol.WorkflowActionResultMessage {
	result := &protocol.WorkflowActionResultMessage{
		Event:   protocol.EventWorkflowActionResult,
		Action:  action,
		Success: err == nil,
	}
	if strings.TrimSpace(runID) != "" {
		result.RunID = protocol.Ptr(runID)
	}
	if run != nil {
		result.Run = run
	}
	if len(runs) > 0 {
		values := make([]protocol.WorkflowRun, 0, len(runs))
		for _, r := range runs {
			if r != nil {
				values = append(values, *r)
			}
		}
		result.Runs = values
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return result
}

// guardWorkflowRunStart enforces the workflows_enabled master switch. A run-level
// upsert carrying running status is the one and only run START (fresh or resume) —
// progress is reported via call upserts, and the finish carries a terminal status.
// Rejecting running-status starts here, at the CLI/engine socket entry (the sole
// run-creation path), blocks new runs when the feature is off while keeping the
// persistence core policy-free and still letting a run that was already in flight
// when the switch flipped off record its terminal result.
func (d *Daemon) guardWorkflowRunStart(run *protocol.WorkflowRun) error {
	if run == nil || run.Status != protocol.WorkflowRunStatusRunning {
		return nil
	}
	if parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)) {
		return nil
	}
	return fmt.Errorf("workflows are disabled; enable Workflows in attn Settings (Agents) to run one")
}

func (d *Daemon) handleWorkflowRunUpsert(conn net.Conn, msg *protocol.WorkflowRunUpsertMessage) {
	if err := d.guardWorkflowRunStart(&msg.Run); err != nil {
		d.sendWorkflowActionResult(conn, "upsert", nil, nil, msg.Run.RunID, err)
		return
	}
	d.registerWorkflowEngine(msg.Run.RunID, connWorkflowEngineSink{conn: conn})
	run, err := d.applyWorkflowRunUpsert(&msg.Run)
	d.sendWorkflowActionResult(conn, "upsert", run, nil, msg.Run.RunID, err)
}

func (d *Daemon) handleWorkflowCallUpsert(conn net.Conn, msg *protocol.WorkflowCallUpsertMessage) {
	d.registerWorkflowEngine(msg.RunID, connWorkflowEngineSink{conn: conn})
	run, err := d.applyWorkflowCallUpsert(msg.RunID, &msg.Call)
	d.sendWorkflowActionResult(conn, "call_upsert", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunGet(conn net.Conn, msg *protocol.WorkflowRunGetMessage) {
	run, err := d.getWorkflowRunHydrated(msg.RunID)
	d.sendWorkflowActionResult(conn, "get", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunList(conn net.Conn, msg *protocol.WorkflowRunListMessage) {
	runs, err := d.listWorkflowRunsHydrated(protocol.Deref(msg.SessionID))
	d.sendWorkflowActionResult(conn, "list", nil, runs, "", err)
}

func (d *Daemon) handleWorkflowRunCancel(conn net.Conn, msg *protocol.WorkflowRunCancelMessage) {
	run, _, err := d.cancelWorkflowRun(msg.RunID)
	d.sendWorkflowActionResult(conn, "cancel", run, nil, msg.RunID, err)
}
