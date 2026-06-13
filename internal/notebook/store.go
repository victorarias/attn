package notebook

import (
	"fmt"
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
	return &Store{root: root}
}

// Root returns the absolute notebook root directory.
func (s *Store) Root() string { return s.root }

// EnsureScaffold idempotently creates the root, the reserved directory layout,
// and the reserved index/log files. It never clobbers an existing file. created
// reports whether the root was newly made or any scaffold file was written.
func (s *Store) EnsureScaffold() (created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, statErr := os.Stat(s.root); os.IsNotExist(statErr) {
		created = true
	} else if statErr != nil {
		return false, statErr
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return false, fmt.Errorf("create notebook root: %w", err)
	}
	for _, dir := range scaffoldDirs() {
		if err := os.MkdirAll(filepath.Join(s.root, filepath.FromSlash(dir)), 0o755); err != nil {
			return false, err
		}
	}
	for _, f := range scaffoldFiles() {
		abs := filepath.Join(s.root, filepath.FromSlash(f.relPath))
		if _, statErr := os.Stat(abs); statErr == nil {
			continue // never clobber an existing file
		} else if !os.IsNotExist(statErr) {
			return false, statErr
		}
		if err := writeAtomic(abs, []byte(f.content)); err != nil {
			return false, err
		}
		created = true
	}
	return created, nil
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
	// Reject an explicitly-declared but invalid kind; absent kind is allowed
	// (permissive). Malformed frontmatter is tolerated here — Write stores
	// bytes; the read/list path is the permissive consumer.
	if doc, perr := Parse(content); perr == nil {
		if k := doc.Kind(); k != "" && !ValidKind(k) {
			return "", nil, fmt.Errorf("notebook: invalid kind %q (want %q or %q)", k, KindJournal, KindMemory)
		}
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
// creating it with kind:journal frontmatter on first write. Appends are
// serialized and never conflict. dateISO must be YYYY-MM-DD.
func (s *Store) AppendJournal(dateISO, entry string) (relPath string, hash string, err error) {
	if !journalDateRE.MatchString(dateISO) {
		return "", "", fmt.Errorf("notebook: invalid journal date %q (want YYYY-MM-DD)", dateISO)
	}
	if strings.TrimSpace(entry) == "" {
		return "", "", fmt.Errorf("notebook: empty journal entry")
	}
	rel := path.Join(DirJournal, dateISO+".md")
	abs, err := s.abs(rel)
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, statErr := os.ReadFile(abs)
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", "", statErr
	}
	var doc Document
	if statErr == nil {
		doc = ParsePermissive(existing)
	} else {
		doc = Document{
			Frontmatter: map[string]any{"kind": KindJournal, "title": dateISO},
			Body:        "# " + dateISO + "\n",
		}
	}
	doc.Body = strings.TrimRight(doc.Body, "\n") + "\n\n" + strings.TrimRight(entry, "\n") + "\n"
	out := doc.Bytes()
	if int64(len(out)) > MaxFileSize {
		return "", "", fmt.Errorf("notebook: journal %s exceeds %d bytes", dateISO, MaxFileSize)
	}
	if err := writeAtomic(abs, out); err != nil {
		return "", "", err
	}
	return rel, Hash(out), nil
}

// List returns the notes under the root, sorted by path. The optional prefix
// (root-absolute or relative) filters to a subtree. The .attn/ dotdir and any
// dotfile are skipped, and non-.md files are ignored. An uninitialized root
// yields an empty list, not an error.
func (s *Store) List(prefix string) ([]Entry, error) {
	want := strings.TrimPrefix(strings.TrimSpace(prefix), "/")
	var entries []Entry
	walkErr := filepath.WalkDir(s.root, func(p string, dirent fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == s.root {
				return fs.SkipAll // root not created yet => empty list
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
		if want != "" && !strings.HasPrefix(rel, want) {
			return nil
		}
		info, ierr := dirent.Info()
		if ierr != nil {
			return nil
		}
		raw, rerr := os.ReadFile(p)
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
			Kind:    doc.Kind(),
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

// abs resolves a notebook path to an absolute filesystem path, validating it and
// defending against any escape outside the root.
func (s *Store) abs(p string) (string, error) {
	rel, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("notebook: %q escapes the notebook root", p)
	}
	return abs, nil
}

// writeAtomic writes content to absPath atomically: it creates parent
// directories, writes to a uniquely-named temp file in the same directory, then
// renames it into place. The temp file is removed on any error.
func writeAtomic(absPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", absPath, os.Getpid(), time.Now().UnixNano())
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
