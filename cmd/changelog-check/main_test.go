package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFragment(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "valid minimal",
			yaml: "kind: fixed\narea: queue\nchange: handover no longer skips settled turns\n",
		},
		{
			name: "valid with optional fields",
			yaml: "kind: fixed\narea: queue\nchange: handover fixed\nsymptom: pressing enter did nothing\nnotes: root cause was a stale band read\n",
		},
		{
			name:    "unknown kind",
			yaml:    "kind: bugfix\narea: queue\nchange: something\n",
			wantErr: "kind must be one of",
		},
		{
			name:    "missing change",
			yaml:    "kind: added\narea: queue\n",
			wantErr: "change is required",
		},
		{
			name:    "missing area",
			yaml:    "kind: added\nchange: something\n",
			wantErr: "area is required",
		},
		{
			name:    "unknown key rejected",
			yaml:    "kind: added\narea: queue\nchange: something\ndescription: typo field\n",
			wantErr: "field description not found",
		},
		{
			name:    "empty file",
			yaml:    "",
			wantErr: "fragment is empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFragment([]byte(tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# ignored\n")
	write("good.yaml", "kind: added\narea: terminal\nchange: something new\n")

	if errs := validateDir(dir); len(errs) != 0 {
		t.Fatalf("expected clean dir, got: %v", errs)
	}

	write("bad-extension.yml", "kind: added\narea: x\nchange: y\n")
	write("bad-schema.yaml", "kind: nope\narea: x\nchange: y\n")

	errs := validateDir(dir)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
}

// TestRepoFragments validates the real changelog.d in this checkout, so a bad
// fragment fails `make test` on the branch that introduced it, not just the CI
// gate job.
func TestRepoFragments(t *testing.T) {
	repo := filepath.Join("..", "..", "changelog.d")
	if _, err := os.Stat(repo); err != nil {
		t.Skipf("no changelog.d at %s", repo)
	}
	if errs := validateDir(repo); len(errs) != 0 {
		t.Fatalf("repo changelog.d has invalid fragments: %v", errs)
	}
}
