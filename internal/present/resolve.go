package present

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/git"
)

// ResolvedAnnotation is an annotation pinned to concrete head-side lines.
type ResolvedAnnotation struct {
	LineStart int
	LineEnd   int
	Comments  []string // Note becomes a 1-element slice
}

// AnchorIssue reports a problem resolving one annotation.
type AnchorIssue struct {
	Path    string
	Index   int // annotation index within the file entry
	Message string
	Warning bool // false = error
}

// ResolveAnnotations resolves every annotation in m against the head content
// of each file (git show headSHA:path in repoDir). Returns resolved
// annotations grouped by file path, plus all issues found. A file with no
// annotations is never looked up.
func ResolveAnnotations(m *Manifest, repoDir, headSHA string) (map[string][]ResolvedAnnotation, []AnchorIssue) {
	resolved := make(map[string][]ResolvedAnnotation)
	var issues []AnchorIssue

	for _, f := range m.Files {
		if len(f.Annotations) == 0 {
			continue
		}

		lines, err := headFileLines(repoDir, headSHA, f.Path)
		if err != nil {
			issues = append(issues, AnchorIssue{
				Path:    f.Path,
				Index:   -1,
				Message: fmt.Sprintf("could not read head content: %v", err),
			})
			continue
		}

		var fileResolved []ResolvedAnnotation
		for i, a := range f.Annotations {
			r, issue, ok := resolveOne(a, lines)
			if issue != nil {
				issue.Path = f.Path
				issue.Index = i
				issues = append(issues, *issue)
			}
			if !ok {
				continue
			}
			fileResolved = append(fileResolved, r)
		}
		if len(fileResolved) > 0 {
			resolved[f.Path] = fileResolved
		}
	}

	return resolved, issues
}

// resolveOne resolves a single annotation entry against a file's head-side
// lines. It returns the resolved annotation, an optional issue (error or
// warning), and whether resolution succeeded (false means the annotation is
// dropped).
func resolveOne(a AnnotationEntry, lines []string) (ResolvedAnnotation, *AnchorIssue, bool) {
	comments := commentsOf(a)

	switch {
	case a.Anchor != "":
		var matches []int
		for i, line := range lines {
			if strings.Contains(line, a.Anchor) {
				matches = append(matches, i+1)
			}
		}
		if len(matches) == 0 {
			return ResolvedAnnotation{}, &AnchorIssue{
				Message: fmt.Sprintf("anchor %q matches no line", a.Anchor),
			}, false
		}
		var issue *AnchorIssue
		if len(matches) > 1 {
			issue = &AnchorIssue{
				Message: fmt.Sprintf("anchor %q matches multiple lines (pins to line %d, also matches lines %s)", a.Anchor, matches[0], joinInts(matches[1:])),
				Warning: true,
			}
		}
		return ResolvedAnnotation{LineStart: matches[0], LineEnd: matches[0], Comments: comments}, issue, true

	case a.Line != 0:
		if a.Line > len(lines) {
			return ResolvedAnnotation{}, &AnchorIssue{
				Message: fmt.Sprintf("line %d is out of bounds (file has %d lines)", a.Line, len(lines)),
			}, false
		}
		return ResolvedAnnotation{LineStart: a.Line, LineEnd: a.Line, Comments: comments}, nil, true

	default: // a.Start / a.End
		if a.End > len(lines) {
			return ResolvedAnnotation{}, &AnchorIssue{
				Message: fmt.Sprintf("end %d is out of bounds (file has %d lines)", a.End, len(lines)),
			}, false
		}
		return ResolvedAnnotation{LineStart: a.Start, LineEnd: a.End, Comments: comments}, nil, true
	}
}

// commentsOf normalizes an annotation's body into an ordered comment slice —
// a single-element slice for note, or the thread as-is.
func commentsOf(a AnnotationEntry) []string {
	if a.Note != "" {
		return []string{a.Note}
	}
	return a.Thread
}

// headFileLines reads a file's content at headSHA via `git show` and splits
// it into lines. Mirrors pin.go's pattern of shelling out to git directly —
// internal/present stays self-contained and never imports internal/daemon.
func headFileLines(repoDir, headSHA, path string) ([]string, error) {
	out, err := git.Output(git.OpDiff, repoDir, "show", headSHA+":"+path)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSuffix(string(out), "\n")
	if content == "" {
		return nil, nil
	}
	return strings.Split(content, "\n"), nil
}

func joinInts(vals []int) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
