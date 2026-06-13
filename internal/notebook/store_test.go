package notebook

import (
	"os"
	"path/filepath"
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
