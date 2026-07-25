package hooks

import (
	"encoding/json"
	"slices"
	"testing"
)

// The payloads below are trimmed from real PostToolUse hook output: Claude Code
// 2.x and codex-cli 0.145.0, captured by pointing each agent's catch-all
// PostToolUse hook at a file and asking it to edit one document and create
// another.
func TestMarkdownEdits(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		toolInput string
		cwd       string
		want      []string
	}{
		{
			name:      "claude edit",
			toolName:  "Edit",
			toolInput: `{"file_path":"/repo/notes.md","old_string":"old","new_string":"new"}`,
			want:      []string{"/repo/notes.md"},
		},
		{
			name:      "claude write",
			toolName:  "Write",
			toolInput: `{"file_path":"/repo/extra.md","content":"# Extra\n"}`,
			want:      []string{"/repo/extra.md"},
		},
		{
			name:      "claude notebook edit reports notebook_path",
			toolName:  "NotebookEdit",
			toolInput: `{"notebook_path":"/repo/analysis.md","new_source":"x"}`,
			want:      []string{"/repo/analysis.md"},
		},
		{
			name:      "codex apply_patch touches several files at once",
			toolName:  "apply_patch",
			toolInput: `{"command":"*** Begin Patch\n*** Update File: /repo/notes.md\n@@\n-old line\n+new line\n*** Add File: /repo/extra.md\n+# Extra\n*** End Patch"}`,
			want:      []string{"/repo/notes.md", "/repo/extra.md"},
		},
		{
			name:     "codex rename is recorded at its destination",
			toolName: "apply_patch",
			// The source path no longer exists once the patch applies, so
			// remembering it would hand the opener a dead entry.
			toolInput: `{"command":"*** Begin Patch\n*** Update File: /repo/old.md\n*** Move to: /repo/new.md\n@@\n-a\n+b\n*** End Patch"}`,
			want:      []string{"/repo/new.md"},
		},
		{
			name:      "codex deletion is not an edit worth remembering",
			toolName:  "apply_patch",
			toolInput: `{"command":"*** Begin Patch\n*** Delete File: /repo/gone.md\n*** End Patch"}`,
			want:      nil,
		},
		{
			name:      "relative paths resolve against the agent's cwd",
			toolName:  "Write",
			toolInput: `{"file_path":"docs/plan.md"}`,
			cwd:       "/repo",
			want:      []string{"/repo/docs/plan.md"},
		},
		{
			name:      "a relative path with no cwd has nothing to resolve against",
			toolName:  "Write",
			toolInput: `{"file_path":"docs/plan.md"}`,
			want:      nil,
		},
		{
			name:      "source files are not markdown the opener can show",
			toolName:  "Edit",
			toolInput: `{"file_path":"/repo/main.go"}`,
			want:      nil,
		},
		{
			name:     "a shell rewrite is not attributable",
			toolName: "Bash",
			// Both agents report shell calls with the command only. Editing a
			// file this way is real but unattributable; accepted coverage gap.
			toolInput: `{"command":"sed -i '' s/a/b/ notes.md"}`,
			want:      nil,
		},
		{
			name:      "a read is not an edit",
			toolName:  "Read",
			toolInput: `{"file_path":"/repo/notes.md"}`,
			want:      nil,
		},
		{
			name:      "the same file named twice is recorded once",
			toolName:  "apply_patch",
			toolInput: `{"command":"*** Update File: /repo/notes.md\n*** Update File: /repo/notes.md"}`,
			want:      []string{"/repo/notes.md"},
		},
		{
			name:      "a malformed payload is ignored rather than fatal",
			toolName:  "Edit",
			toolInput: `"not an object"`,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownEdits(tt.toolName, json.RawMessage(tt.toolInput), tt.cwd)
			if !slices.Equal(got, tt.want) {
				t.Errorf("MarkdownEdits() = %v, want %v", got, tt.want)
			}
		})
	}
}
