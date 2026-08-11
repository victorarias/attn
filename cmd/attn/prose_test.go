package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prose"
)

func TestWriteProseFindings(t *testing.T) {
	var b strings.Builder
	writeProseFindings(&b, []prose.Finding{
		{Rule: "expletive-opener", File: "docs/a.md", Line: 12, Column: 3, Objection: "starts on a placeholder"},
		{Rule: "hidden-verb", File: "docs/a.md", Line: 20, Column: 5, Objection: "buries the verb", Suggestion: "consider"},
	})
	want := "docs/a.md:12:3: expletive-opener: starts on a placeholder\n" +
		"docs/a.md:20:5: hidden-verb: buries the verb\n" +
		"    use \"consider\"\n"
	if b.String() != want {
		t.Errorf("output was:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestProseSummary(t *testing.T) {
	for _, tc := range []struct {
		findings, files int
		want            string
	}{
		{0, 1, "prose check: clean (1 file)"},
		{0, 4, "prose check: clean (4 files)"},
		{1, 1, "prose check: 1 finding in 1 file"},
		{9, 3, "prose check: 9 findings in 3 files"},
	} {
		if got := proseSummary(tc.findings, tc.files); got != tc.want {
			t.Errorf("proseSummary(%d, %d) = %q, want %q", tc.findings, tc.files, got, tc.want)
		}
	}
}

// TestValidateProseRulesNamesTheLimit: an agent can act on "unknown rule
// no-adverbs" and cannot act on a silent no-op that reports nothing.
func TestValidateProseRulesNamesTheLimit(t *testing.T) {
	if err := validateProseRules(prose.RuleNames()); err != nil {
		t.Errorf("every real rule name was rejected: %v", err)
	}
	err := validateProseRules([]string{"idea-density", "no-adverbs"})
	if err == nil {
		t.Fatal("an unknown rule was accepted silently")
	}
	for _, want := range []string{"no-adverbs", "attn prose rules"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestLoadProseVocabularyResolution covers the three ways the word list is
// chosen, including the explicit empty one that turns it off.
func TestLoadProseVocabularyResolution(t *testing.T) {
	root := t.TempDir()
	vocabDir := filepath.Join(root, "docs", "prose", "vocabulary")
	if err := os.MkdirAll(vocabDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vocabDir, "reject.txt"), []byte("utilize\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "docs", "plans", "a.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("We utilize a queue.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := loadProseVocabulary(doc, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if found.Dir != vocabDir {
		t.Errorf("walking up from the file found %q, want %q", found.Dir, vocabDir)
	}

	off, err := loadProseVocabulary(doc, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if off.Dir != "" {
		t.Errorf("--vocab \"\" still resolved to %q; it is the way to turn the list off", off.Dir)
	}

	explicit, err := loadProseVocabulary(doc, vocabDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Dir != vocabDir {
		t.Errorf("--vocab %q resolved to %q", vocabDir, explicit.Dir)
	}
}

// TestProseHelpDocumentsTheInertFlag: --deterministic-only is accepted now and
// does nothing, and a flag that silently does nothing is a trap.
func TestProseHelpDocumentsTheInertFlag(t *testing.T) {
	var b strings.Builder
	writeProseHelp(&b)
	help := b.String()
	if !strings.Contains(help, "--deterministic-only") {
		t.Fatal("the help does not mention --deterministic-only")
	}
	if !strings.Contains(help, "inert") {
		t.Error("the help does not say --deterministic-only currently does nothing")
	}
	for _, code := range []string{"0  clean", "1  findings", "2  "} {
		if !strings.Contains(help, code) {
			t.Errorf("the help does not document exit code %q", code)
		}
	}
}
