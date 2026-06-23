package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	delegationPlacementCurrent  = "current_workspace"
	delegationPlacementExisting = "existing_workspace"
	delegationPlacementNew      = "new_workspace"
)

type internalActionResult struct {
	Event   string  `json:"event"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
	PaneID  *string `json:"pane_id,omitempty"`
}

func newInternalWSClient() *wsClient {
	return &wsClient{send: make(chan outboundMessage, 4)}
}

func readInternalActionResult(client *wsClient) (internalActionResult, error) {
	select {
	case message := <-client.send:
		var result internalActionResult
		if err := json.Unmarshal(message.payload, &result); err != nil {
			return internalActionResult{}, err
		}
		if !result.Success {
			return result, fmt.Errorf("%s", protocol.Deref(result.Error))
		}
		return result, nil
	default:
		return internalActionResult{}, fmt.Errorf("daemon operation returned no result")
	}
}

func delegationLabel(brief string) string {
	line := strings.TrimSpace(strings.SplitN(brief, "\n", 2)[0])
	if line == "" {
		return "Delegated task"
	}
	const maxRunes = 64
	runes := []rune(line)
	if len(runes) > maxRunes {
		line = string(runes[:maxRunes-3]) + "..."
	}
	return line
}

func (d *Daemon) resolveDelegationAgent(sourceAgent string, requested *string) (string, error) {
	agent := strings.TrimSpace(strings.ToLower(protocol.Deref(requested)))
	if agent == "" {
		agent = strings.TrimSpace(strings.ToLower(sourceAgent))
	}
	if agent == "" || agent == protocol.AgentShellValue {
		agent = string(protocol.SessionAgentCodex)
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if !pluginDriver.Capabilities["initial_prompt"] {
			return "", fmt.Errorf("agent %q does not support initial prompts", agent)
		}
		return pluginDriver.Agent, nil
	}
	driver := agentdriver.Get(agent)
	if driver == nil {
		return "", fmt.Errorf("agent %q is not available", agent)
	}
	if !agentdriver.EffectiveCapabilities(driver).HasInitialPrompt {
		return "", fmt.Errorf("agent %q does not support initial prompts", agent)
	}
	return driver.Name(), nil
}

func delegationPlacement(msg *protocol.DelegateMessage) string {
	placement := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Placement)))
	if placement != "" {
		return placement
	}
	if strings.TrimSpace(protocol.Deref(msg.WorkspaceID)) != "" {
		return delegationPlacementExisting
	}
	if strings.TrimSpace(protocol.Deref(msg.Cwd)) != "" {
		return delegationPlacementNew
	}
	return delegationPlacementCurrent
}

func validateDelegationDirectory(path string) (string, error) {
	path = git.CanonicalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("delegation directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("delegation directory is not a directory: %s", path)
	}
	return path, nil
}

func (d *Daemon) rollbackDelegation(createdWorkspaceID, createdWorktreePath string, cause error) error {
	if createdWorkspaceID != "" {
		d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{
			Cmd: protocol.CmdUnregisterWorkspace,
			ID:  createdWorkspaceID,
		})
	}
	if createdWorktreePath == "" {
		return cause
	}
	if err := d.doDeleteWorktree(createdWorktreePath, nil, deleteWorktreeOptions{}); err != nil {
		return fmt.Errorf("%w; rollback worktree %s: %v", cause, createdWorktreePath, err)
	}
	return cause
}

func (d *Daemon) createDelegationWorktree(sourceDirectory string, request *protocol.DelegateWorktreeRequest) (string, error) {
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return "", fmt.Errorf("worktree branch is required")
	}
	repo := strings.TrimSpace(protocol.Deref(request.Repo))
	if repo == "" {
		repoRoot, err := git.GetRepoRoot(sourceDirectory)
		if err != nil {
			return "", fmt.Errorf("source directory is not in a git repository; pass --repo")
		}
		repo = git.ResolveMainRepoPath(repoRoot)
	}
	startingFrom := request.StartingFrom
	if strings.TrimSpace(protocol.Deref(startingFrom)) == "" {
		sourceRoot, sourceRootErr := git.GetRepoRoot(sourceDirectory)
		sourceBranch, sourceBranchErr := git.GetBranchInfo(sourceDirectory)
		if sourceRootErr == nil &&
			sourceBranchErr == nil &&
			sourceBranch != nil &&
			strings.TrimSpace(sourceBranch.Branch) != "" &&
			git.ResolveMainRepoPath(sourceRoot) == git.ResolveMainRepoPath(repo) {
			startingFrom = protocol.Ptr(strings.TrimSpace(sourceBranch.Branch))
		}
	}
	worktreePath, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		Cmd:          protocol.CmdCreateWorktree,
		MainRepo:     repo,
		Branch:       branch,
		Path:         request.Path,
		StartingFrom: startingFrom,
	})
	if err != nil {
		if worktreePath != "" {
			return "", d.rollbackDelegation("", worktreePath, fmt.Errorf("create delegated worktree: %w", err))
		}
		return "", fmt.Errorf("create delegated worktree: %w", err)
	}
	return worktreePath, nil
}

func (d *Daemon) delegate(msg *protocol.DelegateMessage) (*protocol.DelegateResult, error) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		return nil, fmt.Errorf("source_session_id is required")
	}
	brief := strings.TrimSpace(msg.Brief)
	if brief == "" {
		return nil, fmt.Errorf("brief is required")
	}
	source := d.store.Get(sourceSessionID)
	if source == nil {
		return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
	}
	if endpointID := strings.TrimSpace(protocol.Deref(source.EndpointID)); endpointID != "" {
		return nil, fmt.Errorf("delegation from remote session %s on endpoint %s is not supported", sourceSessionID, endpointID)
	}
	agent, err := d.resolveDelegationAgent(source.Agent, msg.Agent)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(protocol.Deref(msg.Label))
	if label == "" {
		label = delegationLabel(brief)
	}
	sessionID := uuid.NewString()
	chiefSessionID := d.chiefOfStaffSessionID()
	trackedByChief := chiefSessionID == sourceSessionID
	paneID := "pane-" + sessionID
	placement := delegationPlacement(msg)
	workspaceID := ""
	directory := ""
	createdWorkspaceID := ""
	createdWorktreePath := ""

	switch placement {
	case delegationPlacementCurrent:
		if strings.TrimSpace(protocol.Deref(msg.WorkspaceID)) != "" || strings.TrimSpace(protocol.Deref(msg.Cwd)) != "" {
			return nil, fmt.Errorf("current_workspace placement does not accept workspace_id or cwd")
		}
		workspaceID = strings.TrimSpace(source.WorkspaceID)
		if workspaceID == "" || d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("source session has no local workspace")
		}
		directory = source.Directory
	case delegationPlacementExisting:
		if msg.Worktree != nil || strings.TrimSpace(protocol.Deref(msg.Cwd)) != "" {
			return nil, fmt.Errorf("existing_workspace placement does not accept cwd or worktree")
		}
		workspaceID = strings.TrimSpace(protocol.Deref(msg.WorkspaceID))
		workspace := d.store.GetWorkspace(workspaceID)
		if workspaceID == "" || workspace == nil {
			return nil, fmt.Errorf("target workspace not found: %s", workspaceID)
		}
		directory = workspace.Directory
	case delegationPlacementNew:
		if strings.TrimSpace(protocol.Deref(msg.WorkspaceID)) != "" {
			return nil, fmt.Errorf("new_workspace placement does not accept workspace_id")
		}
		directory = strings.TrimSpace(protocol.Deref(msg.Cwd))
		if msg.Worktree != nil {
			if directory != "" {
				return nil, fmt.Errorf("new_workspace placement cannot combine cwd and worktree")
			}
		}
		if directory == "" {
			directory = source.Directory
		}
	default:
		return nil, fmt.Errorf("unsupported placement %q", placement)
	}

	if msg.Worktree != nil {
		worktreePath, createErr := d.createDelegationWorktree(source.Directory, msg.Worktree)
		if createErr != nil {
			return nil, createErr
		}
		createdWorktreePath = worktreePath
		validatedDirectory, directoryErr := validateDelegationDirectory(worktreePath)
		if directoryErr != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, directoryErr)
		}
		directory = validatedDirectory
	}

	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, directoryErr)
		}
		directory = validatedDirectory
		workspaceID = "workspace-" + sessionID
		workspaceTitle := filepath.Base(directory)
		if workspaceTitle == "." || workspaceTitle == string(filepath.Separator) || workspaceTitle == "" {
			workspaceTitle = label
		}
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     workspaceTitle,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, fmt.Errorf("create delegated workspace"))
		}
		createdWorkspaceID = workspaceID
	}

	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(label),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("create delegated pane: %w", err))
	}

	spawnClient := newInternalWSClient()
	initialPrompt := brief
	if trackedByChief {
		initialPrompt = chiefOfStaffDispatchPrompt(brief)
	}
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		WorkspaceID:   workspaceID,
		Agent:         agent,
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(label),
		YoloMode:      msg.YoloMode,
		InitialPrompt: protocol.Ptr(initialPrompt),
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("spawn delegated session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("delegated session was not persisted"))
	}
	var dispatch *protocol.ChiefOfStaffDispatch
	if trackedByChief {
		dispatch = d.newChiefOfStaffDispatch(chiefSessionID, session, workspaceID, brief, label, agent)
		if err := d.store.AddChiefOfStaffDispatch(dispatch); err != nil {
			d.unregisterSession(sessionID, syscall.SIGTERM)
			d.removeWorkspaceLayoutPaneForSession(sessionID)
			return nil, d.rollbackDelegation(
				createdWorkspaceID,
				createdWorktreePath,
				fmt.Errorf("persist chief of staff dispatch: %w", err),
			)
		}
		if _, errMsg := d.setWorkspaceMuted(workspaceID, false); errMsg != "" {
			_ = d.store.DeleteChiefOfStaffDispatch(dispatch.ID)
			d.unregisterSession(sessionID, syscall.SIGTERM)
			d.removeWorkspaceLayoutPaneForSession(sessionID)
			return nil, d.rollbackDelegation(
				createdWorkspaceID,
				createdWorktreePath,
				fmt.Errorf("make delegated workspace visible: %s", errMsg),
			)
		}
	}
	result := &protocol.DelegateResult{
		SessionID:   session.ID,
		WorkspaceID: workspaceID,
		Directory:   session.Directory,
		Placement:   placement,
	}
	if createdWorktreePath != "" {
		result.WorktreeCreated = protocol.Ptr(true)
	}
	if session.Branch != nil && strings.TrimSpace(*session.Branch) != "" {
		result.Branch = protocol.Ptr(strings.TrimSpace(*session.Branch))
	}
	if dispatch != nil {
		result.DispatchID = protocol.Ptr(dispatch.ID)
		d.broadcastChiefOfStaffDispatchesUpdated()
	}
	return result, nil
}

func (d *Daemon) handleDelegate(conn net.Conn, msg *protocol.DelegateMessage) {
	result, err := d.delegate(msg)
	if err != nil {
		d.sendError(conn, "delegate: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:             true,
		DelegateResult: result,
	})
}

func (d *Daemon) handleDelegateWS(client *wsClient, msg *protocol.DelegateMessage) {
	result, err := d.delegate(msg)
	response := protocol.DelegateResultMessage{
		Event:   protocol.EventDelegateResult,
		Success: err == nil,
		Result:  result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
