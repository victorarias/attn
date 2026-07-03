package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestExtractConversationSlice_Claude(t *testing.T) {
	lines := []string{
		`{"type":"session_meta","cwd":"/tmp"}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"fix the reconcile classifier budget bug"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","text":"some tool output"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"looking into it"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"found the root cause"}]}}`,
		`{"type":"user","isCompactSummary":true,"message":{"content":"summary: investigated X, did Y"}}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"also handle codex transcripts"}}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"and copilot too"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"handling codex now"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"done, opening PR"}]}}`,
	}
	path := writeJSONL(t, lines...)

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}

	if got.Brief != "fix the reconcile classifier budget bug" {
		t.Errorf("Brief = %q", got.Brief)
	}
	wantRescoping := []string{"also handle codex transcripts", "and copilot too"}
	if len(got.Rescoping) != len(wantRescoping) {
		t.Fatalf("Rescoping = %v, want %v", got.Rescoping, wantRescoping)
	}
	for i, w := range wantRescoping {
		if got.Rescoping[i] != w {
			t.Errorf("Rescoping[%d] = %q, want %q", i, got.Rescoping[i], w)
		}
	}
	if got.Summary != "summary: investigated X, did Y" {
		t.Errorf("Summary = %q", got.Summary)
	}
	wantAgent := []string{
		"looking into it",
		"found the root cause",
		"handling codex now",
		"done, opening PR",
	}
	if len(got.AgentTurns) != len(wantAgent) {
		t.Fatalf("AgentTurns = %v, want %v", got.AgentTurns, wantAgent)
	}
	for i, w := range wantAgent {
		if got.AgentTurns[i] != w {
			t.Errorf("AgentTurns[%d] = %q, want %q", i, got.AgentTurns[i], w)
		}
	}
	if got.HumanCount != 3 {
		t.Errorf("HumanCount = %d, want 3 (tool_result-only line must be dropped)", got.HumanCount)
	}
	if got.AgentCount != 4 {
		t.Errorf("AgentCount = %d, want 4", got.AgentCount)
	}
}

func TestExtractConversationSlice_ClaudeAgentTailCap(t *testing.T) {
	opts := DefaultSliceOptions()
	opts.MaxAgentTurns = 2
	lines := []string{
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"brief"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a1"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a3"}]}}`,
	}
	path := writeJSONL(t, lines...)

	got, err := ExtractConversationSlice(path, opts)
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	want := []string{"a2", "a3"}
	if len(got.AgentTurns) != len(want) {
		t.Fatalf("AgentTurns = %v, want %v", got.AgentTurns, want)
	}
	for i, w := range want {
		if got.AgentTurns[i] != w {
			t.Errorf("AgentTurns[%d] = %q, want %q", i, got.AgentTurns[i], w)
		}
	}
	if got.AgentCount != 3 {
		t.Errorf("AgentCount = %d, want 3 (pre-cap total)", got.AgentCount)
	}
}

func TestExtractConversationSlice_Codex(t *testing.T) {
	lines := []string{
		`{"type":"event_msg","payload":{"type":"user_message","message":"please fix the bug"}}`,
		// consecutive duplicate user_message (e.g. a client retry) must be deduped
		`{"type":"event_msg","payload":{"type":"user_message","message":"please fix the bug"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"on it"}}`,
		// response_item echo of the same user turn must NOT be double-counted
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"please fix the bug"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"actually also check the tests"}}`,
	}
	path := writeJSONL(t, lines...)

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}

	if got.Brief != "please fix the bug" {
		t.Errorf("Brief = %q", got.Brief)
	}
	wantRescoping := []string{"actually also check the tests"}
	if len(got.Rescoping) != len(wantRescoping) || got.Rescoping[0] != wantRescoping[0] {
		t.Errorf("Rescoping = %v, want %v", got.Rescoping, wantRescoping)
	}
	// the consecutive duplicate "please fix the bug" user_message is deduped,
	// and the response_item echo is ignored entirely, so HumanCount is 2, not 4.
	if got.HumanCount != 2 {
		t.Errorf("HumanCount = %d, want 2 (consecutive dup deduped, response_item ignored)", got.HumanCount)
	}
	if got.AgentCount != 1 {
		t.Errorf("AgentCount = %d, want 1", got.AgentCount)
	}
	if len(got.AgentTurns) != 1 || got.AgentTurns[0] != "on it" {
		t.Errorf("AgentTurns = %v", got.AgentTurns)
	}
}

func TestExtractConversationSlice_Copilot(t *testing.T) {
	lines := []string{
		`{"type":"user.message","data":{"content":"please refactor this module"}}`,
		`{"type":"assistant.message","data":{"content":"refactored, running tests"}}`,
	}
	path := writeJSONL(t, lines...)

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	if got.Brief != "please refactor this module" {
		t.Errorf("Brief = %q", got.Brief)
	}
	if len(got.AgentTurns) != 1 || got.AgentTurns[0] != "refactored, running tests" {
		t.Errorf("AgentTurns = %v", got.AgentTurns)
	}
	if got.HumanCount != 1 || got.AgentCount != 1 {
		t.Errorf("HumanCount=%d AgentCount=%d, want 1/1", got.HumanCount, got.AgentCount)
	}
}

func TestExtractConversationSlice_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	if !got.Empty() {
		t.Errorf("expected Empty() == true for empty file, got %+v", got)
	}
}

func TestExtractConversationSlice_MetadataOnly(t *testing.T) {
	path := writeJSONL(t, `{"type":"session_meta","cwd":"/tmp","other":"stuff"}`)

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	if !got.Empty() {
		t.Errorf("expected Empty() == true for metadata-only file, got %+v", got)
	}
}

func TestExtractConversationSlice_MissingPath(t *testing.T) {
	_, err := ExtractConversationSlice("/nonexistent/path/does-not-exist.jsonl", DefaultSliceOptions())
	if err == nil {
		t.Fatal("expected non-nil error for missing path")
	}
}

func TestExtractConversationSlice_Caps(t *testing.T) {
	opts := DefaultSliceOptions()
	opts.TurnCharCap = 20
	opts.SummaryCharCap = 200

	longHuman := strings.Repeat("x", 50)
	// Longer than TurnCharCap (20) but shorter than SummaryCharCap (200): must
	// NOT be truncated, proving the larger summary cap is honored.
	longSummary := strings.Repeat("y", 100)

	lines := []string{
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"` + longHuman + `"}}`,
		`{"type":"user","isCompactSummary":true,"message":{"content":"` + longSummary + `"}}`,
	}
	path := writeJSONL(t, lines...)

	got, err := ExtractConversationSlice(path, opts)
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}

	if !strings.HasPrefix(got.Brief, strings.Repeat("x", 20)) || !strings.Contains(got.Brief, "truncated, 50 chars total") {
		t.Errorf("Brief not truncated as expected: %q", got.Brief)
	}
	if got.Summary != longSummary {
		t.Errorf("Summary should not be truncated (len %d < SummaryCharCap %d), got %q", len(longSummary), opts.SummaryCharCap, got.Summary)
	}
}

func TestExtractConversationSlice_HugeLine(t *testing.T) {
	// ~2MB single line that must not break the streaming pass.
	hugeText := strings.Repeat("z", 2*1024*1024)
	hugeLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + hugeText + `"}]}}`
	normalHuman := `{"type":"user","origin":{"kind":"human"},"message":{"content":"a normal turn"}}`

	path := writeJSONL(t, hugeLine, normalHuman)

	got, err := ExtractConversationSlice(path, DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	if got.Brief != "a normal turn" {
		t.Errorf("Brief = %q, want %q", got.Brief, "a normal turn")
	}
	if got.AgentCount != 1 {
		t.Errorf("AgentCount = %d, want 1", got.AgentCount)
	}
	if len(got.AgentTurns) != 1 {
		t.Fatalf("AgentTurns = %v, want 1 entry", got.AgentTurns)
	}
	// Default TurnCharCap (3000) truncates the huge agent turn.
	if !strings.Contains(got.AgentTurns[0], "truncated") {
		t.Errorf("expected huge agent turn to be truncated")
	}
}

func TestConversationSlice_Render(t *testing.T) {
	s := ConversationSlice{
		Brief:      "the brief",
		Rescoping:  []string{"scope 1", "scope 2"},
		Summary:    "the summary",
		AgentTurns: []string{"agent 1", "agent 2"},
	}
	got := s.Render()

	wantSections := []string{
		"## TICKET BRIEF (first human turn)\nthe brief",
		"## LATER HUMAN TURNS (re-scoping)\nscope 1\n---\nscope 2",
		"## COMPACTION SUMMARY (most recent)\nthe summary",
		"## AGENT'S LAST STATUS TURNS\nagent 1\n---\nagent 2",
	}
	want := strings.Join(wantSections, "\n\n")
	if got != want {
		t.Errorf("Render() =\n%s\nwant:\n%s", got, want)
	}
	if s.Empty() {
		t.Error("expected populated slice to report Empty() == false")
	}
}

func TestConversationSlice_RenderOmitsEmptySections(t *testing.T) {
	s := ConversationSlice{
		Brief:      "only the brief",
		AgentTurns: []string{"only an agent turn"},
	}
	got := s.Render()

	if strings.Contains(got, "LATER HUMAN TURNS") {
		t.Errorf("expected no rescoping section, got:\n%s", got)
	}
	if strings.Contains(got, "COMPACTION SUMMARY") {
		t.Errorf("expected no summary section, got:\n%s", got)
	}
	want := "## TICKET BRIEF (first human turn)\nonly the brief\n\n## AGENT'S LAST STATUS TURNS\nonly an agent turn"
	if got != want {
		t.Errorf("Render() =\n%s\nwant:\n%s", got, want)
	}
}

func TestConversationSlice_EmptyStruct(t *testing.T) {
	var s ConversationSlice
	if !s.Empty() {
		t.Error("expected zero-value ConversationSlice to be Empty()")
	}
	if s.Render() != "" {
		t.Errorf("expected empty Render(), got %q", s.Render())
	}
}
