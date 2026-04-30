package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestStore_SaveWorkspacePanelMovesSessionBetweenWorkspaces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.SaveWorkspacePanel("workspace-1", protocol.WorkspacePanel{
		ID:        "panel-1",
		SessionID: "session-1",
		Kind:      "terminal",
		Title:     "Terminal",
		WorldX:    10,
		WorldY:    20,
		Width:     300,
		Height:    200,
	})
	s.SaveWorkspacePanel("workspace-2", protocol.WorkspacePanel{
		ID:        "panel-2",
		SessionID: "session-1",
		Kind:      "terminal",
		Title:     "Moved Terminal",
		WorldX:    30,
		WorldY:    40,
		Width:     500,
		Height:    260,
	})

	if got := s.ListWorkspacePanels("workspace-1"); len(got) != 0 {
		t.Fatalf("workspace-1 panels = %#v, want none", got)
	}

	got := s.ListWorkspacePanels("workspace-2")
	if len(got) != 1 {
		t.Fatalf("workspace-2 panel count = %d, want 1", len(got))
	}
	if got[0].ID != "panel-2" || got[0].SessionID != "session-1" || got[0].Title != "Moved Terminal" {
		t.Fatalf("workspace-2 panel = %#v, want moved panel", got[0])
	}
}
