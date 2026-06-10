package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestWorkspaceContextRevisionCheckedUpdate(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	initial, err := s.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("GetWorkspaceContext error: %v", err)
	}
	if initial.Revision != 0 || initial.Content != "" {
		t.Fatalf("initial context = %+v", initial)
	}

	unchanged, changed, err := s.UpdateWorkspaceContext("workspace-1", "", "session-1", 0)
	if err != nil {
		t.Fatalf("initial empty UpdateWorkspaceContext error: %v", err)
	}
	if changed || unchanged.Revision != 0 || unchanged.Content != "" {
		t.Fatalf("initial unchanged context = %+v, changed=%v", unchanged, changed)
	}
	if s.HasWorkspaceContext("workspace-1") {
		t.Fatal("initial empty update created a workspace context row")
	}

	updated, changed, err := s.UpdateWorkspaceContext("workspace-1", "# Goal\n", "session-1", 0)
	if err != nil {
		t.Fatalf("UpdateWorkspaceContext error: %v", err)
	}
	if !changed || updated.Revision != 1 || updated.Content != "# Goal\n" {
		t.Fatalf("updated context = %+v, changed=%v", updated, changed)
	}
	if !s.HasWorkspaceContext("workspace-1") {
		t.Fatal("HasWorkspaceContext returned false")
	}

	unchanged, changed, err = s.UpdateWorkspaceContext("workspace-1", "# Goal\n", "session-1", 1)
	if err != nil {
		t.Fatalf("identical UpdateWorkspaceContext error: %v", err)
	}
	if changed || unchanged.Revision != 1 {
		t.Fatalf("unchanged context = %+v, changed=%v", unchanged, changed)
	}

	_, _, err = s.UpdateWorkspaceContext("workspace-1", "# Stale\n", "session-2", 0)
	if !errors.Is(err, ErrWorkspaceContextConflict) {
		t.Fatalf("stale update error = %v, want revision conflict", err)
	}
	current, err := s.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("GetWorkspaceContext after conflict error: %v", err)
	}
	if current.Revision != 1 || current.Content != "# Goal\n" {
		t.Fatalf("context changed after conflict: %+v", current)
	}
}

func TestListWorkspaceContextsUsesWorkspaceCreationOrder(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-b", Title: "B", Directory: "/tmp/b"})
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-a", Title: "A", Directory: "/tmp/a"})

	if _, _, err := s.UpdateWorkspaceContext("workspace-a", "# A", "session-a", 0); err != nil {
		t.Fatalf("UpdateWorkspaceContext workspace-a error: %v", err)
	}
	if _, _, err := s.UpdateWorkspaceContext("workspace-b", "# B", "session-b", 0); err != nil {
		t.Fatalf("UpdateWorkspaceContext workspace-b error: %v", err)
	}

	contexts, err := s.ListWorkspaceContexts()
	if err != nil {
		t.Fatalf("ListWorkspaceContexts error: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("ListWorkspaceContexts len = %d, want 2", len(contexts))
	}
	if contexts[0].WorkspaceID != "workspace-b" || contexts[1].WorkspaceID != "workspace-a" {
		t.Fatalf("ListWorkspaceContexts order = [%s, %s], want [workspace-b, workspace-a]",
			contexts[0].WorkspaceID, contexts[1].WorkspaceID)
	}
}

func TestListWorkspaceContextsExcludesOrphans(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-live", Title: "Live", Directory: "/tmp/live"})
	if _, _, err := s.UpdateWorkspaceContext("workspace-live", "# Live", "session-live", 0); err != nil {
		t.Fatalf("UpdateWorkspaceContext live error: %v", err)
	}
	if _, _, err := s.UpdateWorkspaceContext("workspace-orphan", "# Orphan", "session-old", 0); err != nil {
		t.Fatalf("UpdateWorkspaceContext orphan error: %v", err)
	}

	contexts, err := s.ListWorkspaceContexts()
	if err != nil {
		t.Fatalf("ListWorkspaceContexts error: %v", err)
	}
	if len(contexts) != 1 || contexts[0].WorkspaceID != "workspace-live" {
		t.Fatalf("ListWorkspaceContexts = %+v, want only workspace-live", contexts)
	}
}

func TestRemoveWorkspaceRemovesContext(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Project", Directory: "/tmp/project"})
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", "context", "session-1", 0); err != nil {
		t.Fatalf("UpdateWorkspaceContext error: %v", err)
	}
	s.RemoveWorkspace("workspace-1")
	if s.HasWorkspaceContext("workspace-1") {
		t.Fatal("workspace context survived explicit workspace removal")
	}
}
