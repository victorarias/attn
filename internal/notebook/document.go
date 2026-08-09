package notebook

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is a parsed Notebook markdown file: optional YAML frontmatter plus
// body. A disk-parsed document re-serializes its frontmatter byte-for-byte, so
// externally-written fields survive an attn rewrite untouched; only an
// attn-constructed document is serialized from the map (sorted keys).
type Document struct {
	// Frontmatter is read-only after Parse: mutating it does not change what
	// Bytes emits — construct a fresh Document to serialize edited frontmatter.
	Frontmatter map[string]any
	// Body is the markdown after the frontmatter block, kept byte-for-byte.
	Body string
	// rawFrontmatter is the exact on-disk YAML; when set, Bytes emits it verbatim.
	rawFrontmatter string
}

const frontmatterFence = "---"

// Parse splits raw bytes into frontmatter and body. A missing or unclosed
// fence means no frontmatter, never an error; only malformed YAML inside a
// well-formed block errors — callers that must not fail use ParsePermissive.
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

// decodeFrontmatter decodes a YAML mapping into map[string]any. Timestamps stay
// literal text, not time.Time, so dates round-trip and string accessors work.
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

// ParsePermissive parses raw bytes and never fails: malformed frontmatter
// falls back to treating the whole content as the body.
func ParsePermissive(raw []byte) Document {
	doc, err := Parse(raw)
	if err != nil {
		return Document{Body: string(raw)}
	}
	return doc
}

// Bytes serializes the document back to disk form (sorted frontmatter keys;
// no frontmatter → body alone).
func (d Document) Bytes() []byte {
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
		return []byte(d.Body) // practically unreachable; degrade rather than panic
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

// Type returns the declared OKF `type` ("" if absent or non-string), falling
// back to the legacy `kind` for read-compat; attn always writes `type`.
func (d Document) Type() string {
	if t := d.frontmatterString("type"); t != "" {
		return t
	}
	return d.frontmatterString("kind")
}

// Title returns the first H1's text — attn never reads a frontmatter `title:`.
// "" when the body has no H1; callers fall back to the filename.
func (d Document) Title() string { return firstH1(d.Body) }

var (
	atxH1Re   = regexp.MustCompile(`^ {0,3}#[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	mdFenceRe = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})")
)

// firstH1 returns the first level-1 ATX heading's text, or "". It skips fenced
// code blocks and follows CommonMark ATX rules (trailing #-sequence stripped).
func firstH1(body string) string {
	var fence byte // 0 outside a code fence; otherwise the marker rune ('`' or '~')
	for line := range strings.SplitSeq(body, "\n") {
		if mdFenceRe.MatchString(line) {
			marker := strings.TrimLeft(line, " \t")[0]
			switch {
			case fence == 0:
				fence = marker
			case marker == fence:
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		if m := atxH1Re.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// Summary returns the declared summary ("" if absent).
func (d Document) Summary() string { return d.frontmatterString("summary") }

// Updated returns the declared update timestamp ("" if absent).
func (d Document) Updated() string { return d.frontmatterString("updated") }

// splitFrontmatter returns the YAML between the fences and the body after the
// closing fence; ok is false when either fence is missing (no frontmatter).
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
