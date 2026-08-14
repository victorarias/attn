package hooks

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSentFiles(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		toolInput string
		cwd       string
		want      []string
	}{
		{
			name:      "absolute paths pass through",
			toolName:  "SendUserFile",
			toolInput: `{"files": ["/repo/docs/plan.md", "/repo/report.html"]}`,
			cwd:       "/repo",
			want:      []string{"/repo/docs/plan.md", "/repo/report.html"},
		},
		{
			name:      "relative paths resolve against cwd",
			toolName:  "SendUserFile",
			toolInput: `{"files": ["docs/plan.md"]}`,
			cwd:       "/repo",
			want:      []string{"/repo/docs/plan.md"},
		},
		{
			name:      "relative path without cwd is dropped",
			toolName:  "SendUserFile",
			toolInput: `{"files": ["docs/plan.md"]}`,
			want:      nil,
		},
		{
			name:      "duplicates and blanks collapse",
			toolName:  "SendUserFile",
			toolInput: `{"files": ["/a.md", "  ", "/a.md", "./a.md"]}`,
			cwd:       "/",
			want:      []string{"/a.md"},
		},
		{
			name:      "other tools never match",
			toolName:  "Write",
			toolInput: `{"files": ["/a.md"]}`,
			cwd:       "/repo",
			want:      nil,
		},
		{
			name:      "malformed payload is dropped",
			toolName:  "SendUserFile",
			toolInput: `{"files": "not-an-array"}`,
			cwd:       "/repo",
			want:      nil,
		},
		{
			name:      "empty files array",
			toolName:  "SendUserFile",
			toolInput: `{"files": []}`,
			cwd:       "/repo",
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SentFiles(tt.toolName, json.RawMessage(tt.toolInput), tt.cwd)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SentFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}
