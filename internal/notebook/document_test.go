package notebook

import (
	"reflect"
	"testing"
)

func TestParseRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantFM   map[string]any
		wantBody string
	}{
		{
			name:     "no frontmatter",
			raw:      "# Title\n\nbody text\n",
			wantFM:   nil,
			wantBody: "# Title\n\nbody text\n",
		},
		{
			name:     "frontmatter and body",
			raw:      "---\ntype: note\ntitle: Foo\n---\n# Foo\n\nbody\n",
			wantFM:   map[string]any{"type": "note", "title": "Foo"},
			wantBody: "# Foo\n\nbody\n",
		},
		{
			name:     "frontmatter preserves blank line before body",
			raw:      "---\ntype: note\n---\n\n# Foo\n",
			wantFM:   map[string]any{"type": "note"},
			wantBody: "\n# Foo\n",
		},
		{
			name:     "empty body",
			raw:      "---\ntype: journal\n---\n",
			wantFM:   map[string]any{"type": "journal"},
			wantBody: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(doc.Frontmatter, tc.wantFM) {
				t.Fatalf("Frontmatter = %#v, want %#v", doc.Frontmatter, tc.wantFM)
			}
			if doc.Body != tc.wantBody {
				t.Fatalf("Body = %q, want %q", doc.Body, tc.wantBody)
			}
			// Round-trip: parsing the re-serialized form yields an equal document.
			reparsed, err := Parse(doc.Bytes())
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if !reflect.DeepEqual(reparsed.Frontmatter, doc.Frontmatter) || reparsed.Body != doc.Body {
				t.Fatalf("round-trip mismatch: got (%#v, %q), want (%#v, %q)",
					reparsed.Frontmatter, reparsed.Body, doc.Frontmatter, doc.Body)
			}
		})
	}
}

// Unknown keys (e.g. written by Obsidian or an external sync tool) must survive
// a parse + serialize cycle, not be dropped.
func TestUnknownKeysPreserved(t *testing.T) {
	raw := "---\ntype: note\ntitle: T\nobsidian-tags: [a, b]\ncustom: 42\n---\nbody\n"
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Frontmatter["custom"] != 42 {
		t.Fatalf("custom key dropped or changed: %#v", doc.Frontmatter["custom"])
	}
	if _, ok := doc.Frontmatter["obsidian-tags"]; !ok {
		t.Fatal("obsidian-tags key dropped")
	}
	out := string(doc.Bytes())
	for _, want := range []string{"obsidian-tags", "custom"} {
		if !contains(out, want) {
			t.Fatalf("serialized output dropped %q:\n%s", want, out)
		}
	}
}

// The read/list path must never fail on malformed frontmatter.
func TestParsePermissiveOnMalformedYAML(t *testing.T) {
	raw := "---\ntype: note\n  bad: : indentation\n: nope\n---\nbody\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse should report malformed YAML as an error")
	}
	doc := ParsePermissive([]byte(raw))
	if doc.Frontmatter != nil {
		t.Fatalf("permissive parse should yield nil frontmatter on malformed YAML, got %#v", doc.Frontmatter)
	}
	if doc.Body != raw {
		t.Fatalf("permissive parse should keep raw content as body, got %q", doc.Body)
	}
}

func TestDocumentAccessors(t *testing.T) {
	// Type/summary/updated come from frontmatter; the title comes from the body's
	// first `# H1`, not a frontmatter field.
	doc, _ := Parse([]byte("---\ntype: note\nsummary: S\nupdated: 2026-06-13T00:00:00Z\n---\n# T\n\nbody\n"))
	if doc.Type() != "note" || doc.Title() != "T" || doc.Summary() != "S" || doc.Updated() != "2026-06-13T00:00:00Z" {
		t.Fatalf("accessors = (%q,%q,%q,%q)", doc.Type(), doc.Title(), doc.Summary(), doc.Updated())
	}
	empty := Document{}
	if empty.Type() != "" || empty.Title() != "" {
		t.Fatal("accessors on empty document should return empty strings")
	}
}

// Title is the body's first level-1 ATX heading. A frontmatter `title:` is NOT a
// title source (the canonical title is the `# H1`); the filename is the fallback,
// supplied by callers when Title() is "".
func TestTitleFromH1(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"first line heading", "# Hello world\n\nbody", "Hello world"},
		{"heading after prose", "intro line\n\n# The title\n\nmore", "The title"},
		{"no h1 returns empty", "## Only an h2\n\nbody", ""},
		{"empty body", "", ""},
		{"trailing hashes stripped", "# F# notes ##\n", "F# notes"},
		{"hash without space is not a heading", "#nope\n# yes\n", "yes"},
		{"up to three leading spaces", "   # Indented\n", "Indented"},
		{"four spaces is a code block, not a heading", "    # Not a heading\n# Real\n", "Real"},
		{"h1 inside a fenced block is skipped", "```\n# fake\n```\n# Real one\n", "Real one"},
		{"tilde fence is also skipped", "~~~\n# fake\n~~~\n# Real\n", "Real"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := Document{Body: c.body}
			if got := doc.Title(); got != c.want {
				t.Fatalf("Title() = %q, want %q", got, c.want)
			}
		})
	}
}

// A frontmatter `title:` does not leak into the title — the body's H1 wins, and a
// note with a frontmatter title but no H1 has no title (callers use the filename).
func TestFrontmatterTitleIsNotATitleSource(t *testing.T) {
	withH1, _ := Parse([]byte("---\ntitle: From frontmatter\n---\n# From heading\n"))
	if got := withH1.Title(); got != "From heading" {
		t.Fatalf("Title() = %q, want the body H1 %q", got, "From heading")
	}
	noH1, _ := Parse([]byte("---\ntitle: From frontmatter\n---\n\nbody, no heading\n"))
	if got := noH1.Title(); got != "" {
		t.Fatalf("Title() = %q, want empty (frontmatter title is ignored)", got)
	}
}

// Type() falls back to a legacy `kind` field so a note an external tool wrote
// before the field was renamed still resolves. attn always writes `type`, and a
// present `type` wins over a stray `kind`.
func TestTypeReadsLegacyKind(t *testing.T) {
	legacy, _ := Parse([]byte("---\nkind: memory\n---\n"))
	if legacy.Type() != "memory" {
		t.Fatalf("Type() should fall back to legacy kind, got %q", legacy.Type())
	}
	both, _ := Parse([]byte("---\ntype: note\nkind: memory\n---\n"))
	if both.Type() != "note" {
		t.Fatalf("Type() should prefer type over legacy kind, got %q", both.Type())
	}
}

// A "---" first line without a matching closing fence is not frontmatter.
func TestNoClosingFenceIsBody(t *testing.T) {
	raw := "---\nnot really frontmatter\nstill body\n"
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Frontmatter != nil {
		t.Fatalf("expected no frontmatter, got %#v", doc.Frontmatter)
	}
	if doc.Body != raw {
		t.Fatalf("Body = %q, want full raw", doc.Body)
	}
}

// A document parsed from disk must re-serialize byte-for-byte: comments, key
// order, line endings, and ambiguous scalars (ids like 007, versions like 1.10)
// are all preserved. This is the preserve-verbatim round-trip the design
// requires for Obsidian/sync/user-authored fields.
func TestParseRoundTripIsByteFaithful(t *testing.T) {
	raw := "---\ntype: note\nticket: 007\nver: 1.10\n# a human note\nzeta: last\nalpha: first\n---\n\n# Body\n"
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Type() != "note" {
		t.Fatalf("Type = %q, want note", doc.Type())
	}
	if out := string(doc.Bytes()); out != raw {
		t.Fatalf("round-trip not byte-faithful:\n got: %q\nwant: %q", out, raw)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
