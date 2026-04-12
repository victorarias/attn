package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func sameDirectory(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return os.SameFile(leftInfo, rightInfo)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestParseBrowseInputTreatsTrailingSlashAsBrowseIntoDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	directory, prefix, homePath, err := parseBrowseInput("~/projects/hurdy-gurdy/")
	if err != nil {
		t.Fatalf("parseBrowseInput() error = %v", err)
	}

	wantDirectory := filepath.Join(home, "projects", "hurdy-gurdy")
	if directory != wantDirectory {
		t.Fatalf("directory = %q, want %q", directory, wantDirectory)
	}
	if prefix != "" {
		t.Fatalf("prefix = %q, want empty", prefix)
	}
	if homePath != home {
		t.Fatalf("homePath = %q, want %q", homePath, home)
	}
}

func TestParseBrowseInputUsesParentDirectoryForPartialChildMatch(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	directory, prefix, homePath, err := parseBrowseInput("~/projects/hurdy")
	if err != nil {
		t.Fatalf("parseBrowseInput() error = %v", err)
	}

	wantDirectory := filepath.Join(home, "projects")
	if directory != wantDirectory {
		t.Fatalf("directory = %q, want %q", directory, wantDirectory)
	}
	if prefix != "hurdy" {
		t.Fatalf("prefix = %q, want %q", prefix, "hurdy")
	}
	if homePath != home {
		t.Fatalf("homePath = %q, want %q", homePath, home)
	}
}

func TestInspectPickerPathTreatsSlashVariantsTheSameForRepoRoots(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.Mkdir(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")

	noSlash, err := inspectPickerPath(repoDir)
	if err != nil {
		t.Fatalf("inspect repo without slash: %v", err)
	}
	withSlash, err := inspectPickerPath(repoDir + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("inspect repo with slash: %v", err)
	}

	if !sameDirectory(noSlash.ResolvedPath, repoDir) {
		t.Fatalf("resolved path without slash = %q, want same directory as %q", noSlash.ResolvedPath, repoDir)
	}
	if !sameDirectory(withSlash.ResolvedPath, repoDir) {
		t.Fatalf("resolved path with slash = %q, want same directory as %q", withSlash.ResolvedPath, repoDir)
	}
	if noSlash.RepoRoot == nil || !sameDirectory(*noSlash.RepoRoot, repoDir) {
		t.Fatalf("repo root without slash = %v, want same directory as %q", noSlash.RepoRoot, repoDir)
	}
	if withSlash.RepoRoot == nil || !sameDirectory(*withSlash.RepoRoot, repoDir) {
		t.Fatalf("repo root with slash = %v, want same directory as %q", withSlash.RepoRoot, repoDir)
	}
}

func TestInspectPickerPathOnlyMarksActualRepoRoots(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	worktreeDir := filepath.Join(tmpDir, "repo--feature")
	subdir := filepath.Join(repoDir, ".claude")
	if err := os.Mkdir(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")
	runGit(t, repoDir, "worktree", "add", "-b", "feature", worktreeDir)
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	worktreeInspection, err := inspectPickerPath(worktreeDir)
	if err != nil {
		t.Fatalf("inspect worktree: %v", err)
	}
	if worktreeInspection.RepoRoot == nil || !sameDirectory(*worktreeInspection.RepoRoot, repoDir) {
		t.Fatalf("worktree repo root = %v, want same directory as %q", worktreeInspection.RepoRoot, repoDir)
	}

	subdirInspection, err := inspectPickerPath(subdir)
	if err != nil {
		t.Fatalf("inspect subdir: %v", err)
	}
	if subdirInspection.RepoRoot != nil {
		t.Fatalf("subdir repo root = %q, want nil", *subdirInspection.RepoRoot)
	}
}

func TestInspectPickerPathCanonicalizesSymlinkedRepoRoots(t *testing.T) {
	tmpDir := t.TempDir()
	realRepoDir := filepath.Join(tmpDir, "real-repo")
	symlinkRepoDir := filepath.Join(tmpDir, "repo-link")
	if err := os.Mkdir(realRepoDir, 0o755); err != nil {
		t.Fatalf("mkdir real repo: %v", err)
	}

	runGit(t, realRepoDir, "init", "-b", "main")
	runGit(t, realRepoDir, "config", "user.name", "Test User")
	runGit(t, realRepoDir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(realRepoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, realRepoDir, "add", "README.md")
	runGit(t, realRepoDir, "commit", "-m", "initial")
	if err := os.Symlink(realRepoDir, symlinkRepoDir); err != nil {
		t.Fatalf("symlink repo: %v", err)
	}

	inspection, err := inspectPickerPath(symlinkRepoDir)
	if err != nil {
		t.Fatalf("inspect symlink repo: %v", err)
	}

	if !sameDirectory(inspection.ResolvedPath, realRepoDir) {
		t.Fatalf("resolved path = %q, want same directory as %q", inspection.ResolvedPath, realRepoDir)
	}
	if inspection.RepoRoot == nil || !sameDirectory(*inspection.RepoRoot, realRepoDir) {
		t.Fatalf("repo root = %v, want same directory as %q", inspection.RepoRoot, realRepoDir)
	}
}
