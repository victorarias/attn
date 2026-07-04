package present

import (
	"os"
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string // substring expected in error; empty means no error
	}{
		{
			name: "valid full manifest",
			yaml: `
version: 1
kind: changes
title: Nudge countdown fixes
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
summary: |
  Fixes the countdown.
files:
  - path: internal/daemon/nudge.go
    note: core fix
  - path: internal/daemon/nudge_test.go
skip:
  - app/src/types/generated.ts
`,
		},
		{
			name: "valid minimal manifest",
			yaml: `
version: 1
kind: changes
title: Minimal
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
`,
		},
		{
			name: "unknown top-level key rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
bogus: true
`,
			wantErr: "field bogus not found",
		},
		{
			name: "unknown frame key rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
  bogus: true
`,
			wantErr: "field bogus not found",
		},
		{
			name: "unknown files entry key rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: a.go
    bogus: true
`,
			wantErr: "field bogus not found",
		},
		{
			name: "wrong version rejected",
			yaml: `
version: 2
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
`,
			wantErr: "version must be 1",
		},
		{
			name: "wrong kind rejected",
			yaml: `
version: 1
kind: bogus
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
`,
			wantErr: `kind must be "changes"`,
		},
		{
			name: "empty title rejected",
			yaml: `
version: 1
kind: changes
title: "   "
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
`,
			wantErr: "title is required",
		},
		{
			name: "missing frame.repo rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  base: origin/main
  head: HEAD
`,
			wantErr: "frame.repo is required",
		},
		{
			name: "relative frame.repo rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: relative/path
  base: origin/main
  head: HEAD
`,
			wantErr: "frame.repo must be an absolute path",
		},
		{
			name: "missing frame.base rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  head: HEAD
`,
			wantErr: "frame.base is required",
		},
		{
			name: "missing frame.head rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
`,
			wantErr: "frame.head is required",
		},
		{
			name: "empty files path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: ""
`,
			wantErr: "files[0].path is required",
		},
		{
			name: "absolute files path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: /abs/file.go
`,
			wantErr: "files[0].path must be relative",
		},
		{
			name: "traversal files path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: ../outside.go
`,
			wantErr: "files[0].path must not escape the repo",
		},
		{
			name: "duplicate files path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: a.go
  - path: a.go
`,
			wantErr: "files[1].path is a duplicate",
		},
		{
			name: "empty skip path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
skip:
  - ""
`,
			wantErr: "skip[0] is required",
		},
		{
			name: "absolute skip path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
skip:
  - /abs/file.go
`,
			wantErr: "skip[0] must be relative",
		},
		{
			name: "traversal skip path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
skip:
  - ../outside.go
`,
			wantErr: "skip[0] must not escape the repo",
		},
		{
			name: "duplicate skip path rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
skip:
  - a.go
  - a.go
`,
			wantErr: "skip[1] is a duplicate",
		},
		{
			name: "path in both files and skip rejected",
			yaml: `
version: 1
kind: changes
title: X
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
files:
  - path: a.go
skip:
  - a.go
`,
			wantErr: "also appears in files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tt.yaml))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseManifest() unexpected error: %v", err)
				}
				if m == nil {
					t.Fatal("ParseManifest() returned nil manifest with no error")
				}
				return
			}

			if err == nil {
				t.Fatalf("ParseManifest() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseManifest() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParseManifest_ValidFullManifestFields(t *testing.T) {
	m, err := ParseManifest([]byte(`
version: 1
kind: changes
title: Nudge countdown fixes
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
summary: |
  Fixes the countdown.
files:
  - path: internal/daemon/nudge.go
    note: core fix
  - path: internal/daemon/nudge_test.go
skip:
  - app/src/types/generated.ts
`))
	if err != nil {
		t.Fatalf("ParseManifest() unexpected error: %v", err)
	}

	if m.Title != "Nudge countdown fixes" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Frame != (Frame{Repo: "/abs/worktree", Base: "origin/main", Head: "HEAD"}) {
		t.Errorf("Frame = %+v", m.Frame)
	}
	if len(m.Files) != 2 || m.Files[0].Path != "internal/daemon/nudge.go" || m.Files[0].Note != "core fix" {
		t.Errorf("Files = %+v", m.Files)
	}
	if len(m.Skip) != 1 || m.Skip[0] != "app/src/types/generated.ts" {
		t.Errorf("Skip = %+v", m.Skip)
	}
}

func TestParseManifestFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/manifest.yaml"
	content := []byte(`
version: 1
kind: changes
title: From file
frame:
  repo: /abs/worktree
  base: origin/main
  head: HEAD
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	m, err := ParseManifestFile(path)
	if err != nil {
		t.Fatalf("ParseManifestFile() unexpected error: %v", err)
	}
	if m.Title != "From file" {
		t.Errorf("Title = %q", m.Title)
	}
}

func TestParseManifestFile_MissingFile(t *testing.T) {
	_, err := ParseManifestFile("/nonexistent/manifest.yaml")
	if err == nil {
		t.Fatal("ParseManifestFile() expected error for missing file, got nil")
	}
}
