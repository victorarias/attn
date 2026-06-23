package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/dispatch"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// closedDispatchStatus is the projected Status for a dispatch whose delegated
// session is gone. The literal is owned by internal/dispatch (the watch signal
// classifier consumes it), so reference it here rather than duplicating it.
const closedDispatchStatus = dispatch.SessionClosedStatus
const dispatchWakePrompt = "Check your attn inbox and act on pending messages."

func chiefOfStaffDispatchPrompt(brief string) string {
	return strings.TrimSpace(brief) + `

---
This task is tracked by the chief of staff in attn. Two rules keep the chief aware
of you and able to steer you, without babysitting:

1. SELF-REPORT ONLY AT A TERMINAL OR BLOCKED POINT — never progress, never status.
   One flag declares the outcome; it is the chief's trigger:

       "$ATTN_WRAPPER_PATH" dispatch report --done    --message "<what landed>"
       "$ATTN_WRAPPER_PATH" dispatch report --review  --message "<what to review>"
       "$ATTN_WRAPPER_PATH" dispatch report --failed  --message "<what went wrong>"
       "$ATTN_WRAPPER_PATH" dispatch report --blocked --question "<the decision>" \
           --recommendation "<your pick>" --consequence "<what is at stake>"

   A bare ` + "`--message`" + ` with no flag is a silent note (no trigger) — prefer
   silence between terminal/blocked points. Use ` + "`--file <path>`" + ` for a long update.

2. ARM A QUIET WATCH ON YOUR INBOX so the chief can steer you without a manual
   wake. If your agent supports background watches (e.g. Claude's Monitor), watch

       "$ATTN_WRAPPER_PATH" dispatch inbox --unread

   and when mail arrives, read it, act, then ` + "`dispatch ack --message-id <id>`" + `.
   Keep the watch quiet — it should emit only when there is new mail.

A large durable artifact (report, design doc, findings) goes into the Notebook, not
inline — hand it off and report just the reference:

    "$ATTN_WRAPPER_PATH" dispatch handoff --file <artifact> --to <notebook-path> --message "<update>"

Write to the Notebook path the chief or user designated. If none was designated and
the artifact warrants one, ask the chief in a report rather than inventing a location.

Use ` + "`dispatch status`" + ` to read the chief's durable response to a decision.
Continue working after a blocked report unless you are truly stuck; stop when finished.`
}

func (d *Daemon) newChiefOfStaffDispatch(
	chiefSessionID string,
	session *protocol.Session,
	workspaceID, brief, label, agent string,
) *protocol.ChiefOfStaffDispatch {
	now := string(protocol.TimestampNow())
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:             uuid.NewString(),
		ChiefSessionID: chiefSessionID,
		SessionID:      session.ID,
		WorkspaceID:    workspaceID,
		Brief:          brief,
		Label:          label,
		Agent:          agent,
		Directory:      session.Directory,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if branch := strings.TrimSpace(protocol.Deref(session.Branch)); branch != "" {
		dispatch.Branch = protocol.Ptr(branch)
	}
	return dispatch
}

func (d *Daemon) decorateChiefOfStaffDispatch(dispatch *protocol.ChiefOfStaffDispatch) *protocol.ChiefOfStaffDispatch {
	if dispatch == nil {
		return nil
	}
	decorated := *dispatch
	if dispatch.Branch != nil {
		decorated.Branch = protocol.Ptr(protocol.Deref(dispatch.Branch))
	}
	if dispatch.LatestReport != nil {
		decorated.LatestReport = protocol.Ptr(protocol.Deref(dispatch.LatestReport))
	}
	if dispatch.ReportedAt != nil {
		decorated.ReportedAt = protocol.Ptr(protocol.Deref(dispatch.ReportedAt))
	}
	decorated.StructuredReport = cloneDispatchReportForDisplay(dispatch.StructuredReport)
	if decorated.StructuredReport != nil {
		summary := strings.TrimSpace(decorated.StructuredReport.Summary)
		if summary != "" {
			decorated.ConciseSummary = protocol.Ptr(summary)
		}
		actionable := decorated.StructuredReport.WorkState == protocol.DispatchWorkStateNeedsInput ||
			decorated.StructuredReport.WorkState == protocol.DispatchWorkStateReadyForReview
		if request := decorated.StructuredReport.Request; request != nil {
			actionable = request.Status == protocol.DispatchRequestStatusPending
		}
		decorated.Actionable = protocol.Ptr(actionable)
	} else if dispatch.LatestReport != nil {
		decorated.ConciseSummary = protocol.Ptr(protocol.Deref(dispatch.LatestReport))
		decorated.Actionable = protocol.Ptr(false)
	}
	if unreadCount, err := d.store.CountUnreadDispatchMessages(dispatch.ID); err == nil {
		decorated.UnreadMessageCount = protocol.Ptr(unreadCount)
	} else {
		d.logf("chief dispatch %s unread count: %v", dispatch.ID, err)
	}
	if session := d.store.Get(dispatch.SessionID); session != nil {
		decorated.Status = string(session.State)
		decorated.StatusSince = session.StateSince
	} else {
		decorated.Status = closedDispatchStatus
		decorated.StatusSince = ""
	}
	return &decorated
}

func (d *Daemon) validateDispatchChief(dispatchID, chiefSessionID string) (*protocol.ChiefOfStaffDispatch, error) {
	// Session IDs route commands within the trusted local daemon control plane;
	// the Unix socket, not each command payload, is attn's authorization boundary.
	dispatch := d.store.GetChiefOfStaffDispatch(strings.TrimSpace(dispatchID))
	if dispatch == nil {
		return nil, fmt.Errorf("dispatch %s not found", strings.TrimSpace(dispatchID))
	}
	if dispatch.ChiefSessionID != strings.TrimSpace(chiefSessionID) {
		return nil, fmt.Errorf("dispatch %s is not owned by chief session %s", dispatch.ID, strings.TrimSpace(chiefSessionID))
	}
	return dispatch, nil
}

func (d *Daemon) validateDispatchWorker(sourceSessionID string) (*protocol.ChiefOfStaffDispatch, error) {
	dispatch := d.store.GetChiefOfStaffDispatchBySession(strings.TrimSpace(sourceSessionID))
	if dispatch == nil {
		return nil, fmt.Errorf("session %s is not a tracked dispatch", strings.TrimSpace(sourceSessionID))
	}
	return dispatch, nil
}

func cloneDispatchReportForDisplay(report *protocol.DispatchReport) *protocol.DispatchReport {
	if report == nil {
		return nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	var cloned protocol.DispatchReport
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	artifactIdentity := ""
	if cloned.Artifact != nil {
		artifactIdentity = strings.TrimSpace(cloned.Artifact.Identity)
	}
	for i := range cloned.Verification {
		current := artifactIdentity != "" &&
			strings.TrimSpace(cloned.Verification[i].ArtifactIdentity) == artifactIdentity
		cloned.Verification[i].Current = protocol.Ptr(current)
	}
	return &cloned
}

func validateDispatchReport(report *protocol.DispatchReport) error {
	if report == nil {
		return nil
	}
	switch report.ReportType {
	case protocol.DispatchReportTypeProgress,
		protocol.DispatchReportTypeBlocker,
		protocol.DispatchReportTypeHandoff,
		protocol.DispatchReportTypeCompletion,
		protocol.DispatchReportTypeFailure:
	default:
		return fmt.Errorf("invalid report_type %q", report.ReportType)
	}
	switch report.WorkState {
	case protocol.DispatchWorkStateInProgress,
		protocol.DispatchWorkStateNeedsInput,
		protocol.DispatchWorkStateReadyForReview,
		protocol.DispatchWorkStateCompleted,
		protocol.DispatchWorkStateFailed:
	default:
		return fmt.Errorf("invalid work_state %q", report.WorkState)
	}
	if strings.TrimSpace(report.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if report.Request != nil {
		if strings.TrimSpace(report.Request.Question) == "" {
			return fmt.Errorf("request.question is required")
		}
		if strings.TrimSpace(report.Request.ExpectedResponder) == "" {
			return fmt.Errorf("request.expected_responder is required")
		}
		report.Request.Status = protocol.DispatchRequestStatusPending
		report.Request.Response = nil
		report.Request.ResolutionLink = nil
		report.Request.RespondedBy = nil
		report.Request.RespondedAt = nil
	}
	if report.Artifact != nil && strings.TrimSpace(report.Artifact.Identity) == "" {
		return fmt.Errorf("artifact.identity is required")
	}
	for i := range report.Verification {
		evidence := &report.Verification[i]
		if strings.TrimSpace(evidence.Actor) == "" ||
			strings.TrimSpace(evidence.Target) == "" ||
			strings.TrimSpace(evidence.Result) == "" ||
			strings.TrimSpace(evidence.Timestamp) == "" ||
			strings.TrimSpace(evidence.ArtifactIdentity) == "" {
			return fmt.Errorf("verification[%d] requires actor, target, result, timestamp, and artifact_identity", i)
		}
		evidence.Current = nil
	}
	return nil
}

func (d *Daemon) chiefOfStaffDispatches(chiefSessionID string) []protocol.ChiefOfStaffDispatch {
	records := d.store.ListChiefOfStaffDispatches(chiefSessionID)
	result := make([]protocol.ChiefOfStaffDispatch, 0, len(records))
	for _, record := range records {
		if decorated := d.decorateChiefOfStaffDispatch(record); decorated != nil {
			result = append(result, *decorated)
		}
	}
	return result
}

func (d *Daemon) broadcastChiefOfStaffDispatchesUpdated() {
	if d.wsHub == nil || d.store == nil {
		return
	}
	d.broadcastMessage(&protocol.ChiefOfStaffDispatchesUpdatedMessage{
		Event:      protocol.EventChiefOfStaffDispatchesUpdated,
		Dispatches: d.chiefOfStaffDispatches(""),
	})
}

func (d *Daemon) handleListDispatches(conn net.Conn, msg *protocol.ListDispatchesMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "dispatch list: source_session_id is required")
		return
	}
	if !d.sessionExists(sourceSessionID) && len(d.store.ListChiefOfStaffDispatches(sourceSessionID)) == 0 {
		d.sendError(conn, fmt.Sprintf("dispatch list: source session not found: %s", sourceSessionID))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                     true,
		ChiefOfStaffDispatches: d.chiefOfStaffDispatches(sourceSessionID),
	})
}

func (d *Daemon) handleReportDispatch(conn net.Conn, msg *protocol.ReportDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	report := strings.TrimSpace(msg.Report)
	if sourceSessionID == "" {
		d.sendError(conn, "dispatch report: source_session_id is required")
		return
	}
	if report == "" {
		d.sendError(conn, "dispatch report: report is required")
		return
	}
	if err := validateDispatchReport(msg.StructuredReport); err != nil {
		d.sendError(conn, "dispatch report: "+err.Error())
		return
	}
	dispatch, err := d.store.UpdateChiefOfStaffDispatchReportEnvelope(
		sourceSessionID,
		report,
		msg.StructuredReport,
	)
	if err != nil {
		d.sendError(conn, "dispatch report: "+err.Error())
		return
	}
	// Capture finished delegated work in the durable journal BEFORE acking the
	// report, so a successful response means the outcome is recorded (durable
	// before ack), not merely queued. The stored record carries the server-stamped
	// report time; the embedded marker keeps this idempotent against the
	// session-gone fallback. Running it synchronously also makes the raw-tier write
	// observable to the caller — when the response returns the per-dispatch file is
	// on disk — so a socket-level test never races t.TempDir() cleanup against a
	// still-pending post-response write.
	if isTerminalDispatchReport(msg.StructuredReport) {
		d.journalDispatchOutcome(dispatch)
	}
	decorated := d.decorateChiefOfStaffDispatch(dispatch)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                   true,
		ChiefOfStaffDispatch: decorated,
	})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

// handleHandoffDispatch writes a dispatched agent's large artifact into the
// Notebook at the chief-designated path, then records a normal dispatch report
// whose message embeds the resolved reference. A dispatch report is a small
// payload; the Notebook carries the bulky artifact the agent built (often with the
// user), and the report hands the chief a reference it can act on, move, or
// promote. The Notebook write happens before the report is recorded, so a report
// can never reference an artifact that failed to land.
func (d *Daemon) handleHandoffDispatch(conn net.Conn, msg *protocol.HandoffDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	to := strings.TrimSpace(msg.To)
	if sourceSessionID == "" {
		d.sendError(conn, "dispatch handoff: source_session_id is required")
		return
	}
	if to == "" {
		d.sendError(conn, "dispatch handoff: to is required")
		return
	}
	if strings.TrimSpace(msg.Content) == "" {
		d.sendError(conn, "dispatch handoff: content is required")
		return
	}
	if err := validateDispatchReport(msg.StructuredReport); err != nil {
		d.sendError(conn, "dispatch handoff: "+err.Error())
		return
	}
	// Only a tracked dispatch may hand off into the Notebook: the session must be
	// delegated work the chief is tracking, not an arbitrary caller.
	if _, err := d.validateDispatchWorker(sourceSessionID); err != nil {
		d.sendError(conn, "dispatch handoff: "+err.Error())
		return
	}
	rel, err := notebook.CleanPath(to)
	if err != nil {
		d.sendError(conn, "dispatch handoff: "+err.Error())
		return
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "dispatch handoff: notebook unavailable: "+err.Error())
		return
	}
	hash, err := writeNotebookOverwrite(store, rel, []byte(msg.Content))
	if err != nil {
		d.sendError(conn, "dispatch handoff: "+err.Error())
		return
	}
	// Record the write so the watcher does not re-broadcast it as an external edit,
	// then announce it as an agent-origin change (refreshes any open in-app browser).
	d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel, Hash: hash})
	d.broadcastNotebookChanged(originAgent, rel)

	report := composeHandoffReport(strings.TrimSpace(protocol.Deref(msg.Report)), rel)
	dispatch, err := d.store.UpdateChiefOfStaffDispatchReportEnvelope(
		sourceSessionID,
		report,
		msg.StructuredReport,
	)
	if err != nil {
		d.sendError(conn, "dispatch handoff: "+err.Error())
		return
	}
	if isTerminalDispatchReport(msg.StructuredReport) {
		d.journalDispatchOutcome(dispatch)
	}
	decorated := d.decorateChiefOfStaffDispatch(dispatch)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                   true,
		ChiefOfStaffDispatch: decorated,
	})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

// composeHandoffReport builds the dispatch report message for a handoff: the
// agent's optional note (or a neutral default) followed by a resolvable
// root-absolute reference to the artifact the daemon just wrote. The reference is
// composed server-side so it always points at the real written path.
func composeHandoffReport(note, rel string) string {
	if note == "" {
		note = "Handed off an artifact to the Notebook."
	}
	return note + "\n\nArtifact in the Notebook: /" + rel
}

// writeNotebookOverwrite writes content to rel, creating it or overwriting an
// existing note — a refined re-handoff to the same chief-designated path updates
// the one note. It tries a create-only write first and, on the resulting conflict,
// a hash-CAS overwrite, so the write stays safe against a concurrent edit of the
// same path rather than blindly clobbering it.
func writeNotebookOverwrite(store *notebook.Store, rel string, content []byte) (string, error) {
	hash, conflict, err := store.Write(rel, content, "")
	if err != nil {
		return "", err
	}
	if conflict == nil {
		return hash, nil
	}
	hash, conflict, err = store.Write(rel, content, conflict.CurrentHash)
	if err != nil {
		return "", err
	}
	if conflict != nil {
		return "", fmt.Errorf("notebook: %q changed during handoff; retry", rel)
	}
	return hash, nil
}

func (d *Daemon) handleGetDispatch(conn net.Conn, msg *protocol.GetDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "dispatch status: source_session_id is required")
		return
	}
	dispatch := d.store.GetChiefOfStaffDispatchBySession(sourceSessionID)
	if dispatch == nil {
		d.sendError(conn, fmt.Sprintf("dispatch status: session %s is not a tracked dispatch", sourceSessionID))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                   true,
		ChiefOfStaffDispatch: d.decorateChiefOfStaffDispatch(dispatch),
	})
}

func (d *Daemon) handleResolveDispatchRequest(conn net.Conn, msg *protocol.ResolveDispatchRequestMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	dispatchID := strings.TrimSpace(msg.DispatchID)
	response := strings.TrimSpace(msg.Response)
	if sourceSessionID == "" {
		d.sendError(conn, "dispatch resolve: source_session_id is required")
		return
	}
	if dispatchID == "" {
		d.sendError(conn, "dispatch resolve: dispatch_id is required")
		return
	}
	if response == "" {
		d.sendError(conn, "dispatch resolve: response is required")
		return
	}
	dispatch, err := d.store.ResolveChiefOfStaffDispatchRequest(
		dispatchID,
		sourceSessionID,
		response,
		strings.TrimSpace(protocol.Deref(msg.ResolutionLink)),
	)
	if err != nil {
		d.sendError(conn, "dispatch resolve: "+err.Error())
		return
	}
	decorated := d.decorateChiefOfStaffDispatch(dispatch)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                   true,
		ChiefOfStaffDispatch: decorated,
	})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

func (d *Daemon) handleSendDispatchMessage(conn net.Conn, msg *protocol.SendDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	dispatchID := strings.TrimSpace(msg.DispatchID)
	content := strings.TrimSpace(msg.Content)
	if sourceSessionID == "" || dispatchID == "" || content == "" {
		d.sendError(conn, "dispatch message: source_session_id, dispatch_id, and content are required")
		return
	}
	dispatch, err := d.validateDispatchChief(dispatchID, sourceSessionID)
	if err != nil {
		d.sendError(conn, "dispatch message: "+err.Error())
		return
	}
	if d.store.Get(dispatch.SessionID) == nil {
		d.sendError(conn, fmt.Sprintf("dispatch message: delegated session %s is closed", dispatch.SessionID))
		return
	}
	message := &protocol.DispatchMessage{
		ID:              uuid.NewString(),
		DispatchID:      dispatch.ID,
		SenderSessionID: sourceSessionID,
		TargetSessionID: dispatch.SessionID,
		Content:         content,
		CreatedAt:       string(protocol.TimestampNow()),
	}
	if err := d.store.AddDispatchMessage(message); err != nil {
		d.sendError(conn, "dispatch message: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DispatchMessage: message})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

func (d *Daemon) handleListDispatchMessages(conn net.Conn, msg *protocol.ListDispatchMessagesMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	dispatchID := strings.TrimSpace(protocol.Deref(msg.DispatchID))
	var (
		dispatch *protocol.ChiefOfStaffDispatch
		err      error
	)
	if dispatchID != "" {
		dispatch, err = d.validateDispatchChief(dispatchID, sourceSessionID)
	} else {
		dispatch, err = d.validateDispatchWorker(sourceSessionID)
	}
	if err != nil {
		d.sendError(conn, "dispatch messages: "+err.Error())
		return
	}
	messages, err := d.store.ListDispatchMessages(dispatch.ID, protocol.Deref(msg.UnreadOnly))
	if err != nil {
		d.sendError(conn, "dispatch messages: "+err.Error())
		return
	}
	values := make([]protocol.DispatchMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			values = append(values, *message)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:               true,
		DispatchMessages: values,
	})
}

func (d *Daemon) handleReadDispatchMessage(conn net.Conn, msg *protocol.ReadDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	dispatch, err := d.validateDispatchWorker(sourceSessionID)
	if err != nil {
		d.sendError(conn, "dispatch read: "+err.Error())
		return
	}
	message, err := d.store.MarkDispatchMessageRead(
		strings.TrimSpace(msg.MessageID),
		dispatch.ID,
		sourceSessionID,
	)
	if err != nil {
		d.sendError(conn, "dispatch read: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DispatchMessage: message})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

func (d *Daemon) handleAcknowledgeDispatchMessage(conn net.Conn, msg *protocol.AcknowledgeDispatchMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	dispatch, err := d.validateDispatchWorker(sourceSessionID)
	if err != nil {
		d.sendError(conn, "dispatch ack: "+err.Error())
		return
	}
	message, err := d.store.AcknowledgeDispatchMessage(
		strings.TrimSpace(msg.MessageID),
		dispatch.ID,
		sourceSessionID,
		strings.TrimSpace(protocol.Deref(msg.Acknowledgement)),
	)
	if err != nil {
		d.sendError(conn, "dispatch ack: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DispatchMessage: message})
	d.broadcastChiefOfStaffDispatchesUpdated()
}

func (d *Daemon) handleWakeDispatchAgent(client *wsClient, msg *protocol.WakeDispatchAgentMessage) {
	go d.executeWakeDispatchAgent(client, msg)
}

func (d *Daemon) executeWakeDispatchAgent(client *wsClient, msg *protocol.WakeDispatchAgentMessage) {
	dispatchID := strings.TrimSpace(msg.DispatchID)
	dispatch, err := d.validateDispatchChief(dispatchID, strings.TrimSpace(msg.SourceSessionID))
	if err == nil {
		var unreadCount int
		unreadCount, err = d.store.CountUnreadDispatchMessages(dispatch.ID)
		if err == nil && unreadCount == 0 {
			err = fmt.Errorf("dispatch %s has no unread messages", dispatch.ID)
		}
	}
	var session *protocol.Session
	if err == nil {
		session = d.store.Get(dispatch.SessionID)
		if session == nil {
			err = fmt.Errorf("delegated session %s is closed or remote", dispatch.SessionID)
		} else if session.State != protocol.SessionStateIdle && session.State != protocol.SessionStateWaitingInput {
			err = fmt.Errorf("delegated session %s is %s, not idle or waiting for input", dispatch.SessionID, session.State)
		}
	}
	if err == nil {
		err = d.ptyBackend.Input(context.Background(), dispatch.SessionID, []byte(dispatchWakePrompt))
	}
	if err == nil {
		time.Sleep(100 * time.Millisecond)
		err = d.ptyBackend.Input(context.Background(), dispatch.SessionID, []byte{'\r'})
	}
	result := protocol.WakeDispatchAgentResultMessage{
		Event:      protocol.EventWakeDispatchAgentResult,
		DispatchID: dispatchID,
		RequestID:  msg.RequestID,
		Success:    err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}
