package tour

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/config"
	attngit "github.com/victorarias/attn/internal/git"
	"gopkg.in/yaml.v3"
)

const GuideVersion = 1

type Guide struct {
	Version int         `yaml:"version"`
	Summary string      `yaml:"summary"`
	Files   []GuideFile `yaml:"files"`
	Skip    []string    `yaml:"skip"`
}

type GuideFile struct {
	Path        string            `yaml:"path"`
	View        string            `yaml:"view"`
	Note        string            `yaml:"note"`
	Annotations []GuideAnnotation `yaml:"annotations"`
}

type GuideAnnotation struct {
	Anchor string        `yaml:"anchor"`
	Line   int           `yaml:"line"`
	Start  int           `yaml:"start"`
	End    int           `yaml:"end"`
	Note   string        `yaml:"note"`
	Thread []ThreadEntry `yaml:"thread"`
}

type ThreadEntry struct {
	Author string
	Body   string
}

func (e *ThreadEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		e.Author = "agent"
		e.Body = strings.TrimSpace(node.Value)
	case yaml.MappingNode:
		var value struct {
			Author string `yaml:"author"`
			Body   string `yaml:"body"`
		}
		if err := node.Decode(&value); err != nil {
			return err
		}
		e.Author = strings.TrimSpace(value.Author)
		if e.Author == "" {
			e.Author = "agent"
		}
		e.Body = strings.TrimSpace(value.Body)
	default:
		return fmt.Errorf("thread entry must be a string or {author, body}")
	}
	if e.Body == "" {
		return fmt.Errorf("thread entry body is empty")
	}
	return nil
}

type Comment struct {
	Author string
	Body   string
}

type Annotation struct {
	ID        string
	LineStart int
	LineEnd   int
	Comments  []Comment
}

type File struct {
	Path        string
	OldPath     string
	Status      string
	Additions   int
	Deletions   int
	Group       string
	View        string
	Note        string
	Original    string
	Modified    string
	Annotations []Annotation
}

type Snapshot struct {
	Summary  string
	Files    []File
	Warnings []string
}

type FileContentLoader func(path, oldPath string) (original string, modified string, err error)

func Load(path string) (*Guide, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var guide Guide
	if err := yaml.Unmarshal(data, &guide); err != nil {
		return nil, fmt.Errorf("parse guide: %w", err)
	}
	if guide.Version == 0 {
		guide.Version = GuideVersion
	}
	for i := range guide.Files {
		guide.Files[i].Path = cleanGuidePath(guide.Files[i].Path)
		guide.Files[i].View = strings.TrimSpace(guide.Files[i].View)
		if guide.Files[i].View == "" {
			guide.Files[i].View = "diff"
		}
		guide.Files[i].Note = strings.TrimSpace(guide.Files[i].Note)
	}
	for i := range guide.Skip {
		guide.Skip[i] = cleanGuidePath(guide.Skip[i])
	}
	guide.Summary = strings.TrimSpace(guide.Summary)
	return &guide, nil
}

func BuildSnapshot(guide *Guide, changed []attngit.DiffFileInfo, load FileContentLoader) (*Snapshot, error) {
	if guide == nil {
		return nil, fmt.Errorf("guide is required")
	}
	if guide.Version != GuideVersion {
		return nil, fmt.Errorf("unsupported guide version %d", guide.Version)
	}

	changedByPath := make(map[string]attngit.DiffFileInfo, len(changed))
	for _, file := range changed {
		changedByPath[file.Path] = file
	}

	var errs []string
	var warnings []string
	if err := validateMermaid(guide.Summary); err != nil {
		errs = append(errs, "summary: "+err.Error())
	}

	seen := make(map[string]string)
	files := make([]File, 0, len(changed))
	for _, entry := range guide.Files {
		if entry.Path == "" {
			errs = append(errs, "tour file path is empty")
			continue
		}
		if previous, exists := seen[entry.Path]; exists {
			errs = append(errs, fmt.Sprintf("%q appears in both %s and tour files", entry.Path, previous))
			continue
		}
		seen[entry.Path] = "tour files"
		info, ok := changedByPath[entry.Path]
		if !ok {
			errs = append(errs, fmt.Sprintf("%q is not in the current changeset", entry.Path))
			continue
		}
		if entry.View != "diff" && entry.View != "content" {
			errs = append(errs, fmt.Sprintf("%q view must be diff or content", entry.Path))
			continue
		}
		if err := validateMermaid(entry.Note); err != nil {
			errs = append(errs, fmt.Sprintf("%s note: %v", entry.Path, err))
		}

		original, modified, err := load(entry.Path, info.OldPath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", entry.Path, err))
			continue
		}
		annotations, annotationWarnings, annotationErrs := resolveAnnotations(entry, modified)
		warnings = append(warnings, annotationWarnings...)
		errs = append(errs, annotationErrs...)
		files = append(files, fileFromInfo(info, "tour", entry.View, entry.Note, original, modified, annotations))
	}

	for _, path := range guide.Skip {
		if path == "" {
			errs = append(errs, "skip path is empty")
			continue
		}
		if previous, exists := seen[path]; exists {
			errs = append(errs, fmt.Sprintf("%q appears in both %s and skip", path, previous))
			continue
		}
		seen[path] = "skip"
		info, ok := changedByPath[path]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skip path %q is not in the current changeset", path))
			continue
		}
		original, modified, err := load(path, info.OldPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
		}
		files = append(files, fileFromInfo(info, "skip", "diff", "", original, modified, nil))
	}

	otherPaths := make([]string, 0, len(changed))
	for path := range changedByPath {
		if _, exists := seen[path]; !exists {
			otherPaths = append(otherPaths, path)
		}
	}
	sort.Strings(otherPaths)
	for _, path := range otherPaths {
		info := changedByPath[path]
		original, modified, err := load(path, info.OldPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
		}
		files = append(files, fileFromInfo(info, "other", "diff", "", original, modified, nil))
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return &Snapshot{Summary: guide.Summary, Files: files, Warnings: warnings}, nil
}

func CreateGuidePath(repoPath, sessionID, name string) (string, error) {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	name = sanitizeName(name)
	if name == "" {
		name = "tour"
	}
	repoKey := sanitizeName(filepath.Base(repoPath)) + "-" + shortHash(repoPath)
	sessionKey := sanitizeName(sessionID)
	if sessionKey == "" {
		sessionKey = "session"
	}
	dir := filepath.Join(config.DataDir(), "tours", repoKey, sessionKey, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create tour directory: %w", err)
	}
	path := filepath.Join(dir, "guide.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		skeleton := []byte("version: 1\n\nsummary: >\n  Explain the change and how to read it.\n\nfiles: []\n\nskip: []\n")
		if err := os.WriteFile(path, skeleton, 0o600); err != nil {
			return "", fmt.Errorf("create guide: %w", err)
		}
	} else if err != nil {
		return "", err
	}
	return path, nil
}

func IsSystemGuidePath(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root := filepath.Join(config.DataDir(), "tours")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	resolvedPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveAnnotations(entry GuideFile, content string) ([]Annotation, []string, []string) {
	lines := strings.Split(content, "\n")
	var out []Annotation
	var warnings []string
	var errs []string
	for index, source := range entry.Annotations {
		hasAnchor := strings.TrimSpace(source.Anchor) != ""
		hasLine := source.Line != 0
		hasRange := source.Start != 0 || source.End != 0
		count := 0
		if hasAnchor {
			count++
		}
		if hasLine {
			count++
		}
		if hasRange {
			count++
		}
		if count != 1 {
			errs = append(errs, fmt.Sprintf("%s annotation %d must have exactly one of anchor, line, or start+end", entry.Path, index+1))
			continue
		}

		start, end := source.Line, source.Line
		locator := fmt.Sprintf("line:%d", source.Line)
		if hasAnchor {
			locator = "anchor:" + source.Anchor
			var matches []int
			for lineIndex, line := range lines {
				if strings.Contains(line, source.Anchor) {
					matches = append(matches, lineIndex+1)
				}
			}
			if len(matches) == 0 {
				errs = append(errs, fmt.Sprintf("%s annotation anchor %q does not resolve", entry.Path, source.Anchor))
				continue
			}
			if len(matches) > 1 {
				warnings = append(warnings, fmt.Sprintf("%s anchor %q is ambiguous; first match wins", entry.Path, source.Anchor))
			}
			start, end = matches[0], matches[0]
		} else if hasRange {
			start, end = source.Start, source.End
			locator = fmt.Sprintf("range:%d:%d", start, end)
		}
		if start < 1 || end < start || end > len(lines) {
			errs = append(errs, fmt.Sprintf("%s annotation range %d..%d is outside the file", entry.Path, start, end))
			continue
		}
		comments, err := annotationComments(source)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s annotation %d: %v", entry.Path, index+1, err))
			continue
		}
		for _, comment := range comments {
			if err := validateMermaid(comment.Body); err != nil {
				errs = append(errs, fmt.Sprintf("%s annotation %d: %v", entry.Path, index+1, err))
			}
		}
		out = append(out, Annotation{
			ID:        stableAnnotationID(entry.Path, locator),
			LineStart: start,
			LineEnd:   end,
			Comments:  comments,
		})
	}
	return out, warnings, errs
}

func annotationComments(source GuideAnnotation) ([]Comment, error) {
	hasNote := strings.TrimSpace(source.Note) != ""
	hasThread := len(source.Thread) > 0
	if hasNote == hasThread {
		return nil, fmt.Errorf("must have exactly one of note or thread")
	}
	if hasNote {
		return []Comment{{Author: "agent", Body: strings.TrimSpace(source.Note)}}, nil
	}
	comments := make([]Comment, len(source.Thread))
	for i, entry := range source.Thread {
		comments[i] = Comment{Author: entry.Author, Body: entry.Body}
	}
	return comments, nil
}

func fileFromInfo(info attngit.DiffFileInfo, group, view, note, original, modified string, annotations []Annotation) File {
	return File{
		Path:        info.Path,
		OldPath:     info.OldPath,
		Status:      info.Status,
		Additions:   info.Additions,
		Deletions:   info.Deletions,
		Group:       group,
		View:        view,
		Note:        note,
		Original:    original,
		Modified:    modified,
		Annotations: annotations,
	}
}

func validateMermaid(markdown string) error {
	const fence = "```mermaid"
	rest := markdown
	for {
		start := strings.Index(rest, fence)
		if start < 0 {
			return nil
		}
		rest = rest[start+len(fence):]
		end := strings.Index(rest, "```")
		if end < 0 {
			return fmt.Errorf("unclosed mermaid fence")
		}
		body := strings.TrimSpace(rest[:end])
		first := strings.Fields(body)
		if len(first) == 0 {
			return fmt.Errorf("empty mermaid block")
		}
		switch first[0] {
		case "flowchart", "graph", "stateDiagram-v2", "sequenceDiagram", "classDiagram":
		default:
			return fmt.Errorf("unsupported mermaid diagram type %q", first[0])
		}
		rest = rest[end+3:]
	}
}

var invalidName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeName(value string) string {
	value = invalidName.ReplaceAllString(strings.TrimSpace(value), "-")
	return strings.Trim(value, "-.")
}

func cleanGuidePath(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if value == "." || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return ""
	}
	return value
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}

func stableAnnotationID(path, locator string) string {
	return "ann-" + shortHash(path+"\x00"+locator)
}
