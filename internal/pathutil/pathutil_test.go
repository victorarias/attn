package pathutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergePaths(t *testing.T) {
	tests := []struct {
		name      string
		primary   string
		secondary string
		want      string
	}{
		{
			name:      "empty paths",
			primary:   "",
			secondary: "",
			want:      "",
		},
		{
			name:      "primary only",
			primary:   "/usr/bin:/bin",
			secondary: "",
			want:      "/usr/bin:/bin",
		},
		{
			name:      "secondary only",
			primary:   "",
			secondary: "/usr/bin:/bin",
			want:      "/usr/bin:/bin",
		},
		{
			name:      "no duplicates",
			primary:   "/usr/bin:/bin",
			secondary: "/usr/local/bin:/opt/bin",
			want:      "/usr/bin:/bin:/usr/local/bin:/opt/bin",
		},
		{
			name:      "with duplicates",
			primary:   "/usr/bin:/bin:/usr/local/bin",
			secondary: "/usr/local/bin:/opt/bin:/bin",
			want:      "/usr/bin:/bin:/usr/local/bin:/opt/bin",
		},
		{
			name:      "empty segments ignored",
			primary:   "/usr/bin::/bin",
			secondary: ":/opt/bin:",
			want:      "/usr/bin:/bin:/opt/bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergePaths(tt.primary, tt.secondary)
			if got != tt.want {
				t.Errorf("MergePaths(%q, %q) = %q, want %q", tt.primary, tt.secondary, got, tt.want)
			}
		})
	}
}

func TestCommonPaths(t *testing.T) {
	paths := CommonPaths()

	// Should include Homebrew paths
	hasHomebrew := false
	hasUsrLocal := false
	hasLocalBin := false

	for _, p := range paths {
		if p == "/opt/homebrew/bin" {
			hasHomebrew = true
		}
		if p == "/usr/local/bin" {
			hasUsrLocal = true
		}
		if strings.HasSuffix(p, ".local/bin") {
			hasLocalBin = true
		}
	}

	if !hasHomebrew {
		t.Error("CommonPaths should include /opt/homebrew/bin")
	}
	if !hasUsrLocal {
		t.Error("CommonPaths should include /usr/local/bin")
	}
	if !hasLocalBin {
		t.Error("CommonPaths should include ~/.local/bin")
	}
}

func TestAddExistingPaths(t *testing.T) {
	// Create a temp directory to use as an "existing" path
	tmpDir := t.TempDir()
	existingPath := filepath.Join(tmpDir, "existing")
	if err := os.MkdirAll(existingPath, 0755); err != nil {
		t.Fatal(err)
	}

	nonExistingPath := filepath.Join(tmpDir, "nonexisting")

	current := "/usr/bin"
	result := AddExistingPaths(current, []string{existingPath, nonExistingPath})

	// Should include the existing path
	if !strings.Contains(result, existingPath) {
		t.Errorf("AddExistingPaths should include existing path %s, got %s", existingPath, result)
	}

	// Should not include the non-existing path
	if strings.Contains(result, nonExistingPath) {
		t.Errorf("AddExistingPaths should not include non-existing path %s, got %s", nonExistingPath, result)
	}

	// Should preserve original path
	if !strings.HasPrefix(result, "/usr/bin") {
		t.Errorf("AddExistingPaths should preserve original path, got %s", result)
	}
}
