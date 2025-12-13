// internal/daemon/branch.go
package daemon

import (
	"time"

	"github.com/victorarias/claude-manager/internal/git"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

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
	path := protocol.Deref(msg.Path)
	if path == "" {
		path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
	}

	if err := git.CreateWorktreeFromBranch(msg.MainRepo, msg.Branch, path); err != nil {
		return "", err
	}

	wt := &store.Worktree{
		Path:      path,
		Branch:    msg.Branch,
		MainRepo:  msg.MainRepo,
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
			d.logf("Create branch succeeded: %s", msg.Branch)
		}
		d.sendToClient(client, result)
	}()
}
