package daemon

import (
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

// Workspace registry is intentionally in-memory only for the native-UI spike:
// the canvas client re-registers its workspaces on every reconnect, so there's
// no value in persisting the layout-side association across daemon restarts.

type workspaceEntry struct {
	id        string
	title     string
	directory string
	status    protocol.WorkspaceStatus
	// sessionIDs in this workspace, used for status rollup.
	sessionIDs map[string]struct{}
}

type workspaceRegistry struct {
	mu sync.RWMutex
	// workspaces keyed by id.
	workspaces map[string]*workspaceEntry
	// sessionToWorkspace lets us find the owning workspace from a session id.
	sessionToWorkspace map[string]string
}

func newWorkspaceRegistry() *workspaceRegistry {
	return &workspaceRegistry{
		workspaces:         make(map[string]*workspaceEntry),
		sessionToWorkspace: make(map[string]string),
	}
}

// register inserts or updates a workspace. Returns the snapshot to broadcast
// and a flag indicating whether this is a new registration vs an update.
func (r *workspaceRegistry) register(id, title, directory string) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, existed := r.workspaces[id]
	if !existed {
		entry = &workspaceEntry{
			id:         id,
			status:     protocol.WorkspaceStatusUnknown,
			sessionIDs: make(map[string]struct{}),
		}
		r.workspaces[id] = entry
	}
	entry.title = title
	entry.directory = directory
	return snapshotEntry(entry), !existed
}

func (r *workspaceRegistry) unregister(id string) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	delete(r.workspaces, id)
	for sessionID := range entry.sessionIDs {
		if r.sessionToWorkspace[sessionID] == id {
			delete(r.sessionToWorkspace, sessionID)
		}
	}
	return snapshotEntry(entry), true
}

// associateSession binds a session to a workspace. No-op if the workspace is
// not registered (e.g., session spawned with a stale workspace_id after the
// daemon dropped its in-memory state).
func (r *workspaceRegistry) associateSession(sessionID, workspaceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.workspaces[workspaceID]
	if !ok {
		return false
	}
	if existing, had := r.sessionToWorkspace[sessionID]; had && existing != workspaceID {
		if prev, ok := r.workspaces[existing]; ok {
			delete(prev.sessionIDs, sessionID)
		}
	}
	entry.sessionIDs[sessionID] = struct{}{}
	r.sessionToWorkspace[sessionID] = workspaceID
	return true
}

func (r *workspaceRegistry) dissociateSession(sessionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	workspaceID, ok := r.sessionToWorkspace[sessionID]
	if !ok {
		return ""
	}
	delete(r.sessionToWorkspace, sessionID)
	if entry, ok := r.workspaces[workspaceID]; ok {
		delete(entry.sessionIDs, sessionID)
	}
	return workspaceID
}

func (r *workspaceRegistry) workspaceIDForSession(sessionID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionToWorkspace[sessionID]
}

func (r *workspaceRegistry) sessionIDs(workspaceID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.workspaces[workspaceID]
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(entry.sessionIDs))
	for id := range entry.sessionIDs {
		ids = append(ids, id)
	}
	return ids
}

func (r *workspaceRegistry) snapshot(id string) (protocol.Workspace, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	return snapshotEntry(entry), true
}

func (r *workspaceRegistry) list() []protocol.Workspace {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]protocol.Workspace, 0, len(r.workspaces))
	for _, entry := range r.workspaces {
		out = append(out, snapshotEntry(entry))
	}
	return out
}

// applyStatus updates the cached status for a workspace and returns whether
// it changed (i.e., whether a state-changed broadcast is needed).
func (r *workspaceRegistry) applyStatus(id string, status protocol.WorkspaceStatus) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	if entry.status == status {
		return protocol.Workspace{}, false
	}
	entry.status = status
	return snapshotEntry(entry), true
}

func snapshotEntry(e *workspaceEntry) protocol.Workspace {
	return protocol.Workspace{
		ID:        e.id,
		Title:     e.title,
		Directory: e.directory,
		Status:    e.status,
	}
}

// rollupWorkspaceStatus returns the workspace status that summarizes the
// supplied session states. Higher-priority states win:
// launching > working > waiting_input > pending_approval > idle > unknown.
// An empty slice yields "unknown".
func rollupWorkspaceStatus(sessionStates []protocol.SessionState) protocol.WorkspaceStatus {
	if len(sessionStates) == 0 {
		return protocol.WorkspaceStatusUnknown
	}
	priority := map[protocol.SessionState]int{
		protocol.SessionStateLaunching:       6,
		protocol.SessionStateWorking:         5,
		protocol.SessionStateWaitingInput:    4,
		protocol.SessionStatePendingApproval: 3,
		protocol.SessionStateIdle:            2,
		protocol.SessionStateUnknown:    1,
	}
	statusFor := map[protocol.SessionState]protocol.WorkspaceStatus{
		protocol.SessionStateLaunching:       protocol.WorkspaceStatusLaunching,
		protocol.SessionStateWorking:         protocol.WorkspaceStatusWorking,
		protocol.SessionStateWaitingInput:    protocol.WorkspaceStatusWaitingInput,
		protocol.SessionStatePendingApproval: protocol.WorkspaceStatusPendingApproval,
		protocol.SessionStateIdle:            protocol.WorkspaceStatusIdle,
		protocol.SessionStateUnknown:    protocol.WorkspaceStatusUnknown,
	}
	bestPriority := 0
	best := protocol.WorkspaceStatusUnknown
	for _, s := range sessionStates {
		p, ok := priority[s]
		if !ok {
			p = priority[protocol.SessionStateUnknown]
		}
		if p > bestPriority {
			bestPriority = p
			if status, ok := statusFor[s]; ok {
				best = status
			} else {
				best = protocol.WorkspaceStatusUnknown
			}
		}
	}
	return best
}

// recomputeWorkspaceStatus reads current session states from the store,
// rolls them up, and updates the cached workspace status. Returns the
// updated workspace and whether the status changed.
func (d *Daemon) recomputeWorkspaceStatus(workspaceID string) (protocol.Workspace, bool) {
	if d.workspaces == nil || workspaceID == "" {
		return protocol.Workspace{}, false
	}
	sessionIDs := d.workspaces.sessionIDs(workspaceID)
	states := make([]protocol.SessionState, 0, len(sessionIDs))
	for _, sid := range sessionIDs {
		if sess := d.store.Get(sid); sess != nil {
			states = append(states, sess.State)
		}
	}
	status := rollupWorkspaceStatus(states)
	return d.workspaces.applyStatus(workspaceID, status)
}

// recomputeAndBroadcastWorkspaceForSession is a convenience used after a
// session state change: looks up the owning workspace, recomputes its status,
// and broadcasts WorkspaceStateChanged if the rolled-up status changed.
func (d *Daemon) recomputeAndBroadcastWorkspaceForSession(sessionID string) {
	if d.workspaces == nil {
		return
	}
	workspaceID := d.workspaces.workspaceIDForSession(sessionID)
	if workspaceID == "" {
		return
	}
	updated, changed := d.recomputeWorkspaceStatus(workspaceID)
	if !changed {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceStateChanged,
		Workspace: &updated,
	})
}

func (d *Daemon) handleRegisterWorkspace(client *wsClient, msg *protocol.RegisterWorkspaceMessage) {
	id := strings.TrimSpace(msg.ID)
	title := strings.TrimSpace(msg.Title)
	directory := strings.TrimSpace(msg.Directory)
	if id == "" {
		d.sendCommandError(client, protocol.CmdRegisterWorkspace, "missing id")
		return
	}
	if directory == "" {
		d.sendCommandError(client, protocol.CmdRegisterWorkspace, "missing directory")
		return
	}
	if d.workspaces == nil {
		d.workspaces = newWorkspaceRegistry()
	}
	snapshot, isNew := d.workspaces.register(id, title, directory)
	if !isNew {
		// Re-register: pick up any new associations that occurred while it
		// was registered, then publish a state-changed event so clients
		// see the refreshed title/directory.
		if updated, changed := d.recomputeWorkspaceStatus(id); changed {
			snapshot = updated
		}
	}
	eventName := protocol.EventWorkspaceRegistered
	if !isNew {
		eventName = protocol.EventWorkspaceStateChanged
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     eventName,
		Workspace: &snapshot,
	})
}

func (d *Daemon) handleUnregisterWorkspace(client *wsClient, msg *protocol.UnregisterWorkspaceMessage) {
	id := strings.TrimSpace(msg.ID)
	if id == "" {
		d.sendCommandError(client, protocol.CmdUnregisterWorkspace, "missing id")
		return
	}
	if d.workspaces == nil {
		return
	}
	snapshot, removed := d.workspaces.unregister(id)
	if !removed {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceUnregistered,
		Workspace: &snapshot,
	})
}

// listWorkspaces returns a snapshot of the current workspaces for InitialState.
func (d *Daemon) listWorkspaces() []protocol.Workspace {
	if d.workspaces == nil {
		return nil
	}
	return d.workspaces.list()
}

// associateSessionWithWorkspace binds a freshly spawned session to a workspace
// and seeds the rollup status. Called from handleSpawnSession when the spawn
// message carries workspace_id.
func (d *Daemon) associateSessionWithWorkspace(sessionID, workspaceID string) {
	if workspaceID == "" || d.workspaces == nil {
		return
	}
	if !d.workspaces.associateSession(sessionID, workspaceID) {
		// Stale workspace_id from a client that outlived a daemon restart.
		// Drop silently — broadcast nothing.
		return
	}
	if updated, changed := d.recomputeWorkspaceStatus(workspaceID); changed {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:     protocol.EventWorkspaceStateChanged,
			Workspace: &updated,
		})
	}
}

// dissociateSessionFromWorkspace is called when a session is unregistered, so
// the rolled-up workspace status no longer counts the gone session.
func (d *Daemon) dissociateSessionFromWorkspace(sessionID string) {
	if d.workspaces == nil {
		return
	}
	workspaceID := d.workspaces.dissociateSession(sessionID)
	if workspaceID == "" {
		return
	}
	if updated, changed := d.recomputeWorkspaceStatus(workspaceID); changed {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:     protocol.EventWorkspaceStateChanged,
			Workspace: &updated,
		})
	}
}

// decorateSessionWithWorkspace fills in WorkspaceID on a session about to be
// broadcast, if an in-memory association exists. Called from sessionForBroadcast.
func (d *Daemon) decorateSessionWithWorkspace(session *protocol.Session) {
	if session == nil || d.workspaces == nil {
		return
	}
	if id := d.workspaces.workspaceIDForSession(session.ID); id != "" {
		session.WorkspaceID = protocol.Ptr(id)
	} else {
		session.WorkspaceID = nil
	}
}

