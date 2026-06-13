package notebook

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
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

	// Record the write as attn-originated BEFORE it lands, the way a daemon
	// handler does, then perform the write. The event must be dropped.
	w.NoteSelfWrite("/note.md")
	writeFile(t, filepath.Join(root, "note.md"), "# note\n")

	expectNoChange(t, changes)
}

func TestWatcherSelfWriteSuppressionIsOneShot(t *testing.T) {
	root := t.TempDir()
	w, changes := newTestWatcher(t, root)

	w.NoteSelfWrite("note.md")
	writeFile(t, filepath.Join(root, "note.md"), "# v1\n")
	expectNoChange(t, changes)

	// A later external edit of the same file (no NoteSelfWrite) IS reported —
	// suppression consumes one event round, it does not mute the path forever.
	writeFile(t, filepath.Join(root, "note.md"), "# v2 external\n")
	got := waitChange(t, changes)
	if !reflect.DeepEqual(got, []string{"note.md"}) {
		t.Fatalf("change = %v, want [note.md]", got)
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

	// Machine state, a non-.md file, a dotfile, and an atomic-writer temp file
	// must all be ignored.
	writeFile(t, filepath.Join(root, ".attn", "locks.md"), "x")
	writeFile(t, filepath.Join(root, "notes.txt"), "x")
	writeFile(t, filepath.Join(root, ".hidden.md"), "x")
	writeFile(t, filepath.Join(root, "memory", "foo.md.tmp.123.456"), "x")

	expectNoChange(t, changes)
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
