package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/toolhome"
)

//go:embed attn_skill
var attnSkillFiles embed.FS

func installAttnSkill(skillDir string) error {
	expected := map[string]bool{}
	err := fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, "attn_skill")
		relative = strings.TrimPrefix(relative, "/")
		target := filepath.Join(skillDir, filepath.FromSlash(relative))
		expected[target] = true
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create attn skill directory %s: %w", target, err)
			}
			return nil
		}

		content, err := attnSkillFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read bundled attn skill file %s: %w", path, err)
		}
		if current, err := os.ReadFile(target); err == nil && string(current) == string(content) {
			return nil
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read installed attn skill file %s: %w", target, err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("write attn skill file %s: %w", target, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return pruneOrphanedSkillFiles(skillDir, expected)
}

// pruneOrphanedSkillFiles removes files and directories under skillDir that are
// no longer part of the bundled skill — e.g. a reference retired in a later
// version. installAttnSkill only ever writes/overwrites files present in the
// current embed, so without this an installed skill accumulates stale content
// forever: a retired reference stays loadable by name and can directly
// contradict the current skill's guidance (this is how a leftover
// chief-of-staff.md, removed from the source tree, kept teaching a delegated
// leaf that it could re-delegate like the chief).
func pruneOrphanedSkillFiles(skillDir string, expected map[string]bool) error {
	return filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if expected[path] {
			return nil
		}
		if entry.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove orphaned attn skill directory %s: %w", path, err)
			}
			return fs.SkipDir
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove orphaned attn skill file %s: %w", path, err)
		}
		return nil
	})
}

func ensureAttnClaudeSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Claude skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".claude", "skills", "attn"))
}

func ensureAttnCodexSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for agent skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".agents", "skills", "attn"))
}

func ensureAttnCopilotSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Copilot skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".copilot", "skills", "attn"))
}

func EnsureClaudeSkillInstalled() error {
	return ensureAttnClaudeSkillInstalled()
}

func EnsureCodexSkillInstalled() error {
	return ensureAttnCodexSkillInstalled()
}

func EnsureCopilotSkillInstalled() error {
	return ensureAttnCopilotSkillInstalled()
}
