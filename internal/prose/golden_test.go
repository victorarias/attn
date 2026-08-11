package prose

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// TestGolden pins the findings for each fixture, so a change in a rule shows
// up as a diff of what a writer would be told rather than as a count.
//
// Regenerate with: go test ./internal/prose -run TestGolden -update
func TestGolden(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read golden dir: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			path := filepath.Join(dir, entry.Name())
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			findings, err := Check(entry.Name(), source, Options{})
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			got := renderGolden(findings)

			want := path + ".findings"
			if *update {
				if err := os.WriteFile(want, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			expected, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if got != string(expected) {
				t.Errorf("findings changed for %s\n--- want ---\n%s\n--- got ---\n%s", entry.Name(), expected, got)
			}
		})
	}
}

func renderGolden(findings []Finding) string {
	var b strings.Builder
	if len(findings) == 0 {
		return "(no findings)\n"
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "%s:%d:%d %s\n", f.File, f.Line, f.Column, f.Rule)
		fmt.Fprintf(&b, "    span: %s\n", f.Span)
		fmt.Fprintf(&b, "    objection: %s\n", f.Objection)
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "    suggestion: %s\n", f.Suggestion)
		}
	}
	return b.String()
}

// TestStaccatoIsNotBlessedByLength is the anti-gaming property stated as a
// test: the staccato fixture passes every length check and is still wrong.
func TestStaccatoIsNotBlessedByLength(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "golden", "staccato.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if findings, err := Check("staccato.md", source, Options{Only: []string{"long-sentence", "idea-density"}}); err != nil {
		t.Fatalf("check: %v", err)
	} else if len(findings) != 0 {
		t.Fatalf("the length rules were supposed to bless this fixture; they found %d", len(findings))
	}
	findings, err := Check("staccato.md", source, Options{Only: []string{"staccato-run", "lost-thread", "flat-rhythm"}})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("nothing caught chopped prose that every length rule blessed")
	}
}

// TestMarkdownShapesAreNotProse checks the fixture built entirely out of the
// things that must never be linted. Every one of them carries the phrases the
// rules object to, so a finding anywhere in it names the construct that leaked.
func TestMarkdownShapesAreNotProse(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "golden", "markdown-shapes.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := Parse("markdown-shapes.md", source)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, sent := range doc.Sentences {
		for _, forbidden := range []string{"code fence", "mermaid", "indented code", "HTML comment", "cells are not prose", "there-is-a-url", "front matter"} {
			if strings.Contains(sent.Text, forbidden) {
				line, _ := doc.Position(sent.Start)
				t.Errorf("line %d: %q was linted as prose; it is %s", line, collapseSpace(sent.Text), forbidden)
			}
		}
	}
}
