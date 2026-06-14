package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
)

// recordingStub captures the AgentCall it received so a test can assert the host
// threaded isolation/model/schema off the agent() opts object. It returns a fixed
// schema-valid result so the engine completes.
type recordingStub struct {
	mu     sync.Mutex
	calls  []AgentCall
	result json.RawMessage
}

func (s *recordingStub) Run(call AgentCall) (json.RawMessage, error) {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	return s.result, nil
}

func (s *recordingStub) only(t *testing.T) AgentCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 1 {
		t.Fatalf("recordingStub saw %d calls, want exactly 1", len(s.calls))
	}
	return s.calls[0]
}

// TestHostThreadsIsolationModelSchemaToStub drives the FULL engine path and proves
// the host extracts isolation/model off the agent() opts object and threads them
// (plus the schema) into the AgentCall handed to the stub.
func TestHostThreadsIsolationModelSchemaToStub(t *testing.T) {
	stub := &recordingStub{result: json.RawMessage(`{"ok":true}`)}
	script := `export const meta={name:'t',description:'d'};
		return await agent('x', {isolation:'worktree', model:'m', schema:{type:'object'}});`

	eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("engine run error: %v (status=%s)", err, res.Status)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}

	call := stub.only(t)
	if call.Isolation != "worktree" {
		t.Fatalf("call.Isolation = %q, want \"worktree\"", call.Isolation)
	}
	if call.Model != "m" {
		t.Fatalf("call.Model = %q, want \"m\"", call.Model)
	}
	if len(call.Schema) == 0 {
		t.Fatalf("call.Schema was not threaded: %s", call.Schema)
	}
	if call.Prompt != "x" {
		t.Fatalf("call.Prompt = %q, want \"x\"", call.Prompt)
	}
}

// TestHostDefaultsIsolationEmptyWhenNoOpt proves the no-isolation path: an agent()
// call with no isolation opt yields call.Isolation == "" (the shared-working-tree,
// E3-byte-identical default), and an unknown isolation value normalizes to "".
func TestHostDefaultsIsolationEmptyWhenNoOpt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{"no opts", `return await agent('x');`},
		{"no isolation key", `return await agent('x', {model:'m'});`},
		{"unknown isolation normalizes to none", `return await agent('x', {isolation:'bogus'});`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &recordingStub{result: json.RawMessage(`{"ok":true}`)}
			eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
			res, err := eng.Run(context.Background(), tc.script, nil)
			if err != nil {
				t.Fatalf("engine run error: %v (status=%s)", err, res.Status)
			}
			if got := stub.only(t).Isolation; got != "" {
				t.Fatalf("call.Isolation = %q, want \"\"", got)
			}
		})
	}
}

// --- worktree round-trip (real git, hermetic) -------------------------------

// initTestRepo creates a temp git repo with a user config + an initial commit so
// `git worktree add` and `git status` behave normally. Returns the repo dir.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	// CanonicalizePath resolves symlinks (macOS /var -> /private/var) so the path
	// matches what git reports for worktree listing comparisons.
	return git.CanonicalizePath(dir)
}

// worktreePaths returns the set of worktree paths git currently tracks for repo.
func worktreePaths(t *testing.T, repo string) map[string]bool {
	t.Helper()
	entries, err := git.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	set := map[string]bool{}
	for _, e := range entries {
		set[git.CanonicalizePath(e.Path)] = true
	}
	return set
}

func newIsolationTestDriverAgent(t *testing.T, repo string, runner headlessRunner) *driverAgent {
	t.Helper()
	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:       "codex",
		Executable:     "/bin/true",
		Model:          "test-model",
		RunTmpDir:      t.TempDir(),
		AttnExecutable: "/bin/true",
		MaxRetries:     2,
		Runner:         runner,
		WorkingTree:    repo,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}
	return da
}

// TestWorktreeIsolationKeepsMutatedWorktree (Case A): the subagent writes a file
// into its fresh worktree CWD. The call returns the schema result AND the worktree
// is KEPT (still listed by git) because the mutations are the consumed side effect.
func TestWorktreeIsolationKeepsMutatedWorktree(t *testing.T) {
	repo := initTestRepo(t)

	var sawCWD string
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		sawCWD = req.CWD
		// The subagent ran in a FRESH worktree: not the main repo, a real dir, and a
		// registered worktree.
		if req.CWD == repo {
			t.Fatalf("isolated call ran in the main repo, not a fresh worktree: %q", req.CWD)
		}
		if info, err := os.Stat(req.CWD); err != nil || !info.IsDir() {
			t.Fatalf("worktree CWD %q is not a real directory: %v", req.CWD, err)
		}
		if !worktreePaths(t, repo)[git.CanonicalizePath(req.CWD)] {
			t.Fatalf("worktree CWD %q is not a registered worktree", req.CWD)
		}
		// Mutate the worktree (a tracked-but-modified file => git-dirty) AND write
		// the schema result so the call succeeds.
		if err := os.WriteFile(filepath.Join(req.CWD, "README.md"), []byte("mutated by agent\n"), 0o600); err != nil {
			t.Fatalf("agent write into worktree: %v", err)
		}
		writeValid(t, req.ResultPath, `{"answer":"did work"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newIsolationTestDriverAgent(t, repo, runner)

	got, err := da.Run(AgentCall{Ordinal: ordForTest(), Prompt: "do work", Schema: json.RawMessage(testSchema), Isolation: "worktree"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !jsonStringEqual(string(got), `{"answer":"did work"}`) {
		t.Fatalf("result = %s, want {\"answer\":\"did work\"}", got)
	}
	// The mutated worktree must be KEPT.
	if !worktreePaths(t, repo)[git.CanonicalizePath(sawCWD)] {
		t.Fatalf("mutated worktree %q was removed; it must be kept", sawCWD)
	}
	if _, err := os.Stat(sawCWD); err != nil {
		t.Fatalf("mutated worktree dir %q no longer exists: %v", sawCWD, err)
	}
}

// TestWorktreeIsolationRemovesCleanWorktree (Case B): the subagent writes ONLY the
// schema result (no tree change). The worktree is AUTO-REMOVED after the run.
func TestWorktreeIsolationRemovesCleanWorktree(t *testing.T) {
	repo := initTestRepo(t)

	var sawCWD string
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		sawCWD = req.CWD
		if !worktreePaths(t, repo)[git.CanonicalizePath(req.CWD)] {
			t.Fatalf("worktree CWD %q is not a registered worktree", req.CWD)
		}
		// No tree change: only the result file (which lives in runTmpDir, not the
		// worktree), so the worktree stays git-clean.
		writeValid(t, req.ResultPath, `{"answer":"no edits"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newIsolationTestDriverAgent(t, repo, runner)

	got, err := da.Run(AgentCall{Ordinal: ordForTest(), Prompt: "read only", Schema: json.RawMessage(testSchema), Isolation: "worktree"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !jsonStringEqual(string(got), `{"answer":"no edits"}`) {
		t.Fatalf("result = %s", got)
	}
	// The clean worktree must be AUTO-REMOVED.
	if worktreePaths(t, repo)[git.CanonicalizePath(sawCWD)] {
		t.Fatalf("clean worktree %q is still listed; it must be auto-removed", sawCWD)
	}
	if _, err := os.Stat(sawCWD); !os.IsNotExist(err) {
		t.Fatalf("clean worktree dir %q still exists (stat err=%v); it must be removed", sawCWD, err)
	}
}

// TestWorktreeIsolationNoneRunsInWorkingTree (Case C): isolation == "" runs in the
// shared working tree and creates NO worktree.
func TestWorktreeIsolationNoneRunsInWorkingTree(t *testing.T) {
	repo := initTestRepo(t)
	before := worktreePaths(t, repo)

	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if req.CWD != repo {
			t.Fatalf("non-isolated call CWD = %q, want the working tree %q", req.CWD, repo)
		}
		writeValid(t, req.ResultPath, `{"answer":"shared"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newIsolationTestDriverAgent(t, repo, runner)

	if _, err := da.Run(AgentCall{Ordinal: ordForTest(), Prompt: "shared tree", Schema: json.RawMessage(testSchema)}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	after := worktreePaths(t, repo)
	if len(after) != len(before) {
		t.Fatalf("non-isolated call changed the worktree set: before=%v after=%v", before, after)
	}
}

// TestWorktreeIsolationFailedRunKeepsMutatedWorktree proves the cleanup rule is
// outcome-independent: a FAILED run that still dirtied the worktree keeps it (so a
// partial mutation is never silently discarded), while the call still returns the
// terminal error (engine -> null).
func TestWorktreeIsolationFailedRunKeepsMutatedWorktree(t *testing.T) {
	repo := initTestRepo(t)

	var sawCWD string
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		sawCWD = req.CWD
		// Dirty the tree but NEVER write a result -> terminal failure after retries.
		_ = os.WriteFile(filepath.Join(req.CWD, "README.md"), []byte("half-done\n"), 0o600)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newIsolationTestDriverAgent(t, repo, runner)

	got, err := da.Run(AgentCall{Ordinal: ordForTest(), Prompt: "will fail", Schema: json.RawMessage(testSchema), Isolation: "worktree"})
	if err == nil {
		t.Fatalf("expected a terminal error (engine maps to null), got result %s", got)
	}
	if got != nil {
		t.Fatalf("terminal failure returned non-nil result: %s", got)
	}
	if !worktreePaths(t, repo)[git.CanonicalizePath(sawCWD)] {
		t.Fatalf("failed-but-mutated worktree %q was removed; it must be kept", sawCWD)
	}
}

// TestWorktreeIsolationModelOverride proves call.Model overrides the agent's
// default model for the isolated subagent request.
func TestWorktreeIsolationModelOverride(t *testing.T) {
	repo := initTestRepo(t)
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if req.Model != "override-model" {
			t.Fatalf("req.Model = %q, want per-call override \"override-model\"", req.Model)
		}
		writeValid(t, req.ResultPath, `{"answer":"ok"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newIsolationTestDriverAgent(t, repo, runner) // default model "test-model"

	if _, err := da.Run(AgentCall{Ordinal: ordForTest(), Prompt: "x", Schema: json.RawMessage(testSchema), Isolation: "worktree", Model: "override-model"}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
}
