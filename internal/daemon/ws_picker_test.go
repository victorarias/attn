package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
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

func TestParseBrowseInputTreatsLogingSlashAsBrowseIntoDirectory(t *testing.T) {
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

// seedBrowseTree lays out one directory holding a mix of things path mode must
// distinguish: subdirectories, markdown files, an unrelated file type, git's
// metadata, and a symlink.
func seedBrowseTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"docs", ".claude", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"plan.md", "notes.txt", ".git/COMMIT_EDITMSG"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "plan.md"), filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}
	return root
}

func entryNames(entries []protocol.DirectoryEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// Without an extension filter the listing is directories only — the session
// picker's long-standing behavior, which path mode must not change.
func TestListDirectoryEntriesWithoutExtensionsListsDirectoriesOnly(t *testing.T) {
	root := seedBrowseTree(t)

	entries, err := listDirectoryEntries(root, "", nil)
	if err != nil {
		t.Fatalf("listDirectoryEntries: %v", err)
	}
	if want := []string{".claude", "docs"}; !slicesEqual(entryNames(entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(entries), want)
	}
	for _, entry := range entries {
		if !entry.IsDir {
			t.Fatalf("entry %q reported is_dir = false", entry.Name)
		}
	}
}

// With an extension filter the listing gains matching regular files — and only
// those: another file type, a symlink (fs_read serves regular files only), and
// .git stay out, while dot-directories remain visible so a document under
// ~/.claude is reachable.
func TestListDirectoryEntriesWithExtensionsAddsMatchingFiles(t *testing.T) {
	root := seedBrowseTree(t)

	entries, err := listDirectoryEntries(root, "", []string{"md"})
	if err != nil {
		t.Fatalf("listDirectoryEntries: %v", err)
	}
	// Directories first, then files: "where you can go, then what you can open".
	if want := []string{".claude", "docs", "plan.md"}; !slicesEqual(entryNames(entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(entries), want)
	}
	if entries[2].IsDir {
		t.Fatalf("plan.md reported is_dir = true")
	}
	if entries[2].Path != filepath.Join(root, "plan.md") {
		t.Fatalf("plan.md path = %q, want absolute path under root", entries[2].Path)
	}
}

// The typed last segment filters both groups, and a leading dot reaches a
// dot-directory rather than being swallowed as a hidden-file rule.
func TestListDirectoryEntriesFiltersByPrefixAcrossKinds(t *testing.T) {
	root := seedBrowseTree(t)

	entries, err := listDirectoryEntries(root, ".cla", []string{"md"})
	if err != nil {
		t.Fatalf("listDirectoryEntries: %v", err)
	}
	if want := []string{".claude"}; !slicesEqual(entryNames(entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(entries), want)
	}

	entries, err = listDirectoryEntries(root, "pla", []string{"md"})
	if err != nil {
		t.Fatalf("listDirectoryEntries: %v", err)
	}
	if want := []string{"plan.md"}; !slicesEqual(entryNames(entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(entries), want)
	}
}

// Asking for files is gated on the authenticated app client: directory names
// leak tree shape, file names leak the user's documents. An untrusted client
// gets an error, not a listing.
func TestBrowseDirectoryFileListingRequiresTrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	root := seedBrowseTree(t)

	untrusted := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleBrowseDirectoryWS(untrusted, &protocol.BrowseDirectoryMessage{
		Cmd:        protocol.CmdBrowseDirectory,
		InputPath:  root + "/",
		Extensions: []string{"md"},
	})
	var denied protocol.BrowseDirectoryResultMessage
	readNotebookWSEvent(t, untrusted.send, &denied)
	if denied.Success {
		t.Fatalf("untrusted file listing succeeded: %+v", denied.Entries)
	}

	// The same client may still browse directories: that surface is unchanged.
	d.handleBrowseDirectoryWS(untrusted, &protocol.BrowseDirectoryMessage{
		Cmd:       protocol.CmdBrowseDirectory,
		InputPath: root + "/",
	})
	var allowed protocol.BrowseDirectoryResultMessage
	readNotebookWSEvent(t, untrusted.send, &allowed)
	if !allowed.Success {
		t.Fatalf("untrusted directory listing failed: %v", protocol.Deref(allowed.Error))
	}
	if want := []string{".claude", "docs"}; !slicesEqual(entryNames(allowed.Entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(allowed.Entries), want)
	}

	// The app client gets the files.
	trusted := trustedFsClient(4)
	d.handleBrowseDirectoryWS(trusted, &protocol.BrowseDirectoryMessage{
		Cmd:        protocol.CmdBrowseDirectory,
		InputPath:  root + "/",
		Extensions: []string{"md"},
	})
	var granted protocol.BrowseDirectoryResultMessage
	readNotebookWSEvent(t, trusted.send, &granted)
	if !granted.Success {
		t.Fatalf("trusted file listing failed: %v", protocol.Deref(granted.Error))
	}
	if want := []string{".claude", "docs", "plan.md"}; !slicesEqual(entryNames(granted.Entries), want) {
		t.Fatalf("entries = %v, want %v", entryNames(granted.Entries), want)
	}
}
