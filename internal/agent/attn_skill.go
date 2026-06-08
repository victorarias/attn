package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed attn_skill
var attnSkillFiles embed.FS

func installAttnSkill(skillDir string) error {
	return fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, "attn_skill")
		relative = strings.TrimPrefix(relative, "/")
		target := filepath.Join(skillDir, filepath.FromSlash(relative))
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
}

func ensureAttnClaudeSkillInstalled() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Claude skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".claude", "skills", "attn"))
}

func ensureAttnCodexSkillInstalled() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory for agent skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".agents", "skills", "attn"))
}

func EnsureClaudeSkillInstalled() error {
	return ensureAttnClaudeSkillInstalled()
}

func EnsureCodexSkillInstalled() error {
	return ensureAttnCodexSkillInstalled()
}
