package store

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspace"
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

	snapshot := workspace.Snapshot{
		SessionID:    "sess-1",
		ActivePaneID: "pane-b",
		Layout: workspace.Node{
			Type:      "split",
			SplitID:   "root",
			Direction: workspace.DirectionVertical,
			Ratio:     workspace.DefaultSplitRatio,
			Children: []workspace.Node{
				{Type: "pane", PaneID: workspace.MainPaneID},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []workspace.Pane{
			{PaneID: workspace.MainPaneID, RuntimeID: "sess-1", Kind: workspace.PaneKindMain, Title: workspace.DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "runtime-b", Kind: workspace.PaneKindShell, Title: "Shell 1"},
		},
	}

	if err := s.SaveWorkspace(snapshot); err != nil {
		t.Fatalf("SaveWorkspace() error = %v", err)
	}

	loaded := s.GetWorkspace("sess-1")
	if loaded == nil {
		t.Fatal("GetWorkspace() = nil, want snapshot")
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

	if err := s.SaveWorkspace(workspace.DefaultSnapshot("sess-1")); err != nil {
		t.Fatalf("SaveWorkspace() error = %v", err)
	}

	s.Remove("sess-1")
	if s.GetWorkspace("sess-1") != nil {
		t.Fatal("GetWorkspace() != nil after session removal")
	}
}
