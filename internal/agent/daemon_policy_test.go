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
	if got := RecoveredRunningSessionState(Get("codex"), protocol.StatePendingApproval); got != protocol.SessionStateLaunching {
		t.Fatalf("codex recovered pending_approval = %s, want launching", got)
	}
	if got := RecoveredRunningSessionState(Get("copilot"), protocol.StatePendingApproval); got != protocol.SessionStatePendingApproval {
		t.Fatalf("copilot recovered pending_approval = %s, want pending_approval", got)
	}
}

func TestShouldApplyPTYState_AgentOverrides(t *testing.T) {
	for _, incoming := range []string{
		protocol.StateWorking,
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateIdle,
	} {
		if ShouldApplyPTYState(Get("codex"), protocol.SessionStateWorking, incoming) {
			t.Fatalf("codex should ignore %s PTY state updates", incoming)
		}
	}
	if ShouldApplyPTYState(Get("codex"), protocol.SessionStateLaunching, protocol.StateWorking) {
		t.Fatal("codex should ignore launch-time working PTY noise")
	}
	if ShouldApplyPTYState(Get("codex"), protocol.SessionStateIdle, protocol.StateWorking) {
		t.Fatal("codex should ignore working PTY noise while idle")
	}
	if ShouldApplyPTYState(Get("codex"), protocol.SessionStateWaitingInput, protocol.StateWorking) {
		t.Fatal("codex should ignore working PTY noise while waiting_input")
	}
	// The one exception: no hook fires when the user approves, so codex relies on
	// the rendered screen to clear pending_approval -> working.
	if !ShouldApplyPTYState(Get("codex"), protocol.SessionStatePendingApproval, protocol.StateWorking) {
		t.Fatal("codex should apply working PTY state to clear pending_approval")
	}
	// But only working clears it; other transitions out of pending stay hook-owned.
	for _, incoming := range []string{
		protocol.StateWaitingInput,
		protocol.StateIdle,
		protocol.StatePendingApproval,
	} {
		if ShouldApplyPTYState(Get("codex"), protocol.SessionStatePendingApproval, incoming) {
			t.Fatalf("codex should ignore %s PTY state while pending_approval", incoming)
		}
	}
	if ShouldApplyPTYState(Get("copilot"), protocol.SessionStatePendingApproval, protocol.StateWorking) {
		t.Fatal("copilot should ignore working PTY noise while pending_approval")
	}
}

// TestShouldApplyPTYState_ClaudeProtectsScheduled guards the parked-on-a-loop
// state: Claude's detector still classifies the settled idle prompt while a
// session is scheduled, but only a genuine resume (working) may move it out.
// This also asserts Claude actually satisfies PTYStatePolicyProvider — if the
// interface were unsatisfied, ShouldApplyPTYState would fall to the default
// (return true) and the idle/waiting_input/pending_approval cases below would
// wrongly pass.
func TestShouldApplyPTYState_ClaudeProtectsScheduled(t *testing.T) {
	claude := Get("claude")

	for _, incoming := range []string{
		protocol.StateIdle,
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
	} {
		if ShouldApplyPTYState(claude, protocol.SessionStateScheduled, incoming) {
			t.Fatalf("claude should reject %s PTY state while scheduled", incoming)
		}
	}
	if !ShouldApplyPTYState(claude, protocol.SessionStateScheduled, protocol.StateWorking) {
		t.Fatal("claude should apply working PTY state to resume a scheduled session")
	}

	// Non-scheduled Claude transitions keep the default (detector-trusting) behavior.
	for _, incoming := range []string{
		protocol.StateWorking,
		protocol.StateWaitingInput,
		protocol.StateIdle,
		protocol.StatePendingApproval,
	} {
		if !ShouldApplyPTYState(claude, protocol.SessionStateWorking, incoming) {
			t.Fatalf("claude should apply %s PTY state while working", incoming)
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
