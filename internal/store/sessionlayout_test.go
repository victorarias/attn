package store

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionlayout"
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

	snapshot := sessionlayout.SessionLayout{
		SessionID:    "sess-1",
		ActivePaneID: "pane-b",
		Layout: sessionlayout.Node{
			Type:      "split",
			SplitID:   "root",
			Direction: sessionlayout.DirectionVertical,
			Ratio:     sessionlayout.DefaultSplitRatio,
			Children: []sessionlayout.Node{
				{Type: "pane", PaneID: sessionlayout.MainPaneID},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []sessionlayout.Pane{
			{PaneID: sessionlayout.MainPaneID, RuntimeID: "sess-1", Kind: sessionlayout.PaneKindMain, Title: sessionlayout.DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "runtime-b", Kind: sessionlayout.PaneKindShell, Title: "Shell 1"},
		},
	}

	if err := s.SaveSessionLayout(snapshot); err != nil {
		t.Fatalf("SaveSessionLayout() error = %v", err)
	}

	loaded := s.GetSessionLayout("sess-1")
	if loaded == nil {
		t.Fatal("GetSessionLayout() = nil, want snapshot")
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
}

func TestRemoveSessionDeletesWorkspaceRows(t *testing.T) {
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

	if err := s.SaveSessionLayout(sessionlayout.DefaultSessionLayout("sess-1")); err != nil {
		t.Fatalf("SaveSessionLayout() error = %v", err)
	}

	s.Remove("sess-1")
	if s.GetSessionLayout("sess-1") != nil {
		t.Fatal("GetSessionLayout() != nil after session removal")
	}
}

func TestInMemoryFallbackSupportsSessionWorkspaceAndState(t *testing.T) {
	s := &Store{
		sessions:        make(map[string]*protocol.Session),
		workspaces:      make(map[string]sessionlayout.SessionLayout),
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

	snapshot := sessionlayout.DefaultSessionLayout("sess-1")
	if err := s.SaveSessionLayout(snapshot); err != nil {
		t.Fatalf("SaveSessionLayout() error = %v", err)
	}

	got := s.Get("sess-1")
	if got == nil {
		t.Fatal("Get() = nil, want session")
	}
	if got.State != protocol.SessionStateLaunching {
		t.Fatalf("state = %s, want launching", got.State)
	}

	loadedWorkspace := s.GetSessionLayout("sess-1")
	if loadedWorkspace == nil {
		t.Fatal("GetSessionLayout() = nil, want snapshot")
	}
	if loadedWorkspace.ActivePaneID != sessionlayout.MainPaneID {
		t.Fatalf("ActivePaneID = %q, want %q", loadedWorkspace.ActivePaneID, sessionlayout.MainPaneID)
	}

	s.UpdateState("sess-1", protocol.StateWaitingInput)
	got = s.Get("sess-1")
	if got == nil || got.State != protocol.SessionStateWaitingInput {
		t.Fatalf("state = %v, want waiting_input", got)
	}
}
