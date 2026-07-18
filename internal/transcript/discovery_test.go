package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCopilotSessionState(
	t *testing.T,
	homeDir,
	sessionID,
	cwd string,
	startTime time.Time,
	withStart,
	withAssistant bool,
	modTime time.Time,
) string {
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

func writeCodexTranscript(
	t *testing.T,
	homeDir,
	sessionID,
	cwd string,
	startTime time.Time,
	modTime time.Time,
) string {
	t.Helper()

	sessionDir := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "17")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir codex session dir: %v", err)
	}

	transcriptPath := filepath.Join(sessionDir, fmt.Sprintf("rollout-%s-%s.jsonl", startTime.UTC().Format("2006-01-02T15-04-05"), sessionID))
	lines := fmt.Sprintf(
		`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","timestamp":"%s","cwd":"%s"}}`+"\n",
		startTime.UTC().Format(time.RFC3339Nano),
		sessionID,
		startTime.UTC().Format(time.RFC3339Nano),
		cwd,
	)
	if err := os.WriteFile(transcriptPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write codex transcript: %v", err)
	}
	if err := os.Chtimes(transcriptPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes codex transcript: %v", err)
	}

	return transcriptPath
}

func TestFindCodexTranscript_MatchesSymlinkEquivalentCWD(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	root := t.TempDir()
	realCWD := filepath.Join(root, "real", "project")
	if err := os.MkdirAll(realCWD, 0o755); err != nil {
		t.Fatalf("mkdir real cwd: %v", err)
	}
	linkRoot := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkCWD := filepath.Join(linkRoot, "project")
	startedAt := time.Date(2026, 5, 17, 14, 6, 42, 0, time.UTC)

	expected := writeCodexTranscript(
		t,
		homeDir,
		"codex-session-123",
		realCWD,
		startedAt,
		startedAt.Add(1*time.Minute),
	)

	got := FindCodexTranscript(linkCWD, startedAt)
	if got != expected {
		t.Fatalf("FindCodexTranscript() = %q, want %q", got, expected)
	}
}

func TestFindCodexTranscriptForResume_SelectsExactNativeID(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	wrong := writeCodexTranscript(t, homeDir, "native-wrong", "/repo", now, now)
	want := writeCodexTranscript(t, homeDir, "native-target", "/other", now.Add(time.Second), now.Add(time.Second))
	if got := FindCodexTranscriptForResume("native-target"); got != want {
		t.Fatalf("FindCodexTranscriptForResume() = %q, want %q (wrong=%q)", got, want, wrong)
	}
}

func TestFindCodexTranscriptForResume_HonorsCodexHome(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "07", "18")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sessionDir, "rollout-native-target.jsonl")
	line := fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s","cwd":"/synthetic"}}`+"\n", "native-target")
	if err := os.WriteFile(want, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(want, start, start); err != nil {
		t.Fatal(err)
	}
	if got := FindCodexTranscriptForResume("native-target"); got != want {
		t.Fatalf("FindCodexTranscriptForResume() = %q, want %q", got, want)
	}
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
		startedAt.Add(2*time.Minute),
	)

	got := FindCopilotTranscript(cwd, startedAt)
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
		false,
		true,
		startedAt.Add(1*time.Minute),
	)
	expected := writeCopilotSessionState(
		t,
		homeDir,
		"session-b",
		cwd,
		startedAt,
		false,
		true,
		startedAt.Add(2*time.Minute),
	)

	got := FindCopilotTranscript(cwd, startedAt)
	if got != expected {
		t.Fatalf("FindCopilotTranscript() = %q, want %q", got, expected)
	}
}

func TestFindClaudeTranscript_FindsSessionFile(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	sessionID := "claude-session-123"
	projectDir := filepath.Join(homeDir, ".claude", "projects", "-Users-test-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	expected := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(expected, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	got := FindClaudeTranscript(sessionID)
	if got != expected {
		t.Fatalf("FindClaudeTranscript() = %q, want %q", got, expected)
	}
}

func TestFindClaudeTranscript_ReturnsEmptyWhenMissing(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	projectDir := filepath.Join(homeDir, ".claude", "projects", "-Users-test-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	if got := FindClaudeTranscript("missing-session"); got != "" {
		t.Fatalf("FindClaudeTranscript() = %q, want empty", got)
	}
}
