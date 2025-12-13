package daemon

import (
	"testing"
)

func TestParseGitStatusPorcelain(t *testing.T) {
	// Porcelain v1 format: XY PATH or XY ORIG -> PATH for renames
	input := " M src/App.tsx\x00A  src/new.ts\x00?? untracked.txt\x00"

	staged, unstaged, untracked := parseGitStatusPorcelain(input)

	if len(unstaged) != 1 || unstaged[0].Path != "src/App.tsx" {
		t.Errorf("Expected 1 unstaged file, got %v", unstaged)
	}
	if len(staged) != 1 || staged[0].Path != "src/new.ts" {
		t.Errorf("Expected 1 staged file, got %v", staged)
	}
	if len(untracked) != 1 || untracked[0].Path != "untracked.txt" {
		t.Errorf("Expected 1 untracked file, got %v", untracked)
	}
}

func TestParseGitDiffNumstat(t *testing.T) {
	input := "42\t12\tsrc/App.tsx\n8\t3\tsrc/hook.ts\n"

	stats := parseGitDiffNumstat(input)

	if stats["src/App.tsx"].Additions != 42 || stats["src/App.tsx"].Deletions != 12 {
		t.Errorf("Expected 42/12 for App.tsx, got %v", stats["src/App.tsx"])
	}
}
