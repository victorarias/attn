package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// markdownTileIDPrefix prefixes every markdown tile id. The full id is derived
// from the file path (see markdownTileIDForPath), so each open file gets its
// own tile and multiple markdown tiles can coexist in one workspace.
const markdownTileIDPrefix = "tile-markdown-"

// markdownTileIDForPath derives the stable tile id for a markdown file:
// the prefix plus the first 16 hex chars of sha256 over the absolute path.
// Reopening the same path lands on the same id, which is what makes
// open-markdown reuse an existing tile instead of stacking duplicates.
func markdownTileIDForPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return markdownTileIDPrefix + hex.EncodeToString(sum[:8])
}

// markdownPollInterval is how often the content watcher restats open markdown
// files to detect on-disk changes. Sub-second keeps live reload feeling instant
// without the complexity of per-file OS watches (which editors' atomic saves
// routinely break).
const markdownPollInterval = 750 * time.Millisecond

// markdownHashPollInterval catches same-size rewrites whose modification time
// is preserved without continuously rereading unchanged files.
const markdownHashPollInterval = 5 * time.Second

// maxMarkdownBytes caps how large a markdown file the daemon will read into a
// tile. encoding/json can expand a byte to six bytes; keeping the raw preview
// at 1 MiB leaves room beneath the remote relay's 8 MiB message limit.
const maxMarkdownBytes = 1 << 20

// tileContentSig fingerprints a file between polls. The content hash catches
// same-size rewrites even when an editor restores the previous modification
// time or the filesystem timestamp granularity is coarse.
type tileContentSig struct {
	mod           int64
	size          int64
	hash          [sha256.Size]byte
	hasHash       bool
	missing       bool
	hashCheckedAt time.Time
}

type markdownTileRef struct {
	workspaceID string
	tileID      string
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
	oldID := d.selectedSessionID
	d.selectedSessionID = sessionID
	if workspaceID, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
		d.selectedWorkspaceID = workspaceID
	} else {
		d.selectedWorkspaceID = ""
	}
	d.selectedSessionMu.Unlock()
	// Pause the now-active session's countdown and resume the one we left, so the
	// active session never auto-fires while the user is in it.
	if oldID != sessionID {
		d.updateNudgeSelection(oldID, sessionID)
	}
}

func (d *Daemon) currentlySelectedSession() string {
	d.selectedSessionMu.RLock()
	defer d.selectedSessionMu.RUnlock()
	return d.selectedSessionID
}

func (d *Daemon) setSelectedWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	d.selectedSessionMu.Lock()
	d.selectedWorkspaceID = workspaceID
	d.selectedSessionMu.Unlock()
}

func (d *Daemon) currentlySelectedWorkspace() string {
	d.selectedSessionMu.RLock()
	defer d.selectedSessionMu.RUnlock()
	return d.selectedWorkspaceID
}

// tileFilePath resolves a tile's persisted params into a kind + file path.
func (d *Daemon) tileFilePath(workspaceID, tileID string) (kind, path string, found bool) {
	if d.store == nil {
		return "", "", false
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return "", "", false
	}
	for _, leaf := range workspacelayout.TileLeaves(snapshot.Layout) {
		if leaf.TileID == tileID {
			return leaf.TileKind, strings.TrimSpace(leaf.TileParams), true
		}
	}
	return "", "", false
}

func (d *Daemon) tileStillPointsTo(workspaceID, tileID, kind, path string) bool {
	currentKind, currentPath, found := d.tileFilePath(workspaceID, tileID)
	return found && currentKind == kind && currentPath == path
}

// readMarkdownFile reads a markdown file for a tile, returning a friendly error
// (rather than failing) when the file is missing, a directory, or oversized so
// the tile can render a clear state instead of going blank.
func readMarkdownFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no file is associated with this tile")
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

func statSig(path string) tileContentSig {
	info, err := os.Stat(path)
	if err != nil {
		return tileContentSig{missing: true}
	}
	return tileContentSig{mod: info.ModTime().UnixNano(), size: info.Size()}
}

func refreshTileContentHash(path string, sig tileContentSig, now time.Time) tileContentSig {
	content, err := readMarkdownFile(path)
	if err == nil {
		sig.hash = sha256.Sum256([]byte(content))
		sig.hasHash = true
	}
	sig.hashCheckedAt = now
	return sig
}

func tileContentSubscriptionKey(workspaceID, tileID string) string {
	return workspaceID + "\x00" + tileID
}

const (
	maxTileContentSubscriptions = 128
	tileContentPendingTTL       = 30 * time.Second
)

func (c *wsClient) prunePendingTileContentLocked(now time.Time) {
	for key, createdAt := range c.tileContentPending {
		if now.Sub(createdAt) >= tileContentPendingTTL {
			delete(c.tileContentPending, key)
		}
	}
}

func (c *wsClient) subscribeTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	if c.tileContentSubscriptions == nil {
		c.tileContentSubscriptions = make(map[string]struct{})
	}
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if len(c.tileContentSubscriptions) >= maxTileContentSubscriptions {
		return false
	}
	c.tileContentSubscriptions[key] = struct{}{}
	return true
}

func (c *wsClient) notePendingTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	now := time.Now()
	c.prunePendingTileContentLocked(now)
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if c.tileContentPending == nil {
		c.tileContentPending = make(map[string]time.Time)
	}
	if _, ok := c.tileContentPending[key]; ok {
		return true
	}
	if len(c.tileContentSubscriptions)+len(c.tileContentPending) >= maxTileContentSubscriptions {
		return false
	}
	c.tileContentPending[key] = now
	return true
}

func (c *wsClient) cancelPendingTileContent(workspaceID, tileID string) {
	if c == nil {
		return
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	delete(c.tileContentPending, key)
}

func (c *wsClient) resolvePendingTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	c.prunePendingTileContentLocked(time.Now())
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if _, ok := c.tileContentPending[key]; !ok {
		return false
	}
	delete(c.tileContentPending, key)
	if len(c.tileContentSubscriptions) >= maxTileContentSubscriptions {
		return false
	}
	if c.tileContentSubscriptions == nil {
		c.tileContentSubscriptions = make(map[string]struct{})
	}
	c.tileContentSubscriptions[key] = struct{}{}
	return true
}

func (c *wsClient) wantsTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.RLock()
	defer c.tileContentMu.RUnlock()
	_, ok := c.tileContentSubscriptions[key]
	return ok
}

func (c *wsClient) pruneTileContentSubscriptions(workspaceID string, activeTileIDs map[string]struct{}) {
	if c == nil {
		return
	}
	prefix := strings.TrimSpace(workspaceID) + "\x00"
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	for key := range c.tileContentSubscriptions {
		if strings.HasPrefix(key, prefix) {
			if _, ok := activeTileIDs[key]; !ok {
				delete(c.tileContentSubscriptions, key)
			}
		}
	}
	for key := range c.tileContentPending {
		if strings.HasPrefix(key, prefix) {
			if _, ok := activeTileIDs[key]; !ok {
				delete(c.tileContentPending, key)
			}
		}
	}
}

func (d *Daemon) hasTileContentSubscribers(workspaceID, tileID string) bool {
	return d.wsHub != nil && d.wsHub.AnyClientMatches(func(client *wsClient) bool {
		return client.wantsTileContent(workspaceID, tileID)
	})
}

func (d *Daemon) pruneTileContentSubscriptionsForLayout(workspaceID string, layout *workspacelayout.Node) {
	if d.wsHub == nil {
		return
	}
	activeTileIDs := make(map[string]struct{})
	if layout != nil {
		for _, leaf := range workspacelayout.TileLeaves(*layout) {
			activeTileIDs[tileContentSubscriptionKey(workspaceID, leaf.TileID)] = struct{}{}
		}
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		client.pruneTileContentSubscriptions(workspaceID, activeTileIDs)
	})
}

func (d *Daemon) pruneTileContentSubscriptionsForWorkspace(workspaceID string) {
	if d.store == nil {
		return
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		d.pruneTileContentSubscriptionsForLayout(workspaceID, nil)
		return
	}
	d.pruneTileContentSubscriptionsForLayout(workspaceID, &snapshot.Layout)
}

// broadcastTileContent pushes live reloads only to clients that requested this
// tile. File bodies must not fan out to unrelated web or relay clients.
func (d *Daemon) broadcastTileContent(workspaceID, tileID, kind, path, content string, readErr error) {
	if !d.tileStillPointsTo(workspaceID, tileID, kind, path) {
		return
	}
	msg := protocol.WorkspaceTileContentMessage{
		Event:       protocol.EventWorkspaceTileContent,
		WorkspaceID: workspaceID,
		TileID:      tileID,
		TileKind:    kind,
		Path:        path,
		Content:     content,
	}
	if readErr != nil {
		msg.Error = protocol.Ptr(readErr.Error())
	}
	d.wsHub.SendValueToMatchingClients(msg, func(client *wsClient) bool {
		return client.wantsTileContent(workspaceID, tileID)
	})
}

// broadcastTileContentNow reads and broadcasts a single tile's content
// immediately, so freshly opened tiles show their file without waiting for the
// next poll tick.
func (d *Daemon) broadcastTileContentNow(workspaceID, tileID string) {
	kind, path, found := d.tileFilePath(workspaceID, tileID)
	if !found || kind != string(workspacelayout.TileKindMarkdown) {
		return
	}
	content, readErr := readMarkdownFile(path)
	d.broadcastTileContent(workspaceID, tileID, kind, path, content, readErr)
}

// handleWorkspaceTileContentGet replies to a client's pull request for a
// tile's current content (used on first render).
func (d *Daemon) handleWorkspaceTileContentGet(client *wsClient, msg *protocol.WorkspaceTileContentGetMessage) {
	kind, _, found := d.tileFilePath(msg.WorkspaceID, msg.TileID)
	if !found {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("tile not found: %s", msg.TileID))
		return
	}
	if kind != string(workspacelayout.TileKindMarkdown) {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("unsupported tile kind: %s", kind))
		return
	}
	if !client.subscribeTileContent(msg.WorkspaceID, msg.TileID) {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, "too many tile content subscriptions")
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		kind, path, found := d.tileFilePath(msg.WorkspaceID, msg.TileID)
		if !found {
			d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("tile not found: %s", msg.TileID))
			return
		}
		if kind != string(workspacelayout.TileKindMarkdown) {
			d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("unsupported tile kind: %s", kind))
			return
		}
		content, readErr := readMarkdownFile(path)
		if !d.tileStillPointsTo(msg.WorkspaceID, msg.TileID, kind, path) {
			continue
		}
		reply := protocol.WorkspaceTileContentMessage{
			Event:       protocol.EventWorkspaceTileContent,
			WorkspaceID: msg.WorkspaceID,
			TileID:      msg.TileID,
			TileKind:    kind,
			Path:        path,
			Content:     content,
		}
		if readErr != nil {
			reply.Error = protocol.Ptr(readErr.Error())
		}
		d.sendToClient(client, reply)
		return
	}
	d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, "tile changed while content was loading; retry")
}

// openMarkdownTile docks (or reuses) the markdown tile for a file in the
// workspace that owns sessionID, binding the tile to that session. Shared by
// the `attn open` unix-socket path and the websocket cmd+click path. When the
// file is already open in the workspace the existing tile keeps its position
// and is rebound to the requesting session instead of being re-docked.
func (d *Daemon) openMarkdownTile(path, sessionID string) (workspaceID, tileID string, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("path is required")
	}
	// The tile id contract is sha256(absolute path): reject relative paths
	// (they would resolve against the daemon's cwd) and Clean so spellings
	// like /a/./b.md, /a//b.md, and /a/x/../b.md all land on one tile.
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("path must be absolute: %s", path)
	}
	path = filepath.Clean(path)
	if sessionID == "" {
		return "", "", fmt.Errorf("no session selected; open a session in attn or pass --session")
	}
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok {
		return "", "", fmt.Errorf("no workspace found for session %s", sessionID)
	}

	// Serialize check-then-dock: concurrent opens of different files share
	// last-write-wins layout snapshots, so an unserialized second dock would
	// silently drop the first tile.
	d.openMarkdownMu.Lock()
	defer d.openMarkdownMu.Unlock()

	tileID = markdownTileIDForPath(path)
	alreadyOpen := false
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		alreadyOpen = workspacelayout.HasTile(snapshot.Layout, tileID)
		if !alreadyOpen {
			// Layouts persisted before per-path tile ids used the fixed id
			// "tile-markdown". Match legacy tiles by kind+path so reopening
			// their file reuses them instead of docking a duplicate.
			for _, leaf := range workspacelayout.TileLeaves(snapshot.Layout) {
				if leaf.TileKind == string(workspacelayout.TileKindMarkdown) && leaf.TileParams == path {
					tileID = leaf.TileID
					alreadyOpen = true
					break
				}
			}
		}
	}
	if alreadyOpen {
		if err := d.rebindTileSession(workspaceID, tileID, sessionID); err != nil {
			return "", "", err
		}
	} else if err := d.dockTile(workspaceID, paneID, tileID, string(workspacelayout.TileKindMarkdown), path, sessionID, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		return "", "", err
	}
	d.broadcastTileContentNow(workspaceID, tileID)
	return workspaceID, tileID, nil
}

// rebindTileSession points an existing tile's session binding at sessionID,
// persisting and broadcasting the layout when the binding actually changes.
func (d *Daemon) rebindTileSession(workspaceID, tileID, sessionID string) error {
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}
	if current, ok := workspacelayout.TileSessionIDByID(snapshot.Layout, tileID); ok && current == sessionID {
		return nil
	}
	layout, ok := workspacelayout.UpdateTileSessionID(snapshot.Layout, tileID, sessionID)
	if !ok {
		return fmt.Errorf("tile not found: %s", tileID)
	}
	snapshot.Layout = layout
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		return err
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return nil
}

// handleOpenMarkdown docks (or reuses) a markdown tile for a file. Sent by
// the `attn open` CLI over the unix socket. With no session it targets the
// currently selected session's workspace.
func (d *Daemon) handleOpenMarkdown(conn net.Conn, msg *protocol.OpenMarkdownMessage) {
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	workspaceID, tileID, err := d.openMarkdownTile(msg.Path, sessionID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_markdown: %v", err))
		return
	}
	d.logf("open_markdown: docked %s as %s into workspace %s (session %s)", strings.TrimSpace(msg.Path), tileID, workspaceID, sessionID)
	d.sendOK(conn)
}

// handleOpenMarkdownWS is the websocket flavor of open_markdown, used by the
// frontend when the user cmd+clicks a markdown path in a session terminal.
// The clicked pane's session id rides in the message and becomes the tile's
// session binding.
func (d *Daemon) handleOpenMarkdownWS(client *wsClient, msg *protocol.OpenMarkdownMessage) {
	result := protocol.OpenMarkdownResultMessage{
		Event:   protocol.EventOpenMarkdownResult,
		Success: true,
		Path:    strings.TrimSpace(msg.Path),
	}
	if requestID := strings.TrimSpace(protocol.Deref(msg.RequestID)); requestID != "" {
		result.RequestID = protocol.Ptr(requestID)
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	workspaceID, tileID, err := d.openMarkdownTile(msg.Path, sessionID)
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.WorkspaceID = protocol.Ptr(workspaceID)
	result.TileID = protocol.Ptr(tileID)
	d.logf("open_markdown(ws): docked %s as %s into workspace %s (session %s)", result.Path, tileID, workspaceID, sessionID)
	d.sendToClient(client, result)
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
// tiles from the store, drop ones that disappeared, and broadcast content for
// any whose file changed since last seen.
func (d *Daemon) pollMarkdownOnce() {
	for _, ref := range d.collectChangedMarkdownTiles() {
		content, readErr := readMarkdownFile(ref.path)
		d.broadcastTileContent(ref.workspaceID, ref.tileID, string(workspacelayout.TileKindMarkdown), ref.path, content, readErr)
	}
}

// collectChangedMarkdownTiles derives the current set of open markdown tiles
// from the store and returns those whose file changed (or is newly opened) since
// the previous call, updating the seen fingerprints. Tiles that disappeared are
// pruned. Split out from pollMarkdownOnce so the live-reload change detection is
// testable without the websocket fan-out.
func (d *Daemon) collectChangedMarkdownTiles() []markdownTileRef {
	if d.store == nil {
		return nil
	}

	desired := make(map[string]markdownTileRef)
	for _, workspaceID := range d.store.WorkspaceLayoutIDs() {
		snapshot := d.store.GetWorkspaceLayout(workspaceID)
		if snapshot == nil {
			continue
		}
		for _, leaf := range workspacelayout.TileLeaves(snapshot.Layout) {
			if leaf.TileKind != string(workspacelayout.TileKindMarkdown) {
				continue
			}
			if !d.hasTileContentSubscribers(workspaceID, leaf.TileID) {
				continue
			}
			path := strings.TrimSpace(leaf.TileParams)
			if path == "" {
				continue
			}
			key := workspaceID + "\x00" + leaf.TileID
			desired[key] = markdownTileRef{workspaceID: workspaceID, tileID: leaf.TileID, path: path}
		}
	}

	d.markdownSeenMu.Lock()
	defer d.markdownSeenMu.Unlock()
	if d.markdownSeen == nil {
		d.markdownSeen = make(map[string]tileContentSig)
	}
	for key := range d.markdownSeen {
		if _, ok := desired[key]; !ok {
			delete(d.markdownSeen, key)
		}
	}
	var changed []markdownTileRef
	now := time.Now()
	for key, ref := range desired {
		sig := statSig(ref.path)
		prev, had := d.markdownSeen[key]
		if !had || prev.mod != sig.mod || prev.size != sig.size || prev.missing != sig.missing {
			d.markdownSeen[key] = refreshTileContentHash(ref.path, sig, now)
			changed = append(changed, ref)
			continue
		}
		if now.Sub(prev.hashCheckedAt) < markdownHashPollInterval {
			continue
		}
		next := refreshTileContentHash(ref.path, sig, now)
		d.markdownSeen[key] = next
		if prev.hasHash != next.hasHash || prev.hash != next.hash {
			changed = append(changed, ref)
		}
	}
	return changed
}
