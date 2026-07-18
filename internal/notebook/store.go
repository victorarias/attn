package notebook

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the filesystem-canonical notebook backing a single root directory.
// It is the single in-process writer for attn-originated mutations: every write
// is serialized under mu and applied atomically (temp file + rename). Reads do
// not take the lock.
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore returns a Store rooted at the given absolute directory. The directory
// need not exist yet; EnsureScaffold creates it.
func NewStore(root string) *Store {
	// Normalize the root so containment checks compare against a canonical path.
	// A root entered with a trailing slash (e.g. "~/Notebook/") would otherwise
	// make abs's `HasPrefix(abs, root+separator)` test expect a doubled separator
	// ("/Notebook//"), rejecting a perfectly valid "index.md" as escaping the root.
	if root != "" {
		root = filepath.Clean(root)
	}
	return &Store{root: root}
}

// Root returns the absolute notebook root directory.
func (s *Store) Root() string { return s.root }

// EnsureScaffold idempotently creates the root, the reserved directory layout,
// and the reserved index/log files. It never clobbers an existing file. It
// returns the notebook-relative paths of the files it actually wrote (empty on
// an idempotent no-op run) so callers can record exactly those as self-writes —
// recording reserved paths that were not rewritten would wrongly suppress a real
// external edit to them. On a mid-scaffold failure it returns the files written
// so far ALONGSIDE the error, so the caller can still account for attn's own
// partial writes rather than letting them surface later as external edits.
func (s *Store) EnsureScaffold() (createdPaths []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, statErr := os.Stat(s.root); statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, fmt.Errorf("create notebook root: %w", err)
	}
	for _, dir := range scaffoldDirs() {
		abs := filepath.Join(s.root, filepath.FromSlash(dir))
		if err := s.checkWithinResolvedRoot(abs); err != nil {
			return nil, err // refuse to create through a symlinked subdir
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
	}
	for _, f := range scaffoldFiles() {
		abs := filepath.Join(s.root, filepath.FromSlash(f.relPath))
		if err := s.checkWithinResolvedRoot(abs); err != nil {
			return createdPaths, err
		}
		if _, statErr := os.Stat(abs); statErr == nil {
			continue // never clobber an existing file
		} else if !os.IsNotExist(statErr) {
			return createdPaths, statErr
		}
		if err := writeAtomic(abs, []byte(f.content)); err != nil {
			return createdPaths, err
		}
		createdPaths = append(createdPaths, f.relPath)
	}
	return createdPaths, nil
}

// Read returns the raw bytes of a note and their content hash. A missing note
// yields a *NotFoundError.
func (s *Store) Read(p string) (content []byte, hash string, err error) {
	abs, err := s.abs(p)
	if err != nil {
		return nil, "", err
	}
	content, err = os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &NotFoundError{Path: p}
		}
		return nil, "", err
	}
	return content, Hash(content), nil
}

// Write creates or edits a note. An empty baseHash means create-only: it fails
// with a Conflict if the file already exists. A non-empty baseHash is a
// hash-CAS edit: it applies only if the on-disk hash still matches, else returns
// a Conflict carrying the current hash. On success it returns the new hash and a
// nil Conflict.
func (s *Store) Write(p string, content []byte, baseHash string) (newHash string, conflict *Conflict, err error) {
	abs, err := s.abs(p)
	if err != nil {
		return "", nil, err
	}
	if int64(len(content)) > MaxFileSize {
		return "", nil, fmt.Errorf("notebook: content for %q exceeds %d bytes", p, MaxFileSize)
	}
	// No type validation: OKF leaves the `type` vocabulary open, so the store is
	// a permissive writer (it stores bytes) and the read/list path is the
	// permissive consumer. The guidance, not the store, asks authors for a type.

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, statErr := os.ReadFile(abs)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if baseHash == "" {
		if exists {
			return "", &Conflict{CurrentHash: Hash(existing)}, nil
		}
	} else {
		if !exists {
			return "", &Conflict{CurrentHash: ""}, nil
		}
		if cur := Hash(existing); cur != baseHash {
			return "", &Conflict{CurrentHash: cur}, nil
		}
	}
	if err := writeAtomic(abs, content); err != nil {
		return "", nil, err
	}
	return Hash(content), nil, nil
}

var journalDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// AppendJournal appends an entry to the dated journal file (journal/<date>.md),
// creating it with type:journal frontmatter on first write. Appends are
// serialized and never conflict. dateISO must be YYYY-MM-DD.
func (s *Store) AppendJournal(dateISO, entry string) (relPath string, hash string, err error) {
	if !journalDateRE.MatchString(dateISO) {
		return "", "", fmt.Errorf("notebook: invalid journal date %q (want YYYY-MM-DD)", dateISO)
	}
	if strings.TrimSpace(entry) == "" {
		return "", "", fmt.Errorf("notebook: empty journal entry")
	}
	rel := path.Join(DirJournal, dateISO+".md")
	hash, err = s.appendToNote(rel, entry, newJournalDoc(dateISO))
	return rel, hash, err
}

// AppendJournalEntryOnce appends entry to journal/<date>.md, but only when
// dedupeMarker is not already present in the file. It reports written=false (and
// no error) when the marker is already there, so repeated calls for the same
// logical entry are idempotent. dedupeMarker must be a stable string embedded in
// entry (typically a hidden HTML comment): the journal file itself is the dedup
// ledger, so suppression needs no separate bookkeeping and survives a daemon
// restart. This is how attn auto-captures an event (a dispatch outcome) exactly
// once even when more than one lifecycle path fires for it.
func (s *Store) AppendJournalEntryOnce(dateISO, dedupeMarker, entry string) (relPath string, written bool, hash string, err error) {
	if !journalDateRE.MatchString(dateISO) {
		return "", false, "", fmt.Errorf("notebook: invalid journal date %q (want YYYY-MM-DD)", dateISO)
	}
	if strings.TrimSpace(entry) == "" {
		return "", false, "", fmt.Errorf("notebook: empty journal entry")
	}
	if strings.TrimSpace(dedupeMarker) == "" {
		return "", false, "", fmt.Errorf("notebook: empty journal dedupe marker")
	}
	rel := path.Join(DirJournal, dateISO+".md")
	written, hash, err = s.appendToNoteOnce(rel, dedupeMarker, entry, newJournalDoc(dateISO))
	return rel, written, hash, err
}

// newJournalDoc returns the factory for a fresh dated journal note.
func newJournalDoc(dateISO string) func() Document {
	return func() Document {
		return Document{
			// The `# <date>` H1 in the body is the journal's title (Document.Title
			// reads it); no redundant frontmatter `title:`.
			Frontmatter: map[string]any{"type": TypeJournal},
			Body:        "# " + dateISO + "\n",
		}
	}
}

// AppendInbox appends an entry to the reserved chief inbox note (inbox.md),
// creating it on first write. Like AppendJournal, appends are serialized under
// the store lock and never conflict.
func (s *Store) AppendInbox(entry string) (relPath string, hash string, err error) {
	if strings.TrimSpace(entry) == "" {
		return "", "", fmt.Errorf("notebook: empty inbox entry")
	}
	hash, err = s.appendToNote(FileInbox, entry, func() Document {
		// The `# Chief inbox` H1 in the template is the title; the note needs no
		// frontmatter.
		return Document{Body: inboxTemplate}
	})
	return FileInbox, hash, err
}

// appendToNote appends entry to the note at rel, creating it from newDoc() when
// it does not yet exist. The whole read-modify-write runs under the store lock so
// concurrent appends serialize and never conflict (unlike the hash-CAS Write).
func (s *Store) appendToNote(rel, entry string, newDoc func() Document) (hash string, err error) {
	_, hash, err = s.appendToNoteOnce(rel, "", entry, newDoc)
	return hash, err
}

// appendToNoteOnce is appendToNote with optional idempotency: when dedupeMarker is
// non-empty and already present in the existing note, it appends nothing and
// reports written=false. The read-check-write runs as one critical section under
// the store lock, so the dedup test is atomic with the append and two callers
// racing the same marker can never both write.
func (s *Store) appendToNoteOnce(rel, dedupeMarker, entry string, newDoc func() Document) (written bool, hash string, err error) {
	abs, err := s.abs(rel)
	if err != nil {
		return false, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, statErr := os.ReadFile(abs)
	if statErr != nil && !os.IsNotExist(statErr) {
		return false, "", statErr
	}
	if dedupeMarker != "" && statErr == nil && bytes.Contains(existing, []byte(dedupeMarker)) {
		// Already recorded — return the current hash so callers can still suppress
		// the (absent) watcher event without surfacing a spurious write.
		return false, Hash(existing), nil
	}
	var doc Document
	if statErr == nil {
		doc = ParsePermissive(existing)
	} else {
		doc = newDoc()
	}
	doc.Body = strings.TrimRight(doc.Body, "\n") + "\n\n" + strings.TrimRight(entry, "\n") + "\n"
	out := doc.Bytes()
	if int64(len(out)) > MaxFileSize {
		return false, "", fmt.Errorf("notebook: %s exceeds %d bytes", rel, MaxFileSize)
	}
	if err := writeAtomic(abs, out); err != nil {
		return false, "", err
	}
	return true, Hash(out), nil
}

// List returns the notes under the root, sorted by path. The optional prefix
// (root-absolute or relative) filters to a subtree. The .attn/ dotdir and any
// dotfile are skipped, and non-.md files are ignored. An uninitialized root
// yields an empty list, not an error.
// listFrontmatterScanLimit bounds how many leading bytes List reads per file to
// extract frontmatter. Frontmatter lives at the top, so a small prefix is
// enough — List never loads a whole (possibly externally-written, oversized)
// body into memory just to render the tree.
const listFrontmatterScanLimit = 64 << 10 // 64 KiB

func (s *Store) List(prefix string) ([]Entry, error) {
	// A non-empty prefix scopes a subtree on path-segment boundaries (so
	// "knowledge/areas" does NOT match the sibling "knowledge/areas-archive").
	want := strings.Trim(strings.TrimSpace(prefix), "/")
	var entries []Entry
	walkErr := filepath.WalkDir(s.root, func(p string, dirent fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				if p == s.root {
					return fs.SkipAll // root not created yet => empty list
				}
				// A subtree vanished mid-walk. The root is externally syncable, so
				// an external client can remove a directory during the scan; treat
				// the gone path as empty rather than failing the whole List (and the
				// UI-triggered Backlinks that calls it on every navigation).
				return nil
			}
			return err
		}
		if dirent.IsDir() {
			if p != s.root && strings.HasPrefix(dirent.Name(), ".") {
				return fs.SkipDir // skip .attn/ and any dotdir subtree
			}
			return nil
		}
		name := dirent.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			return nil
		}
		relAbs, rerr := filepath.Rel(s.root, p)
		if rerr != nil {
			return nil
		}
		rel := filepath.ToSlash(relAbs)
		if want != "" && rel != want && !strings.HasPrefix(rel, want+"/") {
			return nil
		}
		info, ierr := dirent.Info()
		if ierr != nil {
			return nil
		}
		// The root is externally syncable/user-editable, so a note entry can be a
		// symlink pointing outside the root (e.g. linked.md -> /outside/private.md).
		// Read/Write defend against this via checkWithinResolvedRoot; List must too,
		// or it would read and expose an outside file's frontmatter (title/summary)
		// over the websocket. Skip any entry that resolves outside the root.
		if err := s.checkWithinResolvedRoot(p); err != nil {
			return nil
		}
		raw, rerr := readPrefix(p, listFrontmatterScanLimit)
		if rerr != nil {
			return nil
		}
		doc := ParsePermissive(raw)
		updated := doc.Updated()
		if updated == "" {
			updated = info.ModTime().UTC().Format(time.RFC3339)
		}
		entries = append(entries, Entry{
			Path:    rel,
			Type:    doc.Type(),
			Title:   doc.Title(),
			Summary: doc.Summary(),
			Updated: updated,
			Size:    info.Size(),
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// Backlinks returns the notes whose body contains a root-absolute markdown link
// targeting the given note, sorted by path. The target's own note is excluded,
// and a link's #anchor is ignored when matching. A target that does not exist on
// disk still surfaces any notes that link to it (dangling-link discovery), so
// the UI can show what points at a note before it is created.
//
// Cost note: this reads every note's body on each call. The Notebook is a small,
// distilled store (that is the point), so a full scan is acceptable; if it ever
// grows large, an incremental reverse index can replace this without changing
// the signature.
func (s *Store) Backlinks(target string) ([]Entry, error) {
	want, err := CleanPath(target)
	if err != nil {
		return nil, err
	}
	entries, err := s.List("")
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if e.Path == want {
			continue // a note linking to itself is not a backlink
		}
		if e.Size > MaxFileSize {
			// attn never writes a note larger than MaxFileSize, so anything bigger
			// is an oversized externally-synced file. Skip it rather than pull its
			// whole body into memory on every navigation — List caps its own
			// per-file read (listFrontmatterScanLimit) for the same reason.
			continue
		}
		content, _, rerr := s.Read(e.Path)
		if rerr != nil {
			continue // skip a note that vanished or is unreadable mid-scan
		}
		if bodyLinksTo(ParsePermissive(content).Body, want) {
			out = append(out, e)
		}
	}
	return out, nil
}

// bodyLinksTo reports whether body contains a root-absolute markdown link whose
// target (ignoring any #anchor) resolves to want (a clean notebook-relative path).
func bodyLinksTo(body, want string) bool {
	for _, link := range Links(body) {
		p := link
		if i := strings.IndexByte(p, '#'); i >= 0 {
			p = p[:i]
		}
		cleaned, err := CleanPath(p)
		if err != nil {
			continue // an anchor-only or malformed target cannot be a backlink
		}
		if cleaned == want {
			return true
		}
	}
	return false
}

// abs resolves a notebook path to an absolute filesystem path, validating it and
// defending against any escape outside the root — both lexical (".." after
// Clean) and via symlinks inside the root that point elsewhere.
func (s *Store) abs(p string) (string, error) {
	rel, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("notebook: %q escapes the notebook root", p)
	}
	if err := s.checkWithinResolvedRoot(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// checkWithinResolvedRoot defends against symlink escape: it resolves the
// deepest existing ancestor of abs and requires it to stay within the resolved
// root. A symlinked directory or file inside the root that points outside is
// rejected; a legitimately symlinked root itself (the user pointing
// ~/attn-notebook at a synced folder) is allowed, because the root is resolved
// too. The lexical guard in abs cannot catch this — a symlink uses no "..".
//
// A residual TOCTOU remains (a symlink could be planted between the check and
// the syscall); for a single-user local app that is an accepted limit.
func (s *Store) checkWithinResolvedRoot(abs string) error {
	return EnsureWithinResolvedRoot(s.root, abs)
}

// EnsureWithinResolvedRoot is the package-level symlink-containment check that
// Store.checkWithinResolvedRoot delegates to, exported so daemon-side writers
// that build paths under the same notebook root (e.g. the raw tier) can apply
// the identical guard. It resolves the deepest existing ancestor of abs and
// requires it to stay within the resolved root; a symlinked ancestor pointing
// outside is rejected, while a legitimately symlinked root is allowed because
// the root is resolved too. abs is expected to be lexically contained already;
// this is the symlink layer on top. The same TOCTOU caveat as above applies.
func EnsureWithinResolvedRoot(root, abs string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // root not created yet; nothing to traverse through
		}
		return err
	}
	probe := abs
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
				return fmt.Errorf("notebook: %q resolves outside the notebook root via a symlink", abs)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		// The leaf (or a not-yet-created ancestor) does not exist; walk up to
		// the deepest existing component and resolve that.
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

// readPrefix reads up to limit leading bytes of a file. Used by List to scan
// frontmatter without loading large bodies.
func readPrefix(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// writeAtomic writes content to absPath atomically: it creates parent
// directories, writes to a uniquely-named temp file in the same directory, then
// renames it into place. The temp file is removed on any error. The temp name is
// dot-prefixed so it lands outside CleanPath's trackable set: a watcher observing
// this directory (self or otherwise) must not treat the transient swap file's own
// fsnotify events as a change to a real, trackable path.
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
