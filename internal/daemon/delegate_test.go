package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func setupDelegationSource(t *testing.T, d *Daemon, backend *fakeSpawnBackend) (string, string, string) {
	t.Helper()
	return setupDelegationSourceAt(t, d, backend, t.TempDir())
}

func setupDelegationSourceAt(t *testing.T, d *Daemon, backend *fakeSpawnBackend, cwd string) (string, string, string) {
	t.Helper()
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-source"
	sessionID := "session-source"

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Source workspace",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-source"),
		SessionID:   sessionID,
		Title:       protocol.Ptr("Source"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-source", true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr("Source"),
	})
	expectSpawnResult(t, client, sessionID, true)
	return workspaceID, sessionID, cwd
}

func consumeDelegatedPrompt(t *testing.T, backend *fakeSpawnBackend) {
	t.Helper()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		if _, err := os.ReadFile(opts.InitialPromptFile); err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}
}

func TestDelegateSpawnsAgentInSourceWorkspaceWithBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, cwd := setupDelegationSource(t, d, backend)

	var prompt string
	var promptPath string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		promptPath = opts.InitialPromptFile
		content, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(promptPath); err != nil {
			t.Fatalf("remove consumed initial prompt: %v", err)
		}
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Investigate the delegated task.",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if result.WorkspaceID != workspaceID || result.Directory != cwd {
		t.Fatalf("result = %+v, want workspace=%s directory=%s", result, workspaceID, cwd)
	}
	if prompt != "Investigate the delegated task." {
		t.Fatalf("initial prompt = %q", prompt)
	}
	if promptPath == "" {
		t.Fatal("delegated spawn had no initial prompt file")
	}
	if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
		t.Fatalf("initial prompt file still exists after spawn: %v", err)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.WorkspaceID != workspaceID || session.Agent != protocol.SessionAgentCodex {
		t.Fatalf("delegated session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("workspace layout = %+v, want two panes", layout)
	}
}

func TestDelegateRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "This spawn should fail.",
		Agent:           protocol.Ptr("codex"),
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	layout := d.store.GetWorkspaceLayout("workspace-source")
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("workspace layout after rollback = %+v", layout)
	}
}

func TestDelegateRejectsCopilotInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Use Copilot for this delegated task.",
		Agent:           protocol.Ptr("copilot"),
	})
	if err == nil || !strings.Contains(err.Error(), "does not support initial prompts") {
		t.Fatalf("delegate() error = %v, want unsupported initial prompt error", err)
	}
	layout := d.store.GetWorkspaceLayout("workspace-source")
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("workspace layout after rejection = %+v", layout)
	}
}

func TestResolveDelegationAgentSupportsRegisteredPluginWithInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registry := d.ensurePluginRegistry()
	registry.mu.Lock()
	registry.drivers["fixture"] = pluginDriverRegistration{
		PluginName: "fixture-plugin",
		Agent:      "fixture",
		Capabilities: map[string]bool{
			"initial_prompt": true,
		},
	}
	registry.mu.Unlock()

	agent, err := d.resolveDelegationAgent("codex", protocol.Ptr("fixture"))
	if err != nil {
		t.Fatalf("resolveDelegationAgent() error = %v", err)
	}
	if agent != "fixture" {
		t.Fatalf("agent = %q", agent)
	}
}

func TestDelegateCreatesNewWorkspaceAtCustomDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Work in a separate directory.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             protocol.Ptr(targetDir),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	targetDir = git.CanonicalizePath(targetDir)
	if result.Placement != delegationPlacementNew || result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil || workspace.Directory != targetDir {
		t.Fatalf("delegated workspace = %+v", workspace)
	}
	layout := d.store.GetWorkspaceLayout(result.WorkspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != result.SessionID {
		t.Fatalf("delegated workspace layout = %+v", layout)
	}
}

func TestDelegateTargetsExistingWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: targetDir,
	})

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Join the target workspace.",
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if result.WorkspaceID != targetWorkspaceID || result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
}

func TestDelegateCreatesWorktreeAndNewWorkspace(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Implement this in an isolated branch.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Directory != worktreePath || !protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("worktree path stat = %v, info = %+v", err, info)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || protocol.Deref(session.Branch) != "feat/delegated" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
}

func TestDelegateRollsBackNewWorkspaceAndWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}
	worktreePath := filepath.Join(root, "repo--feat-rollback")

	if _, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "This spawn should roll back.",
		Placement:       protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after rollback: %v", err)
	}
	for _, workspace := range d.store.ListWorkspaces() {
		if workspace != nil && workspace.Directory == worktreePath {
			t.Fatalf("delegated workspace still exists after rollback: %+v", workspace)
		}
	}
}
