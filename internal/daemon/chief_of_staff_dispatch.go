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

For a longer update, write it to a file and use ` + "`--file <path>`" + `. Continue the assigned work after reporting unless you are blocked or finished.`
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
	if session := d.store.Get(dispatch.SessionID); session != nil {
		decorated.Status = string(session.State)
		decorated.StatusSince = session.StateSince
	} else {
		decorated.Status = closedDispatchStatus
		decorated.StatusSince = ""
	}
	return &decorated
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
	dispatch, err := d.store.UpdateChiefOfStaffDispatchReport(sourceSessionID, report)
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
