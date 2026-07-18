package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// maxAssetMessageBytes bounds the entire marshaled fs_read_asset_result JSON
// message — the unit that actually hits the WebSocket transport, which has no
// other outbound cap. The raw read cap below is DERIVED from it, so the bound
// on the wire is the contract and the file-size cap follows.
const maxAssetMessageBytes = 8 << 20

// assetEnvelopeSlack sizes the PREFLIGHT file-size cap only: a fast reject
// before reading or encoding a file that's obviously too large. It is not the
// wire bound — assetMessageFits below computes that exactly per request,
// including arbitrarily long paths.
const assetEnvelopeSlack = 4 << 10

// maxAssetBytes bounds a single fs_read_asset read so base64(content) plus the
// envelope always fits maxAssetMessageBytes (base64 of n bytes is 4*ceil(n/3)).
const maxAssetBytes = (maxAssetMessageBytes - assetEnvelopeSlack) / 4 * 3

// assetMimeTypes is the explicit allowlist of image extensions fs_read_asset will
// serve. This IS the contract, not a convenience lookup: unlike mime.TypeByExtension
// it deliberately excludes everything non-image, since this surface exists only to
// render ![alt](path) images in the notebook editor without widening Tauri's fs
// permissions.
var assetMimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
}

// resolveFsRoot resolves the raw root a fs_* command asked to operate under:
// empty/blank means "the notebook root" (today's behavior, unchanged); a
// non-empty value must be a normalized-clean absolute path outside the attn
// data dir, same rule as notebook.root, worded for the fs surface.
func (d *Daemon) resolveFsRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return d.notebookRoot()
	}
	resolved, err := normalizeExternalRoot(raw)
	if err != nil {
		return "", fmt.Errorf("fs root %w", err)
	}
	return resolved, nil
}

// fsStoreFor returns the daemon's generic filesystem Store for rawRoot,
// resolving it first (see resolveFsRoot). Stores are cached per resolved root
// so writes to the same root serialize through one in-process writer. Only the
// notebook root gets the shared external-edit watcher today (ensureNotebookWatcher);
// other roots get a Store but no watcher — that lands with the non-text editor
// work (PR2). Returns the resolved root alongside the store so callers can key
// broadcasts and notebook-coupling decisions on it without re-resolving.
func (d *Daemon) fsStoreFor(rawRoot string) (*fsdoc.Store, string, error) {
	root, err := d.resolveFsRoot(rawRoot)
	if err != nil {
		return nil, "", err
	}
	d.fsMu.Lock()
	if d.fsStores == nil {
		d.fsStores = make(map[string]*fsdoc.Store)
	}
	store, ok := d.fsStores[root]
	if !ok {
		store = fsdoc.NewStore(root)
		d.fsStores[root] = store
	}
	d.fsMu.Unlock()
	if notebookRoot, nerr := d.notebookRoot(); nerr == nil && root == notebookRoot {
		d.ensureNotebookWatcher(root)
	}
	return store, root, nil
}

// broadcastFsChanged announces a filesystem change to all websocket clients.
// origin is agent|ui|external, the same vocabulary as notebook_changed. root is
// the resolved absolute root the change happened under.
func (d *Daemon) broadcastFsChanged(root, origin string, paths ...string) {
	d.broadcastMessage(protocol.FsChangedMessage{
		Event:  protocol.EventFsChanged,
		Paths:  paths,
		Origin: origin,
		Root:   root,
	})
}

// sendFsListWSResult lists one directory's immediate children and replies with an
// fs_list_result event correlated by requestID.
func (d *Daemon) sendFsListWSResult(client *wsClient, requestID, path, rawRoot string) {
	var entries []protocol.FsEntry
	store, _, err := d.fsStoreFor(rawRoot)
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
func (d *Daemon) sendFsReadWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsReadResult
	store, _, err := d.fsStoreFor(rawRoot)
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

// sendFsReadAssetWSResult reads one image file's bytes as base64 and replies with
// an fs_read_asset_result event correlated by requestID. Only extensions in
// assetMimeTypes are served; the extension is rejected before the file is read.
func (d *Daemon) sendFsReadAssetWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsReadAssetResult
	mimeType, err := assetMimeTypeFor(path)
	if err == nil {
		var store *fsdoc.Store
		if store, _, err = d.fsStoreFor(rawRoot); err == nil {
			var content []byte
			if content, _, err = store.ReadWithLimit(path, maxAssetBytes); err == nil {
				if len(content) > maxAssetBytes {
					err = fmt.Errorf("asset exceeds the %d byte read cap", maxAssetBytes)
				} else if fits, ferr := assetMessageFits(requestID, path, mimeType, len(content)); ferr != nil {
					err = ferr
				} else if !fits {
					err = fmt.Errorf("asset response exceeds the %d byte message cap", maxAssetMessageBytes)
				} else {
					result = &protocol.FsReadAssetResult{
						Path:       path,
						MimeType:   mimeType,
						DataBase64: base64.StdEncoding.EncodeToString(content),
					}
				}
			}
		}
	}
	msg := protocol.FsReadAssetResultMessage{
		Event:     protocol.EventFsReadAssetResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// assetMimeTypeFor returns the mime type for path's extension, or an error if the
// extension is not in the image allowlist.
func assetMimeTypeFor(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	mimeType, ok := assetMimeTypes[ext]
	if !ok {
		return "", errors.New("not a supported image asset")
	}
	return mimeType, nil
}

// assetMessageFits reports whether the complete fs_read_asset_result message for
// this request would marshal within maxAssetMessageBytes once rawLen content
// bytes are base64-encoded into it. The size is exact, not an estimate: base64
// output never needs JSON escaping, so the full message length is the marshaled
// empty-payload envelope plus EncodedLen(rawLen). This — not the preflight file
// cap — is the authoritative transport bound, and it accounts for the real
// (arbitrarily long) path.
func assetMessageFits(requestID, path, mimeType string, rawLen int) (bool, error) {
	probe := protocol.FsReadAssetResultMessage{
		Event:     protocol.EventFsReadAssetResult,
		RequestID: requestID,
		Success:   true,
		Result:    &protocol.FsReadAssetResult{Path: path, MimeType: mimeType},
	}
	envelope, err := json.Marshal(probe)
	if err != nil {
		return false, err
	}
	return len(envelope)+base64.StdEncoding.EncodedLen(rawLen) <= maxAssetMessageBytes, nil
}

// sendFsWriteWSResult performs a hash-CAS write and replies with an
// fs_write_result event correlated by requestID. A conflict (the file changed on
// disk since the editor loaded it) is a successful result carrying conflict=true
// for the UI to reconcile, not an error. On a successful write it records the
// path as a self-write (so the shared watcher does not echo it as external) and
// broadcasts fs_changed(origin=ui).
func (d *Daemon) sendFsWriteWSResult(client *wsClient, requestID, path, content, baseHash, rawRoot string) {
	var result *protocol.FsWriteResult
	store, root, err := d.fsStoreFor(rawRoot)
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
				if d.isNotebookRoot(root) {
					// Content-aware self-write so the shared watcher does not surface this
					// UI edit as an external one. NoteSelfWrite keys on the notebook's
					// CleanPath, so it only registers a .md path — which is exactly the set
					// the watcher can report today; a non-.md write fires no watcher event,
					// so there is nothing to suppress for it either way.
					d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				}
				d.broadcastFsChanged(root, originUI, changed)
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

func (d *Daemon) sendFsRenameWSResult(client *wsClient, requestID, oldPath, newPath, rawRoot string) {
	oldRel, oldErr := fsdoc.CleanPath(oldPath)
	newRel, newErr := fsdoc.CleanPath(newPath)
	err := errors.Join(oldErr, newErr)
	var result *protocol.FsRenameResult
	store, root, storeErr := d.fsStoreFor(rawRoot)
	if err == nil {
		err = storeErr
	}
	if err == nil {
		_, hash, readErr := store.Read(oldRel)
		if readErr != nil {
			err = readErr
		} else {
			inNotebook := d.isNotebookRoot(root)
			if inNotebook {
				d.noteNotebookSelfWrite(
					notebook.SelfWrite{Rel: oldRel},
					notebook.SelfWrite{Rel: newRel, Hash: hash},
				)
			}
			err = store.Rename(oldRel, newRel)
			if err == nil {
				result = &protocol.FsRenameResult{Path: oldRel, NewPath: newRel}
				if inNotebook {
					d.broadcastNotebookChanged(originUI, oldRel, newRel)
				}
				d.broadcastFsChanged(root, originUI, oldRel, newRel)
			}
		}
	}
	msg := protocol.FsRenameResultMessage{Event: protocol.EventFsRenameResult, RequestID: requestID, Success: err == nil, Result: result}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendFsDeleteWSResult(client *wsClient, requestID, path, rawRoot string) {
	rel, err := fsdoc.CleanPath(path)
	var result *protocol.FsDeleteResult
	store, root, storeErr := d.fsStoreFor(rawRoot)
	if err == nil {
		err = storeErr
	}
	if err == nil {
		inNotebook := d.isNotebookRoot(root)
		if inNotebook {
			d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel})
		}
		err = store.Delete(rel)
		if err == nil {
			result = &protocol.FsDeleteResult{Path: rel}
			if inNotebook {
				d.broadcastNotebookChanged(originUI, rel)
			}
			d.broadcastFsChanged(root, originUI, rel)
		}
	}
	msg := protocol.FsDeleteResultMessage{Event: protocol.EventFsDeleteResult, RequestID: requestID, Success: err == nil, Result: result}
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
func (d *Daemon) sendFsExistsWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsExistsResult
	store, _, err := d.fsStoreFor(rawRoot)
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

// isNotebookRoot reports whether root is the currently resolved notebook root
// (best-effort: a notebookRoot() error is treated as "not the notebook root",
// so a foreign fs root never accidentally couples to notebook broadcasts).
func (d *Daemon) isNotebookRoot(root string) bool {
	notebookRoot, err := d.notebookRoot()
	return err == nil && root == notebookRoot
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
