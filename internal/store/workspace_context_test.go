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
	if _, _, err := s.ApplyKeeperCompactResult(
		"workspace-1", "compacted", "attn-keeper", 1, "codex", "gpt-test",
	); err != nil {
		t.Fatalf("ApplyKeeperCompactResult error: %v", err)
	}
	s.RemoveWorkspace("workspace-1")
	if s.HasWorkspaceContext("workspace-1") {
		t.Fatal("workspace context survived explicit workspace removal")
	}
	if _, err := s.GetKeeperCompactBackup("workspace-1"); !errors.Is(err, ErrKeeperCompactBackupNotFound) {
		t.Fatalf("backup error = %v, want not found", err)
	}
}

func TestKeeperCompactApplyAndRollback(t *testing.T) {
	s := New()
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Workspace", Directory: t.TempDir()})
	source := "# Workspace Context\n\n## Area\n\nShared work.\n\n## Current Picture\n\nA longer current picture.\n"
	compacted := "# Workspace Context\n\n## Area\n\nShared work.\n\n## Current Picture\n\nCurrent.\n"
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", source, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	updated, changed, err := s.ApplyKeeperCompactResult(
		"workspace-1",
		compacted,
		"attn-keeper",
		1,
		"codex",
		"gpt-test",
	)
	if err != nil {
		t.Fatalf("ApplyKeeperCompactResult error: %v", err)
	}
	if !changed || updated.Revision != 2 || updated.UpdatedBySessionID != "attn-keeper" {
		t.Fatalf("updated = %+v, changed=%v", updated, changed)
	}
	backup, err := s.GetKeeperCompactBackup("workspace-1")
	if err != nil {
		t.Fatalf("GetKeeperCompactBackup error: %v", err)
	}
	if backup.SourceRevision != 1 || backup.SourceContent != source ||
		backup.ResultRevision != 2 || backup.Agent != "codex" || backup.Model != "gpt-test" {
		t.Fatalf("backup = %+v", backup)
	}

	restored, err := s.RestoreKeeperCompactBackup("workspace-1", "session-1")
	if err != nil {
		t.Fatalf("RestoreKeeperCompactBackup error: %v", err)
	}
	if restored.Content != source || restored.Revision != 3 || restored.UpdatedBySessionID != "session-1" {
		t.Fatalf("restored = %+v", restored)
	}
}

func TestKeeperCompactRollbackRejectsLaterEdit(t *testing.T) {
	s := New()
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Workspace", Directory: t.TempDir()})
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", "source", "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if _, _, err := s.ApplyKeeperCompactResult(
		"workspace-1", "compacted", "attn-keeper", 1, "claude", "claude-test",
	); err != nil {
		t.Fatalf("compact context: %v", err)
	}
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", "later edit", "session-2", 2); err != nil {
		t.Fatalf("later edit: %v", err)
	}
	if _, err := s.RestoreKeeperCompactBackup("workspace-1", "session-1"); !errors.Is(err, ErrWorkspaceContextConflict) {
		t.Fatalf("rollback error = %v, want conflict", err)
	}
}

func TestKeeperCompactBackupSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "Workspace", Directory: t.TempDir()})
	source := "# Workspace Context\n\n## Area\n\nArea.\n\n## Current Picture\n\nLong current picture.\n"
	compacted := "# Workspace Context\n\n## Area\n\nArea.\n\n## Current Picture\n\nCurrent.\n"
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", source, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if _, _, err := s.ApplyKeeperCompactResult(
		"workspace-1", compacted, "attn-keeper", 1, "claude", "sonnet",
	); err != nil {
		t.Fatalf("compact context: %v", err)
	}
	s.Close()

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	backup, err := reopened.GetKeeperCompactBackup("workspace-1")
	if err != nil {
		t.Fatalf("get reopened backup: %v", err)
	}
	if backup.SourceContent != source || backup.ResultRevision != 2 ||
		backup.Agent != "claude" || backup.Model != "sonnet" {
		t.Fatalf("backup = %+v", backup)
	}
	restored, err := reopened.RestoreKeeperCompactBackup("workspace-1", "session-1")
	if err != nil {
		t.Fatalf("restore reopened backup: %v", err)
	}
	if restored.Content != source || restored.Revision != 3 {
		t.Fatalf("restored = %+v", restored)
	}
}
