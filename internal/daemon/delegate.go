package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	delegationPlacementCurrent  = "current_workspace"
	delegationPlacementExisting = "existing_workspace"
	delegationPlacementNew      = "new_workspace"
	delegationWorktreeOwnerFile = "attn-delegation-owner"
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

const maxDelegationNameRunes = 16

func (d *Daemon) validateDelegationName(name string, creatingWorkspace bool, targetWorkspaceID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a name is required; pass --name")
	}
	if name == "." || name == string(filepath.Separator) {
		return fmt.Errorf("%q is not a usable name; pass --name", name)
	}
	if len([]rune(name)) > maxDelegationNameRunes {
		return fmt.Errorf("name %q is too long (max %d characters); pass a shorter --name", name, maxDelegationNameRunes)
	}
	if creatingWorkspace {
		for _, ws := range d.store.ListWorkspaces() {
			if strings.EqualFold(strings.TrimSpace(ws.Title), name) {
				return fmt.Errorf("workspace name %q is already in use; pass a unique --name", name)
			}
		}
	}
	if targetWorkspaceID != "" {
		for _, sessionID := range d.store.SessionsInWorkspace(targetWorkspaceID) {
			existing := d.store.Get(sessionID)
			if existing != nil && strings.EqualFold(strings.TrimSpace(existing.Label), name) {
				return fmt.Errorf("session name %q is already used in this workspace; pass a unique --name", name)
			}
		}
	}
	return nil
}

func truncateDelegationName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxDelegationNameRunes {
		return name
	}
	return strings.TrimRight(string(runes[:maxDelegationNameRunes]), "-_. \t")
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

func (d *Daemon) validateDelegationModelEffort(agent, model, effort string) error {
	if model == "" && effort == "" {
		return nil
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if model != "" && !pluginDriver.Capabilities["model_pin"] {
			return fmt.Errorf("agent %q does not support --model", agent)
		}
		if effort != "" && !pluginDriver.Capabilities["effort_pin"] {
			return fmt.Errorf("agent %q does not support --effort", agent)
		}
		return nil
	}
	caps := agentdriver.EffectiveCapabilities(agentdriver.Get(agent))
	if model != "" && !caps.HasModelPin {
		return fmt.Errorf("agent %q does not support --model", agent)
	}
	if effort != "" && !caps.HasEffortPin {
		return fmt.Errorf("agent %q does not support --effort", agent)
	}
	return nil
}

func (d *Daemon) defaultDelegationEffort(agent, effort string) string {
	if effort != "" {
		return effort
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if pluginDriver.Capabilities["effort_pin"] {
			return "medium"
		}
		return ""
	}
	if agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasEffortPin {
		return "medium"
	}
	return ""
}

func resolveDelegationRepository(path, flagName string) (string, error) {
	root, err := git.GetRepoRoot(path)
	if err != nil {
		return "", fmt.Errorf("%s %s is not in a Git repository", flagName, git.CanonicalizePath(path))
	}
	return git.ResolveMainRepoPath(root), nil
}

func validateDelegationRepositoryInputs(cwd string, request *protocol.DelegateWorktreeRequest) error {
	if request == nil || strings.TrimSpace(cwd) == "" || strings.TrimSpace(protocol.Deref(request.Repo)) == "" {
		return nil
	}
	cwdRepo, err := resolveDelegationRepository(cwd, "--cwd")
	if err != nil {
		return err
	}
	explicitRepo, err := resolveDelegationRepository(protocol.Deref(request.Repo), "--repo")
	if err != nil {
		return err
	}
	if git.CanonicalizePath(cwdRepo) == git.CanonicalizePath(explicitRepo) {
		return nil
	}
	return fmt.Errorf("repository placement conflict: --cwd resolves to %s, but --repo resolves to %s; remove --repo to branch from --cwd, or make both flags point to the same repository", cwdRepo, explicitRepo)
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

func (d *Daemon) activeSessionInLinkedWorktree(directory string) (string, bool) {
	worktreeRoot, err := git.GetRepoRoot(directory)
	if err != nil || git.GetMainRepoFromWorktree(worktreeRoot) == "" {
		return "", false
	}
	worktreeRoot = git.CanonicalizePath(worktreeRoot)
	for _, session := range d.store.List("") {
		if session.State == protocol.SessionStateIdle || session.State == protocol.SessionStateRecoverable {
			continue
		}
		sessionRoot, err := git.GetRepoRoot(session.Directory)
		if err == nil && git.CanonicalizePath(sessionRoot) == worktreeRoot {
			return worktreeRoot, true
		}
	}
	return worktreeRoot, false
}

// delegationRollback unwinds newest first: a session stops before its pane is removed,
// the pane before its workspace, the workspace before the worktree it points at.
type delegationRollback struct {
	d    *Daemon
	undo []func() error
}

func (d *Daemon) newDelegationRollback() *delegationRollback {
	return &delegationRollback{d: d}
}

func (r *delegationRollback) fail(cause error) error {
	for i := len(r.undo) - 1; i >= 0; i-- {
		if err := r.undo[i](); err != nil {
			cause = fmt.Errorf("%w; %v", cause, err)
		}
	}
	r.undo = nil
	return cause
}

// Only correct when EVERY pending compensation must not be performed; not a
// general "skip cleanup".
func (r *delegationRollback) abandon() {
	r.undo = nil
}

// reused or adopted worktree must never be pushed here.
func (r *delegationRollback) onWorktreeCreated(path string) {
	r.undo = append(r.undo, func() error {
		if err := r.d.doDeleteWorktree(path, nil, deleteWorktreeOptions{}); err != nil {
			return fmt.Errorf("rollback worktree %s: %v", path, err)
		}
		return nil
	})
}

func (r *delegationRollback) onWorkspaceCreated(workspaceID string) {
	r.undo = append(r.undo, func() error {
		r.d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{
			Cmd: protocol.CmdUnregisterWorkspace,
			ID:  workspaceID,
		})
		return nil
	})
}

func (r *delegationRollback) onPaneCreated(sessionID string) {
	r.undo = append(r.undo, func() error {
		r.d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil
	})
}

func (r *delegationRollback) onSessionSpawned(sessionID string) {
	r.undo = append(r.undo, func() error {
		r.d.unregisterSession(sessionID, syscall.SIGTERM)
		return nil
	})
}

func delegationWorktreeOwnerPath(worktreePath string) (string, error) {
	out, err := git.Output(git.OpMetadata, worktreePath, "rev-parse", "--git-path", delegationWorktreeOwnerFile)
	if err != nil {
		return "", fmt.Errorf("resolve delegation worktree owner marker: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("resolve delegation worktree owner marker: git returned an empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktreePath, path)
	}
	return filepath.Clean(path), nil
}

func writeDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write delegation worktree owner marker: %w", err)
	}
	return nil
}

func verifyDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read delegation worktree owner marker: %w", err)
	}
	if token == "" || strings.TrimSpace(string(contents)) != token {
		return fmt.Errorf("delegation worktree owner marker does not match")
	}
	return nil
}

// The member sessions are the authority on the repository, not the workspace's stored
// Directory: a dragged-out pane inherits that wholesale and can name another repo.
func (d *Daemon) delegationWorktreeRepo(workspaceID string) (string, error) {
	seen := map[string]struct{}{}
	var repos []string
	for _, sessionID := range d.store.SessionsInWorkspace(workspaceID) {
		session := d.store.Get(sessionID)
		if session == nil || strings.TrimSpace(session.Directory) == "" {
			continue
		}
		root, err := git.GetRepoRoot(session.Directory)
		if err != nil {
			continue
		}
		repo := git.ResolveMainRepoPath(root)
		if _, ok := seen[repo]; ok {
			continue
		}
		seen[repo] = struct{}{}
		repos = append(repos, repo)
	}

	switch len(repos) {
	case 0:
		return "", nil
	case 1:
		return repos[0], nil
	default:
		sort.Strings(repos)
		return "", fmt.Errorf("workspace %s spans multiple repositories (%s); pass --repo to choose which one the worktree branches from",
			workspaceID, strings.Join(repos, ", "))
	}
}

// Prefers the remote-tracking ref, matching the app's new-worktree flow
// (RepoOptions.tsx), so a delegated branch starts from upstream, not a stale local.
func delegationDefaultStartRef(repo string) string {
	branch, err := git.GetDefaultBranch(repo)
	if err != nil {
		return ""
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	if remoteRef := "origin/" + branch; git.RefExists(repo, remoteRef) {
		return remoteRef
	}
	if git.RefExists(repo, branch) {
		return branch
	}
	return ""
}

func automaticDelegationBranch(label, sessionID string) string {
	slug := ticketSlug(label)
	if slug == "ticket" {
		slug = "work"
	}
	suffix := strings.ReplaceAll(sessionID, "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return "delegate/" + slug + "-" + suffix
}

// A nil worktree request is an explicit opt-out.
func (d *Daemon) applyDefaultDelegationWorktree(msg *protocol.DelegateMessage, placement, workspaceID, directory, sessionID, label string) error {
	if msg.Worktree == nil {
		return nil
	}
	if strings.TrimSpace(msg.Worktree.Branch) != "" {
		return nil
	}

	request := msg.Worktree
	configuredWorktree := strings.TrimSpace(protocol.Deref(request.Repo)) != "" ||
		strings.TrimSpace(protocol.Deref(request.Path)) != "" ||
		strings.TrimSpace(protocol.Deref(request.StartingFrom)) != ""
	repo := strings.TrimSpace(protocol.Deref(request.Repo))
	if repo == "" && placement == delegationPlacementExisting {
		resolvedRepo, err := d.delegationWorktreeRepo(workspaceID)
		if err != nil {
			return err
		}
		repo = resolvedRepo
	}
	if repo == "" {
		root, err := git.GetRepoRoot(directory)
		if err != nil {
			if configuredWorktree {
				return fmt.Errorf("workspace directory is not in a git repository; pass --repo")
			}
			if placement == delegationPlacementCurrent {
				return fmt.Errorf("source directory %s is not a git repository, so this delegate would launch with no checkout; place it with --cwd <repo> or --workspace <id>, or pass --no-worktree to delegate without one", directory)
			}
			msg.Worktree = nil
			return nil
		}
		repo = root
	}
	repo = git.ResolveMainRepoPath(repo)

	request.Repo = protocol.Ptr(repo)
	request.Branch = automaticDelegationBranch(label, sessionID)
	msg.Worktree = request
	return nil
}

func (d *Daemon) createDelegationWorktree(baseDirectory, inferredRepo string, request *protocol.DelegateWorktreeRequest, operationID, ownedPath string, worktreeOwned bool, ownedToken string, allowReuse bool) (string, bool, error) {
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return "", false, fmt.Errorf("worktree branch is required")
	}
	repo := strings.TrimSpace(protocol.Deref(request.Repo))
	if repo == "" {
		repo = strings.TrimSpace(inferredRepo)
	}
	if repo == "" {
		// Never call git with an empty directory: it would run in the daemon's own
		// working directory and could resolve to an unrelated repository.
		if baseDirectory == "" {
			return "", false, fmt.Errorf("cannot determine which repository the worktree belongs to; pass --repo")
		}
		repoRoot, err := git.GetRepoRoot(baseDirectory)
		if err != nil {
			return "", false, fmt.Errorf("workspace directory is not in a git repository; pass --repo")
		}
		repo = git.ResolveMainRepoPath(repoRoot)
	}
	expectedPath := strings.TrimSpace(protocol.Deref(request.Path))
	if expectedPath == "" {
		expectedPath = git.GenerateWorktreePath(repo, branch)
	}
	expectedPath = git.CanonicalizePath(expectedPath)
	if _, statErr := os.Stat(expectedPath); statErr == nil {
		if pathRepo, pathErr := resolveDelegationRepository(expectedPath, "--worktree-path"); pathErr == nil && git.CanonicalizePath(pathRepo) != git.CanonicalizePath(repo) {
			return "", false, fmt.Errorf("repository placement conflict: selected repository resolves to %s, but --worktree-path resolves to %s; choose a worktree path from the selected repository or correct --repo/--cwd", repo, pathRepo)
		}
		wt := d.discoverWorktree(expectedPath)
		if wt == nil || strings.TrimSpace(wt.Branch) != branch {
			return "", false, fmt.Errorf("worktree path already exists and is not branch %q: %s", branch, expectedPath)
		}
		if allowReuse {
			return expectedPath, false, nil
		}
		if worktreeOwned && git.CanonicalizePath(ownedPath) == expectedPath {
			if err := verifyDelegationWorktreeOwner(expectedPath, ownedToken); err != nil {
				return "", false, fmt.Errorf("worktree %s was created before delegation preparation was interrupted, but its current ownership cannot be proven (%v), so it was left untouched", expectedPath, err)
			}
			return expectedPath, true, nil
		}
		if operationID != "" && ownedPath != "" && git.CanonicalizePath(ownedPath) == expectedPath {
			// Git creation and SQLite ownership cannot be one transaction: never adopt or
			// delete an ambiguous path without proof.
			return "", false, fmt.Errorf("worktree %s appeared while delegation preparation was interrupted; ownership cannot be proven, so it was left untouched", expectedPath)
		}
		return "", false, fmt.Errorf("worktree %s already exists; pass --allow-worktree-reuse only when sharing it is intentional", expectedPath)
	} else if !os.IsNotExist(statErr) {
		return "", false, fmt.Errorf("inspect delegated worktree path: %w", statErr)
	}
	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"preparing worktree "+expectedPath, "", "", expectedPath, nil, nil, time.Now()); err != nil {
			return "", false, fmt.Errorf("record delegated worktree preparation: %w", err)
		}
	}
	if d.delegationWorktreePrepareHook != nil {
		d.delegationWorktreePrepareHook(expectedPath)
	}
	startingFrom := request.StartingFrom
	if protocol.Deref(request.ExistingBranch) && strings.TrimSpace(protocol.Deref(startingFrom)) != "" {
		return "", false, fmt.Errorf("an existing branch does not accept a starting ref")
	}
	if !protocol.Deref(request.ExistingBranch) && strings.TrimSpace(protocol.Deref(startingFrom)) == "" {
		ref := delegationDefaultStartRef(repo)
		if ref == "" {
			return "", false, fmt.Errorf("cannot determine the repository's default branch; pass --from or --no-worktree")
		}
		startingFrom = protocol.Ptr(ref)
	}
	var (
		worktreePath string
		err          error
	)
	if protocol.Deref(request.ExistingBranch) {
		worktreePath, err = d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: branch, Path: request.Path,
		})
	} else {
		worktreePath, err = d.doCreateWorktree(&protocol.CreateWorktreeMessage{
			Cmd:          protocol.CmdCreateWorktree,
			MainRepo:     repo,
			Branch:       branch,
			Path:         request.Path,
			StartingFrom: startingFrom,
		})
	}
	if err != nil {
		if worktreePath == "" {
			return "", false, fmt.Errorf("create delegated worktree: %w", err)
		}
		rollback := d.newDelegationRollback()
		rollback.onWorktreeCreated(worktreePath)
		return "", false, rollback.fail(fmt.Errorf("create delegated worktree: %w", err))
	}
	rollback := d.newDelegationRollback()
	rollback.onWorktreeCreated(worktreePath)
	if operationID != "" {
		ownerToken := uuid.NewString()
		if err := writeDelegationWorktreeOwner(worktreePath, ownerToken); err != nil {
			return "", false, rollback.fail(err)
		}
		if err := d.store.MarkDelegationWorktreeOwned(operationID, worktreePath, ownerToken, time.Now()); err != nil {
			return "", false, rollback.fail(fmt.Errorf("record delegated worktree ownership: %w", err))
		}
	}
	rollback.abandon()
	return worktreePath, true, nil
}

func (d *Daemon) delegate(msg *protocol.DelegateMessage) (*protocol.DelegateResult, error) {
	resolved, err := d.resolveDelegationPreferences(msg)
	if err != nil {
		return nil, err
	}
	return d.delegateOperation(msg, "", "", "", false, "", "", resolved)
}

func (d *Daemon) spawnDelegatedRuntime(msg *protocol.DelegateMessage, sessionID, workspaceID, directory, name, agent, model, effort, brief string, fromChief bool, guidance string) error {
	seedID := ""
	initialPrompt := ""
	var err error
	if msg.Handover != nil {
		seedID = strings.TrimSpace(msg.Handover.SeedID)
		initialPrompt = withLeafIdentity(handoverPrompt(brief, protocol.Deref(msg.Handover.Handoff), seedID))
	} else {
		// Bound before the spawn: the launch primer and the delegate's own prompt
		// both read the seed it reports to.
		seedID, err = d.bindDelegationSeed(sessionID, strings.TrimSpace(msg.SourceSessionID),
			brief, name, strings.TrimSpace(protocol.Deref(msg.Plot)), directory, agent, fromChief)
		if err != nil {
			return err
		}
		initialPrompt = withLeafIdentity(delegatedBriefPrompt(brief, seedID))
	}
	if guidance != "" {
		initialPrompt = prompts.DelegationOpeningWithGuidance(initialPrompt, guidance)
	}
	spawnMsg := &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		WorkspaceID:   workspaceID,
		Agent:         agent,
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(name),
		YoloMode:      msg.YoloMode,
		InitialPrompt: protocol.Ptr(initialPrompt),
	}
	if model != "" {
		spawnMsg.Model = protocol.Ptr(model)
	}
	if effort != "" {
		spawnMsg.Effort = protocol.Ptr(effort)
	}
	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, spawnMsg)
	_, err = readInternalActionResult(spawnClient)
	return err
}

func (d *Daemon) delegateOperation(msg *protocol.DelegateMessage, operationID, reservedSessionID, ownedWorktreePath string, worktreeOwned bool, worktreeToken, initiatingChiefSessionID string, resolved *delegationprefs.Resolved) (*protocol.DelegateResult, error) {
	guidance := ""
	if resolved != nil {
		copy := *msg
		msg = &copy
		s := resolved.Selection
		msg.Agent = &s.Harness
		msg.Model = &s.Model
		msg.Effort = &s.Effort
		if s.Provider != "" {
			joined := s.Provider + "/" + s.Model
			msg.Model = &joined
		}
		guidance = prompts.DelegationExecutionGuidance(resolved.RoleName, resolved.Instructions, resolved.StoppingPoint)
	}
	sessionID := reservedSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if ticketID := strings.TrimSpace(protocol.Deref(msg.TicketID)); ticketID != "" {
		return nil, fmt.Errorf(
			"delegating onto ticket %s retired: plant the work as a seed and dispatch at it — `attn seed plant \"<title>\" -m \"<brief>\"`, then `attn delegate --brief \"<brief>\" --plot <seed-id>`",
			ticketID)
	}
	handover, err := d.prepareSeedHandover(msg, operationID, sessionID)
	if err != nil {
		return nil, err
	}
	brief := strings.TrimSpace(msg.Brief)
	if brief == "" && handover == nil {
		return nil, fmt.Errorf("a brief is required")
	}
	var source *protocol.Session
	if sourceSessionID != "" {
		source = d.store.Get(sourceSessionID)
	}
	if source == nil && handover == nil {
		if sourceSessionID == "" {
			return nil, fmt.Errorf("source_session_id is required")
		}
		return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
	}
	if source != nil {
		if endpointID := strings.TrimSpace(protocol.Deref(source.EndpointID)); endpointID != "" {
			return nil, fmt.Errorf("delegation from remote session %s on endpoint %s is not supported", sourceSessionID, endpointID)
		}
	}
	sourceAgent := ""
	if source != nil {
		sourceAgent = string(source.Agent)
	}
	agent, err := d.resolveDelegationAgent(sourceAgent, msg.Agent)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(protocol.Deref(msg.Model))
	effort := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Effort)))
	if err := d.validateDelegationModelEffort(agent, model, effort); err != nil {
		return nil, err
	}
	if resolved == nil && msg.Effort == nil {
		effort = d.defaultDelegationEffort(agent, effort)
	}
	if handover == nil {
		if err := d.validateDispatchCrown(strings.TrimSpace(protocol.Deref(msg.Plot)), sourceSessionID); err != nil {
			return nil, err
		}
	}
	name := strings.TrimSpace(protocol.Deref(msg.Label))
	delegatedByChief := initiatingChiefSessionID != "" ||
		(operationID == "" && d.chiefOfStaffSessionID() == sourceSessionID)
	paneID := "pane-" + sessionID
	placement := delegationPlacement(msg)
	workspaceID := ""
	directory := ""
	createdWorktreePath := ""
	operationWorktreePath := ""
	rollback := d.newDelegationRollback()
	if existing := d.store.Get(sessionID); existing != nil {
		expectedWorkspaceID := ""
		switch placement {
		case delegationPlacementCurrent:
			if source == nil {
				return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
			}
			expectedWorkspaceID = source.WorkspaceID
		case delegationPlacementExisting:
			expectedWorkspaceID = strings.TrimSpace(protocol.Deref(msg.WorkspaceID))
		case delegationPlacementNew:
			expectedWorkspaceID = "workspace-" + sessionID
		}
		if existing.WorkspaceID == "" && expectedWorkspaceID != "" {
			d.store.AssignSessionWorkspace(sessionID, expectedWorkspaceID)
			existing.WorkspaceID = expectedWorkspaceID
		}
		if name != "" && existing.Label != name {
			d.store.UpdateSessionLabel(sessionID, name)
			existing.Label = name
		}
		var watch *launchWatch
		if !d.sessionHasLiveWorker(sessionID) {
			if operationID != "" {
				_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"recovering delegated runtime", existing.WorkspaceID, "", existing.Directory, nil, nil, time.Now())
			}
			watch = d.watchLaunch(sessionID)
			if err := d.spawnDelegatedRuntime(msg, sessionID, existing.WorkspaceID, existing.Directory, existing.Label, agent, model, effort, brief, delegatedByChief, guidance); err != nil {
				d.forgetLaunchWatch(sessionID, watch)
				return nil, fmt.Errorf("recover delegated session runtime: %w", err)
			}
		}
		if handover != nil && !handover.alreadyBound {
			if _, err := d.bindSeedHandover(msg, operationID, sessionID, existing.Directory, agent, delegatedByChief); err != nil {
				return nil, fmt.Errorf("finish seed Handover: %w", err)
			}
		}
		if operationID != "" {
			worktreePath := ""
			if msg.Worktree != nil {
				worktreePath = existing.Directory
			}
			_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
				"recovered delegated session", existing.WorkspaceID, "", worktreePath, nil, nil, time.Now())
		}
		result := d.completedDelegationResult(existing, placement, worktreeOwned)
		if watch != nil {
			if err := d.confirmDelegatedLaunch(operationID, sessionID, agent, watch, result); err != nil {
				return nil, err
			}
		}
		return result, nil
	}

	switch placement {
	case delegationPlacementCurrent:
		if source == nil {
			return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
		}
		if strings.TrimSpace(protocol.Deref(msg.WorkspaceID)) != "" || strings.TrimSpace(protocol.Deref(msg.Cwd)) != "" {
			return nil, fmt.Errorf("current_workspace placement does not accept workspace_id or cwd")
		}
		workspaceID = strings.TrimSpace(source.WorkspaceID)
		if workspaceID == "" || d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("source session has no local workspace")
		}
		directory = source.Directory
	case delegationPlacementExisting:
		if strings.TrimSpace(protocol.Deref(msg.Cwd)) != "" {
			return nil, fmt.Errorf("existing_workspace placement does not accept cwd")
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
		if directory != "" && msg.Worktree != nil {
			validatedCwd, cwdErr := validateDelegationDirectory(directory)
			if cwdErr != nil {
				return nil, cwdErr
			}
			directory = validatedCwd
			if repoErr := validateDelegationRepositoryInputs(directory, msg.Worktree); repoErr != nil {
				return nil, repoErr
			}
		}
		if directory == "" {
			if source != nil {
				directory = source.Directory
			}
		}
	default:
		return nil, fmt.Errorf("unsupported placement %q", placement)
	}

	// A workspace places the pane, never the checkout: without a worktree or an
	// explicit --cwd, the agent stays in the source session's checkout.
	if msg.Worktree == nil && strings.TrimSpace(protocol.Deref(msg.Cwd)) == "" {
		directory = source.Directory
	}

	if err := d.applyDefaultDelegationWorktree(msg, placement, workspaceID, directory, sessionID, name); err != nil {
		return nil, err
	}

	creatingWorkspace := placement == delegationPlacementNew
	sessionNameWorkspaceID := ""
	if !creatingWorkspace {
		sessionNameWorkspaceID = workspaceID
	}
	if name != "" {
		if err := d.validateDelegationName(name, creatingWorkspace, sessionNameWorkspaceID); err != nil {
			return nil, err
		}
	}

	inferredWorktreeRepo := ""
	if msg.Worktree != nil && placement == delegationPlacementExisting {
		if strings.TrimSpace(protocol.Deref(msg.Worktree.Repo)) == "" {
			resolvedRepo, repoErr := d.delegationWorktreeRepo(workspaceID)
			if repoErr != nil {
				return nil, repoErr
			}
			if resolvedRepo == "" {
				if root, rootErr := git.GetRepoRoot(directory); rootErr == nil {
					resolvedRepo = git.ResolveMainRepoPath(root)
				}
			}
			inferredWorktreeRepo = resolvedRepo
		}
	}

	if msg.Worktree != nil {
		worktreePath, created, createErr := d.createDelegationWorktree(directory, inferredWorktreeRepo, msg.Worktree, operationID, ownedWorktreePath, worktreeOwned, worktreeToken, protocol.Deref(msg.AllowWorktreeReuse))
		if createErr != nil {
			return nil, createErr
		}
		if created {
			createdWorktreePath = worktreePath
			rollback.onWorktreeCreated(worktreePath)
		}
		resolvedDirectory, directoryErr := handover.applyRecreatedSubdir(worktreePath)
		if directoryErr != nil {
			return nil, rollback.fail(directoryErr)
		}
		validatedDirectory, directoryErr := validateDelegationDirectory(resolvedDirectory)
		if directoryErr != nil {
			return nil, rollback.fail(directoryErr)
		}
		directory = validatedDirectory
		operationWorktreePath = directory
	}
	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, rollback.fail(directoryErr)
		}
		directory = validatedDirectory
	}
	if worktreeRoot, occupied := d.activeSessionInLinkedWorktree(directory); occupied && !protocol.Deref(msg.AllowWorktreeReuse) {
		// Once another active session occupies the worktree it must not be rolled back,
		// even if this operation created it.
		rollback.abandon()
		return nil, fmt.Errorf("an active session already uses worktree %s; pass --allow-worktree-reuse only when sharing it is intentional", worktreeRoot)
	}

	if name == "" {
		name = truncateDelegationName(filepath.Base(directory))
		if err := d.validateDelegationName(name, creatingWorkspace, sessionNameWorkspaceID); err != nil {
			return nil, rollback.fail(err)
		}
	}

	if placement == delegationPlacementNew {
		workspaceID = "workspace-" + sessionID
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     name,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, rollback.fail(fmt.Errorf("create delegated workspace"))
		}
		rollback.onWorkspaceCreated(workspaceID)
	}
	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"assembling workspace and session", workspaceID, "", operationWorktreePath, nil, nil, time.Now()); err != nil {
			return nil, rollback.fail(err)
		}
	}

	if existingWorkspaceID, _, found := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); found {
		if existingWorkspaceID != workspaceID {
			return nil, rollback.fail(
				fmt.Errorf("reserved delegated pane belongs to workspace %s, want %s", existingWorkspaceID, workspaceID))
		}
	} else {
		paneClient := newInternalWSClient()
		d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
			Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
			WorkspaceID: workspaceID,
			PaneID:      protocol.Ptr(paneID),
			SessionID:   sessionID,
			Title:       protocol.Ptr(name),
		})
		if _, err := readInternalActionResult(paneClient); err != nil {
			return nil, rollback.fail(fmt.Errorf("create delegated pane: %w", err))
		}
	}
	rollback.onPaneCreated(sessionID)

	watch := d.watchLaunch(sessionID)
	if err := d.spawnDelegatedRuntime(msg, sessionID, workspaceID, directory, name, agent, model, effort, brief, delegatedByChief, guidance); err != nil {
		d.forgetLaunchWatch(sessionID, watch)
		return nil, rollback.fail(fmt.Errorf("spawn delegated session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, rollback.fail(fmt.Errorf("delegated session was not persisted"))
	}
	rollback.onSessionSpawned(sessionID)
	if d.delegationFinalizeHook != nil {
		if err := d.delegationFinalizeHook(); err != nil {
			return nil, rollback.fail(err)
		}
	}
	if delegatedByChief {
		if _, errMsg := d.setWorkspaceMuted(workspaceID, false); errMsg != "" {
			return nil, rollback.fail(fmt.Errorf("make delegated workspace visible: %s", errMsg))
		}
	}
	if handover != nil {
		if _, err := d.bindSeedHandover(msg, operationID, sessionID, directory, agent, delegatedByChief); err != nil {
			return nil, rollback.fail(fmt.Errorf("finish seed Handover: %w", err))
		}
	}
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"delegated session bound", workspaceID, "", operationWorktreePath, nil, nil, time.Now())
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
	// Everything built so far stays on a failure here: the dead pane is the
	// evidence, and undoing it would leave nothing to read.
	rollback.abandon()
	if err := d.confirmDelegatedLaunch(operationID, sessionID, agent, watch, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Daemon) confirmDelegatedLaunch(operationID, sessionID, agent string, watch *launchWatch, result *protocol.DelegateResult) error {
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			fmt.Sprintf("waiting for %s's first turn", agent), "", "", "", nil, nil, time.Now())
	}
	outcome := d.awaitDelegatedLaunch(sessionID, watch)
	if outcome.exit != nil {
		seedID, _ := d.gardenDispatchCrown(sessionID)
		d.noteDelegatedExitOnSeed(seedID, agent, sessionID, outcome.exit)
		return delegationExitError(agent, sessionID, outcome.exit)
	}
	if outcome.unconfirmed != "" {
		result.FirstTurnUnconfirmed = protocol.Ptr(outcome.unconfirmed)
	}
	if !outcome.startedAt.IsZero() {
		result.FirstTurnAt = protocol.Ptr(outcome.startedAt.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func (d *Daemon) completedDelegationResult(session *protocol.Session, placement string, worktreeCreated bool) *protocol.DelegateResult {
	result := &protocol.DelegateResult{
		SessionID: session.ID, WorkspaceID: session.WorkspaceID, Directory: session.Directory, Placement: placement,
	}
	if worktreeCreated {
		result.WorktreeCreated = protocol.Ptr(true)
	}
	if branch := strings.TrimSpace(protocol.Deref(session.Branch)); branch != "" {
		result.Branch = protocol.Ptr(branch)
	}
	return result
}

func withLeafIdentity(prompt string) string {
	return prompts.RenderText("delegation", "identity", prompts.Values{"brief": prompt})
}

func delegatedBriefPrompt(brief, seedID string) string {
	return prompts.RenderText("delegation", "brief", prompts.Values{"brief": brief, "seed_id": seedID})
}

func (d *Daemon) handleDelegate(conn net.Conn, msg *protocol.DelegateMessage) {
	operation, err := d.startDelegation(msg)
	if err != nil {
		d.sendError(conn, "delegate: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                  true,
		DelegationOperation: operation,
	})
}

func (d *Daemon) handleDelegateStatus(conn net.Conn, msg *protocol.DelegateStatusMessage) {
	operation, err := d.delegationOperation(msg.ID)
	if err != nil {
		d.sendError(conn, "delegate status: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DelegationOperation: operation})
}

func (d *Daemon) handleDelegateWS(client *wsClient, msg *protocol.DelegateMessage) {
	operation, err := d.startDelegation(msg)
	if err == nil {
		for operation.State == protocol.DelegationOperationStateAccepted || operation.State == protocol.DelegationOperationStatePreparing {
			time.Sleep(100 * time.Millisecond)
			operation, err = d.delegationOperation(operation.OperationID)
			if err != nil {
				break
			}
		}
	}
	var result *protocol.DelegateResult
	if operation != nil {
		result = operation.Result
		if operation.State == protocol.DelegationOperationStateFailed && operation.Error != nil {
			err = fmt.Errorf("%s", protocol.Deref(operation.Error))
		}
	}
	response := protocol.DelegateResultMessage{
		Event:     protocol.EventDelegateResult,
		RequestID: protocol.Ptr(msg.RequestID),
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
