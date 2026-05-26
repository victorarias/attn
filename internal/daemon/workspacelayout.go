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
		return nil, fmt.Errorf("workspace has no initial session: %s", workspaceID)
	}
	primarySessionID := memberIDs[0]
	rootPane := d.workspaceRootPane(primarySessionID)

	current := d.store.GetWorkspaceLayout(workspaceID)
	if current == nil {
		snapshot := workspacelayout.DefaultWorkspaceLayoutForRoot(workspaceID, rootPane)
		if err := d.store.SaveWorkspaceLayout(snapshot); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}

	for _, pane := range current.Panes {
		if pane.PaneID == workspacelayout.MainPaneID && strings.TrimSpace(pane.SessionID) != "" {
			rootPane = pane
			break
		}
	}
	normalized := workspacelayout.NormalizeWorkspaceLayout(*current, rootPane)
	if !reflect.DeepEqual(*current, normalized) {
		if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
			return nil, err
		}
	}
	return &normalized, nil
}

func (d *Daemon) workspaceRootPane(sessionID string) workspacelayout.Pane {
	root := workspacelayout.Pane{
		RuntimeID: sessionID,
		SessionID: sessionID,
		Kind:      workspacelayout.PaneKindAgent,
		Title:     workspacelayout.DefaultPaneTitle,
	}
	if session := d.store.Get(sessionID); session != nil {
		if session.Agent == protocol.SessionAgentShell {
			root.Kind = workspacelayout.PaneKindShell
			root.Title = workspacelayout.DefaultShellTitle
		}
		if title := strings.TrimSpace(session.Label); title != "" {
			root.Title = title
		}
	}
	return workspacelayout.NormalizeRootPane(root)
}

func (d *Daemon) protocolWorkspaceLayout(workspaceID string) (*protocol.WorkspaceLayout, error) {
	if d.workspaces != nil {
		if _, exists := d.workspaces.snapshot(workspaceID); exists && len(d.workspaces.sessionIDs(workspaceID)) == 0 {
			return &protocol.WorkspaceLayout{
				WorkspaceID:  workspaceID,
				ActivePaneID: "",
				LayoutJson:   `{"type":"empty"}`,
				Panes:        []protocol.WorkspaceLayoutPane{},
			}, nil
		}
	}
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

func (d *Daemon) recordWorkspacePaneFocus(workspaceID, paneID string) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return
	}
	d.workspacePaneHistoryMu.Lock()
	defer d.workspacePaneHistoryMu.Unlock()
	if d.workspacePaneHistory == nil {
		d.workspacePaneHistory = make(map[string][]string)
	}
	history := d.workspacePaneHistory[workspaceID]
	filtered := history[:0]
	for _, existing := range history {
		if existing != paneID {
			filtered = append(filtered, existing)
		}
	}
	d.workspacePaneHistory[workspaceID] = append(filtered, paneID)
}

func (d *Daemon) recordWorkspacePaneActivation(workspaceID, previousPaneID, paneID string) {
	if previousPaneID != paneID {
		d.recordWorkspacePaneFocus(workspaceID, previousPaneID)
	}
	d.recordWorkspacePaneFocus(workspaceID, paneID)
}

func (d *Daemon) previouslyFocusedSurvivingPane(workspaceID, removedPaneID string, layout workspacelayout.Node) string {
	d.workspacePaneHistoryMu.Lock()
	defer d.workspacePaneHistoryMu.Unlock()
	if d.workspacePaneHistory == nil {
		d.workspacePaneHistory = make(map[string][]string)
	}
	history := d.workspacePaneHistory[workspaceID]
	surviving := make([]string, 0, len(history))
	for _, paneID := range history {
		if paneID != removedPaneID && workspacelayout.HasPane(layout, paneID) {
			surviving = append(surviving, paneID)
		}
	}
	d.workspacePaneHistory[workspaceID] = surviving
	if len(surviving) == 0 {
		return ""
	}
	return surviving[len(surviving)-1]
}

func (d *Daemon) clearWorkspacePaneFocusHistory(workspaceID string) {
	d.workspacePaneHistoryMu.Lock()
	defer d.workspacePaneHistoryMu.Unlock()
	delete(d.workspacePaneHistory, workspaceID)
}

func (d *Daemon) restoreWorkspacePaneAfterRemoval(snapshot *workspacelayout.WorkspaceLayout, removedPaneID string) {
	preferredPaneID := d.previouslyFocusedSurvivingPane(snapshot.WorkspaceID, removedPaneID, snapshot.Layout)
	if snapshot.ActivePaneID == removedPaneID && preferredPaneID != "" {
		snapshot.ActivePaneID = preferredPaneID
	}
}

func (d *Daemon) ensureAgentPaneInWorkspace(workspaceID, sessionID, title, requestedTargetPaneID string, requestedDirection *protocol.WorkspaceLayoutSplitDirection) error {
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
	targetPaneID := strings.TrimSpace(requestedTargetPaneID)
	if targetPaneID == "" {
		targetPaneID = snapshot.ActivePaneID
	}
	if !workspacelayout.HasPane(snapshot.Layout, targetPaneID) {
		targetPaneID = workspacelayout.MainPaneID
	}
	direction := workspacelayout.DirectionVertical
	if requestedDirection != nil {
		direction = protocolDirection(*requestedDirection)
	}
	layout, changed := workspacelayout.Split(
		snapshot.Layout,
		targetPaneID,
		paneID,
		splitID,
		direction,
		workspacelayout.DefaultSplitRatio,
	)
	if !changed {
		return fmt.Errorf("active pane not found: %s", targetPaneID)
	}
	if strings.TrimSpace(title) == "" {
		title = workspacelayout.DefaultPaneTitle
	}
	previousPaneID := snapshot.ActivePaneID
	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, workspacelayout.Pane{
		PaneID:    paneID,
		RuntimeID: sessionID,
		SessionID: sessionID,
		Kind:      workspacelayout.PaneKindAgent,
		Title:     title,
	})
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		return err
	}
	d.recordWorkspacePaneActivation(snapshot.WorkspaceID, previousPaneID, paneID)
	return nil
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
		d.recordWorkspacePaneFocus(msg.WorkspaceID, msg.PaneID)
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
		return
	}
	previousPaneID := snapshot.ActivePaneID
	snapshot.ActivePaneID = msg.PaneID
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutFocusPane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.recordWorkspacePaneActivation(msg.WorkspaceID, previousPaneID, msg.PaneID)
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
	runtimeID := newWorkspaceLayoutEntityID("runtime")
	splitID := newWorkspaceLayoutEntityID("split")
	title := nextShellTitle(*snapshot)

	cwd := strings.TrimSpace(protocol.Deref(msg.Cwd))
	if cwd == "" {
		cwd = workspace.Directory
	}
	cwd = resolveSpawnCWD(cwd)
	if err := d.ptyBackend.Spawn(context.Background(), ptySpawnShellOptions(runtimeID, cwd, title)); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), err)
		return
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

	previousPaneID := snapshot.ActivePaneID
	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, workspacelayout.Pane{
		PaneID:    paneID,
		RuntimeID: runtimeID,
		Kind:      workspacelayout.PaneKindShell,
		Title:     title,
	})
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0])
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		_ = d.removePTYSession(runtimeID)
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutSplitPane, msg.WorkspaceID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	d.recordWorkspacePaneActivation(msg.WorkspaceID, previousPaneID, paneID)
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
	if msg.PaneID == workspacelayout.MainPaneID {
		d.handleUnregisterWorkspace(client, &protocol.UnregisterWorkspaceMessage{
			Cmd: protocol.CmdUnregisterWorkspace,
			ID:  msg.WorkspaceID,
		})
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)
		return
	}

	snapshot, err := d.ensureWorkspaceLayout(msg.WorkspaceID)
	if err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}

	runtimeID := ""
	nextPanes := make([]workspacelayout.Pane, 0, len(snapshot.Panes))
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
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}

	layout, _ := workspacelayout.Remove(snapshot.Layout, msg.PaneID)
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	d.restoreWorkspacePaneAfterRemoval(snapshot, msg.PaneID)
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0])
	if err := d.store.SaveWorkspaceLayout(normalized); err != nil {
		d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastWorkspaceLayoutUpdated(msg.WorkspaceID)
	d.sendWorkspaceLayoutActionResult(client, protocol.CmdWorkspaceLayoutClosePane, msg.WorkspaceID, protocol.Ptr(msg.PaneID), nil)

	if strings.TrimSpace(runtimeID) != "" {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) killWorkspaceLayoutRuntimes(workspaceID string) {
	snapshot, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		return
	}
	for _, runtimeID := range workspacelayout.SortedShellRuntimeIDs(*snapshot) {
		if pane := snapshotPaneForRuntime(*snapshot, runtimeID); pane == nil || pane.SessionID == "" {
			d.terminateSession(runtimeID, syscall.SIGTERM)
		}
	}
}

func snapshotPaneForRuntime(snapshot workspacelayout.WorkspaceLayout, runtimeID string) *workspacelayout.Pane {
	for i := range snapshot.Panes {
		if snapshot.Panes[i].RuntimeID == runtimeID {
			return &snapshot.Panes[i]
		}
	}
	return nil
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
	d.restoreWorkspacePaneAfterRemoval(snapshot, paneID)
	normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0])
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
			if pane.SessionID != "" {
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
			normalized := workspacelayout.NormalizeWorkspaceLayout(*snapshot, snapshot.Panes[0])
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
