package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func mdAnchor(startLine, endLine, start int, exact string) *protocol.MarkdownAnnotationAnchor {
	return &protocol.MarkdownAnnotationAnchor{
		BlockID:   "b",
		StartLine: startLine,
		EndLine:   endLine,
		Start:     start,
		End:       start + len(exact),
		Exact:     exact,
	}
}

// Mixed document: sorting by position, range + single-line labels, deletion
// fence, quick label with tip, global last, label summary, closing line.
func TestFormatMarkdownAnnotationPayloadMixed(t *testing.T) {
	anns := []protocol.MarkdownAnnotation{
		// Deliberately scrambled creation order to prove position sorting.
		{ID: "del", Type: "deletion", Anchor: mdAnchor(30, 30, 0, "old paragraph"), CreatedAt: 1},
		{ID: "glob", Type: "global", Text: protocol.Ptr("a global comment"), CreatedAt: 2},
		{ID: "range", Type: "comment", Anchor: mdAnchor(12, 18, 4, "the selected text"), Text: protocol.Ptr("the reviewer's comment"), CreatedAt: 3},
		{ID: "ql", Type: "comment", Anchor: mdAnchor(5, 5, 2, "selected text"),
			QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"),
			QuickLabelTip: protocol.Ptr("Keep more of this"), CreatedAt: 4},
	}
	want := "# Markdown Annotations\n" +
		"\n" +
		"File: /tmp/doc.md\n" +
		"\n" +
		"I've reviewed this document and have 4 pieces of feedback:\n" +
		"\n" +
		"## 1. (line 5) [👍 Looks good] Feedback on: \"selected text\"\n" +
		"> Keep more of this\n" +
		"\n" +
		"## 2. (lines 12–18) Feedback on: \"the selected text\"\n" +
		"> the reviewer's comment\n" +
		"\n" +
		"## 3. (line 30) Remove this\n" +
		"```\n" +
		"old paragraph\n" +
		"```\n" +
		"> I don't want this in the document.\n" +
		"\n" +
		"## 4. General feedback about the document\n" +
		"> a global comment\n" +
		"\n" +
		"---\n" +
		"## Label Summary\n" +
		"\n" +
		"- **👍 Looks good**: 1\n" +
		"\n" +
		"Please address the annotation feedback above."
	got := formatMarkdownAnnotationPayload("/tmp/doc.md", anns, nil)
	if got != want {
		t.Fatalf("payload mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A single annotation uses the singular "1 piece of feedback" and skips the
// label summary entirely (no quick labels).
func TestFormatMarkdownAnnotationPayloadSingularNoSummary(t *testing.T) {
	anns := []protocol.MarkdownAnnotation{
		{ID: "c", Type: "comment", Anchor: mdAnchor(7, 7, 0, "text"), Text: protocol.Ptr("note"), CreatedAt: 1},
	}
	want := "# Markdown Annotations\n" +
		"\n" +
		"File: /doc.md\n" +
		"\n" +
		"I've reviewed this document and have 1 piece of feedback:\n" +
		"\n" +
		"## 1. (line 7) Feedback on: \"text\"\n" +
		"> note\n" +
		"\n" +
		"---\n" +
		"Please address the annotation feedback above."
	got := formatMarkdownAnnotationPayload("/doc.md", anns, nil)
	if got != want {
		t.Fatalf("payload mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Orphaned ids keep their last-known line as "(~line N, moved)" and are still
// included in the payload.
func TestFormatMarkdownAnnotationPayloadOrphaned(t *testing.T) {
	anns := []protocol.MarkdownAnnotation{
		{ID: "orph", Type: "comment", Anchor: mdAnchor(7, 9, 0, "moved text"), Text: protocol.Ptr("still relevant"), CreatedAt: 1},
	}
	got := formatMarkdownAnnotationPayload("/doc.md", anns, map[string]bool{"orph": true})
	wantLine := "## 1. (~line 7, moved) Feedback on: \"moved text\"\n> still relevant\n"
	if !containsStr(got, wantLine) {
		t.Fatalf("payload missing orphan label %q:\n%s", wantLine, got)
	}
}

// Quick-label behavior: the tip repeats on every occurrence, a tip-less label
// emits no quote line, a missing quick_label_text falls back to the raw id,
// and the label summary groups by display text in first-appearance order.
func TestFormatMarkdownAnnotationPayloadQuickLabels(t *testing.T) {
	tip := protocol.Ptr("nice")
	anns := []protocol.MarkdownAnnotation{
		{ID: "a", Type: "comment", Anchor: mdAnchor(1, 1, 0, "one"),
			QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"), QuickLabelTip: tip, CreatedAt: 1},
		{ID: "b", Type: "comment", Anchor: mdAnchor(2, 2, 0, "two"),
			QuickLabelID: protocol.Ptr("confusing"), CreatedAt: 2}, // no text -> raw id; no tip -> no quote line
		{ID: "c", Type: "comment", Anchor: mdAnchor(3, 3, 0, "three"),
			QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"), QuickLabelTip: tip, CreatedAt: 3},
	}
	want := "# Markdown Annotations\n" +
		"\n" +
		"File: /doc.md\n" +
		"\n" +
		"I've reviewed this document and have 3 pieces of feedback:\n" +
		"\n" +
		"## 1. (line 1) [👍 Looks good] Feedback on: \"one\"\n" +
		"> nice\n" +
		"\n" +
		"## 2. (line 2) [confusing] Feedback on: \"two\"\n" +
		"\n" +
		"## 3. (line 3) [👍 Looks good] Feedback on: \"three\"\n" +
		"> nice\n" +
		"\n" +
		"---\n" +
		"## Label Summary\n" +
		"\n" +
		"- **👍 Looks good**: 2\n" +
		"- **confusing**: 1\n" +
		"\n" +
		"Please address the annotation feedback above."
	got := formatMarkdownAnnotationPayload("/doc.md", anns, nil)
	if got != want {
		t.Fatalf("payload mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Anchored items sort by (start_line, start) — same line orders by start
// offset, never by creation order.
func TestFormatMarkdownAnnotationPayloadSortWithinLine(t *testing.T) {
	anns := []protocol.MarkdownAnnotation{
		{ID: "later", Type: "comment", Anchor: mdAnchor(4, 4, 20, "tail"), Text: protocol.Ptr("second"), CreatedAt: 1},
		{ID: "earlier", Type: "comment", Anchor: mdAnchor(4, 4, 2, "head"), Text: protocol.Ptr("first"), CreatedAt: 2},
	}
	got := formatMarkdownAnnotationPayload("/doc.md", anns, nil)
	wantOrder := "## 1. (line 4) Feedback on: \"head\"\n> first\n\n## 2. (line 4) Feedback on: \"tail\"\n> second\n"
	if !containsStr(got, wantOrder) {
		t.Fatalf("items not ordered by start offset:\n%s", got)
	}
}

// Defensive: an anchored-type annotation with a nil anchor (corrupt JSON blob)
// formats without a line label instead of panicking.
func TestFormatMarkdownAnnotationPayloadNilAnchor(t *testing.T) {
	anns := []protocol.MarkdownAnnotation{
		{ID: "x", Type: "comment", Text: protocol.Ptr("dangling"), CreatedAt: 1},
	}
	got := formatMarkdownAnnotationPayload("/doc.md", anns, nil)
	want := "## 1. Feedback on: \"\"\n> dangling\n"
	if !containsStr(got, want) {
		t.Fatalf("nil-anchor item mis-rendered, want %q in:\n%s", want, got)
	}
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
