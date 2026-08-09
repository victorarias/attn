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

// DefaultWatchDebounce is the fixed coalescing window opened by a burst's first
// event — not an idle window; later events do not extend it.
const DefaultWatchDebounce = 400 * time.Millisecond

// selfWriteTTL bounds an unconsumed NoteSelfWrite record whose event never
// arrives, so a stale record cannot suppress a real edit forever.
const selfWriteTTL = 3 * time.Second

// Watcher observes a notebook root for changes made outside attn and invokes
// onChange with the affected notebook-relative paths. attn's own writes are
// excluded: NoteSelfWrite records the path before the file is mutated.
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

// selfWriteRecord is the per-path suppression record: expiry plus the content
// hash attn wrote (empty for an unconditional suppression).
type selfWriteRecord struct {
	expiry time.Time
	hash   string
}

// SelfWrite identifies a notebook-relative path attn just wrote. A non-empty
// Hash makes suppression content-aware — an external edit in the same debounce
// window still surfaces; an empty Hash suppresses the next event unconditionally.
type SelfWrite struct {
	Rel  string
	Hash string
}

// NewWatcher starts watching root (must exist — errors rather than silently
// watching nothing) and every non-dotdir subdirectory, coalescing events over
// debounce before calling onChange. .md files only; see NewWatcherWithCleaner.
func NewWatcher(root string, debounce time.Duration, onChange func(paths []string)) (*Watcher, error) {
	return NewWatcherWithCleaner(root, debounce, CleanPath, onChange)
}

// NewWatcherWithCleaner is NewWatcher with an injectable path rule: cleanPath
// maps a root-relative slash-path to its canonical form, or errors to skip it.
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

// NoteSelfWrite marks paths as attn-originated so their next filesystem event
// is dropped (see SelfWrite). A nil Watcher is a no-op.
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

// Close stops watching and waits for the event loop, so no onChange can fire
// after it returns. Safe to call more than once and on a nil Watcher.
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

// handleEvent records a trackable change into pending; a newly created
// directory gets a watch attached (fsnotify is not recursive).
func (w *Watcher) handleEvent(ev fsnotify.Event, pending map[string]struct{}) {
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			if base := filepath.Base(ev.Name); base == "." || strings.HasPrefix(base, ".") {
				return // skip .attn/ and any dotdir subtree
			}
			// Surface whatever addTree found even if the walk aborted partway;
			// dropping already-discovered files would silently miss external edits.
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
// prunes expired records; a hashed record drops the path only if the on-disk
// bytes still match, so a same-window external edit is surfaced.
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
	// Recheck outside the lock (it reads files); only the single loop goroutine
	// calls dropSelfWrites, so this stays race-free.
	for _, rc := range pending {
		if w.diskHash(rc.rel) != rc.hash {
			out = append(out, rc.rel)
		}
	}
	return out
}

// diskHash returns the content hash of rel on disk, or "" when unreadable —
// "" never equals a real hash, so a deleted path surfaces as a change.
func (w *Watcher) diskHash(rel string) string {
	content, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return Hash(content)
}

// addTree watches dir and every non-dotdir subdirectory, returning the
// trackable files it contains so a freshly created tree's files are not missed.
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

// trackable reports whether an absolute path passes the cleanPath rule and
// returns its clean relative path.
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
