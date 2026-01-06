// internal/git/diff_test.go
package git

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Test helper functions (pure functions, no git needed)

func TestParseGitStatus(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"A", "added"},
		{"M", "modified"},
		{"D", "deleted"},
		{"R", "renamed"},
		{"R100", "renamed"},
		{"R050", "renamed"},
		{"C", "copied"},
		{"T", "typechange"},
		{"X", "modified"}, // Unknown defaults to modified
		{"", "modified"},  // Empty defaults to modified
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := parseGitStatus(tt.code)
			if result != tt.expected {
				t.Errorf("parseGitStatus(%q) = %q, want %q", tt.code, result, tt.expected)
			}
		})
	}
}

func TestParseGitPorcelainStatus(t *testing.T) {
	tests := []struct {
		xy       string
		expected string
	}{
		{"??", "untracked"},
		{"A ", "added"},
		{" A", "added"},
		{"AM", "added"},
		{"D ", "deleted"},
		{" D", "deleted"},
		{"R ", "renamed"},
		{" R", "renamed"},
		{"M ", "modified"},
		{" M", "modified"},
		{"MM", "modified"},
		{"  ", "modified"}, // Empty status
		{"", "modified"},   // Too short
	}

	for _, tt := range tests {
		t.Run(tt.xy, func(t *testing.T) {
			result := parseGitPorcelainStatus(tt.xy)
			if result != tt.expected {
				t.Errorf("parseGitPorcelainStatus(%q) = %q, want %q", tt.xy, result, tt.expected)
			}
		})
	}
}

func TestExtractRenamePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Simple rename
		{"old.go => new.go", "new.go"},
		{"src/old.go => src/new.go", "src/new.go"},

		// Brace format - directory rename
		{"{old => new}/file.go", "new/file.go"},
		{"src/{old => new}/file.go", "src/new/file.go"},

		// Brace format - file rename in directory
		{"dir/{old.go => new.go}", "dir/new.go"},
		{"src/dir/{old.go => new.go}", "src/dir/new.go"},

		// No rename (passthrough)
		{"file.go", "file.go"},
		{"src/file.go", "src/file.go"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractRenamePath(tt.input)
			if result != tt.expected {
				t.Errorf("extractRenamePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Integration tests with actual git repos

func TestGetBranchDiffFiles_CommittedChanges(t *testing.T) {
	// Create temp git repo
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Create a branch point (origin/main simulation)
	runGit(t, dir, "branch", "base-point")

	// Make some changes
	writeFile(t, dir, "new-file.go", "package main\n\nfunc main() {}\n")
	runGit(t, dir, "add", "new-file.go")
	runGit(t, dir, "commit", "-m", "add new file")

	// Get diff against base-point
	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}

	if files[0].Path != "new-file.go" {
		t.Errorf("expected path 'new-file.go', got %q", files[0].Path)
	}
	if files[0].Status != "added" {
		t.Errorf("expected status 'added', got %q", files[0].Status)
	}
	if files[0].HasUncommitted {
		t.Error("expected HasUncommitted=false for committed file")
	}
}

func TestGetBranchDiffFiles_UncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	// Create initial file and commit
	writeFile(t, dir, "existing.go", "package main\n")
	runGit(t, dir, "add", "existing.go")
	runGit(t, dir, "commit", "-m", "init")

	// Create base point
	runGit(t, dir, "branch", "base-point")

	// Make uncommitted change
	writeFile(t, dir, "existing.go", "package main\n\n// modified\n")

	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}

	if files[0].Path != "existing.go" {
		t.Errorf("expected path 'existing.go', got %q", files[0].Path)
	}
	if !files[0].HasUncommitted {
		t.Error("expected HasUncommitted=true for uncommitted change")
	}
}

func TestGetBranchDiffFiles_MixedChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	// Create initial file and commit
	writeFile(t, dir, "file1.go", "package main\n")
	runGit(t, dir, "add", "file1.go")
	runGit(t, dir, "commit", "-m", "init")

	// Create base point
	runGit(t, dir, "branch", "base-point")

	// Commit a new file
	writeFile(t, dir, "file2.go", "package util\n")
	runGit(t, dir, "add", "file2.go")
	runGit(t, dir, "commit", "-m", "add file2")

	// Make uncommitted changes to both
	writeFile(t, dir, "file1.go", "package main\n// uncommitted\n")
	writeFile(t, dir, "file2.go", "package util\n// uncommitted\n")

	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	// Sort for consistent comparison
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(files), files)
	}

	// file1.go - only uncommitted changes (not in committed diff)
	if files[0].Path != "file1.go" {
		t.Errorf("expected file1.go, got %q", files[0].Path)
	}
	if !files[0].HasUncommitted {
		t.Error("file1.go should have HasUncommitted=true")
	}

	// file2.go - committed + uncommitted
	if files[1].Path != "file2.go" {
		t.Errorf("expected file2.go, got %q", files[1].Path)
	}
	if files[1].Status != "added" {
		t.Errorf("file2.go should have status 'added', got %q", files[1].Status)
	}
	if !files[1].HasUncommitted {
		t.Error("file2.go should have HasUncommitted=true")
	}
}

func TestGetBranchDiffFiles_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	// Create file and commit
	writeFile(t, dir, "to-delete.go", "package main\n")
	runGit(t, dir, "add", "to-delete.go")
	runGit(t, dir, "commit", "-m", "init")

	// Create base point
	runGit(t, dir, "branch", "base-point")

	// Delete the file
	os.Remove(filepath.Join(dir, "to-delete.go"))
	runGit(t, dir, "add", "to-delete.go")
	runGit(t, dir, "commit", "-m", "delete file")

	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}

	if files[0].Path != "to-delete.go" {
		t.Errorf("expected path 'to-delete.go', got %q", files[0].Path)
	}
	if files[0].Status != "deleted" {
		t.Errorf("expected status 'deleted', got %q", files[0].Status)
	}
}

func TestGetBranchDiffFiles_UntrackedFile(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	runGit(t, dir, "branch", "base-point")

	// Create untracked file
	writeFile(t, dir, "untracked.go", "package main\n")

	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %+v", len(files), files)
	}

	if files[0].Path != "untracked.go" {
		t.Errorf("expected path 'untracked.go', got %q", files[0].Path)
	}
	if files[0].Status != "untracked" {
		t.Errorf("expected status 'untracked', got %q", files[0].Status)
	}
	if !files[0].HasUncommitted {
		t.Error("expected HasUncommitted=true for untracked file")
	}
}

func TestGetBranchDiffFiles_NoChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	runGit(t, dir, "branch", "base-point")

	// No changes since base-point
	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files for no changes, got %d: %+v", len(files), files)
	}
}

func TestGetBranchDiffFiles_InvalidBaseRef(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Create uncommitted file
	writeFile(t, dir, "file.go", "package main\n")

	// Invalid base ref should gracefully fall back to just uncommitted
	files, err := GetBranchDiffFiles(dir, "nonexistent-ref")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	// Should still show the uncommitted file
	if len(files) != 1 {
		t.Fatalf("expected 1 file (uncommitted), got %d: %+v", len(files), files)
	}
}

func TestGetBranchDiffFiles_LineStats(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	runGit(t, dir, "branch", "base-point")

	// Create file with known line count
	writeFile(t, dir, "stats.go", "line1\nline2\nline3\n")
	runGit(t, dir, "add", "stats.go")
	runGit(t, dir, "commit", "-m", "add stats file")

	files, err := GetBranchDiffFiles(dir, "base-point")
	if err != nil {
		t.Fatalf("GetBranchDiffFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	// New file should have additions but no deletions
	if files[0].Additions != 3 {
		t.Errorf("expected 3 additions, got %d", files[0].Additions)
	}
	if files[0].Deletions != 0 {
		t.Errorf("expected 0 deletions, got %d", files[0].Deletions)
	}
}

// writeFile is defined in stash_test.go
