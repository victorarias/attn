package daemon

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// markdownPanelID is the stable id of the single markdown panel per workspace.
// `attn open` re-docks this id, so opening another file retargets the existing
// panel rather than stacking up new ones.
const markdownPanelID = "panel-markdown"

// markdownPollInterval is how often the content watcher restats open markdown
// files to detect on-disk changes. Sub-second keeps live reload feeling instant
// without the complexity of per-file OS watches (which editors' atomic saves
// routinely break).
const markdownPollInterval = 750 * time.Millisecond

// maxMarkdownBytes caps how large a markdown file the daemon will read into a
// panel. Big enough for any real doc, small enough to never wedge the socket.
const maxMarkdownBytes = 5 << 20

// panelContentSig is a cheap fingerprint of a file used to detect changes
// between polls without re-reading the contents every tick.
type panelContentSig struct {
	mod     int64
	size    int64
	missing bool
}

type markdownPanelRef struct {
	workspaceID string
	panelID     string
	path        string
}

// setSelectedSession records the session the UI is currently showing. `attn
// open` with no explicit session targets this session's workspace.
func (d *Daemon) setSelectedSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	d.selectedSessionMu.Lock()
	d.selectedSessionID = sessionID
	d.selectedSessionMu.Unlock()
}

func (d *Daemon) currentlySelectedSession() string {
	d.selectedSessionMu.RLock()
	defer d.selectedSessionMu.RUnlock()
	return d.selectedSessionID
}

// panelFilePath resolves a panel's persisted params into a kind + file path.
func (d *Daemon) panelFilePath(workspaceID, panelID string) (kind, path string, found bool) {
	if d.store == nil {
		return "", "", false
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return "", "", false
	}
	for _, leaf := range workspacelayout.PanelLeaves(snapshot.Layout) {
		if leaf.PanelID == panelID {
			return leaf.PanelKind, strings.TrimSpace(leaf.PanelParams), true
		}
	}
	return "", "", false
}

// readMarkdownFile reads a markdown file for a panel, returning a friendly error
// (rather than failing) when the file is missing, a directory, or oversized so
// the panel can render a clear state instead of going blank.
func readMarkdownFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no file is associated with this panel")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("not a file: %s", path)
	}
	if info.Size() > maxMarkdownBytes {
		return "", fmt.Errorf("file is too large to preview (%d bytes, max %d)", info.Size(), maxMarkdownBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func statSig(path string) panelContentSig {
	info, err := os.Stat(path)
	if err != nil {
		return panelContentSig{missing: true}
	}
	return panelContentSig{mod: info.ModTime().UnixNano(), size: info.Size()}
}

// broadcastPanelContent pushes a panel's current rendered content to all
// clients (live reload). Clients that don't have the panel ignore it.
func (d *Daemon) broadcastPanelContent(workspaceID, panelID, kind, path, content string, readErr error) {
	msg := protocol.WorkspacePanelContentMessage{
		Event:       protocol.EventWorkspacePanelContent,
		WorkspaceID: workspaceID,
		PanelID:     panelID,
		PanelKind:   kind,
		Path:        path,
		Content:     content,
	}
	if readErr != nil {
		msg.Error = protocol.Ptr(readErr.Error())
	}
	d.wsHub.BroadcastValue(msg)
}

// broadcastPanelContentNow reads and broadcasts a single panel's content
// immediately, so freshly opened panels show their file without waiting for the
// next poll tick.
func (d *Daemon) broadcastPanelContentNow(workspaceID, panelID string) {
	kind, path, found := d.panelFilePath(workspaceID, panelID)
	if !found {
		return
	}
	content, readErr := readMarkdownFile(path)
	d.broadcastPanelContent(workspaceID, panelID, kind, path, content, readErr)
}

// handleWorkspacePanelContentGet replies to a client's pull request for a
// panel's current content (used on first render).
func (d *Daemon) handleWorkspacePanelContentGet(client *wsClient, msg *protocol.WorkspacePanelContentGetMessage) {
	kind, path, found := d.panelFilePath(msg.WorkspaceID, msg.PanelID)
	if !found {
		d.sendCommandError(client, protocol.CmdWorkspacePanelContentGet, fmt.Sprintf("panel not found: %s", msg.PanelID))
		return
	}
	content, readErr := readMarkdownFile(path)
	reply := protocol.WorkspacePanelContentMessage{
		Event:       protocol.EventWorkspacePanelContent,
		WorkspaceID: msg.WorkspaceID,
		PanelID:     msg.PanelID,
		PanelKind:   kind,
		Path:        path,
		Content:     content,
	}
	if readErr != nil {
		reply.Error = protocol.Ptr(readErr.Error())
	}
	d.sendToClient(client, reply)
}

// handleOpenMarkdown docks (or retargets) the markdown panel for a file. Sent by
// the `attn open` CLI over the unix socket. With no session it targets the
// currently selected session's workspace.
func (d *Daemon) handleOpenMarkdown(conn net.Conn, msg *protocol.OpenMarkdownMessage) {
	path := strings.TrimSpace(msg.Path)
	if path == "" {
		d.sendError(conn, "open_markdown: path is required")
		return
	}

	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	if sessionID == "" {
		d.sendError(conn, "open_markdown: no session selected; open a session in attn or pass --session")
		return
	}

	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok {
		d.sendError(conn, fmt.Sprintf("open_markdown: no workspace found for session %s", sessionID))
		return
	}

	if err := d.dockPanel(workspaceID, paneID, markdownPanelID, string(workspacelayout.PanelKindMarkdown), path, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		d.sendError(conn, fmt.Sprintf("open_markdown: %v", err))
		return
	}
	d.broadcastPanelContentNow(workspaceID, markdownPanelID)
	d.logf("open_markdown: docked %s into workspace %s (session %s)", path, workspaceID, sessionID)
	d.sendOK(conn)
}

// runMarkdownContentWatcher polls open markdown files for changes and broadcasts
// fresh content. It re-derives the watch set from the store every tick, so it
// needs no hooks into layout mutations and self-heals across restarts.
func (d *Daemon) runMarkdownContentWatcher(done <-chan struct{}) {
	ticker := time.NewTicker(markdownPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			d.pollMarkdownOnce()
		}
	}
}

// pollMarkdownOnce runs a single watch pass: rebuild the set of open markdown
// panels from the store, drop ones that disappeared, and broadcast content for
// any whose file changed since last seen.
func (d *Daemon) pollMarkdownOnce() {
	for _, ref := range d.collectChangedMarkdownPanels() {
		content, readErr := readMarkdownFile(ref.path)
		d.broadcastPanelContent(ref.workspaceID, ref.panelID, string(workspacelayout.PanelKindMarkdown), ref.path, content, readErr)
	}
}

// collectChangedMarkdownPanels derives the current set of open markdown panels
// from the store and returns those whose file changed (or is newly opened) since
// the previous call, updating the seen fingerprints. Panels that disappeared are
// pruned. Split out from pollMarkdownOnce so the live-reload change detection is
// testable without the websocket fan-out.
func (d *Daemon) collectChangedMarkdownPanels() []markdownPanelRef {
	if d.store == nil {
		return nil
	}

	desired := make(map[string]markdownPanelRef)
	for _, workspaceID := range d.store.WorkspaceLayoutIDs() {
		snapshot := d.store.GetWorkspaceLayout(workspaceID)
		if snapshot == nil {
			continue
		}
		for _, leaf := range workspacelayout.PanelLeaves(snapshot.Layout) {
			if leaf.PanelKind != string(workspacelayout.PanelKindMarkdown) {
				continue
			}
			path := strings.TrimSpace(leaf.PanelParams)
			if path == "" {
				continue
			}
			key := workspaceID + "\x00" + leaf.PanelID
			desired[key] = markdownPanelRef{workspaceID: workspaceID, panelID: leaf.PanelID, path: path}
		}
	}

	d.markdownSeenMu.Lock()
	defer d.markdownSeenMu.Unlock()
	if d.markdownSeen == nil {
		d.markdownSeen = make(map[string]panelContentSig)
	}
	for key := range d.markdownSeen {
		if _, ok := desired[key]; !ok {
			delete(d.markdownSeen, key)
		}
	}
	var changed []markdownPanelRef
	for key, ref := range desired {
		sig := statSig(ref.path)
		if prev, had := d.markdownSeen[key]; had && prev == sig {
			continue
		}
		d.markdownSeen[key] = sig
		changed = append(changed, ref)
	}
	return changed
}
