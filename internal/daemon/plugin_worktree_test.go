package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDoCreateWorktree_ProviderHandledRegistersValidatedWorktree(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))

	client, done := startPluginPipe(t, d, "custom-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	providerPath := filepath.Join(tmpDir, "provider-created")
	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		if params.MainRepo != git.ResolveMainRepoPath(mainDir) {
			t.Fatalf("provider main repo=%q, want %q", params.MainRepo, git.ResolveMainRepoPath(mainDir))
		}
		if params.Branch != "feat/provider-create" {
			t.Fatalf("provider branch=%q, want feat/provider-create", params.Branch)
		}
		if params.RequestedPath != nil {
			t.Fatalf("provider requested path=%q, want nil", *params.RequestedPath)
		}

		runGitDaemon(t, mainDir, "worktree", "add", "-b", params.Branch, providerPath)
		return worktreeCreateProviderResult{
			Status: providerStatusHandled,
			Path:   providerPath,
			Branch: params.Branch,
		}
	})

	path, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/provider-create",
	})
	if err != nil {
		t.Fatalf("doCreateWorktree failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	if canonicalPathDaemon(path) != canonicalPathDaemon(providerPath) {
		t.Fatalf("created path=%q, want %q", path, providerPath)
	}
	if wt := d.store.GetWorktree(git.CanonicalizePath(providerPath)); wt == nil {
		t.Fatalf("expected provider-created worktree %q in store", providerPath)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("create provider connection did not close")
	}
}

func TestDoCreateWorktree_BeforeCreateHookRunsBeforeBuiltInCreate(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))

	client, done := startPluginPipe(t, d, "before-create-hook", []string{worktreeBeforeCreateSurface})
	defer client.Close()

	hookDone := respondToBeforeCreateHookCall(t, client, func(params worktreeCreateProviderParams) error {
		if params.MainRepo != git.ResolveMainRepoPath(mainDir) {
			return fmt.Errorf("before hook main repo=%q, want %q", params.MainRepo, git.ResolveMainRepoPath(mainDir))
		}
		if params.Branch != "feat/before-hook" {
			return fmt.Errorf("before hook branch=%q, want feat/before-hook", params.Branch)
		}
		return nil
	})

	path, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/before-hook",
	})
	if err != nil {
		t.Fatalf("doCreateWorktree failed: %v", err)
	}
	waitForProviderResponse(t, hookDone)
	if wt := d.store.GetWorktree(path); wt == nil {
		t.Fatalf("expected before-hook worktree %q in store", path)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("before create hook connection did not close")
	}
}

func TestDoCreateWorktree_AfterCreateHookErrorReturnsCreatedPath(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))

	client, done := startPluginPipe(t, d, "after-create-hook", []string{worktreeAfterCreateSurface})
	defer client.Close()

	hookDone := respondToAfterCreateHookCall(t, client, func(params worktreeAfterCreateHookParams) error {
		if params.MainRepo != git.ResolveMainRepoPath(mainDir) {
			return fmt.Errorf("after hook main repo=%q, want %q", params.MainRepo, git.ResolveMainRepoPath(mainDir))
		}
		if params.Branch != "feat/after-hook" {
			return fmt.Errorf("after hook branch=%q, want feat/after-hook", params.Branch)
		}
		return fmt.Errorf("dependency bootstrap failed")
	})

	path, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/after-hook",
	})
	if err == nil {
		t.Fatal("doCreateWorktree error=nil, want after-create hook error")
	}
	if path == "" {
		t.Fatal("doCreateWorktree path empty, want created worktree path on after-create hook error")
	}
	waitForProviderResponse(t, hookDone)
	if wt := d.store.GetWorktree(path); wt == nil {
		t.Fatalf("expected after-hook-created worktree %q in store", path)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("after create hook connection did not close")
	}
}

func TestDoCreateWorktree_ProviderDeclineFallsBackToBuiltInGit(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))

	client, done := startPluginPipe(t, d, "declining-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		return worktreeCreateProviderResult{Status: providerStatusDecline}
	})

	path, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/fallback-create",
	})
	if err != nil {
		t.Fatalf("doCreateWorktree fallback failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	wantPath := git.GenerateWorktreePath(mainDir, "feat/fallback-create")
	if canonicalPathDaemon(path) != canonicalPathDaemon(wantPath) {
		t.Fatalf("fallback path=%q, want generated git worktree path", path)
	}
	if wt := d.store.GetWorktree(path); wt == nil {
		t.Fatalf("expected fallback-created worktree %q in store", path)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("declining create provider connection did not close")
	}
}

func TestDoCreateWorktree_ProviderHandledPathMustBeRealExpectedWorktree(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))

	client, done := startPluginPipe(t, d, "invalid-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		return worktreeCreateProviderResult{
			Status: providerStatusHandled,
			Path:   filepath.Join(tmpDir, "not-a-worktree"),
			Branch: params.Branch,
		}
	})

	if _, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/invalid-create",
	}); err == nil {
		t.Fatal("doCreateWorktree error=nil, want invalid provider result")
	}
	waitForProviderResponse(t, responseDone)

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("invalid create provider connection did not close")
	}
}

func TestDoCreateWorktree_ProviderHandledPathMustBeNewWorktree(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	existingPath := filepath.Join(tmpDir, "existing")
	runGitDaemon(t, mainDir, "worktree", "add", "-b", "feat/existing", existingPath)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	client, done := startPluginPipe(t, d, "existing-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		return worktreeCreateProviderResult{
			Status: providerStatusHandled,
			Path:   existingPath,
			Branch: "feat/existing",
		}
	})

	if _, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		MainRepo: mainDir,
		Branch:   "feat/new-request",
	}); err == nil {
		t.Fatal("doCreateWorktree error=nil, want pre-existing provider result rejected")
	}
	waitForProviderResponse(t, responseDone)

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("existing create provider connection did not close")
	}
}

func TestDoCreateWorktreeFromBranch_ProviderHandledRegistersValidatedWorktree(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	runGitDaemon(t, mainDir, "branch", "feature/existing")

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	client, done := startPluginPipe(t, d, "branch-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	providerPath := filepath.Join(tmpDir, "provider-existing-branch")
	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		if params.Branch != "feature/existing" {
			t.Fatalf("provider branch=%q, want feature/existing", params.Branch)
		}
		if params.StartingFrom != "feature/existing" {
			t.Fatalf("provider starting_from=%q, want feature/existing", params.StartingFrom)
		}
		runGitDaemon(t, mainDir, "worktree", "add", providerPath, params.StartingFrom)
		return worktreeCreateProviderResult{
			Status: providerStatusHandled,
			Path:   providerPath,
			Branch: params.Branch,
		}
	})

	path, err := d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
		MainRepo: mainDir,
		Branch:   "feature/existing",
	})
	if err != nil {
		t.Fatalf("doCreateWorktreeFromBranch failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	if canonicalPathDaemon(path) != canonicalPathDaemon(providerPath) {
		t.Fatalf("created path=%q, want %q", path, providerPath)
	}
	if wt := d.store.GetWorktree(git.CanonicalizePath(providerPath)); wt == nil {
		t.Fatalf("expected branch-provider worktree %q in store", providerPath)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("branch create provider connection did not close")
	}
}

func TestDoCreateWorktreeFromBranch_ProviderHandledRemoteBranchStoresLocalBranch(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	client, done := startPluginPipe(t, d, "remote-branch-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	providerPath := filepath.Join(tmpDir, "provider-remote-branch")
	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		if params.Branch != "feature/remote" {
			t.Fatalf("provider branch=%q, want feature/remote", params.Branch)
		}
		if params.StartingFrom != "origin/feature/remote" {
			t.Fatalf("provider starting_from=%q, want origin/feature/remote", params.StartingFrom)
		}
		runGitDaemon(t, mainDir, "worktree", "add", "-b", params.Branch, providerPath)
		return worktreeCreateProviderResult{
			Status: providerStatusHandled,
			Path:   providerPath,
			Branch: params.Branch,
		}
	})

	path, err := d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
		MainRepo: mainDir,
		Branch:   "origin/feature/remote",
	})
	if err != nil {
		t.Fatalf("doCreateWorktreeFromBranch failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	if canonicalPathDaemon(path) != canonicalPathDaemon(providerPath) {
		t.Fatalf("created path=%q, want %q", path, providerPath)
	}
	created := d.store.GetWorktree(git.CanonicalizePath(providerPath))
	if created == nil {
		t.Fatalf("expected remote branch provider worktree %q in store", providerPath)
	}
	if created.Branch != "feature/remote" {
		t.Fatalf("stored branch=%q, want feature/remote", created.Branch)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("remote branch create provider connection did not close")
	}
}

func TestDoCreateWorktreeFromBranch_ProviderDeclinesRemoteBranchFallsBackToBuiltInGit(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	remoteBranch := "feature/remote-fallback"
	originDir := filepath.Join(tmpDir, "origin.git")
	runGitDaemon(t, tmpDir, "init", "--bare", originDir)
	runGitDaemon(t, mainDir, "remote", "add", "origin", originDir)
	runGitDaemon(t, mainDir, "branch", remoteBranch)
	runGitDaemon(t, mainDir, "push", "origin", remoteBranch)
	runGitDaemon(t, mainDir, "branch", "-D", remoteBranch)
	runGitDaemon(t, mainDir, "fetch", "origin")

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	client, done := startPluginPipe(t, d, "declining-remote-branch-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()

	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		if params.Branch != remoteBranch {
			t.Fatalf("provider branch=%q, want %s", params.Branch, remoteBranch)
		}
		if params.StartingFrom != "origin/"+remoteBranch {
			t.Fatalf("provider starting_from=%q, want origin/%s", params.StartingFrom, remoteBranch)
		}
		return worktreeCreateProviderResult{Status: providerStatusDecline}
	})

	path, err := d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
		MainRepo: mainDir,
		Branch:   "origin/" + remoteBranch,
	})
	if err != nil {
		t.Fatalf("doCreateWorktreeFromBranch fallback failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	wantPath := git.GenerateWorktreePath(mainDir, remoteBranch)
	if canonicalPathDaemon(path) != canonicalPathDaemon(wantPath) {
		t.Fatalf("fallback path=%q, want %q", path, wantPath)
	}
	created := d.store.GetWorktree(path)
	if created == nil {
		t.Fatalf("expected fallback remote worktree %q in store", path)
	}
	if created.Branch != remoteBranch {
		t.Fatalf("stored branch=%q, want %s", created.Branch, remoteBranch)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("declining remote branch provider connection did not close")
	}
}

func TestDoDeleteWorktree_ProviderHandledFinalizesDaemonState(t *testing.T) {
	tmpDir, mainDir := initProviderTestRepo(t)
	worktreePath := filepath.Join(tmpDir, "provider-delete")
	runGitDaemon(t, mainDir, "worktree", "add", "-b", "feat/provider-delete", worktreePath)
	worktreePath = git.CanonicalizePath(worktreePath)

	d := NewForTesting(filepath.Join(tmpDir, "attn.sock"))
	d.registerCreatedWorktree(mainDir, worktreePath, "feat/provider-delete")

	client, done := startPluginPipe(t, d, "custom-delete-provider", []string{worktreeDeleteProviderSurface})
	defer client.Close()

	responseDone := respondToDeleteProviderCall(t, client, func(params worktreeDeleteProviderParams) worktreeDeleteProviderResult {
		if params.Path != worktreePath {
			t.Fatalf("provider delete path=%q, want %q", params.Path, worktreePath)
		}
		if params.Branch != "feat/provider-delete" {
			t.Fatalf("provider delete branch=%q, want feat/provider-delete", params.Branch)
		}
		if err := git.DeleteWorktree(mainDir, worktreePath); err != nil {
			t.Fatalf("provider delete worktree failed: %v", err)
		}
		return worktreeDeleteProviderResult{Status: providerStatusHandled}
	})

	if err := d.doDeleteWorktree(worktreePath, nil); err != nil {
		t.Fatalf("doDeleteWorktree failed: %v", err)
	}
	waitForProviderResponse(t, responseDone)

	if wt := d.store.GetWorktree(worktreePath); wt != nil {
		t.Fatalf("expected deleted worktree removed from store, got %#v", wt)
	}
	worktrees, err := git.ListWorktrees(mainDir)
	if err != nil {
		t.Fatalf("list worktrees after provider delete: %v", err)
	}
	for _, worktree := range worktrees {
		if worktree.Path == worktreePath {
			t.Fatalf("provider-deleted worktree still listed: %#v", worktree)
		}
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delete provider connection did not close")
	}
}

func initProviderTestRepo(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatalf("mkdir main repo: %v", err)
	}
	runGitDaemon(t, mainDir, "init")
	runGitDaemon(t, mainDir, "commit", "--allow-empty", "-m", "init")
	return tmpDir, git.ResolveMainRepoPath(mainDir)
}

func respondToCreateProviderCall(
	t *testing.T,
	conn net.Conn,
	handle func(worktreeCreateProviderParams) worktreeCreateProviderResult,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		request := decodeJSONRPCMessage(t, conn)
		if request.Method != worktreeCreateProviderSurface {
			done <- fmt.Errorf("provider method=%q, want %s", request.Method, worktreeCreateProviderSurface)
			return
		}
		var params worktreeCreateProviderParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			done <- fmt.Errorf("decode create provider params: %w", err)
			return
		}
		writeProviderResult(t, conn, request.ID, handle(params))
	}()
	return done
}

func respondToDeleteProviderCall(
	t *testing.T,
	conn net.Conn,
	handle func(worktreeDeleteProviderParams) worktreeDeleteProviderResult,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		request := decodeJSONRPCMessage(t, conn)
		if request.Method != worktreeDeleteProviderSurface {
			done <- fmt.Errorf("provider method=%q, want %s", request.Method, worktreeDeleteProviderSurface)
			return
		}
		var params worktreeDeleteProviderParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			done <- fmt.Errorf("decode delete provider params: %w", err)
			return
		}
		writeProviderResult(t, conn, request.ID, handle(params))
	}()
	return done
}

func respondToBeforeCreateHookCall(
	t *testing.T,
	conn net.Conn,
	handle func(worktreeCreateProviderParams) error,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		request := decodeJSONRPCMessage(t, conn)
		if request.Method != worktreeBeforeCreateSurface {
			done <- fmt.Errorf("hook method=%q, want %s", request.Method, worktreeBeforeCreateSurface)
			return
		}
		var params worktreeCreateProviderParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			done <- fmt.Errorf("decode before create hook params: %w", err)
			return
		}
		writeHookResult(t, conn, request.ID, handle(params))
	}()
	return done
}

func respondToAfterCreateHookCall(
	t *testing.T,
	conn net.Conn,
	handle func(worktreeAfterCreateHookParams) error,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		request := decodeJSONRPCMessage(t, conn)
		if request.Method != worktreeAfterCreateSurface {
			done <- fmt.Errorf("hook method=%q, want %s", request.Method, worktreeAfterCreateSurface)
			return
		}
		var params worktreeAfterCreateHookParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			done <- fmt.Errorf("decode after create hook params: %w", err)
			return
		}
		writeHookResult(t, conn, request.ID, handle(params))
	}()
	return done
}

func waitForProviderResponse(t *testing.T, done <-chan error) {
	t.Helper()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func writeProviderResult(t *testing.T, conn net.Conn, id json.RawMessage, result interface{}) {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal provider result: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  payload,
	}); err != nil {
		t.Fatalf("encode provider result: %v", err)
	}
}

func writeHookResult(t *testing.T, conn net.Conn, id json.RawMessage, hookErr error) {
	t.Helper()
	message := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
	}
	if hookErr != nil {
		message.Error = &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: hookErr.Error(),
		}
	}
	if err := json.NewEncoder(conn).Encode(message); err != nil {
		t.Fatalf("encode hook result: %v", err)
	}
}
