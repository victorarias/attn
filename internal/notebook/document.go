package notebook

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is a parsed Notebook markdown file: optional YAML frontmatter plus a
// markdown body. A document parsed from disk re-serializes its frontmatter
// content byte-for-byte — key order, comments, and scalar text (e.g. ids like
// 007 or versions like 1.10) are all preserved — so fields written by Obsidian,
// an external sync tool, or the user survive an attn rewrite untouched (the
// "---" fence lines themselves are normalized to LF). Only an attn-constructed
// document is serialized from the parsed map (in deterministic sorted-key order).
type Document struct {
	// Frontmatter holds every key from the YAML frontmatter block, parsed for
	// reading (accessors). A nil map means the file had no frontmatter block.
	// Mutating this map after Parse does NOT change what Bytes emits for a
	// parsed document — construct a fresh Document (leaving rawFrontmatter
	// empty) to serialize edited frontmatter.
	Frontmatter map[string]any
	// Body is the markdown content after the frontmatter block, with the
	// closing fence (and its trailing newline) removed and the remainder kept
	// byte-for-byte.
	Body string
	// rawFrontmatter is the exact YAML text between the fences as read from
	// disk (empty for an attn-constructed document). When set, Bytes emits it
	// verbatim, making round-trips byte-faithful.
	rawFrontmatter string
}

const frontmatterFence = "---"

// Parse splits raw bytes into frontmatter and body. A file whose first line is
// not a "---" fence, or that has no closing fence, is treated as having no
// frontmatter (the whole content is the body) — never an error. Parse only
// returns an error when a well-formed frontmatter block contains malformed
// YAML; callers that must not fail should use ParsePermissive.
func Parse(raw []byte) (Document, error) {
	fm, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return Document{Body: body}, nil
	}
	if strings.TrimSpace(fm) == "" {
		return Document{Frontmatter: map[string]any{}, Body: body, rawFrontmatter: fm}, nil
	}
	meta, err := decodeFrontmatter([]byte(fm))
	if err != nil {
		return Document{Body: string(raw)}, fmt.Errorf("parse frontmatter: %w", err)
	}
	return Document{Frontmatter: meta, Body: body, rawFrontmatter: fm}, nil
}

// decodeFrontmatter decodes a YAML mapping into map[string]any, preserving every
// key. Scalars keep their native YAML type (int/bool/float/string) EXCEPT
// timestamps, which are kept as their literal text rather than coerced to
// time.Time — so frontmatter dates and ids round-trip exactly and the string
// accessors (Updated, etc.) work without type surprises.
func decodeFrontmatter(text []byte) (map[string]any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(text, &root); err != nil {
		return nil, err
	}
	content := &root
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return map[string]any{}, nil
		}
		content = root.Content[0]
	}
	v := nodeToValue(content)
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("frontmatter is not a mapping")
	}
	return m, nil
}

func nodeToValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = nodeToValue(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			s = append(s, nodeToValue(c))
		}
		return s
	case yaml.AliasNode:
		if n.Alias != nil {
			return nodeToValue(n.Alias)
		}
		return nil
	case yaml.ScalarNode:
		if n.Tag == "!!timestamp" {
			return n.Value // keep timestamps as literal text, not time.Time
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		return v
	default:
		return nil
	}
}

// ParsePermissive parses raw bytes and never fails: a malformed frontmatter
// block falls back to treating the whole content as the body. Use it on the
// read/list path, where agent- or human-authored files may be malformed.
func ParsePermissive(raw []byte) Document {
	doc, err := Parse(raw)
	if err != nil {
		return Document{Body: string(raw)}
	}
	return doc
}

// Bytes serializes the document back to disk form. Frontmatter keys are emitted
// in deterministic (sorted) order. A document with no frontmatter serializes to
// its body alone.
func (d Document) Bytes() []byte {
	// A parsed document re-emits its frontmatter byte-for-byte.
	if d.rawFrontmatter != "" {
		return []byte(frontmatterFence + "\n" + d.rawFrontmatter + frontmatterFence + "\n" + d.Body)
	}
	if len(d.Frontmatter) == 0 {
		return []byte(d.Body)
	}
	var buf bytes.Buffer
	buf.WriteString(frontmatterFence + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.Frontmatter); err != nil {
		// map[string]any from Parse always marshals; this is unreachable in
		// practice, but degrade to body-only rather than panic.
		return []byte(d.Body)
	}
	_ = enc.Close()
	buf.WriteString(frontmatterFence + "\n")
	buf.WriteString(d.Body)
	return buf.Bytes()
}

func (d Document) frontmatterString(key string) string {
	if d.Frontmatter == nil {
		return ""
	}
	v, _ := d.Frontmatter[key].(string)
	return v
}

// Kind returns the declared kind ("" if absent or non-string).
func (d Document) Kind() string { return d.frontmatterString("kind") }

// Title returns the declared title ("" if absent).
func (d Document) Title() string { return d.frontmatterString("title") }

// Summary returns the declared summary ("" if absent).
func (d Document) Summary() string { return d.frontmatterString("summary") }

// Updated returns the declared update timestamp ("" if absent).
func (d Document) Updated() string { return d.frontmatterString("updated") }

// splitFrontmatter returns the YAML text between the leading fences and the body
// after the closing fence. ok is false when the content does not open with a
// "---" line or has no matching closing fence (both treated as no frontmatter).
// Body bytes after the closing fence's newline are returned verbatim.
func splitFrontmatter(s string) (fm, body string, ok bool) {
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return "", s, false // single line; can't carry a fenced block
	}
	if strings.TrimRight(s[:nl], "\r") != frontmatterFence {
		return "", s, false
	}
	afterOpen := nl + 1
	for off := afterOpen; off <= len(s); {
		var line string
		var next int
		if end := strings.IndexByte(s[off:], '\n'); end < 0 {
			line, next = s[off:], len(s)
		} else {
			line, next = s[off:off+end], off+end+1
		}
		if strings.TrimRight(line, "\r") == frontmatterFence {
			return s[afterOpen:off], s[next:], true
		}
		if next == len(s) {
			break // reached EOF without a closing fence
		}
		off = next
	}
	return "", s, false
}
