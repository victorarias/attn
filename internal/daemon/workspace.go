package daemon

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspace"
)

var shellTitlePattern = regexp.MustCompile(`^Shell (\d+)$`)

func (d *Daemon) ensureWorkspaceSnapshot(sessionID string) (*workspace.Snapshot, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	current := d.store.GetWorkspace(sessionID)
	if current == nil {
		snapshot := workspace.DefaultSnapshot(sessionID)
		if err := d.store.SaveWorkspace(snapshot); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}

	normalized := workspace.NormalizeSnapshot(*current, sessionID)
	if !reflect.DeepEqual(*current, normalized) {
		if err := d.store.SaveWorkspace(normalized); err != nil {
			return nil, err
		}
	}
	return &normalized, nil
}

func (d *Daemon) protocolWorkspaceSnapshot(sessionID string) (*protocol.WorkspaceSnapshot, error) {
	snapshot, err := d.ensureWorkspaceSnapshot(sessionID)
	if err != nil {
		return nil, err
	}
	return protocolWorkspaceSnapshot(*snapshot)
}

func protocolWorkspaceSnapshot(snapshot workspace.Snapshot) (*protocol.WorkspaceSnapshot, error) {
	layoutJSON, err := workspace.EncodeLayout(snapshot.Layout)
	if err != nil {
		return nil, err
	}
	panes := make([]protocol.WorkspacePane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		next := protocol.WorkspacePane{
			PaneID: pane.PaneID,
			Kind:   protocol.WorkspacePaneKind(pane.Kind),
			Title:  pane.Title,
		}
		if strings.TrimSpace(pane.RuntimeID) != "" {
			next.RuntimeID = protocol.Ptr(strings.TrimSpace(pane.RuntimeID))
		}
		panes = append(panes, next)
	}
	ws := &protocol.WorkspaceSnapshot{
		SessionID:    snapshot.SessionID,
		ActivePaneID: snapshot.ActivePaneID,
		LayoutJson:   layoutJSON,
		Panes:        panes,
	}
	if strings.TrimSpace(snapshot.UpdatedAt) != "" {
		ws.UpdatedAt = protocol.Ptr(snapshot.UpdatedAt)
	}
	return ws, nil
}

func (d *Daemon) listWorkspaceSnapshots(sessions []*protocol.Session) []protocol.WorkspaceSnapshot {
	if len(sessions) == 0 {
		return nil
	}
	workspaces := make([]protocol.WorkspaceSnapshot, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		snapshot, err := d.protocolWorkspaceSnapshot(session.ID)
		if err != nil {
			d.logf("workspace snapshot failed for session %s: %v", session.ID, err)
			continue
		}
		workspaces = append(workspaces, *snapshot)
	}
	return workspaces
}

func (d *Daemon) sendWorkspaceSnapshot(client *wsClient, sessionID string) {
	snapshot, err := d.protocolWorkspaceSnapshot(sessionID)
	if err != nil {
		d.sendCommandError(client, protocol.CmdWorkspaceGet, err.Error())
		return
	}
	d.sendToClient(client, protocol.WorkspaceSnapshotMessage{
		Event:     protocol.EventWorkspaceSnapshot,
		Workspace: *snapshot,
	})
}

func (d *Daemon) sendWorkspaceActionResult(client *wsClient, action, sessionID string, paneID *string, err error) {
	result := protocol.WorkspaceActionResultMessage{
		Event:     protocol.EventWorkspaceActionResult,
		Action:    action,
		SessionID: sessionID,
		Success:   err == nil,
	}
	if paneID != nil && strings.TrimSpace(*paneID) != "" {
		result.PaneID = protocol.Ptr(strings.TrimSpace(*paneID))
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) broadcastWorkspaceUpdated(sessionID string) {
	snapshot, err := d.protocolWorkspaceSnapshot(sessionID)
	if err != nil {
		d.logf("workspace update failed for session %s: %v", sessionID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceUpdated,
		Workspace: snapshot,
	})
}

func (d *Daemon) broadcastWorkspaceSnapshot(sessionID string) {
	snapshot, err := d.protocolWorkspaceSnapshot(sessionID)
	if err != nil {
		d.logf("workspace snapshot failed for session %s: %v", sessionID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceSnapshot,
		Workspace: snapshot,
	})
}

func protocolDirection(direction protocol.WorkspaceSplitDirection) workspace.Direction {
	switch direction {
	case protocol.WorkspaceSplitDirectionHorizontal:
		return workspace.DirectionHorizontal
	default:
		return workspace.DirectionVertical
	}
}

func nextShellTitle(snapshot workspace.Snapshot) string {
	maxShell := 0
	for _, pane := range snapshot.Panes {
		if pane.Kind != workspace.PaneKindShell {
			continue
		}
		matches := shellTitlePattern.FindStringSubmatch(strings.TrimSpace(pane.Title))
		if len(matches) != 2 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(matches[1], "%d", &n); err == nil && n > maxShell {
			maxShell = n
		}
	}
	return fmt.Sprintf("Shell %d", maxShell+1)
}

func newWorkspaceEntityID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (d *Daemon) handleWorkspaceGet(client *wsClient, msg *protocol.WorkspaceGetMessage) {
	d.sendWorkspaceSnapshot(client, msg.SessionID)
}

func (d *Daemon) handleWorkspaceFocusPane(client *wsClient, msg *protocol.WorkspaceFocusPaneMessage) {
	snapshot, err := d.ensureWorkspaceSnapshot(msg.SessionID)
	if err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	if !workspace.HasPane(snapshot.Layout, msg.PaneID) {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if snapshot.ActivePaneID == msg.PaneID {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
		return
	}
	snapshot.ActivePaneID = msg.PaneID
	if err := d.store.SaveWorkspace(*snapshot); err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceUpdated(msg.SessionID)
	d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleWorkspaceRenamePane(client *wsClient, msg *protocol.WorkspaceRenamePaneMessage) {
	if msg.PaneID == workspace.MainPaneID {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("main pane title is fixed"))
		return
	}

	snapshot, err := d.ensureWorkspaceSnapshot(msg.SessionID)
	if err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("title cannot be empty"))
		return
	}
	updated := false
	for i := range snapshot.Panes {
		if snapshot.Panes[i].PaneID != msg.PaneID {
			continue
		}
		snapshot.Panes[i].Title = title
		updated = true
		break
	}
	if !updated {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if err := d.store.SaveWorkspace(*snapshot); err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceUpdated(msg.SessionID)
	d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleWorkspaceSplitPane(client *wsClient, msg *protocol.WorkspaceSplitPaneMessage) {
	if d.ptyBackend == nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pty backend unavailable"))
		return
	}
	snapshot, err := d.ensureWorkspaceSnapshot(msg.SessionID)
	if err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	session := d.store.Get(msg.SessionID)
	if session == nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("session not found: %s", msg.SessionID))
		return
	}
	if !workspace.HasPane(snapshot.Layout, msg.TargetPaneID) {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	paneID := newWorkspaceEntityID("pane")
	runtimeID := newWorkspaceEntityID("runtime")
	splitID := newWorkspaceEntityID("split")
	title := nextShellTitle(*snapshot)

	if err := d.ptyBackend.Spawn(context.Background(), ptySpawnShellOptions(runtimeID, session.Directory, title)); err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}

	layout, changed := workspace.Split(
		snapshot.Layout,
		msg.TargetPaneID,
		paneID,
		splitID,
		protocolDirection(msg.Direction),
		workspace.DefaultSplitRatio,
	)
	if !changed {
		_ = d.removePTYSession(runtimeID)
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, workspace.Pane{
		PaneID:    paneID,
		RuntimeID: runtimeID,
		Kind:      workspace.PaneKindShell,
		Title:     title,
	})
	normalized := workspace.NormalizeSnapshot(*snapshot, msg.SessionID)
	if err := d.store.SaveWorkspace(normalized); err != nil {
		_ = d.removePTYSession(runtimeID)
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	d.broadcastWorkspaceUpdated(msg.SessionID)
	d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), nil)
}

func ptySpawnShellOptions(runtimeID, cwd, label string) ptybackend.SpawnOptions {
	return ptybackend.SpawnOptions{
		ID:    runtimeID,
		CWD:   cwd,
		Agent: protocol.AgentShellValue,
		Label: label,
		Cols:  80,
		Rows:  24,
	}
}

func (d *Daemon) handleWorkspaceClosePane(client *wsClient, msg *protocol.WorkspaceClosePaneMessage) {
	if msg.PaneID == workspace.MainPaneID {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("main pane cannot be closed"))
		return
	}

	snapshot, err := d.ensureWorkspaceSnapshot(msg.SessionID)
	if err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}

	runtimeID := ""
	nextPanes := make([]workspace.Pane, 0, len(snapshot.Panes))
	found := false
	for _, pane := range snapshot.Panes {
		if pane.PaneID == msg.PaneID {
			runtimeID = pane.RuntimeID
			found = true
			continue
		}
		nextPanes = append(nextPanes, pane)
	}
	if !found {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}

	layout, _ := workspace.Remove(snapshot.Layout, msg.PaneID)
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspace.NormalizeSnapshot(*snapshot, msg.SessionID)
	if err := d.store.SaveWorkspace(normalized); err != nil {
		d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceUpdated(msg.SessionID)
	d.sendWorkspaceActionResult(client, protocol.CmdWorkspaceClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)

	if strings.TrimSpace(runtimeID) != "" {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) killWorkspaceRuntimesForSession(sessionID string) {
	snapshot, err := d.ensureWorkspaceSnapshot(sessionID)
	if err != nil {
		return
	}
	for _, runtimeID := range workspace.SortedShellRuntimeIDs(*snapshot) {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) handleWorkspaceRuntimeExit(runtimeID string, exitCode int, signal string) bool {
	sessionID, paneID, ok := d.store.FindWorkspacePaneByRuntimeID(runtimeID)
	if !ok {
		return false
	}

	snapshot, err := d.ensureWorkspaceSnapshot(sessionID)
	if err != nil {
		d.logf("workspace runtime exit reconcile failed for runtime %s: %v", runtimeID, err)
		return false
	}

	layout, _ := workspace.Remove(snapshot.Layout, paneID)
	nextPanes := make([]workspace.Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			nextPanes = append(nextPanes, pane)
		}
	}
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspace.NormalizeSnapshot(*snapshot, sessionID)
	if err := d.store.SaveWorkspace(normalized); err != nil {
		d.logf("workspace runtime exit save failed for runtime %s: %v", runtimeID, err)
		return false
	}

	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceRuntimeExited,
		SessionID: protocol.Ptr(sessionID),
		PaneID:    protocol.Ptr(paneID),
		RuntimeID: protocol.Ptr(runtimeID),
		ExitCode:  protocol.Ptr(exitCode),
		Signal:    optionalStringPtr(signal),
	})
	d.broadcastWorkspaceUpdated(sessionID)
	return true
}

func optionalStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return protocol.Ptr(value)
}

func (d *Daemon) reconcileWorkspacesWithPTYBackend(ctx context.Context) {
	if d.store == nil {
		return
	}

	liveIDs := make(map[string]struct{})
	if d.ptyBackend != nil {
		for _, id := range d.ptyBackend.SessionIDs(ctx) {
			liveIDs[id] = struct{}{}
		}
	}

	referencedRuntimeIDs := make(map[string]struct{})
	for _, session := range d.store.List("") {
		snapshot, err := d.ensureWorkspaceSnapshot(session.ID)
		if err != nil {
			d.logf("workspace ensure failed for session %s: %v", session.ID, err)
			continue
		}

		nextPanes := make([]workspace.Pane, 0, len(snapshot.Panes))
		changed := false
		for _, pane := range snapshot.Panes {
			if pane.Kind != workspace.PaneKindShell {
				nextPanes = append(nextPanes, pane)
				continue
			}
			if _, ok := liveIDs[pane.RuntimeID]; !ok {
				changed = true
				continue
			}
			referencedRuntimeIDs[pane.RuntimeID] = struct{}{}
			nextPanes = append(nextPanes, pane)
		}

		if changed {
			snapshot.Panes = nextPanes
			normalized := workspace.NormalizeSnapshot(*snapshot, session.ID)
			if err := d.store.SaveWorkspace(normalized); err != nil {
				d.logf("workspace reconcile save failed for session %s: %v", session.ID, err)
			}
			continue
		}
		for _, pane := range snapshot.Panes {
			if pane.Kind == workspace.PaneKindShell && pane.RuntimeID != "" {
				referencedRuntimeIDs[pane.RuntimeID] = struct{}{}
			}
		}
	}

	for runtimeID := range liveIDs {
		if d.store.Get(runtimeID) != nil {
			continue
		}
		if _, ok := referencedRuntimeIDs[runtimeID]; ok {
			continue
		}
		if err := d.removePTYSession(runtimeID); err != nil {
			d.logf("workspace reconcile prune failed for runtime %s: %v", runtimeID, err)
		}
	}
}
