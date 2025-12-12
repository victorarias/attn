// internal/daemon/worktree.go
package daemon

import (
	"net"
	"time"

	"github.com/victorarias/claude-manager/internal/git"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

func (d *Daemon) handleListWorktrees(conn net.Conn, msg *protocol.ListWorktreesMessage) {
	// Get from registry first
	worktrees := d.store.ListWorktreesByRepo(msg.MainRepo)

	// Also scan git for any we don't have
	gitWorktrees, err := git.ListWorktrees(msg.MainRepo)
	if err == nil {
		for _, gwt := range gitWorktrees {
			// Skip main repo
			if gwt.Path == msg.MainRepo {
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
					MainRepo:  msg.MainRepo,
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

	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
	d.sendOK(conn)
}

func (d *Daemon) handleCreateWorktree(conn net.Conn, msg *protocol.CreateWorktreeMessage) {
	path := msg.Path
	if path == "" {
		path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
	}

	// Create the worktree
	err := git.CreateWorktree(msg.MainRepo, msg.Branch, path)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	// Register in store
	wt := &store.Worktree{
		Path:      path,
		Branch:    msg.Branch,
		MainRepo:  msg.MainRepo,
		CreatedAt: time.Now(),
	}
	d.store.AddWorktree(wt)

	d.sendOK(conn)

	// Broadcast created event
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeCreated,
		Worktrees: []*protocol.Worktree{{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: wt.CreatedAt.Format(time.RFC3339),
		}},
	})
}

func (d *Daemon) handleDeleteWorktree(conn net.Conn, msg *protocol.DeleteWorktreeMessage) {
	wt := d.store.GetWorktree(msg.Path)
	if wt == nil {
		d.sendError(conn, "worktree not found in registry")
		return
	}

	// Delete the worktree
	err := git.DeleteWorktree(wt.MainRepo, msg.Path)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	// Remove from store
	d.store.RemoveWorktree(msg.Path)

	d.sendOK(conn)

	// Broadcast deleted event
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeDeleted,
		Worktrees: []*protocol.Worktree{{
			Path: msg.Path,
		}},
	})
}

// WebSocket handlers for async result pattern

func (d *Daemon) handleListWorktreesWS(client *wsClient, msg *protocol.ListWorktreesMessage) {
	// Get from registry first
	worktrees := d.store.ListWorktreesByRepo(msg.MainRepo)

	// Also scan git for any we don't have
	gitWorktrees, err := git.ListWorktrees(msg.MainRepo)
	if err == nil {
		for _, gwt := range gitWorktrees {
			// Skip main repo
			if gwt.Path == msg.MainRepo {
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
					MainRepo:  msg.MainRepo,
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

	// Send to requesting client
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventWorktreesUpdated,
		Worktrees: protoWorktrees,
	})
}

func (d *Daemon) handleCreateWorktreeWS(client *wsClient, msg *protocol.CreateWorktreeMessage) {
	go func() {
		path := msg.Path
		if path == "" {
			path = git.GenerateWorktreePath(msg.MainRepo, msg.Branch)
		}

		// Create the worktree
		err := git.CreateWorktree(msg.MainRepo, msg.Branch, path)
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
			// Register in store
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
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleDeleteWorktreeWS(client *wsClient, msg *protocol.DeleteWorktreeMessage) {
	go func() {
		wt := d.store.GetWorktree(msg.Path)
		result := protocol.DeleteWorktreeResultMessage{
			Event: protocol.EventDeleteWorktreeResult,
			Path:  msg.Path,
		}

		if wt == nil {
			result.Success = false
			result.Error = "worktree not found in registry"
			d.sendToClient(client, result)
			return
		}

		// Delete the worktree
		err := git.DeleteWorktree(wt.MainRepo, msg.Path)
		result.Success = err == nil
		if err != nil {
			result.Error = err.Error()
			d.logf("Delete worktree failed for %s: %v", msg.Path, err)
		} else {
			d.logf("Delete worktree succeeded: %s", msg.Path)
			// Remove from store
			d.store.RemoveWorktree(msg.Path)

			// Broadcast deleted event to all clients
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event: protocol.EventWorktreeDeleted,
				Worktrees: []*protocol.Worktree{{
					Path: msg.Path,
				}},
			})
		}
		d.sendToClient(client, result)
	}()
}
