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
	"github.com/victorarias/attn/internal/store"
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

// delegationRollback is the compensation stack for delegation's saga, shared with
// ticket resume, which reassembles the same resources. Delegation acquires several
// in sequence — a worktree, a workspace, a layout pane, a live session — and any
// step after the first can fail. Each acquisition pushes its own undo here; a
// later failure unwinds everything pushed so far, newest first.
//
// The point is that a failure site no longer decides WHICH compensations apply.
// Hand-listing them at each `return` is what leaks a workspace, a pane, or a
// worktree the moment a new failure point is added between two existing ones,
// because the correct set is only visible by reading every site above it.
//
// Unwind order is acquisition order reversed, which is also the only safe order:
// the session must stop before its pane is removed, the pane must go before its
// workspace is unregistered, and the workspace must go before the worktree its
// directory points at is deleted.
type delegationRollback struct {
	d    *Daemon
	undo []func() error
}

func (d *Daemon) newDelegationRollback() *delegationRollback {
	return &delegationRollback{d: d}
}

// fail unwinds every compensation pushed so far and returns cause, annotated with
// any compensation that itself failed. The stack is emptied, so a caller that
// keeps using the same rollback after a handled failure cannot double-undo.
func (r *delegationRollback) fail(cause error) error {
	for i := len(r.undo) - 1; i >= 0; i-- {
		if err := r.undo[i](); err != nil {
			cause = fmt.Errorf("%w; %v", cause, err)
		}
	}
	r.undo = nil
	return cause
}

// abandon drops the pending compensations without running them, for the case where
// undoing is no longer safe. Only correct when EVERY pending compensation is one
// this operation must not perform; it is not a general "skip cleanup".
func (r *delegationRollback) abandon() {
	r.undo = nil
}

// onWorktreeCreated registers deletion of a worktree THIS operation created. A
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

// delegationDefaultStartRef names the ref an automatically created delegated
// worktree starts from. It returns "" when the repository's default branch
// cannot be resolved, so the caller can ask for an explicit --from value.
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

// applyDefaultDelegationWorktree resolves an empty worktree request into the
// automatic Git-repository default. A nil request is the caller's explicit
// opt-out, while a named branch preserves the existing explicit behavior.
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
			// Bare flags promise a worktree; a non-repository source silently
			// degraded that into no checkout at all, and the delegate woke in a
			// directory where its brief's paths do not exist. Every other
			// placement flag is the caller's explicit consent to that target, so
			// only the default refuses. A confusing error beats a silent
			// misplacement.
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

// createDelegationWorktree creates the worktree. inferredRepo, when non-empty,
// names the main repository already resolved by the caller; baseDirectory, when
// non-empty, is a working directory the repository may be inferred from. The
// starting ref is always explicit: --from when supplied, otherwise the
// repository's default branch.
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
	if strings.TrimSpace(protocol.Deref(startingFrom)) == "" {
		ref := delegationDefaultStartRef(repo)
		if ref == "" {
			return "", false, fmt.Errorf("cannot determine the repository's default branch; pass --from or --no-worktree")
		}
		startingFrom = protocol.Ptr(ref)
	}
	worktreePath, err := d.doCreateWorktree(&protocol.CreateWorktreeMessage{
		Cmd:          protocol.CmdCreateWorktree,
		MainRepo:     repo,
		Branch:       branch,
		Path:         request.Path,
		StartingFrom: startingFrom,
	})
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
	// Handed to the caller intact; its own rollback owns the worktree from here.
	rollback.abandon()
	return worktreePath, true, nil
}

func (d *Daemon) delegate(msg *protocol.DelegateMessage) (*protocol.DelegateResult, error) {
	return d.delegateOperation(msg, "", "", "", false, "", "")
}

func (d *Daemon) spawnDelegatedRuntime(msg *protocol.DelegateMessage, sessionID, workspaceID, directory, name, agent, model, effort, brief string) error {
	initialPrompt := withLeafIdentity(delegatedTicketPrompt(brief))
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
	// Recorded before the spawn, because the launch primer reads it: a delegate
	// dispatched at a crown must launch already knowing its plot.
	if crown := strings.TrimSpace(protocol.Deref(msg.Plot)); crown != "" {
		if err := d.recordGardenDispatch(sessionID, crown); err != nil {
			return fmt.Errorf("dispatch %s at %s: %w", sessionID, crown, err)
		}
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
	ticketID := strings.TrimSpace(protocol.Deref(msg.TicketID))
	if (brief == "") == (ticketID == "") {
		return nil, fmt.Errorf("exactly one of brief or ticket_id is required")
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
	// The crown is resolved before any worktree or runtime side effect: a
	// delegation aimed at nothing should refuse, not launch unaimed.
	if err := d.validateDispatchCrown(strings.TrimSpace(protocol.Deref(msg.Plot))); err != nil {
		return nil, err
	}
	sessionID := reservedSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	// An adopted ticket is validated before any worktree or runtime side effect.
	// The durable operation reservation prevents another delegation from racing
	// this one; the store repeats the ownership check atomically at bind time so a
	// concurrent ticket-take still cannot be overwritten silently.
	var adoptedTicket *store.Ticket
	if ticketID != "" {
		adoptedTicket, err = d.store.GetTicket(ticketID)
		if err != nil {
			return nil, err
		}
		if adoptedTicket == nil {
			return nil, fmt.Errorf("ticket not found: %s", ticketID)
		}
		if err := store.ValidateTicketDelegationAdoption(adoptedTicket, sessionID, protocol.Deref(msg.Confirm)); err != nil {
			return nil, fmt.Errorf("adopt ticket %s: %w", ticketID, err)
		}
		brief = strings.TrimSpace(adoptedTicket.Description)
	}
	// name is the explicit --name, otherwise an adopted ticket's title, otherwise
	// the finalized directory basename below.
	name := strings.TrimSpace(protocol.Deref(msg.Label))
	if name == "" && adoptedTicket != nil {
		name = truncateDelegationName(adoptedTicket.Title)
	}
	// Every delegation is ticket-tracked; only the chief's own delegations are
	// additionally chief-OWNED. delegatedByChief therefore no longer gates tracking,
	// just the two behaviors that are genuinely about the chief: durable role
	// ownership of the ticket (which also drives the delegated-from-chief sidebar
	// badge) and unmuting a hidden target workspace. The chief-ness of an operation
	// is fixed when it is claimed (initiatingChiefSessionID), so a role transfer
	// mid-launch cannot change it underneath a resumed operation.
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
			if err := d.spawnDelegatedRuntime(msg, sessionID, existing.WorkspaceID, existing.Directory, existing.Label, agent, model, effort, brief); err != nil {
				return nil, fmt.Errorf("recover delegated session runtime: %w", err)
			}
		}
		ticket, ticketErr := d.store.ActiveTicketForSession(sessionID)
		if ticketErr != nil {
			return nil, ticketErr
		}
		// Adoption may have committed immediately before a daemon crash, and the
		// agent may even have moved the ticket to a terminal column before this
		// operation's completed state persisted. Recognize the requested ticket by
		// assignment, not only through ActiveTicketForSession (which excludes
		// terminal tickets), so recovery never reopens finished work.
		if ticketID != "" && adoptedTicket.Assignee == sessionID {
			ticket = adoptedTicket
		}
		if ticket != nil && ticketID != "" && ticket.ID != ticketID {
			return nil, fmt.Errorf("reserved delegated session is already bound to ticket %s", ticket.ID)
		}
		if ticket == nil {
			boundTicketID := ""
			if ticketID != "" {
				boundTicketID, ticketErr = d.adoptDelegatedTicket(sourceSessionID, delegatedByChief, existing, ticketID, agent, protocol.Deref(msg.Confirm))
			} else {
				boundTicketID, ticketErr = d.createDelegatedTicket(sourceSessionID, delegatedByChief, existing, brief, existing.Label, agent)
			}
			if ticketErr != nil {
				return nil, ticketErr
			}
			if operationID != "" {
				worktreePath := ""
				if msg.Worktree != nil {
					worktreePath = existing.Directory
				}
				_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"recovered delegated ticket", existing.WorkspaceID, boundTicketID, worktreePath, nil, nil, time.Now())
			}
			if ticketID != "" {
				d.notifyTicketObservers(ticketID)
			}
			d.publishTicketFact(FactTicketAssigned, boundTicketID)
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

	if err := d.applyDefaultDelegationWorktree(msg, placement, workspaceID, directory, sessionID, name); err != nil {
		return nil, err
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
	// authority (see delegationWorktreeRepo). Existing-workspace placement must
	// infer the repository from its member sessions. Starting-ref selection is
	// independent of placement: an explicit --from wins, otherwise
	// createDelegationWorktree uses the repository's default branch.
	inferredWorktreeRepo := ""
	if msg.Worktree != nil && placement == delegationPlacementExisting {
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
		worktreePath, created, createErr := d.createDelegationWorktree(directory, inferredWorktreeRepo, msg.Worktree, operationID, ownedWorktreePath, worktreeOwned, worktreeToken, protocol.Deref(msg.AllowWorktreeReuse))
		if createErr != nil {
			return nil, createErr
		}
		if created {
			createdWorktreePath = worktreePath
			rollback.onWorktreeCreated(worktreePath)
		}
		validatedDirectory, directoryErr := validateDelegationDirectory(worktreePath)
		if directoryErr != nil {
			return nil, rollback.fail(directoryErr)
		}
		directory = validatedDirectory
		operationWorktreePath = directory
	}
	// Finalize a new workspace's directory before resolving the name so a
	// directory-basename default reflects the real directory.
	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, rollback.fail(directoryErr)
		}
		directory = validatedDirectory
	}
	if worktreeRoot, occupied := d.activeSessionInLinkedWorktree(directory); occupied && !protocol.Deref(msg.AllowWorktreeReuse) {
		// Once another active session occupies the worktree, it is no longer safe
		// to roll the directory back even if this operation originally created it.
		// The worktree is the only thing acquired so far, so abandoning the whole
		// stack is exactly "leave the occupied worktree alone".
		rollback.abandon()
		return nil, fmt.Errorf("an active session already uses worktree %s; pass --allow-worktree-reuse only when sharing it is intentional", worktreeRoot)
	}

	// Default the name to the directory basename when --name was not given, then
	// validate the final name. Only a worktree may exist at this point, so a
	// validation failure rolls it back (no workspace/pane/session yet).
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
	// Covers the reserved pane too: an interrupted operation's pane is this
	// operation's to clean up once it has adopted it, which is what the previous
	// hand-listed compensations did at every failure site below.
	rollback.onPaneCreated(sessionID)

	if err := d.spawnDelegatedRuntime(msg, sessionID, workspaceID, directory, name, agent, model, effort, brief); err != nil {
		return nil, rollback.fail(fmt.Errorf("spawn delegated session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, rollback.fail(fmt.Errorf("delegated session was not persisted"))
	}
	rollback.onSessionSpawned(sessionID)
	// Unmuting the target workspace stays chief-only: an ordinary delegation
	// preserves the workspace's current mute state (references/delegation.md).
	if delegatedByChief {
		if _, errMsg := d.setWorkspaceMuted(workspaceID, false); errMsg != "" {
			return nil, rollback.fail(fmt.Errorf("make delegated workspace visible: %s", errMsg))
		}
	}
	// The ticket is not incidental to a delegation: the delegated agent's own prompt
	// tells it to report through `attn ticket status`, and the ticket is the only
	// channel back to it. A ticket failure therefore still fails the whole delegation
	// atomically rather than leaving a running session nobody can reach.
	boundTicketID := ""
	if ticketID != "" {
		boundTicketID, err = d.adoptDelegatedTicket(sourceSessionID, delegatedByChief, session, ticketID, agent, protocol.Deref(msg.Confirm))
	} else {
		boundTicketID, err = d.createDelegatedTicket(sourceSessionID, delegatedByChief, session, brief, name, agent)
	}
	if err != nil {
		return nil, rollback.fail(fmt.Errorf("bind delegated ticket: %w", err))
	}
	d.logf("delegate: bound ticket %q to session %s", boundTicketID, session.ID)
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"delegated session and ticket bound", workspaceID, boundTicketID, operationWorktreePath, nil, nil, time.Now())
	}
	if ticketID != "" {
		d.notifyTicketObservers(ticketID)
	}
	d.publishTicketFact(FactTicketAssigned, boundTicketID)
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
// leaf identity line. Tracking is universal, so this line carries the whole
// "you are a leaf" signal — a bound ticket is never a promotion to coordinator.
func withLeafIdentity(prompt string) string {
	return leafIdentityPreamble + "\n\n---\n\n" + strings.TrimSpace(prompt)
}

// delegatedTicketPrompt augments every delegated agent's brief with the self-report
// contract: the agent's work is bound to an attn ticket (assignee == session), and
// it moves that ticket across the board by reporting its own work state. The
// delegator, the agent, and the chief of staff all read that board.
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
