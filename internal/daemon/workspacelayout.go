package daemon

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func (d *Daemon) ensureWorkspaceLayout(workspaceID string) (*workspacelayout.WorkspaceLayout, error) {
	if d.store.GetWorkspace(workspaceID) == nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	current := d.store.GetWorkspaceLayout(workspaceID)
	if current == nil {
		return nil, fmt.Errorf("workspace has no layout: %s", workspaceID)
	}

	normalized := workspacelayout.NormalizeWorkspaceLayout(*current)
	if len(normalized.Panes) == 0 {
		d.store.RemoveWorkspaceLayout(workspaceID)
		return nil, fmt.Errorf("workspace has no layout panes: %s", workspaceID)
	}
	return &normalized, nil
}

func (d *Daemon) currentOrEmptyWorkspaceLayout(workspaceID string) (*workspacelayout.WorkspaceLayout, error) {
	if d.store.GetWorkspace(workspaceID) == nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	current := d.store.GetWorkspaceLayout(workspaceID)
	if current == nil {
		return &workspacelayout.WorkspaceLayout{WorkspaceID: workspaceID}, nil
	}
	normalized := workspacelayout.NormalizeWorkspaceLayout(*current)
	return &normalized, nil
}

func (d *Daemon) setWorkspacePaneStatusForSession(sessionID string, status workspacelayout.PaneStatus, errMsg string) bool {
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok {
		return false
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return false
	}
	changed := false
	for i := range snapshot.Panes {
		if snapshot.Panes[i].PaneID != paneID {
			continue
		}
		if snapshot.Panes[i].Status != status || snapshot.Panes[i].Error != errMsg {
			snapshot.Panes[i].Status = status
			snapshot.Panes[i].Error = errMsg
			changed = true
		}
	}
	if !changed {
		return false
	}
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.logf("workspace pane status update failed for session %s: %v", sessionID, err)
		return false
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return true
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
			Status: protocol.WorkspaceLayoutPaneStatus(pane.Status),
		}
		if next.Status == "" {
			next.Status = protocol.WorkspaceLayoutPaneStatusReady
		}
		if strings.TrimSpace(pane.RuntimeID) != "" {
			next.RuntimeID = protocol.Ptr(strings.TrimSpace(pane.RuntimeID))
		}
		if strings.TrimSpace(pane.SessionID) != "" {
			next.SessionID = protocol.Ptr(strings.TrimSpace(pane.SessionID))
		}
		if strings.TrimSpace(pane.Error) != "" {
			next.Error = protocol.Ptr(strings.TrimSpace(pane.Error))
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
		targetPaneID = firstWorkspaceLayoutPaneID(*snapshot)
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

func firstWorkspaceLayoutPaneID(snapshot workspacelayout.WorkspaceLayout) string {
	for _, pane := range snapshot.Panes {
		if strings.TrimSpace(pane.PaneID) != "" {
			return pane.PaneID
		}
	}
	return ""
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

func (d *Daemon) handleWorkspaceLayoutSetSplitRatio(client *wsClient, msg *protocol.WorkspaceLayoutSetSplitRatioMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSetSplitRatio, msg.WorkspaceID, nil, err)
		return
	}
	splitID := strings.TrimSpace(msg.SplitID)
	if splitID == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSetSplitRatio, msg.WorkspaceID, nil, fmt.Errorf("split_id is required"))
		return
	}
	layout, ok := workspacelayout.SetSplitRatio(snapshot.Layout, splitID, msg.Ratio)
	if !ok {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSetSplitRatio, msg.WorkspaceID, nil, fmt.Errorf("split not found: %s", splitID))
		return
	}
	snapshot.Layout = layout
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSetSplitRatio, msg.WorkspaceID, nil, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSetSplitRatio, msg.WorkspaceID, nil, nil)
}

// defaultPanelFraction is the share of the split a freshly docked panel takes
// when the client doesn't specify one. Roughly a third keeps the panel readable
// without crowding the terminals.
const defaultPanelFraction = 0.32

// dockEdgeToSplit translates a dock edge into a split direction and whether the
// panel sits before (children[0]) the anchor.
func dockEdgeToSplit(edge protocol.WorkspaceLayoutDockEdge) (workspacelayout.Direction, bool) {
	switch edge {
	case protocol.WorkspaceLayoutDockEdgeLeft:
		return workspacelayout.DirectionVertical, true
	case protocol.WorkspaceLayoutDockEdgeTop:
		return workspacelayout.DirectionHorizontal, true
	case protocol.WorkspaceLayoutDockEdgeBottom:
		return workspacelayout.DirectionHorizontal, false
	default: // right
		return workspacelayout.DirectionVertical, false
	}
}

func (d *Daemon) handleWorkspaceLayoutDockPanel(client *wsClient, msg *protocol.WorkspaceLayoutDockPanelMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, err)
		return
	}
	panelID := strings.TrimSpace(msg.PanelID)
	panelKind := strings.TrimSpace(msg.PanelKind)
	anchorPaneID := strings.TrimSpace(msg.AnchorPaneID)
	if panelID == "" || panelKind == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, fmt.Errorf("panel_id and panel_kind are required"))
		return
	}
	// Fall back to the active pane when the client doesn't name an anchor (e.g.
	// the sidebar toggle just wants the panel somewhere sensible).
	if anchorPaneID == "" {
		anchorPaneID = snapshot.ActivePaneID
	}
	if !workspacelayout.HasPane(snapshot.Layout, anchorPaneID) {
		anchorPaneID = firstWorkspaceLayoutPaneID(*snapshot)
	}
	if anchorPaneID == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, fmt.Errorf("workspace has no anchor pane"))
		return
	}

	direction, before := dockEdgeToSplit(msg.Edge)
	panelFraction := defaultPanelFraction
	if msg.Ratio != nil && *msg.Ratio > 0 && *msg.Ratio < 1 {
		panelFraction = *msg.Ratio
	}
	// DockPanel takes the children[0] fraction; convert from the panel's share.
	childZeroRatio := panelFraction
	if !before {
		childZeroRatio = 1 - panelFraction
	}

	layout, ok := workspacelayout.DockPanel(
		snapshot.Layout,
		anchorPaneID,
		direction,
		before,
		newWorkspaceLayoutEntityID("split"),
		panelID,
		panelKind,
		childZeroRatio,
	)
	if !ok {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, fmt.Errorf("could not dock panel against pane: %s", anchorPaneID))
		return
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutDockPanel, msg.WorkspaceID, nil, nil)
}

func (d *Daemon) handleWorkspaceLayoutUndockPanel(client *wsClient, msg *protocol.WorkspaceLayoutUndockPanelMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutUndockPanel, msg.WorkspaceID, nil, err)
		return
	}
	panelID := strings.TrimSpace(msg.PanelID)
	if panelID == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutUndockPanel, msg.WorkspaceID, nil, fmt.Errorf("panel_id is required"))
		return
	}
	layout, ok := workspacelayout.UndockPanel(snapshot.Layout, panelID)
	if !ok {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutUndockPanel, msg.WorkspaceID, nil, fmt.Errorf("panel not found: %s", panelID))
		return
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutUndockPanel, msg.WorkspaceID, nil, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutUndockPanel, msg.WorkspaceID, nil, nil)
}

func (d *Daemon) handleWorkspaceLayoutAddSessionPane(client *wsClient, msg *protocol.WorkspaceLayoutAddSessionPaneMessage) {
	snapshot, err := d.currentOrEmptyWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, nil, err)
		return
	}

	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, nil, fmt.Errorf("session_id is required"))
		return
	}
	for _, pane := range snapshot.Panes {
		if pane.SessionID == sessionID {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(pane.PaneID), nil)
			return
		}
	}

	paneID := strings.TrimSpace(protocol.Deref(msg.PaneID))
	if paneID == "" {
		paneID = newWorkspaceLayoutEntityID("pane")
	}
	for _, pane := range snapshot.Panes {
		if pane.PaneID == paneID {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(paneID), fmt.Errorf("pane already exists: %s", paneID))
			return
		}
	}
	if workspacelayout.HasPane(snapshot.Layout, paneID) {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(paneID), fmt.Errorf("pane already exists: %s", paneID))
		return
	}
	title := strings.TrimSpace(protocol.Deref(msg.Title))
	if title == "" {
		title = workspacelayout.DefaultPaneTitle
	}
	nextPane := workspacelayout.Pane{
		PaneID:    paneID,
		RuntimeID: sessionID,
		SessionID: sessionID,
		Kind:      workspacelayout.PaneKindAgent,
		Title:     title,
		Status:    workspacelayout.PaneStatusSpawning,
	}

	targetPaneID := strings.TrimSpace(protocol.Deref(msg.TargetPaneID))
	if len(snapshot.Panes) == 0 || snapshot.Layout.Type == "" {
		if targetPaneID != "" {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(targetPaneID), fmt.Errorf("cannot target pane in empty layout"))
			return
		}
		snapshot.Layout = workspacelayout.DefaultLayout(paneID)
	} else {
		if targetPaneID == "" {
			targetPaneID = snapshot.ActivePaneID
		}
		if !workspacelayout.HasPane(snapshot.Layout, targetPaneID) {
			targetPaneID = firstWorkspaceLayoutPaneID(*snapshot)
		}
		if targetPaneID == "" {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, nil, fmt.Errorf("workspace has no target pane"))
			return
		}
		layout, changed := workspacelayout.Split(
			snapshot.Layout,
			targetPaneID,
			paneID,
			newWorkspaceLayoutEntityID("split"),
			protocolDirection(protocol.Deref(msg.Direction)),
			workspacelayout.DefaultSplitRatio,
		)
		if !changed {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(targetPaneID), fmt.Errorf("pane not found: %s", targetPaneID))
			return
		}
		snapshot.Layout = layout
	}

	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, nextPane)
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(paneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(paneID), nil)
}

func (d *Daemon) handleWorkspaceLayoutClosePane(client *wsClient, msg *protocol.WorkspaceLayoutClosePaneMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}

	sessionID := ""
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
	found := false
	for _, pane := range snapshot.Panes {
		if pane.PaneID == msg.PaneID {
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
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)

	if strings.TrimSpace(sessionID) != "" {
		if session := d.unregisterSession(sessionID, syscall.SIGTERM); session != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionUnregistered,
				Session: d.sessionForBroadcast(session),
			})
			d.dissociateSessionFromWorkspace(session.ID)
		}
	}

	if len(normalized.Panes) == 0 {
		d.store.RemoveWorkspaceLayout(msg.WorkspaceID)
	} else {
		if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
			return
		}
	}
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)

	if len(normalized.Panes) > 0 {
		d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	}
}

func (d *Daemon) removeWorkspaceLayoutPaneForSession(sessionID string) {
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok || paneID == "" {
		return
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
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
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if len(normalized.Panes) == 0 {
		d.store.RemoveWorkspaceLayout(workspaceID)
	} else {
		if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
			d.logf("workspace layout session unregister save failed for session %s: %v", sessionID, err)
			return
		}
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
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

	for _, workspace := range d.workspaces.list() {
		snapshot, err := d.ensureWorkspaceLayout(workspace.ID)
		if err != nil {
			d.logf("workspace layout ensure failed for session %s: %v", workspace.ID, err)
			continue
		}

		nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
		changed := false
		for _, pane := range snapshot.Panes {
			if pane.Kind == workspacelayout.PaneKindAgent && strings.TrimSpace(pane.SessionID) != "" {
				nextPanes = append(nextPanes, pane)
				continue
			}
			changed = true
		}

		if changed {
			snapshot.Panes = nextPanes
			normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
			if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
				d.logf("workspace layout reconcile save failed for session %s: %v", workspace.ID, err)
			}
			continue
		}
	}

	for runtimeID := range liveIDs {
		if d.store.Get(runtimeID) != nil {
			continue
		}
		if err := d.removePTYSession(runtimeID); err != nil {
			d.logf("workspace layout reconcile prune failed for runtime %s: %v", runtimeID, err)
		}
	}
}
