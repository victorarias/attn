package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type noPolicyDriver struct {
	testDriver
}

type executableClassifierDriver struct {
	testDriver
}

func (d executableClassifierDriver) Classify(text string, timeout time.Duration) (string, error) {
	return "idle", nil
}

func (d executableClassifierDriver) ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error) {
	if executable == "custom-bin" && workDir == "/tmp/repo" {
		return "waiting_input", nil
	}
	return "idle", nil
}

func TestRecoveredRunningSessionState_DefaultAndAgentOverrides(t *testing.T) {
	defaultDriver := noPolicyDriver{
		testDriver: testDriver{
			name: "nopolicy",
			caps: Capabilities{
				HasTranscript: true,
			},
		},
	}
	if got, ok := RecoveredRunningSessionState(defaultDriver, protocol.StateWaitingInput); !ok || got != protocol.SessionStateWaitingInput {
		t.Fatalf("default recovered waiting_input = %s (ok=%v), want waiting_input", got, ok)
	}
	if got, ok := RecoveredRunningSessionState(Get("copilot"), protocol.StatePendingApproval); !ok || got != protocol.SessionStatePendingApproval {
		t.Fatalf("copilot recovered pending_approval = %s (ok=%v), want pending_approval", got, ok)
	}
	// No opinion, not a state. Codex announces approvals in its title, which the
	// resolver reads as evidence, so its worker caches nothing to recover; and a
	// driver with nothing to say must not have `launching` put in its mouth,
	// because recovery reads that as permission to overwrite the session's real
	// state.
	if got, ok := RecoveredRunningSessionState(Get("codex"), protocol.StateWaitingInput); ok {
		t.Fatalf("codex recovered waiting_input = %s (ok=%v), want no opinion", got, ok)
	}
	if got, ok := RecoveredRunningSessionState(Get("codex"), protocol.StatePendingApproval); ok {
		t.Fatalf("codex recovered pending_approval = %s (ok=%v), want no opinion", got, ok)
	}
	if got, ok := RecoveredRunningSessionState(Get("claude"), protocol.StateWorking); ok {
		t.Fatalf("claude recovered working = %s (ok=%v), want no opinion", got, ok)
	}
	if got, ok := RecoveredRunningSessionState(defaultDriver, protocol.StateWorking); ok {
		t.Fatalf("default recovered working = %s (ok=%v), want no opinion", got, ok)
	}
}

// No driver may filter live PTY state any more: the interface is gone. The one
// source that still applies a state is the worker poll, whose only job is ending
// `launching`, and a per-agent veto over that arbitrates against the resolver
// rather than for it. This test is the guard that nobody reintroduces one.
func TestNoDriverFiltersPTYState(t *testing.T) {
	type ptyStateFilter interface {
		ShouldApplyPTYState(current protocol.SessionState, incoming string) bool
	}
	for _, name := range []string{"claude", "codex", "copilot"} {
		if _, ok := Get(name).(ptyStateFilter); ok {
			t.Fatalf("%s filters PTY state; its state comes from the resolver", name)
		}
	}
}

func TestResumePolicy_Claude(t *testing.T) {
	claude := Get("claude")
	resolved := ResolveSpawnResumeSessionID(claude, "sess-1", "", "stored-resume")
	if resolved != "stored-resume" {
		t.Fatalf("ResolveSpawnResumeSessionID() = %q, want stored-resume", resolved)
	}
	persisted := SpawnResumeSessionID(claude, "sess-1", "", false)
	if persisted != "sess-1" {
		t.Fatalf("SpawnResumeSessionID() = %q, want sess-1", persisted)
	}
	pathResume := ResumeSessionIDFromStopTranscriptPath(claude, "/tmp/abc-123.jsonl")
	if pathResume != "abc-123" {
		t.Fatalf("ResumeSessionIDFromStopTranscriptPath() = %q, want abc-123", pathResume)
	}
}

func TestResumePolicy_Codex(t *testing.T) {
	codex := Get("codex")
	resolved := ResolveSpawnResumeSessionID(codex, "attn-session", "attn-session", "codex-session")
	if resolved != "codex-session" {
		t.Fatalf("ResolveSpawnResumeSessionID() = %q, want codex-session", resolved)
	}
	persisted := SpawnResumeSessionID(codex, "attn-session", "", false)
	if persisted != "" {
		t.Fatalf("SpawnResumeSessionID() = %q, want empty until hook reports Codex id", persisted)
	}
}

func TestExtractLastAssistantForClassification_DefaultFallback(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"done"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	defaultDriver := noPolicyDriver{
		testDriver: testDriver{
			name: "nopolicy",
			caps: Capabilities{
				HasTranscript: true,
			},
		},
	}
	msg, turnID, err := ExtractLastAssistantForClassification(defaultDriver, path, 500, time.Now(), "")
	if err != nil {
		t.Fatalf("ExtractLastAssistantForClassification() error = %v", err)
	}
	if msg != "done" {
		t.Fatalf("ExtractLastAssistantForClassification() message = %q, want done", msg)
	}
	if turnID != "" {
		t.Fatalf("ExtractLastAssistantForClassification() turnID = %q, want empty", turnID)
	}
}

func TestExtractLastAssistantForClassification_ClaudeNoNewTurn(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	now := time.Now().Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"` + now + `"}`,
		`{"type":"assistant","uuid":"turn-1","message":{"role":"assistant","content":"hello"},"timestamp":"` + now + `"}`,
	}
	if err := os.WriteFile(path, []byte(lines[0]+"\n"+lines[1]+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	_, _, err := (&Claude{}).extractLastAssistantForClassification(
		path,
		500,
		time.Now(),
		"turn-1",
		0,
		0,
	)
	if !errors.Is(err, ErrNoNewAssistantTurn) {
		t.Fatalf("expected ErrNoNewAssistantTurn, got %v", err)
	}
}

func TestClassifyWithDriver_ExecutableProvider(t *testing.T) {
	d := executableClassifierDriver{
		testDriver: testDriver{
			name: "exec-classifier",
			caps: Capabilities{
				HasClassifier: true,
			},
		},
	}
	state, err, ok := ClassifyWithDriver(d, "test", "custom-bin", "/tmp/repo", 5*time.Second)
	if !ok {
		t.Fatal("expected classifier dispatch")
	}
	if err != nil {
		t.Fatalf("ClassifyWithDriver() error = %v", err)
	}
	if state != "waiting_input" {
		t.Fatalf("ClassifyWithDriver() = %q, want waiting_input", state)
	}
}

func TestClassifyWithDriver_NoClassifier(t *testing.T) {
	d := noPolicyDriver{
		testDriver: testDriver{
			name: "no-classifier",
			caps: Capabilities{},
		},
	}
	_, _, ok := ClassifyWithDriver(d, "test", "", "", time.Second)
	if ok {
		t.Fatal("expected no classifier dispatch when capability disabled")
	}
}
