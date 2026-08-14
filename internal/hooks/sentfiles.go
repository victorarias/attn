package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// SentFiles returns the absolute paths a completed SendUserFile call handed
// the user, read from Claude's PostToolUse payload. SendUserFile is the one
// tool that means "look at this", so its files are worth surfacing; which of
// them attn can actually show is the daemon's call, so no type filtering
// happens here. Codex has no equivalent tool, so its payloads never match.
func SentFiles(toolName string, toolInput json.RawMessage, cwd string) []string {
	if toolName != "SendUserFile" {
		return nil
	}
	var input struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(toolInput, &input); err != nil {
		return nil
	}

	var sent []string
	seen := map[string]bool{}
	for _, path := range input.Files {
		path = strings.TrimSpace(path)
		if path == "" {
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
		sent = append(sent, path)
	}
	return sent
}
