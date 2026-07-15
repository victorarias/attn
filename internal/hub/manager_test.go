package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"nhooyr.io/websocket"
)

func TestManagerRemoteSessionsTagAndSeparateEndpoints(t *testing.T) {
	endpointStore := store.New()
	first, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint(first) error = %v", err)
	}
	second, err := endpointStore.AddEndpoint("dev-box", "dev", "")
	if err != nil {
		t.Fatalf("AddEndpoint(second) error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)

	if changed := manager.replaceRemoteSessions(first.ID, []protocol.Session{{
		ID:        "sess-a",
		Label:     "GPU review",
		Directory: "/srv/repo",
		State:     protocol.SessionStateWorking,
		LastSeen:  "2026-04-03T10:00:00Z",
	}}); !changed {
		t.Fatal("replaceRemoteSessions(first) reported no change")
	}
	if changed := manager.replaceRemoteSessions(second.ID, []protocol.Session{{
		ID:        "sess-b",
		Label:     "DEV fix",
		Directory: "/srv/repo",
		State:     protocol.SessionStateIdle,
		LastSeen:  "2026-04-03T10:01:00Z",
	}}); !changed {
		t.Fatal("replaceRemoteSessions(second) reported no change")
	}

	got := manager.RemoteSessions()
	if len(got) != 2 {
		t.Fatalf("RemoteSessions() len = %d, want 2", len(got))
	}
	if protocol.Deref(got[0].EndpointID) == protocol.Deref(got[1].EndpointID) {
		t.Fatalf("RemoteSessions() endpoint ids = %q and %q, want distinct endpoints", protocol.Deref(got[0].EndpointID), protocol.Deref(got[1].EndpointID))
	}
	for _, session := range got {
		if protocol.Deref(session.EndpointID) == "" {
			t.Fatalf("RemoteSessions() session %+v missing endpoint id", session)
		}
	}
}

func TestManagerRemoteSessionsUpsertAndClear(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)

	changed, count := manager.upsertRemoteSession(record.ID, protocol.Session{
		ID:             "sess-1",
		Label:          "Remote",
		Directory:      "/srv/repo",
		State:          protocol.SessionStateWorking,
		StateSince:     "2026-04-03T10:00:00Z",
		StateUpdatedAt: "2026-04-03T10:00:00Z",
		LastSeen:       "2026-04-03T10:00:00Z",
		Todos:          []string{"one"},
	})
	if !changed || count != 1 {
		t.Fatalf("upsertRemoteSession() = (%v, %d), want (true, 1)", changed, count)
	}

	changed, count = manager.upsertRemoteSession(record.ID, protocol.Session{
		ID:             "sess-1",
		Label:          "Remote",
		Directory:      "/srv/repo",
		State:          protocol.SessionStateIdle,
		StateSince:     "2026-04-03T10:05:00Z",
		StateUpdatedAt: "2026-04-03T10:05:00Z",
		LastSeen:       "2026-04-03T10:05:00Z",
		Todos:          []string{"one", "two"},
	})
	if !changed || count != 1 {
		t.Fatalf("upsertRemoteSession(update) = (%v, %d), want (true, 1)", changed, count)
	}

	sessions := manager.RemoteSessions()
	if len(sessions) != 1 {
		t.Fatalf("RemoteSessions() len = %d, want 1", len(sessions))
	}
	if sessions[0].State != protocol.SessionStateIdle {
		t.Fatalf("RemoteSessions()[0].State = %q, want %q", sessions[0].State, protocol.SessionStateIdle)
	}
	if len(sessions[0].Todos) != 2 {
		t.Fatalf("RemoteSessions()[0].Todos len = %d, want 2", len(sessions[0].Todos))
	}

	if changed := manager.clearRemoteSessions(record.ID); !changed {
		t.Fatal("clearRemoteSessions() reported no change")
	}
	if got := manager.RemoteSessions(); len(got) != 0 {
		t.Fatalf("RemoteSessions() len after clear = %d, want 0", len(got))
	}
}

func TestManagerRemoteWorkspacesTrackAndClear(t *testing.T) {
	endpointStore := store.New()
	first, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint(first) error = %v", err)
	}
	second, err := endpointStore.AddEndpoint("dev-box", "dev", "")
	if err != nil {
		t.Fatalf("AddEndpoint(second) error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)

	if changed, count := manager.upsertRemoteSession(first.ID, protocol.Session{ID: "sess-a", Directory: "/srv/repo"}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession(first) = (%v, %d), want (true, 1)", changed, count)
	}
	if changed, count := manager.upsertRemoteSession(second.ID, protocol.Session{ID: "sess-b", Directory: "/srv/repo"}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession(second) = (%v, %d), want (true, 1)", changed, count)
	}

	if changed := manager.replaceRemoteWorkspaces(first.ID, []protocol.Workspace{{
		ID:        "ws-a",
		Title:     "GPU review",
		Directory: "/srv/repo",
		Status:    protocol.WorkspaceStatusWorking,
		Layout: &protocol.WorkspaceLayout{
			WorkspaceID:  "ws-a",
			ActivePaneID: "pane-session",
			LayoutJson:   `{"type":"pane","paneId":"pane-session"}`,
			Panes: []protocol.WorkspaceLayoutPane{{
				PaneID:    "pane-session",
				Kind:      protocol.WorkspaceLayoutPaneKindAgent,
				Title:     "Agent",
				RuntimeID: protocol.Ptr("sess-a"),
				SessionID: protocol.Ptr("sess-a"),
			}, {
				PaneID:    "agent-2",
				Kind:      protocol.WorkspaceLayoutPaneKindAgent,
				Title:     "Agent 2",
				RuntimeID: protocol.Ptr("sess-b"),
				SessionID: protocol.Ptr("sess-b"),
			}},
		},
	}}); !changed {
		t.Fatal("replaceRemoteWorkspaces(first) reported no change")
	}
	if changed := manager.replaceRemoteWorkspaces(second.ID, []protocol.Workspace{{
		ID:        "ws-b",
		Title:     "DEV fix",
		Directory: "/srv/repo",
		Status:    protocol.WorkspaceStatusIdle,
		Layout: &protocol.WorkspaceLayout{
			WorkspaceID:  "ws-b",
			ActivePaneID: "pane-session",
			LayoutJson:   `{"type":"pane","paneId":"pane-session"}`,
			Panes: []protocol.WorkspaceLayoutPane{{
				PaneID:    "pane-session",
				Kind:      protocol.WorkspaceLayoutPaneKindAgent,
				Title:     "Agent",
				RuntimeID: protocol.Ptr("sess-b"),
				SessionID: protocol.Ptr("sess-b"),
			}},
		}},
	}); !changed {
		t.Fatal("replaceRemoteWorkspaces(second) reported no change")
	}

	got := manager.RemoteWorkspaces()
	if len(got) != 2 {
		t.Fatalf("RemoteWorkspaces() len = %d, want 2", len(got))
	}
	if got[0].ID != "ws-a" || got[1].ID != "ws-b" {
		t.Fatalf("RemoteWorkspaces() ids = %q, %q, want ws-a, ws-b", got[0].ID, got[1].ID)
	}

	if endpointID, ok := manager.EndpointIDForSession("missing"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForSession(missing) = (%q, %v), want ('', false)", endpointID, ok)
	}

	_, _ = manager.upsertRemoteSession(first.ID, protocol.Session{ID: "sess-a", Directory: "/srv/repo", State: protocol.SessionStateWorking})
	if endpointID, ok := manager.EndpointIDForSession("sess-a"); !ok || endpointID != first.ID {
		t.Fatalf("EndpointIDForSession(sess-a) = (%q, %v), want (%q, true)", endpointID, ok, first.ID)
	}
	if endpointID, ok := manager.EndpointIDForWorkspace("ws-a"); !ok || endpointID != first.ID {
		t.Fatalf("EndpointIDForWorkspace(ws-a) = (%q, %v), want (%q, true)", endpointID, ok, first.ID)
	}
	workspace := manager.RemoteWorkspace("ws-a")
	if workspace == nil || workspace.Layout == nil || workspace.Layout.WorkspaceID != "ws-a" {
		t.Fatalf("RemoteWorkspace(ws-a) = %+v", workspace)
	}
	workspace.Layout.Panes[0].Title = "mutated copy"
	if fresh := manager.RemoteWorkspace("ws-a"); fresh == nil || fresh.Layout.Panes[0].Title == "mutated copy" {
		t.Fatal("RemoteWorkspace returned shared layout state")
	}
	if endpointID, ok := manager.EndpointIDForPTYTarget("sess-a"); !ok || endpointID != first.ID {
		t.Fatalf("EndpointIDForPTYTarget(sess-a) = (%q, %v), want (%q, true)", endpointID, ok, first.ID)
	}

	if changed := manager.clearRemoteWorkspaceLayouts(first.ID); !changed {
		t.Fatal("clearRemoteWorkspaceLayouts(first) reported no change")
	}
	got = manager.RemoteWorkspaces()
	if len(got) != 1 || got[0].ID != "ws-b" {
		t.Fatalf("RemoteWorkspaces() after clear = %+v, want only ws-b", got)
	}
}

func TestManagerIgnoresLayoutUpdatesForRemovedRemoteWorkspaces(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	if changed := manager.upsertRemoteWorkspace(record.ID, protocol.Workspace{ID: "ws-1", Directory: "/srv/repo"}); !changed {
		t.Fatal("upsertRemoteWorkspace() reported no change")
	}
	if changed := manager.upsertRemoteWorkspaceLayout(record.ID, protocol.WorkspaceLayout{
		WorkspaceID:  "ws-1",
		ActivePaneID: "pane-session",
		LayoutJson:   `{"type":"pane","paneId":"pane-session"}`,
	}); !changed {
		t.Fatal("upsertRemoteWorkspaceLayout() reported no change")
	}

	if changed := manager.removeRemoteWorkspace(record.ID, "ws-1"); !changed {
		t.Fatal("removeRemoteWorkspace() reported no change")
	}
	if changed := manager.upsertRemoteWorkspaceLayout(record.ID, protocol.WorkspaceLayout{
		WorkspaceID:  "ws-1",
		ActivePaneID: "pane-session",
		LayoutJson:   `{"type":"pane","paneId":"pane-session"}`,
	}); changed {
		t.Fatal("upsertRemoteWorkspaceLayout() should ignore removed workspace")
	}
	if got := manager.RemoteWorkspaces(); len(got) != 0 {
		t.Fatalf("RemoteWorkspaces() = %+v, want empty after stale workspace update", got)
	}
}

func TestManagerForgetSessionLeavesRemoteWorkspaceUntilWorkspaceEvent(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	if changed, count := manager.upsertRemoteSession(record.ID, protocol.Session{ID: "sess-1", Directory: "/srv/repo"}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession() = (%v, %d), want (true, 1)", changed, count)
	}
	if changed := manager.upsertRemoteWorkspace(record.ID, protocol.Workspace{ID: "ws-1", Directory: "/srv/repo"}); !changed {
		t.Fatal("upsertRemoteWorkspace() reported no change")
	}
	if changed := manager.upsertRemoteWorkspaceLayout(record.ID, protocol.WorkspaceLayout{
		WorkspaceID:  "ws-1",
		ActivePaneID: "pane-session",
		LayoutJson:   `{"type":"pane","paneId":"pane-session"}`,
	}); !changed {
		t.Fatal("upsertRemoteWorkspaceLayout() reported no change")
	}

	session := manager.RemoteSession("sess-1")
	if session == nil || session.ID != "sess-1" {
		t.Fatalf("RemoteSession(sess-1) = %+v, want session", session)
	}

	if changed := manager.ForgetSession("sess-1"); !changed {
		t.Fatal("ForgetSession() reported no change")
	}
	if got := manager.RemoteSession("sess-1"); got != nil {
		t.Fatalf("RemoteSession(sess-1) after forget = %+v, want nil", got)
	}
	if got := manager.RemoteWorkspaces(); len(got) != 1 || got[0].ID != "ws-1" {
		t.Fatalf("RemoteWorkspaces() after forget = %+v, want retained workspace", got)
	}
	if endpointID, ok := manager.EndpointIDForSession("sess-1"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForSession(sess-1) after forget = (%q, %v), want ('', false)", endpointID, ok)
	}
}

func TestManagerPendingSessionRouteReservesSpawnEndpoint(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	manager.ReservePendingSessionRoute(record.ID, "sess-pending")

	if endpointID, ok := manager.EndpointIDForPTYTarget("sess-pending"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForPTYTarget(sess-pending) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}

	if changed, count := manager.upsertRemoteSession(record.ID, protocol.Session{
		ID:        "sess-pending",
		Label:     "Remote",
		Directory: "/srv/repo",
		State:     protocol.SessionStateLaunching,
		LastSeen:  "2026-04-03T12:00:00Z",
	}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession() = (%v, %d), want (true, 1)", changed, count)
	}

	manager.mu.RLock()
	_, stillPending := manager.pending["sess-pending"]
	manager.mu.RUnlock()
	if stillPending {
		t.Fatal("pending route should be cleared after remote session registration")
	}
}

func TestManagerPendingSessionRouteExpires(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	manager.mu.Lock()
	manager.pending["sess-expired"] = pendingSessionRoute{
		endpointID: record.ID,
		expiresAt:  time.Now().Add(-time.Second),
	}
	manager.mu.Unlock()

	if endpointID, ok := manager.EndpointIDForPTYTarget("sess-expired"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForPTYTarget(sess-expired) = (%q, %v), want ('', false)", endpointID, ok)
	}
}

func TestManagerEndpointIDForPathMatchesSessionDirectoryAndMainRepo(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	_, _ = manager.upsertRemoteSession(record.ID, protocol.Session{
		ID:        "sess-1",
		Directory: "/srv/projects/worktree-a",
		MainRepo:  protocol.Ptr("/srv/projects/repo"),
		State:     protocol.SessionStateWorking,
	})

	if endpointID, ok := manager.EndpointIDForPath("/srv/projects/worktree-a"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForPath(worktree) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}
	if endpointID, ok := manager.EndpointIDForPath("/srv/projects/repo"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForPath(main repo) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}
	if endpointID, ok := manager.EndpointIDForPath("/missing"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForPath(missing) = (%q, %v), want ('', false)", endpointID, ok)
	}
}

func TestCapabilitiesFromInitialStateIncludesRemoteWebFields(t *testing.T) {
	caps := capabilitiesFromInitialState(&protocol.InitialStateMessage{
		ProtocolVersion:  protocol.Ptr("49"),
		DaemonInstanceID: protocol.Ptr("d-123"),
		Settings: map[string]interface{}{
			"codex_available":    "true",
			"snipe_available":    "true",
			"tailscale_enabled":  "true",
			"tailscale_status":   "running",
			"tailscale_url":      "https://gpu-box.tail.ts.net/",
			"tailscale_domain":   "gpu-box.tail.ts.net",
			"tailscale_auth_url": "https://login.tailscale.example/auth",
			"tailscale_error":    "",
			"projects_directory": "/srv/projects",
			"pty_backend_mode":   "worker",
		},
	})
	if caps == nil {
		t.Fatal("capabilitiesFromInitialState() = nil")
	}
	if got := strings.Join(caps.AgentsAvailable, ","); got != "codex,snipe" {
		t.Fatalf("caps.AgentsAvailable = %q, want dynamic plugin agent", got)
	}
	if protocol.Deref(caps.TailscaleEnabled) != true {
		t.Fatalf("caps.TailscaleEnabled = %v, want true", protocol.Deref(caps.TailscaleEnabled))
	}
	if got := protocol.Deref(caps.TailscaleStatus); got != "running" {
		t.Fatalf("caps.TailscaleStatus = %q, want running", got)
	}
	if got := protocol.Deref(caps.TailscaleURL); got != "https://gpu-box.tail.ts.net/" {
		t.Fatalf("caps.TailscaleURL = %q, want remote URL", got)
	}
	if got := protocol.Deref(caps.TailscaleDomain); got != "gpu-box.tail.ts.net" {
		t.Fatalf("caps.TailscaleDomain = %q, want DNS name", got)
	}
	if got := protocol.Deref(caps.TailscaleAuthURL); got != "https://login.tailscale.example/auth" {
		t.Fatalf("caps.TailscaleAuthURL = %q, want auth URL", got)
	}
}

func TestManagerHandleRemoteSettingsUpdatedRefreshesDynamicAgentAvailability(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	manager.mu.Lock()
	manager.runtimes[record.ID].info.Capabilities = &protocol.EndpointCapabilities{
		AgentsAvailable: []string{"codex"},
	}
	manager.mu.Unlock()

	manager.handleRemoteSettingsUpdated(record.ID, &protocol.SettingsUpdatedMessage{
		Settings: map[string]interface{}{
			"codex_available": "true",
			"snipe_available": "true",
		},
	})
	if got := strings.Join(manager.List()[0].Capabilities.AgentsAvailable, ","); got != "codex,snipe" {
		t.Fatalf("agents after plugin registration = %q, want codex,snipe", got)
	}

	manager.handleRemoteSettingsUpdated(record.ID, &protocol.SettingsUpdatedMessage{
		Settings: map[string]interface{}{
			"codex_available": "true",
		},
	})
	if got := strings.Join(manager.List()[0].Capabilities.AgentsAvailable, ","); got != "codex" {
		t.Fatalf("agents after plugin disconnect = %q, want codex", got)
	}
}

func TestManagerHandleRemoteSettingsUpdatedResolvesRemoteWebAction(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	manager.mu.Lock()
	runtime := manager.runtimes[record.ID]
	runtime.info.Capabilities = &protocol.EndpointCapabilities{
		ProtocolVersion: "49",
		AgentsAvailable: []string{"codex"},
	}
	pending := &pendingRemoteWebAction{
		desiredEnabled: true,
		done:           make(chan error, 1),
	}
	runtime.pendingRemoteWeb = pending
	manager.mu.Unlock()

	manager.handleRemoteSettingsUpdated(record.ID, &protocol.SettingsUpdatedMessage{
		ChangedKey: protocol.Ptr("tailscale_enabled"),
		Settings: map[string]interface{}{
			"tailscale_enabled": "true",
			"tailscale_status":  "running",
			"tailscale_url":     "https://gpu-box.tail.ts.net/",
		},
	})

	select {
	case err := <-pending.done:
		if err != nil {
			t.Fatalf("pending.done returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending remote web action did not resolve")
	}

	endpoints := manager.List()
	if len(endpoints) != 1 {
		t.Fatalf("List() len = %d, want 1", len(endpoints))
	}
	if got := protocol.Deref(endpoints[0].Capabilities.TailscaleStatus); got != "running" {
		t.Fatalf("endpoint remote web status = %q, want running", got)
	}
}

func TestManagerHandleRemoteSettingsUpdatedIgnoresUnrelatedPendingRemoteWebUpdates(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	manager.mu.Lock()
	runtime := manager.runtimes[record.ID]
	runtime.info.Capabilities = &protocol.EndpointCapabilities{
		ProtocolVersion:  "49",
		AgentsAvailable:  []string{"codex"},
		TailscaleEnabled: protocol.Ptr(false),
	}
	pending := &pendingRemoteWebAction{
		desiredEnabled: true,
		done:           make(chan error, 1),
	}
	runtime.pendingRemoteWeb = pending
	manager.mu.Unlock()

	manager.handleRemoteSettingsUpdated(record.ID, &protocol.SettingsUpdatedMessage{
		ChangedKey: protocol.Ptr("projects_directory"),
		Settings: map[string]interface{}{
			"projects_directory": "/srv/projects",
		},
	})

	select {
	case err := <-pending.done:
		t.Fatalf("pending.done resolved unexpectedly: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	manager.mu.RLock()
	stillPending := manager.runtimes[record.ID].pendingRemoteWeb == pending
	manager.mu.RUnlock()
	if !stillPending {
		t.Fatal("pending remote web action was cleared by unrelated settings update")
	}
}

func TestForwardsRawEventIncludesPickerResults(t *testing.T) {
	for _, event := range []string{
		protocol.EventRecentLocationsResult,
		protocol.EventBrowseDirectoryResult,
		protocol.EventInspectPathResult,
		protocol.EventWorkspaceTileContent,
	} {
		if !forwardsRawEvent(event) {
			t.Fatalf("forwardsRawEvent(%q) = false, want true", event)
		}
	}
}

// The remote daemon rejects every capability-gated command (register_workspace,
// spawn_session, forwarded client payloads, ...) from a connection that never
// sent client_hello. The hub's persistent endpoint connection must therefore
// declare workspace_sessions before anything else is written.
func TestSendClientHelloDeclaresWorkspaceSessions(t *testing.T) {
	received := make(chan protocol.ClientHelloMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("Read() error = %v", err)
			return
		}
		var hello protocol.ClientHelloMessage
		if err := json.Unmarshal(payload, &hello); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
			return
		}
		received <- hello
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := sendClientHello(ctx, conn); err != nil {
		t.Fatalf("sendClientHello() error = %v", err)
	}

	select {
	case hello := <-received:
		if hello.Cmd != protocol.CmdClientHello {
			t.Errorf("cmd = %q, want %q", hello.Cmd, protocol.CmdClientHello)
		}
		if hello.ClientKind != "hub" {
			t.Errorf("client_kind = %q, want %q", hello.ClientKind, "hub")
		}
		if want := "protocol-" + protocol.ProtocolVersion; hello.Version != want {
			t.Errorf("version = %q, want %q", hello.Version, want)
		}
		found := false
		for _, c := range hello.Capabilities {
			if c == protocol.CapabilityWorkspaceSessions {
				found = true
			}
		}
		if !found {
			t.Errorf("capabilities = %v, missing %q", hello.Capabilities, protocol.CapabilityWorkspaceSessions)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for client_hello")
	}
}

// publishConnectionAndSendHello is what runEndpointLoop calls once the
// connection is dialed: it must publish the connection and send the hello as
// a single unit with respect to ForwardEndpointCommand, otherwise a forwarded
// command racing in right after the connection becomes visible could write
// before the hello and get rejected by the remote daemon. This drives that
// race through the real manager/forwarding seam (not just the hello helper in
// isolation) by hammering ForwardEndpointCommand concurrently with the
// publish call and asserting the hello is always the first frame the remote
// sees.
func TestPublishConnectionAndSendHelloOrdersBeforeForwardedCommands(t *testing.T) {
	frames := make(chan []byte, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			frames <- payload
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	manager := NewManager(store.New(), nil, nil, nil, nil)
	manager.runtimes["endpoint-1"] = &endpointRuntime{}

	// Fire ForwardEndpointCommand in a tight loop from before the connection
	// exists until well after publish completes, to give it every chance to
	// win the race against the hello.
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for i := 0; i < 500; i++ {
			_ = manager.ForwardEndpointCommand(ctx, "endpoint-1", []byte(`{"cmd":"forwarded"}`))
		}
	}()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := manager.publishConnectionAndSendHello(ctx, "endpoint-1", conn, nil); err != nil {
		t.Fatalf("publishConnectionAndSendHello() error = %v", err)
	}

	<-forwardDone

	var first []byte
	select {
	case first = <-frames:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first frame")
	}
	// Drain any commands the forwarder managed to send after the hello so the
	// server goroutine doesn't block on a full channel. Started only after
	// the first frame is captured above, so it can't race the assertion.
	go func() {
		for range frames {
		}
	}()

	var hello protocol.ClientHelloMessage
	if err := json.Unmarshal(first, &hello); err != nil {
		t.Fatalf("Unmarshal() error = %v; first frame = %s", err, first)
	}
	if hello.Cmd != protocol.CmdClientHello {
		t.Fatalf("first frame cmd = %q, want %q (frame: %s)", hello.Cmd, protocol.CmdClientHello, first)
	}
}

func TestManagerForwardBrowserControlReturnsOwningEndpointResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("Read() error = %v", err)
			return
		}
		var request protocol.BrowserControlMessage
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
			return
		}
		if protocol.Deref(request.WorkspaceID) != "remote-workspace" {
			t.Errorf("workspace_id = %q, want remote-workspace", protocol.Deref(request.WorkspaceID))
		}
		response, err := json.Marshal(protocol.BrowserControlResponseMessage{
			Event:     protocol.EventBrowserControlResponse,
			RequestID: protocol.Deref(request.RequestID),
			Success:   true,
			Data:      protocol.Ptr(`{"title":"Remote"}`),
		})
		if err != nil {
			t.Errorf("Marshal() error = %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
			t.Errorf("Write() error = %v", err)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	manager := NewManager(store.New(), nil, nil, nil, nil)
	manager.runtimes["endpoint-1"] = &endpointRuntime{conn: conn}
	go func() {
		_, _ = manager.consumeRemote(ctx, "endpoint-1", conn)
	}()

	data, err := manager.ForwardBrowserControl(ctx, "endpoint-1", protocol.BrowserControlMessage{
		Cmd:         protocol.CmdBrowserControl,
		Action:      "get_title",
		RequestID:   protocol.Ptr("request-1"),
		WorkspaceID: protocol.Ptr("remote-workspace"),
	})
	if err != nil {
		t.Fatalf("ForwardBrowserControl() error = %v", err)
	}
	if data != `{"title":"Remote"}` {
		t.Fatalf("ForwardBrowserControl() data = %q", data)
	}
}

func TestManagerBrowserControlResponseMustComeFromOwningEndpoint(t *testing.T) {
	manager := NewManager(store.New(), nil, nil, nil, nil)
	done := make(chan browserControlResult, 1)
	manager.browserControls["request-1"] = pendingBrowserControl{
		endpointID: "endpoint-1",
		done:       done,
	}
	payload, err := json.Marshal(protocol.BrowserControlResponseMessage{
		Event:     protocol.EventBrowserControlResponse,
		RequestID: "request-1",
		Success:   true,
		Data:      protocol.Ptr("result"),
	})
	if err != nil {
		t.Fatal(err)
	}

	manager.resolveBrowserControl("endpoint-2", payload)
	select {
	case result := <-done:
		t.Fatalf("accepted browser result from wrong endpoint: %+v", result)
	default:
	}

	manager.resolveBrowserControl("endpoint-1", payload)
	select {
	case result := <-done:
		if result.err != nil || result.data != "result" {
			t.Fatalf("browser result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owning endpoint result")
	}
}
