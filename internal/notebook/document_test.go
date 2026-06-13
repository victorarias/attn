package notebook

import (
	"reflect"
	"testing"
)

func TestParseRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantFM  map[string]any
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
			raw:      "---\nkind: memory\ntitle: Foo\n---\n# Foo\n\nbody\n",
			wantFM:   map[string]any{"kind": "memory", "title": "Foo"},
			wantBody: "# Foo\n\nbody\n",
		},
		{
			name:     "frontmatter preserves blank line before body",
			raw:      "---\nkind: memory\n---\n\n# Foo\n",
			wantFM:   map[string]any{"kind": "memory"},
			wantBody: "\n# Foo\n",
		},
		{
			name:     "empty body",
			raw:      "---\nkind: journal\n---\n",
			wantFM:   map[string]any{"kind": "journal"},
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
	raw := "---\nkind: memory\ntitle: T\nobsidian-tags: [a, b]\ncustom: 42\n---\nbody\n"
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
	raw := "---\nkind: memory\n  bad: : indentation\n: nope\n---\nbody\n"
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
	doc, _ := Parse([]byte("---\nkind: memory\ntitle: T\nsummary: S\nupdated: 2026-06-13T00:00:00Z\n---\n"))
	if doc.Kind() != "memory" || doc.Title() != "T" || doc.Summary() != "S" || doc.Updated() != "2026-06-13T00:00:00Z" {
		t.Fatalf("accessors = (%q,%q,%q,%q)", doc.Kind(), doc.Title(), doc.Summary(), doc.Updated())
	}
	empty := Document{}
	if empty.Kind() != "" || empty.Title() != "" {
		t.Fatal("accessors on empty document should return empty strings")
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
	raw := "---\nkind: memory\nticket: 007\nver: 1.10\n# a human note\nzeta: last\nalpha: first\n---\n\n# Body\n"
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Kind() != "memory" {
		t.Fatalf("Kind = %q, want memory", doc.Kind())
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
