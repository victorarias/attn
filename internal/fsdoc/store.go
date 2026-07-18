// Package fsdoc is a generic filesystem view over a single root directory — the
// raw layer beneath the curated notebook. Where internal/notebook is markdown-
// and PARA-aware (frontmatter, links, a reserved layout), fsdoc is deliberately
// structure-blind: it lists, reads, and writes arbitrary files and directories
// exactly as they sit on disk, so a UI can render the real folder tree over any
// content. It shares the notebook's root (the notebook.root setting) and reuses
// the notebook's vetted symlink-containment guard and content hash, so both
// surfaces agree on what "inside the root" and "the bytes' hash" mean.
package fsdoc

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/notebook"
)

// MaxFileSize bounds a single fs_write so a runaway write cannot balloon the
// root. It mirrors notebook.MaxFileSize: the same root, the same sync-friendly
// goal. Reads use the same cap before allocating file contents for the WebSocket.
const MaxFileSize = 2 << 20 // 2 MiB

// Store is the generic filesystem store for one root directory. Writes serialize
// under mu and apply atomically (temp file + rename); reads take no lock. It is
// the single in-process writer for fs-originated mutations under this root.
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore returns a Store rooted at the given absolute directory. The directory
// need not exist yet. The root is cleaned so containment checks compare against a
// canonical path (a trailing slash would otherwise break the prefix test in abs).
func NewStore(root string) *Store {
	if root != "" {
		root = filepath.Clean(root)
	}
	return &Store{root: root}
}

// Root returns the absolute root directory.
func (s *Store) Root() string { return s.root }

// Entry is one immediate child of a listed directory: enough to render a tree
// node and decide whether to expand it (is_dir) or open it.
type Entry struct {
	Path     string // root-relative, slash-separated, e.g. "knowledge/areas/foo.md"
	Name     string // the base name, e.g. "foo.md"
	IsDir    bool
	Size     int64  // file byte size; 0 for a directory
	Modified string // file mtime (RFC3339); "" for a directory
}

// Conflict reports that a hash-CAS write did not apply because the on-disk
// content no longer matches the base the caller read. CurrentHash is the hash now
// on disk ("" when the target file is gone).
type Conflict struct {
	CurrentHash string
}

// NotFoundError is returned when a requested path does not exist.
type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("fsdoc: %s not found", e.Path) }

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// List returns the immediate children of dir (root-relative; "" = the root
// itself), files and subdirectories alike, sorted directories-first then by name.
// It is SHALLOW by design: a tree UI calls it once per expanded node. Dot-entries
// (.attn, .git, dotfiles) are hidden, and a child that resolves outside the root
// via a symlink is skipped rather than exposed. A missing root yields an empty
// list (it may not be created yet); a missing non-root directory yields a
// NotFoundError; a path that is a file, not a directory, is an error.
func (s *Store) List(dir string) ([]Entry, error) {
	rel, err := cleanRel(dir, true)
	if err != nil {
		return nil, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			if rel == "" {
				return []Entry{}, nil // root not created yet => empty
			}
			return nil, &NotFoundError{Path: dir}
		}
		return nil, statErr
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("fsdoc: %q is not a directory", dir)
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, dirent := range dirents {
		name := dirent.Name()
		if strings.HasPrefix(name, ".") {
			continue // hide .attn/, .git/, and any dotfile
		}
		childAbs := filepath.Join(abs, name)
		// The root is externally syncable, so a child can be a symlink pointing
		// outside it. Read/Write defend via abs; List must too, or it would expose
		// an outside file's name and size over the websocket. Skip such an entry.
		if notebook.EnsureWithinResolvedRoot(s.root, childAbs) != nil {
			continue
		}
		childInfo, ierr := dirent.Info()
		if ierr != nil {
			continue // vanished mid-scan
		}
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		e := Entry{Path: childRel, Name: name, IsDir: childInfo.IsDir()}
		if !childInfo.IsDir() {
			e.Size = childInfo.Size()
			e.Modified = childInfo.ModTime().UTC().Format(time.RFC3339)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // directories first, then files
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Read returns the raw bytes of a file and their content hash, bounded by
// MaxFileSize. A missing file yields a *NotFoundError.
func (s *Store) Read(p string) (content []byte, hash string, err error) {
	return s.ReadWithLimit(p, MaxFileSize)
}

// ReadWithLimit returns the raw bytes of a file and their content hash, bounded
// by maxBytes before allocating file contents. Callers must supply the limit for
// their transport or presentation surface; generic text reads use Read.
func (s *Store) ReadWithLimit(p string, maxBytes int64) (content []byte, hash string, err error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return nil, "", err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return nil, "", err
	}
	info, statErr := os.Lstat(abs)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, "", &NotFoundError{Path: p}
		}
		return nil, "", statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("fsdoc: %q is not a regular file", p)
	}
	if info.Size() > maxBytes {
		return nil, "", fmt.Errorf("fsdoc: %q exceeds %d byte read cap", p, maxBytes)
	}
	content, err = os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &NotFoundError{Path: p}
		}
		return nil, "", err
	}
	return content, notebook.Hash(content), nil
}

// Write creates or edits a file with the same hash-CAS contract as the notebook.
// An empty baseHash means create-only: it fails with a Conflict if the file
// already exists. A non-empty baseHash is a hash-CAS edit: it applies only if the
// on-disk hash still matches, else returns a Conflict carrying the current hash.
// On success it returns the new hash and a nil Conflict. Parent directories are
// created as needed.
func (s *Store) Write(p string, content []byte, baseHash string) (newHash string, conflict *Conflict, err error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return "", nil, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return "", nil, err
	}
	if int64(len(content)) > MaxFileSize {
		return "", nil, fmt.Errorf("fsdoc: content for %q exceeds %d bytes", p, MaxFileSize)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, statErr := os.ReadFile(abs)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if baseHash == "" {
		if exists {
			return "", &Conflict{CurrentHash: notebook.Hash(existing)}, nil
		}
	} else {
		if !exists {
			return "", &Conflict{CurrentHash: ""}, nil
		}
		if cur := notebook.Hash(existing); cur != baseHash {
			return "", &Conflict{CurrentHash: cur}, nil
		}
	}
	if err := writeAtomic(abs, content); err != nil {
		return "", nil, err
	}
	return notebook.Hash(content), nil, nil
}

// Exists reports whether a path exists under the root (file or directory),
// without reading it. Used to flag in-notebook markdown links that point at a
// missing note. A path that escapes the root or is otherwise invalid returns an
// error, so the caller leaves such a link unflagged rather than guessing; only a
// genuine "not there" returns (false, nil).
func (s *Store) Exists(p string) (bool, error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return false, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Rename moves one regular file within the root. The destination is never
// overwritten, and directories are outside this operation's scope.
func (s *Store) Rename(from, to string) error {
	fromRel, err := cleanRel(from, false)
	if err != nil {
		return err
	}
	toRel, err := cleanRel(to, false)
	if err != nil {
		return err
	}
	fromAbs, err := s.abs(fromRel)
	if err != nil {
		return err
	}
	toAbs, err := s.abs(toRel)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(fromAbs)
	if os.IsNotExist(err) {
		return &NotFoundError{Path: from}
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsdoc: %q is not a regular file", from)
	}
	if fromAbs == toAbs {
		return nil
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return fmt.Errorf("fsdoc: %q already exists", to)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return err
	}
	return os.Rename(fromAbs, toAbs)
}

// Delete removes one regular file within the root.
func (s *Store) Delete(p string) error {
	rel, err := cleanRel(p, false)
	if err != nil {
		return err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return &NotFoundError{Path: p}
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsdoc: %q is not a regular file", p)
	}
	return os.Remove(abs)
}

// CleanPath normalizes a file path (root-absolute or relative) to its clean,
// slash-separated root-relative form — the same form List returns — or errors if
// it escapes the root or names the root itself. The daemon uses it to echo and
// broadcast a write's path in the canonical form, so result/event paths match
// what fs_list keys on (mirroring notebook.CleanPath, but without the .md rule).
func CleanPath(p string) (string, error) { return cleanRel(p, false) }

// cleanRel validates and normalizes p to a clean, slash-separated path relative
// to the root. The input may be root-absolute ("/knowledge/foo.md") or relative;
// ".." is neutralized to within the root. Unlike notebook.CleanPath it imposes NO
// extension restriction (fsdoc serves any file). allowRoot permits the empty/root
// path (List uses it for the root directory); Read/Write reject it because they
// need a file. Dotfile/dotdir segments and empty segments are rejected.
func cleanRel(p string, allowRoot bool) (string, error) {
	trimmed := strings.TrimSpace(p)
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(trimmed, "/")), "/")
	if rel == "" || rel == "." {
		if allowRoot {
			return "", nil
		}
		return "", fmt.Errorf("fsdoc: %q is the root, not a file", p)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" {
			return "", fmt.Errorf("fsdoc: %q has an empty path segment", p)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("fsdoc: %q has a dotfile/dotdir segment", p)
		}
	}
	return rel, nil
}

// abs resolves a cleaned root-relative path to an absolute filesystem path,
// rejecting any escape outside the root — lexical (defense in depth atop
// cleanRel) and via symlinks inside the root that point elsewhere. rel == ""
// resolves to the root itself (for List).
func (s *Store) abs(rel string) (string, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("fsdoc: %q escapes the root", rel)
	}
	if err := notebook.EnsureWithinResolvedRoot(s.root, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// writeAtomic writes content to absPath atomically: it creates parent
// directories, writes a uniquely-named temp file in the same directory, then
// renames it into place. The temp file is removed on any error. (A focused copy
// of the notebook's identical helper; both keep the same atomic-write semantics.)
// The temp name is dot-prefixed so it falls outside CleanPath's trackable set:
// fsdoc has no extension filter, so without this a watcher on the root would see
// the transient swap file's own fsnotify events as a change to a real path.
func writeAtomic(absPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(absPath), fmt.Sprintf(".%s.tmp.%d.%d", filepath.Base(absPath), os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
