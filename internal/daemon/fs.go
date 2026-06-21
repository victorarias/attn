package daemon

import (
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// fsStoreFor returns the daemon's single generic filesystem Store for the active
// root. The root is the SAME one the notebook uses (notebook.root): fsdoc is the
// raw layer beneath the curated notebook surface. The Store is cached and reused
// so writes serialize through one in-process writer, and rebuilt only when the
// resolved root changes. As with the notebook, every fs operation lazily ensures
// the one shared root watcher is running (so fs_changed fires for external edits).
func (d *Daemon) fsStoreFor() (*fsdoc.Store, error) {
	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	d.fsMu.Lock()
	if d.fsStore == nil || d.fsStore.Root() != root {
		d.fsStore = fsdoc.NewStore(root)
	}
	store := d.fsStore
	d.fsMu.Unlock()
	d.ensureNotebookWatcher(root)
	return store, nil
}

// broadcastFsChanged announces a filesystem change to all websocket clients.
// origin is agent|ui|external, the same vocabulary as notebook_changed.
func (d *Daemon) broadcastFsChanged(origin string, paths ...string) {
	d.broadcastMessage(protocol.FsChangedMessage{
		Event:  protocol.EventFsChanged,
		Paths:  paths,
		Origin: origin,
	})
}

// sendFsListWSResult lists one directory's immediate children and replies with an
// fs_list_result event correlated by requestID.
func (d *Daemon) sendFsListWSResult(client *wsClient, requestID, path string) {
	var entries []protocol.FsEntry
	store, err := d.fsStoreFor()
	if err == nil {
		var list []fsdoc.Entry
		if list, err = store.List(path); err == nil {
			entries = fsEntriesToProtocol(list)
		}
	}
	msg := protocol.FsListResultMessage{
		Event:     protocol.EventFsListResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendFsReadWSResult reads one file and replies with an fs_read_result event
// correlated by requestID.
func (d *Daemon) sendFsReadWSResult(client *wsClient, requestID, path string) {
	var result *protocol.FsReadResult
	store, err := d.fsStoreFor()
	if err == nil {
		var content []byte
		var hash string
		if content, hash, err = store.Read(path); err == nil {
			result = &protocol.FsReadResult{Path: path, Content: string(content), Hash: hash}
		}
	}
	msg := protocol.FsReadResultMessage{
		Event:     protocol.EventFsReadResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendFsWriteWSResult performs a hash-CAS write and replies with an
// fs_write_result event correlated by requestID. A conflict (the file changed on
// disk since the editor loaded it) is a successful result carrying conflict=true
// for the UI to reconcile, not an error. On a successful write it records the
// path as a self-write (so the shared watcher does not echo it as external) and
// broadcasts fs_changed(origin=ui).
func (d *Daemon) sendFsWriteWSResult(client *wsClient, requestID, path, content, baseHash string) {
	var result *protocol.FsWriteResult
	store, err := d.fsStoreFor()
	if err == nil {
		// Normalize the path to the canonical form fs_list/fs_changed key on, so the
		// echoed result.path and the broadcast match it (not the raw leading-slash
		// input). A path that fails to normalize still reaches Write, which returns
		// the precise error.
		changed := path
		if rel, cerr := fsdoc.CleanPath(path); cerr == nil {
			changed = rel
		}
		var hash string
		var conflict *fsdoc.Conflict
		if hash, conflict, err = store.Write(path, []byte(content), baseHash); err == nil {
			result = &protocol.FsWriteResult{Path: changed}
			if conflict != nil {
				result.Conflict = true
				if conflict.CurrentHash != "" {
					result.CurrentHash = protocol.Ptr(conflict.CurrentHash)
				}
			} else {
				result.Hash = protocol.Ptr(hash)
				// Content-aware self-write so the shared watcher does not surface this
				// UI edit as an external one. NoteSelfWrite keys on the notebook's
				// CleanPath, so it only registers a .md path — which is exactly the set
				// the watcher can report today; a non-.md write fires no watcher event,
				// so there is nothing to suppress for it either way.
				d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				d.broadcastFsChanged(originUI, changed)
			}
		}
	}
	msg := protocol.FsWriteResultMessage{
		Event:     protocol.EventFsWriteResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendFsExistsWSResult reports whether a path exists under the root and replies
// with an fs_exists_result event correlated by requestID. It does not read the
// file — the frontend uses it to flag an in-notebook link whose target note is
// missing. A path that fails to validate (escapes the root, dotfile) is an error
// the frontend treats as "unknown, leave the link unflagged", not as "missing".
func (d *Daemon) sendFsExistsWSResult(client *wsClient, requestID, path string) {
	var result *protocol.FsExistsResult
	store, err := d.fsStoreFor()
	if err == nil {
		var exists bool
		if exists, err = store.Exists(path); err == nil {
			result = &protocol.FsExistsResult{Path: path, Exists: exists}
		}
	}
	msg := protocol.FsExistsResultMessage{
		Event:     protocol.EventFsExistsResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// fsEntriesToProtocol converts store entries to their protocol shape.
func fsEntriesToProtocol(entries []fsdoc.Entry) []protocol.FsEntry {
	out := make([]protocol.FsEntry, len(entries))
	for i, e := range entries {
		out[i] = protocol.FsEntry{
			Path:  e.Path,
			Name:  e.Name,
			IsDir: e.IsDir,
			Size:  int(e.Size),
		}
		if e.Modified != "" {
			out[i].Modified = protocol.Ptr(e.Modified)
		}
	}
	return out
}
