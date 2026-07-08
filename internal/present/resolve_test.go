package present

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveTestRepo creates a one-commit repo with a single file whose content
// is exactly lines, so tests can assert resolution against known line numbers.
func resolveTestRepo(t *testing.T, path string, lines []string) (dir, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init")

	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	runGit(t, dir, "add", path)
	runGit(t, dir, "commit", "-m", "head")
	headSHA = runGit(t, dir, "rev-parse", "HEAD")
	return dir, headSHA
}

func TestResolveAnnotations_UniqueAnchor(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"package a", "func Foo() {", "  return", "}"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Anchor: "func Foo", Note: "entry point"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
	got := resolved["a.go"]
	if len(got) != 1 {
		t.Fatalf("resolved[a.go] = %+v, want 1 entry", got)
	}
	if got[0].LineStart != 2 || got[0].LineEnd != 2 {
		t.Errorf("LineStart/End = %d/%d, want 2/2", got[0].LineStart, got[0].LineEnd)
	}
	if len(got[0].Comments) != 1 || got[0].Comments[0] != "entry point" {
		t.Errorf("Comments = %+v, want [entry point]", got[0].Comments)
	}
}

func TestResolveAnnotations_AmbiguousAnchorWarnsAndResolvesFirst(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"foo bar", "baz foo", "qux"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Anchor: "foo", Note: "note"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want 1", issues)
	}
	if !issues[0].Warning {
		t.Errorf("issue.Warning = false, want true for ambiguous anchor")
	}
	if issues[0].Path != "a.go" || issues[0].Index != 0 {
		t.Errorf("issue path/index = %q/%d, want a.go/0", issues[0].Path, issues[0].Index)
	}

	got := resolved["a.go"]
	if len(got) != 1 || got[0].LineStart != 1 || got[0].LineEnd != 1 {
		t.Fatalf("resolved[a.go] = %+v, want first match at line 1", got)
	}
}

func TestResolveAnnotations_MissingAnchorErrorsAndDrops(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"package a"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Anchor: "nonexistent", Note: "note"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 1 || issues[0].Warning {
		t.Fatalf("issues = %+v, want 1 error issue", issues)
	}
	if _, ok := resolved["a.go"]; ok {
		t.Errorf("resolved[a.go] present, want dropped after unresolved anchor")
	}
}

func TestResolveAnnotations_LineOutOfBounds(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"one", "two"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Line: 10, Note: "note"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 1 || issues[0].Warning {
		t.Fatalf("issues = %+v, want 1 error issue", issues)
	}
	if _, ok := resolved["a.go"]; ok {
		t.Errorf("resolved[a.go] present, want dropped for out-of-bounds line")
	}
}

func TestResolveAnnotations_RangeOutOfBounds(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"one", "two"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Start: 1, End: 10, Note: "note"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 1 || issues[0].Warning {
		t.Fatalf("issues = %+v, want 1 error issue", issues)
	}
	if _, ok := resolved["a.go"]; ok {
		t.Errorf("resolved[a.go] present, want dropped for out-of-bounds range")
	}
}

func TestResolveAnnotations_RangeAndThreadShape(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"one", "two", "three"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go", Annotations: []AnnotationEntry{
			{Start: 1, End: 3, Thread: []string{"first", "second"}},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
	got := resolved["a.go"]
	if len(got) != 1 || got[0].LineStart != 1 || got[0].LineEnd != 3 {
		t.Fatalf("resolved[a.go] = %+v, want range 1-3", got)
	}
	if len(got[0].Comments) != 2 || got[0].Comments[0] != "first" || got[0].Comments[1] != "second" {
		t.Errorf("Comments = %+v, want [first second]", got[0].Comments)
	}
}

func TestResolveAnnotations_MissingFileDropsAllItsAnnotations(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"one"})

	m := &Manifest{Files: []FileEntry{
		{Path: "does-not-exist.go", Annotations: []AnnotationEntry{
			{Line: 1, Note: "note"},
		}},
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 1 || issues[0].Warning || issues[0].Path != "does-not-exist.go" || issues[0].Index != -1 {
		t.Fatalf("issues = %+v, want 1 file-level error issue", issues)
	}
	if _, ok := resolved["does-not-exist.go"]; ok {
		t.Errorf("resolved[does-not-exist.go] present, want absent")
	}
}

func TestResolveAnnotations_NoAnnotationsSkipsFileEntirely(t *testing.T) {
	dir, headSHA := resolveTestRepo(t, "a.go", []string{"one"})

	m := &Manifest{Files: []FileEntry{
		{Path: "a.go"},
		{Path: "does-not-exist.go"}, // no annotations: must not be looked up at all
	}}

	resolved, issues := ResolveAnnotations(m, dir, headSHA)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none (no annotations anywhere)", issues)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved = %+v, want empty", resolved)
	}
}
