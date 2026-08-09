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

// Store is the filesystem-canonical notebook for one root directory. Every
// write serializes under mu and applies atomically; reads do not take the lock.
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore returns a Store rooted at the given absolute directory, which need
// not exist yet (EnsureScaffold creates it).
func NewStore(root string) *Store {
	// Clean the root: a trailing slash would break abs's HasPrefix containment test.
	if root != "" {
		root = filepath.Clean(root)
	}
	return &Store{root: root}
}

// Root returns the absolute notebook root directory.
func (s *Store) Root() string { return s.root }

// EnsureScaffold idempotently creates the root, reserved directories, and
// reserved files, never clobbering an existing file. It returns only the paths
// it actually wrote — even alongside a mid-scaffold error — so callers record
// exactly those as self-writes.
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

// Write creates or edits a note. Empty baseHash = create-only (Conflict if the
// file exists); non-empty = hash-CAS edit, returning a Conflict with the
// current hash when the on-disk hash no longer matches.
func (s *Store) Write(p string, content []byte, baseHash string) (newHash string, conflict *Conflict, err error) {
	abs, err := s.abs(p)
	if err != nil {
		return "", nil, err
	}
	if int64(len(content)) > MaxFileSize {
		return "", nil, fmt.Errorf("notebook: content for %q exceeds %d bytes", p, MaxFileSize)
	}
	// No type validation: the `type` vocabulary is open, so the store stays a
	// permissive writer; guidance, not the store, asks authors for a type.

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

// AppendJournal appends an entry to journal/<date>.md, creating it with
// type:journal frontmatter on first write. dateISO must be YYYY-MM-DD.
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

// AppendJournalEntryOnce appends entry to journal/<date>.md unless dedupeMarker
// is already present (then written=false, no error). The marker must be a stable
// string embedded in entry: the journal file itself is the dedup ledger, so
// idempotency survives a daemon restart with no separate bookkeeping.
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
			// The H1 is the title (Document.Title reads it); no frontmatter title:.
			Frontmatter: map[string]any{"type": TypeJournal},
			Body:        "# " + dateISO + "\n",
		}
	}
}

// AppendInbox appends an entry to the reserved chief inbox note (inbox.md),
// creating it on first write; appends serialize and never conflict.
func (s *Store) AppendInbox(entry string) (relPath string, hash string, err error) {
	if strings.TrimSpace(entry) == "" {
		return "", "", fmt.Errorf("notebook: empty inbox entry")
	}
	hash, err = s.appendToNote(FileInbox, entry, func() Document {
		return Document{Body: inboxTemplate}
	})
	return FileInbox, hash, err
}

// appendToNote appends entry to rel, creating it from newDoc() when absent.
// The read-modify-write runs under the store lock, so appends never conflict.
func (s *Store) appendToNote(rel, entry string, newDoc func() Document) (hash string, err error) {
	_, hash, err = s.appendToNoteOnce(rel, "", entry, newDoc)
	return hash, err
}

// appendToNoteOnce is appendToNote with optional idempotency via dedupeMarker.
// The read-check-write is one critical section under the store lock, so two
// callers racing the same marker can never both write.
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
		return false, Hash(existing), nil // already recorded; hash lets callers still suppress
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

// listFrontmatterScanLimit bounds the leading bytes List reads per file — it
// must never load a whole (possibly oversized, externally-written) body.
const listFrontmatterScanLimit = 64 << 10 // 64 KiB

// List returns the notes under the root, sorted by path; prefix scopes a
// subtree. Dotfiles/dotdirs and non-.md files are skipped; an uninitialized
// root yields an empty list, not an error.
func (s *Store) List(prefix string) ([]Entry, error) {
	// Prefix matches on path-segment boundaries, never partial names.
	want := strings.Trim(strings.TrimSpace(prefix), "/")
	var entries []Entry
	walkErr := filepath.WalkDir(s.root, func(p string, dirent fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				if p == s.root {
					return fs.SkipAll // root not created yet => empty list
				}
				// Subtree vanished mid-walk (root is externally syncable);
				// treat as empty rather than failing the whole List.
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
		// A note can be a symlink pointing outside the root; without this check
		// List would expose an outside file's frontmatter over the websocket.
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

// Backlinks returns the notes linking to target (anchors ignored, self
// excluded), sorted by path; a target absent on disk still surfaces its
// linkers. Full scan per call — acceptable while the Notebook stays small.
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
			// Bigger than attn ever writes = oversized externally-synced file;
			// skip rather than pull its whole body into memory per navigation.
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

// abs resolves a notebook path to an absolute filesystem path, rejecting both
// lexical ("..") and symlink escapes from the root.
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

// checkWithinResolvedRoot rejects symlink escape (which the lexical guard in
// abs cannot catch) via EnsureWithinResolvedRoot.
func (s *Store) checkWithinResolvedRoot(abs string) error {
	return EnsureWithinResolvedRoot(s.root, abs)
}

// EnsureWithinResolvedRoot requires abs's deepest existing ancestor to resolve
// within the resolved root: a symlinked ancestor pointing outside is rejected,
// a legitimately symlinked root is allowed. abs must already be lexically
// contained. Residual TOCTOU accepted for a single-user local app.
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
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

// readPrefix reads up to limit leading bytes of a file.
func readPrefix(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// writeAtomic writes via temp file + rename. The temp name is dot-prefixed so
// it lands outside CleanPath's trackable set — a watcher must not treat the
// transient swap file's events as a change to a real path.
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
