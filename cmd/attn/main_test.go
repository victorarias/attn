package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/transcript"
)

func writeCopilotSessionState(t *testing.T, homeDir, sessionID, cwd string, startTime time.Time, withStart, withAssistant bool, modTime time.Time) string {
	t.Helper()

	sessionDir := filepath.Join(homeDir, ".copilot", "session-state", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	workspace := fmt.Sprintf("id: %s\ncwd: %s\n", sessionID, cwd)
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(workspace), 0o644); err != nil {
		t.Fatalf("write workspace.yaml: %v", err)
	}

	lines := ""
	if withStart {
		lines += fmt.Sprintf(
			`{"type":"session.start","data":{"sessionId":"%s","startTime":"%s"}}`+"\n",
			sessionID,
			startTime.UTC().Format(time.RFC3339Nano),
		)
	}
	if withAssistant {
		lines += `{"type":"assistant.message","data":{"content":"ok"}}` + "\n"
	} else {
		lines += `{"type":"user.message","data":{"content":"hi"}}` + "\n"
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}
	if err := os.Chtimes(eventsPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes events.jsonl: %v", err)
	}

	return eventsPath
}

func TestFindCopilotTranscript_PrefersClosestStartTime(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)

	expected := writeCopilotSessionState(
		t,
		homeDir,
		"session-a",
		cwd,
		startedAt.Add(5*time.Second),
		true,
		true,
		startedAt.Add(1*time.Minute),
	)
	_ = writeCopilotSessionState(
		t,
		homeDir,
		"session-b",
		cwd,
		startedAt.Add(-30*time.Minute),
		true,
		true,
		startedAt.Add(2*time.Minute), // Newer modtime, but wrong start window.
	)

	got := transcript.FindCopilotTranscript(cwd, startedAt)
	if got != expected {
		t.Fatalf("FindCopilotTranscript() = %q, want %q", got, expected)
	}
}

func TestFindCopilotTranscript_FallsBackToNewestModTime(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)

	_ = writeCopilotSessionState(
		t,
		homeDir,
		"session-a",
		cwd,
		startedAt,
		false, // No start metadata, forces fallback
		true,
		startedAt.Add(1*time.Minute),
	)
	expected := writeCopilotSessionState(
		t,
		homeDir,
		"session-b",
		cwd,
		startedAt,
		false, // No start metadata, forces fallback
		true,
		startedAt.Add(2*time.Minute),
	)

	got := transcript.FindCopilotTranscript(cwd, startedAt)
	if got != expected {
		t.Fatalf("FindCopilotTranscript() = %q, want %q", got, expected)
	}
}

func TestResolveCopilotTranscript_PrefersResumeSessionPath(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)
	resumeID := "resume-session-id"
	expected := writeCopilotSessionState(
		t,
		homeDir,
		resumeID,
		"/repo/from-resume",
		startedAt,
		true,
		true,
		startedAt.Add(30*time.Second),
	)

	got := transcript.FindCopilotTranscriptForResume(resumeID)
	if got == "" {
		got = transcript.FindCopilotTranscript("/repo/other", startedAt)
	}
	if got != expected {
		t.Fatalf("copilot transcript resolution = %q, want %q", got, expected)
	}
}

func TestResolveCopilotTranscript_FallsBackWhenResumePathMissing(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)
	expected := writeCopilotSessionState(
		t,
		homeDir,
		"fallback-session",
		cwd,
		startedAt.Add(3*time.Second),
		true,
		true,
		startedAt.Add(1*time.Minute),
	)

	got := transcript.FindCopilotTranscriptForResume("missing-resume-id")
	if got == "" {
		got = transcript.FindCopilotTranscript(cwd, startedAt)
	}
	if got != expected {
		t.Fatalf("copilot transcript resolution = %q, want %q", got, expected)
	}
}

func TestParseDirectLaunchArgs_ResumePickerWithFlagAfterResume(t *testing.T) {
	parsed := parseDirectLaunchArgs([]string{"--resume", "--fork-session", "--", "--model", "foo"})
	if !parsed.resumePicker {
		t.Fatalf("expected resume picker to be enabled")
	}
	if parsed.resumeID != "" {
		t.Fatalf("expected empty resume id, got %q", parsed.resumeID)
	}
	if !parsed.forkSession {
		t.Fatalf("expected fork-session flag to be preserved")
	}
	if len(parsed.agentArgs) != 2 || parsed.agentArgs[0] != "--model" || parsed.agentArgs[1] != "foo" {
		t.Fatalf("unexpected agent args: %#v", parsed.agentArgs)
	}
}

func TestParseDirectLaunchArgs_ResumeIDStillAccepted(t *testing.T) {
	parsed := parseDirectLaunchArgs([]string{"--resume", "abc123", "--fork-session"})
	if parsed.resumePicker {
		t.Fatalf("expected resume picker to be disabled")
	}
	if parsed.resumeID != "abc123" {
		t.Fatalf("expected resume id abc123, got %q", parsed.resumeID)
	}
	if !parsed.forkSession {
		t.Fatalf("expected fork-session flag to be true")
	}
}
