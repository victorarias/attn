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

func (d *Daemon) createDelegationWorktree(baseDirectory string, request *protocol.DelegateWorktreeRequest) (string, error) {
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return "", fmt.Errorf("worktree branch is required")
	}
	repo := strings.TrimSpace(protocol.Deref(request.Repo))
	if repo == "" {
		repoRoot, err := git.GetRepoRoot(baseDirectory)
		if err != nil {
			return "", fmt.Errorf("workspace directory is not in a git repository; pass --repo")
		}
		repo = git.ResolveMainRepoPath(repoRoot)
	}
	startingFrom := request.StartingFrom
	if strings.TrimSpace(protocol.Deref(startingFrom)) == "" {
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
	model := strings.TrimSpace(protocol.Deref(msg.Model))
	effort := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Effort)))
	if err := d.validateDelegationModelEffort(agent, model, effort); err != nil {
		return nil, err
	}
	// name is the explicit --name, or empty to default to the directory basename
	// once the directory is finalized below.
	name := strings.TrimSpace(protocol.Deref(msg.Label))
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

	if msg.Worktree != nil {
		worktreePath, createErr := d.createDelegationWorktree(directory, msg.Worktree)
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

	// Finalize a new workspace's directory before resolving the name so a
	// directory-basename default reflects the real directory.
	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, d.rollbackDelegation("", createdWorktreePath, directoryErr)
		}
		directory = validatedDirectory
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

	spawnClient := newInternalWSClient()
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
	d.handleSpawnSession(spawnClient, spawnMsg)
	if _, err := readInternalActionResult(spawnClient); err != nil {
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

    "$ATTN_WRAPPER_PATH" ticket status in_progress --comment "<progress and next action>"

Use the state that matches the outcome when work needs input, is ready, or ends:

    "$ATTN_WRAPPER_PATH" ticket status needs_input --comment "<needed decision>"
    "$ATTN_WRAPPER_PATH" ticket status ready_for_review --comment "<what is ready>"
    "$ATTN_WRAPPER_PATH" ticket status completed --comment "<completed outcome>"
    "$ATTN_WRAPPER_PATH" ticket status failed --comment "<terminal failure>"

When the deliverable is a durable plan, design, or other Markdown artifact, hand
it over with ` + "`" + `"$ATTN_WRAPPER_PATH" ticket attach --file <path>` + "`" + `. Use repeatable
` + "`" + `--file` + "`" + ` flags for multiple artifacts and optionally include ` + "`" + `--state` + "`" + ` and
` + "`" + `--comment` + "`" + `. After success, the returned Notebook paths are canonical: keep
those files current, and report meaningful edits, renames, or deletions through
ticket status or a ticket comment so the chief can react.

Closing a ticket is the user's call, not yours: when you believe the work is
done, ask the user to confirm and wait for their go-ahead before you report the
completed state. Report the other states as they happen.

Continue the assigned work after reporting unless you are blocked or waiting on
the user.`
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
