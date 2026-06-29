package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeClaudeTranscriptFixture(t *testing.T, sessionID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", t.TempDir())
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

// Drivers that don't implement ResumeAvailabilityProvider are assumed
// always-resumable, so their resume behavior is unchanged. Codex resumes by
// rollout id (not a transcript lookup) and a zero-turn codex never stores a
// rollout id, so it never reaches the reload path with a doomed resume id.
func TestResumeAvailableDefaultsTrueForCodex(t *testing.T) {
	if _, ok := any(&Codex{}).(ResumeAvailabilityProvider); ok {
		t.Skip("Codex now implements ResumeAvailabilityProvider; update this test to assert its real behavior")
	}
	if !ResumeAvailable(&Codex{}, "any-rollout-id") {
		t.Fatal("ResumeAvailable should default to true for a driver without the capability")
	}
}
