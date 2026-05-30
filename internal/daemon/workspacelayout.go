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
	"github.com/victorarias/attn/internal/workspacelayout"
)

var shellTitlePattern = regexp.MustCompile(`^Shell (\d+)$`)

func (d *Daemon) ensureWorkspaceLayout(workspaceID string) (*workspacelayout.WorkspaceLayout, error) {
	if d.store.GetWorkspace(workspaceID) == nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	memberIDs := d.store.SessionsInWorkspace(workspaceID)
	if len(memberIDs) == 0 {
		return nil, fmt.Errorf("workspace has no agent session: %s", workspaceID)
	}
	primarySessionID := memberIDs[0]

	current := d.store.GetWorkspaceLayout(workspaceID)
	if current == nil {
		snapshot := workspacelayout.DefaultWorkspaceLayout(workspaceID, primarySessionID)
		if err := d.store.SaveWorkspaceLayout(snapshot); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}

	normalized := workspacelayout.NormalizeWorkspaceLayout(*current, primarySessionID)
	if !reflect.DeepEqual(*current, normalized) {
		if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
			return nil, err
		}
	}
	return &normalized, nil
}

func (d *Daemon) protocolWorkspaceLayout(workspaceID string) (*protocol.WorkspaceLayout, error) {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return nil, err
	}
	return protocolWorkspaceLayout(*snapshot)
}

func protocolWorkspaceLayout(snapshot workspacelayout.WorkspaceLayout) (*protocol.WorkspaceLayout, error) {
	layoutJSON, err := workspacelayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		return nil, err
	}
	panes := make([]protocol.WorkspaceLayoutPane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		next := protocol.WorkspaceLayoutPane{
			PaneID: pane.PaneID,
			Kind:   protocol.WorkspaceLayoutPaneKind(pane.Kind),
			Title:  pane.Title,
		}
		if strings.TrimSpace(pane.RuntimeID) != "" {
			next.RuntimeID = protocol.Ptr(strings.TrimSpace(pane.RuntimeID))
		}
		if strings.TrimSpace(pane.SessionID) != "" {
			next.SessionID = protocol.Ptr(strings.TrimSpace(pane.SessionID))
		}
		panes = append(panes, next)
	}
	layout := &protocol.WorkspaceLayout{
		WorkspaceID:  snapshot.WorkspaceID,
		ActivePaneID: snapshot.ActivePaneID,
		LayoutJson:   layoutJSON,
		Panes:        panes,
	}
	if strings.TrimSpace(snapshot.UpdatedAt) != "" {
		layout.UpdatedAt = protocol.Ptr(snapshot.UpdatedAt)
	}
	return layout, nil
}

func (d *Daemon) sendWorkspaceLayout(client *wsClient, workspaceID string) {
	snapshot, err := d.protocolWorkspaceLayout(workspaceID)
	if err != nil {
		d.sendCommandError(client, protocol.CmdWorkspaceLayoutGet, err.Error())
		return
	}
	d.sendToClient(client, protocol.WorkspaceLayoutMessage{
		Event:           protocol.EventWorkspaceLayout,
		WorkspaceLayout: *snapshot,
	})
}

func (d *Daemon) sendWorkspaceLayoutActionResult(client *wsClient, action, workspaceID string, paneID *string, err error) {
	result := protocol.WorkspaceLayoutActionResultMessage{
		Event:       protocol.EventWorkspaceLayoutActionResult,
		Action:      action,
		WorkspaceID: workspaceID,
		Success:     err == nil,
	}
	if paneID != nil && strings.TrimSpace(*paneID) != "" {
		result.PaneID = protocol.Ptr(strings.TrimSpace(*paneID))
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) broadcastWorkspaceLayoutUpdated(workspaceID string) {
	snapshot, err := d.protocolWorkspaceLayout(workspaceID)
	if err != nil {
		d.logf("workspace layout update failed for workspace %s: %v", workspaceID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:           protocol.EventWorkspaceLayoutUpdated,
		WorkspaceLayout: snapshot,
	})
}

func (d *Daemon) broadcastWorkspaceLayout(workspaceID string) {
	snapshot, err := d.protocolWorkspaceLayout(workspaceID)
	if err != nil {
		d.logf("workspace layout snapshot failed for workspace %s: %v", workspaceID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:           protocol.EventWorkspaceLayout,
		WorkspaceLayout: snapshot,
	})
}

func protocolDirection(direction protocol.WorkspaceLayoutSplitDirection) workspacelayout.Direction {
	switch direction {
	case protocol.WorkspaceLayoutSplitDirectionHorizontal:
		return workspacelayout.DirectionHorizontal
	default:
		return workspacelayout.DirectionVertical
	}
}

func nextShellTitle(snapshot workspacelayout.WorkspaceLayout) string {
	maxShell := 0
	for _, pane := range snapshot.Panes {
		if pane.Kind != workspacelayout.PaneKindShell {
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

func newWorkspaceLayoutEntityID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (d *Daemon) ensureAgentPaneInWorkspace(workspaceID, sessionID, title string) error {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return err
	}
	for _, pane := range snapshot.Panes {
		if pane.Kind == workspacelayout.PaneKindAgent && pane.SessionID == sessionID {
			return nil
		}
	}

	paneID := newWorkspaceLayoutEntityID("agent")
	splitID := newWorkspaceLayoutEntityID("split")
	targetPaneID := snapshot.ActivePaneID
	if !workspacelayout.HasPane(snapshot.Layout, targetPaneID) {
		targetPaneID = workspacelayout.MainPaneID
	}
	layout, changed := workspacelayout.Split(
		snapshot.Layout,
		targetPaneID,
		paneID,
		splitID,
		workspacelayout.DirectionVertical,
		workspacelayout.DefaultSplitRatio,
	)
	if !changed {
		return fmt.Errorf("active pane not found: %s", targetPaneID)
	}
	if strings.TrimSpace(title) == "" {
		title = workspacelayout.DefaultPaneTitle
	}
	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, workspacelayout.Pane{
		PaneID:    paneID,
		RuntimeID: sessionID,
		SessionID: sessionID,
		Kind:      workspacelayout.PaneKindAgent,
		Title:     title,
	})
	return d.store.SaveWorkspaceLayout(*snapshot)
}

func (d *Daemon) handleWorkspaceLayoutGet(client *wsClient, msg *protocol.WorkspaceLayoutGetMessage) {
	d.sendWorkspaceLayout(client, msg.WorkspaceID)
}

func (d *Daemon) handleWorkspaceLayoutFocusPane(client *wsClient, msg *protocol.WorkspaceLayoutFocusPaneMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	if !workspacelayout.HasPane(snapshot.Layout, msg.PaneID) {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if snapshot.ActivePaneID == msg.PaneID {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
		return
	}
	snapshot.ActivePaneID = msg.PaneID
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleWorkspaceLayoutRenamePane(client *wsClient, msg *protocol.WorkspaceLayoutRenamePaneMessage) {
	if msg.PaneID == workspacelayout.MainPaneID {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("agent pane title is fixed"))
		return
	}

	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("title cannot be empty"))
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
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutRenamePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleWorkspaceLayoutSplitPane(client *wsClient, msg *protocol.WorkspaceLayoutSplitPaneMessage) {
	if d.ptyBackend == nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pty backend unavailable"))
		return
	}
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	workspace := d.store.GetWorkspace(msg.WorkspaceID)
	if workspace == nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("workspace not found: %s", msg.WorkspaceID))
		return
	}
	if !workspacelayout.HasPane(snapshot.Layout, msg.TargetPaneID) {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	paneID := newWorkspaceLayoutEntityID("pane")
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	runtimeID := sessionID
	if runtimeID == "" {
		runtimeID = newWorkspaceLayoutEntityID("runtime")
	}
	splitID := newWorkspaceLayoutEntityID("split")
	title := strings.TrimSpace(protocol.Deref(msg.Title))
	if title == "" {
		title = nextShellTitle(*snapshot)
	}

	if sessionID == "" {
		if err := d.ptyBackend.Spawn(context.Background(), ptySpawnShellOptions(runtimeID, workspace.Directory, title)); err != nil {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), err)
			return
		}
	}

	layout, changed := workspacelayout.Split(
		snapshot.Layout,
		msg.TargetPaneID,
		paneID,
		splitID,
		protocolDirection(msg.Direction),
		workspacelayout.DefaultSplitRatio,
	)
	if !changed {
		_ = d.removePTYSession(runtimeID)
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	pane := workspacelayout.Pane{
		PaneID:    paneID,
		RuntimeID: runtimeID,
		Kind:      workspacelayout.PaneKindShell,
		Title:     title,
	}
	if sessionID != "" {
		pane.SessionID = sessionID
		pane.Kind = workspacelayout.PaneKindAgent
	}
	snapshot.Panes = append(snapshot.Panes, pane)
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0].SessionID)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		_ = d.removePTYSession(runtimeID)
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), nil)
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

func (d *Daemon) handleWorkspaceLayoutClosePane(client *wsClient, msg *protocol.WorkspaceLayoutClosePaneMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}

	if msg.PaneID == workspacelayout.MainPaneID {
		for _, pane := range snapshot.Panes {
			if pane.PaneID != workspacelayout.MainPaneID || strings.TrimSpace(pane.SessionID) == "" {
				continue
			}
			if session := d.unregisterSession(pane.SessionID, syscall.SIGTERM); session != nil {
				d.removeWorkspaceLayoutPaneForSession(session.ID)
				d.wsHub.Broadcast(&protocol.WebSocketEvent{
					Event:   protocol.EventSessionUnregistered,
					Session: d.sessionForBroadcast(session),
				})
				d.dissociateSessionFromWorkspace(session.ID)
			}
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
			return
		}
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}

	runtimeID := ""
	sessionID := ""
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
	found := false
	for _, pane := range snapshot.Panes {
		if pane.PaneID == msg.PaneID {
			runtimeID = pane.RuntimeID
			sessionID = pane.SessionID
			found = true
			continue
		}
		nextPanes = append(nextPanes, pane)
	}
	if !found {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, msg.PaneID)
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0].SessionID)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)

	if strings.TrimSpace(sessionID) != "" {
		if session := d.unregisterSession(sessionID, syscall.SIGTERM); session != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionUnregistered,
				Session: d.sessionForBroadcast(session),
			})
			d.dissociateSessionFromWorkspace(session.ID)
		}
		return
	}
	if strings.TrimSpace(runtimeID) != "" {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) removeWorkspaceLayoutPaneForSession(sessionID string) {
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok || paneID == "" {
		return
	}
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		d.logf("workspace layout session unregister reconcile failed for session %s: %v", sessionID, err)
		return
	}

	if paneID == workspacelayout.MainPaneID {
		d.promoteWorkspaceLayoutPaneForClosedMainSession(workspaceID, sessionID, snapshot)
		return
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, paneID)
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			nextPanes = append(nextPanes, pane)
		}
	}
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0].SessionID)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.logf("workspace layout session unregister save failed for session %s: %v", sessionID, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
}

func (d *Daemon) promoteWorkspaceLayoutPaneForClosedMainSession(workspaceID, closedSessionID string, snapshot *workspacelayout.WorkspaceLayout) {
	var replacement workspacelayout.Pane
	foundReplacement := false
	for _, pane := range snapshot.Panes {
		if pane.PaneID == workspacelayout.MainPaneID {
			continue
		}
		if pane.Kind != workspacelayout.PaneKindAgent || strings.TrimSpace(pane.SessionID) == "" {
			continue
		}
		replacement = pane
		foundReplacement = true
		break
	}
	if !foundReplacement {
		return
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, replacement.PaneID)
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
	nextPanes = append(nextPanes, workspacelayout.Pane{
		PaneID:    workspacelayout.MainPaneID,
		RuntimeID: replacement.RuntimeID,
		SessionID: replacement.SessionID,
		Kind:      workspacelayout.PaneKindAgent,
		Title:     replacement.Title,
	})
	for _, pane := range snapshot.Panes {
		if pane.PaneID == workspacelayout.MainPaneID || pane.PaneID == replacement.PaneID || pane.SessionID == closedSessionID {
			continue
		}
		nextPanes = append(nextPanes, pane)
	}
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	if snapshot.ActivePaneID == replacement.PaneID || snapshot.ActivePaneID == workspacelayout.MainPaneID {
		snapshot.ActivePaneID = workspacelayout.MainPaneID
	}
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, replacement.SessionID)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.logf("workspace layout main session promotion save failed for session %s: %v", closedSessionID, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
}

func (d *Daemon) killWorkspaceLayoutRuntimes(workspaceID string) {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return
	}
	for _, runtimeID := range workspacelayout.SortedShellRuntimeIDs(*snapshot) {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) handleWorkspaceLayoutRuntimeExit(runtimeID string, exitCode int, signal string) bool {
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneByRuntimeID(runtimeID)
	if !ok {
		return false
	}

	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		d.logf("workspace layout runtime exit reconcile failed for runtime %s: %v", runtimeID, err)
		return false
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, paneID)
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			nextPanes = append(nextPanes, pane)
		}
	}
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0].SessionID)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.logf("workspace layout runtime exit save failed for runtime %s: %v", runtimeID, err)
		return false
	}

	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:       protocol.EventWorkspaceLayoutRuntimeExited,
		WorkspaceID: protocol.Ptr(workspaceID),
		PaneID:      protocol.Ptr(paneID),
		RuntimeID:   protocol.Ptr(runtimeID),
		ExitCode:    protocol.Ptr(exitCode),
		Signal:      optionalStringPtr(signal),
	})
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return true
}

func optionalStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return protocol.Ptr(value)
}

func (d *Daemon) reconcileWorkspaceLayoutsWithPTYBackend(ctx context.Context) {
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
	for _, workspace := range d.workspaces.list() {
		snapshot, err := d.ensureWorkspaceLayout(workspace.ID)
		if err != nil {
			d.logf("workspace layout ensure failed for session %s: %v", workspace.ID, err)
			continue
		}

		nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
		changed := false
		for _, pane := range snapshot.Panes {
			if pane.Kind != workspacelayout.PaneKindShell {
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
			normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0].SessionID)
			if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
				d.logf("workspace layout reconcile save failed for session %s: %v", workspace.ID, err)
			}
			continue
		}
		for _, pane := range snapshot.Panes {
			if pane.Kind == workspacelayout.PaneKindShell && pane.RuntimeID != "" {
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
			d.logf("workspace layout reconcile prune failed for runtime %s: %v", runtimeID, err)
		}
	}
}
