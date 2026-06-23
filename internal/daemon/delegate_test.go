package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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

func TestChiefOfStaffDelegateCreatesTrackedDispatch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, _ := setupDelegationSource(t, d, backend)
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
	if result.DispatchID == nil || protocol.Deref(result.DispatchID) == "" {
		t.Fatalf("delegate result = %+v, want dispatch id", result)
	}
	if !strings.Contains(prompt, "Investigate the tracked task.") ||
		!strings.Contains(prompt, "dispatch report --message") {
		t.Fatalf("tracked initial prompt = %q", prompt)
	}

	dispatches := d.chiefOfStaffDispatches(sourceSessionID)
	if len(dispatches) != 1 {
		t.Fatalf("dispatches = %+v", dispatches)
	}
	dispatch := dispatches[0]
	if dispatch.ID != protocol.Deref(result.DispatchID) ||
		dispatch.ChiefSessionID != sourceSessionID ||
		dispatch.SessionID != result.SessionID ||
		dispatch.WorkspaceID != workspaceID ||
		dispatch.Brief != "Investigate the tracked task." {
		t.Fatalf("dispatch = %+v", dispatch)
	}

	d.store.UpdateState(result.SessionID, string(protocol.SessionStateWaitingInput))
	dispatches = d.chiefOfStaffDispatches(sourceSessionID)
	if len(dispatches) != 1 || dispatches[0].Status != string(protocol.SessionStateWaitingInput) {
		t.Fatalf("updated dispatches = %+v", dispatches)
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
			dispatch := d.store.GetChiefOfStaffDispatchBySession(spawn.ID)
			if dispatch == nil || dispatch.ID != protocol.Deref(result.DispatchID) ||
				dispatch.WorkspaceID != result.WorkspaceID {
				t.Fatalf("dispatch = %+v, result = %+v", dispatch, result)
			}

			reportServer, reportClient := net.Pipe()
			go func() {
				d.handleReportDispatch(reportServer, &protocol.ReportDispatchMessage{
					Cmd:             protocol.CmdReportDispatch,
					SourceSessionID: spawn.ID,
					Report:          "Placement identity verified.",
				})
				_ = reportServer.Close()
			}()
			var reportResponse protocol.Response
			if err := json.NewDecoder(reportClient).Decode(&reportResponse); err != nil {
				t.Fatalf("decode report response: %v", err)
			}
			_ = reportClient.Close()
			if !reportResponse.Ok || reportResponse.ChiefOfStaffDispatch == nil ||
				reportResponse.ChiefOfStaffDispatch.SessionID != spawn.ID {
				t.Fatalf("report response = %+v", reportResponse)
			}

			inboxServer, inboxClient := net.Pipe()
			go func() {
				d.handleListDispatchMessages(inboxServer, &protocol.ListDispatchMessagesMessage{
					Cmd:             protocol.CmdListDispatchMessages,
					SourceSessionID: spawn.ID,
					UnreadOnly:      protocol.Ptr(true),
				})
				_ = inboxServer.Close()
			}()
			var inboxResponse protocol.Response
			if err := json.NewDecoder(inboxClient).Decode(&inboxResponse); err != nil {
				t.Fatalf("decode inbox response: %v", err)
			}
			_ = inboxClient.Close()
			if !inboxResponse.Ok {
				t.Fatalf("inbox response = %+v", inboxResponse)
			}

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
	if result.DispatchID != nil {
		t.Fatalf("ordinary delegation should not create a dispatch: %+v", result)
	}

	delegated := d.sessionForBroadcast(d.store.Get(result.SessionID))
	if delegated == nil {
		t.Fatal("delegated session missing")
	}
	if protocol.Deref(delegated.DelegatedFromChief) {
		t.Fatalf("ordinary delegated session should not carry delegated_from_chief: %+v", delegated)
	}
}

func TestDispatchReportUpdatesTrackedRecord(t *testing.T) {
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
		Brief:           "Produce a tracked report.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	server, client := net.Pipe()
	defer client.Close()
	go func() {
		d.handleReportDispatch(server, &protocol.ReportDispatchMessage{
			Cmd:             protocol.CmdReportDispatch,
			SourceSessionID: result.SessionID,
			Report:          "Implementation complete; focused tests pass.",
		})
		_ = server.Close()
	}()

	var response protocol.Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("decode report response: %v", err)
	}
	if !response.Ok || response.ChiefOfStaffDispatch == nil {
		t.Fatalf("report response = %+v", response)
	}
	if protocol.Deref(response.ChiefOfStaffDispatch.LatestReport) != "Implementation complete; focused tests pass." {
		t.Fatalf("reported dispatch = %+v", response.ChiefOfStaffDispatch)
	}
}

func TestDispatchMailboxAuthorizesChiefAndWorker(t *testing.T) {
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
		Brief:           "Investigate mailbox behavior.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	dispatchID := protocol.Deref(result.DispatchID)

	server, client := net.Pipe()
	sendServer := server
	go func() {
		d.handleSendDispatchMessage(sendServer, &protocol.SendDispatchMessage{
			Cmd:             protocol.CmdSendDispatchMessage,
			SourceSessionID: chiefSessionID,
			DispatchID:      dispatchID,
			Content:         "Re-check the current branch.",
		})
		_ = sendServer.Close()
	}()
	var sent protocol.Response
	if err := json.NewDecoder(client).Decode(&sent); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	_ = client.Close()
	if !sent.Ok || sent.DispatchMessage == nil || sent.DispatchMessage.TargetSessionID != result.SessionID {
		t.Fatalf("send response = %+v", sent)
	}
	if got := protocol.Deref(d.decorateChiefOfStaffDispatch(
		d.store.GetChiefOfStaffDispatch(dispatchID),
	).UnreadMessageCount); got != 1 {
		t.Fatalf("unread count = %d, want 1", got)
	}

	server, client = net.Pipe()
	inboxServer := server
	go func() {
		d.handleListDispatchMessages(inboxServer, &protocol.ListDispatchMessagesMessage{
			Cmd:             protocol.CmdListDispatchMessages,
			SourceSessionID: result.SessionID,
			UnreadOnly:      protocol.Ptr(true),
		})
		_ = inboxServer.Close()
	}()
	var inbox protocol.Response
	if err := json.NewDecoder(client).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox response: %v", err)
	}
	_ = client.Close()
	if !inbox.Ok || len(inbox.DispatchMessages) != 1 ||
		inbox.DispatchMessages[0].Content != "Re-check the current branch." {
		t.Fatalf("inbox response = %+v", inbox)
	}

	server, client = net.Pipe()
	ackServer := server
	go func() {
		d.handleAcknowledgeDispatchMessage(ackServer, &protocol.AcknowledgeDispatchMessage{
			Cmd:             protocol.CmdAcknowledgeDispatchMessage,
			SourceSessionID: result.SessionID,
			MessageID:       sent.DispatchMessage.ID,
			Acknowledgement: protocol.Ptr("Re-check complete."),
		})
		_ = ackServer.Close()
	}()
	var acknowledged protocol.Response
	if err := json.NewDecoder(client).Decode(&acknowledged); err != nil {
		t.Fatalf("decode acknowledge response: %v", err)
	}
	_ = client.Close()
	unreadCount, unreadErr := d.store.CountUnreadDispatchMessages(dispatchID)
	if !acknowledged.Ok || acknowledged.DispatchMessage == nil ||
		protocol.Deref(acknowledged.DispatchMessage.Acknowledgement) != "Re-check complete." ||
		unreadErr != nil || unreadCount != 0 {
		t.Fatalf("acknowledge response = %+v", acknowledged)
	}

	server, client = net.Pipe()
	sentMessagesServer := server
	go func() {
		d.handleListDispatchMessages(sentMessagesServer, &protocol.ListDispatchMessagesMessage{
			Cmd:             protocol.CmdListDispatchMessages,
			SourceSessionID: chiefSessionID,
			DispatchID:      protocol.Ptr(dispatchID),
		})
		_ = sentMessagesServer.Close()
	}()
	var sentMessages protocol.Response
	if err := json.NewDecoder(client).Decode(&sentMessages); err != nil {
		t.Fatalf("decode sent messages response: %v", err)
	}
	_ = client.Close()
	if !sentMessages.Ok || len(sentMessages.DispatchMessages) != 1 ||
		protocol.Deref(sentMessages.DispatchMessages[0].Acknowledgement) != "Re-check complete." ||
		sentMessages.DispatchMessages[0].AcknowledgedAt == nil {
		t.Fatalf("sent messages response = %+v", sentMessages)
	}

	server, client = net.Pipe()
	unauthorizedServer := server
	go func() {
		d.handleSendDispatchMessage(unauthorizedServer, &protocol.SendDispatchMessage{
			Cmd:             protocol.CmdSendDispatchMessage,
			SourceSessionID: "other-session",
			DispatchID:      dispatchID,
			Content:         "Unauthorized.",
		})
		_ = unauthorizedServer.Close()
	}()
	var unauthorized protocol.Response
	if err := json.NewDecoder(client).Decode(&unauthorized); err != nil {
		t.Fatalf("decode unauthorized response: %v", err)
	}
	_ = client.Close()
	if unauthorized.Ok {
		t.Fatalf("unauthorized send response = %+v", unauthorized)
	}

	d.store.Remove(result.SessionID)
	server, client = net.Pipe()
	closedServer := server
	go func() {
		d.handleSendDispatchMessage(closedServer, &protocol.SendDispatchMessage{
			Cmd:             protocol.CmdSendDispatchMessage,
			SourceSessionID: chiefSessionID,
			DispatchID:      dispatchID,
			Content:         "This worker is closed.",
		})
		_ = closedServer.Close()
	}()
	var closed protocol.Response
	if err := json.NewDecoder(client).Decode(&closed); err != nil {
		t.Fatalf("decode closed worker response: %v", err)
	}
	_ = client.Close()
	if closed.Ok || !strings.Contains(protocol.Deref(closed.Error), "is closed") {
		t.Fatalf("closed worker response = %+v", closed)
	}
}

func TestWakeDispatchAgentInjectsOnlyInboxPrompt(t *testing.T) {
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
		Brief:           "Investigate wake behavior.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	dispatchID := protocol.Deref(result.DispatchID)
	if err := d.store.AddDispatchMessage(&protocol.DispatchMessage{
		ID:              "message-secret",
		DispatchID:      dispatchID,
		SenderSessionID: chiefSessionID,
		TargetSessionID: result.SessionID,
		Content:         "Mailbox content must not reach the PTY.",
		CreatedAt:       string(protocol.TimestampNow()),
	}); err != nil {
		t.Fatalf("add dispatch message: %v", err)
	}
	d.store.UpdateState(result.SessionID, string(protocol.SessionStateWaitingInput))

	var inputSessionIDs []string
	var inputs []string
	backend.onInput = func(sessionID string, data []byte) {
		inputSessionIDs = append(inputSessionIDs, sessionID)
		inputs = append(inputs, string(data))
	}
	wsClient := newWorkspaceProtocolTestClient()
	d.handleWakeDispatchAgent(wsClient, &protocol.WakeDispatchAgentMessage{
		Cmd:             protocol.CmdWakeDispatchAgent,
		SourceSessionID: chiefSessionID,
		DispatchID:      dispatchID,
		RequestID:       "wake-request-1",
	})
	select {
	case outbound := <-wsClient.send:
		var response protocol.WakeDispatchAgentResultMessage
		if err := json.Unmarshal(outbound.payload, &response); err != nil {
			t.Fatalf("decode wake response: %v", err)
		}
		if !response.Success || response.RequestID != "wake-request-1" {
			t.Fatalf("wake response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wake response")
	}
	if len(inputs) != 2 ||
		inputSessionIDs[0] != result.SessionID ||
		inputSessionIDs[1] != result.SessionID ||
		inputs[0] != dispatchWakePrompt ||
		inputs[1] != "\r" {
		t.Fatalf("PTY inputs = (%q, %q)", inputSessionIDs, inputs)
	}
	if strings.Contains(strings.Join(inputs, ""), "Mailbox content") {
		t.Fatalf("PTY input included mailbox content: %q", inputs)
	}
}

func TestStructuredDispatchReportSeparatesRuntimeAndSupportsResolution(t *testing.T) {
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
		Brief:           "Produce a structured tracked report.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	d.store.UpdateState(result.SessionID, string(protocol.SessionStateIdle))

	report := &protocol.DispatchReport{
		ReportType: protocol.DispatchReportTypeBlocker,
		Summary:    "Core implementation ready locally",
		WorkState:  protocol.DispatchWorkStateNeedsInput,
		NextActor:  protocol.Ptr("team"),
		NextAction: protocol.Ptr("Decide the event contract"),
		Request: &protocol.DispatchDecisionRequest{
			Question:          "Which event contract should be used?",
			Recommendation:    protocol.Ptr("Use AisNoOperationV1"),
			Consequence:       protocol.Ptr("Event emission remains blocked"),
			ExpectedResponder: "team",
		},
		Artifact: &protocol.DispatchArtifact{
			Identity: "dirty:abc123",
		},
		Verification: []protocol.DispatchVerification{
			{
				Actor:            "agent",
				Target:           "go test ./internal/feature",
				Result:           "passed",
				Timestamp:        string(protocol.TimestampNow()),
				ArtifactIdentity: "commit:old",
			},
		},
	}
	server, client := net.Pipe()
	reportDone := make(chan struct{})
	go func() {
		d.handleReportDispatch(server, &protocol.ReportDispatchMessage{
			Cmd:              protocol.CmdReportDispatch,
			SourceSessionID:  result.SessionID,
			Report:           "Core implementation ready; decision required.",
			StructuredReport: report,
		})
		_ = server.Close()
		close(reportDone)
	}()
	var reportResponse protocol.Response
	if err := json.NewDecoder(client).Decode(&reportResponse); err != nil {
		t.Fatalf("decode report response: %v", err)
	}
	_ = client.Close()
	<-reportDone
	dispatch := reportResponse.ChiefOfStaffDispatch
	if !reportResponse.Ok || dispatch == nil || dispatch.StructuredReport == nil {
		t.Fatalf("report response = %+v", reportResponse)
	}
	if dispatch.Status != string(protocol.SessionStateIdle) ||
		dispatch.StructuredReport.WorkState != protocol.DispatchWorkStateNeedsInput {
		t.Fatalf("runtime/work state = (%q, %q)", dispatch.Status, dispatch.StructuredReport.WorkState)
	}
	if !protocol.Deref(dispatch.Actionable) ||
		protocol.Deref(dispatch.ConciseSummary) != "Core implementation ready locally" {
		t.Fatalf("actionable dispatch = %+v", dispatch)
	}
	if protocol.Deref(dispatch.StructuredReport.Verification[0].Current) {
		t.Fatalf("stale verification shown current: %+v", dispatch.StructuredReport.Verification)
	}

	server, client = net.Pipe()
	resolveServer := server
	resolveDone := make(chan struct{})
	go func() {
		d.handleResolveDispatchRequest(resolveServer, &protocol.ResolveDispatchRequestMessage{
			Cmd:             protocol.CmdResolveDispatchRequest,
			SourceSessionID: chiefSessionID,
			DispatchID:      protocol.Deref(result.DispatchID),
			Response:        "Use AisNoOperationV1.",
			ResolutionLink:  protocol.Ptr("https://example.test/decision"),
		})
		_ = resolveServer.Close()
		close(resolveDone)
	}()
	var resolveResponse protocol.Response
	if err := json.NewDecoder(client).Decode(&resolveResponse); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}
	_ = client.Close()
	<-resolveDone
	resolved := resolveResponse.ChiefOfStaffDispatch
	if !resolveResponse.Ok || resolved == nil || resolved.StructuredReport == nil {
		t.Fatalf("resolve response = %+v", resolveResponse)
	}
	if resolved.StructuredReport.Request.Status != protocol.DispatchRequestStatusResolved ||
		protocol.Deref(resolved.StructuredReport.Request.Response) != "Use AisNoOperationV1." ||
		protocol.Deref(resolved.Actionable) {
		t.Fatalf("resolved dispatch = %+v", resolved)
	}
	persisted := d.store.GetChiefOfStaffDispatchBySession(result.SessionID)
	if persisted == nil {
		t.Fatal("resolved dispatch was not persisted")
	}
	if _, err := json.Marshal(d.decorateChiefOfStaffDispatch(persisted)); err != nil {
		t.Fatalf("marshal persisted dispatch: %v", err)
	}

	server, client = net.Pipe()
	statusServer := server
	go func() {
		d.handleGetDispatch(statusServer, &protocol.GetDispatchMessage{
			Cmd:             protocol.CmdGetDispatch,
			SourceSessionID: result.SessionID,
		})
		_ = statusServer.Close()
	}()
	var statusResponse protocol.Response
	if err := json.NewDecoder(client).Decode(&statusResponse); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	_ = client.Close()
	if !statusResponse.Ok ||
		statusResponse.ChiefOfStaffDispatch == nil ||
		protocol.Deref(statusResponse.ChiefOfStaffDispatch.StructuredReport.Request.Response) != "Use AisNoOperationV1." {
		t.Fatalf("delegated status response = %+v", statusResponse)
	}
}

func TestReadyForReviewDispatchIsActionable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dispatch := d.decorateChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		StructuredReport: &protocol.DispatchReport{
			Summary:   "Implementation ready",
			WorkState: protocol.DispatchWorkStateReadyForReview,
		},
	})
	if dispatch == nil || !protocol.Deref(dispatch.Actionable) {
		t.Fatalf("ready-for-review dispatch = %+v, want actionable", dispatch)
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
	if result.DispatchID == nil {
		t.Fatalf("chief delegation missing dispatch: %+v", result)
	}
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || workspace.Muted {
		t.Fatalf("chief delegation did not unmute target workspace: %+v", workspace)
	}
	workspace, ok := d.workspaces.snapshot(targetWorkspaceID)
	if !ok || workspace.Muted {
		t.Fatalf("registry target workspace still muted: %+v, found=%v", workspace, ok)
	}
}

func TestDelegateRejectsWorktreeInExistingWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Do not create this unsupported worktree.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr("workspace-source"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/unsupported",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "existing_workspace placement does not accept cwd or worktree") {
		t.Fatalf("delegate() error = %v", err)
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
