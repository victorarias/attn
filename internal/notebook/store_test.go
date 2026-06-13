package notebook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureScaffold(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nb")
	s := NewStore(root)

	created, err := s.EnsureScaffold()
	if err != nil {
		t.Fatalf("EnsureScaffold: %v", err)
	}
	if !created {
		t.Fatal("first EnsureScaffold should report created=true")
	}
	for _, rel := range []string{"index.md", "log.md", "memory/index.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("scaffold file %s missing: %v", rel, err)
		}
	}
	for _, rel := range []string{"journal", "memory/decisions", "memory/gotchas", "memory/domain"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || !info.IsDir() {
			t.Fatalf("scaffold dir %s missing: %v", rel, err)
		}
	}

	// Idempotent: a second run creates nothing and never clobbers.
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = s.EnsureScaffold()
	if err != nil {
		t.Fatalf("second EnsureScaffold: %v", err)
	}
	if created {
		t.Fatal("second EnsureScaffold should report created=false")
	}
	got, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if string(got) != "EDITED" {
		t.Fatalf("EnsureScaffold clobbered an existing file: %q", got)
	}
}

func TestReadNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Read("memory/missing.md"); !IsNotFound(err) {
		t.Fatalf("Read missing = %v, want NotFoundError", err)
	}
}

func TestWriteCreateAndConflict(t *testing.T) {
	s := NewStore(t.TempDir())
	content := []byte("---\nkind: memory\n---\nhello\n")

	hash, conflict, err := s.Write("memory/decisions/foo.md", content, "")
	if err != nil || conflict != nil {
		t.Fatalf("create: err=%v conflict=%v", err, conflict)
	}
	if hash != Hash(content) {
		t.Fatalf("create hash = %q, want %q", hash, Hash(content))
	}

	// Create-only against an existing file is a conflict, not an overwrite.
	_, conflict, err = s.Write("memory/decisions/foo.md", []byte("other"), "")
	if err != nil {
		t.Fatalf("create-conflict err: %v", err)
	}
	if conflict == nil || conflict.CurrentHash != Hash(content) {
		t.Fatalf("expected create conflict carrying current hash, got %#v", conflict)
	}
	got, _, _ := s.Read("memory/decisions/foo.md")
	if string(got) != string(content) {
		t.Fatalf("create-conflict overwrote the file: %q", got)
	}
}

func TestWriteCASEdit(t *testing.T) {
	s := NewStore(t.TempDir())
	v1 := []byte("---\nkind: memory\n---\nv1\n")
	h1, _, err := s.Write("memory/decisions/foo.md", v1, "")
	if err != nil {
		t.Fatal(err)
	}

	// Stale base hash => conflict, no write.
	v2 := []byte("---\nkind: memory\n---\nv2\n")
	_, conflict, err := s.Write("memory/decisions/foo.md", v2, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil || conflict.CurrentHash != h1 {
		t.Fatalf("stale-base write = conflict %#v, want current hash %q", conflict, h1)
	}

	// Matching base hash => applies.
	h2, conflict, err := s.Write("memory/decisions/foo.md", v2, h1)
	if err != nil || conflict != nil {
		t.Fatalf("CAS edit: err=%v conflict=%v", err, conflict)
	}
	got, gotHash, _ := s.Read("memory/decisions/foo.md")
	if string(got) != string(v2) || gotHash != h2 {
		t.Fatalf("CAS edit did not apply: content=%q hash=%q", got, gotHash)
	}
}

func TestWriteRejectsInvalidKind(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _, err := s.Write("memory/foo.md", []byte("---\nkind: bogus\n---\nx\n"), "")
	if err == nil {
		t.Fatal("Write should reject an explicitly-declared invalid kind")
	}
	// Absent kind is permitted.
	if _, _, err := s.Write("memory/bar.md", []byte("# no frontmatter\n"), ""); err != nil {
		t.Fatalf("Write without a kind should be allowed: %v", err)
	}
}

func TestAppendJournal(t *testing.T) {
	s := NewStore(t.TempDir())

	rel, _, err := s.AppendJournal("2026-06-13", "first entry")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rel != "journal/2026-06-13.md" {
		t.Fatalf("journal path = %q", rel)
	}
	rel, _, err = s.AppendJournal("2026-06-13", "second entry")
	if err != nil {
		t.Fatalf("second append: %v", err)
	}

	content, _, _ := s.Read(rel)
	doc := ParsePermissive(content)
	if doc.Kind() != KindJournal {
		t.Fatalf("journal kind = %q, want %q", doc.Kind(), KindJournal)
	}
	if !strings.Contains(doc.Body, "first entry") || !strings.Contains(doc.Body, "second entry") {
		t.Fatalf("journal body missing entries:\n%s", doc.Body)
	}

	if _, _, err := s.AppendJournal("not-a-date", "x"); err == nil {
		t.Fatal("AppendJournal should reject a malformed date")
	}
}

// A symlinked directory inside the root that points outside must not let
// reads/writes escape the root (the notebook lives in the user's home and is
// externally writable, so a planted symlink is realistic).
func TestStoreRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "nb")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("TOP SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)

	if _, _, err := s.Read("evil/secret.md"); err == nil {
		t.Fatal("Read through a symlinked dir should be rejected")
	}
	if _, _, err := s.Write("evil/secret.md", []byte("---\nkind: memory\n---\nx\n"), ""); err == nil {
		t.Fatal("Write through a symlinked dir should be rejected")
	}
	if got, _ := os.ReadFile(secret); string(got) != "TOP SECRET\n" {
		t.Fatalf("outside file was modified through the symlink: %q", got)
	}
}

// A legitimately symlinked root (user points ~/attn-notebook at a synced folder)
// must still work — the guard resolves the root too.
func TestStoreAllowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s := NewStore(link)
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatalf("EnsureScaffold on a symlinked root: %v", err)
	}
	if _, _, err := s.Write("memory/foo.md", []byte("---\nkind: memory\n---\nx\n"), ""); err != nil {
		t.Fatalf("write under a symlinked root should work: %v", err)
	}
}

// The prefix scopes a subtree on path-segment boundaries, not raw substring.
func TestListPrefixIsPathSegmentBoundary(t *testing.T) {
	s := NewStore(t.TempDir())
	body := []byte("---\nkind: memory\n---\nx\n")
	if _, _, err := s.Write("memory/decisions/foo.md", body, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Write("memory/decisions-archive/bar.md", body, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List("memory/decisions")
	if err != nil {
		t.Fatal(err)
	}
	foundFoo, foundArchive := false, false
	for _, e := range entries {
		switch e.Path {
		case "memory/decisions/foo.md":
			foundFoo = true
		case "memory/decisions-archive/bar.md":
			foundArchive = true
		}
	}
	if !foundFoo {
		t.Fatalf("prefix should include memory/decisions/foo.md; got %+v", entries)
	}
	if foundArchive {
		t.Fatalf("prefix must NOT leak the sibling memory/decisions-archive/bar.md; got %+v", entries)
	}
}

// List extracts frontmatter from a large file without loading the whole body,
// and still reports the full size.
func TestListReadsFrontmatterFromLargeFile(t *testing.T) {
	s := NewStore(t.TempDir())
	big := "---\nkind: memory\ntitle: Big\n---\n" + strings.Repeat("x\n", 100<<10) // ~200 KiB body
	if _, _, err := s.Write("memory/big.md", []byte(big), ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List("memory/big.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "memory" || entries[0].Title != "Big" {
		t.Fatalf("List did not extract frontmatter from a large file: %+v", entries)
	}
	if entries[0].Size != int64(len(big)) {
		t.Fatalf("List size = %d, want %d", entries[0].Size, len(big))
	}
}

// AppendJournal must not corrupt frontmatter an external tool wrote (comments,
// key order, ambiguous scalars all survive the next attn append).
func TestAppendJournalPreservesExistingFrontmatter(t *testing.T) {
	s := NewStore(t.TempDir())
	existing := "---\nkind: journal\n# external note\nobsidian_id: 007\nzeta: z\ntitle: jrnl\n---\n# entries\n\nfirst\n"
	if _, _, err := s.Write("journal/2026-06-13.md", []byte(existing), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendJournal("2026-06-13", "second"); err != nil {
		t.Fatal(err)
	}
	content, _, _ := s.Read("journal/2026-06-13.md")
	got := string(content)
	for _, want := range []string{"# external note", "obsidian_id: 007", "first", "second"} {
		if !strings.Contains(got, want) {
			t.Fatalf("append dropped %q:\n%s", want, got)
		}
	}
}

func TestList(t *testing.T) {
	s := NewStore(t.TempDir())

	// Uninitialized root => empty list, not an error.
	entries, err := s.List("")
	if err != nil {
		t.Fatalf("List uninitialized: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List uninitialized = %d entries, want 0", len(entries))
	}

	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Write("memory/decisions/foo.md", []byte("---\nkind: memory\ntitle: Foo\nsummary: a decision\n---\nbody\n"), ""); err != nil {
		t.Fatal(err)
	}
	// Machine state under .attn/ must never be surfaced.
	if err := os.MkdirAll(filepath.Join(s.Root(), ".attn", "dreams"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), ".attn", "dreams", "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err = s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	paths := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Path, ".attn/") {
			t.Fatalf("List surfaced machine state: %q", e.Path)
		}
		paths[e.Path] = e
	}
	foo, ok := paths["memory/decisions/foo.md"]
	if !ok {
		t.Fatalf("List missing the written note; got %v", entries)
	}
	if foo.Kind != "memory" || foo.Title != "Foo" || foo.Summary != "a decision" {
		t.Fatalf("List entry metadata = %+v", foo)
	}

	// Prefix filters to a subtree.
	mem, err := s.List("/memory/decisions")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range mem {
		if !strings.HasPrefix(e.Path, "memory/decisions") {
			t.Fatalf("prefixed List returned out-of-subtree entry %q", e.Path)
		}
	}
	if len(mem) == 0 {
		t.Fatal("prefixed List returned nothing")
	}
}

func TestBacklinks(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}

	// target is the note we want backlinks for.
	mustWrite(t, s, "memory/decisions/target.md", "---\nkind: memory\ntitle: Target\n---\nthe decision\n")
	// linker references target with a trailing #anchor — the anchor must be
	// ignored when matching.
	mustWrite(t, s, "memory/decisions/linker.md", "---\nkind: memory\ntitle: Linker\n---\nsee [the call](/memory/decisions/target.md#why) for context\n")
	// journal references target with a plain root-absolute link.
	mustWrite(t, s, "journal/2026-06-13.md", "---\nkind: journal\n---\nfollowed [target](/memory/decisions/target.md) today\n")
	// unrelated links elsewhere and must not appear.
	mustWrite(t, s, "memory/gotchas/unrelated.md", "---\nkind: memory\n---\nlinks [elsewhere](/memory/decisions/other.md) only\n")
	// self-link: target links to itself and must be excluded from its own backlinks.
	mustWrite(t, s, "memory/decisions/target.md", "---\nkind: memory\ntitle: Target\n---\nthe decision; see [self](/memory/decisions/target.md)\n")

	got, err := s.Backlinks("/memory/decisions/target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	gotPaths := make([]string, len(got))
	for i, e := range got {
		gotPaths[i] = e.Path
	}
	want := []string{"journal/2026-06-13.md", "memory/decisions/linker.md"} // sorted by path
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("Backlinks paths = %v, want %v", gotPaths, want)
	}
	// Metadata (title) should ride along so the UI can render a label.
	for _, e := range got {
		if e.Path == "memory/decisions/linker.md" && e.Title != "Linker" {
			t.Fatalf("backlink entry lost metadata: %+v", e)
		}
	}

	// Dangling-link discovery: a target that does not exist still surfaces its
	// linkers, so the UI can show what points at a not-yet-created note.
	dangling, err := s.Backlinks("/memory/decisions/other.md")
	if err != nil {
		t.Fatalf("Backlinks(dangling): %v", err)
	}
	if len(dangling) != 1 || dangling[0].Path != "memory/gotchas/unrelated.md" {
		t.Fatalf("dangling Backlinks = %v, want [memory/gotchas/unrelated.md]", dangling)
	}

	// A note nobody links to has no backlinks.
	none, err := s.Backlinks("/journal/2026-06-13.md")
	if err != nil {
		t.Fatalf("Backlinks(none): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Backlinks(none) = %v, want empty", none)
	}
}

func TestBacklinksSkipsOversizedExternalFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}

	// A normal note that links to the target is a real backlink.
	mustWrite(t, s, "memory/decisions/linker.md", "---\nkind: memory\n---\nsee [the call](/memory/decisions/target.md) here\n")

	// An oversized file (larger than attn ever writes) is synced in externally,
	// bypassing Write's MaxFileSize guard. It also links to the target, but
	// Backlinks must not pull its whole body into memory — so it is skipped.
	big := append([]byte("---\nkind: memory\n---\nlinks [target](/memory/decisions/target.md)\n"), make([]byte, MaxFileSize+1)...)
	bigPath := filepath.Join(dir, "memory", "decisions", "oversized.md")
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.Backlinks("/memory/decisions/target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	gotPaths := make([]string, len(got))
	for i, e := range got {
		gotPaths[i] = e.Path
	}
	want := []string{"memory/decisions/linker.md"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("Backlinks skipped/included the wrong files: got %v, want %v", gotPaths, want)
	}
}

func mustWrite(t *testing.T, s *Store, relPath, content string) {
	t.Helper()
	// A create-only write (empty baseHash) against an existing path returns a
	// non-nil Conflict with a nil error, not an error — so retrying on err alone
	// silently no-ops the rewrite. Retry as a hash-CAS edit using the conflict's
	// current hash so a test can intentionally overwrite a note.
	_, conflict, err := s.Write(relPath, []byte(content), "")
	if err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	if conflict != nil {
		if _, _, werr := s.Write(relPath, []byte(content), conflict.CurrentHash); werr != nil {
			t.Fatalf("rewrite %s: %v", relPath, werr)
		}
	}
}
