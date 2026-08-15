package agent

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

func assertAttnSkillTree(t *testing.T, skillDir string) {
	t.Helper()

	expected := map[string]bool{}
	err := fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "attn_skill" {
			return nil
		}

		relative, err := filepath.Rel("attn_skill", path)
		if err != nil {
			return err
		}
		relative = filepath.FromSlash(relative)
		expected[relative] = true

		installedPath := filepath.Join(skillDir, relative)
		if entry.IsDir() {
			info, err := os.Stat(installedPath)
			if err != nil {
				t.Fatalf("stat installed skill directory %s: %v", installedPath, err)
			}
			if !info.IsDir() {
				t.Fatalf("installed skill path %s is not a directory", installedPath)
			}
			return nil
		}

		want, err := attnSkillFiles.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatalf("read installed skill file %s: %v", installedPath, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("installed skill file %s differs from bundled source", installedPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundled attn skill: %v", err)
	}

	err = filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == skillDir {
			return nil
		}
		relative, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		if !expected[relative] {
			t.Fatalf("installed skill contains unexpected path %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk installed attn skill: %v", err)
	}
}

func TestEnsureAttnClaudeSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnClaudeSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".claude", "skills", "attn"))
}

// TestEnsureAttnClaudeSkillInstalledPrunesOrphanedFiles guards against the
// actual mechanism behind a reported incident: a reference retired from the
// skill source (chief-of-staff.md) survived indefinitely on an installed
// machine because the installer only ever wrote/overwrote known files and
// never deleted files that fell out of the bundle. A stale reference can
// directly contradict the current skill's guidance, so install must prune.
func TestEnsureAttnClaudeSkillInstalledPrunesOrphanedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	skillDir := filepath.Join(home, ".claude", "skills", "attn")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	orphanFile := filepath.Join(skillDir, "references", "chief-of-staff.md")
	if err := os.WriteFile(orphanFile, []byte("stale, retired guidance"), 0o644); err != nil {
		t.Fatalf("seed orphaned reference: %v", err)
	}
	orphanDir := filepath.Join(skillDir, "references", "retired-subdir")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("seed orphaned directory: %v", err)
	}

	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnClaudeSkillInstalled() error = %v", err)
	}

	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference file was not pruned: stat err = %v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference directory was not pruned: stat err = %v", err)
	}
	assertAttnSkillTree(t, skillDir)
}

func TestEnsureAttnAgentsSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnAgentsSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnAgentsSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".agents", "skills", "attn"))
}

func TestEnsureAttnCopilotSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnCopilotSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnCopilotSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".copilot", "skills", "attn"))
}

// TestEnsureAttnCopilotSkillInstalledPrunesOrphanedFiles mirrors the Claude
// orphan-pruning guard: a stale reference retired from the bundle must not
// survive on disk and keep teaching outdated guidance (e.g. a leftover
// chief-of-staff.md telling a delegated leaf it can re-delegate).
func TestEnsureAttnCopilotSkillInstalledPrunesOrphanedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	skillDir := filepath.Join(home, ".copilot", "skills", "attn")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	orphanFile := filepath.Join(skillDir, "references", "chief-of-staff.md")
	if err := os.WriteFile(orphanFile, []byte("stale, retired guidance"), 0o644); err != nil {
		t.Fatalf("seed orphaned reference: %v", err)
	}

	if err := ensureAttnCopilotSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnCopilotSkillInstalled() error = %v", err)
	}

	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference file was not pruned: stat err = %v", err)
	}
	assertAttnSkillTree(t, skillDir)
}

func TestUserGlobalSkillSyncIsSkippedOutsideDefaultAndDevProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "fixture-lab")

	for name, ensure := range map[string]func() (bool, error){
		"claude":  EnsureClaudeSkillInstalled,
		"agents":  EnsureAgentsSkillInstalled,
		"copilot": EnsureCopilotSkillInstalled,
	} {
		t.Run(name, func(t *testing.T) {
			synced, err := ensure()
			if err != nil {
				t.Fatalf("ensure skill: %v", err)
			}
			if synced {
				t.Fatal("a verification profile synchronized a user-global skill")
			}
		})
	}

	for _, root := range []string{".claude", ".agents", ".copilot"} {
		if _, err := os.Stat(filepath.Join(home, root)); !os.IsNotExist(err) {
			t.Fatalf("verification profile wrote %s: stat err = %v", root, err)
		}
	}
}

func TestUserGlobalSkillSyncRunsForDevProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "dev")

	for name, test := range map[string]struct {
		ensure   func() (bool, error)
		skillDir string
	}{
		"claude":  {EnsureClaudeSkillInstalled, filepath.Join(home, ".claude", "skills", "attn")},
		"agents":  {EnsureAgentsSkillInstalled, filepath.Join(home, ".agents", "skills", "attn")},
		"copilot": {EnsureCopilotSkillInstalled, filepath.Join(home, ".copilot", "skills", "attn")},
	} {
		t.Run(name, func(t *testing.T) {
			synced, err := test.ensure()
			if err != nil {
				t.Fatalf("ensure skill: %v", err)
			}
			if !synced {
				t.Fatal("dev profile skipped user-global skill synchronization")
			}
			assertAttnSkillTree(t, test.skillDir)
		})
	}
}
