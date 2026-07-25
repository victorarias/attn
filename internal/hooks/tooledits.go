package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// MarkdownEdits returns the absolute markdown files a completed tool call
// wrote, read from the agent's PostToolUse payload. Both supported agents
// report the same envelope (tool_name, tool_input, cwd) but describe an edit
// differently:
//
//   - Claude names the tool (Edit, Write, MultiEdit, NotebookEdit) and carries
//     the target in tool_input.file_path (notebook_path for NotebookEdit).
//   - Codex routes every file write through apply_patch, whose tool_input.command
//     is the patch envelope; the touched paths are its file headers.
//
// Coverage is deliberately partial: a file an agent rewrites through the shell
// (`sed -i`, a heredoc) arrives as an ordinary Bash call with no attributable
// path, and is not recorded. Deletions are skipped — a path that no longer
// exists is not worth a slot in the opener.
func MarkdownEdits(toolName string, toolInput json.RawMessage, cwd string) []string {
	var paths []string
	switch toolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		var input struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		if err := json.Unmarshal(toolInput, &input); err != nil {
			return nil
		}
		paths = []string{input.FilePath, input.NotebookPath}
	case "apply_patch":
		var input struct {
			Command string `json:"command"`
			Input   string `json:"input"`
		}
		if err := json.Unmarshal(toolInput, &input); err != nil {
			return nil
		}
		patch := input.Command
		if strings.TrimSpace(patch) == "" {
			patch = input.Input
		}
		paths = applyPatchTargets(patch)
	default:
		return nil
	}

	var edited []string
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || !isMarkdownPath(path) {
			continue
		}
		if !filepath.IsAbs(path) {
			if cwd == "" {
				continue // nothing to resolve a relative path against
			}
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		edited = append(edited, path)
	}
	return edited
}

// applyPatchTargets reads the file headers out of a Codex apply_patch envelope.
// A rename is reported at its destination ("*** Move to:"), which is the file
// that now exists.
func applyPatchTargets(patch string) []string {
	var targets []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "*** Add File: "), strings.HasPrefix(line, "*** Update File: "):
			targets = append(targets, strings.TrimSpace(line[strings.Index(line, ": ")+2:]))
		case strings.HasPrefix(line, "*** Move to: "):
			// A move follows the "Update File" header for its source, which no
			// longer exists once the patch applies. Replace it.
			if len(targets) > 0 {
				targets = targets[:len(targets)-1]
			}
			targets = append(targets, strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: ")))
		}
	}
	return targets
}

func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}
