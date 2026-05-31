package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestStoreWorkspaceRoundTripAndMembership(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Project", Directory: "/tmp/project"})
	s.Add(&protocol.Session{
		ID: "session-1", Label: "Agent", Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/project", WorkspaceID: "workspace-1",
		State: protocol.SessionStateIdle, StateSince: string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()), LastSeen: string(protocol.TimestampNow()),
	})

	got := s.GetWorkspace("workspace-1")
	if got == nil || got.Title != "Project" {
		t.Fatalf("GetWorkspace() = %#v, want persisted workspace", got)
	}
	if got.Muted {
		t.Fatalf("new workspace muted = true, want false")
	}
	members := s.SessionsInWorkspace("workspace-1")
	if len(members) != 1 || members[0] != "session-1" {
		t.Fatalf("SessionsInWorkspace() = %#v, want session-1", members)
	}
}

func TestToggleWorkspaceMute(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Project", Directory: "/tmp/project"})

	s.ToggleWorkspaceMute("workspace-1")
	if got := s.GetWorkspace("workspace-1"); got == nil || !got.Muted {
		t.Fatalf("workspace after first toggle = %+v, want muted", got)
	}

	s.ToggleWorkspaceMute("workspace-1")
	if got := s.GetWorkspace("workspace-1"); got == nil || got.Muted {
		t.Fatalf("workspace after second toggle = %+v, want unmuted", got)
	}
}

func TestAssignSessionWorkspaceRefusesEmptyWorkspace(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	now := string(protocol.TimestampNow())
	s.Add(&protocol.Session{
		ID: "session-1", Label: "Agent", Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/project", WorkspaceID: "workspace-1",
		State: protocol.SessionStateIdle, StateSince: now,
		StateUpdatedAt: now, LastSeen: now,
	})

	s.AssignSessionWorkspace("session-1", "")

	got := s.Get("session-1")
	if got == nil || got.WorkspaceID != "workspace-1" {
		t.Fatalf("workspace_id after empty assignment = %+v, want workspace-1", got)
	}
}
