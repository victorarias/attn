package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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

	if changed := manager.replaceRemoteSessionLayouts(first.ID, []protocol.SessionLayout{{
		SessionID:    "sess-a",
		ActivePaneID: "main",
		LayoutJson:   `{"type":"pane","paneId":"main"}`,
		Panes: []protocol.SessionLayoutPane{{
			PaneID: "main",
			Kind:   protocol.SessionLayoutPaneKindMain,
			Title:  "Main",
		}, {
			PaneID:    "shell-1",
			Kind:      protocol.SessionLayoutPaneKindShell,
			Title:     "Shell 1",
			RuntimeID: protocol.Ptr("runtime-a"),
		}},
	}}); !changed {
		t.Fatal("replaceRemoteSessionLayouts(first) reported no change")
	}
	if changed := manager.replaceRemoteSessionLayouts(second.ID, []protocol.SessionLayout{{
		SessionID:    "sess-b",
		ActivePaneID: "main",
		LayoutJson:   `{"type":"pane","paneId":"main"}`,
		Panes: []protocol.SessionLayoutPane{{
			PaneID: "main",
			Kind:   protocol.SessionLayoutPaneKindMain,
			Title:  "Main",
		}},
	}}); !changed {
		t.Fatal("replaceRemoteSessionLayouts(second) reported no change")
	}

	got := manager.RemoteWorkspaces()
	if len(got) != 2 {
		t.Fatalf("RemoteWorkspaces() len = %d, want 2", len(got))
	}
	if got[0].SessionID != "sess-a" || got[1].SessionID != "sess-b" {
		t.Fatalf("RemoteWorkspaces() session ids = %q, %q, want sess-a, sess-b", got[0].SessionID, got[1].SessionID)
	}

	if endpointID, ok := manager.EndpointIDForSession("missing"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForSession(missing) = (%q, %v), want ('', false)", endpointID, ok)
	}

	_, _ = manager.upsertRemoteSession(first.ID, protocol.Session{ID: "sess-a", Directory: "/srv/repo", State: protocol.SessionStateWorking})
	if endpointID, ok := manager.EndpointIDForSession("sess-a"); !ok || endpointID != first.ID {
		t.Fatalf("EndpointIDForSession(sess-a) = (%q, %v), want (%q, true)", endpointID, ok, first.ID)
	}
	if endpointID, ok := manager.EndpointIDForPTYTarget("runtime-a"); !ok || endpointID != first.ID {
		t.Fatalf("EndpointIDForPTYTarget(runtime-a) = (%q, %v), want (%q, true)", endpointID, ok, first.ID)
	}

	if changed := manager.clearRemoteSessionLayouts(first.ID); !changed {
		t.Fatal("clearRemoteSessionLayouts(first) reported no change")
	}
	got = manager.RemoteWorkspaces()
	if len(got) != 1 || got[0].SessionID != "sess-b" {
		t.Fatalf("RemoteWorkspaces() after clear = %+v, want only sess-b", got)
	}
}

func TestManagerIgnoresWorkspaceUpdatesForRemovedRemoteSessions(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	if changed, count := manager.upsertRemoteSession(record.ID, protocol.Session{ID: "sess-1", Directory: "/srv/repo"}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession() = (%v, %d), want (true, 1)", changed, count)
	}
	if changed := manager.upsertRemoteSessionLayout(record.ID, protocol.SessionLayout{
		SessionID:    "sess-1",
		ActivePaneID: "main",
		LayoutJson:   `{"type":"pane","paneId":"main"}`,
	}); !changed {
		t.Fatal("upsertRemoteSessionLayout() reported no change")
	}

	if changed, count := manager.removeRemoteSession(record.ID, "sess-1"); !changed || count != 0 {
		t.Fatalf("removeRemoteSession() = (%v, %d), want (true, 0)", changed, count)
	}
	if changed := manager.removeRemoteSessionLayout(record.ID, "sess-1"); !changed {
		t.Fatal("removeRemoteSessionLayout() reported no change")
	}
	if changed := manager.upsertRemoteSessionLayout(record.ID, protocol.SessionLayout{
		SessionID:    "sess-1",
		ActivePaneID: "main",
		LayoutJson:   `{"type":"pane","paneId":"main"}`,
	}); changed {
		t.Fatal("upsertRemoteSessionLayout() should ignore workspace for removed session")
	}
	if got := manager.RemoteWorkspaces(); len(got) != 0 {
		t.Fatalf("RemoteWorkspaces() = %+v, want empty after stale workspace update", got)
	}
}

func TestManagerForgetSessionRemovesRemoteSessionAndWorkspace(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)
	if changed, count := manager.upsertRemoteSession(record.ID, protocol.Session{ID: "sess-1", Directory: "/srv/repo"}); !changed || count != 1 {
		t.Fatalf("upsertRemoteSession() = (%v, %d), want (true, 1)", changed, count)
	}
	if changed := manager.upsertRemoteSessionLayout(record.ID, protocol.SessionLayout{
		SessionID:    "sess-1",
		ActivePaneID: "main",
		LayoutJson:   `{"type":"pane","paneId":"main"}`,
	}); !changed {
		t.Fatal("upsertRemoteSessionLayout() reported no change")
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
	if got := manager.RemoteWorkspaces(); len(got) != 0 {
		t.Fatalf("RemoteWorkspaces() after forget = %+v, want empty", got)
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
	} {
		if !forwardsRawEvent(event) {
			t.Fatalf("forwardsRawEvent(%q) = false, want true", event)
		}
	}
}

func TestManagerObserveRemoteEventCachesReviewCommentAndLoopOwnership(t *testing.T) {
	endpointStore := store.New()
	record, err := endpointStore.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}

	manager := NewManager(endpointStore, nil, nil, nil, nil)

	reviewStatePayload, err := json.Marshal(protocol.GetReviewStateResultMessage{
		Event:   protocol.EventGetReviewStateResult,
		Success: true,
		State: &protocol.ReviewState{
			ReviewID: "review-1",
			RepoPath: "/srv/repo",
			Branch:   "main",
		},
	})
	if err != nil {
		t.Fatalf("Marshal(review state) error = %v", err)
	}
	manager.observeRemoteEvent(record.ID, protocol.EventGetReviewStateResult, reviewStatePayload)

	commentPayload, err := json.Marshal(protocol.AddCommentResultMessage{
		Event:   protocol.EventAddCommentResult,
		Success: true,
		Comment: &protocol.ReviewComment{
			ID:       "comment-1",
			ReviewID: "review-1",
		},
	})
	if err != nil {
		t.Fatalf("Marshal(add comment) error = %v", err)
	}
	manager.observeRemoteEvent(record.ID, protocol.EventAddCommentResult, commentPayload)

	loopPayload, err := json.Marshal(protocol.ReviewLoopResultMessage{
		Event:   protocol.EventReviewLoopResult,
		Success: true,
		ReviewLoopRun: &protocol.ReviewLoopRun{
			LoopID:          "loop-1",
			SourceSessionID: "sess-1",
			RepoPath:        "/srv/repo",
			ResolvedPrompt:  "review",
			Status:          protocol.ReviewLoopRunStatusRunning,
			CreatedAt:       "2026-04-03T00:00:00Z",
			UpdatedAt:       "2026-04-03T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("Marshal(review loop) error = %v", err)
	}
	manager.observeRemoteEvent(record.ID, protocol.EventReviewLoopResult, loopPayload)

	if endpointID, ok := manager.EndpointIDForReview("review-1"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForReview(review-1) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}
	if endpointID, ok := manager.EndpointIDForComment("comment-1"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForComment(comment-1) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}
	if endpointID, ok := manager.EndpointIDForReviewLoop("loop-1"); !ok || endpointID != record.ID {
		t.Fatalf("EndpointIDForReviewLoop(loop-1) = (%q, %v), want (%q, true)", endpointID, ok, record.ID)
	}

	manager.mu.Lock()
	runtime := manager.runtimes[record.ID]
	manager.stopRuntimeLocked(runtime)
	manager.mu.Unlock()

	if endpointID, ok := manager.EndpointIDForReview("review-1"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForReview(after stop) = (%q, %v), want ('', false)", endpointID, ok)
	}
	if endpointID, ok := manager.EndpointIDForComment("comment-1"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForComment(after stop) = (%q, %v), want ('', false)", endpointID, ok)
	}
	if endpointID, ok := manager.EndpointIDForReviewLoop("loop-1"); ok || endpointID != "" {
		t.Fatalf("EndpointIDForReviewLoop(after stop) = (%q, %v), want ('', false)", endpointID, ok)
	}
}
