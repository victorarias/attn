package daemon

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

// Ticket "Resume" reopens the agent session bound to a ticket. The daemon owns the
// whole composite — validate, register workspace, add pane, spawn, roll back on
// failure — mirroring delegate(). It used to live in the frontend, which seeded a
// local session placeholder and then made two websocket round trips before reading
// its spawn args back out of the local store; any broadcast that landed in that
// window pruned the unprotected placeholder and the resume failed with "Session
// spawn arguments were not prepared" + a rollback. Moving the composite here removes
// that race: the daemon already owns every input (the ticket's cwd, agent, and
// mirrored resume id), so the frontend sends one command and focuses the result.
//
// The session/pane reach the UI through the normal session_registered /
// workspace_layout_updated broadcasts — exactly how a delegated session appears —
// so the frontend seeds nothing. Resume writes NOTHING to the ticket (no status, no
// event): attn's only self-authored status is `crashed`; the board informs, it never
// gates.

type ticketResumeOutcome struct {
	SessionID      string
	WorkspaceID    string
	AlreadyRunning bool
}

// resumeTicket reopens the agent bound to ticketID. It reuses the ticket's assignee
// as the session id so assignee == session stays the identity binding (which keeps
// the resumed agent's `attn ticket status` on the same ticket) and lets
// handleSpawnSession resolve the mirrored resume id for a precise resume. When the
// assignee is still a tracked session it is focused, not re-spawned (already_running).
func (d *Daemon) resumeTicket(ticketID string) (*ticketResumeOutcome, error) {
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return nil, fmt.Errorf("ticket_id is required")
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		return nil, err
	}
	if ticket == nil {
		return nil, fmt.Errorf("ticket not found: %s", ticketID)
	}
	cwd := strings.TrimSpace(ticket.Cwd)
	agent := strings.TrimSpace(ticket.LastAgentID)
	if cwd == "" || agent == "" {
		return nil, fmt.Errorf("ticket has no agent session to resume")
	}

	// Reuse the bound id (its assignee) so the daemon can resolve the mirrored
	// resume id; mint a fresh session only when there is no usable bound id
	// (unassigned, or the human "you").
	sessionID := strings.TrimSpace(ticket.Assignee)
	if sessionID == "" || sessionID == "you" {
		sessionID = uuid.NewString()
	} else if existing := d.store.Get(sessionID); existing != nil {
		// The bound session is still tracked — focus it instead of spawning a
		// duplicate. Re-spawning its id would poison the local store; a dead-but-
		// recoverable pane revives itself via the attach path on mount.
		return &ticketResumeOutcome{
			SessionID:      existing.ID,
			WorkspaceID:    existing.WorkspaceID,
			AlreadyRunning: true,
		}, nil
	}

	// A worktree may have been removed since the session closed — validate before
	// any side effects so a missing directory is a clean error, not a phantom
	// workspace left behind.
	directory, err := validateDelegationDirectory(cwd)
	if err != nil {
		return nil, err
	}

	// Register the workspace under the same id delegate() uses. Only unregister it on
	// rollback if this call created it — a re-register is idempotent and preserves a
	// stored rename (handleRegisterWorkspace's title guard), so it must survive.
	workspaceID := "workspace-" + sessionID
	createdWorkspaceID := ""
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     ticket.Title,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("create resume workspace")
		}
		createdWorkspaceID = workspaceID
	}

	paneID := "pane-" + sessionID
	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(ticket.Title),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		return nil, d.rollbackDelegation(createdWorkspaceID, "", fmt.Errorf("create resume pane: %w", err))
	}

	// ResumePicker (not a passed ResumeSessionID) keeps handleSpawnSession the single
	// resume-id resolver: its ticket-resume branch resolves the mirrored id for this
	// session, downgrading to the cwd-scoped picker when the transcript is gone.
	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:          protocol.CmdSpawnSession,
		ID:           sessionID,
		Cwd:          directory,
		WorkspaceID:  workspaceID,
		Agent:        agent,
		Cols:         80,
		Rows:         24,
		Label:        protocol.Ptr(ticket.Title),
		ResumePicker: protocol.Ptr(true),
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, "", fmt.Errorf("spawn resume session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, "", fmt.Errorf("resume session was not persisted"))
	}

	d.logf("resume: reopened ticket %q as session %s in %s", ticketID, sessionID, directory)
	return &ticketResumeOutcome{SessionID: sessionID, WorkspaceID: workspaceID}, nil
}

// handleTicketResume runs the resume composite and replies with a
// ticket_resume_result, correlated by request_id. Unlike the other ticket actions
// this reply carries a payload (the session to focus); the session and pane
// themselves reach the UI through the normal broadcasts.
func (d *Daemon) handleTicketResume(client *wsClient, msg *protocol.TicketResumeMessage) {
	requestID := protocol.Deref(msg.RequestID)
	outcome, err := d.resumeTicket(msg.TicketID)
	response := protocol.TicketResumeResultMessage{
		Event:     protocol.EventTicketResumeResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.SessionID = protocol.Ptr(outcome.SessionID)
		response.WorkspaceID = protocol.Ptr(outcome.WorkspaceID)
		if outcome.AlreadyRunning {
			response.AlreadyRunning = protocol.Ptr(true)
		}
	}
	d.sendToClient(client, response)
}
