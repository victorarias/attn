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
