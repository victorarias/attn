package notebook

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// newTestWatcher starts a Watcher with a short debounce and a buffered channel
// that receives each coalesced change set.
func newTestWatcher(t *testing.T, root string) (*Watcher, chan []string) {
	t.Helper()
	changes := make(chan []string, 16)
	w, err := NewWatcher(root, 120*time.Millisecond, func(paths []string) {
		changes <- paths
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	// Let the watch registration settle before the test mutates the tree.
	time.Sleep(50 * time.Millisecond)
	return w, changes
}

func writeFile(t *testing.T, abs, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitChange(t *testing.T, ch chan []string) []string {
	t.Helper()
	select {
	case paths := <-ch:
		return paths
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not report a change")
		return nil
	}
}

func expectNoChange(t *testing.T, ch chan []string) {
	t.Helper()
	select {
	case paths := <-ch:
		t.Fatalf("watcher reported an unexpected change: %v", paths)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWatcherDetectsExternalWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, changes := newTestWatcher(t, root)

	writeFile(t, filepath.Join(root, "memory", "foo.md"), "# foo\n")

	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"memory/foo.md"}) {
		t.Fatalf("change = %v, want [memory/foo.md]", got)
	}
}

func TestWatcherSuppressesSelfWrite(t *testing.T) {
	root := t.TempDir()
	w, changes := newTestWatcher(t, root)

	// Record the write as attn-originated with the hash attn wrote, the way a
	// daemon handler does, then perform that write. The event must be dropped
	// because the on-disk bytes still match what attn wrote.
	const content = "# note\n"
	w.NoteSelfWrite(SelfWrite{Rel: "/note.md", Hash: Hash([]byte(content))})
	writeFile(t, filepath.Join(root, "note.md"), content)

	expectNoChange(t, changes)
}

func TestWatcherSelfWriteSuppressionIsOneShot(t *testing.T) {
	root := t.TempDir()
	w, changes := newTestWatcher(t, root)

	const v1 = "# v1\n"
	w.NoteSelfWrite(SelfWrite{Rel: "note.md", Hash: Hash([]byte(v1))})
	writeFile(t, filepath.Join(root, "note.md"), v1)
	expectNoChange(t, changes)

	// A later external edit of the same file (no NoteSelfWrite) IS reported —
	// suppression consumes one event round, it does not mute the path forever.
	writeFile(t, filepath.Join(root, "note.md"), "# v2 external\n")
	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"note.md"}) {
		t.Fatalf("change = %v, want [note.md]", got)
	}
}

func TestWatcherUnconditionalSelfWriteSuppressesAnyContent(t *testing.T) {
	root := t.TempDir()
	w, changes := newTestWatcher(t, root)

	// A self-write with no hash (the scaffold path, where the written content is
	// not threaded through) suppresses the next event for the path regardless of
	// its on-disk content.
	w.NoteSelfWrite(SelfWrite{Rel: "index.md"})
	writeFile(t, filepath.Join(root, "index.md"), "# whatever lands\n")

	expectNoChange(t, changes)
}

func TestWatcherSurfacesSameWindowExternalEdit(t *testing.T) {
	root := t.TempDir()
	w, changes := newTestWatcher(t, root)
	abs := filepath.Join(root, "note.md")

	// attn writes content A and records a content-aware self-write for it...
	const attnContent = "# attn wrote this\n"
	w.NoteSelfWrite(SelfWrite{Rel: "note.md", Hash: Hash([]byte(attnContent))})
	writeFile(t, abs, attnContent)
	// ...then an external tool overwrites the SAME path within the SAME debounce
	// window with DIFFERENT bytes. Both fs events coalesce onto one pending entry.
	writeFile(t, abs, "# EXTERNAL overwrote it\n")

	// The external edit must still surface: on disk the bytes no longer match what
	// attn wrote, so the self-write must not swallow it.
	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"note.md"}) {
		t.Fatalf("change = %v, want [note.md] (same-window external edit must surface)", got)
	}
}

func TestWatcherCoalescesBurst(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, changes := newTestWatcher(t, root)

	// Several writes inside one debounce window collapse to a single emit.
	writeFile(t, filepath.Join(root, "journal", "a.md"), "a")
	writeFile(t, filepath.Join(root, "journal", "b.md"), "b")
	writeFile(t, filepath.Join(root, "index.md"), "i")

	got := waitChange(t, changes)
	sort.Strings(got)
	want := []string{"index.md", "journal/a.md", "journal/b.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced change = %v, want %v", got, want)
	}
	// No second emit for the same burst.
	expectNoChange(t, changes)
}

func TestWatcherIgnoresDotDirsTempAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".attn", "memory"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, changes := newTestWatcher(t, root)

	// All ignored: machine state under .attn/ and the dotfile by the dotdir/
	// dotfile rule; the .txt and the atomic-writer temp suffix (.tmp.<pid>.<nano>)
	// by the .md-suffix rule (the temp suffix is just one more non-.md name).
	writeFile(t, filepath.Join(root, ".attn", "locks.md"), "x")
	writeFile(t, filepath.Join(root, "notes.txt"), "x")
	writeFile(t, filepath.Join(root, ".hidden.md"), "x")
	writeFile(t, filepath.Join(root, "memory", "foo.md.tmp.123.456"), "x")

	expectNoChange(t, changes)
}

func TestNewWatcherErrorsOnMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := NewWatcher(missing, DefaultWatchDebounce, func([]string) {}); err == nil {
		t.Fatal("NewWatcher on a missing root should error, not silently watch nothing")
	}
}

func TestWatcherSelfWriteRecordExpires(t *testing.T) {
	root := t.TempDir()
	changes := make(chan []string, 8)
	w, err := NewWatcher(root, 80*time.Millisecond, func(paths []string) { changes <- paths })
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Drive the suppression clock from the test (w.now is read under w.mu, so a
	// controllable clock makes the TTL deterministic without real-time waits).
	base := time.Unix(1_700_000_000, 0)
	var fakeNanos atomic.Int64
	fakeNanos.Store(base.UnixNano())
	w.mu.Lock()
	w.now = func() time.Time { return time.Unix(0, fakeNanos.Load()) }
	w.mu.Unlock()
	time.Sleep(50 * time.Millisecond)

	// Record a self-write, then advance the clock past selfWriteTTL so its record
	// expires before its (never-arriving) event would have consumed it.
	w.NoteSelfWrite(SelfWrite{Rel: "note.md", Hash: Hash([]byte("ignored; pruned before recheck"))})
	fakeNanos.Store(base.Add(selfWriteTTL + time.Second).UnixNano())

	// The write now lands as a genuine external edit: the expired record is
	// pruned, not used to suppress it.
	writeFile(t, filepath.Join(root, "note.md"), "external edit")
	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"note.md"}) {
		t.Fatalf("change = %v, want [note.md] (expired self-write must not suppress)", got)
	}
}

func TestWatcherWatchesNewSubdir(t *testing.T) {
	root := t.TempDir()
	_, changes := newTestWatcher(t, root)

	// A directory created after the watcher started must be watched so a note
	// written inside it is still observed.
	writeFile(t, filepath.Join(root, "memory", "decisions", "x.md"), "# x\n")

	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"memory/decisions/x.md"}) {
		t.Fatalf("change = %v, want [memory/decisions/x.md]", got)
	}
}

func TestWatcherDetectsDelete(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gone.md")
	writeFile(t, abs, "# gone\n")
	_, changes := newTestWatcher(t, root)

	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"gone.md"}) {
		t.Fatalf("change = %v, want [gone.md]", got)
	}
}

func TestAddTreeReturnsPartialFilesOnWalkError(t *testing.T) {
	root := t.TempDir()
	// a.md sorts before zbad/, so WalkDir visits and records it, then aborts when
	// it cannot descend into the unreadable subdir. The already-discovered note
	// must still be returned (and surfaced), not dropped with the error.
	writeFile(t, filepath.Join(root, "a.md"), "# a\n")
	bad := filepath.Join(root, "zbad")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsw.Close() })
	w := &Watcher{root: filepath.Clean(root), fsw: fsw}

	mdFiles, walkErr := w.addTree(root)
	if walkErr == nil {
		t.Skip("WalkDir did not error on the unreadable subdir (likely running as root)")
	}
	found := false
	for _, rel := range mdFiles {
		if rel == "a.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("addTree dropped already-discovered files on a walk error; got %v, want it to include a.md", mdFiles)
	}
}
