package prosegate

import "regexp"

// Structure counts the parts of a message that carry information prose cannot.
// A nudge that asks for concision will eventually delete a mermaid diagram, so
// preservation is checked rather than requested.
type Structure struct {
	FencedBlocks int `json:"fenced_blocks"`
	Tables       int `json:"tables"`
	Links        int `json:"links"`
	ListItems    int `json:"list_items"`
	Headings     int `json:"headings"`
}

var (
	fenceOpen = regexp.MustCompile("(?m)^\\s*```")
	tableSep  = regexp.MustCompile(`(?m)^\s*\|?[\s:|-]*-{3,}[\s:|-]*\|?\s*$`)
	linkAny   = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)|https?://\S+`)
	listItem  = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s+\S`)
	heading   = regexp.MustCompile(`(?m)^\s*#{1,6}\s+\S`)
)

// StructureOf counts the structural elements in markdown.
func StructureOf(markdown string) Structure {
	// Fences come in pairs; count blocks, not delimiters.
	return Structure{
		FencedBlocks: len(fenceOpen.FindAllString(markdown, -1)) / 2,
		Tables:       len(tableSep.FindAllString(markdown, -1)),
		Links:        len(linkAny.FindAllString(markdown, -1)),
		ListItems:    len(listItem.FindAllString(markdown, -1)),
		Headings:     len(heading.FindAllString(markdown, -1)),
	}
}

// Lost reports what a rewrite dropped that prose cannot carry: diagrams and
// code, tables, links. Headings and list items are counted but not reported —
// reorganising them is what a plainness rewrite is for, and a warning that
// fires on every honest rewrite is one nobody reads.
func (before Structure) Lost(after Structure) []string {
	var lost []string
	check := func(name string, b, a int) {
		if a < b {
			lost = append(lost, name)
		}
	}
	check("fenced blocks", before.FencedBlocks, after.FencedBlocks)
	check("tables", before.Tables, after.Tables)
	check("links", before.Links, after.Links)
	return lost
}

// Preserved reports whether a rewrite kept everything Lost watches.
func (before Structure) Preserved(after Structure) bool {
	return len(before.Lost(after)) == 0
}
