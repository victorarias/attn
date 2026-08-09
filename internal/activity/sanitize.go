package activity

import (
	"strings"
	"unicode/utf8"
)

// Sanitize turns whatever an agent returned into the one line a dashboard row
// can render, and reports whether anything usable survived.
//
// It is deliberately forgiving where Check is strict. Check exists to make a
// prompt regression loud in the harness; Sanitize exists so a single wrapped
// quote or a stray trailing period never costs the user their line. The one
// thing it will not do is invent content: an empty or preamble-only answer comes
// back not-ok, and the caller keeps the previous line rather than writing
// nothing over something true.
func Sanitize(raw string) (string, bool) {
	line := firstMeaningfulLine(raw)
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`")
	line = strings.TrimSpace(line)

	// A model that wraps its answer in quotes is answering correctly in the
	// wrong costume. Strip a matched pair, once.
	if len(line) >= 2 {
		first, last := line[0], line[len(line)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			line = strings.TrimSpace(line[1 : len(line)-1])
		}
	}

	// A trailing period reads as a sentence in a column of fragments. A trailing
	// ellipsis is worse — it looks like the UI is still loading.
	line = strings.TrimRight(line, " .…")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}

	// Truncate rather than reject: a line that ran long is still mostly right,
	// and an over-long line is a rendering problem the row would have anyway.
	if utf8.RuneCountInString(line) > MaxLineRunes {
		runes := []rune(line)
		line = strings.TrimRight(string(runes[:MaxLineRunes-1]), " ,;:-") + "…"
	}
	return line, true
}

// firstMeaningfulLine takes the first non-blank line, skipping markdown
// scaffolding a chatty model may wrap its answer in. It takes the FIRST rather
// than the last because a model that says more than one line has already
// answered on line one and is elaborating.
func firstMeaningfulLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		// A bulleted answer is still an answer.
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		return trimmed
	}
	return ""
}
