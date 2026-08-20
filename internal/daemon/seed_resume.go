package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// Seed "Resume" reopens the agent that tends a seed. The daemon owns the whole
// composite — validate, register workspace, add pane, spawn, roll back on
// failure — mirroring delegate(). The frontend sends one command and focuses
// the result; the session and pane reach it through the normal
// session_registered / workspace_layout_updated broadcasts.
//
// The inputs all come from the daemon. A dispatch record is authoritative when
// one exists; a seed-owned identity is the fallback for pre-garden and external
// conversations attn did not launch.
//
// Resume writes NOTHING to the seed: reopening a delegate is not a note about
// its work, and the seed's lifecycle is only ever moved by a deliberate verb.

type seedResumeOutcome struct {
	SessionID      string
	WorkspaceID    string
	AlreadyRunning bool
}

// resumeSeed reopens the conversation attached to seedID. A surviving tender's
// session id stays stable; an external conversation uses its agent-native id as
// the new container id. A tracked container is focused, not spawned twice.
func (d *Daemon) resumeSeed(seedID string) (*seedResumeOutcome, error) {
	seedID = strings.TrimSpace(seedID)
	if seedID == "" {
		return nil, fmt.Errorf("seed_id is required")
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(seed.TenderSession)
	if sessionID != "" {
		if existing := d.store.Get(sessionID); existing != nil {
			// The tender is still tracked — focus it instead of spawning a duplicate.
			// Re-spawning its id would poison the local store; a dead-but-recoverable
			// pane revives itself via the attach path on mount.
			return &seedResumeOutcome{
				SessionID:      existing.ID,
				WorkspaceID:    existing.WorkspaceID,
				AlreadyRunning: true,
			}, nil
		}
	}

	dispatch, hasDispatch := d.gardenDispatch(sessionID)
	cwd, agent, resumeID := "", "", ""
	seedFallback := false
	if hasDispatch {
		cwd = strings.TrimSpace(dispatch.Cwd)
		agent = strings.TrimSpace(dispatch.Agent)
	} else {
		resumeID, cwd, agent = strings.TrimSpace(seed.ResumeSessionID), strings.TrimSpace(seed.ResumeCwd), strings.TrimSpace(seed.ResumeAgent)
		if resumeID == "" || cwd == "" || agent == "" {
			if sessionID == "" {
				return nil, fmt.Errorf("%s is untended — there is nobody to reopen; `attn seed notes %s` has its log", seedID, seedID)
			}
			return nil, fmt.Errorf("%s was tended by session %s, which attn did not launch — nothing to reopen", seedID, sessionID)
		}
		seedFallback = true
		// An external conversation has no attn container id to preserve. Reusing
		// its native id gives repeat Resume clicks one stable container to focus.
		if sessionID == "" {
			sessionID = resumeID
		}
		if existing := d.store.Get(sessionID); existing != nil {
			return &seedResumeOutcome{
				SessionID: existing.ID, WorkspaceID: existing.WorkspaceID, AlreadyRunning: true,
			}, nil
		}
	}
	if cwd == "" || agent == "" {
		return nil, fmt.Errorf("%s has no agent session to resume", seedID)
	}

	// A dispatch-backed resume lets the spawn pipeline resolve its mirrored id.
	// A seed-backed resume passes it directly; ResumePicker still provides the
	// cwd picker if the driver says the conversation is gone.
	var directResume *string
	if seedFallback {
		directResume = protocol.Ptr(resumeID)
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
	rollback := d.newDelegationRollback()
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     seed.Title,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("create resume workspace")
		}
		rollback.onWorkspaceCreated(workspaceID)
	}

	paneID := "pane-" + sessionID
	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(seed.Title),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("create resume pane: %w", err))
	}
	rollback.onPaneCreated(sessionID)

	// ResumePicker (not a passed ResumeSessionID) keeps handleSpawnSession the single
	// resume-id resolver: its resume branch resolves the mirrored id for this
	// session, downgrading to the cwd-scoped picker when the transcript is gone.
	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              sessionID,
		Cwd:             directory,
		WorkspaceID:     workspaceID,
		Agent:           agent,
		Cols:            80,
		Rows:            24,
		Label:           protocol.Ptr(seed.Title),
		ResumePicker:    protocol.Ptr(true),
		ResumeSessionID: directResume,
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("spawn resume session: %w", err))
	}

	if session := d.store.Get(sessionID); session == nil {
		return nil, rollback.fail(fmt.Errorf("resume session was not persisted"))
	}
	if seedFallback {
		if err := d.recordGardenDispatch(sessionID, seed.ID, directory, agent, false); err != nil {
			d.logf("resume: reopened %s but could not bind its fallback session %s: %v", seed.ID, sessionID, err)
		} else {
			d.rememberDispatchResume(sessionID, resumeID)
		}
	}

	d.logf("resume: reopened seed %q as session %s in %s", seedID, sessionID, directory)
	return &seedResumeOutcome{SessionID: sessionID, WorkspaceID: workspaceID}, nil
}

// handleSeedResume runs the resume composite and replies with a
// seed_resume_result, correlated by request_id. The reply carries the session to
// focus; the session and pane themselves reach the UI through the normal
// broadcasts.
func (d *Daemon) handleSeedResume(client *wsClient, msg *protocol.SeedResumeMessage) {
	requestID := protocol.Deref(msg.RequestID)
	outcome, err := d.resumeSeed(msg.SeedID)
	response := protocol.SeedResumeResultMessage{
		Event:     protocol.EventSeedResumeResult,
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
