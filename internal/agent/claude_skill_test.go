package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAttnClaudeSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnClaudeSkillInstalled() error = %v", err)
	}

	skillPath := filepath.Join(home, ".claude", "skills", "attn", "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(content)
	if !strings.Contains(text, "name: attn") {
		t.Fatalf("skill content missing name: %q", text)
	}
	if !strings.Contains(text, "attn review-loop start --prompt") {
		t.Fatalf("skill content missing review loop start command: %q", text)
	}
	if !strings.Contains(text, "attn review-loop answer --loop <loop-id> --interaction <interaction-id> --answer") {
		t.Fatalf("skill content missing review loop answer command: %q", text)
	}
	if !strings.Contains(text, "attn review-loop show --loop <loop-id>") {
		t.Fatalf("skill content missing review loop show command: %q", text)
	}
	if !strings.Contains(text, "make sure your current implementation work is committed before starting the loop") {
		t.Fatalf("skill content missing commit-before-review guidance: %q", text)
	}
	if !strings.Contains(text, `use your attn skill to start a review loop`) {
		t.Fatalf("skill content missing natural-language trigger guidance: %q", text)
	}
	if !strings.Contains(text, `"summary": "Brief description of what changed."`) {
		t.Fatalf("skill content missing handoff file guidance: %q", text)
	}
	if !strings.Contains(text, "must not take additional coding action just because the loop produced logs") {
		t.Fatalf("skill content missing loop autonomy guidance: %q", text)
	}
}
