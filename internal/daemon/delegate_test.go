package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func setupDelegationSource(t *testing.T, d *Daemon, backend *fakeSpawnBackend) (string, string, string) {
	t.Helper()
	return setupDelegationSourceAt(t, d, backend, t.TempDir())
}

func setupDelegationSourceAt(t *testing.T, d *Daemon, backend *fakeSpawnBackend, cwd string) (string, string, string) {
	t.Helper()
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-source"
	sessionID := "session-source"

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Source workspace",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-source"),
		SessionID:   sessionID,
		Title:       protocol.Ptr("Source"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-source", true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr("Source"),
	})
	expectSpawnResult(t, client, sessionID, true)
	return workspaceID, sessionID, cwd
}

func consumeDelegatedPrompt(t *testing.T, backend *fakeSpawnBackend) {
	t.Helper()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		if _, err := os.ReadFile(opts.InitialPromptFile); err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}
}

func TestDelegateSpawnsAgentInSourceWorkspaceWithBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, cwd := setupDelegationSource(t, d, backend)

	var prompt string
	var promptPath string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		promptPath = opts.InitialPromptFile
		content, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(promptPath); err != nil {
			t.Fatalf("remove consumed initial prompt: %v", err)
		}
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Investigate the delegated task.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if result.WorkspaceID != workspaceID || result.Directory != cwd {
		t.Fatalf("result = %+v, want workspace=%s directory=%s", result, workspaceID, cwd)
	}
	if prompt != "Investigate the delegated task." {
		t.Fatalf("initial prompt = %q", prompt)
	}
	if promptPath == "" {
		t.Fatal("delegated spawn had no initial prompt file")
	}
	if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
		t.Fatalf("initial prompt file still exists after spawn: %v", err)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.WorkspaceID != workspaceID || session.Agent != protocol.SessionAgentCodex {
		t.Fatalf("delegated session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("workspace layout = %+v, want two panes", layout)
	}
}

func TestChiefOfStaffDelegateBindsTicketAndPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID || opts.InitialPromptFile == "" {
			return
		}
		content, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Investigate the tracked task.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	// A chief delegation now binds a ticket (no parallel dispatch record), and the
	// initial prompt teaches the agent to self-report via `ticket status`.
	if !strings.Contains(prompt, "Investigate the tracked task.") ||
		!strings.Contains(prompt, "ticket status in_progress") ||
		!strings.Contains(prompt, "ticket status completed") ||
		strings.Contains(prompt, "dispatch ") {
		t.Fatalf("tracked initial prompt = %q", prompt)
	}

	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket == nil {
		t.Fatalf("delegation did not bind a ticket to session %s", result.SessionID)
	}
	if ticket.Assignee != result.SessionID || ticket.Description != "Investigate the tracked task." {
		t.Fatalf("bound ticket = %+v", ticket)
	}

	// The delegated session is recognized as chief-delegated via the ticket binding.
	if ids := d.delegatedFromChiefSessionIDs(); !ids[result.SessionID] {
		t.Fatalf("delegated session missing from chief-delegated set: %v", ids)
	}
}

func TestChiefOfStaffDelegationPreservesCoordinationIdentityAcrossPlacements(t *testing.T) {
	for _, placement := range []string{
		delegationPlacementCurrent,
		delegationPlacementNew,
		delegationPlacementExisting,
	} {
		t.Run(placement, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			backend := &fakeSpawnBackend{}
			_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
			if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefSessionID); err != nil {
				t.Fatalf("set chief role: %v", err)
			}
			consumeDelegatedPrompt(t, backend)

			msg := &protocol.DelegateMessage{
				Cmd:             protocol.CmdDelegate,
				SourceSessionID: chiefSessionID,
				Brief:           "Exercise tracked coordination identity.",
				Agent:           protocol.Ptr("codex"),
				Placement:       protocol.Ptr(placement),
			}
			switch placement {
			case delegationPlacementNew:
				msg.Cwd = protocol.Ptr(t.TempDir())
			case delegationPlacementExisting:
				targetDirectory := t.TempDir()
				msg.WorkspaceID = protocol.Ptr("workspace-target")
				d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
					Cmd:       protocol.CmdRegisterWorkspace,
					ID:        protocol.Deref(msg.WorkspaceID),
					Title:     "Target",
					Directory: targetDirectory,
				})
			}

			result, err := d.delegate(msg)
			if err != nil {
				t.Fatalf("delegate() error = %v", err)
			}
			spawn, ok := backend.LastSpawn()
			if !ok || spawn.ID != result.SessionID || spawn.CWD != result.Directory {
				t.Fatalf("spawn = %+v, result = %+v", spawn, result)
			}

			// The delegated session owns a ticket in its resolved workspace.
			ticket, err := d.store.ActiveTicketForSession(spawn.ID)
			if err != nil || ticket == nil || ticket.Assignee != spawn.ID {
				t.Fatalf("active ticket = %+v, err=%v", ticket, err)
			}

			// Coordination identity is preserved: the delegated session, not the chief,
			// owns the workspace context across every placement.
			checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{
				SourceSessionID: spawn.ID,
			})
			if err != nil {
				t.Fatalf("checkout workspace context: %v", err)
			}
			if checkout.SessionID != spawn.ID || checkout.WorkspaceID != result.WorkspaceID {
				t.Fatalf("checkout = %+v, spawn = %+v, result = %+v", checkout, spawn, result)
			}
			if err := os.WriteFile(checkout.Path, []byte("# Workspace Context\n\n## Area\nDelegation identity.\n\n## Current Picture\nCoordination is routed by the delegated session.\n"), 0o600); err != nil {
				t.Fatalf("edit workspace context: %v", err)
			}
			updated, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{
				SourceSessionID: spawn.ID,
			})
			if err != nil || !changed || updated.SessionID != spawn.ID ||
				updated.WorkspaceID != result.WorkspaceID {
				t.Fatalf("workspace context update = %+v, changed=%v, err=%v", updated, changed, err)
			}
			canonical, err := d.store.GetWorkspaceContext(result.WorkspaceID)
			if err != nil || canonical.UpdatedBySessionID != spawn.ID {
				t.Fatalf("canonical workspace context = %+v, err=%v", canonical, err)
			}
		})
	}
}

func TestDelegatedFromChiefDecoratesBroadcastSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Investigate the tracked task.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	// Exercise the same list-broadcast path the sidebar receives.
	var delegated, chief *protocol.Session
	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		session := session
		switch session.ID {
		case result.SessionID:
			delegated = &session
		case sourceSessionID:
			chief = &session
		}
	}

	if delegated == nil {
		t.Fatal("delegated session missing from broadcast")
	}
	if !protocol.Deref(delegated.DelegatedFromChief) {
		t.Fatalf("delegated session = %+v, want delegated_from_chief=true", delegated)
	}
	if protocol.Deref(delegated.ChiefOfStaff) {
		t.Fatalf("delegated session should not be the chief itself: %+v", delegated)
	}

	if chief == nil {
		t.Fatal("chief session missing from broadcast")
	}
	if !protocol.Deref(chief.ChiefOfStaff) {
		t.Fatalf("chief session = %+v, want chief_of_staff=true", chief)
	}
	if protocol.Deref(chief.DelegatedFromChief) {
		t.Fatalf("chief session should not carry delegated_from_chief: %+v", chief)
	}
}

func TestOrdinaryDelegationDoesNotDecorateDelegatedFromChief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	// No chief role is set, so this is a plain session-to-session delegation.
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Plain delegated task.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	// Ordinary (non-chief) delegation binds no ticket and decorates nothing.
	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket != nil {
		t.Fatalf("ordinary delegation should not bind a ticket: %+v", ticket)
	}

	delegated := d.sessionForBroadcast(d.store.Get(result.SessionID))
	if delegated == nil {
		t.Fatal("delegated session missing")
	}
	if protocol.Deref(delegated.DelegatedFromChief) {
		t.Fatalf("ordinary delegated session should not carry delegated_from_chief: %+v", delegated)
	}
}

// Delegating from the chief creates and binds a ticket: the brief is the
// description, the delegated session is the assignee (its observer identity), the
// ticket is in-flight (Working), and a created event lands authored by the chief.
func TestDelegateCreatesAndBindsTicket(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: chiefSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket == nil {
		t.Fatal("delegate did not create a ticket bound to the session")
	}
	if ticket.Description != "Migrate the store to X" {
		t.Fatalf("ticket description = %q, want the brief", ticket.Description)
	}
	if ticket.Assignee != result.SessionID {
		t.Fatalf("ticket assignee = %q, want session id %q", ticket.Assignee, result.SessionID)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("ticket status = %q, want working", ticket.Status)
	}
	if ticket.Cwd == "" {
		t.Fatal("ticket cwd not set (needed for resume)")
	}

	// The created event is authored by the chief, so the agent observes it as its
	// "assigned to you" signal and the chief never sees its own action.
	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var created *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticket.ID && events[i].Kind == store.TicketEventCreated {
			created = &events[i]
		}
	}
	if created == nil {
		t.Fatalf("no created event for ticket %q", ticket.ID)
	}
	if created.Author != chiefSessionID {
		t.Fatalf("created event author = %q, want chief %q", created.Author, chiefSessionID)
	}
}

func TestDelegateRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "This spawn should fail.",
		Agent:           protocol.Ptr("codex"),
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	layout := d.store.GetWorkspaceLayout("workspace-source")
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("workspace layout after rollback = %+v", layout)
	}
}

// Copilot now supports initial-prompt delegation (it auto-executes the brief via
// `copilot --interactive`), so delegating to it must succeed and spawn a tracked
// session carrying the brief — the inverse of the old "rejects" assertion this
// replaces.
func TestDelegateAcceptsCopilotInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID || opts.InitialPromptFile == "" {
			return
		}
		content, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Use Copilot for this delegated task.",
		Agent:           protocol.Ptr("copilot"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v, want copilot delegation to succeed", err)
	}
	if prompt != "Use Copilot for this delegated task." {
		t.Fatalf("delegated initial prompt = %q", prompt)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.WorkspaceID != workspaceID || session.Agent != protocol.SessionAgentCopilot {
		t.Fatalf("delegated session = %+v, want copilot session in %s", session, workspaceID)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("workspace layout = %+v, want source + delegated panes", layout)
	}
}

func TestDelegateRejectsRemoteSourceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	source := d.store.Get(sourceSessionID)
	source.EndpointID = protocol.Ptr("endpoint-remote")
	d.store.Add(source)

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Do not launch this locally.",
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "delegation from remote session") {
		t.Fatalf("delegate() error = %v, want remote source rejection", err)
	}
	if len(backend.spawnOpts) != 1 {
		t.Fatalf("spawn count = %d, want only source session", len(backend.spawnOpts))
	}
}

func TestDelegateWebSocketCommandReturnsResult(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	payload, err := json.Marshal(protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Handle this through the websocket dispatcher.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("marshal delegate message: %v", err)
	}
	d.handleClientMessage(client, payload)

	select {
	case outbound := <-client.send:
		var result protocol.DelegateResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode delegate result: %v", err)
		}
		if result.Event != protocol.EventDelegateResult || !result.Success || result.Result == nil {
			t.Fatalf("delegate result = %+v", result)
		}
		if result.Result.WorkspaceID != "workspace-source" {
			t.Fatalf("workspace = %q, want workspace-source", result.Result.WorkspaceID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delegate_result")
	}
}

func TestDelegateKillsSpawnedRuntimeWhenPersistenceFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID {
			return
		}
		if opts.InitialPromptFile != "" {
			_ = os.Remove(opts.InitialPromptFile)
		}
		if err := d.store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Persistence will fail after this runtime starts.",
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "persist spawned session") {
		t.Fatalf("delegate() error = %v, want persistence failure", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ID == sourceSessionID {
		t.Fatalf("last spawn = %+v, want delegated runtime", spawn)
	}
	if !backend.WasKilledAndRemoved(spawn.ID) {
		t.Fatalf("delegated runtime %s was not killed and removed", spawn.ID)
	}
}

func TestResolveDelegationAgentSupportsRegisteredPluginWithInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registry := d.ensurePluginRegistry()
	registry.mu.Lock()
	registry.drivers["fixture"] = pluginDriverRegistration{
		PluginName: "fixture-plugin",
		Agent:      "fixture",
		Capabilities: map[string]bool{
			"initial_prompt": true,
		},
	}
	registry.mu.Unlock()

	agent, err := d.resolveDelegationAgent("codex", protocol.Ptr("fixture"))
	if err != nil {
		t.Fatalf("resolveDelegationAgent() error = %v", err)
	}
	if agent != "fixture" {
		t.Fatalf("agent = %q", agent)
	}
}

func TestDelegateCreatesNewWorkspaceAtCustomDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Work in a separate directory.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	targetDir = git.CanonicalizePath(targetDir)
	if result.Placement != delegationPlacementNew || result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil || workspace.Directory != targetDir {
		t.Fatalf("delegated workspace = %+v", workspace)
	}
	layout := d.store.GetWorkspaceLayout(result.WorkspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != result.SessionID {
		t.Fatalf("delegated workspace layout = %+v", layout)
	}
}

func TestDelegateNamesNewWorkspaceAndSessionFromExplicitName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Name everything from --name.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
		Label:           protocol.Ptr("launcher"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if workspace := d.store.GetWorkspace(result.WorkspaceID); workspace == nil || workspace.Title != "launcher" {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, "launcher")
	}
	if session := d.store.Get(result.SessionID); session == nil || session.Label != "launcher" {
		t.Fatalf("delegated session = %+v, want label %q", session, "launcher")
	}
	layout := d.store.GetWorkspaceLayout(result.WorkspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].Title != "launcher" {
		t.Fatalf("delegated layout = %+v, want one pane titled %q", layout, "launcher")
	}
}

func TestDelegateDefaultsNameToDirectoryBasename(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Default the name to the folder.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if workspace := d.store.GetWorkspace(result.WorkspaceID); workspace == nil || workspace.Title != "myproj" {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, "myproj")
	}
	if session := d.store.Get(result.SessionID); session == nil || session.Label != "myproj" {
		t.Fatalf("delegated session = %+v, want label %q", session, "myproj")
	}
}

func TestDelegateRejectsNameTooLong(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Name is too long.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
		Label:           protocol.Ptr("this-name-is-way-too-long"),
	})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("delegate() error = %v, want a name-too-long error", err)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 {
		t.Fatalf("workspaces = %+v, want only the source workspace", workspaces)
	}
}

func TestDelegateRejectsDuplicateWorkspaceName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "workspace-taken",
		Title:     "taken",
		Directory: t.TempDir(),
	})

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Reuse a workspace name.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(t.TempDir()),
		Label:           protocol.Ptr("taken"),
	})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("delegate() error = %v, want a duplicate-workspace error", err)
	}
}

func TestDelegateRejectsDuplicateSessionNameInWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	first, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "First agent.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
		Label:           protocol.Ptr("alpha"),
	})
	if err != nil {
		t.Fatalf("first delegate() error = %v", err)
	}

	_, err = d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Second agent, same name, same workspace.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(first.WorkspaceID),
		Label:           protocol.Ptr("alpha"),
	})
	if err == nil || !strings.Contains(err.Error(), "already used in this workspace") {
		t.Fatalf("second delegate() error = %v, want a duplicate-session error", err)
	}
}

func TestDelegateRejectsLongWorktreeDefaultNameAndRollsBack(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated-long")

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "No --name; the worktree folder is too long.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated-long",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("delegate() error = %v, want a name-too-long error", err)
	}
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Fatalf("worktree still exists after rollback: %v", statErr)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 {
		t.Fatalf("workspaces = %+v, want only the source workspace", workspaces)
	}
}

func TestValidateDelegationName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-taken", Title: "Taken", Directory: t.TempDir(),
	})
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-busy", Title: "Busy WS", Directory: t.TempDir(),
	})
	d.store.Add(&protocol.Session{ID: "sess-busy", Label: "Busy", WorkspaceID: "ws-busy", Directory: t.TempDir()})

	cases := []struct {
		name              string
		input             string
		creatingWorkspace bool
		targetWorkspaceID string
		wantErr           string // substring; "" means expect success
	}{
		{"sixteen ASCII accepted", strings.Repeat("a", 16), false, "", ""},
		{"seventeen ASCII rejected", strings.Repeat("a", 17), false, "", "too long"},
		{"sixteen runes accepted", strings.Repeat("é", 16), false, "", ""},           // 16 runes, 32 bytes
		{"seventeen runes rejected", strings.Repeat("é", 17), false, "", "too long"}, // 17 runes
		{"blank rejected", "   ", false, "", "a name is required"},
		{"dot rejected", ".", false, "", "not a usable name"},
		{"separator rejected", string(filepath.Separator), false, "", "not a usable name"},
		{"workspace duplicate is case-insensitive", "taken", true, "", "already in use"},
		{"fresh workspace name accepted", "fresh", true, "", ""},
		{"session duplicate is case-insensitive", "busy", false, "ws-busy", "already used in this workspace"},
		{"distinct session name accepted", "other", false, "ws-busy", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.validateDelegationName(tc.input, tc.creatingWorkspace, tc.targetWorkspaceID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateDelegationName(%q) = %v, want nil", tc.input, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateDelegationName(%q) = %v, want error containing %q", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestDelegateRejectsDuplicateWorkspaceNameFromDefault(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-existing", Title: "myproj", Directory: t.TempDir(),
	})
	// No --name: the directory basename defaults to "myproj", which collides
	// with the existing workspace title. The default path must still validate.
	targetDir := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Default name collides with an existing workspace.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
	})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("delegate() error = %v, want a duplicate-workspace error from the default-name path", err)
	}
}

func TestDelegateRejectsNameMatchingSourceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	source := d.store.Get(sourceSessionID)
	if source == nil || strings.TrimSpace(source.Label) == "" {
		t.Fatalf("source session has no label: %+v", source)
	}
	// Delegating into the current workspace with the source session's own label
	// (different case) must be rejected as a within-workspace duplicate.
	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Clash with the pre-existing source session name.",
		Label:           protocol.Ptr(strings.ToLower(source.Label)),
	})
	if err == nil || !strings.Contains(err.Error(), "already used in this workspace") {
		t.Fatalf("delegate() error = %v, want a duplicate-session error", err)
	}
}

func TestDelegateTargetsExistingWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: targetDir,
	})
	if _, errMsg := d.toggleWorkspaceMute(targetWorkspaceID); errMsg != "" {
		t.Fatalf("mute target workspace: %s", errMsg)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Join the target workspace.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if result.WorkspaceID != targetWorkspaceID || result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || !workspace.Muted {
		t.Fatalf("ordinary delegation changed target mute state: %+v", workspace)
	}
}

func TestDelegateTargetsPinnedEmptyWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-empty-pinned"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Empty pinned",
		Directory: targetDir,
	})
	if _, errMsg := d.setWorkspacePinned(targetWorkspaceID, true); errMsg != "" {
		t.Fatalf("pin target workspace: %s", errMsg)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Reuse the empty pinned workspace.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if result.WorkspaceID != targetWorkspaceID || result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	if sessions := d.store.SessionsInWorkspace(targetWorkspaceID); len(sessions) != 1 || sessions[0] != result.SessionID {
		t.Fatalf("target workspace sessions = %v, want delegated session %s", sessions, result.SessionID)
	}
}

func TestChiefOfStaffDelegateUnmutesExistingWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-muted-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Muted target",
		Directory: targetDir,
	})
	if _, errMsg := d.toggleWorkspaceMute(targetWorkspaceID); errMsg != "" {
		t.Fatalf("mute target workspace: %s", errMsg)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Join the muted target workspace.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if ticket, err := d.store.ActiveTicketForSession(result.SessionID); err != nil || ticket == nil {
		t.Fatalf("chief delegation missing bound ticket: ticket=%+v err=%v", ticket, err)
	}
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || workspace.Muted {
		t.Fatalf("chief delegation did not unmute target workspace: %+v", workspace)
	}
	workspace, ok := d.workspaces.snapshot(targetWorkspaceID)
	if !ok || workspace.Muted {
		t.Fatalf("registry target workspace still muted: %+v, found=%v", workspace, ok)
	}
}

func TestDelegateCreatesWorktreeInExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: mainRepo,
	})

	worktreePath := filepath.Join(root, "repo--feat-existing-ws")
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Work in a worktree placed in an existing workspace.",
		Label:           protocol.Ptr("delegated"),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/existing-ws",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Placement != delegationPlacementExisting ||
		result.WorkspaceID != targetWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.WorkspaceID != targetWorkspaceID ||
		session.Directory != worktreePath ||
		session.Label != "delegated" ||
		protocol.Deref(session.Branch) != "feat/existing-ws" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(targetWorkspaceID)
	if layout == nil || len(layout.Panes) != 1 {
		t.Fatalf("target workspace layout = %+v, want one pane", layout)
	}
}

func TestDelegateCreatesWorktreeInSourceWorkspace(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated-current")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Implement this in an isolated branch in this workspace.",
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated-current",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Placement != delegationPlacementCurrent ||
		result.WorkspaceID != sourceWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 {
		t.Fatalf("workspaces = %+v, want only source workspace", workspaces)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.WorkspaceID != sourceWorkspaceID ||
		session.Directory != worktreePath ||
		session.Label != "delegated" ||
		protocol.Deref(session.Branch) != "feat/delegated-current" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(sourceWorkspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("source workspace layout = %+v, want two panes", layout)
	}
}

func TestDelegateCreatesWorktreeAndNewWorkspace(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Implement this in an isolated branch.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Placement != delegationPlacementNew ||
		result.WorkspaceID == sourceWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want source and delegated workspaces", workspaces)
	}
	delegatedWorkspace := d.store.GetWorkspace(result.WorkspaceID)
	if delegatedWorkspace == nil || delegatedWorkspace.Title != "delegated" {
		t.Fatalf("delegated workspace = %+v, want title %q", delegatedWorkspace, "delegated")
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("worktree path stat = %v, info = %+v", err, info)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || protocol.Deref(session.Branch) != "feat/delegated" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
}

func TestDelegateRollsBackCurrentWorkspaceWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}
	worktreePath := filepath.Join(root, "repo--feat-current-rollback")

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "This spawn should roll back in the source workspace.",
		Label:           protocol.Ptr("rollback"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/current-rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after rollback: %v", err)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 || workspaces[0].ID != sourceWorkspaceID {
		t.Fatalf("workspaces after rollback = %+v, want only source workspace", workspaces)
	}
	layout := d.store.GetWorkspaceLayout(sourceWorkspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("source workspace layout after rollback = %+v", layout)
	}
}

func TestDelegateRollsBackNewWorkspaceAndWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}
	worktreePath := filepath.Join(root, "repo--feat-rollback")

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "This spawn should roll back.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("rollback"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after rollback: %v", err)
	}
	for _, workspace := range d.store.ListWorkspaces() {
		if workspace != nil && workspace.Directory == worktreePath {
			t.Fatalf("delegated workspace still exists after rollback: %+v", workspace)
		}
	}
}
