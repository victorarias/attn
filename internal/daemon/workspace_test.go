package daemon

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestRollupWorkspaceStatus_PriorityOrdering(t *testing.T) {
	cases := []struct {
		name   string
		states []protocol.SessionState
		want   protocol.WorkspaceStatus
	}{
		{
			name:   "empty yields idle",
			states: nil,
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "single working",
			states: []protocol.SessionState{protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "working beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "working beats waiting_input",
			states: []protocol.SessionState{protocol.SessionStateWaitingInput, protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "waiting_input beats pending_approval",
			states: []protocol.SessionState{protocol.SessionStatePendingApproval, protocol.SessionStateWaitingInput},
			want:   protocol.WorkspaceStatusWaitingInput,
		},
		{
			name:   "pending_approval beats idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStatePendingApproval},
			want:   protocol.WorkspaceStatusPendingApproval,
		},
		{
			name:   "idle beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateIdle},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "launching beats unrecognised session_state_unknown",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStateLaunching},
			want:   protocol.WorkspaceStatusLaunching,
		},
		{
			name:   "all session_state_unknown yields idle",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStateUnknown},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "all idle yields idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateIdle},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "all launching yields launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateLaunching},
			want:   protocol.WorkspaceStatusLaunching,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rollupWorkspaceStatus(tc.states)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkspaceRegistry_RegisterUnregister(t *testing.T) {
	r := newWorkspaceRegistry()
	snapshot, isNew := r.register("ws1", "Workspace 1", "/repo")
	if !isNew {
		t.Fatal("first register should be new")
	}
	if snapshot.ID != "ws1" || snapshot.Title != "Workspace 1" || snapshot.Directory != "/repo" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Status != protocol.WorkspaceStatusIdle {
		t.Fatalf("initial status = %q, want idle", snapshot.Status)
	}

	_, isNew = r.register("ws1", "Renamed", "/repo")
	if isNew {
		t.Fatal("second register should not be new")
	}

	unreg, removed := r.unregister("ws1")
	if !removed {
		t.Fatal("unregister returned not removed")
	}
	if unreg.Title != "Renamed" {
		t.Fatalf("unregister snapshot title = %q, want Renamed", unreg.Title)
	}
	if _, removed := r.unregister("ws1"); removed {
		t.Fatal("second unregister should report not removed")
	}
}

func TestWorkspaceRegistry_AssociateAndDissociate(t *testing.T) {
	r := newWorkspaceRegistry()
	r.register("ws1", "ws", "/repo")
	r.register("ws2", "ws", "/repo")

	if !r.associateSession("s1", "ws1") {
		t.Fatal("associate should succeed")
	}
	if r.workspaceIDForSession("s1") != "ws1" {
		t.Fatal("association lookup failed")
	}
	// re-associate to a different workspace clears the previous link
	if !r.associateSession("s1", "ws2") {
		t.Fatal("re-associate should succeed")
	}
	if got := r.workspaceIDForSession("s1"); got != "ws2" {
		t.Fatalf("workspaceIDForSession = %q, want ws2", got)
	}
	if ids := r.sessionIDs("ws1"); len(ids) != 0 {
		t.Fatalf("ws1 should have no sessions after move, got %v", ids)
	}
	if ids := r.sessionIDs("ws2"); len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("ws2 sessionIDs = %v, want [s1]", ids)
	}

	// associate to a non-existent workspace returns false and changes nothing
	if r.associateSession("s2", "missing") {
		t.Fatal("associate to unknown workspace should fail")
	}

	// dissociate returns the workspace and removes the link
	if got := r.dissociateSession("s1"); got != "ws2" {
		t.Fatalf("dissociate returned %q, want ws2", got)
	}
	if r.workspaceIDForSession("s1") != "" {
		t.Fatal("session still associated after dissociate")
	}
}

func TestWorkspaceRegistry_UnregisterCleansSessionLinks(t *testing.T) {
	r := newWorkspaceRegistry()
	r.register("ws1", "ws", "/repo")
	r.associateSession("s1", "ws1")
	r.associateSession("s2", "ws1")

	if _, removed := r.unregister("ws1"); !removed {
		t.Fatal("unregister failed")
	}
	if r.workspaceIDForSession("s1") != "" || r.workspaceIDForSession("s2") != "" {
		t.Fatal("session-to-workspace links should be cleared on unregister")
	}
}

func newDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

type broadcastCapture struct {
	mu     sync.Mutex
	events []protocol.WebSocketEvent
}

func (c *broadcastCapture) snapshot() []protocol.WebSocketEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.WebSocketEvent, len(c.events))
	copy(out, c.events)
	return out
}

func captureBroadcasts(d *Daemon) *broadcastCapture {
	c := &broadcastCapture{}
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event == nil {
			return
		}
		c.mu.Lock()
		c.events = append(c.events, *event)
		c.mu.Unlock()
	}
	return c
}

func TestHandleRegisterWorkspace_BroadcastsRegisteredThenStateChanged(t *testing.T) {
	d := newDaemonForTest(t)
	cap := captureBroadcasts(d)

	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "ws1",
		Title:     "Workspace 1",
		Directory: "/repo",
	})

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(events))
	}
	if events[0].Event != protocol.EventWorkspaceRegistered {
		t.Fatalf("first broadcast = %q, want workspace_registered", events[0].Event)
	}
	if events[0].Workspace == nil || events[0].Workspace.ID != "ws1" {
		t.Fatalf("workspace payload missing or wrong: %+v", events[0].Workspace)
	}

	// Second registration should publish state_changed instead
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "ws1",
		Title:     "Workspace 1 renamed",
		Directory: "/repo",
	})
	events = cap.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 broadcasts, got %d", len(events))
	}
	if events[1].Event != protocol.EventWorkspaceStateChanged {
		t.Fatalf("second broadcast = %q, want workspace_state_changed", events[1].Event)
	}
}

func TestHandleUnregisterWorkspace_BroadcastsOnlyForKnown(t *testing.T) {
	d := newDaemonForTest(t)
	cap := captureBroadcasts(d)

	// Unknown workspace: no broadcast
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{
		Cmd: protocol.CmdUnregisterWorkspace,
		ID:  "missing",
	})
	if events := cap.snapshot(); len(events) != 0 {
		t.Fatalf("expected no broadcast for unknown workspace, got %d", len(events))
	}

	// Register then unregister
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "ws1",
		Title:     "ws",
		Directory: "/repo",
	})
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{
		Cmd: protocol.CmdUnregisterWorkspace,
		ID:  "ws1",
	})
	events := cap.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 broadcasts (registered, unregistered), got %d", len(events))
	}
	if events[1].Event != protocol.EventWorkspaceUnregistered {
		t.Fatalf("second broadcast = %q, want workspace_unregistered", events[1].Event)
	}
}

func TestRecomputeWorkspaceStatus_UpdatesOnSessionStateChange(t *testing.T) {
	d := newDaemonForTest(t)
	now := string(protocol.TimestampNow())

	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "ws1",
		Title:     "ws",
		Directory: "/repo",
	})

	// Add a session to the store and bind it to the workspace
	d.store.Add(&protocol.Session{
		ID:             "s1",
		Label:          "s1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/repo",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.associateSessionWithWorkspace("s1", "ws1")

	cap := captureBroadcasts(d)

	// Session goes to working — workspace should recompute to Working and broadcast
	d.store.UpdateState("s1", protocol.StateWorking)
	d.recomputeAndBroadcastWorkspaceForSession("s1")

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(events))
	}
	if events[0].Event != protocol.EventWorkspaceStateChanged {
		t.Fatalf("event = %q, want workspace_state_changed", events[0].Event)
	}
	if events[0].Workspace == nil || events[0].Workspace.Status != protocol.WorkspaceStatusWorking {
		t.Fatalf("workspace payload = %+v, want status=working", events[0].Workspace)
	}
}

func TestRecomputeWorkspaceStatus_SuppressesNoChangeBroadcast(t *testing.T) {
	d := newDaemonForTest(t)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws1", Title: "ws", Directory: "/repo",
	})
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "s1", Agent: protocol.SessionAgentCodex, Directory: "/repo",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.associateSessionWithWorkspace("s1", "ws1")

	// Force the cached workspace status to idle so the next recompute is a no-op.
	d.recomputeWorkspaceStatus("ws1")

	cap := captureBroadcasts(d)
	d.recomputeAndBroadcastWorkspaceForSession("s1")
	if events := cap.snapshot(); len(events) != 0 {
		t.Fatalf("expected no broadcast when status unchanged, got %d", len(events))
	}
}

func TestSessionForBroadcast_PopulatesWorkspaceID(t *testing.T) {
	d := newDaemonForTest(t)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws1", Title: "ws", Directory: "/repo",
	})
	now := string(protocol.TimestampNow())
	session := &protocol.Session{
		ID: "s1", Label: "s1", Agent: protocol.SessionAgentCodex, Directory: "/repo",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}
	d.store.Add(session)
	d.associateSessionWithWorkspace("s1", "ws1")

	got := d.sessionForBroadcast(d.store.Get("s1"))
	if got == nil {
		t.Fatal("nil broadcast clone")
	}
	if got.WorkspaceID == nil || *got.WorkspaceID != "ws1" {
		t.Fatalf("workspace_id = %v, want pointer to ws1", got.WorkspaceID)
	}

	// After dissociation the field should be cleared.
	d.dissociateSessionFromWorkspace("s1")
	got = d.sessionForBroadcast(d.store.Get("s1"))
	if got.WorkspaceID != nil {
		t.Fatalf("workspace_id should be nil after dissociate, got %v", *got.WorkspaceID)
	}
}

func TestListWorkspaces_IncludesRegistered(t *testing.T) {
	d := newDaemonForTest(t)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws1", Title: "A", Directory: "/a",
	})
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws2", Title: "B", Directory: "/b",
	})

	list := d.listWorkspaces()
	if len(list) != 2 {
		t.Fatalf("expected 2 workspaces, got %d (%+v)", len(list), list)
	}
}

func TestAssociateSessionWithWorkspace_StaleIDIsDropped(t *testing.T) {
	d := newDaemonForTest(t)
	cap := captureBroadcasts(d)
	// Stale workspace_id (from a client whose previous workspace was lost on a
	// daemon restart). Should NOT broadcast or panic.
	d.associateSessionWithWorkspace("s1", "ghost")
	if events := cap.snapshot(); len(events) != 0 {
		t.Fatalf("expected no broadcast for stale workspace_id, got %d", len(events))
	}
}

func TestRegisterWorkspace_PersistsToStoreAndUpsertsRecentLocation(t *testing.T) {
	d := newDaemonForTest(t)
	dir := t.TempDir() // Real path so GetRecentLocations doesn't filter it out.
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "ws1",
		Title:     "Workspace 1",
		Directory: dir,
	})

	persisted := d.store.GetWorkspace("ws1")
	if persisted == nil {
		t.Fatal("workspace was not persisted to store")
	}
	if persisted.Title != "Workspace 1" || persisted.Directory != dir {
		t.Fatalf("persisted workspace mismatch: %+v", persisted)
	}

	// Recent locations should include the workspace's directory.
	found := false
	for _, loc := range d.store.GetRecentLocations(50) {
		if loc != nil && loc.Path == dir {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("workspace directory was not added to recent_locations")
	}
}

func TestUnregisterWorkspace_CascadeClosesMemberSessions(t *testing.T) {
	d := newDaemonForTest(t)
	now := string(protocol.TimestampNow())

	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws1", Title: "ws", Directory: "/repo",
	})
	for _, sid := range []string{"s1", "s2"} {
		d.store.Add(&protocol.Session{
			ID: sid, Label: sid, Agent: protocol.SessionAgentCodex, Directory: "/repo",
			State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		d.associateSessionWithWorkspace(sid, "ws1")
	}

	cap := captureBroadcasts(d)
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{
		Cmd: protocol.CmdUnregisterWorkspace, ID: "ws1",
	})

	// Both sessions should be gone from the store.
	if d.store.Get("s1") != nil || d.store.Get("s2") != nil {
		t.Fatal("member sessions were not removed from the store")
	}
	// Workspace itself is gone from the store.
	if d.store.GetWorkspace("ws1") != nil {
		t.Fatal("workspace was not removed from the store")
	}

	// Broadcast order: two session_unregistered, then workspace_unregistered.
	events := cap.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 broadcasts (2 session_unregistered + 1 workspace_unregistered), got %d: %+v", len(events), events)
	}
	for i, want := range []string{
		protocol.EventSessionUnregistered,
		protocol.EventSessionUnregistered,
		protocol.EventWorkspaceUnregistered,
	} {
		if events[i].Event != want {
			t.Fatalf("event[%d] = %q, want %q", i, events[i].Event, want)
		}
	}
}

func TestLoadWorkspacesFromStore_RebuildsRegistryAndReassociates(t *testing.T) {
	d := newDaemonForTest(t)
	now := string(protocol.TimestampNow())

	// Seed the store directly, simulating state that was persisted before a
	// daemon restart.
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws1", Title: "ws", Directory: "/repo"})
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "s1", Agent: protocol.SessionAgentCodex, Directory: "/repo",
		WorkspaceID: protocol.Ptr("ws1"),
		State:       protocol.SessionStateWorking,
		StateSince:  now, StateUpdatedAt: now, LastSeen: now,
	})

	// Fresh registry; load from the store.
	d.workspaces = newWorkspaceRegistry()
	d.loadWorkspacesFromStore()

	// Registry should know about ws1, the session-to-workspace link, and the
	// rollup status (working).
	if got := d.workspaces.workspaceIDForSession("s1"); got != "ws1" {
		t.Fatalf("association not reloaded: got %q, want ws1", got)
	}
	snap, ok := d.workspaces.snapshot("ws1")
	if !ok {
		t.Fatal("workspace not in registry after load")
	}
	if snap.Status != protocol.WorkspaceStatusWorking {
		t.Fatalf("status after load = %q, want working", snap.Status)
	}
}

func TestAssociateSessionWithWorkspace_PersistsToStore(t *testing.T) {
	d := newDaemonForTest(t)
	now := string(protocol.TimestampNow())
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws1", Title: "ws", Directory: "/repo",
	})
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "s1", Agent: protocol.SessionAgentCodex, Directory: "/repo",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.associateSessionWithWorkspace("s1", "ws1")

	got := d.store.Get("s1")
	if got == nil || got.WorkspaceID == nil || *got.WorkspaceID != "ws1" {
		t.Fatalf("workspace_id was not persisted on session: %+v", got)
	}

	// And the dissociate path clears it.
	d.dissociateSessionFromWorkspace("s1")
	got = d.store.Get("s1")
	if got == nil || got.WorkspaceID != nil {
		t.Fatalf("workspace_id was not cleared on dissociate: %+v", got)
	}
}
