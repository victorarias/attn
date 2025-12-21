// internal/daemon/branch.go
package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/claude-manager/internal/git"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

const fetchCacheTTL = 30 * time.Minute

func (d *Daemon) getCachedBranches(repo string) ([]protocol.Branch, time.Time, bool) {
	d.repoCacheMu.RLock()
	defer d.repoCacheMu.RUnlock()

	cache, ok := d.repoCaches[repo]
	if !ok {
		return nil, time.Time{}, false
	}
	if time.Since(cache.fetchedAt) > fetchCacheTTL {
		return nil, time.Time{}, false
	}
	return cache.branches, cache.fetchedAt, true
}

func (d *Daemon) setCachedBranches(repo string, branches []protocol.Branch) {
	d.repoCacheMu.Lock()
	defer d.repoCacheMu.Unlock()

	d.repoCaches[repo] = &repoCache{
		fetchedAt: time.Now(),
		branches:  branches,
	}
}

func (d *Daemon) invalidateBranchCache(repo string) {
	d.repoCacheMu.Lock()
	defer d.repoCacheMu.Unlock()
	delete(d.repoCaches, repo)
}

// doListBranches fetches local branches not checked out in any worktree
func (d *Daemon) doListBranches(mainRepo string) ([]protocol.Branch, error) {
	branches, err := git.ListBranches(mainRepo)
	if err != nil {
		return nil, err
	}

	result := make([]protocol.Branch, len(branches))
	for i, b := range branches {
		result[i] = protocol.Branch{Name: b}
	}
	return result, nil
}

// doDeleteBranch deletes a local branch
func (d *Daemon) doDeleteBranch(mainRepo, branch string, force bool) error {
	return git.DeleteBranch(mainRepo, branch, force)
}

// doSwitchBranch switches the main repo to a different branch
func (d *Daemon) doSwitchBranch(mainRepo, branch string) error {
	return git.SwitchBranch(mainRepo, branch)
}

// doCreateBranch creates a new branch (without checking it out)
func (d *Daemon) doCreateBranch(mainRepo, branch string) error {
	return git.CreateBranch(mainRepo, branch)
}

// doCreateWorktreeFromBranch creates a worktree from an existing branch
func (d *Daemon) doCreateWorktreeFromBranch(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	mainRepo := git.ExpandPath(msg.MainRepo)
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
		path = git.GenerateWorktreePath(msg.MainRepo, localBranch)
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

func (d *Daemon) handleListBranchesWS(client *wsClient, msg *protocol.ListBranchesMessage) {
	go func() {
		branches, err := d.doListBranches(msg.MainRepo)
		if err != nil {
			d.sendToClient(client, &protocol.WebSocketEvent{
				Event:   protocol.EventBranchesResult,
				Success: protocol.Ptr(false),
				Error:   protocol.Ptr(err.Error()),
			})
			return
		}
		d.sendToClient(client, &protocol.WebSocketEvent{
			Event:    protocol.EventBranchesResult,
			Branches: branches,
			Success:  protocol.Ptr(true),
		})
	}()
}

func (d *Daemon) handleDeleteBranchWS(client *wsClient, msg *protocol.DeleteBranchMessage) {
	go func() {
		err := d.doDeleteBranch(msg.MainRepo, msg.Branch, msg.Force)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventDeleteBranchResult,
			Branch:  protocol.Ptr(msg.Branch),
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Delete branch failed for %s: %v", msg.Branch, err)
		} else {
			d.invalidateBranchCache(msg.MainRepo)
			d.logf("Delete branch succeeded: %s", msg.Branch)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleSwitchBranchWS(client *wsClient, msg *protocol.SwitchBranchMessage) {
	go func() {
		err := d.doSwitchBranch(msg.MainRepo, msg.Branch)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventSwitchBranchResult,
			Branch:  protocol.Ptr(msg.Branch),
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Switch branch failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Switch branch succeeded: %s", msg.Branch)
		}
		d.sendToClient(client, result)
	}()
}

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
			// Invalidate branch cache since a branch is now checked out in worktree
			d.invalidateBranchCache(msg.MainRepo)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleCreateBranchWS(client *wsClient, msg *protocol.CreateBranchMessage) {
	go func() {
		err := d.doCreateBranch(msg.MainRepo, msg.Branch)
		result := protocol.CreateBranchResultMessage{
			Event:   protocol.EventCreateBranchResult,
			Branch:  msg.Branch,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create branch failed for %s: %v", msg.Branch, err)
		} else {
			d.invalidateBranchCache(msg.MainRepo)
			d.logf("Create branch succeeded: %s", msg.Branch)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	go func() {
		repo := git.ExpandPath(msg.Repo)

		// Get current branch and commit
		currentBranch, err := git.GetCurrentBranch(repo)
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:   protocol.EventGetRepoInfoResult,
				Success: false,
				Error:   protocol.Ptr(err.Error()),
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

		// Check cache first
		var branches []protocol.Branch
		var fetchedAt *string
		cachedBranches, cachedTime, cacheHit := d.getCachedBranches(repo)

		if cacheHit {
			// Use cached branches
			branches = cachedBranches
			timeStr := cachedTime.Format(time.RFC3339)
			fetchedAt = &timeStr
		} else {
			// Cache miss - fetch branches with commit info
			branchesWithCommits, err := git.ListBranchesWithCommits(repo)
			if err != nil {
				d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
					Event:   protocol.EventGetRepoInfoResult,
					Success: false,
					Error:   protocol.Ptr(err.Error()),
				})
				return
			}

			branches = make([]protocol.Branch, len(branchesWithCommits))
			for i, b := range branchesWithCommits {
				branches[i] = b.ToProtocol()
			}

			// Cache the result
			d.setCachedBranches(repo, branches)
		}

		d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
			Event: protocol.EventGetRepoInfoResult,
			Info: &protocol.RepoInfo{
				Repo:              repo,
				CurrentBranch:     currentBranch,
				CurrentCommitHash: commitHash,
				CurrentCommitTime: commitTime,
				DefaultBranch:     defaultBranch,
				Worktrees:         worktrees,
				Branches:          branches,
				FetchedAt:         fetchedAt,
			},
			Success: true,
		})
	}()
}
