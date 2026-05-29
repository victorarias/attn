package store

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func TestWorkspaceSaveLoadRoundTrip(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	s.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "Session 1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Session 1", Directory: "/tmp/sess-1"})

	snapshot := workspacelayout.WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-b",
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "root",
			Direction: workspacelayout.DirectionVertical,
			Ratio:     workspacelayout.DefaultSplitRatio,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: workspacelayout.MainPaneID},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []workspacelayout.Pane{
			{PaneID: workspacelayout.MainPaneID, RuntimeID: "sess-1", SessionID: "sess-1", Kind: workspacelayout.PaneKindAgent, Title: workspacelayout.DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "runtime-b", Kind: workspacelayout.PaneKindShell, Title: "Shell 1"},
		},
	}

	if err := s.SaveWorkspaceLayout(snapshot); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	loaded := s.GetWorkspaceLayout("workspace-1")
	if loaded == nil {
		t.Fatal("GetWorkspaceLayout() = nil, want snapshot")
	}
	if loaded.ActivePaneID != "pane-b" {
		t.Fatalf("ActivePaneID = %q, want pane-b", loaded.ActivePaneID)
	}
	if len(loaded.Panes) != 2 {
		t.Fatalf("len(Panes) = %d, want 2", len(loaded.Panes))
	}
	if loaded.Layout.Type != "split" || len(loaded.Layout.Children) != 2 {
		t.Fatalf("Layout = %+v, want split with 2 children", loaded.Layout)
	}

	panes := s.ListWorkspaceLayoutPanes("workspace-1")
	if len(panes) != 2 {
		t.Fatalf("ListWorkspaceLayoutPanes() len = %d, want 2", len(panes))
	}
	if panes[0].PaneID != workspacelayout.MainPaneID || panes[1].PaneID != "pane-b" {
		t.Fatalf("ListWorkspaceLayoutPanes() = %+v, want stable pane order", panes)
	}
	if workspaceID, paneID, ok := s.FindWorkspaceLayoutPaneBySessionID("sess-1"); !ok || workspaceID != "workspace-1" || paneID != workspacelayout.MainPaneID {
		t.Fatalf("FindWorkspaceLayoutPaneBySessionID() = (%q, %q, %v), want workspace-1/main/true", workspaceID, paneID, ok)
	}
	if workspaceID, paneID, ok := s.FindWorkspaceLayoutPaneByRuntimeID("runtime-b"); !ok || workspaceID != "workspace-1" || paneID != "pane-b" {
		t.Fatalf("FindWorkspaceLayoutPaneByRuntimeID() = (%q, %q, %v), want workspace-1/pane-b/true", workspaceID, paneID, ok)
	}
}

func TestRemoveWorkspaceDeletesLayoutRows(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	s.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "Session 1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Session 1", Directory: "/tmp/sess-1"})

	if err := s.SaveWorkspaceLayout(workspacelayout.DefaultWorkspaceLayout("workspace-1", "sess-1")); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	s.RemoveWorkspace("workspace-1")
	if s.GetWorkspaceLayout("workspace-1") != nil {
		t.Fatal("GetWorkspaceLayout() != nil after session removal")
	}
}

func TestInMemoryFallbackSupportsSessionWorkspaceAndState(t *testing.T) {
	s := &Store{
		sessions:        make(map[string]*protocol.Session),
		workspaces:      make(map[string]workspacelayout.WorkspaceLayout),
		recentLocations: make(map[string]*protocol.RecentLocation),
	}

	s.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "Session 1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateLaunching,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})

	snapshot := workspacelayout.DefaultWorkspaceLayout("workspace-1", "sess-1")
	if err := s.SaveWorkspaceLayout(snapshot); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	got := s.Get("sess-1")
	if got == nil {
		t.Fatal("Get() = nil, want session")
	}
	if got.State != protocol.SessionStateLaunching {
		t.Fatalf("state = %s, want launching", got.State)
	}

	loadedWorkspace := s.GetWorkspaceLayout("workspace-1")
	if loadedWorkspace == nil {
		t.Fatal("GetWorkspaceLayout() = nil, want snapshot")
	}
	if loadedWorkspace.ActivePaneID != workspacelayout.MainPaneID {
		t.Fatalf("ActivePaneID = %q, want %q", loadedWorkspace.ActivePaneID, workspacelayout.MainPaneID)
	}

	s.UpdateState("sess-1", protocol.StateWaitingInput)
	got = s.Get("sess-1")
	if got == nil || got.State != protocol.SessionStateWaitingInput {
		t.Fatalf("state = %v, want waiting_input", got)
	}
}
