// Package pathutil provides PATH environment utilities for GUI app launches.
// On macOS, GUI apps often start with a minimal PATH that doesn't include
// common locations like /opt/homebrew/bin. This package ensures external
// tools like 'gh' can be found.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// CommonPaths returns paths that should be available but are often missing
// from GUI app environments on macOS.
func CommonPaths() []string {
	paths := []string{
		"/opt/homebrew/bin", // Homebrew on Apple Silicon
		"/opt/homebrew/sbin",
		"/usr/local/bin", // Homebrew on Intel Mac, also common for other tools
		"/usr/local/sbin",
	}

	// Add user's local bin if home directory is available
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".local", "bin"))
	}

	return paths
}

// MergePaths combines two PATH strings, preserving order and removing duplicates.
// Primary paths come first, then secondary paths that aren't already present.
func MergePaths(primary, secondary string) string {
	seen := make(map[string]bool)
	var merged []string

	for _, pathList := range []string{primary, secondary} {
		for _, part := range strings.Split(pathList, ":") {
			if part != "" && !seen[part] {
				seen[part] = true
				merged = append(merged, part)
			}
		}
	}
	return strings.Join(merged, ":")
}

// AddExistingPaths adds paths that exist on disk to the current PATH.
// Returns the merged PATH string.
func AddExistingPaths(currentPath string, paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			currentPath = MergePaths(currentPath, p)
		}
	}
	return currentPath
}
