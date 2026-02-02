// Package pathutil provides PATH environment utilities for GUI app launches.
// On macOS, GUI apps often start with a minimal PATH that doesn't include
// common locations like /opt/homebrew/bin. This package ensures external
// tools like 'gh' can be found.
package pathutil

import "strings"

// mergePaths combines two PATH strings, preserving order and removing duplicates.
// Primary paths come first, then secondary paths that aren't already present.
func mergePaths(primary, secondary string) string {
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
