package daemon

import (
	"strings"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/rankkey"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type workspaceEntry struct {
	id         string
	title      string
	directory  string
	status     protocol.WorkspaceStatus
	muted      bool
	pinned     bool
	rank       string
	sessionIDs map[string]struct{}
}

type workspaceRegistry struct {
	mu                 sync.RWMutex
	workspaces         map[string]*workspaceEntry
	sessionToWorkspace map[string]string
}

func newWorkspaceRegistry() *workspaceRegistry {
	return &workspaceRegistry{
		workspaces:         make(map[string]*workspaceEntry),
		sessionToWorkspace: make(map[string]string),
	}
}

func (r *workspaceRegistry) register(id, title, directory, rank string, muted, pinned bool) (protocol.Workspace, bool) {
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
	entry.pinned = pinned
	if rank != "" {
		entry.rank = rank
	}
	return snapshotEntry(entry), !existed
}

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

func (r *workspaceRegistry) setMuted(id string, muted bool) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	entry.muted = muted
	return snapshotEntry(entry), true
}

func (r *workspaceRegistry) setPinned(id string, pinned bool) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	entry.pinned = pinned
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

func (r *workspaceRegistry) applyRank(id, rank string) (protocol.Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workspaces[id]
	if !ok {
		return protocol.Workspace{}, false
	}
	entry.rank = rank
	return snapshotEntry(entry), true
}

// "" doubles as rankkey.Between's MIN/MAX sentinel, so an unranked or missing
// neighbour resolves to the open bound.
func (r *workspaceRegistry) rankOf(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.workspaces[id]; ok {
		return entry.rank
	}
	return ""
}

func (r *workspaceRegistry) maxRank() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	max := ""
	for _, entry := range r.workspaces {
		if entry.rank > max {
			max = entry.rank
		}
	}
	return max
}

func snapshotEntry(e *workspaceEntry) protocol.Workspace {
	return protocol.Workspace{
		ID:        e.id,
		Title:     e.title,
		Directory: e.directory,
		Status:    e.status,
		Muted:     e.muted,
		Pinned:    e.pinned,
		Rank:      e.rank,
	}
}

func rollupWorkspaceStatus(sessionStates []protocol.SessionState) protocol.WorkspaceStatus {
	priority := map[protocol.SessionState]int{
		protocol.SessionStateWorking:         8,
		protocol.SessionStateWaitingInput:    7,
		protocol.SessionStatePendingApproval: 6,
		protocol.SessionStateUnknown:         5,
		protocol.SessionStateScheduled:       4,
		protocol.SessionStateIdle:            3,
		protocol.SessionStateRecoverable:     2,
		protocol.SessionStateLaunching:       1,
	}
	statusFor := map[protocol.SessionState]protocol.WorkspaceStatus{
		protocol.SessionStateLaunching:       protocol.WorkspaceStatusLaunching,
		protocol.SessionStateWorking:         protocol.WorkspaceStatusWorking,
		protocol.SessionStateWaitingInput:    protocol.WorkspaceStatusWaitingInput,
		protocol.SessionStatePendingApproval: protocol.WorkspaceStatusPendingApproval,
		protocol.SessionStateUnknown:         protocol.WorkspaceStatusUnknown,
		protocol.SessionStateScheduled:       protocol.WorkspaceStatusScheduled,
		protocol.SessionStateIdle:            protocol.WorkspaceStatusIdle,
		protocol.SessionStateRecoverable:     protocol.WorkspaceStatusIdle,
	}
	bestPriority := 0
	best := protocol.WorkspaceStatusIdle
	for _, s := range sessionStates {
		p, ok := priority[s]
		if !ok {
			continue
		}
		if p > bestPriority {
			bestPriority = p
			best = statusFor[s]
		}
	}
	return best
}

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

func (d *Daemon) reseedWorkspaceStatuses() {
	if d.workspaces == nil {
		return
	}
	for _, ws := range d.workspaces.list() {
		d.recomputeWorkspaceStatus(ws.ID)
	}
}

func (d *Daemon) projectWorkspaceEvent(event, workspaceID string) {
	if d.workspaces == nil {
		return
	}
	snapshot, ok := d.workspaces.snapshot(workspaceID)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     event,
		Workspace: &snapshot,
	})
}

func (d *Daemon) projectWorkspaceUnregistered(ev bus.Event) {
	snapshot, ok := decodeFact[protocol.Workspace](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceUnregistered,
		Workspace: &snapshot,
	})
}

func (d *Daemon) recomputeAndBroadcastWorkspaceForSession(sessionID string) {
	if d.workspaces == nil {
		return
	}
	workspaceID := d.workspaces.workspaceIDForSession(sessionID)
	if workspaceID == "" {
		return
	}
	if _, changed := d.recomputeWorkspaceStatus(workspaceID); !changed {
		return
	}
	d.publishFact(FactWorkspaceStatusChanged, workspaceID, nil)
}

// The store persists rank on INSERT only, so this seed must be set before
// AddWorkspace or it is silently dropped.
func (d *Daemon) resolveWorkspaceRank(existing *protocol.Workspace) string {
	if existing != nil && existing.Rank != "" {
		return existing.Rank
	}
	return rankkey.After(d.workspaces.maxRank())
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
	pinned := existing != nil && existing.Pinned
	if existing != nil && strings.TrimSpace(existing.Title) != "" {
		title = existing.Title
	}
	rank := d.resolveWorkspaceRank(existing)
	snapshot, isNew := d.workspaces.register(id, title, directory, rank, muted, pinned)
	d.store.AddWorkspace(&snapshot)
	d.store.UpsertRecentLocation(directory)
	fact := FactWorkspaceRegistered
	if !isNew {
		d.recomputeWorkspaceStatus(id)
		fact = FactWorkspaceReregistered
	}
	d.publishFact(fact, id, nil)
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
	d.publishFact(FactWorkspaceMuteChanged, id, nil)
	return snapshot, ""
}

func (d *Daemon) setWorkspaceMuted(workspaceID string, muted bool) (protocol.Workspace, string) {
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return protocol.Workspace{}, "missing workspace_id"
	}
	if d.workspaces == nil {
		return protocol.Workspace{}, "workspace registry unavailable"
	}
	current, ok := d.workspaces.snapshot(id)
	if !ok {
		return protocol.Workspace{}, "workspace not found"
	}
	if current.Muted == muted {
		return current, ""
	}
	if err := d.store.SetWorkspaceMuted(id, muted); err != nil {
		return protocol.Workspace{}, "persist workspace mute: " + err.Error()
	}
	snapshot, ok := d.workspaces.setMuted(id, muted)
	if !ok {
		_ = d.store.SetWorkspaceMuted(id, current.Muted)
		return protocol.Workspace{}, "workspace disappeared while updating mute state"
	}
	d.publishFact(FactWorkspaceMuteChanged, id, nil)
	return snapshot, ""
}

func (d *Daemon) handlePinWorkspaceWS(client *wsClient, msg *protocol.PinWorkspaceMessage) {
	if _, errMsg := d.setWorkspacePinned(msg.WorkspaceID, msg.Pinned); errMsg != "" {
		d.sendCommandError(client, protocol.CmdPinWorkspace, errMsg)
	}
}

func (d *Daemon) setWorkspacePinned(workspaceID string, pinned bool) (protocol.Workspace, string) {
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return protocol.Workspace{}, "missing workspace_id"
	}
	if d.workspaces == nil {
		return protocol.Workspace{}, "workspace registry unavailable"
	}
	current, ok := d.workspaces.snapshot(id)
	if !ok {
		return protocol.Workspace{}, "workspace not found"
	}
	if current.Pinned == pinned {
		return current, ""
	}
	if err := d.store.SetWorkspacePinned(id, pinned); err != nil {
		return protocol.Workspace{}, "persist workspace pin: " + err.Error()
	}
	snapshot, ok := d.workspaces.setPinned(id, pinned)
	if !ok {
		_ = d.store.SetWorkspacePinned(id, current.Pinned)
		return protocol.Workspace{}, "workspace disappeared while updating pin state"
	}
	d.publishFact(FactWorkspacePinChanged, id, nil)
	return snapshot, ""
}

func (d *Daemon) tearDownRemovedWorkspace(snapshot protocol.Workspace) {
	id := snapshot.ID
	d.store.RemoveWorkspace(id)
	d.pruneTileContentSubscriptionsForLayout(id, nil)
	// Rides in the payload: the registry entry is gone by projection time.
	d.publishFact(FactWorkspaceUnregistered, id, snapshot)
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

	// Snapshot before removing session state: the association map changes with it.
	// session_unregistered must reach clients before workspace_unregistered.
	memberIDs := d.workspaces.sessionIDs(id)
	teardowns := make(map[string]*sessionTeardown, len(memberIDs))
	for _, sid := range memberIDs {
		teardown, err := d.prepareSessionTeardown(sid)
		if err != nil {
			for preparedID := range teardowns {
				d.cancelSessionTeardown(preparedID)
			}
			d.sendCommandError(client, protocol.CmdUnregisterWorkspace, err.Error())
			return
		}
		teardowns[sid] = teardown
	}
	for _, sid := range memberIDs {
		teardown := teardowns[sid]
		d.commitSessionUnregister(sid, store.SessionClose{By: store.SessionClosedByUser})
		d.publishSessionUnregistered(teardown.session)
	}

	snapshot, removed := d.workspaces.unregister(id)
	if !removed {
		return
	}
	d.tearDownRemovedWorkspace(snapshot)
	for _, sid := range memberIDs {
		if teardown := teardowns[sid]; teardown != nil {
			d.terminateSessionAsync(sid, syscall.SIGTERM, teardown)
		}
	}
}

// Every workspace must be registered before sessions are re-bound, or
// associateSession has nowhere to land.
func (d *Daemon) loadWorkspacesFromStore() []string {
	if d.workspaces == nil {
		d.workspaces = newWorkspaceRegistry()
	}
	var reaped []string
	for _, ws := range d.store.ListWorkspaces() {
		if ws == nil {
			continue
		}
		if len(d.store.SessionsInWorkspace(ws.ID)) == 0 {
			_, registered := d.workspaces.snapshot(ws.ID)
			if !registered &&
				!ws.Pinned &&
				!d.workspaceHasPendingSpawn(ws.ID) &&
				!d.workspaceHasSessionlessContent(ws.ID) {
				d.store.RemoveWorkspace(ws.ID)
				reaped = append(reaped, ws.ID)
				continue
			}
		}
		d.workspaces.register(ws.ID, ws.Title, ws.Directory, ws.Rank, ws.Muted, ws.Pinned)
	}
	for _, session := range d.store.List("") {
		if session == nil {
			continue
		}
		if wsID := session.WorkspaceID; wsID != "" {
			d.workspaces.associateSession(session.ID, wsID, session.Label)
		}
	}
	for _, ws := range d.workspaces.list() {
		d.recomputeWorkspaceStatus(ws.ID)
	}
	return reaped
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

func (d *Daemon) workspaceHasSessionlessContent(workspaceID string) bool {
	return d.workspaceLayoutHasTiles(workspaceID)
}

func (d *Daemon) listLocalWorkspaces() []protocol.Workspace {
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
	return workspaces
}

func (d *Daemon) listWorkspaces() []protocol.Workspace {
	workspaces := d.listLocalWorkspaces()
	if d.hubManager != nil {
		workspaces = append(workspaces, d.hubManager.RemoteWorkspaces()...)
	}
	return workspaces
}

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
	d.recomputeWorkspaceStatus(workspaceID)
	d.publishFact(FactWorkspaceSessionAssociated, workspaceID, nil)
}

func (d *Daemon) dissociateSessionFromWorkspace(sessionID string) {
	if d.workspaces == nil {
		return
	}
	workspaceID, remaining := d.workspaces.dissociateSession(sessionID)
	if workspaceID == "" {
		return
	}
	if remaining == 0 {
		snap, _ := d.workspaces.snapshot(workspaceID)
		if snap.Pinned || d.workspaceHasSessionlessContent(workspaceID) {
			d.recomputeWorkspaceStatus(workspaceID)
			d.publishFact(FactWorkspaceSessionDissociated, workspaceID, nil)
			return
		}
		snapshot, removed := d.workspaces.unregister(workspaceID)
		if !removed {
			return
		}
		d.tearDownRemovedWorkspace(snapshot)
		return
	}
	d.recomputeWorkspaceStatus(workspaceID)
	d.publishFact(FactWorkspaceSessionDissociated, workspaceID, nil)
}

func (d *Daemon) decorateSessionWithWorkspace(session *protocol.Session) {
	if session == nil || d.workspaces == nil {
		return
	}
	if id := d.workspaces.workspaceIDForSession(session.ID); id != "" {
		session.WorkspaceID = id
	}
}

func (d *Daemon) decorateSessionWithWorkspaceMute(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	workspace := d.store.GetWorkspace(session.WorkspaceID)
	if workspace != nil && workspace.Muted {
		session.WorkspaceMuted = protocol.Ptr(true)
		return
	}
	session.WorkspaceMuted = nil
}
