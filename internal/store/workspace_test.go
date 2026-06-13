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

func TestUpdateWorkspaceTitle(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Project", Directory: "/tmp/project"})

	s.UpdateWorkspaceTitle("workspace-1", "Renamed")

	got := s.GetWorkspace("workspace-1")
	if got == nil || got.Title != "Renamed" {
		t.Fatalf("workspace title after rename = %+v, want Renamed", got)
	}
	// Other columns must be left intact.
	if got.Directory != "/tmp/project" {
		t.Fatalf("directory changed during rename = %q", got.Directory)
	}
}

func TestSetWorkspaceRank(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Project", Directory: "/tmp/project", Rank: "a"})

	s.UpdateWorkspaceRank("workspace-1", "m")

	got := s.GetWorkspace("workspace-1")
	if got == nil || got.Rank != "m" {
		t.Fatalf("workspace rank after update = %+v, want m", got)
	}
	// Other columns must be left intact.
	if got.Title != "Project" || got.Directory != "/tmp/project" {
		t.Fatalf("non-rank columns changed during rank update = %+v", got)
	}
}

func TestListWorkspacesOrderedByRank(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	// Insert out of rank order; created_at advances in insert order, so a stable
	// ORDER BY created_at would yield c, a, b. ORDER BY rank must yield a, b, c.
	s.AddWorkspace(&protocol.Workspace{ID: "ws-c", Title: "C", Directory: "/tmp/c", Rank: "c"})
	s.AddWorkspace(&protocol.Workspace{ID: "ws-a", Title: "A", Directory: "/tmp/a", Rank: "a"})
	s.AddWorkspace(&protocol.Workspace{ID: "ws-b", Title: "B", Directory: "/tmp/b", Rank: "b"})

	list := s.ListWorkspaces()
	if len(list) != 3 {
		t.Fatalf("ListWorkspaces() returned %d workspaces, want 3", len(list))
	}
	wantOrder := []string{"ws-a", "ws-b", "ws-c"}
	for i, ws := range list {
		if ws.ID != wantOrder[i] {
			t.Fatalf("ListWorkspaces()[%d] = %s, want %s (full order: %v)", i, ws.ID, wantOrder[i], idsOf(list))
		}
	}
}

func idsOf(list []*protocol.Workspace) []string {
	ids := make([]string, len(list))
	for i, ws := range list {
		ids[i] = ws.ID
	}
	return ids
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
