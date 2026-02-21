// internal/daemon/worktree.go
package daemon

import (
	"net"
	"os"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Core worktree operations - shared between Unix socket and WebSocket handlers

// doListWorktrees fetches worktrees from store and git, merges them, returns protocol type.
// It also prunes stale worktrees that no longer exist in git.
func (d *Daemon) doListWorktrees(mainRepo string) []protocol.Worktree {
	// Get from registry first
	storedWorktrees := d.store.ListWorktreesByRepo(mainRepo)

	// Get current git worktrees
	gitWorktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		// If we can't read git, just return stored worktrees
		protoWorktrees := make([]protocol.Worktree, len(storedWorktrees))
		for i, wt := range storedWorktrees {
			protoWorktrees[i] = protocol.Worktree{
				Path:      wt.Path,
				Branch:    wt.Branch,
				MainRepo:  wt.MainRepo,
				CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
			}
		}
		return protoWorktrees
	}

	// Build set of valid git worktree paths
	gitWorktreePaths := make(map[string]bool)
	for _, gwt := range gitWorktrees {
		gitWorktreePaths[gwt.Path] = true
	}

	// Prune stale worktrees from store and build valid list
	var validWorktrees []*store.Worktree
	for _, wt := range storedWorktrees {
		if gitWorktreePaths[wt.Path] {
			validWorktrees = append(validWorktrees, wt)
		} else {
			// Worktree no longer exists in git - remove from store
			d.store.RemoveWorktree(wt.Path)
		}
	}

	// Add any git worktrees not in registry
	for _, gwt := range gitWorktrees {
		// Skip main repo
		if gwt.Path == mainRepo {
			continue
		}
		// Add if not in registry
		found := false
		for _, wt := range validWorktrees {
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
			validWorktrees = append(validWorktrees, newWt)
		}
	}

	// Convert to protocol type
	protoWorktrees := make([]protocol.Worktree, len(validWorktrees))
	for i, wt := range validWorktrees {
		protoWorktrees[i] = protocol.Worktree{
			Path:      wt.Path,
			Branch:    wt.Branch,
			MainRepo:  wt.MainRepo,
			CreatedAt: protocol.Ptr(wt.CreatedAt.Format(time.RFC3339)),
		}
	}
	return protoWorktrees
}

// doCreateWorktree creates a git worktree and registers it in the store.
// Returns the created worktree path and any error.
func (d *Daemon) doCreateWorktree(msg *protocol.CreateWorktreeMessage) (string, error) {
	mainRepo := git.ResolveMainRepoPath(msg.MainRepo)

	path := protocol.Deref(msg.Path)
	if path == "" {
		path = git.GenerateWorktreePath(mainRepo, msg.Branch)
	}

	startingFrom := protocol.Deref(msg.StartingFrom)
	var err error
	if startingFrom != "" {
		err = git.CreateWorktreeFromPoint(mainRepo, msg.Branch, path, startingFrom)
	} else {
		err = git.CreateWorktree(mainRepo, msg.Branch, path)
	}
	if err != nil {
		return "", err
	}

	wt := &store.Worktree{
		Path:      path,
		Branch:    msg.Branch,
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

// discoverWorktree tries to find a worktree from git state when it's not in the registry.
// This handles cases where the DB was reset or the worktree was created manually.
// Returns nil if the path is not a valid worktree or cannot be discovered.
func (d *Daemon) discoverWorktree(path string) *store.Worktree {
	// Get the main repo from the worktree's .git file
	mainRepo := git.GetMainRepoFromWorktree(path)
	if mainRepo == "" {
		return nil
	}

	// List all worktrees from git to find matching entry
	gitWorktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		return nil
	}

	// Find the matching worktree
	for _, gwt := range gitWorktrees {
		if gwt.Path == path {
			// Found it - register in store and return
			wt := &store.Worktree{
				Path:      gwt.Path,
				Branch:    gwt.Branch,
				MainRepo:  mainRepo,
				CreatedAt: time.Now(),
			}
			d.store.AddWorktree(wt)
			d.logf("Discovered worktree not in registry: %s (branch: %s, main: %s)", path, gwt.Branch, mainRepo)
			return wt
		}
	}

	return nil
}

// doDeleteWorktree removes a worktree from git and the store.
// Also cleans up any sessions in that directory.
func (d *Daemon) doDeleteWorktree(path string) error {
	// Stop/remove sessions in this directory before deleting the worktree.
	for _, session := range d.store.List("") {
		if session.Directory != path {
			continue
		}
		d.terminateSession(session.ID, syscall.SIGTERM)
		d.store.Remove(session.ID)
		d.clearLongRunTracking(session.ID)
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionUnregistered,
			Session: d.sessionForBroadcast(session),
		})
	}

	wt := d.store.GetWorktree(path)
	if wt == nil {
		// Try to discover it from git state
		wt = d.discoverWorktree(path)
		if wt == nil {
			// Check if the path exists on disk
			if _, err := os.Stat(path); os.IsNotExist(err) {
				// Path doesn't exist and not in registry - nothing to delete
				// Broadcast deleted event anyway so UI removes it
				d.logf("Worktree %s doesn't exist and not in registry, treating as already deleted", path)
				d.wsHub.Broadcast(&protocol.WebSocketEvent{
					Event: protocol.EventWorktreeDeleted,
					Worktrees: []protocol.Worktree{{
						Path: path,
					}},
				})
				return nil
			}
			return &worktreeNotFoundError{path: path}
		}
	}

	// Save branch name before deleting worktree
	branch := wt.Branch
	mainRepo := wt.MainRepo

	if err := git.DeleteWorktree(mainRepo, path); err != nil {
		return err
	}

	d.store.RemoveWorktree(path)

	// Also delete the branch (force=true since worktree is already deleted)
	if branch != "" {
		if err := git.DeleteBranch(mainRepo, branch, true); err != nil {
			d.logf("Warning: worktree deleted but failed to delete branch %s: %v", branch, err)
			// Don't fail the whole operation - worktree is already deleted
		} else {
			d.logf("Deleted branch %s along with worktree", branch)
		}
	}

	// Broadcast deleted event to all clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventWorktreeDeleted,
		Worktrees: []protocol.Worktree{{
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
			Path:    protocol.Ptr(path),
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
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
				Sessions: d.sessionsForBroadcast(d.store.List("")),
			})
		}()

		err := d.doDeleteWorktree(msg.Path)
		result := protocol.DeleteWorktreeResultMessage{
			Event:   protocol.EventDeleteWorktreeResult,
			Path:    msg.Path,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Delete worktree failed for %s: %v", msg.Path, err)
		} else {
			d.logf("Delete worktree succeeded: %s", msg.Path)
		}
		d.sendToClient(client, result)
	}()
}
