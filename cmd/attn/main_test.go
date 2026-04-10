package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
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

func TestParseDirectLaunchArgs_YoloFlag(t *testing.T) {
	parsed := parseDirectLaunchArgs([]string{"--yolo", "--", "--model", "foo"})
	if !parsed.yoloMode {
		t.Fatal("expected yolo mode to be enabled")
	}
	if len(parsed.agentArgs) != 2 || parsed.agentArgs[0] != "--model" || parsed.agentArgs[1] != "foo" {
		t.Fatalf("unexpected agent args: %#v", parsed.agentArgs)
	}
}

func TestIsVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "long flag", args: []string{"attn", "--version"}, want: true},
		{name: "subcommand", args: []string{"attn", "version"}, want: true},
		{name: "other flag", args: []string{"attn", "--help"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVersionCommand(tt.args); got != tt.want {
				t.Fatalf("isVersionCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsProtocolVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "protocol flag", args: []string{"attn", "--protocol-version"}, want: true},
		{name: "version flag", args: []string{"attn", "--version"}, want: false},
		{name: "subcommand", args: []string{"attn", "version"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProtocolVersionCommand(tt.args); got != tt.want {
				t.Fatalf("isProtocolVersionCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsBuildInfoJSONCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "build info flag", args: []string{"attn", "--build-info-json"}, want: true},
		{name: "protocol flag", args: []string{"attn", "--protocol-version"}, want: false},
		{name: "version flag", args: []string{"attn", "--version"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBuildInfoJSONCommand(tt.args); got != tt.want {
				t.Fatalf("isBuildInfoJSONCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestApplyLegacyBuildInfoOverrides_UsesLegacyMainInjectionWhenNeeded(t *testing.T) {
	previousBuildinfoVersion := buildinfo.Version
	previousBuildinfoBuildTime := buildinfo.BuildTime
	previousBuildinfoSourceFingerprint := buildinfo.SourceFingerprint
	previousBuildinfoGitCommit := buildinfo.GitCommit
	previousVersion := version
	previousBuildTime := buildTime
	previousSourceFingerprint := sourceFingerprint
	previousGitCommit := gitCommit
	t.Cleanup(func() {
		buildinfo.Version = previousBuildinfoVersion
		buildinfo.BuildTime = previousBuildinfoBuildTime
		buildinfo.SourceFingerprint = previousBuildinfoSourceFingerprint
		buildinfo.GitCommit = previousBuildinfoGitCommit
		version = previousVersion
		buildTime = previousBuildTime
		sourceFingerprint = previousSourceFingerprint
		gitCommit = previousGitCommit
	})

	buildinfo.Version = "dev"
	buildinfo.BuildTime = "unknown"
	buildinfo.SourceFingerprint = "unknown"
	buildinfo.GitCommit = "unknown"
	version = "1.2.3"
	buildTime = "2026-04-06T00:00:00Z"
	sourceFingerprint = "tree:abc123"
	gitCommit = "1234567890abcdef"

	applyLegacyBuildInfoOverrides()

	if buildinfo.Version != "1.2.3" {
		t.Fatalf("buildinfo.Version = %q, want legacy main.version value", buildinfo.Version)
	}
	if buildinfo.BuildTime != "2026-04-06T00:00:00Z" {
		t.Fatalf("buildinfo.BuildTime = %q, want legacy main.buildTime value", buildinfo.BuildTime)
	}
	if buildinfo.SourceFingerprint != "tree:abc123" {
		t.Fatalf("buildinfo.SourceFingerprint = %q, want legacy main.sourceFingerprint value", buildinfo.SourceFingerprint)
	}
	if buildinfo.GitCommit != "1234567890abcdef" {
		t.Fatalf("buildinfo.GitCommit = %q, want legacy main.gitCommit value", buildinfo.GitCommit)
	}
}

func TestApplyLegacyBuildInfoOverrides_PreservesInjectedBuildinfo(t *testing.T) {
	previousBuildinfoVersion := buildinfo.Version
	previousBuildinfoBuildTime := buildinfo.BuildTime
	previousBuildinfoSourceFingerprint := buildinfo.SourceFingerprint
	previousBuildinfoGitCommit := buildinfo.GitCommit
	previousVersion := version
	previousBuildTime := buildTime
	previousSourceFingerprint := sourceFingerprint
	previousGitCommit := gitCommit
	t.Cleanup(func() {
		buildinfo.Version = previousBuildinfoVersion
		buildinfo.BuildTime = previousBuildinfoBuildTime
		buildinfo.SourceFingerprint = previousBuildinfoSourceFingerprint
		buildinfo.GitCommit = previousBuildinfoGitCommit
		version = previousVersion
		buildTime = previousBuildTime
		sourceFingerprint = previousSourceFingerprint
		gitCommit = previousGitCommit
	})

	buildinfo.Version = "9.9.9"
	buildinfo.BuildTime = "2026-04-06T12:34:56Z"
	buildinfo.SourceFingerprint = "git:new"
	buildinfo.GitCommit = "fedcba0987654321"
	version = "1.2.3"
	buildTime = "2001-01-01T00:00:00Z"
	sourceFingerprint = "tree:old"
	gitCommit = "0123456789abcdef"

	applyLegacyBuildInfoOverrides()

	if buildinfo.Version != "9.9.9" {
		t.Fatalf("buildinfo.Version = %q, want primary buildinfo injection to win", buildinfo.Version)
	}
	if buildinfo.BuildTime != "2026-04-06T12:34:56Z" {
		t.Fatalf("buildinfo.BuildTime = %q, want primary buildinfo injection to win", buildinfo.BuildTime)
	}
	if buildinfo.SourceFingerprint != "git:new" {
		t.Fatalf("buildinfo.SourceFingerprint = %q, want primary buildinfo injection to win", buildinfo.SourceFingerprint)
	}
	if buildinfo.GitCommit != "fedcba0987654321" {
		t.Fatalf("buildinfo.GitCommit = %q, want primary buildinfo injection to win", buildinfo.GitCommit)
	}
}
