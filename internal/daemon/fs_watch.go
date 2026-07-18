package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// maxFsWatchers caps the number of live non-notebook watchers at once. Each is
// a goroutine + an OS watch handle (kqueue fd), so this is a resource bound, not
// a UI limit — a client that wants more must fs_unwatch something first.
const maxFsWatchers = 16

// fsRootWatch is one live generic-root watcher plus the clients holding it open.
// Never created for the notebook root — that watcher is always-on via
// ensureNotebookWatcher, independent of any client subscription.
type fsRootWatch struct {
	watcher *notebook.Watcher
	refs    map[*wsClient]int // per-client refcount
}

// handleFsWatch subscribes client to fs_changed broadcasts for rawRoot: it
// resolves the root through resolveFsRoot — the single chokepoint that gates
// an explicit root on the authenticated app client, so fs_watch inherits that
// gate with no separate check here — and for any root other than the notebook
// root, starts a live watcher on first subscriber (refcounted so repeat calls
// and multiple clients share one watcher). The notebook root is always watched
// already, so fs_watch on it is a success no-op that does not touch the
// registry. Replies with an fs_watch_result event correlated by requestID.
func (d *Daemon) handleFsWatch(client *wsClient, requestID, rawRoot string) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil && !d.isNotebookRoot(root) {
		err = d.addFsWatchRef(client, root)
	}
	msg := protocol.FsWatchResultMessage{
		Event:     protocol.EventFsWatchResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err == nil {
		msg.Root = protocol.Ptr(root)
	} else {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// handleFsUnwatch drops client's subscription to rawRoot, closing the watcher
// once no client holds it open. The notebook root is a success no-op (nothing
// to unwatch — it stays alive independent of subscriptions). Unwatching a root
// this client never watched is also a success no-op. Replies with an
// fs_unwatch_result event correlated by requestID.
func (d *Daemon) handleFsUnwatch(client *wsClient, requestID, rawRoot string) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil && !d.isNotebookRoot(root) {
		d.dropFsWatchRef(client, root)
	}
	msg := protocol.FsUnwatchResultMessage{
		Event:     protocol.EventFsUnwatchResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err == nil {
		msg.Root = protocol.Ptr(root)
	} else {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// addFsWatchRef increments client's ref on root's watcher, creating it (and the
// registry entry) on the first subscriber. Returns an error if root cannot be
// watched (e.g. it does not exist) or the live-watcher cap is exceeded.
func (d *Daemon) addFsWatchRef(client *wsClient, root string) error {
	d.fsWatchMu.Lock()
	defer d.fsWatchMu.Unlock()
	if d.fsWatchers == nil {
		d.fsWatchers = make(map[string]*fsRootWatch)
	}
	entry, ok := d.fsWatchers[root]
	if !ok {
		if len(d.fsWatchers) >= maxFsWatchers {
			return fmt.Errorf("too many watched roots")
		}
		w, err := notebook.NewWatcherWithCleaner(root, notebook.DefaultWatchDebounce, fsdoc.CleanPath, func(paths []string) {
			d.broadcastFsChanged(root, originExternal, paths...)
		})
		if err != nil {
			return err
		}
		entry = &fsRootWatch{watcher: w, refs: make(map[*wsClient]int)}
		d.fsWatchers[root] = entry
	}
	entry.refs[client]++
	return nil
}

// dropFsWatchRef decrements client's ref on root's watcher, deleting the ref (at
// zero) and closing+deleting the watcher entry once no client holds it. A no-op
// if root has no live registry entry or client never held a ref on it.
func (d *Daemon) dropFsWatchRef(client *wsClient, root string) {
	d.fsWatchMu.Lock()
	entry, ok := d.fsWatchers[root]
	if !ok {
		d.fsWatchMu.Unlock()
		return
	}
	if entry.refs[client] > 1 {
		entry.refs[client]--
		d.fsWatchMu.Unlock()
		return
	}
	delete(entry.refs, client)
	var toClose *notebook.Watcher
	if len(entry.refs) == 0 {
		delete(d.fsWatchers, root)
		toClose = entry.watcher
	}
	d.fsWatchMu.Unlock()
	// Close outside fsWatchMu: it joins the watcher's loop goroutine and can
	// block briefly.
	_ = toClose.Close()
}

// dropFsWatchClient removes client's refs from every fs_watch registry entry
// (called on websocket client disconnect), closing and deleting any entry that
// reaches zero clients as a result.
func (d *Daemon) dropFsWatchClient(client *wsClient) {
	d.fsWatchMu.Lock()
	var toClose []*notebook.Watcher
	for root, entry := range d.fsWatchers {
		if _, held := entry.refs[client]; !held {
			continue
		}
		delete(entry.refs, client)
		if len(entry.refs) == 0 {
			delete(d.fsWatchers, root)
			toClose = append(toClose, entry.watcher)
		}
	}
	d.fsWatchMu.Unlock()
	for _, w := range toClose {
		_ = w.Close()
	}
}

// stopFsWatchers closes every live registry watcher (daemon shutdown).
func (d *Daemon) stopFsWatchers() {
	d.fsWatchMu.Lock()
	watchers := d.fsWatchers
	d.fsWatchers = nil
	d.fsWatchMu.Unlock()
	for _, entry := range watchers {
		_ = entry.watcher.Close()
	}
}

// fsWatcherFor returns the live registry watcher for root, or nil if none is
// active (root is unwatched, or is the notebook root — which is never in this
// registry).
func (d *Daemon) fsWatcherFor(root string) *notebook.Watcher {
	d.fsWatchMu.Lock()
	defer d.fsWatchMu.Unlock()
	entry, ok := d.fsWatchers[root]
	if !ok {
		return nil
	}
	return entry.watcher
}

// sendFsChangedToWatchers delivers msg only to the clients currently holding
// an fs_watch ref on root — the audience restriction that keeps a generic
// editor root's absolute path and changed paths from leaking to a connected
// client that never subscribed to it. A no-op if root has no live registry
// entry (e.g. the last subscriber unwatched between the change firing and
// this call).
func (d *Daemon) sendFsChangedToWatchers(root string, msg protocol.FsChangedMessage) {
	d.fsWatchMu.Lock()
	entry, ok := d.fsWatchers[root]
	var clients []*wsClient
	if ok {
		clients = make([]*wsClient, 0, len(entry.refs))
		for c := range entry.refs {
			clients = append(clients, c)
		}
	}
	d.fsWatchMu.Unlock()
	for _, c := range clients {
		d.sendToClient(c, msg)
	}
}
