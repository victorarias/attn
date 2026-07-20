package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

func writeClaudeTranscriptFixture(t *testing.T, sessionID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	projDir := filepath.Join(home, ".claude", "projects", "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projDir, sessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript fixture: %v", err)
	}
}

// Claude resumes via `claude -r <id>`, which needs a transcript on disk. The
// transcript is written lazily on the first turn, so resumability == transcript
// existence.
func TestClaudeResumeAvailable(t *testing.T) {
	claude := &Claude{}

	// Empty home: no transcript for this id -> not resumable.
	t.Setenv(toolhome.EnvVar, t.TempDir())
	if ResumeAvailable(claude, "no-transcript-id") {
		t.Fatal("ResumeAvailable should be false when no transcript exists")
	}

	// With a transcript on disk -> resumable.
	writeClaudeTranscriptFixture(t, "has-transcript-id")
	if !ResumeAvailable(claude, "has-transcript-id") {
		t.Fatal("ResumeAvailable should be true when the transcript exists")
	}
}

// An empty resume id is never resumable: there is nothing to resume.
func TestResumeAvailableEmptyID(t *testing.T) {
	if ResumeAvailable(&Claude{}, "") {
		t.Fatal("ResumeAvailable(\"\") must be false")
	}
}

func TestCodexResumeAvailable(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if ResumeAvailable(&Codex{}, "missing-rollout-id") {
		t.Fatal("ResumeAvailable should be false when no Codex rollout exists")
	}

	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "07", "20")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir Codex sessions dir: %v", err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"has-rollout-id","cwd":"/tmp"}}` + "\n")
	if err := os.WriteFile(filepath.Join(sessionsDir, "rollout-fixture.jsonl"), rollout, 0o644); err != nil {
		t.Fatalf("write Codex rollout fixture: %v", err)
	}
	if !ResumeAvailable(&Codex{}, "has-rollout-id") {
		t.Fatal("ResumeAvailable should be true when the exact Codex rollout exists")
	}
}
