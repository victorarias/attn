package activity

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeTakesTheAnswerOutOfWhateverItArrivedIn(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "running the frontend test suite", "running the frontend test suite"},
		{"trailing period", "running the frontend test suite.", "running the frontend test suite"},
		{"trailing ellipsis", "running the frontend test suite…", "running the frontend test suite"},
		{"wrapped in quotes", `"running the frontend test suite"`, "running the frontend test suite"},
		{"wrapped in backticks", "`running the test suite`", "running the test suite"},
		{"fenced", "```\nrunning the test suite\n```", "running the test suite"},
		{"bulleted", "- running the test suite", "running the test suite"},
		{"elaborated", "running the test suite\n\nIt has been going for a while.", "running the test suite"},
		{"padded", "   running the test suite   ", "running the test suite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Sanitize(tc.raw)
			if !ok {
				t.Fatalf("Sanitize(%q) reported nothing usable", tc.raw)
			}
			if got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// Nothing usable must come back not-ok rather than empty, so the caller keeps
// the previous line instead of writing nothing over something true.
func TestSanitizeReportsWhenNothingSurvived(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\n", "```\n```", "...", `""`} {
		if line, ok := Sanitize(raw); ok {
			t.Errorf("Sanitize(%q) = %q, ok — want nothing usable", raw, line)
		}
	}
}

func TestSanitizeTruncatesRatherThanRejects(t *testing.T) {
	long := strings.Repeat("running the frontend test suite ", 10)
	line, ok := Sanitize(long)
	if !ok {
		t.Fatal("a long line came back unusable; it is still mostly right")
	}
	if n := utf8.RuneCountInString(line); n > MaxLineRunes {
		t.Errorf("line is %d runes, want at most %d", n, MaxLineRunes)
	}
	if !strings.HasSuffix(line, "…") {
		t.Errorf("truncated line = %q, want it to show that it was cut", line)
	}
}
