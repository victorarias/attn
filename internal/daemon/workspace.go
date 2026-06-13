package daemon

import (
	"strings"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// The in-memory registry is the runtime cache; the SQLite store is the source
// of truth. Mutations write through to the store; the registry is rebuilt from
// the store at daemon start. Status is NOT persisted — it's recomputed from
// member sessions on every load and on every state change.

type workspaceEntry struct {
	id        string
	title     string
	directory string
	status    protocol.WorkspaceStatus
	muted     bool
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
func (r *workspaceRegistry) register(id, title, directory string, muted bool) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, existed := r.workspaces[id]
	if !existed {
		entry = &workspaceEntry{
			id:         id,
			status:     protocol.WorkspaceStatusIdle,
			sessionIDs: make(map[string]struct{}),
		}
		r.workspaces[id] = entry
	}
	entry.title = title
	entry.directory = directory
	entry.muted = muted
	return snapshotEntry(entry), !existed
}

// rename updates a workspace's cached title. Returns the refreshed snapshot and
// whether the workspace was found. The store is the durable authority; callers
// persist the new title alongside this in-memory update.
func (r *workspaceRegistry) rename(id, title string) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	entry.title = title
	return snapshotEntry(entry), true
}

func (r *workspaceRegistry) toggleMuted(id string) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	entry.muted = !entry.muted
	return snapshotEntry(entry), true
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

// associateSession binds a session to an already registered workspace.
func (r *workspaceRegistry) associateSession(sessionID, workspaceID, title string) bool {
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

func (r *workspaceRegistry) dissociateSession(sessionID string) (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	workspaceID, ok := r.sessionToWorkspace[sessionID]
	if !ok {
		return "", 0
	}
	delete(r.sessionToWorkspace, sessionID)
	remaining := 0
	if entry, ok := r.workspaces[workspaceID]; ok {
		delete(entry.sessionIDs, sessionID)
		remaining = len(entry.sessionIDs)
	}
	return workspaceID, remaining
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
		Muted:     e.muted,
	}
}

// rollupWorkspaceStatus returns the workspace status that summarizes the
// supplied session states. Higher-priority states win:
// working > waiting_input > pending_approval > scheduled > idle > launching.
// `scheduled` sits above `idle` because a parked-on-schedule session will
// auto-resume — more informative than a settled idle peer — but below the
// attention states since it needs no steering. `launching` sits below `idle`
// on purpose — it carries less information than any settled state, so as soon
// as one session reports a real state, that one wins over a peer that's still
// booting. There's no `unknown` workspace status: an empty slice or sessions
// all in `unknown` fall through to `idle`, since a workspace always has a
// directory and a registry entry, so the rollup always has a meaningful answer.
func rollupWorkspaceStatus(sessionStates []protocol.SessionState) protocol.WorkspaceStatus {
	priority := map[protocol.SessionState]int{
		protocol.SessionStateWorking:         6,
		protocol.SessionStateWaitingInput:    5,
		protocol.SessionStatePendingApproval: 4,
		protocol.SessionStateScheduled:       3,
		protocol.SessionStateIdle:            2,
		protocol.SessionStateLaunching:       1,
	}
	statusFor := map[protocol.SessionState]protocol.WorkspaceStatus{
		protocol.SessionStateLaunching:       protocol.WorkspaceStatusLaunching,
		protocol.SessionStateWorking:         protocol.WorkspaceStatusWorking,
		protocol.SessionStateWaitingInput:    protocol.WorkspaceStatusWaitingInput,
		protocol.SessionStatePendingApproval: protocol.WorkspaceStatusPendingApproval,
		protocol.SessionStateScheduled:       protocol.WorkspaceStatusScheduled,
		protocol.SessionStateIdle:            protocol.WorkspaceStatusIdle,
	}
	bestPriority := 0
	best := protocol.WorkspaceStatusIdle
	for _, s := range sessionStates {
		p, ok := priority[s]
		if !ok {
			// Unrecognised state (e.g. SessionStateUnknown) — skip; we
			// fall back to `idle` if nothing scores higher.
			continue
		}
		if p > bestPriority {
			bestPriority = p
			best = statusFor[s]
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
	existing := d.store.GetWorkspace(id)
	muted := existing != nil && existing.Muted
	// Preserve a user-applied rename across re-registration. A reconnect or
	// retry can re-register the same workspace id with the old derived title;
	// the only authoritative way to change a title is the rename_workspace
	// command, so a non-empty stored title always wins here. Mirrors the
	// session/workspace title guards in handleRegister.
	if existing != nil && strings.TrimSpace(existing.Title) != "" {
		title = existing.Title
	}
	snapshot, isNew := d.workspaces.register(id, title, directory, muted)
	d.store.AddWorkspace(&snapshot)
	// Make workspace directories available in the recent-locations picker.
	d.store.UpsertRecentLocation(directory)
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

func (d *Daemon) handleMuteWorkspaceWS(client *wsClient, msg *protocol.MuteWorkspaceMessage) {
	if _, errMsg := d.toggleWorkspaceMute(msg.WorkspaceID); errMsg != "" {
		d.sendCommandError(client, protocol.CmdMuteWorkspace, errMsg)
	}
}

func (d *Daemon) toggleWorkspaceMute(workspaceID string) (protocol.Workspace, string) {
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return protocol.Workspace{}, "missing workspace_id"
	}
	if d.workspaces == nil {
		return protocol.Workspace{}, "workspace registry unavailable"
	}
	snapshot, ok := d.workspaces.toggleMuted(id)
	if !ok {
		return protocol.Workspace{}, "workspace not found"
	}
	d.store.ToggleWorkspaceMute(id)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceStateChanged,
		Workspace: &snapshot,
	})
	return snapshot, ""
}

// handleUnregisterWorkspace closes the workspace AND every session that
// belongs to it. Sessions get a graceful SIGTERM through unregisterSession
// (same path as the unix-socket "unregister" command), so transcripts flush
// and PTYs drain. We broadcast session_unregistered for each closed session
// before the workspace_unregistered, so clients can update their session
// list before they discover the workspace is gone.
func (d *Daemon) handleUnregisterWorkspace(client *wsClient, msg *protocol.UnregisterWorkspaceMessage) {
	id := strings.TrimSpace(msg.ID)
	if id == "" {
		d.sendCommandError(client, protocol.CmdUnregisterWorkspace, "missing id")
		return
	}
	if d.workspaces == nil {
		return
	}

	// Snapshot member sessions before tearing down — unregisterSession will
	// mutate the in-memory association map, which would race the snapshot
	// the registry hands out otherwise.
	memberIDs := d.workspaces.sessionIDs(id)
	for _, sid := range memberIDs {
		closed := d.unregisterSession(sid, syscall.SIGTERM)
		if closed != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionUnregistered,
				Session: d.sessionForBroadcast(closed),
			})
		}
	}

	snapshot, removed := d.workspaces.unregister(id)
	if !removed {
		return
	}
	d.cancelWorkspaceContextJanitor(id)
	d.store.RemoveWorkspace(id)
	d.pruneTileContentSubscriptionsForLayout(id, nil)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceUnregistered,
		Workspace: &snapshot,
	})
}

// loadWorkspacesFromStore rebuilds the in-memory registry from SQLite at
// daemon start. Order matters: we register every workspace first (so
// associateSession has somewhere to land), then walk persisted sessions and
// re-bind those that have a workspace_id. Status is recomputed last from the
// loaded session states.
func (d *Daemon) loadWorkspacesFromStore() {
	if d.workspaces == nil {
		d.workspaces = newWorkspaceRegistry()
	}
	for _, ws := range d.store.ListWorkspaces() {
		if ws == nil {
			continue
		}
		if len(d.store.SessionsInWorkspace(ws.ID)) == 0 {
			// Older startup reconciliation paths could reap a session without
			// removing its workspace. Preserve workspaces waiting for their
			// first spawn: the layout pane is persisted before spawn_session
			// creates the session row, and load can run after a daemon restart
			// in that gap. Also preserve workspaces with visible sessionless
			// content such as docked tiles.
			_, registered := d.workspaces.snapshot(ws.ID)
			if !registered &&
				!d.workspaceHasPendingSpawn(ws.ID) &&
				!d.workspaceHasSessionlessContent(ws.ID) {
				d.store.RemoveWorkspace(ws.ID)
				continue
			}
		}
		d.workspaces.register(ws.ID, ws.Title, ws.Directory, ws.Muted)
	}
	for _, session := range d.store.List("") {
		if session == nil {
			continue
		}
		if wsID := session.WorkspaceID; wsID != "" {
			d.workspaces.associateSession(session.ID, wsID, session.Label)
		}
	}
	// Seed each workspace's status from its members. No broadcast — clients
	// get the current rollup via InitialState.
	for _, ws := range d.workspaces.list() {
		d.recomputeWorkspaceStatus(ws.ID)
	}
}

func (d *Daemon) workspaceHasPendingSpawn(workspaceID string) bool {
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil {
		return false
	}
	for _, pane := range layout.Panes {
		if pane.Status == workspacelayout.PaneStatusSpawning {
			return true
		}
	}
	return false
}

// workspaceHasSessionlessContent is the single retention rule for a workspace
// after its final session leaves. Only visible docked tiles keep it alive;
// workspace context is deleted with the workspace.
func (d *Daemon) workspaceHasSessionlessContent(workspaceID string) bool {
	return d.workspaceLayoutHasTiles(workspaceID)
}

// listWorkspaces returns a snapshot of the current workspaces for InitialState.
func (d *Daemon) listWorkspaces() []protocol.Workspace {
	if d.workspaces == nil {
		return nil
	}
	workspaces := d.workspaces.list()
	for i := range workspaces {
		layout, err := d.protocolWorkspaceLayout(workspaces[i].ID)
		if err == nil {
			workspaces[i].Layout = layout
		}
	}
	if d.hubManager != nil {
		workspaces = append(workspaces, d.hubManager.RemoteWorkspaces()...)
	}
	return workspaces
}

// associateSessionWithWorkspace binds a freshly spawned session to a workspace
// and seeds the rollup status. Called from handleSpawnSession when the spawn
// message carries workspace_id.
func (d *Daemon) associateSessionWithWorkspace(sessionID, workspaceID string) {
	if workspaceID == "" || d.workspaces == nil {
		return
	}
	title := sessionID
	if session := d.store.Get(sessionID); session != nil && session.Label != "" {
		title = session.Label
	}
	if !d.workspaces.associateSession(sessionID, workspaceID, title) {
		d.logf("workspace association rejected for session %s: workspace not registered: %s", sessionID, workspaceID)
		return
	}
	d.store.AssignSessionWorkspace(sessionID, workspaceID)
	updated, changed := d.recomputeWorkspaceStatus(workspaceID)
	if !changed {
		updated, _ = d.workspaces.snapshot(workspaceID)
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceStateChanged,
		Workspace: &updated,
	})
}

// dissociateSessionFromWorkspace is called when a session is unregistered, so
// the rolled-up workspace status no longer counts the gone session.
func (d *Daemon) dissociateSessionFromWorkspace(sessionID string) {
	if d.workspaces == nil {
		return
	}
	workspaceID, remaining := d.workspaces.dissociateSession(sessionID)
	if workspaceID == "" {
		return
	}
	if remaining == 0 {
		// A workspace whose last session leaves normally tears down. Visible
		// sessionless content keeps it alive. This runs before the session pane
		// is removed, so a retained tile is still visible in the stored layout.
		if d.workspaceHasSessionlessContent(workspaceID) {
			updated, changed := d.recomputeWorkspaceStatus(workspaceID)
			if !changed {
				updated, _ = d.workspaces.snapshot(workspaceID)
			}
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:     protocol.EventWorkspaceStateChanged,
				Workspace: &updated,
			})
			return
		}
		snapshot, removed := d.workspaces.unregister(workspaceID)
		if !removed {
			return
		}
		d.cancelWorkspaceContextJanitor(workspaceID)
		d.store.RemoveWorkspace(workspaceID)
		d.pruneTileContentSubscriptionsForLayout(workspaceID, nil)
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:     protocol.EventWorkspaceUnregistered,
			Workspace: &snapshot,
		})
		return
	}
	updated, changed := d.recomputeWorkspaceStatus(workspaceID)
	if !changed {
		updated, _ = d.workspaces.snapshot(workspaceID)
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceStateChanged,
		Workspace: &updated,
	})
}

// decorateSessionWithWorkspace fills in WorkspaceID on a session about to be
// broadcast, if an in-memory association exists. Called from sessionForBroadcast.
func (d *Daemon) decorateSessionWithWorkspace(session *protocol.Session) {
	if session == nil || d.workspaces == nil {
		return
	}
	if id := d.workspaces.workspaceIDForSession(session.ID); id != "" {
		session.WorkspaceID = id
	} else {
		session.WorkspaceID = ""
	}
}
