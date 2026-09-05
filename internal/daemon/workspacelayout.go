package daemon

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/rankkey"
	"github.com/victorarias/attn/internal/store"
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

// The layout travels in the payload rather than being re-read at projection time: a
// re-read cannot reproduce the deliberately empty layout a workspace with no panes gets.
func (d *Daemon) broadcastWorkspaceLayoutSnapshotUpdated(snapshot *protocol.WorkspaceLayout) {
	if snapshot == nil {
		return
	}
	d.publishFact(FactWorkspaceLayoutChanged, snapshot.WorkspaceID, snapshot)
}

func (d *Daemon) projectWorkspaceLayoutChanged(ev bus.Event) {
	snapshot, ok := decodeFact[*protocol.WorkspaceLayout](d, ev)
	if !ok || snapshot == nil {
		return
	}
	d.pruneTileContentSubscriptionsForWorkspace(snapshot.WorkspaceID)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:           protocol.EventWorkspaceLayoutUpdated,
		WorkspaceLayout: snapshot,
	})
}

func (d *Daemon) broadcastWorkspaceLayout(workspaceID string) {
	if _, err := d.protocolWorkspaceLayout(workspaceID); err != nil {
		d.logf("workspace layout snapshot failed for workspace %s: %v", workspaceID, err)
		return
	}
	d.publishFact(FactWorkspaceLayoutRepublished, workspaceID, nil)
}

func (d *Daemon) projectWorkspaceLayoutRepublished(workspaceID string) {
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

const defaultTileFraction = 0.32

func dockEdgeToSplit(edge protocol.WorkspaceLayoutDockEdge) (workspacelayout.Direction, bool) {
	switch edge {
	case protocol.WorkspaceLayoutDockEdgeLeft:
		return workspacelayout.DirectionVertical, true
	case protocol.WorkspaceLayoutDockEdgeTop:
		return workspacelayout.DirectionHorizontal, true
	case protocol.WorkspaceLayoutDockEdgeBottom:
		return workspacelayout.DirectionHorizontal, false
	default:
		return workspacelayout.DirectionVertical, false
	}
}

func (d *Daemon) handleWorkspaceLayoutDockTile(client *wsClient, msg *protocol.WorkspaceLayoutDockTileMessage) {
	params := strings.TrimSpace(protocol.Deref(msg.TileParams))
	if params == "" {
		if snapshot := d.store.GetWorkspaceLayout(msg.WorkspaceID); snapshot != nil {
			params, _ = workspacelayout.TileParamsByID(snapshot.Layout, strings.TrimSpace(msg.TileID))
		}
	}
	err := d.dockTile(msg.WorkspaceID, msg.AnchorPaneID, msg.TileID, msg.TileKind, params, "", msg.Edge, msg.Ratio)
	d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutDockTile, msg.WorkspaceID, msg.TileID, err)
}

// An empty tileSessionID preserves any existing binding.
func (d *Daemon) dockTile(workspaceID, anchorPaneID, tileID, tileKind, tileParams, tileSessionID string, edge protocol.WorkspaceLayoutDockEdge, ratio *float64) error {
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
		strings.TrimSpace(tileSessionID),
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
	if workspacelayout.LayoutEmpty(normalized.Layout) {
		// Drop the layout row rather than storing a leafless one: ensureWorkspaceLayout rejects
		// it, so the sidebar row would survive with a close button that never works.
		d.store.RemoveWorkspaceLayout(msg.WorkspaceID)
		d.sendWorkspaceLayoutTileActionResult(client, protocol.CmdWorkspaceLayoutUndockTile, msg.WorkspaceID, tileID, nil)
		if d.unregisterWorkspaceIfEmpty(msg.WorkspaceID) {
			return
		}
		// It survives, so publish the empty layout instead of leaving clients
		// replaying the undocked tile.
		emptyLayout, err := protocolWorkspaceLayout(normalized)
		if err != nil {
			d.logf("workspace empty layout update failed for workspace %s: %v", msg.WorkspaceID, err)
			return
		}
		d.broadcastWorkspaceLayoutSnapshotUpdated(emptyLayout)
		return
	}
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
	if retarget := strings.TrimSpace(protocol.Deref(msg.TileSessionID)); retarget != "" {
		// Never persist a binding to a session the daemon does not know: a racing
		// client would write a dangling id into the layout.
		if d.store.Get(retarget) == nil {
			d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, fmt.Errorf("session not found: %s", retarget))
			return
		}
		if err := d.rebindTileSession(msg.WorkspaceID, tileID, retarget); err != nil {
			d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, err)
			return
		}
		if tileKind == string(workspacelayout.TileKindMarkdown) {
			d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, nil)
			return
		}
		// The params-update path below saves a whole normalized snapshot, so
		// re-fetch it or the save clobbers the binding just persisted.
		if snapshot, err = d.ensureWorkspaceLayout(msg.WorkspaceID); err != nil {
			d.sendWorkspaceLayoutTileActionResultWithRequest(client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID, tileID, requestID, err)
			return
		}
	}

	switch tileKind {
	case string(workspacelayout.TileKindBrowser):
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
	case string(workspacelayout.TileKindNotebook):
		// tileParams is the open file's path — opaque here, already trimmed.
	case string(workspacelayout.TileKindSeed):
		if err := d.requireHome(garden.Surface); err != nil {
			d.sendWorkspaceLayoutTileActionResultWithRequest(
				client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID,
				tileID, requestID, err,
			)
			return
		}
		if _, _, err := d.readSeed(tileParams); err != nil {
			d.sendWorkspaceLayoutTileActionResultWithRequest(
				client, protocol.CmdWorkspaceLayoutUpdateTile, msg.WorkspaceID,
				tileID, requestID, err,
			)
			return
		}
	default:
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

// The workspace_registered broadcast goes out BEFORE the layout move so clients learn the
// workspace exists before its first workspace_layout_updated references it.
func (d *Daemon) handleWorkspaceLayoutMoveLeafToNewWorkspace(client *wsClient, msg *protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage) {
	sourceWorkspaceID := strings.TrimSpace(msg.SourceWorkspaceID)
	leafID := strings.TrimSpace(msg.LeafID)
	if sourceWorkspaceID == "" {
		d.sendWorkspaceLayoutMoveToNewWorkspaceResult(client, sourceWorkspaceID, "", leafID, "", fmt.Errorf("source_workspace_id is required"))
		return
	}
	if leafID == "" {
		d.sendWorkspaceLayoutMoveToNewWorkspaceResult(client, sourceWorkspaceID, "", leafID, "", fmt.Errorf("leaf_id is required"))
		return
	}
	if d.workspaces == nil {
		d.sendWorkspaceLayoutMoveToNewWorkspaceResult(client, sourceWorkspaceID, "", leafID, "", fmt.Errorf("workspace registry unavailable"))
		return
	}
	source, ok := d.workspaces.snapshot(sourceWorkspaceID)
	if !ok {
		d.sendWorkspaceLayoutMoveToNewWorkspaceResult(client, sourceWorkspaceID, "", leafID, "", fmt.Errorf("workspace not found: %s", sourceWorkspaceID))
		return
	}

	newWorkspaceID := uuid.NewString()
	title := d.newWorkspaceTitleForMovedLeaf(sourceWorkspaceID, leafID, source.Title)
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        newWorkspaceID,
		Title:     title,
		Directory: source.Directory,
	})

	finalLeafID, err := d.moveLeafToWorkspace(sourceWorkspaceID, newWorkspaceID, leafID, protocol.Deref(msg.AnchorID), protocol.Deref(msg.Edge), msg.Ratio)
	if err != nil {
		d.unregisterWorkspaceIfEmpty(newWorkspaceID)
	}
	d.sendWorkspaceLayoutMoveToNewWorkspaceResult(client, sourceWorkspaceID, newWorkspaceID, leafID, finalLeafID, err)
}

func (d *Daemon) newWorkspaceTitleForMovedLeaf(sourceWorkspaceID, leafID, fallbackTitle string) string {
	if snapshot := d.store.GetWorkspaceLayout(sourceWorkspaceID); snapshot != nil {
		for _, pane := range snapshot.Panes {
			if pane.PaneID == leafID {
				if title := strings.TrimSpace(pane.Title); title != "" {
					return title
				}
				break
			}
		}
	}
	return strings.TrimSpace(fallbackTitle)
}

func (d *Daemon) sendWorkspaceLayoutMoveToNewWorkspaceResult(client *wsClient, sourceWorkspaceID, newWorkspaceID, leafID, finalLeafID string, err error) {
	result := protocol.WorkspaceLayoutActionResultMessage{
		Event:             protocol.EventWorkspaceLayoutActionResult,
		Action:            protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace,
		WorkspaceID:       sourceWorkspaceID,
		SourceWorkspaceID: protocol.Ptr(strings.TrimSpace(sourceWorkspaceID)),
		LeafID:            protocol.Ptr(strings.TrimSpace(leafID)),
		Success:           err == nil,
	}
	if strings.TrimSpace(newWorkspaceID) != "" {
		result.TargetWorkspaceID = protocol.Ptr(strings.TrimSpace(newWorkspaceID))
	}
	if strings.TrimSpace(finalLeafID) != "" {
		result.FinalLeafID = protocol.Ptr(strings.TrimSpace(finalLeafID))
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

// prev_workspace_id ends up ABOVE the moved workspace and next_workspace_id
// BELOW it; the daemon computes the fractional key between their ranks.
func (d *Daemon) handleSetWorkspaceRank(client *wsClient, msg *protocol.SetWorkspaceRankMessage) {
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	prevID := strings.TrimSpace(protocol.Deref(msg.PrevWorkspaceID))
	nextID := strings.TrimSpace(protocol.Deref(msg.NextWorkspaceID))
	if workspaceID == "" {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, fmt.Errorf("missing workspace_id"))
		return
	}
	if d.workspaces == nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, fmt.Errorf("workspace registry unavailable"))
		return
	}
	if _, ok := d.workspaces.snapshot(workspaceID); !ok {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, fmt.Errorf("workspace not found: %s", workspaceID))
		return
	}

	// An empty neighbour id (move to top/bottom) resolves to "" — the MIN/MAX
	// sentinel rankkey.Between expects.
	prevRank := d.workspaces.rankOf(prevID)
	nextRank := d.workspaces.rankOf(nextID)
	rank, err := rankkey.Between(prevRank, nextRank)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, fmt.Errorf("could not rank workspace between %q and %q: %w", prevID, nextID, err))
		return
	}

	d.store.UpdateWorkspaceRank(workspaceID, rank)
	if _, ok := d.workspaces.applyRank(workspaceID, rank); !ok {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, fmt.Errorf("workspace not found: %s", workspaceID))
		return
	}
	d.publishFact(FactWorkspaceRankChanged, workspaceID, nil)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdSetWorkspaceRank, workspaceID, nil, nil)
}

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
		if strings.TrimSpace(movedPane.SessionID) == "" {
			return "", fmt.Errorf("agent pane has no backing session: %s", leafID)
		}
		if d.store.Get(movedPane.SessionID) == nil && movedPane.Status != workspacelayout.PaneStatusSpawning {
			return "", fmt.Errorf("agent pane session no longer exists: %s", movedPane.SessionID)
		}
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

	// Broadcast layout changes before changing session ownership: the frontend filters
	// visible sessions through layouts, so the opposite order hides the moved session.
	d.broadcastWorkspaceLayoutUpdated(targetWorkspaceID)
	if !sourceEmpty {
		d.broadcastWorkspaceLayoutUpdated(sourceWorkspaceID)
	}

	if movedPane != nil && movedPane.SessionID != "" {
		if d.workspaces != nil {
			d.workspaces.associateSession(movedPane.SessionID, targetWorkspaceID, movedPane.Title)
		}
		d.store.AssignSessionWorkspace(movedPane.SessionID, targetWorkspaceID)
		d.publishFact(FactSessionWorkspaceChanged, movedPane.SessionID, nil)
	}

	if sourceEmpty {
		d.unregisterWorkspaceIfEmpty(sourceWorkspaceID)
	} else {
		d.recomputeAndBroadcastWorkspace(sourceWorkspaceID)
	}
	d.recomputeAndBroadcastWorkspace(targetWorkspaceID)
	return move.FinalLeafID, nil
}

func (d *Daemon) unregisterWorkspaceIfEmpty(workspaceID string) bool {
	if d.workspaces == nil {
		return false
	}
	if len(d.workspaces.sessionIDs(workspaceID)) > 0 ||
		d.workspaceHasSessionlessContent(workspaceID) {
		d.recomputeAndBroadcastWorkspace(workspaceID)
		return false
	}
	snapshot, removed := d.workspaces.unregister(workspaceID)
	if !removed {
		return false
	}
	d.tearDownRemovedWorkspace(snapshot)
	return true
}

func (d *Daemon) recomputeAndBroadcastWorkspace(workspaceID string) {
	if d.workspaces == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	if _, changed := d.recomputeWorkspaceStatus(workspaceID); !changed {
		if _, ok := d.workspaces.snapshot(workspaceID); !ok {
			return
		}
	}
	d.publishFact(FactWorkspaceStatusChanged, workspaceID, nil)
}

func (d *Daemon) handleWorkspaceLayoutAddSessionPane(client *wsClient, msg *protocol.WorkspaceLayoutAddSessionPaneMessage) {
	paneID, err := d.addWorkspaceSessionPane(msg)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutAddSessionPane, msg.WorkspaceID, paneID, err)
}

func (d *Daemon) addWorkspaceSessionPane(msg *protocol.WorkspaceLayoutAddSessionPaneMessage) (*string, error) {
	snapshot, err := d.currentOrEmptyWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		return msg.PaneID, err
	}

	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	for _, pane := range snapshot.Panes {
		if pane.SessionID == sessionID {
			return protocol.Ptr(pane.PaneID), nil
		}
	}

	paneID := strings.TrimSpace(protocol.Deref(msg.PaneID))
	if paneID == "" {
		paneID = newWorkspaceLayoutEntityID("pane")
	}
	for _, pane := range snapshot.Panes {
		if pane.PaneID == paneID {
			return protocol.Ptr(paneID), fmt.Errorf("pane already exists: %s", paneID)
		}
	}
	if workspacelayout.HasPane(snapshot.Layout, paneID) {
		return protocol.Ptr(paneID), fmt.Errorf("pane already exists: %s", paneID)
	}
	if workspacelayout.HasTile(snapshot.Layout, paneID) {
		return protocol.Ptr(paneID), fmt.Errorf("tile already exists: %s", paneID)
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
			return protocol.Ptr(targetPaneID), fmt.Errorf("cannot target pane in empty layout")
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
			return nil, fmt.Errorf("workspace has no target pane")
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
			return protocol.Ptr(targetPaneID), fmt.Errorf("pane not found: %s", targetPaneID)
		}
		snapshot.Layout = layout
	}

	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, nextPane)
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		return protocol.Ptr(paneID), err
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	return protocol.Ptr(paneID), nil
}

func (d *Daemon) ensureWorkspaceSessionPane(workspaceID, sessionID, title string) (string, error) {
	msg := &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
	}
	if strings.TrimSpace(title) != "" {
		msg.Title = protocol.Ptr(title)
	}
	paneID, err := d.addWorkspaceSessionPane(msg)
	return protocol.Deref(paneID), err
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

	if closeErr := d.sessionCloseError(sessionID); closeErr != nil {
		d.logf("refusing to close pane %s for protected session %s: %v", msg.PaneID, sessionID, closeErr)
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), closeErr)
		return
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, msg.PaneID)
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
	layoutEmpty := workspacelayout.LayoutEmpty(normalized.Layout)
	var teardown *sessionTeardown
	if strings.TrimSpace(sessionID) != "" {
		var err error
		teardown, err = d.prepareSessionTeardown(sessionID)
		if err != nil {
			d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
			return
		}
	}

	// Commit the pane removal before terminating the session: teardown broadcasts
	// immediately, and every observer of those events must see the pane-free layout.
	if layoutEmpty {
		d.store.RemoveWorkspaceLayout(msg.WorkspaceID)
	} else if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		if teardown != nil {
			d.cancelSessionTeardown(sessionID)
		}
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}

	if teardown != nil {
		d.commitSessionUnregister(sessionID, store.SessionClose{By: store.SessionClosedByUser})
		if teardown.session != nil {
			d.publishSessionUnregistered(teardown.session)
			d.dissociateSessionFromWorkspace(teardown.session.ID)
		}
	}

	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)

	if layoutEmpty {
		// Publish the empty layout so clients cannot retain and replay the
		// removed pane.
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

	if teardown != nil {
		d.terminateSessionAsync(sessionID, syscall.SIGTERM, teardown)
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
	for _, id := range d.liveRuntimeSessionIDs(ctx) {
		liveIDs[id] = struct{}{}
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
			sessionID := strings.TrimSpace(pane.SessionID)
			if pane.Kind == workspacelayout.PaneKindAgent && sessionID != "" &&
				(d.store.Get(sessionID) != nil || pane.Status == workspacelayout.PaneStatusSpawning) {
				nextPanes = append(nextPanes, pane)
				continue
			}
			if pane.Kind == workspacelayout.PaneKindAgent {
				snapshot.Layout, _ = workspacelayout.Remove(snapshot.Layout, pane.PaneID)
			}
			changed = true
		}

		if changed {
			snapshot.Panes = nextPanes
			normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot)
			if workspacelayout.LayoutEmpty(normalized.Layout) {
				d.store.RemoveWorkspaceLayout(workspace.ID)
			} else if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
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
