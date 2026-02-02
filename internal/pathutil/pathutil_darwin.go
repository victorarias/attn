//go:build darwin

package pathutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureGUIPath ensures the PATH includes common tool locations.
// On macOS, GUI apps start with a minimal PATH. This function:
// 1. Runs /usr/libexec/path_helper to get system-configured paths
// 2. Adds common Homebrew and user paths that may be missing
// 3. Updates the PATH environment variable
//
// This should be called early in daemon startup before spawning
// subprocesses that need tools like 'gh'.
func EnsureGUIPath() error {
	currentPath := os.Getenv("PATH")

	// Try path_helper first - it reads /etc/paths and /etc/paths.d/*
	if helperPath := runPathHelper(); helperPath != "" {
		currentPath = mergePaths(currentPath, helperPath)
	}

	// Add common paths that exist on disk
	commonPaths := []string{
		"/opt/homebrew/bin", // Homebrew on Apple Silicon
		"/opt/homebrew/sbin",
		"/usr/local/bin", // Homebrew on Intel Mac
		"/usr/local/sbin",
	}
	if home, err := os.UserHomeDir(); err == nil {
		commonPaths = append(commonPaths, filepath.Join(home, ".local", "bin"))
	}

	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			currentPath = mergePaths(currentPath, p)
		}
	}

	return os.Setenv("PATH", currentPath)
}

func runPathHelper() string {
	cmd := exec.Command("/usr/libexec/path_helper", "-s")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return extractPathFromShellOutput(string(output))
}

// extractPathFromShellOutput parses the output of `path_helper -s`
// which outputs: PATH="..."; export PATH;
func extractPathFromShellOutput(output string) string {
	const prefix = "PATH=\""
	start := strings.Index(output, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(output[start:], "\"")
	if end == -1 {
		return ""
	}
	return output[start : start+end]
}
