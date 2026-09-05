package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// Measured 2026-08-14: `claude --session-id <id>` refuses a second launch under an id
// it already used, so the resume-preserving reload cannot serve the nap.

var crewNapPrompt = prompts.RenderText("crew", "successor", prompts.Values{})

func (d *Daemon) transferCrewBinding(memberID, from, to string) error {
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.BindingSession != from {
			return false, fmt.Errorf("%s's day is no longer session %s; nothing was moved", crew.DisplayName(member.ID), shortSessionID(from))
		}
		member.BindingSession = to
		// The filed-letter fields are left alone: a rollback that puts the binding
		// back finds them still true.
		return true, nil
	})
	if err != nil {
		return err
	}
	if err := d.migrateCrewTicketIdentity(memberID, from, to); err != nil {
		_, rollbackErr := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
			if member.BindingSession != to {
				return false, nil
			}
			member.BindingSession = from
			return true, nil
		})
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("restore %s's binding after ticket migration refusal: %w", crew.DisplayName(memberID), rollbackErr))
		}
		return err
	}
	d.publishFact(FactCrewBound, memberID, nil)
	d.logf("crew: %s's binding moved from session %s to %s", crew.DisplayName(memberID), from, to)
	return nil
}

func (d *Daemon) crewMemberForSession(sessionID string) (crew.Member, bool) {
	if sessionID == "" || d.store == nil {
		return crew.Member{}, false
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for session %s: %v", sessionID, err)
		}
		return crew.Member{}, false
	}
	for _, member := range members {
		if member.BindingSession == sessionID {
			return member, true
		}
	}
	return crew.Member{}, false
}

func (d *Daemon) crewHandoff(sessionID, note string, retry bool, close protocol.CrewDayClose) (*protocol.CrewHandoffResult, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, fmt.Errorf("a handoff is filed by the session living the day; none was named (set ATTN_SESSION_ID or pass --session)")
	}
	member, bound := d.crewMemberForSession(sessionID)
	if !bound {
		return nil, fmt.Errorf("this session is not living a crew member's day, so it has no day-line to close. A crew handoff is a member's own letter to its successor; the note you write for whoever tends a piece of work next is `attn seed note <id> -m \"…\" --handoff`")
	}
	path, err := d.crewLetterForHandoff(member, sessionID, note, retry)
	if err != nil {
		return nil, err
	}

	if retry && close == "" {
		close = protocol.CrewDayCloseNap
	}

	result := &protocol.CrewHandoffResult{Member: member.ID, Path: path}
	teardown, err := d.prepareSessionTeardown(sessionID)
	if err != nil {
		return nil, fmt.Errorf("prepare %s's day to close: %w", crew.DisplayName(member.ID), err)
	}
	if d.crewDayEndsHere(close, time.Now()) {
		d.closeNappedSession(sessionID, teardown)
		d.logf("crew: %s went to sleep — session %s ended and nobody was woken behind it", crew.DisplayName(member.ID), sessionID)
		result.Outcome = protocol.Ptr(protocol.CrewDayCloseSleep)
		return result, nil
	}
	newSessionID, err := d.crewNap(member, sessionID, teardown)
	if err != nil {
		d.logf("crew: %s's letter is filed but the nap did not run: %v", crew.DisplayName(member.ID), err)
		result.NapError = protocol.Ptr(err.Error())
		return result, nil
	}
	result.SessionID = protocol.Ptr(newSessionID)
	result.Outcome = protocol.Ptr(protocol.CrewDayCloseNap)
	return result, nil
}

func (d *Daemon) crewDayEndsHere(close protocol.CrewDayClose, now time.Time) bool {
	switch close {
	case protocol.CrewDayCloseSleep:
		return true
	case protocol.CrewDayCloseNap:
		return false
	}
	return d.UserAwayFor(now) >= d.crewAwayLimit()
}

func (d *Daemon) crewLetterForHandoff(member crew.Member, sessionID, note string, retry bool) (string, error) {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return "", err
	}
	filed, hasFiled := member.FiledLetterFor(sessionID)
	if retry {
		if !hasFiled {
			return "", fmt.Errorf("%s's day has filed no letter yet, so there is no turnover to retry — write one with `attn handoff -m \"<your letter>\"`", crew.DisplayName(member.ID))
		}
		if err := d.validateCrewLetterPath(member, filed); err != nil {
			return "", err
		}
		if _, err := os.Stat(filed); err != nil {
			return "", fmt.Errorf("%s's filed letter is recorded at %s but is not readable there (%v); file this day's letter again with `attn handoff -m \"<your letter>\"`", crew.DisplayName(member.ID), filed, err)
		}
		d.logf("crew: %s is retrying its turnover with the letter already filed at %s", crew.DisplayName(member.ID), filed)
		return filed, nil
	}
	if err := crew.ValidateHandoffNote(note); err != nil {
		return "", err
	}
	if _, err := d.validateCrewHandoffsDir(member); err != nil {
		return "", err
	}
	path, err := crew.FileHandoff(member.HomeDir, member.ID, note, time.Now())
	if err != nil {
		if errors.Is(err, crew.ErrHandoffExists) && hasFiled {
			return "", fmt.Errorf("%s's letter for this minute is already filed at %s — if the turnover is what failed, `attn handoff --retry` runs it against that letter; if this is a correction, file it as its own letter a minute from now", crew.DisplayName(member.ID), filed)
		}
		return "", err
	}
	d.logf("crew: %s filed a letter at %s (%d bytes)", crew.DisplayName(member.ID), path, len(note))
	d.recordCrewLetter(member.ID, sessionID, path)
	return path, nil
}

// Best-effort: the letter is on disk either way, and a failed registry write
// must not fail the nap.
func (d *Daemon) recordCrewLetter(memberID, sessionID, path string) {
	if _, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.BindingSession != sessionID {
			return false, nil
		}
		member.LetterPath = path
		member.LetterSession = sessionID
		return true, nil
	}); err != nil {
		d.logf("crew: recording %s's filed letter at %s: %v", crew.DisplayName(memberID), path, err)
	}
}

func (d *Daemon) crewNap(member crew.Member, oldSessionID string, teardown *sessionTeardown) (newSessionID string, err error) {
	committed := false
	defer func() {
		if !committed {
			d.cancelSessionTeardown(oldSessionID)
		}
	}()
	if err := d.validateCrewMemberPaths(member); err != nil {
		return "", err
	}
	if err := d.validateCrewWorkDirs(member); err != nil {
		return "", err
	}
	session := d.store.Get(oldSessionID)
	if session == nil {
		return "", fmt.Errorf("session %s is no longer here", shortSessionID(oldSessionID))
	}
	// Checked before anything is spawned, so a member past its allowance keeps the
	// day it has rather than losing it to a refused wake.
	now := time.Now()
	if d.UserAwayFor(now) >= d.crewAwayLimit() {
		if err := d.chargeAutonomousWake(member.ID, now); err != nil {
			return "", err
		}
	}
	spawnMsg, policy := d.crewNapSpawn(member, session)
	launchDir, err := d.resolveCrewWorkDir(spawnMsg.Cwd)
	if err != nil {
		return "", fmt.Errorf("wake %s's successor in %s: %w", crew.DisplayName(member.ID), spawnMsg.Cwd, err)
	}
	spawnMsg.Cwd = launchDir
	newSessionID = spawnMsg.ID

	// Before the spawn: the launching wrapper asks `crew_prime` for what to inject,
	// and the binding is what answers. One write, so the member is never unbound.
	if err := d.transferCrewBinding(member.ID, oldSessionID, newSessionID); err != nil {
		return "", err
	}
	undoBinding := func() {
		if err := d.transferCrewBinding(member.ID, newSessionID, oldSessionID); err != nil {
			d.logf("crew: could not give %s's binding back to session %s: %v", crew.DisplayName(member.ID), oldSessionID, err)
		}
	}

	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: spawnMsg.WorkspaceID,
		PaneID:      protocol.Ptr("pane-" + newSessionID),
		SessionID:   newSessionID,
		Title:       protocol.Ptr(crew.DisplayName(member.ID)),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		undoBinding()
		return "", fmt.Errorf("create %s's next pane: %w", crew.DisplayName(member.ID), err)
	}

	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		d.removeWorkspaceLayoutPaneForSession(newSessionID)
		undoBinding()
		return "", fmt.Errorf("wake %s's successor: %w", crew.DisplayName(member.ID), rejection.reason())
	}

	// releaseCrewBindingIfSession only clears a binding pointing at the id being
	// closed, so this releases nothing the member still needs.
	d.closeNappedSession(oldSessionID, teardown)
	committed = true
	d.logf("crew: %s napped — session %s ended, session %s is the new day", crew.DisplayName(member.ID), oldSessionID, newSessionID)
	return newSessionID, nil
}

func (d *Daemon) crewNapSpawn(member crew.Member, session *protocol.Session) (*protocol.SpawnSessionMessage, internalSpawnPolicy) {
	cols, rows := d.crewSessionGeometry(session.ID)
	var spawnMsg *protocol.SpawnSessionMessage
	var policy internalSpawnPolicy
	if intent, ok := d.store.LaunchIntent(session.ID); ok {
		spawnMsg, policy = buildStoredIntentSpawn(session, intent, cols, rows)
	} else {
		d.logf("crew: no launch intent for %s's closing day; the successor launches with defaults", crew.DisplayName(member.ID))
		spawnMsg = &protocol.SpawnSessionMessage{
			Cmd:         protocol.CmdSpawnSession,
			Cwd:         session.Directory,
			Agent:       string(session.Agent),
			WorkspaceID: session.WorkspaceID,
			Label:       protocol.Ptr(crew.DisplayName(member.ID)),
			Cols:        cols,
			Rows:        rows,
		}
	}
	spawnMsg.ID = uuid.NewString()
	spawnMsg.Label = protocol.Ptr(crew.DisplayName(member.ID))
	spawnMsg.InitialPrompt = protocol.Ptr(crewNapPrompt)
	spawnMsg.Model = d.crewWakeModel(member, spawnMsg.Agent)
	// A resume would carry the closed day's transcript into the new one.
	spawnMsg.ResumeSessionID = nil
	spawnMsg.ResumeConversationFile = nil
	if strings.TrimSpace(spawnMsg.WorkspaceID) == "" {
		spawnMsg.WorkspaceID = crewWorkspaceID(member.ID)
	}
	if strings.TrimSpace(spawnMsg.Cwd) == "" {
		spawnMsg.Cwd = member.HomeDir
	}
	return spawnMsg, policy
}

func (d *Daemon) crewSessionGeometry(sessionID string) (int, int) {
	cols, rows := 80, 24
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return cols, rows
	}
	info, err := provider.SessionInfo(context.Background(), sessionID)
	if err != nil {
		return cols, rows
	}
	if info.Cols > 0 {
		cols = int(info.Cols)
	}
	if info.Rows > 0 {
		rows = int(info.Rows)
	}
	return cols, rows
}

func (d *Daemon) closeNappedSession(sessionID string, teardown *sessionTeardown) {
	d.commitSessionUnregister(sessionID, store.SessionClose{By: store.SessionClosedByUser, Reason: "crew member put to sleep"})
	if teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.dissociateSessionFromWorkspace(teardown.session.ID)
		d.removeWorkspaceLayoutPaneForSession(teardown.session.ID)
		d.publishFact(FactSessionTerminated, teardown.session.ID, nil)
	}
	d.terminateSessionAsync(sessionID, syscall.SIGTERM, teardown)
}

func (d *Daemon) handleCrewHandoff(conn net.Conn, msg *protocol.CrewHandoffMessage) {
	result, err := d.crewHandoff(strings.TrimSpace(msg.SessionID), msg.Note, protocol.Deref(msg.Retry), protocol.Deref(msg.Close))
	if err != nil {
		d.sendCrewError(conn, "handoff", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewHandoffResult: result})
}
