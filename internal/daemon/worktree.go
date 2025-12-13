// internal/daemon/worktree.go
package daemon

import (
	"net"
	"time"

	"github.com/victorarias/claude-manager/internal/git"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

// Core worktree operations - shared between Unix socket and WebSocket handlers

// doListWorktrees fetches worktrees from store and git, merges them, returns protocol type
func (d *Daemon) doListWorktrees(mainRepo string) []*protocol.Worktree {
	// Get from registry first
	worktrees := d.store.ListWorktreesByRepo(mainRepo)

	// Also scan git for any we don't have
	gitWorktrees, err := git.ListWorktrees(mainRepo)
	if err == nil {
		for _, gwt := range gitWorktrees {
			// Skip main repo
			if gwt.Path == mainRepo {
				continue
			}
			// Add if not in registry
			found := false
			for _, wt := range worktrees {
				if wt.Path == gwt.Path {
					found = true
					break
				}
			}
			if !found {
				newWt := &store.Worktree{
					Path:      gwt.Path,
					Branch:    gwt.Branch,
					MainRepo:  mainRepo,
					CreatedAt: time.Now(),
				}
				d.store.AddWorktree(newWt)
				worktrees = append(worktrees, newWt)
			}
		}
	}

	// Convert to protocol type
	protoWorktrees := make([]*protocol.Worktree, len(worktrees))
	for i, wt := range worktrees {
		protoWorktrees[i] = &protocol.Worktree{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: wt.CreatedAt.Format(time.RFC3339),
		}
	}
	return protoWorktrees
}

// doCreateWorktree creates a git worktree and registers it in the store.
// Returns the created worktree path and any error.
func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	path := msg.Path
	if path == "" {
		path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
	}

	if err := git.CreateWorktree(msg.MainRepo, msg.Branch, path); err != nil {
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
		Worktrees: []*protocol.Worktree{{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: wt.CreatedAt.Format(time.RFC3339),
		}},
	})

	return path, nil
}

// doDeleteWorktree removes a worktree from git and the store.
// Also cleans up any sessions in that directory.
func (d *Daemon) doDeleteWorktree(path string) error {
	// Remove any sessions in this directory (they're stale if we're deleting the worktree)
	d.store.RemoveSessionsInDirectory(path)

	wt := d.store.GetWorktree(path)
	if wt == nil {
		return &worktreeNotFoundError{path: path}
	}

	if err := git.DeleteWorktree(wt.MainRepo, path); err != nil {
		return err
	}

	d.store.RemoveWorktree(path)

	// Broadcast deleted event to all clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeDeleted,
		Worktrees: []*protocol.Worktree{{
			Path: path,
		}},
	})

	return nil
}

type worktreeNotFoundError struct {
	path string
}

func (e *worktreeNotFoundError) Error() string {
	return "worktree not found in registry: " + e.path
}

// Unix socket handlers

func (d *Daemon) handleListWorktrees(conn net.Conn, msg *protocol.ListWorktreesMessage) {
	protoWorktrees := d.doListWorktrees(msg.MainRepo)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
	d.sendOK(conn)
}

func (d *Daemon) handleCreateWorktree(conn net.Conn, msg *protocol.CreateWorktreeMessage) {
	_, err := d.doCreateWorktree(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handleDeleteWorktree(conn net.Conn, msg *protocol.DeleteWorktreeMessage) {
	if err := d.doDeleteWorktree(msg.Path); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

// WebSocket handlers for async result pattern

func (d *Daemon) handleListWorktreesWS(client *wsClient, msg *protocol.ListWorktreesMessage) {
	protoWorktrees := d.doListWorktrees(msg.MainRepo)
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
}

func (d *Daemon) handleCreateWorktreeWS(client *wsClient, msg *protocol.CreateWorktreeMessage) {
	go func() {
		path, err := d.doCreateWorktree(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:   protocol.EventCreateWorktreeResult,
			Path:    path,
			Success: err == nil,
		}
		if err != nil {
			result.Error = err.Error()
			d.logf("Create worktree failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleDeleteWorktreeWS(client *wsClient, msg *protocol.DeleteWorktreeMessage) {
	go func() {
		// Broadcast updated sessions list (doDeleteWorktree removes sessions internally)
		defer func() {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:    protocol.EventSessionsUpdated,
				Sessions: d.store.List(""),
			})
		}()

		err := d.doDeleteWorktree(msg.Path)
		result := protocol.DeleteWorktreeResultMessage{
			Event:   protocol.EventDeleteWorktreeResult,
			Path:    msg.Path,
			Success: err == nil,
		}
		if err != nil {
			result.Error = err.Error()
			d.logf("Delete worktree failed for %s: %v", msg.Path, err)
		} else {
			d.logf("Delete worktree succeeded: %s", msg.Path)
		}
		d.sendToClient(client, result)
	}()
}
