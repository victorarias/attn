package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLocalCloneChecksExactOrigin(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:Owner/Repo.git")
	main, err := ValidateLocalClone(repo, "github.com/owner/repo")
	if err != nil || main != CanonicalizePath(repo) {
		t.Fatalf("main=%q err=%v", main, err)
	}
	if _, err := ValidateLocalClone(repo, "github.com/other/repo"); err == nil || !strings.Contains(err.Error(), "origin mismatch") {
		t.Fatalf("mismatch err = %v", err)
	}
	runGit(t, repo, "remote", "set-url", "origin", "http://github.com/owner/repo.git")
	if _, err := ValidateLocalClone(repo, "github.com/owner/repo"); err == nil || !strings.Contains(err.Error(), "plaintext HTTP") {
		t.Fatalf("plaintext origin err = %v", err)
	}
}

func TestEnsureManagedCloneDoesNotReplaceMismatchedExistingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "evidence")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureManagedClone("https://github.com/owner/repo.git", target, "github.com/owner/repo", ""); err == nil {
		t.Fatal("expected non-repository target failure")
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("existing target was changed: %q %v", content, err)
	}
}

func TestEnsurePullRequestRevisionKeepsAvailableSnapshotAfterRefMoves(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", origin)
	producer := filepath.Join(root, "producer")
	runGit(t, root, "clone", origin, producer)
	runGit(t, producer, "commit", "--allow-empty", "-m", "first")
	first, err := GetHeadCommit(producer)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, producer, "push", "origin", "HEAD:refs/pull/1/head")
	consumer := filepath.Join(root, "consumer")
	runGit(t, root, "clone", origin, consumer)
	if err := EnsurePullRequestRevision(consumer, "origin", 1, first, ""); err != nil {
		t.Fatal(err)
	}
	runGit(t, producer, "commit", "--allow-empty", "-m", "second")
	runGit(t, producer, "push", "--force", "origin", "HEAD:refs/pull/1/head")
	if err := EnsurePullRequestRevision(consumer, "origin", 1, first, ""); err != nil {
		t.Fatalf("available immutable snapshot rejected after move: %v", err)
	}
	missing := strings.Repeat("f", 40)
	if err := EnsurePullRequestRevision(consumer, "origin", 1, missing, ""); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing snapshot err = %v", err)
	}
}
