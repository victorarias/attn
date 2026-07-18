package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// fsIndex drives handleFsIndex directly and returns the decoded result event.
func fsIndex(t *testing.T, d *Daemon, client *wsClient, requestID, root string) protocol.FsIndexResultMessage {
	t.Helper()
	d.handleFsIndex(client, requestID, root)
	var res protocol.FsIndexResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// fs_index over a real tree returns every regular file's root-relative slash
// path, sorted, while excluding: a dot-dir's contents (not just the dir
// itself), a node_modules dir's contents, dot-files, and symlinked files.
// truncated must be false since nothing hits the cap.
func TestFsIndexListsFilesExcludingDotDirsNodeModulesAndSymlinks(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	mustWrite := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("top.md")
	mustWrite("nested/dir/deep.md")
	mustWrite("nested/dir/deep2.txt")
	mustWrite(".hidden-dir/inside.md")  // whole dot-dir excluded
	mustWrite("node_modules/pkg/index.js") // whole node_modules dir excluded
	mustWrite(".dotfile")                  // dot-file excluded

	// A symlinked file must be excluded (listed as an entry but skipped, not
	// followed).
	symlinkTarget := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(symlinkTarget, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(symlinkTarget, filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}

	client := trustedFsClient(4)
	res := fsIndex(t, d, client, "i1", root)
	if !res.Success {
		t.Fatalf("fs_index(root) failed: %v", res.Error)
	}
	if res.Root != root {
		t.Fatalf("fs_index.root = %q, want %q", res.Root, root)
	}
	if res.Truncated {
		t.Fatalf("fs_index.truncated = true, want false")
	}
	want := []string{"nested/dir/deep.md", "nested/dir/deep2.txt", "top.md"}
	if !equalStrings(res.Files, want) {
		t.Fatalf("fs_index.files = %v, want %v", res.Files, want)
	}
}

// equalStrings reports whether a and b hold the same elements in the same
// order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// indexRoot (the injectable-cap walk helper) truncates once the cap is hit
// rather than returning an unbounded list: with more files than cap, it must
// report truncated=true and return exactly cap entries.
func TestIndexRootTruncatesAtInjectedCap(t *testing.T) {
	root := t.TempDir()
	const total = 12
	const cap = 5
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("file%02d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, truncated, err := indexRoot(root, cap)
	if err != nil {
		t.Fatalf("indexRoot: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true (total=%d cap=%d)", total, cap)
	}
	if len(files) != cap {
		t.Fatalf("len(files) = %d, want %d", len(files), cap)
	}
}

// normalizeExternalRoot only requires the deepest EXISTING ancestor to
// canonicalize, so a nonexistent explicit root (or one pointing at a regular
// file rather than a directory) passes resolveFsRoot. Without an explicit
// pre-walk check, WalkDir's error-recovery branch swallows the "root does not
// exist" error and the walk silently "succeeds" with zero files — a
// silently-empty finder instead of a visible error. Both cases must fail with
// Success=false, not report an empty index.
func TestFsIndexRejectsNonexistentAndNonDirectoryRoots(t *testing.T) {
	d := newFsDaemon(t)
	base := t.TempDir()

	client := trustedFsClient(4)
	missing := filepath.Join(base, "does-not-exist")
	res := fsIndex(t, d, client, "i1", missing)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_index(nonexistent root) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, missing) {
		t.Fatalf("fs_index(nonexistent root) error = %q, want it to mention the root %q", *res.Error, missing)
	}

	regularFile := filepath.Join(base, "not-a-dir.txt")
	if err := os.WriteFile(regularFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileClient := trustedFsClient(4)
	fileRes := fsIndex(t, d, fileClient, "i2", regularFile)
	if fileRes.Success {
		t.Fatalf("fs_index(root = regular file) = %+v, want failure", fileRes)
	}
}

// An ordinary (unauthenticated) client asking to index an explicit root must
// be denied with no file listing — fs_index inherits the resolveFsRoot
// chokepoint's auth gate just like fs_watch does. The same client succeeds
// with root omitted (the notebook root), since the gate is scoped to the
// explicit-root escape hatch.
func TestFsIndexWithExplicitRootDeniedForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	untrusted := &wsClient{send: make(chan outboundMessage, 8)}
	res := fsIndex(t, d, untrusted, "i1", root)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_index(explicit root, untrusted client) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_index(explicit root, untrusted client) error = %q, want it to mention the authenticated app", *res.Error)
	}
	if len(res.Files) != 0 {
		t.Fatalf("fs_index(explicit root, untrusted client) files = %v, want empty", res.Files)
	}

	omittedRes := fsIndex(t, d, untrusted, "i2", "")
	if !omittedRes.Success {
		t.Fatalf("fs_index(omitted root, untrusted client) = %+v, want success", omittedRes)
	}
}
