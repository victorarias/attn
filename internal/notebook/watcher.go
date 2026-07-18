package notebook

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultWatchDebounce is the fixed window, opened by the first event of a
// burst, over which filesystem events are coalesced into one change
// notification. It is not an idle/quiet window: later events in the same burst
// do not extend it, so a burst flushes at most this long after it starts.
const DefaultWatchDebounce = 400 * time.Millisecond

// selfWriteTTL caps how long an unconsumed NoteSelfWrite record lingers before
// it is pruned. Suppression itself is one-shot: a record is consumed by the
// first coalesced flush that contains its path (a single atomic write fires its
// events within one debounce window, so all of attn's own events for that write
// coalesce into that one flush). The TTL only bounds a record whose matching
// event never arrives, so a stale record cannot suppress a real edit forever.
const selfWriteTTL = 3 * time.Second

// Watcher observes a notebook root for changes made OUTSIDE attn — an external
// markdown sync tool such as Obsidian, or the user editing files directly — and
// invokes onChange with the affected notebook-relative paths. attn's own writes
// are excluded: a daemon write records the path via NoteSelfWrite before it
// mutates the file, so the resulting event is recognized and dropped.
//
// It lives in the notebook package (not the daemon) so it is unit testable
// without a daemon and reuses the package's path rules; the daemon supplies
// onChange (which broadcasts notebook_changed) and drives NoteSelfWrite from its
// write handlers.
type Watcher struct {
	root      string
	debounce  time.Duration
	cleanPath func(string) (string, error)
	onChange  func(paths []string)

	fsw *fsnotify.Watcher

	mu         sync.Mutex
	selfWrites map[string]selfWriteRecord // notebook-relative path -> suppression record
	closeOnce  sync.Once
	loopDone   chan struct{}    // closed when loop() returns
	now        func() time.Time // injectable clock for tests
}

// selfWriteRecord is what NoteSelfWrite stores per path: when the record expires,
// and the content hash attn wrote (empty for an unconditional suppression).
type selfWriteRecord struct {
	expiry time.Time
	hash   string
}

// SelfWrite identifies a notebook-relative path attn just wrote. A non-empty Hash
// makes suppression content-aware: the matching filesystem event is dropped only
// if the file on disk still holds exactly those bytes, so a genuine external edit
// that lands in the SAME debounce window (different bytes, or a delete) is still
// surfaced instead of being swallowed by the self-write. An empty Hash suppresses
// the next event for the path unconditionally (used where the written content is
// not readily available, e.g. the scaffold).
type SelfWrite struct {
	Rel  string
	Hash string
}

// NewWatcher starts watching root (which must already exist) and every
// non-dotdir subdirectory under it, coalescing events over debounce before
// calling onChange. Close stops it. It errors if root does not exist or is not a
// directory, rather than silently watching nothing. Only .md files are
// trackable (the notebook's rule); see NewWatcherWithCleaner for a generic root.
func NewWatcher(root string, debounce time.Duration, onChange func(paths []string)) (*Watcher, error) {
	return NewWatcherWithCleaner(root, debounce, CleanPath, onChange)
}

// NewWatcherWithCleaner is NewWatcher with an injectable path-cleaning rule.
// cleanPath decides which filesystem paths are trackable: it is handed a
// root-relative slash-path and either returns the canonical form to key events
// on, or an error to skip the path entirely. Passing CleanPath (the .md rule)
// reproduces NewWatcher's behavior; a permissive cleaner (e.g. fsdoc.CleanPath)
// makes the watcher generic over every file under root.
func NewWatcherWithCleaner(root string, debounce time.Duration, cleanPath func(string) (string, error), onChange func(paths []string)) (*Watcher, error) {
	clean := filepath.Clean(root)
	if info, err := os.Stat(clean); err != nil {
		return nil, fmt.Errorf("notebook watcher: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("notebook watcher: %s is not a directory", clean)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:       clean,
		debounce:   debounce,
		cleanPath:  cleanPath,
		onChange:   onChange,
		fsw:        fsw,
		selfWrites: make(map[string]selfWriteRecord),
		loopDone:   make(chan struct{}),
		now:        time.Now,
	}
	if _, err := w.addTree(w.root); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// NoteSelfWrite marks paths as attn-originated so the next filesystem event for
// each is treated as our own write, not an external edit. With a per-write content
// hash, suppression is content-aware (see SelfWrite). A nil Watcher (none running
// yet) is a no-op.
func (w *Watcher) NoteSelfWrite(writes ...SelfWrite) {
	if w == nil || len(writes) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	exp := w.now().Add(selfWriteTTL)
	for _, sw := range writes {
		clean, err := w.cleanPath(sw.Rel)
		if err != nil {
			continue
		}
		w.selfWrites[clean] = selfWriteRecord{expiry: exp, hash: sw.Hash}
	}
}

// Close stops watching and waits for the event loop to return, so no onChange
// can fire after it returns. It is safe to call more than once and on a nil
// Watcher.
func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	var err error
	w.closeOnce.Do(func() {
		err = w.fsw.Close() // closes Events/Errors, unblocking loop
	})
	<-w.loopDone // join the loop goroutine (idempotent: the channel stays closed)
	return err
}

func (w *Watcher) loop() {
	defer close(w.loopDone)
	pending := make(map[string]struct{})
	var timerC <-chan time.Time
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev, pending)
			if len(pending) > 0 && timerC == nil {
				timerC = time.After(w.debounce)
			}
		case <-timerC:
			timerC = nil
			w.flush(pending)
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// best-effort watcher: a transient watch error must not kill the loop.
		}
	}
}

// handleEvent records a trackable .md change into pending and, for a newly
// created directory, attaches a watch so its future contents are observed
// (fsnotify is not recursive).
func (w *Watcher) handleEvent(ev fsnotify.Event, pending map[string]struct{}) {
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			if base := filepath.Base(ev.Name); base == "." || strings.HasPrefix(base, ".") {
				return // skip .attn/ and any dotdir subtree
			}
			// Surface whatever addTree discovered even if the walk aborted partway
			// (e.g. a permission error on a sibling subdir): the files found before
			// the failure are real trackable paths whose parent dirs are already
			// watched, so dropping them would silently miss external edits.
			files, _ := w.addTree(ev.Name)
			for _, rel := range files {
				pending[rel] = struct{}{}
			}
			return
		}
	}
	if rel, ok := w.trackable(ev.Name); ok {
		pending[rel] = struct{}{}
	}
}

// flush emits the coalesced, self-write-filtered change set and clears pending.
func (w *Watcher) flush(pending map[string]struct{}) {
	if len(pending) == 0 {
		return
	}
	rels := make([]string, 0, len(pending))
	for rel := range pending {
		rels = append(rels, rel)
		delete(pending, rel)
	}
	rels = w.dropSelfWrites(rels)
	if len(rels) == 0 {
		return
	}
	sort.Strings(rels)
	w.onChange(rels)
}

// dropSelfWrites removes paths attn just wrote (consuming each record once) and
// prunes expired records. For a record that carries a content hash, suppression
// is content-aware: the path is dropped only if the file on disk still holds
// exactly the bytes attn wrote. If an external edit coalesced into the same
// debounce window (so the on-disk bytes differ, or the file was deleted), the
// path is surfaced instead of being swallowed by the self-write.
func (w *Watcher) dropSelfWrites(rels []string) []string {
	w.mu.Lock()
	now := w.now()
	for k, rec := range w.selfWrites {
		if now.After(rec.expiry) {
			delete(w.selfWrites, k)
		}
	}
	out := make([]string, 0, len(rels))
	type recheck struct{ rel, hash string }
	var pending []recheck
	for _, rel := range rels {
		if rec, ok := w.selfWrites[rel]; ok && !now.After(rec.expiry) {
			delete(w.selfWrites, rel) // consume: one event round per record
			if rec.hash == "" {
				continue // unconditional suppression
			}
			pending = append(pending, recheck{rel: rel, hash: rec.hash})
			continue
		}
		out = append(out, rel)
	}
	w.mu.Unlock()
	// Content-aware recheck runs OUTSIDE the lock (it reads files): surface a path
	// whose on-disk bytes no longer match what attn wrote — an external edit that
	// landed in the same window. dropSelfWrites is only ever called from the single
	// loop goroutine, so this stays race-free.
	for _, rc := range pending {
		if w.diskHash(rc.rel) != rc.hash {
			out = append(out, rc.rel)
		}
	}
	return out
}

// diskHash returns the content hash of the note at rel on disk, or "" if it cannot
// be read (e.g. it was deleted). "" never equals a real recorded hash, so an
// unreadable or deleted path is treated as a change worth surfacing.
func (w *Watcher) diskHash(rel string) string {
	content, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return Hash(content)
}

// addTree adds a watch for dir and every non-dotdir subdirectory, returning the
// root-relative paths of the trackable files it contains (so files that appear
// alongside a freshly created directory are not missed).
func (w *Watcher) addTree(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, dirent fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // vanished mid-walk; ignore
			}
			return err
		}
		if dirent.IsDir() {
			if p != w.root && strings.HasPrefix(dirent.Name(), ".") {
				return fs.SkipDir // skip .attn/ and any dotdir subtree
			}
			_ = w.fsw.Add(p)
			return nil
		}
		if rel, ok := w.trackable(p); ok {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// trackable reports whether an absolute path is trackable under this watcher's
// cleanPath rule (no dotfile/dotdir segment under the root, plus whatever else
// cleanPath enforces — e.g. .md-only for the notebook rule) and returns its
// clean relative path.
func (w *Watcher) trackable(absPath string) (string, bool) {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return "", false
	}
	clean, err := w.cleanPath(filepath.ToSlash(rel))
	if err != nil {
		return "", false
	}
	return clean, true
}
