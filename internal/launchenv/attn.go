// Package launchenv builds the small part of a child-process environment that
// must be consistent across every attn launch surface.
package launchenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const wrapperPathEnv = "ATTN_WRAPPER_PATH"

// ActiveAttnExecutable resolves the attn binary that owns this process. An
// explicit wrapper is authoritative because it names the active app/profile;
// the remaining candidates preserve standalone and recovery behavior.
func ActiveAttnExecutable() string {
	candidates := make([]string, 0, 4)
	if wrapperPath := strings.TrimSpace(os.Getenv(wrapperPathEnv)); wrapperPath != "" {
		candidates = append(candidates, wrapperPath)
	}
	if executable, err := os.Executable(); err == nil && executable != "" {
		candidates = append(candidates, executable)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "attn"))
	}
	if path, err := exec.LookPath("attn"); err == nil && path != "" {
		candidates = append(candidates, path)
	}
	if resolved, ok := FirstExecutablePath(candidates); ok {
		return resolved
	}
	return "attn"
}

// WithActiveAttnFirst puts the active attn binary's directory first in PATH and
// deduplicates path entries. It does not make any other environment filtering
// decisions for the caller.
func WithActiveAttnFirst(env []string, executable string) []string {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return append([]string(nil), env...)
	}
	dir := filepath.Dir(executable)
	if dir == "." || dir == "" {
		return append([]string(nil), env...)
	}

	out := make([]string, 0, len(env)+1)
	pathIndex := -1
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && name == "PATH" {
			if pathIndex >= 0 {
				continue
			}
			pathIndex = len(out)
		}
		out = append(out, entry)
	}

	pathValue := ""
	if pathIndex >= 0 {
		pathValue = strings.TrimPrefix(out[pathIndex], "PATH=")
	}
	entries := make([]string, 0, len(filepath.SplitList(pathValue))+1)
	seen := map[string]struct{}{pathEntryKey(dir): {}}
	entries = append(entries, dir)
	for _, entry := range filepath.SplitList(pathValue) {
		key := pathEntryKey(entry)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, entry)
	}
	pathEntry := "PATH=" + strings.Join(entries, string(os.PathListSeparator))
	if pathIndex >= 0 {
		out[pathIndex] = pathEntry
		return out
	}
	return append(out, pathEntry)
}

func pathEntryKey(entry string) string {
	if entry == "" {
		return ""
	}
	return filepath.Clean(entry)
}

// FirstExecutablePath returns the first executable file in candidates.
func FirstExecutablePath(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, true
	}
	return "", false
}
