package fsdoc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/notebook"
)

func write(t *testing.T, abs, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// List is shallow (immediate children only), sorts directories before files, and
// hides dot-entries. File entries carry size and mtime; directory entries do not.
func TestListShallowSortedAndFiltered(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "b.md"), "bb")
	write(t, filepath.Join(root, "a.txt"), "aaa")
	write(t, filepath.Join(root, "knowledge", "deep.md"), "deep") // creates the dir
	write(t, filepath.Join(root, ".hidden"), "x")                 // dotfile, skipped
	if err := os.MkdirAll(filepath.Join(root, ".attn"), 0o755); err != nil {
		t.Fatal(err) // dotdir, skipped
	}

	s := NewStore(root)
	entries, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	// knowledge/ (dir) first, then files a.txt, b.md; no dot-entries; not recursive
	// (deep.md is not surfaced).
	want := []string{"knowledge", "a.txt", "b.md"}
	if len(names) != len(want) {
		t.Fatalf("list names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("list names = %v, want %v", names, want)
		}
	}
	for _, e := range entries {
		switch e.Name {
		case "knowledge":
			if !e.IsDir || e.Path != "knowledge" || e.Size != 0 || e.Modified != "" {
				t.Fatalf("dir entry = %+v", e)
			}
		case "a.txt":
			if e.IsDir || e.Path != "a.txt" || e.Size != 3 || e.Modified == "" {
				t.Fatalf("file entry = %+v", e)
			}
		}
	}
}

// Listing a subdirectory scopes to that directory's immediate children, with
// root-relative paths.
func TestListSubdirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "knowledge", "areas", "foo.md"), "x")
	write(t, filepath.Join(root, "knowledge", "index.md"), "y")

	s := NewStore(root)
	entries, err := s.List("knowledge")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Path != "knowledge/areas" || !entries[0].IsDir ||
		entries[1].Path != "knowledge/index.md" || entries[1].IsDir {
		t.Fatalf("subdir list = %+v", entries)
	}
}

func TestListMissingRootIsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "does-not-exist-yet"))
	entries, err := s.List("")
	if err != nil {
		t.Fatalf("missing root List error = %v, want nil", err)
	}
	if len(entries) != 0 {
		t.Fatalf("missing root List = %+v, want empty", entries)
	}
}

func TestListMissingSubdirIsNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.List("nope")
	if !IsNotFound(err) {
		t.Fatalf("List(missing subdir) err = %v, want NotFound", err)
	}
}

func TestListFileIsError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.md"), "x")
	s := NewStore(root)
	if _, err := s.List("a.md"); err == nil || IsNotFound(err) {
		t.Fatalf("List(file) err = %v, want a not-a-directory error", err)
	}
}

func TestReadReturnsContentAndHash(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "hello")
	s := NewStore(root)
	content, hash, err := s.Read("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" || hash != notebook.Hash([]byte("hello")) {
		t.Fatalf("read = %q hash %q", content, hash)
	}
}

func TestReadMissingIsNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Read("gone.md"); !IsNotFound(err) {
		t.Fatalf("Read(missing) err = %v, want NotFound", err)
	}
}

// Write creates, refuses to clobber on create-only, applies a matching hash-CAS
// edit, and reports a conflict (with the current hash) on a stale base.
func TestWriteCreateEditAndConflict(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	h1, conflict, err := s.Write("dir/a.txt", []byte("v1"), "")
	if err != nil || conflict != nil || h1 != notebook.Hash([]byte("v1")) {
		t.Fatalf("create = %q conflict %+v err %v", h1, conflict, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "dir", "a.txt")); string(got) != "v1" {
		t.Fatalf("on-disk = %q after create", got)
	}

	// Create-only against an existing file => conflict carrying the current hash.
	_, conflict, err = s.Write("dir/a.txt", []byte("v2"), "")
	if err != nil || conflict == nil || conflict.CurrentHash != h1 {
		t.Fatalf("create-only over existing = conflict %+v err %v", conflict, err)
	}

	// Stale base hash => conflict, no write.
	_, conflict, err = s.Write("dir/a.txt", []byte("v2"), "deadbeef")
	if err != nil || conflict == nil || conflict.CurrentHash != h1 {
		t.Fatalf("stale CAS = conflict %+v err %v", conflict, err)
	}

	// Correct base hash => the edit applies.
	h2, conflict, err := s.Write("dir/a.txt", []byte("v2"), h1)
	if err != nil || conflict != nil || h2 != notebook.Hash([]byte("v2")) {
		t.Fatalf("CAS edit = %q conflict %+v err %v", h2, conflict, err)
	}
}

// A hash-CAS edit of a file that does not exist is a conflict with an empty
// current hash (not a create), so the editor learns the file vanished.
func TestWriteCASMissingIsConflict(t *testing.T) {
	s := NewStore(t.TempDir())
	_, conflict, err := s.Write("gone.txt", []byte("x"), "somehash")
	if err != nil || conflict == nil || conflict.CurrentHash != "" {
		t.Fatalf("CAS over missing = conflict %+v err %v", conflict, err)
	}
}

func TestWriteRejectsOversizeContent(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Write("big.bin", make([]byte, MaxFileSize+1), ""); err == nil {
		t.Fatal("oversize Write err = nil, want rejection")
	}
}

// Lexical escapes (.. above the root) are rejected by every operation.
func TestPathEscapesRejected(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, p := range []string{"../outside.txt", "/../../etc/passwd", "a/../../b.txt"} {
		if _, _, err := s.Read(p); err == nil {
			t.Fatalf("Read(%q) err = nil, want escape rejection", p)
		}
	}
}

// A symlink inside the root that points outside it is skipped by List and
// rejected by Read — the same containment the notebook enforces, reused here.
func TestSymlinkEscapeContained(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	write(t, secret, "top secret")

	root := t.TempDir()
	write(t, filepath.Join(root, "normal.txt"), "ok")
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	s := NewStore(root)
	entries, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "leak.txt" {
			t.Fatalf("List surfaced an escaping symlink: %+v", e)
		}
	}
	if _, _, err := s.Read("leak.txt"); err == nil {
		t.Fatal("Read followed an escaping symlink")
	}
}
