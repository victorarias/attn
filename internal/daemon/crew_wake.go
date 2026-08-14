package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

// Wake: a member's day starts at launch. The daemon claims the binding, then
// spawns a session in the member's own cwd, with its awareness dirs and its
// priming — charter, the freshest letter, and the verbs of its own home.
//
// The claim happens before the spawn, the way a garden dispatch is recorded
// before one: the launching wrapper asks `crew_prime` for what to inject, and
// that answer is the binding. A wake that cannot bind never spawns.
//
// A member that is already awake is not woken twice. Two agents with the same
// identity never run at once, so the result names the live day and the caller
// focuses it — a refusal here would make the sidebar's one action fail exactly
// when the member is present.

// crewWakeAgent is the harness a wake launches when the caller names none. The
// crew simulation has run on Claude Code since 2026-08-06; `--agent` picks
// another, and a member is plain markdown so any harness can live in one.
const crewWakeAgent = "claude"

// crewWakeModel is what a member wakes on, hardcoded on purpose. A member's
// session is one Victor drives himself and those run Fable; a per-member model
// knob is a quiet way to end up with a member subtly wrong in a way only a
// spirit-read of its prose catches. It names a Claude model, so it is pinned
// only for the default harness — `--agent codex` still picks that harness's own
// default.
const crewWakeModel = "claude-fable-5"

// crewWakeModelPin is the pin as a spawn field: set for the default harness,
// nil for any other, so it can be applied by every path that starts a member's
// day without each one restating the rule.
func crewWakeModelPin(agent string) *string {
	if strings.TrimSpace(strings.ToLower(agent)) != crewWakeAgent {
		return nil
	}
	return protocol.Ptr(crewWakeModel)
}

// crewWakePrompt is the first thing a woken member is asked to do. Without it a
// member launches primed and silent, and the user has to open the session to
// find out anybody is there.
const crewWakePrompt = "You have been woken for today. Orient from your charter and your predecessor's letter, verify anything load-bearing they left you, then greet Victor in a few lines: who you are, what you were left with, what you believe the current state is, and what you would do next."

// crewWorkspaceID is a member's durable workspace — one per member, reused by
// every day. A workspace per wake would litter the sidebar with a new group
// each morning.
func crewWorkspaceID(memberID string) string { return "workspace-crew-" + memberID }

// crewMember reads one member by id, behind the fence.
func (d *Daemon) crewMember(name string) (crew.Member, docstore.Document, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	member, ok := crew.Resolve(name, members)
	if !ok {
		return crew.Member{}, docstore.Document{}, fmt.Errorf("no crew member %q is registered; `attn crew list` names the roster", name)
	}
	return member, docs[member.ID], nil
}

// crewLaunchDir is where a member's sessions launch: its recorded cwd, or its
// own home when nobody recorded one. The home always exists — it is what made
// the member — so a wake never fails for want of a directory, and a cwd that
// has since moved is named rather than silently swapped.
func crewLaunchDir(member crew.Member) (string, error) {
	dir := strings.TrimSpace(member.CWD)
	if dir == "" {
		return member.HomeDir, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("%s launches in %s, which is not there (%v); `attn crew set %[1]s --cwd <dir>` moves it", member.ID, dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s launches in %s, which is not a directory; `attn crew set %[1]s --cwd <dir>` moves it", member.ID, dir)
	}
	return dir, nil
}

// crewPriming composes what a member's launch injects, reading the prose off
// the home each time: the files are canonical, so a charter edited between two
// wakes takes effect at the next one with nothing to invalidate.
func (d *Daemon) crewPriming(member crew.Member) crew.Priming {
	priming := crew.Priming{
		Member:        member.ID,
		HomeDir:       member.HomeDir,
		CharterPath:   member.CharterPath,
		CWD:           member.CWD,
		AwarenessDirs: member.AwarenessDirs,
	}
	if charter, err := os.ReadFile(member.CharterPath); err == nil {
		priming.Charter = string(charter)
	} else if !os.IsNotExist(err) {
		d.logf("crew: reading %s's charter at %s: %v", member.ID, member.CharterPath, err)
	}

	handoffsDir := filepath.Join(member.HomeDir, crew.HandoffsDirName)
	entries, err := os.ReadDir(handoffsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			d.logf("crew: reading %s's handoffs at %s: %v", member.ID, handoffsDir, err)
		}
		return priming
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	if len(names) == 0 {
		return priming
	}
	priming.HandoffName = names[0]
	priming.OlderHandoffs = names[1:]
	if letter, err := os.ReadFile(filepath.Join(handoffsDir, names[0])); err == nil {
		priming.Handoff = string(letter)
	} else {
		d.logf("crew: reading %s's freshest handoff %s: %v", member.ID, names[0], err)
	}
	return priming
}

// IPC handlers.

func (d *Daemon) handleCrewWake(conn net.Conn, msg *protocol.CrewWakeMessage) {
	result, err := d.crewWake(strings.TrimSpace(msg.Member), strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Agent))))
	if err != nil {
		d.sendCrewError(conn, "wake", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewWakeResult: result})
}

// handleCrewWakeWS is the sidebar's way in: one command, and the woken session
// reaches the UI through the normal session and layout broadcasts. A refusal —
// no such member, a cwd that moved, an outpost — comes back as the error the
// caller shows, never as a click that did nothing.
func (d *Daemon) handleCrewWakeWS(client *wsClient, msg *protocol.CrewWakeMessage) {
	result, err := d.crewWake(strings.TrimSpace(msg.Member), strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Agent))))
	response := protocol.CrewWakeResultMessage{
		Event:     protocol.EventCrewWakeResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = protocol.Ptr(result.Member)
		response.SessionID = protocol.Ptr(result.SessionID)
		response.WorkspaceID = protocol.Ptr(result.WorkspaceID)
		if result.AlreadyAwake {
			response.AlreadyAwake = protocol.Ptr(true)
		}
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewWake(name, agent string) (*protocol.CrewWakeResult, error) {
	member, _, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	if d.crewBindingLive(member) {
		awake := &protocol.CrewWakeResult{
			Member:       member.ID,
			SessionID:    member.BindingSession,
			AlreadyAwake: true,
		}
		if session := d.store.Get(member.BindingSession); session != nil {
			awake.WorkspaceID = session.WorkspaceID
		}
		return awake, nil
	}
	if agent == "" {
		agent = crewWakeAgent
	}
	if driver := agentdriver.Get(agent); driver == nil {
		if _, ok := d.ensurePluginRegistry().driver(agent); !ok {
			return nil, fmt.Errorf("agent %q is not available", agent)
		}
	}
	directory, err := crewLaunchDir(member)
	if err != nil {
		return nil, err
	}

	sessionID := uuid.NewString()
	// Claimed before the spawn, because the launch reads it: the wrapper asks
	// `crew_prime` for what to inject and the binding is what answers. A member
	// whose day is claimed by a launch that then fails is released below.
	if _, err := d.claimCrewBinding(member.ID, sessionID); err != nil {
		return nil, err
	}

	workspaceID := crewWorkspaceID(member.ID)
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     member.ID,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			d.releaseCrewBindingIfSession(sessionID)
			return nil, fmt.Errorf("create %s's workspace", member.ID)
		}
	}
	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-" + sessionID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(member.ID),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		d.releaseCrewBindingIfSession(sessionID)
		return nil, fmt.Errorf("create %s's pane: %w", member.ID, err)
	}

	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		WorkspaceID:   workspaceID,
		Agent:         agent,
		Model:         crewWakeModelPin(agent),
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(member.ID),
		InitialPrompt: protocol.Ptr(crewWakePrompt),
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		d.releaseCrewBindingIfSession(sessionID)
		return nil, fmt.Errorf("wake %s: %w", member.ID, err)
	}
	d.logf("crew: woke %s in session %s at %s", member.ID, sessionID, directory)
	return &protocol.CrewWakeResult{
		Member:      member.ID,
		SessionID:   sessionID,
		WorkspaceID: workspaceID,
	}, nil
}

func (d *Daemon) handleCrewPrime(conn net.Conn, msg *protocol.CrewPrimeMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "prime", err)
		return
	}
	result := &protocol.CrewPrimeResult{AwarenessDirs: []string{}}
	member, block, bound := d.crewPrimeForSession(sessionID)
	if bound {
		result.Member = protocol.Ptr(member.ID)
		result.Guidance = protocol.Ptr(block)
		result.AwarenessDirs = append(result.AwarenessDirs, member.AwarenessDirs...)
		result.PrimingBytes = len(block)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewPrimeResult: result})
}

// crewPrimeForSession composes what a launching session must be injected with
// to be its member, and logs the size — the budget receipt the wake limit and
// the heartbeat are waiting on. One line per injection, naming what each part
// cost: grep `crew: priming`. Reports false for a session that is nobody.
func (d *Daemon) crewPrimeForSession(sessionID string) (crew.Member, string, bool) {
	if sessionID == "" || d.store == nil {
		return crew.Member{}, "", false
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, "", false
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to prime %s: %v", sessionID, err)
		}
		return crew.Member{}, "", false
	}
	for _, member := range members {
		if member.BindingSession != sessionID {
			continue
		}
		priming := d.crewPriming(member)
		block := priming.Block()
		handoff := priming.HandoffName
		if handoff == "" {
			handoff = "(none)"
		}
		d.logf("crew: priming %s for session %s: %d bytes (charter %d, handoff %s %d, older %d)",
			member.ID, sessionID, len(block), len(priming.Charter),
			handoff, len(priming.Handoff), len(priming.OlderHandoffs))
		return member, block, true
	}
	return crew.Member{}, "", false
}

func (d *Daemon) handleCrewSet(conn net.Conn, msg *protocol.CrewSetMessage) {
	member, doc, err := d.crewMember(strings.TrimSpace(msg.Member))
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	if msg.Cwd != nil {
		cwd, err := resolveCrewDir(*msg.Cwd)
		if err != nil {
			d.sendCrewError(conn, "set", err)
			return
		}
		member.CWD = cwd
	}
	// The way out arrives as its own flag: an empty list marshals away, so an
	// empty AwarenessDirs is indistinguishable from "leave it alone" on the wire.
	if protocol.Deref(msg.ClearAwarenessDirs) {
		member.AwarenessDirs = nil
	} else if msg.AwarenessDirs != nil {
		dirs := make([]string, 0, len(msg.AwarenessDirs))
		for _, dir := range msg.AwarenessDirs {
			resolved, err := resolveCrewDir(dir)
			if err != nil {
				d.sendCrewError(conn, "set", err)
				return
			}
			if resolved != "" {
				dirs = append(dirs, resolved)
			}
		}
		member.AwarenessDirs = dirs
	}
	if err := d.writeCrewMember(*schema, member, doc.Rev); err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.sendGardenResponse(conn, protocol.Response{
		Ok:            true,
		CrewSetResult: &protocol.CrewSetResult{Member: d.crewMemberWire(member)},
	})
}

// resolveCrewDir makes a directory absolute and insists it exists. A recorded
// cwd that is not there only fails at the next wake, which is the wrong end of
// the day to learn about a typo. An empty value clears the field.
func resolveCrewDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%s is not a usable path: %w", dir, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("%s is not there", absolute)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absolute)
	}
	return absolute, nil
}
