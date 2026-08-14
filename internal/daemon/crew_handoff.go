package daemon

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Handoff and the nap: how a member's day ends and the next one starts, as one
// motion. The member writes the letter; attn files it append-only, then
// replaces the day's session with a fresh one primed by that letter.
//
// The nap is a replacement, not a resume. Measured 2026-08-14: `claude
// --session-id <id>` refuses a second launch under an id it has already used
// ("Session ID … is already in use"), which is why the daemon's existing reload
// (internal/daemon/reload.go) is resume-preserving — and resuming is the one
// thing the nap must not do, because the whole point is that the member's
// letter, not a transcript or a compaction summary, is the thread into the new
// day. So the new day is a new session, spawned into the same workspace with
// the same launch params, and the old one is closed behind it.
//
// The binding does not blink. It moves from the old session to the new one in a
// single registry write against the revision it was read at: there is no moment
// where the member is unbound, so no other wake can slip into a gap and produce
// a second copy. Every refusal path leaves the old session running and still
// holding the binding — a member is never torn down with its letter unfiled,
// and a letter already on disk is never undone by what fails after it.

// crewNapPrompt is what the successor is asked first. It differs from the cold
// wake's prompt in what it can assume: the letter it is holding was written
// minutes ago by the session it replaced, so there is a live thread to pick up
// rather than a night's worth of drift to verify.
const crewNapPrompt = "Your predecessor just closed their day and left you the letter above. Pick the thread up: orient from it, verify anything load-bearing that may have moved, then tell Victor in a few lines who you are, where things stand, and what you are doing next."

// transferCrewBinding moves a member's binding from one session to another in
// one write, so the registry never shows the member unbound. It refuses unless
// `from` is the binding it is asked to move, which is what makes it safe to
// call from the nap's rollback: a binding that has since moved on is not
// quietly stolen back.
func (d *Daemon) transferCrewBinding(memberID, from, to string) error {
	if err := d.requireHome(crew.Surface); err != nil {
		return err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return err
		}
		member, ok := crew.Resolve(memberID, members)
		if !ok {
			return fmt.Errorf("no crew member %q is registered", memberID)
		}
		if member.BindingSession != from {
			return fmt.Errorf("%s's day is no longer session %s; nothing was moved", member.ID, shortSessionID(from))
		}
		member.BindingSession = to
		err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {
			d.publishFact(FactCrewBound, member.ID, nil)
			d.logf("crew: %s's binding moved from session %s to %s", member.ID, from, to)
			return nil
		}
		if !docstore.IsConflict(err) {
			return err
		}
	}
	return fmt.Errorf("the registry record for %q was rewritten under all %d attempts to move its binding; try again", memberID, attempts)
}

// crewMemberForSession answers which member a session is living, judged the
// same way every other crew read judges it.
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

// crewHandoff files the letter and runs the nap. The letter is filed first and
// is never rolled back: it is the member's honest closure, and a nap that could
// not run is a day that did not start, not a letter that was not written.
func (d *Daemon) crewHandoff(sessionID, note string) (*protocol.CrewHandoffResult, error) {
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
	if err := crew.ValidateHandoffNote(note); err != nil {
		return nil, err
	}
	path, err := crew.FileHandoff(member.HomeDir, member.ID, note, time.Now())
	if err != nil {
		return nil, err
	}
	d.logf("crew: %s filed a letter at %s (%d bytes)", member.ID, path, len(note))

	result := &protocol.CrewHandoffResult{Member: member.ID, Path: path}
	newSessionID, err := d.crewNap(member, sessionID)
	if err != nil {
		// The letter is filed and the day's session is untouched: say why nobody
		// was woken and leave the member awake where it is.
		d.logf("crew: %s's letter is filed but the nap did not run: %v", member.ID, err)
		result.NapError = protocol.Ptr(err.Error())
		return result, nil
	}
	result.SessionID = protocol.Ptr(newSessionID)
	return result, nil
}

// crewNap starts the member's next day in place of the one that just ended: a
// fresh session in the same workspace, carrying the closed day's launch params,
// primed by the letter that was just filed. The old session is closed only once
// the new one is running.
func (d *Daemon) crewNap(member crew.Member, oldSessionID string) (string, error) {
	session := d.store.Get(oldSessionID)
	if session == nil {
		return "", fmt.Errorf("session %s is no longer here", shortSessionID(oldSessionID))
	}
	spawnMsg, policy := d.crewNapSpawn(member, session)
	newSessionID := spawnMsg.ID

	// Moved before the spawn for the same reason the wake claims before it
	// spawns: the launching wrapper asks `crew_prime` for what to inject, and
	// the binding is what answers. One write, so the member is never unbound.
	if err := d.transferCrewBinding(member.ID, oldSessionID, newSessionID); err != nil {
		return "", err
	}
	undoBinding := func() {
		if err := d.transferCrewBinding(member.ID, newSessionID, oldSessionID); err != nil {
			d.logf("crew: could not give %s's binding back to session %s: %v", member.ID, oldSessionID, err)
		}
	}

	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: spawnMsg.WorkspaceID,
		PaneID:      protocol.Ptr("pane-" + newSessionID),
		SessionID:   newSessionID,
		Title:       protocol.Ptr(member.ID),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		undoBinding()
		return "", fmt.Errorf("create %s's next pane: %w", member.ID, err)
	}

	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		d.removeWorkspaceLayoutPaneForSession(newSessionID)
		undoBinding()
		return "", fmt.Errorf("wake %s's successor: %w", member.ID, rejection.reason())
	}

	// The new day is running, so the old one can end. Closing it releases
	// nothing the member still needs: the binding already names the new session,
	// and releaseCrewBindingIfSession only clears a binding pointing at the id
	// being closed.
	d.closeNappedSession(oldSessionID)
	d.logf("crew: %s napped — session %s ended, session %s is the new day", member.ID, oldSessionID, newSessionID)
	return newSessionID, nil
}

// crewNapSpawn builds the successor's launch. The closed day's launch intent is
// the authority for how the member runs — yolo, executable, model, effort,
// approval route — so a member woken unattended does not silently come back
// attended at the first nap. Never a resume: a fresh conversation is the point.
func (d *Daemon) crewNapSpawn(member crew.Member, session *protocol.Session) (*protocol.SpawnSessionMessage, internalSpawnPolicy) {
	cols, rows := d.crewSessionGeometry(session.ID)
	var spawnMsg *protocol.SpawnSessionMessage
	var policy internalSpawnPolicy
	if intent, ok := d.store.LaunchIntent(session.ID); ok {
		spawnMsg, policy = buildStoredIntentSpawn(session, intent, cols, rows)
	} else {
		d.logf("crew: no launch intent for %s's closing day; the successor launches with defaults", member.ID)
		spawnMsg = &protocol.SpawnSessionMessage{
			Cmd:         protocol.CmdSpawnSession,
			Cwd:         session.Directory,
			Agent:       string(session.Agent),
			WorkspaceID: session.WorkspaceID,
			Label:       protocol.Ptr(member.ID),
			Cols:        cols,
			Rows:        rows,
		}
	}
	spawnMsg.ID = uuid.NewString()
	spawnMsg.Label = protocol.Ptr(member.ID)
	spawnMsg.InitialPrompt = protocol.Ptr(crewNapPrompt)
	// A resume would carry the closed day's transcript into the new one, which
	// is the compaction nap this design exists to replace.
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

// crewSessionGeometry reads the closing day's terminal size so the successor
// comes back the same shape. The 80x24 fallback is the same one every spawn
// path uses when no live worker can be asked.
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

// closeNappedSession ends the day that just handed off. It is the app's own
// close path minus the client detach: terminate, forget, drop the pane, and say
// so on the wire, so the sidebar shows one member living one day rather than a
// dead pane beside a live one.
func (d *Daemon) closeNappedSession(sessionID string) {
	session := d.unregisterSession(sessionID, syscall.SIGTERM)
	if session == nil {
		return
	}
	d.publishSessionUnregistered(session)
	d.dissociateSessionFromWorkspace(session.ID)
	d.removeWorkspaceLayoutPaneForSession(session.ID)
	d.publishFact(FactSessionTerminated, session.ID, nil)
}

// IPC handlers.

func (d *Daemon) handleCrewHandoff(conn net.Conn, msg *protocol.CrewHandoffMessage) {
	result, err := d.crewHandoff(strings.TrimSpace(msg.SessionID), msg.Note)
	if err != nil {
		d.sendCrewError(conn, "handoff", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewHandoffResult: result})
}
