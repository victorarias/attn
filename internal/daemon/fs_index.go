package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/git"
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
// check here — then enumerates it bounded by maxFsIndexEntries. extensions
// filters the result server-side (see indexRoot). Replies with an
// fs_index_result event correlated by requestID.
func (d *Daemon) handleFsIndex(client *wsClient, requestID, rawRoot string, extensions []string) {
	var files []string
	var truncated bool
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil {
		files, truncated, err = indexRoot(root, maxFsIndexEntries, extensions)
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

// normalizeExtensions lowercases the requested extensions and strips a leading
// dot, so a client may send "md" or ".md". An empty result means "every file".
func normalizeExtensions(extensions []string) []string {
	normalized := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ext), ".")))
		if ext != "" {
			normalized = append(normalized, ext)
		}
	}
	return normalized
}

// matchesExtension reports whether name has one of the wanted extensions. No
// filter means everything matches.
func matchesExtension(name string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return false
	}
	for _, want := range extensions {
		if ext == want {
			return true
		}
	}
	return false
}

// hiddenPath reports whether any component of a root-relative slash path is
// dot-prefixed. fs_index has always hidden dot files and never descended
// dot-directories; git enumeration lists them, so it applies the same rule to
// keep both enumerations returning the same set.
func hiddenPath(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

// indexRoot enumerates the files under root, returning their root-relative
// slash paths sorted lexicographically plus whether the enumeration was
// truncated at cap entries. extensions (dotless, case-insensitive) filter the
// result, and the cap applies AFTER filtering — otherwise a large repository
// exhausts the cap on files nobody asked for and the documents being searched
// for silently vanish from the tail of the alphabet.
//
// Inside a git repository the file list comes from git (tracked files plus
// untracked-but-not-ignored ones), which honors .gitignore and reads an index
// git already maintains instead of stat-ing the whole tree. Outside one — or
// if git fails for any reason — it falls back to walking the tree.
//
// cap is injected so tests can exercise truncation with a tiny value instead
// of the real maxFsIndexEntries.
//
// normalizeExternalRoot only canonicalizes the deepest EXISTING ancestor, so a
// root that does not exist (or a root that is a file, not a directory) can
// still pass resolveFsRoot. Without an explicit check here, WalkDir's first
// callback invocation for such a root fires with a non-nil err and a nil
// DirEntry, the generic "skip and continue" branch below treats that as
// nothing-to-walk, and the whole call silently succeeds with zero files —
// fs_index would report success+empty for a root fs_list correctly errors on.
// Stat first and fail loudly instead.
func indexRoot(root string, cap int, extensions []string) ([]string, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("root %s is not a directory", root)
	}
	wanted := normalizeExtensions(extensions)
	if files, truncated, ok := indexRootViaGit(root, cap, wanted); ok {
		return files, truncated, nil
	}
	return indexRootViaWalk(root, cap, wanted)
}

// indexRootViaGit lists root's files through `git ls-files`, run inside root so
// paths come back relative to it. ok=false means git could not answer (root is
// not in a repository, git is missing, the command failed or timed out) and the
// caller should fall back to walking.
func indexRootViaGit(root string, cap int, extensions []string) (files []string, truncated bool, ok bool) {
	// -z: NUL-separated, so paths with spaces, newlines, or non-ASCII bytes
	// come back verbatim instead of git's quoted form.
	out, err := git.Output(git.OpMetadata, root,
		"ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false, false
	}

	seen := make(map[string]struct{})
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || hiddenPath(rel) || !matchesExtension(rel, extensions) {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		// git lists index entries, which include symlinks, submodule gitlinks,
		// and files deleted from the working tree. fs_read only serves regular
		// files, so drop anything else rather than advertising it as openable.
		info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if len(files) >= cap {
			truncated = true
			break
		}
		seen[rel] = struct{}{}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, truncated, true
}

// indexRootViaWalk walks root recursively. It skips any directory whose name
// starts with "." or is named "node_modules" (not descended at all),
// dot-prefixed files, and any non-regular-file entry — symlinks, FIFOs,
// sockets, device nodes — via DirEntry.Type().IsRegular(), a no-syscall
// type-bits check (a symlinked dir is also never descended by WalkDir). This
// matters beyond symlinks: a FIFO or socket that slipped into the list would be
// advertised as an openable file when fs_read rejects anything that isn't a
// regular file. A per-entry walk error (e.g. permission denied) skips that
// entry/subtree without failing the whole index.
func indexRootViaWalk(root string, cap int, extensions []string) ([]string, bool, error) {
	var files []string
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if !matchesExtension(name, extensions) {
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
