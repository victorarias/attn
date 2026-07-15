package daemon

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Annotation types on the wire (plain strings in the protocol, mirroring the
// frontend's AnnotationType union).
const (
	markdownAnnotationTypeComment  = "comment"
	markdownAnnotationTypeDeletion = "deletion"
	markdownAnnotationTypeGlobal   = "global"
)

// formatMarkdownAnnotationPayload renders the annotation draft for one file
// into the single feedback message typed into the target session's PTY.
// Format ported from plannotator's exportAnnotations with subject "document"
// and the mild annotate framing (never the plan-deny preamble).
//
// Ordering: anchored annotations by document position — (anchor.start_line,
// anchor.start) ascending, stable — then global comments last in creation
// order. (Deliberate deviation from the donor, whose findIndex==-1 accident
// put globals first; the plan's example puts them last.)
//
// orphaned holds the annotation ids the client currently shows as orphaned
// (anchor no longer resolves). Orphanhood is client-derived and non-persisted,
// so it travels in the submit message; orphaned items keep their last-known
// line as "(~line N, moved)".
//
// Defensive: an anchored-type annotation with a nil anchor (shouldn't persist,
// but the store blob is JSON) formats without a line label rather than
// panicking.
func formatMarkdownAnnotationPayload(path string, anns []protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	var anchored, globals []protocol.MarkdownAnnotation
	for _, a := range anns {
		if a.Type == markdownAnnotationTypeGlobal {
			globals = append(globals, a)
		} else {
			anchored = append(anchored, a)
		}
	}
	sort.SliceStable(anchored, func(i, j int) bool {
		li, si := anchorSortKey(anchored[i])
		lj, sj := anchorSortKey(anchored[j])
		if li != lj {
			return li < lj
		}
		return si < sj
	})
	sort.SliceStable(globals, func(i, j int) bool {
		return globals[i].CreatedAt < globals[j].CreatedAt
	})
	sorted := append(anchored, globals...)

	var b strings.Builder
	piece := "pieces"
	if len(sorted) == 1 {
		piece = "piece"
	}
	fmt.Fprintf(&b, "# Markdown Annotations\n\nFile: %s\n\nI've reviewed this document and have %d %s of feedback:\n\n", path, len(sorted), piece)

	// Label Summary bookkeeping: counts by display text, first-appearance
	// order over the sorted list.
	var labelOrder []string
	labelCounts := map[string]int{}

	for i, a := range sorted {
		fmt.Fprintf(&b, "## %d. ", i+1)
		label := markdownAnnotationLineLabel(a, orphaned)
		if label != "" {
			b.WriteString(label)
			b.WriteString(" ")
		}
		exact := ""
		if a.Anchor != nil {
			exact = a.Anchor.Exact
		}
		switch {
		case a.Type == markdownAnnotationTypeDeletion:
			b.WriteString("Remove this\n")
			fmt.Fprintf(&b, "```\n%s\n```\n", exact)
			b.WriteString("> I don't want this in the document.\n")
		case a.Type == markdownAnnotationTypeGlobal:
			b.WriteString("General feedback about the document\n")
			fmt.Fprintf(&b, "> %s\n", protocol.Deref(a.Text))
		case a.QuickLabelID != nil && *a.QuickLabelID != "":
			display := markdownQuickLabelDisplay(a)
			if _, seen := labelCounts[display]; !seen {
				labelOrder = append(labelOrder, display)
			}
			labelCounts[display]++
			fmt.Fprintf(&b, "[%s] Feedback on: \"%s\"\n", display, exact)
			// The tip repeats on every occurrence of the label (donor
			// behavior); a tip-less label emits no quote line.
			if tip := protocol.Deref(a.QuickLabelTip); tip != "" {
				fmt.Fprintf(&b, "> %s\n", tip)
			}
		default: // plain comment
			fmt.Fprintf(&b, "Feedback on: \"%s\"\n", exact)
			fmt.Fprintf(&b, "> %s\n", protocol.Deref(a.Text))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	if len(labelOrder) > 0 {
		b.WriteString("## Label Summary\n\n")
		for _, display := range labelOrder {
			fmt.Fprintf(&b, "- **%s**: %d\n", display, labelCounts[display])
		}
		b.WriteString("\n")
	}
	b.WriteString("Please address the annotation feedback above.")
	return b.String()
}

// anchorSortKey returns the document-position key for an anchored annotation;
// nil anchors sort first, deterministically.
func anchorSortKey(a protocol.MarkdownAnnotation) (line, start int) {
	if a.Anchor == nil {
		return 0, 0
	}
	return a.Anchor.StartLine, a.Anchor.Start
}

// markdownAnnotationLineLabel renders the parenthesized position label:
// "(line N)" for single-line anchors, "(lines A–B)" (en-dash) for ranges,
// "(~line N, moved)" for orphaned ids (last-known line, matching the
// sidebar's "~line N (moved)" display), and "" for a missing anchor.
func markdownAnnotationLineLabel(a protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	if a.Anchor == nil {
		return ""
	}
	if orphaned[a.ID] {
		return fmt.Sprintf("(~line %d, moved)", a.Anchor.StartLine)
	}
	if a.Anchor.EndLine <= a.Anchor.StartLine {
		return fmt.Sprintf("(line %d)", a.Anchor.StartLine)
	}
	return fmt.Sprintf("(lines %d–%d)", a.Anchor.StartLine, a.Anchor.EndLine)
}

// markdownQuickLabelDisplay is the label text shown in the payload:
// the display text snapshotted at creation when present, else the raw id
// (forward-compat fallback mirroring the frontend rule).
func markdownQuickLabelDisplay(a protocol.MarkdownAnnotation) string {
	if text := protocol.Deref(a.QuickLabelText); text != "" {
		return text
	}
	return protocol.Deref(a.QuickLabelID)
}
