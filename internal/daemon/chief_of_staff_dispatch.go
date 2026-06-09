package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

const closedDispatchStatus = "closed"

func chiefOfStaffDispatchPrompt(brief string) string {
	return strings.TrimSpace(brief) + `

---
This task is tracked by the chief of staff in attn.
Send a concise update when you reach a meaningful milestone, need input, or finish:

    "$ATTN_WRAPPER_PATH" dispatch report --message "<update>"

For a longer update, write it to a file and use ` + "`--file <path>`" + `.
When work is blocked, ready for review, completed, or failed, attach structured
coordination fields with ` + "`--coordination-file <json>`" + `. Use
` + "`dispatch status`" + ` to read a chief's durable response to a decision request.
Continue the assigned work after reporting unless you are blocked or finished.`
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
	if session := d.store.Get(dispatch.SessionID); session != nil {
		decorated.Status = string(session.State)
		decorated.StatusSince = session.StateSince
	} else {
		decorated.Status = closedDispatchStatus
		decorated.StatusSince = ""
	}
	return &decorated
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
	decorated := d.decorateChiefOfStaffDispatch(dispatch)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                   true,
		ChiefOfStaffDispatch: decorated,
	})
	d.broadcastChiefOfStaffDispatchesUpdated()
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
