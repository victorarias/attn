package daemon

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
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
// panel. encoding/json can expand a byte to six bytes; keeping the raw preview
// at 1 MiB leaves room beneath the remote relay's 8 MiB message limit.
const maxMarkdownBytes = 1 << 20

// panelContentSig fingerprints a file between polls. The content hash catches
// same-size rewrites even when an editor restores the previous modification
// time or the filesystem timestamp granularity is coarse.
type panelContentSig struct {
	mod     int64
	size    int64
	hash    [sha256.Size]byte
	hasHash bool
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
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > maxMarkdownBytes {
		return "", fmt.Errorf("file is too large to preview (%d bytes, max %d)", info.Size(), maxMarkdownBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMarkdownBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxMarkdownBytes {
		return "", fmt.Errorf("file is too large to preview (more than %d bytes)", maxMarkdownBytes)
	}
	return string(data), nil
}

func statSig(path string) panelContentSig {
	info, err := os.Stat(path)
	if err != nil {
		return panelContentSig{missing: true}
	}
	sig := panelContentSig{mod: info.ModTime().UnixNano(), size: info.Size()}
	content, err := readMarkdownFile(path)
	if err == nil {
		sig.hash = sha256.Sum256([]byte(content))
		sig.hasHash = true
	}
	return sig
}

func panelContentSubscriptionKey(workspaceID, panelID string) string {
	return workspaceID + "\x00" + panelID
}

func (c *wsClient) subscribePanelContent(workspaceID, panelID string) {
	if c == nil {
		return
	}
	key := panelContentSubscriptionKey(workspaceID, panelID)
	c.panelContentMu.Lock()
	defer c.panelContentMu.Unlock()
	if c.panelContentSubscriptions == nil {
		c.panelContentSubscriptions = make(map[string]struct{})
	}
	c.panelContentSubscriptions[key] = struct{}{}
}

func (c *wsClient) wantsPanelContent(workspaceID, panelID string) bool {
	if c == nil {
		return false
	}
	key := panelContentSubscriptionKey(workspaceID, panelID)
	c.panelContentMu.RLock()
	defer c.panelContentMu.RUnlock()
	_, ok := c.panelContentSubscriptions[key]
	return ok
}

// broadcastPanelContent pushes live reloads only to clients that requested this
// panel. File bodies must not fan out to unrelated web or relay clients.
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
	d.wsHub.SendValueToMatchingClients(msg, func(client *wsClient) bool {
		return client.wantsPanelContent(workspaceID, panelID)
	})
}

// broadcastPanelContentNow reads and broadcasts a single panel's content
// immediately, so freshly opened panels show their file without waiting for the
// next poll tick.
func (d *Daemon) broadcastPanelContentNow(workspaceID, panelID string) {
	kind, path, found := d.panelFilePath(workspaceID, panelID)
	if !found || kind != string(workspacelayout.PanelKindMarkdown) {
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
	if kind != string(workspacelayout.PanelKindMarkdown) {
		d.sendCommandError(client, protocol.CmdWorkspacePanelContentGet, fmt.Sprintf("unsupported panel kind: %s", kind))
		return
	}
	client.subscribePanelContent(msg.WorkspaceID, msg.PanelID)
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
