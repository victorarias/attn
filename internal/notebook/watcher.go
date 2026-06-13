package notebook

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultWatchDebounce is the quiet window over which a burst of filesystem
// events is coalesced into a single change notification.
const DefaultWatchDebounce = 400 * time.Millisecond

// selfWriteTTL bounds how long a NoteSelfWrite record suppresses the matching
// filesystem event. attn's own write fires its event within milliseconds of the
// rename, so a few seconds is comfortably safe while still expiring a record
// whose event never arrives (e.g. the write failed after the record was taken).
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
	root     string
	debounce time.Duration
	onChange func(paths []string)

	fsw *fsnotify.Watcher

	mu         sync.Mutex
	selfWrites map[string]time.Time // notebook-relative path -> suppression expiry
	closeOnce  sync.Once
	now        func() time.Time // injectable clock for tests
}

// NewWatcher starts watching root (which must already exist) and every
// non-dotdir subdirectory under it, coalescing events over debounce before
// calling onChange. Close stops it.
func NewWatcher(root string, debounce time.Duration, onChange func(paths []string)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:       filepath.Clean(root),
		debounce:   debounce,
		onChange:   onChange,
		fsw:        fsw,
		selfWrites: make(map[string]time.Time),
		now:        time.Now,
	}
	if _, err := w.addTree(w.root); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// NoteSelfWrite marks notebook-relative paths as attn-originated so the next
// filesystem event for each is treated as our own write, not an external edit.
// Call it immediately BEFORE the store mutation so the record is in place before
// the event can fire. A nil Watcher (none running yet) is a no-op.
func (w *Watcher) NoteSelfWrite(rels ...string) {
	if w == nil || len(rels) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	exp := w.now().Add(selfWriteTTL)
	for _, rel := range rels {
		clean, err := CleanPath(rel)
		if err != nil {
			continue
		}
		w.selfWrites[clean] = exp
	}
}

// Close stops watching. It is safe to call more than once and on a nil Watcher.
func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	var err error
	w.closeOnce.Do(func() {
		err = w.fsw.Close() // closes Events/Errors, unblocking loop
	})
	return err
}

func (w *Watcher) loop() {
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
			if mdFiles, err := w.addTree(ev.Name); err == nil {
				for _, rel := range mdFiles {
					pending[rel] = struct{}{}
				}
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
// prunes expired records.
func (w *Watcher) dropSelfWrites(rels []string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	for k, exp := range w.selfWrites {
		if now.After(exp) {
			delete(w.selfWrites, k)
		}
	}
	out := rels[:0]
	for _, rel := range rels {
		if exp, ok := w.selfWrites[rel]; ok && !now.After(exp) {
			delete(w.selfWrites, rel) // consume: suppress exactly this event round
			continue
		}
		out = append(out, rel)
	}
	return out
}

// addTree adds a watch for dir and every non-dotdir subdirectory, returning the
// notebook-relative paths of the .md files it contains (so files that appear
// alongside a freshly created directory are not missed).
func (w *Watcher) addTree(dir string) ([]string, error) {
	var mdFiles []string
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
			mdFiles = append(mdFiles, rel)
		}
		return nil
	})
	return mdFiles, err
}

// trackable reports whether an absolute path is a notebook note (a .md file with
// no dotfile/dotdir segment under the root) and returns its clean relative path.
func (w *Watcher) trackable(absPath string) (string, bool) {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return "", false
	}
	clean, err := CleanPath(filepath.ToSlash(rel))
	if err != nil {
		return "", false
	}
	return clean, true
}
