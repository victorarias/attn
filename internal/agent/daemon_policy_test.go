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

func (d executableClassifierDriver) ClassifyWithExecutable(text, executable string, timeout time.Duration) (string, error) {
	if executable == "custom-bin" {
		return "waiting_input", nil
	}
	return "idle", nil
}

func TestRecoverOnMissingPTY(t *testing.T) {
	if !RecoverOnMissingPTY(Get("claude")) {
		t.Fatal("claude should be recoverable when PTY is missing")
	}
	if RecoverOnMissingPTY(Get("codex")) {
		t.Fatal("codex should not be recoverable when PTY is missing")
	}
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
	if got := RecoveredRunningSessionState(defaultDriver, protocol.StateWaitingInput); got != protocol.SessionStateWaitingInput {
		t.Fatalf("default recovered waiting_input = %s, want waiting_input", got)
	}
	if got := RecoveredRunningSessionState(Get("codex"), protocol.StateWaitingInput); got != protocol.SessionStateLaunching {
		t.Fatalf("codex recovered waiting_input = %s, want launching", got)
	}
	if got := RecoveredRunningSessionState(Get("copilot"), protocol.StatePendingApproval); got != protocol.SessionStatePendingApproval {
		t.Fatalf("copilot recovered pending_approval = %s, want pending_approval", got)
	}
}

func TestShouldApplyPTYState_AgentOverrides(t *testing.T) {
	if ShouldApplyPTYState(Get("codex"), protocol.SessionStateWorking, protocol.StateWaitingInput) {
		t.Fatal("codex should ignore waiting_input PTY state updates")
	}
	if !ShouldApplyPTYState(Get("codex"), protocol.SessionStateWorking, protocol.StatePendingApproval) {
		t.Fatal("codex should accept pending_approval PTY state updates")
	}
	if ShouldApplyPTYState(Get("copilot"), protocol.SessionStatePendingApproval, protocol.StateWorking) {
		t.Fatal("copilot should ignore working PTY noise while pending_approval")
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

	_, _, err := ExtractLastAssistantForClassification(
		Get("claude"),
		path,
		500,
		time.Now(),
		"turn-1",
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
	state, err, ok := ClassifyWithDriver(d, "test", "custom-bin", 5*time.Second)
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
	_, _, ok := ClassifyWithDriver(d, "test", "", time.Second)
	if ok {
		t.Fatal("expected no classifier dispatch when capability disabled")
	}
}
