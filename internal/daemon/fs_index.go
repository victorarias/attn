package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// maxFsIndexEntries bounds a single fs_index walk. It is a server-side
// constant, not a client-controlled limit — the ⌘P finder needs a bound that
// cannot be widened by a request, so a walk that would exceed it stops early
// and reports truncated=true instead of returning a partial-but-unbounded
// list.
const maxFsIndexEntries = 25000

// handleFsIndex resolves rawRoot through resolveFsRoot — the same chokepoint
// every other fs_* command uses, so fs_index inherits the auth gate (an
// explicit root is honored only for the authenticated app client; an
// omitted/empty root always resolves to the notebook root) with no separate
// check here — then walks it bounded by maxFsIndexEntries. Replies with an
// fs_index_result event correlated by requestID.
func (d *Daemon) handleFsIndex(client *wsClient, requestID, rawRoot string) {
	var files []string
	var truncated bool
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil {
		files, truncated, err = indexRoot(root, maxFsIndexEntries)
	}
	msg := protocol.FsIndexResultMessage{
		Event:     protocol.EventFsIndexResult,
		RequestID: requestID,
		Success:   err == nil,
		Root:      root,
		Files:     files,
		Truncated: truncated,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
		msg.Files = []string{}
	}
	d.sendToClient(client, msg)
}

// indexRoot walks root recursively and returns every regular file's
// root-relative slash path, sorted lexicographically, plus whether the walk
// was truncated at cap entries. It skips any directory whose name starts with
// "." or is named "node_modules" (not descended at all), dot-prefixed files,
// and any non-regular-file entry — symlinks, FIFOs, sockets, device nodes —
// via DirEntry.Type().IsRegular(), a no-syscall type-bits check (a symlinked
// dir is also never descended by WalkDir). This matters beyond symlinks: a
// FIFO or socket that slipped into the list would be advertised as an
// openable file when fs_read rejects anything that isn't a regular file. A
// per-entry walk error (e.g. permission denied) skips that entry/subtree
// without failing the whole index. cap is injected so tests can exercise
// truncation with a tiny value instead of the real maxFsIndexEntries.
//
// normalizeExternalRoot only canonicalizes the deepest EXISTING ancestor, so a
// root that does not exist (or a root that is a file, not a directory) can
// still pass resolveFsRoot. Without an explicit check here, WalkDir's first
// callback invocation for such a root fires with a non-nil err and a nil
// DirEntry, the generic "skip and continue" branch below treats that as
// nothing-to-walk, and the whole call silently succeeds with zero files —
// fs_index would report success+empty for a root fs_list correctly errors on.
// Stat first and fail loudly instead.
func indexRoot(root string, cap int) ([]string, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("root %s is not a directory", root)
	}
	var files []string
	truncated := false
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip the entry (and, for a directory, its subtree) rather than
			// aborting the whole walk on one permission-denied or similar error.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= cap {
			truncated = true
			return fs.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(files)
	return files, truncated, nil
}
