package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBrowseInputTreatsTrailingSlashAsBrowseIntoDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	directory, prefix, homePath, err := parseBrowseInput("~/projects/hurdy-gurdy/")
	if err != nil {
		t.Fatalf("parseBrowseInput() error = %v", err)
	}

	wantDirectory := filepath.Join(home, "projects", "hurdy-gurdy")
	if directory != wantDirectory {
		t.Fatalf("directory = %q, want %q", directory, wantDirectory)
	}
	if prefix != "" {
		t.Fatalf("prefix = %q, want empty", prefix)
	}
	if homePath != home {
		t.Fatalf("homePath = %q, want %q", homePath, home)
	}
}

func TestParseBrowseInputUsesParentDirectoryForPartialChildMatch(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	directory, prefix, homePath, err := parseBrowseInput("~/projects/hurdy")
	if err != nil {
		t.Fatalf("parseBrowseInput() error = %v", err)
	}

	wantDirectory := filepath.Join(home, "projects")
	if directory != wantDirectory {
		t.Fatalf("directory = %q, want %q", directory, wantDirectory)
	}
	if prefix != "hurdy" {
		t.Fatalf("prefix = %q, want %q", prefix, "hurdy")
	}
	if homePath != home {
		t.Fatalf("homePath = %q, want %q", homePath, home)
	}
}
