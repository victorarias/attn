package present

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPin(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "base commit")
	baseSHA := runGit(t, dir, "rev-parse", "HEAD")

	// The initial branch name may be "main" or "master" depending on git
	// config; resolve it before switching away so the test isn't
	// environment-dependent.
	initialBranch := runGit(t, dir, "branch", "--show-current")
	if initialBranch == "" {
		t.Fatal("could not determine initial branch name")
	}

	runGit(t, dir, "checkout", "-b", "feature")
	runGit(t, dir, "commit", "--allow-empty", "-m", "feature commit")
	headSHA := runGit(t, dir, "rev-parse", "HEAD")

	m := &Manifest{
		Version: 1,
		Kind:    "changes",
		Title:   "test",
		Frame: Frame{
			Repo: dir,
			Base: initialBranch,
			Head: "feature",
		},
	}

	gotBase, gotHead, err := Pin(m)
	if err != nil {
		t.Fatalf("Pin() unexpected error: %v", err)
	}
	if gotBase != baseSHA {
		t.Errorf("base SHA = %q, want %q", gotBase, baseSHA)
	}
	if gotHead != headSHA {
		t.Errorf("head SHA = %q, want %q", gotHead, headSHA)
	}
	if len(gotBase) != 40 {
		t.Errorf("base SHA %q is not 40 chars", gotBase)
	}
	if len(gotHead) != 40 {
		t.Errorf("head SHA %q is not 40 chars", gotHead)
	}
}

func TestPin_HEADRef(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "only commit")
	sha := runGit(t, dir, "rev-parse", "HEAD")

	m := &Manifest{
		Version: 1,
		Kind:    "changes",
		Title:   "test",
		Frame: Frame{
			Repo: dir,
			Base: "HEAD",
			Head: "HEAD",
		},
	}

	gotBase, gotHead, err := Pin(m)
	if err != nil {
		t.Fatalf("Pin() unexpected error: %v", err)
	}
	if gotBase != sha || gotHead != sha {
		t.Errorf("Pin() = (%q, %q), want (%q, %q)", gotBase, gotHead, sha, sha)
	}
}

func TestPin_MissingRef(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	m := &Manifest{
		Version: 1,
		Kind:    "changes",
		Title:   "test",
		Frame: Frame{
			Repo: dir,
			Base: "does-not-exist",
			Head: "HEAD",
		},
	}

	_, _, err := Pin(m)
	if err == nil {
		t.Fatal("Pin() expected error for missing ref, got nil")
	}
	if !strings.Contains(err.Error(), "frame.base") {
		t.Errorf("Pin() error = %q, want to mention frame.base", err.Error())
	}
}

func TestPin_MissingRepo(t *testing.T) {
	m := &Manifest{
		Version: 1,
		Kind:    "changes",
		Title:   "test",
		Frame: Frame{
			Repo: "/nonexistent/repo/dir",
			Base: "HEAD",
			Head: "HEAD",
		},
	}

	_, _, err := Pin(m)
	if err == nil {
		t.Fatal("Pin() expected error for missing repo, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Pin() error = %q, want to mention repo does not exist", err.Error())
	}
}
