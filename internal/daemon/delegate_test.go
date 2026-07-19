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
	// Every delegated agent's initial prompt is prefixed with the leaf identity
	// line so a non-chief-delegated leaf — which gets no ticket-report contract —
	// still has a positive signal that it is a leaf, not a coordinator.
	if !strings.Contains(prompt, "a leaf, not a coordinator") ||
		!strings.Contains(prompt, "Investigate the delegated task.") {
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
	// Completion follows strong terminal evidence, not a mandatory confirmation
	// ritual; implementation finished while review remains maps to in-review.
	for _, expected := range []string{"strong terminal evidence", "user accepted the work", "requested PR merged", "ready_for_review"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("tracked initial prompt missing evidence-based completion guidance %q: %q", expected, prompt)
		}
	}
	if strings.Contains(prompt, "ask the user to confirm") {
		t.Fatalf("tracked initial prompt retained mandatory confirmation gate: %q", prompt)
	}
	for _, expected := range []string{"ticket attach-plan --file", "--scope <affected-component>", "committed repository plan stays canonical in Git", "never deletes a tracked", "meaningful edits, renames, or deletions"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("tracked initial prompt missing %q: %q", expected, prompt)
		}
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
	if !strings.Contains(prompt, "a leaf, not a coordinator") ||
		!strings.Contains(prompt, "Use Copilot for this delegated task.") {
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

// --model / --effort ride the delegate request into the spawned session's
// launch options; effort is normalized to lowercase on the way through.
func TestDelegateThreadsModelAndEffortIntoSpawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Run pinned to a specific model.",
		Agent:           protocol.Ptr("claude"),
		Model:           protocol.Ptr("claude-fable-5"),
		Effort:          protocol.Ptr("Low"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ID != result.SessionID {
		t.Fatalf("last spawn = %+v, want delegated session %s", spawn, result.SessionID)
	}
	if spawn.Model != "claude-fable-5" || spawn.Effort != "low" {
		t.Fatalf("spawn model/effort = %q/%q, want claude-fable-5/low", spawn.Model, spawn.Effort)
	}
}

func TestDelegateThreadsModelAndEffortIntoPluginDriver(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	client, done := startPluginPipe(t, d, "fixture-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "fixture", map[string]bool{
		"initial_prompt": true,
		"model_pin":      true,
		"effort_pin":     true,
	})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		for {
			request := decodeJSONRPCMessage(t, client)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, client, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.spawn" {
				t.Errorf("method=%q, want driver.spawn", request.Method)
				return
			}
			var got pluginDriverSpawnParams
			if err := json.Unmarshal(request.Params, &got); err != nil {
				t.Errorf("decode plugin spawn params: %v", err)
				return
			}
			if got.Model != "spotify-glm/zai-org/GLM-5.2-FP8" || got.Effort != "low" {
				t.Errorf("plugin spawn pins=%q/%q, want selected model/low", got.Model, got.Effort)
			}
			respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"fixture"}})
			return
		}
	}()

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Use the selected OpenCode variant.",
		Agent:           protocol.Ptr("fixture"),
		Model:           protocol.Ptr("spotify-glm/zai-org/GLM-5.2-FP8"),
		Effort:          protocol.Ptr("LOW"),
	}); err != nil {
		t.Fatalf("delegate() error=%v", err)
	}
	<-requestDone
}

// A delegation without pins must not inherit any: the spawned agent keeps its
// own defaults (empty model/effort all the way down).
func TestDelegateWithoutModelEffortLeavesAgentDefaults(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Run with agent defaults.",
		Agent:           protocol.Ptr("claude"),
	}); err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.Model != "" || spawn.Effort != "" {
		t.Fatalf("spawn model/effort = %q/%q, want both empty", spawn.Model, spawn.Effort)
	}
}

// Copilot's launch command applies neither pin, so both flags fail fast at
// delegate time instead of being silently dropped by the spawned session.
func TestDelegateRejectsModelEffortForUnsupportedAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	for flag, msg := range map[string]*protocol.DelegateMessage{
		"--model": {
			Cmd:             protocol.CmdDelegate,
			SourceSessionID: sourceSessionID,
			Brief:           "Pin a model on copilot.",
			Agent:           protocol.Ptr("copilot"),
			Model:           protocol.Ptr("gpt-5"),
		},
		"--effort": {
			Cmd:             protocol.CmdDelegate,
			SourceSessionID: sourceSessionID,
			Brief:           "Pin effort on copilot.",
			Agent:           protocol.Ptr("copilot"),
			Effort:          protocol.Ptr("high"),
		},
	} {
		_, err := d.delegate(msg)
		if err == nil || !strings.Contains(err.Error(), "does not support "+flag) {
			t.Fatalf("delegate(%s) error = %v, want unsupported-agent rejection", flag, err)
		}
	}
	if len(backend.spawnOpts) != 1 {
		t.Fatalf("spawn count = %d, want only source session", len(backend.spawnOpts))
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
	case <-time.After(5 * time.Second):
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

func TestDelegateTruncatesLongWorktreeDefaultName(t *testing.T) {
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

	// No --name; the worktree folder basename ("repo--feat-delegated-long", 26
	// runes) exceeds maxDelegationNameRunes. A derived name is truncated instead
	// of rejected, so the delegation succeeds with a shortened, clean name.
	result, err := d.delegate(&protocol.DelegateMessage{
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
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantName := "repo--feat-deleg"
	if len([]rune(wantName)) > maxDelegationNameRunes {
		t.Fatalf("test setup bug: wantName %q exceeds max", wantName)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil || workspace.Title != wantName {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, wantName)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.Label != wantName {
		t.Fatalf("delegated session = %+v, want label %q", session, wantName)
	}
}

func TestDelegateRejectsActiveWorktreeCollisionWithoutExplicitReuse(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--shared")
	base := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID, Brief: "First owner.",
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("owner"), Placement: protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/shared", Path: protocol.Ptr(worktreePath)},
	}
	if _, err := d.delegate(&base); err != nil {
		t.Fatalf("first delegate: %v", err)
	}

	retry := base
	retry.Brief = "Unintentional second owner."
	retry.Label = protocol.Ptr("collision")
	retry.Placement = protocol.Ptr(delegationPlacementCurrent)
	if _, err := d.delegate(&retry); err == nil || !strings.Contains(err.Error(), "--allow-worktree-reuse") {
		t.Fatalf("collision error=%v, want explicit reuse guidance", err)
	}
	retry.AllowWorktreeReuse = protocol.Ptr(true)
	result, err := d.delegate(&retry)
	if err != nil {
		t.Fatalf("explicit reuse: %v", err)
	}
	if result.Directory != git.CanonicalizePath(worktreePath) {
		t.Fatalf("directory=%q want %q", result.Directory, worktreePath)
	}

	byCWD := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID, Brief: "CWD collision.",
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("cwd-collision"),
		Placement: protocol.Ptr(delegationPlacementNew), Cwd: protocol.Ptr(worktreePath),
	}
	if main := git.GetMainRepoFromWorktree(worktreePath); main == "" {
		t.Fatal("test setup did not produce a linked worktree")
	}
	if !d.store.HasSessionInDirectory(git.CanonicalizePath(worktreePath)) {
		t.Fatal("test setup has no active session in shared worktree")
	}
	if protocol.Deref(byCWD.AllowWorktreeReuse) {
		t.Fatal("cwd collision unexpectedly opted into reuse")
	}
	if _, err := d.delegate(&byCWD); err == nil || !strings.Contains(err.Error(), "--allow-worktree-reuse") {
		t.Fatalf("cwd collision error=%v, want explicit reuse guidance", err)
	}
	byCWD.AllowWorktreeReuse = protocol.Ptr(true)
	if _, err := d.delegate(&byCWD); err != nil {
		t.Fatalf("explicit cwd reuse: %v", err)
	}

	subdir := filepath.Join(worktreePath, "nested", "package")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bySubdir := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID, Brief: "Nested CWD collision.",
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("nested-collision"),
		Placement: protocol.Ptr(delegationPlacementNew), Cwd: protocol.Ptr(subdir),
	}
	if _, err := d.delegate(&bySubdir); err == nil || !strings.Contains(err.Error(), git.CanonicalizePath(worktreePath)) {
		t.Fatalf("nested cwd collision error=%v, want resolved worktree root", err)
	}
	bySubdir.AllowWorktreeReuse = protocol.Ptr(true)
	if _, err := d.delegate(&bySubdir); err != nil {
		t.Fatalf("explicit nested cwd reuse: %v", err)
	}
}

func TestTruncateDelegationName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"within limit is unchanged", "myproj", "myproj"},
		{"exactly at limit is unchanged", strings.Repeat("a", 16), strings.Repeat("a", 16)},
		{"cuts to the rune limit", "attn--feat-agent-cost-tooling", "attn--feat-agent"},
		{"trims a trailing dash after the cut", strings.Repeat("a", 15) + "-more-stuff", strings.Repeat("a", 15)},
		{"trims trailing punctuation and whitespace", "twelve chars.   more", "twelve chars"},
		{"multi-byte runes counted as one", strings.Repeat("é", 20), strings.Repeat("é", 16)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateDelegationName(tc.input)
			if got != tc.want {
				t.Fatalf("truncateDelegationName(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if len([]rune(got)) > maxDelegationNameRunes {
				t.Fatalf("truncateDelegationName(%q) = %q exceeds max runes", tc.input, got)
			}
		})
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

// TestDelegateComposesCwdAndWorktree covers the new_workspace placement with
// both --cwd and --worktree set: the worktree's repo and starting ref are
// inferred from the cwd (not the source session's directory), and the new
// workspace ends up placed at the created worktree path, not the cwd itself.
func TestDelegateComposesCwdAndWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	// Source session lives elsewhere so a correct implementation must infer
	// the worktree's repo/starting-ref from --cwd, not from the source.
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-cwd-compose")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Compose --cwd with --worktree.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("composed"),
		Cwd:             protocol.Ptr(mainRepo),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/cwd-compose",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Placement != delegationPlacementNew ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v, want directory %q", result, worktreePath)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil || workspace.Directory != worktreePath {
		t.Fatalf("delegated workspace = %+v, want directory %q", workspace, worktreePath)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.Directory != worktreePath ||
		protocol.Deref(session.Branch) != "feat/cwd-compose" {
		t.Fatalf("delegated session = %+v", session)
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("worktree path stat = %v, info = %+v", err, info)
	}
}

// TestDelegateComposedCwdWorktreeRequiresRepoWhenNotAGitRepo covers the
// failure mode: a --cwd that is not itself a git repository, combined with
// --worktree and no --repo, must still surface the existing "pass --repo"
// error rather than silently falling back to the source session's repo.
func TestDelegateComposedCwdWorktreeRequiresRepoWhenNotAGitRepo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	notARepo := t.TempDir()

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "cwd is not a git repo and no --repo is given.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(notARepo),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/no-repo",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not in a git repository; pass --repo") {
		t.Fatalf("delegate() error = %v, want a not-a-git-repository error", err)
	}
}

// TestDelegateTruncatesLongDirectoryDefaultName covers the plain (no
// worktree) new_workspace case: a directory whose basename exceeds
// maxDelegationNameRunes, with no explicit --name, gets a truncated name
// instead of a rejected delegation.
func TestDelegateTruncatesLongDirectoryDefaultName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := filepath.Join(t.TempDir(), "a-very-long-directory-name-indeed")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "No --name; the directory basename is too long.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantName := "a-very-long-dire"
	if len([]rune(wantName)) > maxDelegationNameRunes {
		t.Fatalf("test setup bug: wantName %q exceeds max", wantName)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil || workspace.Title != wantName {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, wantName)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.Label != wantName {
		t.Fatalf("delegated session = %+v, want label %q", session, wantName)
	}
}

// initDelegationRepo creates a real git repository with one commit.
func initDelegationRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "init")
	return git.CanonicalizePath(repo)
}

// addWorkspaceSessionAt spawns an extra session into an existing workspace at
// the given directory, the way a real pane in that workspace would exist.
func addWorkspaceSessionAt(t *testing.T, d *Daemon, workspaceID, sessionID, cwd string) {
	t.Helper()
	client := newWorkspaceProtocolTestClient()
	paneID := "pane-" + sessionID
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(sessionID),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr(sessionID),
	})
	expectSpawnResult(t, client, sessionID, true)
}

// TestDelegateWorktreeIgnoresStaleWorkspaceDirectory is the regression test for
// a worktree landing in the wrong repository. The target workspace holds a
// session in repoA, but its stored directory has drifted to repoB — which is
// what production does, since AddWorkspace overwrites directory on every
// re-registration and never recomputes it from member sessions.
//
// The worktree must be created off repoA (where the workspace's session
// actually lives), and nothing at all may appear beside repoB.
func TestDelegateWorktreeIgnoresStaleWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-target", repoA)
	// Drift the stored directory to an unrelated repo, exactly as a
	// re-registration does in production.
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoB,
	})
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || workspace.Directory != repoB {
		t.Fatalf("precondition: workspace directory = %+v, want %q", workspace, repoB)
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Work on the repo this workspace actually uses.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/right-repo",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repoA, "feat/right-repo"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q (off repoA)", result.Directory, wantPath)
	}
	if main := git.GetMainRepoFromWorktree(result.Directory); git.CanonicalizePath(main) != repoA {
		t.Fatalf("worktree main repo = %q, want %q", main, repoA)
	}
	// The decisive assertion: no worktree may exist anywhere beside repoB.
	strayPath := git.CanonicalizePath(git.GenerateWorktreePath(repoB, "feat/right-repo"))
	if _, statErr := os.Stat(strayPath); !os.IsNotExist(statErr) {
		t.Fatalf("worktree created in the wrong repository at %s", strayPath)
	}
}

// TestDelegateWorktreeAmbiguousWorkspaceRepoRequiresRepo covers the case the
// member sessions cannot settle: they span two repositories, so there is no
// defensible inference. Fail and name --repo rather than pick one silently.
func TestDelegateWorktreeAmbiguousWorkspaceRepoRequiresRepo(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", repoA)
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-b", repoB)

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Ambiguous repo.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/ambiguous",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "pass --repo") {
		t.Fatalf("delegate() error = %v, want an ambiguous-repository error naming --repo", err)
	}
	for _, repo := range []string{repoA, repoB} {
		strayPath := git.CanonicalizePath(git.GenerateWorktreePath(repo, "feat/ambiguous"))
		if _, statErr := os.Stat(strayPath); !os.IsNotExist(statErr) {
			t.Fatalf("worktree created at %s despite ambiguous repository", strayPath)
		}
	}
}

// TestDelegateWorktreeExplicitRepoOverridesWorkspaceSessions confirms --repo
// still wins over the inferred repository.
func TestDelegateWorktreeExplicitRepoOverridesWorkspaceSessions(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-target", repoA)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Explicit repo wins.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(repoB),
			Branch: "feat/explicit",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repoB, "feat/explicit"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q (off explicit --repo)", result.Directory, wantPath)
	}
}

// TestDelegateWorktreeSameRepoDifferentBranchesUsesRepoDefault covers the case
// where a workspace's member sessions sit in different worktrees of the SAME
// main repository, each on its own branch. The repository is unambiguous, so
// the delegation must succeed — but no member session's branch is a defensible
// starting point, and picking a representative session would make the starting
// ref depend on session-id ordering.
//
// The new branch must therefore start from the repository's default, matching
// neither member branch, regardless of which session sorts first.
func TestDelegateWorktreeSameRepoDifferentBranchesUsesRepoDefault(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	mainHead := gitRevParseDaemon(t, repo, "HEAD")

	// Two sibling worktrees of the same repo, each with its own extra commit so
	// their heads differ from main and from each other.
	worktreeA := filepath.Join(root, "repo--feat-a")
	worktreeB := filepath.Join(root, "repo--feat-b")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/a", worktreeA)
	runGitDaemon(t, worktreeA, "commit", "--allow-empty", "-m", "a")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/b", worktreeB)
	runGitDaemon(t, worktreeB, "commit", "--allow-empty", "-m", "b")
	headA := gitRevParseDaemon(t, worktreeA, "HEAD")
	headB := gitRevParseDaemon(t, worktreeB, "HEAD")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repo,
	})
	// "session-a" sorts before "session-b", so an ordering-dependent
	// implementation starts the new branch from feat/a.
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", worktreeA)
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-b", worktreeB)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Same repo, two branches.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/from-default",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v, want success (repository is unambiguous)", err)
	}
	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repo, "feat/from-default"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q", result.Directory, wantPath)
	}

	head := gitRevParseDaemon(t, result.Directory, "HEAD")
	if head == headA || head == headB {
		t.Fatalf("new branch started from a member session's branch (head %s; feat/a %s, feat/b %s); "+
			"the starting ref must not depend on which session sorts first", head, headA, headB)
	}
	if head != mainHead {
		t.Fatalf("new branch head = %s, want the repository default %s", head, mainHead)
	}
}

// TestDelegateWorktreeExplicitFromStillWins confirms an explicit --from is
// still honoured for an existing-workspace placement.
func TestDelegateWorktreeExplicitFromStillWins(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	worktreeA := filepath.Join(root, "repo--feat-a")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/a", worktreeA)
	runGitDaemon(t, worktreeA, "commit", "--allow-empty", "-m", "a")
	headA := gitRevParseDaemon(t, worktreeA, "HEAD")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repo,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", worktreeA)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Explicit from.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch:       "feat/explicit-from",
			StartingFrom: protocol.Ptr("feat/a"),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if head := gitRevParseDaemon(t, result.Directory, "HEAD"); head != headA {
		t.Fatalf("new branch head = %s, want feat/a head %s", head, headA)
	}
}
