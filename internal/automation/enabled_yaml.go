package automation

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// SetEnabledInYAML sets the top-level `enabled` key of a definition YAML
// document to enabled, rewriting only the exact bytes of that one scalar
// token and leaving every other byte — comments, key order, indentation,
// quoting style, blank lines — untouched. A round-trip through
// yaml.Marshal (as MarshalDefinitionYAML does) would lose the user's
// original formatting entirely, which defeats this PR's headline feature:
// hand-written comments must survive a toggle.
//
// It locates the `enabled` value using yaml.v3's parsed Line/Column (both
// 1-based) rather than a text scan, so a nested `enabled:` under `launch:`
// or `policy:`, or the word "enabled:" inside a comment or a quoted string,
// can never be mistaken for the real key: those never appear as a key node
// in the top-level mapping's own Content.
//
// If the document has no top-level `enabled` key, one is appended as a new
// `enabled: <v>` line at the very end of the document (preceded by a
// newline if the document doesn't already end in one) rather than inserted
// among the existing keys — simpler than reconstructing a "right" position,
// and just as valid: YAML block-mapping keys don't need to stay grouped.
//
// Returns an error if doc does not parse, or its top level is not a
// mapping.
func SetEnabledInYAML(doc []byte, enabled bool) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("parse definition yaml: %w", err)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("definition yaml must be a top-level mapping")
	}
	mapping := root.Content[0]
	newValue := "false"
	if enabled {
		newValue = "true"
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Value != "enabled" {
			continue
		}
		value := mapping.Content[i+1]
		start, ok := scalarStartOffset(doc, value.Line, value.Column)
		if !ok {
			return nil, errors.New("locate enabled value in definition yaml")
		}
		end, err := scalarEndOffset(doc, start, value.Style)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(doc)-(end-start)+len(newValue))
		out = append(out, doc[:start]...)
		out = append(out, newValue...)
		out = append(out, doc[end:]...)
		return out, nil
	}
	out := make([]byte, 0, len(doc)+len(newValue)+len("enabled: \n")+1)
	out = append(out, doc...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, "enabled: "...)
	out = append(out, newValue...)
	out = append(out, '\n')
	return out, nil
}

// scalarStartOffset converts a yaml.v3 1-based (line, column) position into
// a byte offset into doc. Column advances by rune, not byte, matching how
// yaml.v3's scanner counts columns — for the ASCII "enabled:" key this is
// the same as a byte count, but staying rune-accurate keeps this correct
// even when other lines in the document carry non-ASCII text (e.g. a
// comment or prompt earlier in the file).
func scalarStartOffset(doc []byte, line, column int) (int, bool) {
	offset := 0
	currentLine := 1
	for currentLine < line {
		idx := bytes.IndexByte(doc[offset:], '\n')
		if idx < 0 {
			return 0, false
		}
		offset += idx + 1
		currentLine++
	}
	for i := 1; i < column; i++ {
		r, size := utf8.DecodeRune(doc[offset:])
		if size == 0 || (r == utf8.RuneError && size == 1) {
			return 0, false
		}
		offset += size
	}
	return offset, true
}

// scalarEndOffset returns the byte offset just past the scalar token that
// starts at start, given the style yaml.v3 recorded for it. Plain and
// quoted styles are the only ones a hand-written boolean value realistically
// uses; block styles (| or >) are rejected rather than mishandled.
func scalarEndOffset(doc []byte, start int, style yaml.Style) (int, error) {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return doubleQuotedEnd(doc, start)
	case style&yaml.SingleQuotedStyle != 0:
		return singleQuotedEnd(doc, start)
	case style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return 0, errors.New("enabled value uses a block scalar style, which is not supported")
	default:
		return plainScalarEnd(doc, start), nil
	}
}

// doubleQuotedEnd scans from the opening `"` at start to the byte after its
// matching closing `"`, treating any backslash as escaping the next byte
// (adequate for finding the terminator; it does not need to interpret the
// escape).
func doubleQuotedEnd(doc []byte, start int) (int, error) {
	for i := start + 1; i < len(doc); i++ {
		switch doc[i] {
		case '\\':
			i++
		case '"':
			return i + 1, nil
		}
	}
	return 0, errors.New("unterminated double-quoted enabled value")
}

// singleQuotedEnd scans from the opening `'` at start to the byte after its
// matching closing `'`, treating two consecutive single quotes as an
// escaped literal quote rather than the terminator, per YAML single-quote
// escaping.
func singleQuotedEnd(doc []byte, start int) (int, error) {
	for i := start + 1; i < len(doc); i++ {
		if doc[i] != '\'' {
			continue
		}
		if i+1 < len(doc) && doc[i+1] == '\'' {
			i++
			continue
		}
		return i + 1, nil
	}
	return 0, errors.New("unterminated single-quoted enabled value")
}

// plainScalarEnd returns the end of an unquoted scalar token starting at
// start: the first line break, the first "<whitespace>#" comment marker, or
// a flow terminator (',', ']', '}'), whichever comes first, with any
// trailing spaces/tabs trimmed off.
func plainScalarEnd(doc []byte, start int) int {
	i := start
	for i < len(doc) {
		c := doc[i]
		if c == '\n' {
			break
		}
		if c == '#' && i > start && (doc[i-1] == ' ' || doc[i-1] == '\t') {
			break
		}
		if c == ',' || c == ']' || c == '}' {
			break
		}
		i++
	}
	end := i
	for end > start && (doc[end-1] == ' ' || doc[end-1] == '\t') {
		end--
	}
	return end
}
