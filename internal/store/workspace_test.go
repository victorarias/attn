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
		Directory: "/tmp/project", WorkspaceID: protocol.Ptr("workspace-1"),
		State: protocol.SessionStateIdle, StateSince: string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()), LastSeen: string(protocol.TimestampNow()),
	})

	got := s.GetWorkspace("workspace-1")
	if got == nil || got.Title != "Project" {
		t.Fatalf("GetWorkspace() = %#v, want persisted workspace", got)
	}
	members := s.SessionsInWorkspace("workspace-1")
	if len(members) != 1 || members[0] != "session-1" {
		t.Fatalf("SessionsInWorkspace() = %#v, want session-1", members)
	}
}
