//go:build darwin

package pathutil

import (
	"os"
	"strings"
	"testing"
)

func TestExtractPathFromShellOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard path_helper output",
			output: `PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"; export PATH;`,
			want:   "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		},
		{
			name:   "path with homebrew",
			output: `PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"; export PATH;`,
			want:   "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "malformed - no PATH",
			output: "something else",
			want:   "",
		},
		{
			name:   "malformed - no closing quote",
			output: `PATH="/usr/bin`,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathFromShellOutput(tt.output)
			if got != tt.want {
				t.Errorf("extractPathFromShellOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestEnsureGUIPath(t *testing.T) {
	// Save and restore PATH for test isolation
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)

	err := EnsureGUIPath()
	if err != nil {
		t.Errorf("EnsureGUIPath() returned error: %v", err)
	}

	// Verify PATH was not cleared
	newPath := os.Getenv("PATH")
	if newPath == "" {
		t.Error("EnsureGUIPath() cleared PATH")
	}

	// Verify each original path entry is preserved
	newPathSet := make(map[string]bool)
	for _, p := range strings.Split(newPath, ":") {
		newPathSet[p] = true
	}
	for _, p := range strings.Split(origPath, ":") {
		if p != "" && !newPathSet[p] {
			t.Errorf("EnsureGUIPath() lost original path entry: %s", p)
		}
	}
}
