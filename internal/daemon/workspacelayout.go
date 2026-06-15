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
	if workspacelayout.LayoutEmpty(normalized.Layout) {
		d.store.RemoveWorkspaceLayout(workspaceID)
		return nil, fmt.Errorf("workspace has no layout leaves: %s", workspaceID)
	}
	return &normalized, nil
}

// workspaceLayoutHasTiles reports whether a workspace's stored layout still
// holds at least one docked tile. It decides whether a workspace outlives its
// last session: a tile the user left behind keeps the workspace alive.
func (d *Daemon) workspaceLayoutHasTiles(workspaceID string) bool {
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return false
	}
	return len(workspacelayout.TileIDs(snapshot.Layout)) > 0
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

func (d *Daemon) sendWorkspaceLayoutSplitActionResult(client *wsClient, workspaceID, splitID string, requestID *string, err error) {
	result := protocol.WorkspaceLayoutActionResultMessage{
		Event:       protocol.EventWorkspaceLayoutActionResult,
		Action:      protocol.CmdWorkspaceLayoutSetSplitRatio,
		WorkspaceID: workspaceID,
		SplitID:     protocol.Ptr(strings.TrimSpace(splitID)),
		RequestID:   requestID,
		Success:     err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) sendWorkspaceLayoutTileActionResult(client *wsClient, action, workspaceID, tileID string, err error) {
	d.sendWorkspaceLayoutTileActionResultWithRequest(client, action, workspaceID, tileID, nil, err)
}

func (d *Daemon) sendWorkspaceLayoutTileActionResultWithRequest(
	client *wsClient,
	action, workspaceID, tileID string,
	requestID *string,
	err error,
) {
	result := protocol.WorkspaceLayoutActionResultMessage{
		Event:       protocol.EventWorkspaceLayoutActionResult,
		Action:      action,
		WorkspaceID: workspaceID,
		TileID:      protocol.Ptr(strings.TrimSpace(tileID)),
		RequestID:   requestID,
		Success:     err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) sendWorkspaceLayoutMoveToWorkspaceResult(client *wsClient, sourceWorkspaceID, targetWorkspaceID, leafID, finalLeafID string, err error) {
	result := protocol.WorkspaceLayoutActionResultMessage{
		Event:             protocol.EventWorkspaceLayoutActionResult,
		Action:            protocol.CmdWorkspaceLayoutMoveLeafToWorkspace,
		WorkspaceID:       sourceWorkspaceID,
		SourceWorkspaceID: protocol.Ptr(strings.TrimSpace(sourceWorkspaceID)),
		TargetWorkspaceID: protocol.Ptr(strings.TrimSpace(targetWorkspaceID)),
		LeafID:            protocol.Ptr(strings.TrimSpace(leafID)),
		FinalLeafID:       protocol.Ptr(strings.TrimSpace(finalLeafID)),
		Success:           err == nil,
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
	d.broadcastWorkspaceLayoutSnapshotUpdated(snapshot)
}

func (d *Daemon) broadcastWorkspaceLayoutSnapshotUpdated(snapshot *protocol.WorkspaceLayout) {
	if snapshot == nil {
		return
	}
	workspaceID := snapshot.WorkspaceID
	d.pruneTileContentSubscriptionsForWorkspace(workspaceID)
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
	d.pruneTileContentSubscriptionsForWorkspace(workspaceID)
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

func workspaceLayoutHasLeaf(layout workspacelayout.Node, leafID string) bool {
	if strings.TrimSpace(leafID) == "" {
		return false
	}
	for _, paneID := range workspacelayout.PaneIDs(layout) {
		if paneID == leafID {
			return true
		}
	}
	for _, tileID := range workspacelayout.TileIDs(layout) {
		if tileID == leafID {
			return true
		}
	}
	return false
}

func firstWorkspaceLayoutPaneID(snapshot workspacelayout.WorkspaceLayout) string {
	if workspaceLayoutHasLeaf(snapshot.Layout, snapshot.ActivePaneID) {
		return snapshot.ActivePaneID
	}
	for _, pane := range snapshot.Panes {
		if strings.TrimSpace(pane.PaneID) != "" {
			return pane.PaneID
		}
	}
	if tileIDs := workspacelayout.TileIDs(snapshot.Layout); len(tileIDs) > 0 {
		return tileIDs[0]
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
		d.sendWorkspaceLayoutSplitActionResult(client, msg.WorkspaceID, msg.SplitID, msg.RequestID, err)
		return
	}
	splitID := strings.TrimSpace(msg.SplitID)
	if splitID == "" {
		d.sendWorkspaceLayoutSplitActionResult(client, msg.WorkspaceID, splitID, msg.RequestID, fmt.Errorf("split_id is required"))
		return
	}
	layout, ok := workspacelayout.SetSplitRatio(snapshot.Layout, splitID, msg.Ratio)
	if !ok {
		d.sendWorkspaceLayoutSplitActionResult(client, msg.WorkspaceID, splitID, msg.RequestID, fmt.Errorf("split not found: %s", splitID))
		return
	}
	snapshot.Layout = layout
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.sendWorkspaceLayoutSplitActionResult(client, msg.WorkspaceID, splitID, msg.RequestID, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutSplitActionResult(client, msg.WorkspaceID, splitID, msg.RequestID, nil)
}

// defaultTileFraction is the share of the split a freshly docked tile takes
// when the client doesn't specify one. Roughly a third keeps the tile readable
// without crowding the terminals.
const defaultTileFraction = 0.32

// dockEdgeToSplit translates a dock edge into a split direction and whether the
// tile sits before (children[0]) the anchor.
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

func (d *Daemon) handleWorkspaceLayoutDockTile(client *wsClient, msg *protocol.WorkspaceLayoutDockTileMessage) {
	params := ""
	if snapshot := d.store.GetWorkspaceLayout(msg.WorkspaceID); snapshot != nil {
		params, _ = workspacelayout.TileParamsByID(snapshot.Layout, strings.TrimSpace(msg.TileID))
	}
	err := d.dockTile(msg.WorkspaceID, msg.AnchorPaneID, msg.TileID, msg.TileKind, params, msg.Edge, msg.Ratio)
	d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutDockTile, msg.WorkspaceID, msg.TileID, err)
}

// dockTile docks (or moves) a tile into a workspace layout and persists it.
// It is shared by the websocket dock command and the `attn open` unix command.
// anchorPaneID may be empty — it falls back to the active leaf, then the first
// pane or tile. tileParams is opaque layout data (the markdown file path, for markdown).
func (d *Daemon) dockTile(workspaceID, anchorPaneID, tileID, tileKind, tileParams string, edge protocol.WorkspaceLayoutDockEdge, ratio *float64) error {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return err
	}
	tileID = strings.TrimSpace(tileID)
	tileKind = strings.TrimSpace(tileKind)
	anchorPaneID = strings.TrimSpace(anchorPaneID)
	if tileID == "" || tileKind == "" {
		return fmt.Errorf("tile_id and tile_kind are required")
	}
	if workspacelayout.HasPane(snapshot.Layout, tileID) {
		return fmt.Errorf("pane already exists: %s", tileID)
	}
	// Fall back to the active pane when the caller doesn't name an anchor.
	if anchorPaneID == "" {
		anchorPaneID = snapshot.ActivePaneID
	}
	if !workspaceLayoutHasLeaf(snapshot.Layout, anchorPaneID) {
		anchorPaneID = firstWorkspaceLayoutPaneID(*snapshot)
	}
	if anchorPaneID == "" {
		return fmt.Errorf("workspace has no anchor pane")
	}

	direction, before := dockEdgeToSplit(edge)
	tileFraction := defaultTileFraction
	if existingFraction, ok := workspacelayout.TileFractionByID(snapshot.Layout, tileID); ok && existingFraction > 0 && existingFraction < 1 {
		tileFraction = existingFraction
	}
	if ratio != nil && *ratio > 0 && *ratio < 1 {
		tileFraction = *ratio
	}
	// DockTile takes the children[0] fraction; convert from the tile's share.
	childZeroRatio := tileFraction
	if !before {
		childZeroRatio = 1 - tileFraction
	}

	layout, ok := workspacelayout.DockTile(
		snapshot.Layout,
		anchorPaneID,
		direction,
		before,
		newWorkspaceLayoutEntityID("split"),
		tileID,
		tileKind,
		strings.TrimSpace(tileParams),
		childZeroRatio,
	)
	if !ok {
		return fmt.Errorf("could not dock tile against pane: %s", anchorPaneID)
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		return err
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return nil
}

func (d *Daemon) handleWorkspaceLayoutUndockTile(client *wsClient, msg *protocol.WorkspaceLayoutUndockTileMessage) {
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, msg.TileID, err)
		return
	}
	tileID := strings.TrimSpace(msg.TileID)
	if tileID == "" {
		d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, tileID, fmt.Errorf("tile_id is required"))
		return
	}
	layout, ok := workspacelayout.UndockTile(snapshot.Layout, tileID)
	if !ok {
		d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, tileID, fmt.Errorf("tile not found: %s", tileID))
		return
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, tileID, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, tileID, nil)
}

func (d *Daemon) handleWorkspaceLayoutUpdateTile(client *wsClient, msg *protocol.WorkspaceLayoutUpdateTileMessage) {
	requestID := protocol.Ptr(strings.TrimSpace(msg.RequestID))
	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, msg.TileID, requestID, err)
		return
	}
	tileID := strings.TrimSpace(msg.TileID)
	tileParams := strings.TrimSpace(msg.TileParams)
	if tileID == "" || tileParams == "" {
		d.sendWorkspaceLayoutTileActionResultWithRequest(
			client,
			protocol.CmdWorkspaceLayoutUpdateTile,
			msg.WorkspaceID,
			tileID,
			requestID,
			fmt.Errorf("tile_id and tile_params are required"),
		)
		return
	}
	var tileKind string
	for _, tile := range workspacelayout.TileLeaves(snapshot.Layout) {
		if tile.TileID == tileID {
			tileKind = tile.TileKind
			break
		}
	}
	if tileKind == "" {
		d.sendWorkspaceLayoutTileActionResultWithRequest(
			client,
			protocol.CmdWorkspaceLayoutUpdateTile,
			msg.WorkspaceID,
			tileID,
			requestID,
			fmt.Errorf("tile not found: %s", tileID),
		)
		return
	}
	if tileKind != string(workspacelayout.TileKindBrowser) {
		d.sendWorkspaceLayoutTileActionResultWithRequest(
			client,
			protocol.CmdWorkspaceLayoutUpdateTile,
			msg.WorkspaceID,
			tileID,
			requestID,
			fmt.Errorf("tile parameters cannot be updated for tile kind %q", tileKind),
		)
		return
	}
	tileParams, err = validateBrowserURL(tileParams)
	if err != nil {
		d.sendWorkspaceLayoutTileActionResultWithRequest(
			client,
			protocol.CmdWorkspaceLayoutUpdateTile,
			msg.WorkspaceID,
			tileID,
			requestID,
			err,
		)
		return
	}
	layout, ok := workspacelayout.UpdateTileParams(snapshot.Layout, tileID, tileParams)
	if !ok {
		d.sendWorkspaceLayoutTileActionResultWithRequest(
			client,
			protocol.CmdWorkspaceLayoutUpdateTile,
			msg.WorkspaceID,
			tileID,
			requestID,
			fmt.Errorf("tile not found: %s", tileID),
		)
		return
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, nil)
}

func (d *Daemon) handleWorkspaceLayoutMoveLeaf(client *wsClient, msg *protocol.WorkspaceLayoutMoveLeafMessage) {
	err := d.moveLeaf(msg.WorkspaceID, msg.LeafID, msg.AnchorID, msg.Edge, msg.Ratio)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutMoveLeaf, msg.WorkspaceID, protocol.Ptr(strings.TrimSpace(msg.LeafID)), err)
}

func (d *Daemon) handleWorkspaceLayoutMoveLeafToWorkspace(client *wsClient, msg *protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage) {
	finalLeafID, err := d.moveLeafToWorkspace(msg.SourceWorkspaceID, msg.TargetWorkspaceID, msg.LeafID, protocol.Deref(msg.AnchorID), msg.Edge, msg.Ratio)
	d.sendWorkspaceLayoutMoveToWorkspaceResult(client, msg.SourceWorkspaceID, msg.TargetWorkspaceID, msg.LeafID, finalLeafID, err)
}

// moveLeaf relocates an existing leaf (terminal pane or docked tile) within a
// workspace layout and persists it. An empty anchorID docks the leaf against the
// whole workspace (the root). edge picks the split direction and side; ratio is
// the moved leaf's fraction of the new split, defaulting to an equal split. The
// move is rejected (returns an error the client ignores) when it can't happen —
// a self-drop, an unknown leaf, or the only leaf in the workspace.
func (d *Daemon) moveLeaf(workspaceID, leafID, anchorID string, edge protocol.WorkspaceLayoutDockEdge, ratio *float64) error {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return err
	}
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if leafID == "" {
		return fmt.Errorf("leaf_id is required")
	}

	direction, before := dockEdgeToSplit(edge)
	leafFraction := workspacelayout.DefaultSplitRatio
	if ratio != nil && *ratio > 0 && *ratio < 1 {
		leafFraction = *ratio
	}
	// MoveLeaf takes the children[0] fraction; convert from the moved leaf's share.
	childZeroRatio := leafFraction
	if !before {
		childZeroRatio = 1 - leafFraction
	}

	layout, ok := workspacelayout.MoveLeaf(
		snapshot.Layout,
		leafID,
		anchorID,
		newWorkspaceLayoutEntityID("split"),
		direction,
		before,
		childZeroRatio,
	)
	if !ok {
		return fmt.Errorf("could not move leaf: %s", leafID)
	}
	snapshot.Layout = layout
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		return err
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return nil
}

func (d *Daemon) moveLeafToWorkspace(sourceWorkspaceID, targetWorkspaceID, leafID, anchorID string, edge protocol.WorkspaceLayoutDockEdge, ratio *float64) (string, error) {
	sourceWorkspaceID = strings.TrimSpace(sourceWorkspaceID)
	targetWorkspaceID = strings.TrimSpace(targetWorkspaceID)
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if sourceWorkspaceID == "" || targetWorkspaceID == "" {
		return "", fmt.Errorf("source_workspace_id and target_workspace_id are required")
	}
	if leafID == "" {
		return "", fmt.Errorf("leaf_id is required")
	}
	if sourceWorkspaceID == targetWorkspaceID {
		if err := d.moveLeaf(sourceWorkspaceID, leafID, anchorID, edge, ratio); err != nil {
			return "", err
		}
		return leafID, nil
	}

	source, err := d.ensureWorkspaceLayout(sourceWorkspaceID)
	if err != nil {
		return "", err
	}
	target, err := d.currentOrEmptyWorkspaceLayout(targetWorkspaceID)
	if err != nil {
		return "", err
	}

	var movedPane *workspacelayout.Pane
	for i := range source.Panes {
		if source.Panes[i].PaneID == leafID {
			pane := source.Panes[i]
			movedPane = &pane
			break
		}
	}
	if movedPane != nil {
		for _, pane := range target.Panes {
			if pane.SessionID != "" && pane.SessionID == movedPane.SessionID {
				return "", fmt.Errorf("target workspace already has session: %s", movedPane.SessionID)
			}
		}
	}

	direction, before := dockEdgeToSplit(edge)
	leafFraction := workspacelayout.DefaultSplitRatio
	if ratio != nil && *ratio > 0 && *ratio < 1 {
		leafFraction = *ratio
	}
	childZeroRatio := leafFraction
	if !before {
		childZeroRatio = 1 - leafFraction
	}

	move, ok := workspacelayout.MoveLeafBetweenLayouts(
		source.Layout,
		target.Layout,
		leafID,
		anchorID,
		newWorkspaceLayoutEntityID("split"),
		direction,
		before,
		childZeroRatio,
		newWorkspaceLayoutEntityID("leaf"),
	)
	if !ok {
		return "", fmt.Errorf("could not move leaf: %s", leafID)
	}

	source.Layout = move.SourceLayout
	target.Layout = move.TargetLayout
	if movedPane != nil {
		nextSourcePanes := make([]workspacelayout.Pane, 0, len(source.Panes)-1)
		for _, pane := range source.Panes {
			if pane.PaneID == movedPane.PaneID {
				continue
			}
			nextSourcePanes = append(nextSourcePanes, pane)
		}
		movedPane.PaneID = move.FinalLeafID
		target.Panes = append(target.Panes, *movedPane)
		source.Panes = nextSourcePanes
		target.ActivePaneID = movedPane.PaneID
	}

	sourceNormalized := workspacelayout.NormalizeWorkspaceLayout(*source)
	targetNormalized := workspacelayout.NormalizeWorkspaceLayout(*target)
	sourceEmpty := workspacelayout.LayoutEmpty(sourceNormalized.Layout)

	if err := d.store.SaveWorkspaceLayout(targetNormalized); err != nil {
		return "", err
	}
	if sourceEmpty {
		d.store.RemoveWorkspaceLayout(sourceWorkspaceID)
	} else if err := d.store.SaveWorkspaceLayout(sourceNormalized); err != nil {
		return "", err
	}

	// Broadcast layout changes before changing session ownership. The frontend
	// filters visible sessions through workspace layouts, so the opposite order
	// creates a transient state where the moved session belongs to the target
	// workspace but the target layout snapshot does not include it yet.
	d.broadcastWorkspaceLayoutUpdated(targetWorkspaceID)
	if !sourceEmpty {
		d.broadcastWorkspaceLayoutUpdated(sourceWorkspaceID)
	}

	if movedPane != nil && movedPane.SessionID != "" {
		if d.workspaces != nil {
			d.workspaces.associateSession(movedPane.SessionID, targetWorkspaceID, movedPane.Title)
		}
		d.store.AssignSessionWorkspace(movedPane.SessionID, targetWorkspaceID)
		if session := d.store.Get(movedPane.SessionID); session != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionStateChanged,
				Session: d.sessionForBroadcast(session),
			})
		}
	}

	if sourceEmpty {
		d.unregisterWorkspaceIfEmptyAfterMove(sourceWorkspaceID)
	} else {
		d.recomputeAndBroadcastWorkspace(sourceWorkspaceID)
	}
	d.recomputeAndBroadcastWorkspace(targetWorkspaceID)
	return move.FinalLeafID, nil
}

func (d *Daemon) unregisterWorkspaceIfEmptyAfterMove(workspaceID string) {
	if d.workspaces == nil {
		return
	}
	if len(d.workspaces.sessionIDs(workspaceID)) > 0 ||
		d.workspaceHasSessionlessContent(workspaceID) {
		d.recomputeAndBroadcastWorkspace(workspaceID)
		return
	}
	snapshot, removed := d.workspaces.unregister(workspaceID)
	if !removed {
		return
	}
	d.forgetWorkspaceContextCompaction(workspaceID)
	d.snapshotWorkspaceContextOnRemove(workspaceID, snapshot.Title)
	d.store.RemoveWorkspace(workspaceID)
	d.pruneTileContentSubscriptionsForLayout(workspaceID, nil)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceUnregistered,
		Workspace: &snapshot,
	})
}

func (d *Daemon) recomputeAndBroadcastWorkspace(workspaceID string) {
	if d.workspaces == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	updated, changed := d.recomputeWorkspaceStatus(workspaceID)
	if !changed {
		var ok bool
		updated, ok = d.workspaces.snapshot(workspaceID)
		if !ok {
			return
		}
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorkspaceStateChanged,
		Workspace: &updated,
	})
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
	if workspacelayout.HasTile(snapshot.Layout, paneID) {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, protocol.Ptr(paneID), fmt.Errorf("tile already exists: %s", paneID))
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

	// A workspace dies only when its last leaf is gone. A tile left behind
	// keeps the workspace (now sessionless) and its layout alive.
	layoutEmpty := workspacelayout.LayoutEmpty(normalized.Layout)
	if layoutEmpty {
		d.store.RemoveWorkspaceLayout(msg.WorkspaceID)
	} else {
		if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
			return
		}
	}
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)

	if layoutEmpty {
		// A context-bearing workspace can survive with no layout. Publish that
		// empty state so clients cannot retain and replay the removed pane.
		if d.store.GetWorkspace(msg.WorkspaceID) != nil {
			emptyLayout, err := protocolWorkspaceLayout(normalized)
			if err != nil {
				d.logf("workspace empty layout update failed for workspace %s: %v", msg.WorkspaceID, err)
				return
			}
			d.broadcastWorkspaceLayoutSnapshotUpdated(emptyLayout)
		}
	} else {
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
	if workspacelayout.LayoutEmpty(normalized.Layout) {
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
