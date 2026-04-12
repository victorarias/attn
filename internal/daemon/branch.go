// internal/daemon/branch.go
package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// doCreateWorktreeFromBranch creates a worktree from an existing branch
func (d *Daemon) doCreateWorktreeFromBranch(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	mainRepo := git.ResolveMainRepoPath(msg.MainRepo)
	branch := msg.Branch

	// For remote branches (origin/xxx), extract local name for path and tracking
	localBranch := branch
	isRemote := strings.HasPrefix(branch, "origin/")
	if isRemote {
		localBranch = strings.TrimPrefix(branch, "origin/")
	}

	path := protocol.Deref(msg.Path)
	if path == "" {
		// Use local branch name for cleaner worktree path
		path = git.GenerateWorktreePath(mainRepo, localBranch)
	}
	path = git.ExpandPath(path)

	if isRemote {
		// Create worktree with local branch tracking remote
		createdBranch, err := git.CreateWorktreeFromRemoteBranch(mainRepo, branch, path)
		if err != nil {
			return "", err
		}
		localBranch = createdBranch
	} else {
		if err := git.CreateWorktreeFromBranch(mainRepo, branch, path); err != nil {
			return "", err
		}
	}

	wt := &store.Worktree{
		Path:      path,
		Branch:    localBranch, // Store local branch name, not origin/xxx
		MainRepo:  mainRepo,
		CreatedAt: time.Now(),
	}
	d.store.AddWorktree(wt)

	// Broadcast created event to all clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeCreated,
		Worktrees: []protocol.Worktree{{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
		}},
	})

	return path, nil
}

// WebSocket handlers

func (d *Daemon) handleCreateWorktreeFromBranchWS(client *wsClient, msg *protocol.CreateWorktreeFromBranchMessage) {
	go func() {
		path, err := d.doCreateWorktreeFromBranch(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:   protocol.EventCreateWorktreeResult,
			Path:    protocol.Ptr(path),
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create worktree from branch failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree from branch succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleListBranchesWS(client *wsClient, msg *protocol.ListBranchesMessage) {
	go func() {
		branches, err := git.ListBranchesWithCommits(msg.MainRepo)
		result := protocol.BranchesResultMessage{
			Event:   protocol.EventBranchesResult,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Branches = make([]protocol.Branch, len(branches))
			for i, b := range branches {
				result.Branches[i] = b.ToProtocol()
			}
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	go func() {
		repo := git.CanonicalizePath(msg.Repo)

		// Get current branch and commit
		currentBranch, err := git.GetCurrentBranch(repo)
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:      protocol.EventGetRepoInfoResult,
				EndpointID: msg.EndpointID,
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		// Get current commit hash and time
		commitHash, commitTime := git.GetHeadCommitInfo(repo)

		// Get default branch
		defaultBranch, _ := git.GetDefaultBranch(repo)
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		// Get worktrees
		worktrees := d.doListWorktrees(repo)

		d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
			Event:      protocol.EventGetRepoInfoResult,
			EndpointID: msg.EndpointID,
			Info: &protocol.RepoInfo{
				Repo:              repo,
				CurrentBranch:     currentBranch,
				CurrentCommitHash: commitHash,
				CurrentCommitTime: commitTime,
				DefaultBranch:     defaultBranch,
				Worktrees:         worktrees,
			},
			Success: true,
		})
	}()
}

func (d *Daemon) handleGetDefaultBranchWS(client *wsClient, msg *protocol.GetDefaultBranchMessage) {
	go func() {
		branch, err := git.GetDefaultBranch(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventGetDefaultBranchResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Branch = protocol.Ptr(branch)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleFetchRemotesWS(client *wsClient, msg *protocol.FetchRemotesMessage) {
	go func() {
		err := git.FetchRemotes(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventFetchRemotesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("FetchRemotes failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("FetchRemotes succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleListRemoteBranchesWS(client *wsClient, msg *protocol.ListRemoteBranchesMessage) {
	go func() {
		branches, err := git.ListRemoteBranches(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventListRemoteBranchesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			branchList := make([]protocol.Branch, len(branches))
			for i, b := range branches {
				branchList[i] = protocol.Branch{Name: b}
			}
			result.Branches = branchList
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleEnsureRepoWS(client *wsClient, msg *protocol.EnsureRepoMessage) {
	go func() {
		result := &protocol.WebSocketEvent{
			Event:      protocol.EventEnsureRepoResult,
			TargetPath: protocol.Ptr(msg.TargetPath),
		}

		// Ensure the repo exists (clone if needed)
		cloned, err := git.EnsureRepo(msg.CloneURL, msg.TargetPath)
		if err != nil {
			result.Success = protocol.Ptr(false)
			result.Error = protocol.Ptr(err.Error())
			result.Cloned = protocol.Ptr(false)
			d.logf("EnsureRepo failed for %s: %v", msg.TargetPath, err)
			d.sendToClient(client, result)
			return
		}

		// Fetch remotes (whether repo was cloned or already existed)
		if err := git.FetchRemotes(msg.TargetPath); err != nil {
			result.Success = protocol.Ptr(false)
			result.Error = protocol.Ptr("repo exists but fetch failed: " + err.Error())
			result.Cloned = protocol.Ptr(cloned)
			d.logf("FetchRemotes failed for %s after ensure: %v", msg.TargetPath, err)
			d.sendToClient(client, result)
			return
		}

		result.Success = protocol.Ptr(true)
		result.Cloned = protocol.Ptr(cloned)
		if cloned {
			d.logf("EnsureRepo cloned %s to %s", msg.CloneURL, msg.TargetPath)
		} else {
			d.logf("EnsureRepo found existing repo at %s, fetched remotes", msg.TargetPath)
		}
		d.sendToClient(client, result)
	}()
}
