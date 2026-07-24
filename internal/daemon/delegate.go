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
	"github.com/victorarias/attn/internal/git"
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

// maxDelegationNameRunes bounds a delegated session/workspace display name.
// Names are short, human, and glanceable in the sidebar; longer strings (e.g. a
// worktree folder like "attn--feat-some-long-branch") are rejected so the caller
// supplies a real name with --name.
const maxDelegationNameRunes = 16

// validateDelegationName enforces the naming rules for a resolved delegation
// name (whether it came from --name or the directory-basename default):
//
//   - non-empty and at most maxDelegationNameRunes runes
//   - when a new workspace is being created, unique across workspace titles
//   - unique among the session labels already in the target workspace
//
// targetWorkspaceID is the workspace whose sessions are checked for a clash; it
// is empty when a brand-new (and therefore empty) workspace is being created.
func (d *Daemon) validateDelegationName(name string, creatingWorkspace bool, targetWorkspaceID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a name is required; pass --name")
	}
	if name == "." || name == string(filepath.Separator) {
		// A directory-basename default can degenerate to "." or "/" for an odd
		// directory; those are not usable names, so ask for an explicit one.
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

// truncateDelegationName shortens a directory-basename-derived name to fit
// maxDelegationNameRunes. Unlike an explicit --name (which must fail loudly so
// the caller learns the limit), a derived default should just fit — a worktree
// checkout like "attn--feat-some-long-branch" always exceeds 16 runes, and
// erroring there would make --worktree unusable without also passing --name.
// Trailing "-", "_", ".", and whitespace are trimmed off the cut so the result
// reads cleanly (e.g. "attn--feat-agent" rather than "attn--feat-agent-").
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

// validateDelegationModelEffort rejects --model / --effort for agents whose
// launch command cannot apply them, so the pin fails fast at delegate time
// instead of being silently dropped by the spawned session. Values themselves
// are passed through (aliases, full ids, and new effort levels stay legal
// without an allowlist to rot); the agent CLI is the authority on them.
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

// delegationWorktreeRepo resolves the main repository a worktree delegated into
// an existing workspace belongs to, or "" when the workspace offers nothing to
// infer from.
//
// A workspace's stored Directory is the location it was last registered at, not
// a claim about the repositories its sessions occupy. It is overwritten on every
// re-registration (unlike title/rank/muted/pinned, which are deliberately
// preserved), it is inherited wholesale when a pane is dragged out into a new
// workspace, and it is never recomputed when a member session moves into a
// worktree. It can therefore name a repository no member session has ever been
// in, and trusting it here silently created worktrees in unrelated repositories.
//
// The member sessions are the authority instead: they carry a directory that is
// re-derived from the real cwd on every register and spawn. When they disagree
// on a main repository the choice is genuinely ambiguous, so fail and ask for
// --repo rather than guess — a confusing error beats a silent misplacement.
//
// This deliberately answers only "which repository". Several member sessions can
// sit in different worktrees of that one repository, each on its own branch, so
// no member session's branch is a defensible starting point for the new one;
// picking a representative session here would make the starting ref depend on
// session ordering. Starting-ref selection is left to the caller (see the
// worktreeStartRefBase comment in delegateOperation).
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
		// Distinct worktrees of one repository all resolve to the same main
		// repository, so they are not an ambiguity.
		repo := git.ResolveMainRepoPath(root)
		if _, ok := seen[repo]; ok {
			continue
		}
		seen[repo] = struct{}{}
		repos = append(repos, repo)
	}

	switch len(repos) {
	case 0:
		// An empty or non-git workspace offers nothing to infer from; the caller
		// falls back to the stored directory so its own repo check reports it.
		return "", nil
	case 1:
		return repos[0], nil
	default:
		sort.Strings(repos)
		return "", fmt.Errorf("workspace %s spans multiple repositories (%s); pass --repo to choose which one the worktree branches from",
			workspaceID, strings.Join(repos, ", "))
	}
}

// delegationDefaultStartRef names the ref a delegated worktree starts from when
// no working directory supplies a defensible starting point. It returns "" when
// the repository's default branch cannot be resolved at all, leaving the caller
// with git's own current-HEAD behaviour as a last resort.
//
// Prefers the remote-tracking ref, matching how the app's own new-worktree flow
// defaults (RepoOptions.tsx), so a delegated branch starts from what upstream
// has rather than from however stale the local checkout is; doCreateWorktree
// fetches it before creating. Falls back to the local default branch for
// repositories with no matching remote branch.
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

// createDelegationWorktree creates the worktree. inferredRepo, when non-empty,
// names the main repository already resolved by the caller; baseDirectory, when
// non-empty, is a working directory the repository and the starting ref may be
// inferred from. A caller that knows the repository but has no defensible
// starting point passes the repository alone and leaves baseDirectory empty.
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
			// Git creation and SQLite ownership cannot be one atomic transaction.
			// A crash after `git worktree add` but before Mark...Owned leaves the
			// path ambiguous: it may be ours, or another actor may have created it
			// after the durable intent record. The product contract permits a
			// terminal failure on restart; never adopt or delete without proof.
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
	if strings.TrimSpace(protocol.Deref(startingFrom)) == "" && baseDirectory == "" {
		// No working directory whose branch could serve as a starting point, so
		// name the repository's default branch explicitly. Leaving this empty
		// would make `git worktree add -b` start from the main checkout's current
		// HEAD (see starting_from in the protocol schema) — whatever that
		// checkout happens to have checked out today, which is the same kind of
		// ambient state this resolution exists to eliminate.
		if ref := delegationDefaultStartRef(repo); ref != "" {
			startingFrom = protocol.Ptr(ref)
		}
	}
	if strings.TrimSpace(protocol.Deref(startingFrom)) == "" && baseDirectory != "" {
		baseRoot, baseRootErr := git.GetRepoRoot(baseDirectory)
		baseBranch, baseBranchErr := git.GetBranchInfo(baseDirectory)
		if baseRootErr == nil &&
			baseBranchErr == nil &&
			baseBranch != nil &&
			strings.TrimSpace(baseBranch.Branch) != "" &&
			git.ResolveMainRepoPath(baseRoot) == git.ResolveMainRepoPath(repo) {
			startingFrom = protocol.Ptr(strings.TrimSpace(baseBranch.Branch))
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
			return "", false, d.rollbackDelegation("", worktreePath, fmt.Errorf("create delegated worktree: %w", err))
		}
		return "", false, fmt.Errorf("create delegated worktree: %w", err)
	}
	if operationID != "" {
		ownerToken := uuid.NewString()
		if err := writeDelegationWorktreeOwner(worktreePath, ownerToken); err != nil {
			return "", false, d.rollbackDelegation("", worktreePath, err)
		}
		if err := d.store.MarkDelegationWorktreeOwned(operationID, worktreePath, ownerToken, time.Now()); err != nil {
			return "", false, d.rollbackDelegation("", worktreePath, fmt.Errorf("record delegated worktree ownership: %w", err))
		}
	}
	return worktreePath, true, nil
}

func (d *Daemon) delegate(msg *protocol.DelegateMessage) (*protocol.DelegateResult, error) {
	return d.delegateOperation(msg, "", "", "", false, "", "")
}

func (d *Daemon) spawnDelegatedRuntime(msg *protocol.DelegateMessage, sessionID, workspaceID, directory, name, agent, model, effort, brief string, trackedByChief bool) error {
	initialPrompt := brief
	if trackedByChief {
		initialPrompt = delegatedTicketPrompt(brief)
	}
	initialPrompt = withLeafIdentity(initialPrompt)
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
	_, err := readInternalActionResult(spawnClient)
	return err
}

func (d *Daemon) delegateOperation(msg *protocol.DelegateMessage, operationID, reservedSessionID, ownedWorktreePath string, worktreeOwned bool, worktreeToken, initiatingChiefSessionID string) (*protocol.DelegateResult, error) {
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
	model := strings.TrimSpace(protocol.Deref(msg.Model))
	effort := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Effort)))
	if err := d.validateDelegationModelEffort(agent, model, effort); err != nil {
		return nil, err
	}
	// name is the explicit --name, or empty to default to the directory basename
	// once the directory is finalized below.
	name := strings.TrimSpace(protocol.Deref(msg.Label))
	sessionID := reservedSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	chiefSessionID := initiatingChiefSessionID
	if operationID == "" && d.chiefOfStaffSessionID() == sourceSessionID {
		chiefSessionID = sourceSessionID
	}
	trackedByChief := chiefSessionID != ""
	paneID := "pane-" + sessionID
	placement := delegationPlacement(msg)
	workspaceID := ""
	directory := ""
	createdWorkspaceID := ""
	createdWorktreePath := ""
	operationWorktreePath := ""
	if existing := d.store.Get(sessionID); existing != nil {
		expectedWorkspaceID := ""
		switch placement {
		case delegationPlacementCurrent:
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
		if !d.sessionHasLiveWorker(sessionID) {
			if operationID != "" {
				_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"recovering delegated runtime", existing.WorkspaceID, "", existing.Directory, nil, nil, time.Now())
			}
			if err := d.spawnDelegatedRuntime(msg, sessionID, existing.WorkspaceID, existing.Directory, existing.Label, agent, model, effort, brief, trackedByChief); err != nil {
				return nil, fmt.Errorf("recover delegated session runtime: %w", err)
			}
		}
		if trackedByChief {
			ticket, ticketErr := d.store.ActiveTicketForSession(sessionID)
			if ticketErr != nil {
				return nil, ticketErr
			}
			if ticket == nil {
				ticketID, ticketErr := d.createDelegatedTicket(chiefSessionID, existing, brief, existing.Label, agent)
				if ticketErr != nil {
					return nil, ticketErr
				}
				if operationID != "" {
					worktreePath := ""
					if msg.Worktree != nil {
						worktreePath = existing.Directory
					}
					_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
						"recovered delegated ticket", existing.WorkspaceID, ticketID, worktreePath, nil, nil, time.Now())
				}
			}
		}
		return d.completedDelegationResult(existing, placement, worktreeOwned), nil
	}

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
			// --cwd + --worktree compose: the worktree's repo and starting ref are
			// inferred from this base directory below (createDelegationWorktree),
			// and the workspace ends up placed at the created worktree path.
			validatedCwd, cwdErr := validateDelegationDirectory(directory)
			if cwdErr != nil {
				return nil, cwdErr
			}
			directory = validatedCwd
		}
		if directory == "" {
			directory = source.Directory
		}
	default:
		return nil, fmt.Errorf("unsupported placement %q", placement)
	}

	// Naming scope: a new workspace must take a globally-unique name; a session
	// must be unique among the sessions already in its target workspace (empty
	// for a brand-new workspace). Validate an explicit --name now, before any
	// side effects, so a bad name fails fast without creating a worktree.
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

	// The workspace record's directory is a registration artifact, not a repo
	// authority (see delegationWorktreeRepo). Only an existing-workspace
	// placement would otherwise infer a repo from it; current/new placements
	// already base off a real session directory or an explicit --cwd, where that
	// directory legitimately supplies both the repository and the starting ref.
	//
	// worktreeStartRefBase is cleared once the repository is known from the
	// member sessions, which decouples the two inferences. Those sessions may sit
	// in different worktrees of the one repository, each on its own branch, so
	// there is no member branch that deserves to become the implicit --from.
	// Clearing it makes createDelegationWorktree resolve the repository's default
	// branch explicitly (delegationDefaultStartRef) rather than a branch chosen
	// by session ordering — and, just as importantly, rather than the main
	// checkout's current HEAD. An explicit --from still wins.
	worktreeStartRefBase := directory
	inferredWorktreeRepo := ""
	if msg.Worktree != nil && placement == delegationPlacementExisting {
		// Unconditionally, including when --repo is given: the workspace record's
		// directory never supplies a starting ref for this placement. --repo
		// selects the repository; only --from selects a non-default starting ref.
		worktreeStartRefBase = ""
		if strings.TrimSpace(protocol.Deref(msg.Worktree.Repo)) == "" {
			resolvedRepo, repoErr := d.delegationWorktreeRepo(workspaceID)
			if repoErr != nil {
				return nil, repoErr
			}
			if resolvedRepo == "" {
				// A workspace with no member sessions to learn from leaves the
				// stored directory as the only remaining signal for *which*
				// repository — still never for which ref.
				if root, rootErr := git.GetRepoRoot(directory); rootErr == nil {
					resolvedRepo = git.ResolveMainRepoPath(root)
				}
			}
			inferredWorktreeRepo = resolvedRepo
		}
	}

	if msg.Worktree != nil {
		worktreePath, created, createErr := d.createDelegationWorktree(worktreeStartRefBase, inferredWorktreeRepo, msg.Worktree, operationID, ownedWorktreePath, worktreeOwned, worktreeToken, protocol.Deref(msg.AllowWorktreeReuse))
		if createErr != nil {
			return nil, createErr
		}
		if created {
			createdWorktreePath = worktreePath
		}
		validatedDirectory, directoryErr := validateDelegationDirectory(worktreePath)
		if directoryErr != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, directoryErr)
		}
		directory = validatedDirectory
		operationWorktreePath = directory
	}
	// Finalize a new workspace's directory before resolving the name so a
	// directory-basename default reflects the real directory.
	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, directoryErr)
		}
		directory = validatedDirectory
	}
	if worktreeRoot, occupied := d.activeSessionInLinkedWorktree(directory); occupied && !protocol.Deref(msg.AllowWorktreeReuse) {
		// Once another active session occupies the worktree, it is no longer safe
		// to roll the directory back even if this operation originally created it.
		return nil, fmt.Errorf("an active session already uses worktree %s; pass --allow-worktree-reuse only when sharing it is intentional", worktreeRoot)
	}

	// Default the name to the directory basename when --name was not given, then
	// validate the final name. Only a worktree may exist at this point, so a
	// validation failure rolls it back (no workspace/pane/session yet).
	if name == "" {
		name = truncateDelegationName(filepath.Base(directory))
		if err := d.validateDelegationName(name, creatingWorkspace, sessionNameWorkspaceID); err != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, err)
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
			return nil, d.rollbackDelegation("", createdWorktreePath, fmt.Errorf("create delegated workspace"))
		}
		createdWorkspaceID = workspaceID
	}
	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"assembling workspace and session", workspaceID, "", operationWorktreePath, nil, nil, time.Now()); err != nil {
			return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, err)
		}
	}

	if existingWorkspaceID, _, found := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); found {
		if existingWorkspaceID != workspaceID {
			return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath,
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
			return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("create delegated pane: %w", err))
		}
	}

	if err := d.spawnDelegatedRuntime(msg, sessionID, workspaceID, directory, name, agent, model, effort, brief, trackedByChief); err != nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("spawn delegated session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil, d.rollbackDelegation(createdWorkspaceID, createdWorktreePath, fmt.Errorf("delegated session was not persisted"))
	}
	if trackedByChief {
		if _, errMsg := d.setWorkspaceMuted(workspaceID, false); errMsg != "" {
			d.unregisterSession(sessionID, syscall.SIGTERM)
			d.removeWorkspaceLayoutPaneForSession(sessionID)
			return nil, d.rollbackDelegation(
				createdWorkspaceID,
				createdWorktreePath,
				fmt.Errorf("make delegated workspace visible: %s", errMsg),
			)
		}
		ticketID, err := d.createDelegatedTicket(chiefSessionID, session, brief, name, agent)
		if err != nil {
			d.unregisterSession(sessionID, syscall.SIGTERM)
			d.removeWorkspaceLayoutPaneForSession(sessionID)
			return nil, d.rollbackDelegation(
				createdWorkspaceID,
				createdWorktreePath,
				fmt.Errorf("create delegated ticket: %w", err),
			)
		}
		d.logf("delegate: bound ticket %q to session %s", ticketID, session.ID)
		if operationID != "" {
			_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
				"delegated session and ticket created", workspaceID, ticketID, operationWorktreePath, nil, nil, time.Now())
		}
		d.broadcastTicketsUpdated()
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
	return result, nil
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

// leafIdentityPreamble is prepended to every delegated agent's initial prompt.
// attn marks a chief of staff with a passive, positive signal (an env var and a
// system-prompt block); a delegated leaf gets nothing analogous — it is defined
// only by the absence of those chief markers, an absence it shares with every
// ordinary top-level session. Without this line, a leaf delegated by a non-chief
// session is byte-identical to an ordinary session and has no way to learn it is
// a leaf, so it can misapply chief-only guidance (like the delegation license) to
// itself. See docs/plans/2026-06-30-delegated-leaf-not-chief.md.
const leafIdentityPreamble = "You are a delegated attn session — a leaf, not a " +
	"coordinator. Do the work below in this session. For your own subtasks, use " +
	"native subagents (your Task/Agent tools), not `attn delegate` — delegating " +
	"offloads your assigned work into a session the user who delegated you isn't " +
	"watching. Spawn a visible attn agent only if the user steering this session " +
	"explicitly asks for one."

// withLeafIdentity prefixes a delegated agent's composed initial prompt with the
// leaf identity line, applied uniformly whether or not the delegation is tracked
// by the chief of staff.
func withLeafIdentity(prompt string) string {
	return leafIdentityPreamble + "\n\n---\n\n" + strings.TrimSpace(prompt)
}

// delegatedTicketPrompt augments a chief-delegated agent's brief with the
// self-report contract: the agent's work is bound to an attn ticket (assignee ==
// session), and it moves that ticket across the board by reporting its own work
// state. This replaces the retired chief-of-staff dispatch reporting surface —
// the chief reads the ticket board instead of a parallel dispatch record.
func delegatedTicketPrompt(brief string) string {
	return strings.TrimSpace(brief) + `

---
This task is tracked as a ticket in attn. Report your work state so the ticket
moves across the board and the chief of staff can see your progress:

    attn ticket status in_progress --comment "<progress and next action>"

Use the state that matches the outcome when work needs input, is ready, or ends:

    attn ticket status needs_input --comment "<needed decision>"
    attn ticket status ready_for_review --comment "<what is ready>"
    attn ticket status completed --comment "<completed outcome>"
    attn ticket status failed --comment "<terminal failure>"

When the deliverable is a durable Markdown plan or design, hand it over with
` + "`" + `attn ticket attach-plan --file <path>` + "`" + `. In a monorepo, add
` + "`" + `--scope <affected-component>` + "`" + `. The command follows the applicable repository
convention: a committed repository plan stays canonical in Git and the ticket gets
a Notebook reference; otherwise the plan is promoted to the Notebook and its
untracked staging source is retired after verification. It never deletes a tracked
source. Use ` + "`" + `ticket attach` + "`" + ` for other artifacts. Keep the reported canonical
source current, and report meaningful edits, renames, or deletions through ticket
status or a ticket comment so the chief can react.

Report ` + "`" + `completed` + "`" + ` when strong terminal evidence shows the requested outcome is
done and no review or decision remains — for example, the user accepted the work or
the requested PR merged. You do not need a separate closure confirmation when that
evidence is already clear. If you merely finished your implementation but acceptance,
review, or another decision is still pending, report ` + "`" + `ready_for_review` + "`" + ` instead.
Report the other states as they happen.

Continue the assigned work after reporting unless you are blocked or waiting on
the user.`
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
		Event:   protocol.EventDelegateResult,
		Success: err == nil,
		Result:  result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
