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
	"github.com/victorarias/attn/internal/sessionlayout"
)

var shellTitlePattern = regexp.MustCompile(`^Shell (\d+)$`)

func (d *Daemon) ensureSessionLayout(sessionID string) (*sessionlayout.SessionLayout, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	current := d.store.GetSessionLayout(sessionID)
	if current == nil {
		snapshot := sessionlayout.DefaultSessionLayout(sessionID)
		if err := d.store.SaveSessionLayout(snapshot); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}

	normalized := sessionlayout.NormalizeSessionLayout(*current, sessionID)
	if !reflect.DeepEqual(*current, normalized) {
		if err := d.store.SaveSessionLayout(normalized); err != nil {
			return nil, err
		}
	}
	return &normalized, nil
}

func (d *Daemon) protocolSessionLayout(sessionID string) (*protocol.SessionLayout, error) {
	snapshot, err := d.ensureSessionLayout(sessionID)
	if err != nil {
		return nil, err
	}
	return protocolSessionLayout(*snapshot)
}

func protocolSessionLayout(snapshot sessionlayout.SessionLayout) (*protocol.SessionLayout, error) {
	layoutJSON, err := sessionlayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		return nil, err
	}
	panes := make([]protocol.SessionLayoutPane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		next := protocol.SessionLayoutPane{
			PaneID: pane.PaneID,
			Kind:   protocol.SessionLayoutPaneKind(pane.Kind),
			Title:  pane.Title,
		}
		if strings.TrimSpace(pane.RuntimeID) != "" {
			next.RuntimeID = protocol.Ptr(strings.TrimSpace(pane.RuntimeID))
		}
		panes = append(panes, next)
	}
	ws := &protocol.SessionLayout{
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

func (d *Daemon) listSessionLayouts(sessions []*protocol.Session) []protocol.SessionLayout {
	if len(sessions) == 0 {
		return nil
	}
	workspaces := make([]protocol.SessionLayout, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		snapshot, err := d.protocolSessionLayout(session.ID)
		if err != nil {
			d.logf("workspace snapshot failed for session %s: %v", session.ID, err)
			continue
		}
		workspaces = append(workspaces, *snapshot)
	}
	return workspaces
}

func (d *Daemon) sendSessionLayout(client *wsClient, sessionID string) {
	snapshot, err := d.protocolSessionLayout(sessionID)
	if err != nil {
		d.sendCommandError(client, protocol.CmdSessionLayoutGet, err.Error())
		return
	}
	d.sendToClient(client, protocol.SessionLayoutMessage{
		Event:         protocol.EventSessionLayout,
		SessionLayout: *snapshot,
	})
}

func (d *Daemon) sendSessionLayoutActionResult(client *wsClient, action, sessionID string, paneID *string, err error) {
	result := protocol.SessionLayoutActionResultMessage{
		Event:     protocol.EventSessionLayoutActionResult,
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

func (d *Daemon) broadcastSessionLayoutUpdated(sessionID string) {
	snapshot, err := d.protocolSessionLayout(sessionID)
	if err != nil {
		d.logf("workspace update failed for session %s: %v", sessionID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:         protocol.EventSessionLayoutUpdated,
		SessionLayout: snapshot,
	})
}

func (d *Daemon) broadcastSessionLayout(sessionID string) {
	snapshot, err := d.protocolSessionLayout(sessionID)
	if err != nil {
		d.logf("workspace snapshot failed for session %s: %v", sessionID, err)
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:         protocol.EventSessionLayout,
		SessionLayout: snapshot,
	})
}

func protocolDirection(direction protocol.SessionLayoutSplitDirection) sessionlayout.Direction {
	switch direction {
	case protocol.SessionLayoutSplitDirectionHorizontal:
		return sessionlayout.DirectionHorizontal
	default:
		return sessionlayout.DirectionVertical
	}
}

func nextShellTitle(snapshot sessionlayout.SessionLayout) string {
	maxShell := 0
	for _, pane := range snapshot.Panes {
		if pane.Kind != sessionlayout.PaneKindShell {
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

func newSessionLayoutEntityID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (d *Daemon) handleSessionLayoutGet(client *wsClient, msg *protocol.SessionLayoutGetMessage) {
	d.sendSessionLayout(client, msg.SessionID)
}

func (d *Daemon) handleSessionLayoutFocusPane(client *wsClient, msg *protocol.SessionLayoutFocusPaneMessage) {
	snapshot, err := d.ensureSessionLayout(msg.SessionID)
	if err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	if !sessionlayout.HasPane(snapshot.Layout, msg.PaneID) {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if snapshot.ActivePaneID == msg.PaneID {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
		return
	}
	snapshot.ActivePaneID = msg.PaneID
	if err := d.store.SaveSessionLayout(*snapshot); err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastSessionLayoutUpdated(msg.SessionID)
	d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutFocusPane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleSessionLayoutRenamePane(client *wsClient, msg *protocol.SessionLayoutRenamePaneMessage) {
	if msg.PaneID == sessionlayout.MainPaneID {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("main pane title is fixed"))
		return
	}

	snapshot, err := d.ensureSessionLayout(msg.SessionID)
	if err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("title cannot be empty"))
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
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}
	if err := d.store.SaveSessionLayout(*snapshot); err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastSessionLayoutUpdated(msg.SessionID)
	d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutRenamePane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)
}

func (d *Daemon) handleSessionLayoutSplitPane(client *wsClient, msg *protocol.SessionLayoutSplitPaneMessage) {
	if d.ptyBackend == nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pty backend unavailable"))
		return
	}
	snapshot, err := d.ensureSessionLayout(msg.SessionID)
	if err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	session := d.store.Get(msg.SessionID)
	if session == nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("session not found: %s", msg.SessionID))
		return
	}
	if !sessionlayout.HasPane(snapshot.Layout, msg.TargetPaneID) {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	paneID := newSessionLayoutEntityID("pane")
	runtimeID := newSessionLayoutEntityID("runtime")
	splitID := newSessionLayoutEntityID("split")
	title := nextShellTitle(*snapshot)

	if err := d.ptyBackend.Spawn(context.Background(), ptySpawnShellOptions(runtimeID, session.Directory, title)); err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}

	layout, changed := sessionlayout.Split(
		snapshot.Layout,
		msg.TargetPaneID,
		paneID,
		splitID,
		protocolDirection(msg.Direction),
		sessionlayout.DefaultSplitRatio,
	)
	if !changed {
		_ = d.removePTYSession(runtimeID)
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), fmt.Errorf("pane not found: %s", msg.TargetPaneID))
		return
	}

	snapshot.Layout = layout
	snapshot.ActivePaneID = paneID
	snapshot.Panes = append(snapshot.Panes, sessionlayout.Pane{
		PaneID:    paneID,
		RuntimeID: runtimeID,
		Kind:      sessionlayout.PaneKindShell,
		Title:     title,
	})
	normalized := sessionlayout.NormalizeSessionLayout(*snapshot, msg.SessionID)
	if err := d.store.SaveSessionLayout(normalized); err != nil {
		_ = d.removePTYSession(runtimeID)
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), err)
		return
	}
	d.broadcastSessionLayoutUpdated(msg.SessionID)
	d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutSplitPane, msg.SessionID, protocol.Ptr(msg.TargetPaneID), nil)
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

func (d *Daemon) handleSessionLayoutClosePane(client *wsClient, msg *protocol.SessionLayoutClosePaneMessage) {
	if msg.PaneID == sessionlayout.MainPaneID {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("main pane cannot be closed"))
		return
	}

	snapshot, err := d.ensureSessionLayout(msg.SessionID)
	if err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}

	runtimeID := ""
	nextPanes := make([]sessionlayout.Pane, 0, len(snapshot.Panes))
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
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), fmt.Errorf("pane not found: %s", msg.PaneID))
		return
	}

	layout, _ := sessionlayout.Remove(snapshot.Layout, msg.PaneID)
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := sessionlayout.NormalizeSessionLayout(*snapshot, msg.SessionID)
	if err := d.store.SaveSessionLayout(normalized); err != nil {
		d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), err)
		return
	}
	d.broadcastSessionLayoutUpdated(msg.SessionID)
	d.sendSessionLayoutActionResult(client, protocol.CmdSessionLayoutClosePane, msg.SessionID, protocol.Ptr(msg.PaneID), nil)

	if strings.TrimSpace(runtimeID) != "" {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) killWorkspaceRuntimesForSession(sessionID string) {
	snapshot, err := d.ensureSessionLayout(sessionID)
	if err != nil {
		return
	}
	for _, runtimeID := range sessionlayout.SortedShellRuntimeIDs(*snapshot) {
		d.terminateSession(runtimeID, syscall.SIGTERM)
	}
}

func (d *Daemon) handleWorkspaceRuntimeExit(runtimeID string, exitCode int, signal string) bool {
	sessionID, paneID, ok := d.store.FindSessionLayoutPaneByRuntimeID(runtimeID)
	if !ok {
		return false
	}

	snapshot, err := d.ensureSessionLayout(sessionID)
	if err != nil {
		d.logf("workspace runtime exit reconcile failed for runtime %s: %v", runtimeID, err)
		return false
	}

	layout, _ := sessionlayout.Remove(snapshot.Layout, paneID)
	nextPanes := make([]sessionlayout.Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			nextPanes = append(nextPanes, pane)
		}
	}
	snapshot.Layout = layout
	snapshot.Panes = nextPanes
	normalized := sessionlayout.NormalizeSessionLayout(*snapshot, sessionID)
	if err := d.store.SaveSessionLayout(normalized); err != nil {
		d.logf("workspace runtime exit save failed for runtime %s: %v", runtimeID, err)
		return false
	}

	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventSessionLayoutRuntimeExited,
		SessionID: protocol.Ptr(sessionID),
		PaneID:    protocol.Ptr(paneID),
		RuntimeID: protocol.Ptr(runtimeID),
		ExitCode:  protocol.Ptr(exitCode),
		Signal:    optionalStringPtr(signal),
	})
	d.broadcastSessionLayoutUpdated(sessionID)
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
		snapshot, err := d.ensureSessionLayout(session.ID)
		if err != nil {
			d.logf("workspace ensure failed for session %s: %v", session.ID, err)
			continue
		}

		nextPanes := make([]sessionlayout.Pane, 0, len(snapshot.Panes))
		changed := false
		for _, pane := range snapshot.Panes {
			if pane.Kind != sessionlayout.PaneKindShell {
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
			normalized := sessionlayout.NormalizeSessionLayout(*snapshot, session.ID)
			if err := d.store.SaveSessionLayout(normalized); err != nil {
				d.logf("workspace reconcile save failed for session %s: %v", session.ID, err)
			}
			continue
		}
		for _, pane := range snapshot.Panes {
			if pane.Kind == sessionlayout.PaneKindShell && pane.RuntimeID != "" {
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
