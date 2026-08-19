package agent

import (
	"strings"
	"testing"
)

func TestResolveLaunchModel(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		requested string
		want      string
		wantError []string
	}{
		{name: "claude alias", agent: "claude", requested: "opus", want: "claude-opus-5"},
		{name: "claude canonical", agent: "claude", requested: "claude-opus-5", want: "claude-opus-5"},
		{name: "plugin model", agent: "fixture", requested: "provider/custom-model", want: "provider/custom-model"},
		{
			name: "missing claude prefix", agent: "claude", requested: "opus-5",
			wantError: []string{`did you mean "claude-opus-5"?`, "supported models: claude-fable-5, claude-opus-5"},
		},
		{
			name: "ambiguous codex family", agent: "codex", requested: "gpt-5.6",
			wantError: []string{"did you mean one of", `"gpt-5.6-luna"`, `"gpt-5.6-terra"`, `"gpt-5.6-sol"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveLaunchModel(test.agent, test.requested)
			if len(test.wantError) == 0 {
				if err != nil || got != test.want {
					t.Fatalf("ResolveLaunchModel(%q, %q) = %q, %v; want %q, nil", test.agent, test.requested, got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResolveLaunchModel(%q, %q) unexpectedly succeeded with %q", test.agent, test.requested, got)
			}
			for _, want := range test.wantError {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want substring %q", err, want)
				}
			}
		})
	}
}
