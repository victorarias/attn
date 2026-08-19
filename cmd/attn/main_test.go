package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

type fakeWorkspaceContextCheckoutClient struct {
	failures int
	calls    int
	path     string
}

func (f *fakeWorkspaceContextCheckoutClient) CheckoutWorkspaceContext(string, bool) (*protocol.WorkspaceContextResult, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, fmt.Errorf("source session not found")
	}
	return &protocol.WorkspaceContextResult{Path: f.path}, nil
}

func TestWritePrivateFileReplacesPublicFileWithOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(path, []byte("private")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("capture permissions = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "private" {
		t.Fatalf("capture contents = %q, want private", data)
	}
}

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
	t.Setenv(toolhome.EnvVar, homeDir)

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
	t.Setenv(toolhome.EnvVar, homeDir)

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
	t.Setenv(toolhome.EnvVar, homeDir)

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
	t.Setenv(toolhome.EnvVar, homeDir)

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
	parsed, err := parseDirectLaunchArgs([]string{"--resume", "--yolo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !parsed.resumePicker {
		t.Fatalf("expected resume picker to be enabled")
	}
	if parsed.resumeID != "" {
		t.Fatalf("expected empty resume id, got %q", parsed.resumeID)
	}
	if !parsed.yoloMode {
		t.Fatalf("expected yolo flag to be preserved")
	}
}

func TestParseDirectLaunchArgs_ResumeIDStillAccepted(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--resume", "abc123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.resumePicker {
		t.Fatalf("expected resume picker to be disabled")
	}
	if parsed.resumeID != "abc123" {
		t.Fatalf("expected resume id abc123, got %q", parsed.resumeID)
	}
}

func TestParseDirectLaunchArgs_LabelAndYolo(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"-s", "my-label", "--yolo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.label != "my-label" {
		t.Fatalf("expected label my-label, got %q", parsed.label)
	}
	if !parsed.yoloMode {
		t.Fatal("expected yolo mode to be enabled")
	}
}

// A member launch names who the session is. The label follows the member, so a
// member's day is titled after it without anybody typing -s.
func TestParseDirectLaunchArgs_MemberNamesTheSession(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" {
		t.Fatalf("member = %q, want trellis", parsed.member)
	}
	// The label is the display side of the same member: a name, while the id
	// the binding takes stays lowercase.
	if parsed.label != "Trellis" {
		t.Fatalf("label = %q, want the member's name", parsed.label)
	}
}

// -s still wins: the member decides identity, the label decides display.
func TestParseDirectLaunchArgs_LabelOverridesTheMemberName(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis", "-s", "crew slice 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" || parsed.label != "crew slice 1" {
		t.Fatalf("member/label = %q/%q, want trellis/crew slice 1", parsed.member, parsed.label)
	}
}

// parseDirectLaunchArgs understands only -s/--resume/--yolo/--member.
// Everything else is rejected rather than silently forwarded to the agent.
func TestParseDirectLaunchArgs_RejectsUnrecognizedArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "foo"}, // arbitrary agent flag
		{"--"},             // the old passthrough separator
		{"--help"},         // must not reach the agent
		{"random"},         // bare positional
		{"-s"},             // missing label value
		{"--member"},       // missing member value
	} {
		if _, err := parseDirectLaunchArgs(args); err == nil {
			t.Fatalf("expected error for args %#v, got nil", args)
		}
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

func TestDetectPresence(t *testing.T) {
	t.Run("outside attn", func(t *testing.T) {
		t.Setenv("ATTN_INSIDE_APP", "")
		t.Setenv("ATTN_SESSION_ID", "stale-session")

		sessionID, present := detectPresence()
		if present || sessionID != "" {
			t.Fatalf("detectPresence() = (%q, %v), want empty session and false", sessionID, present)
		}
	})

	t.Run("inside attn", func(t *testing.T) {
		t.Setenv("ATTN_INSIDE_APP", "1")
		t.Setenv("ATTN_SESSION_ID", " session-1 ")

		sessionID, present := detectPresence()
		if !present || sessionID != "session-1" {
			t.Fatalf("detectPresence() = (%q, %v), want session-1 and true", sessionID, present)
		}
	})
}

func TestParseDirectLaunchArgs_InitialPromptFile(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--initial-prompt-file", "/tmp/brief.md"})
	if err != nil {
		t.Fatalf("parseDirectLaunchArgs() error = %v", err)
	}
	if parsed.initialPromptFile != "/tmp/brief.md" {
		t.Fatalf("initialPromptFile = %q, want /tmp/brief.md", parsed.initialPromptFile)
	}
}

func TestConsumeOneShotBoolEnvRemovesLaunchCapability(t *testing.T) {
	t.Setenv("ATTN_TEST_ONE_SHOT", "1")
	if !consumeOneShotBoolEnv("ATTN_TEST_ONE_SHOT") {
		t.Fatal("consumeOneShotBoolEnv() = false, want true")
	}
	if _, ok := os.LookupEnv("ATTN_TEST_ONE_SHOT"); ok {
		t.Fatal("one-shot launch capability remained in the environment")
	}
}

func TestConsumeOneShotEnvRemovesLaunchPin(t *testing.T) {
	t.Setenv("ATTN_TEST_ONE_SHOT_VALUE", " pinned ")
	if got := consumeOneShotEnv("ATTN_TEST_ONE_SHOT_VALUE"); got != "pinned" {
		t.Fatalf("consumeOneShotEnv() = %q, want pinned", got)
	}
	if _, ok := os.LookupEnv("ATTN_TEST_ONE_SHOT_VALUE"); ok {
		t.Fatal("one-shot launch pin remained in the environment")
	}
}

func TestReadInitialPromptFileRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(path, []byte("delegated brief"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	prompt, err := readInitialPromptFile(path)
	if err != nil {
		t.Fatalf("readInitialPromptFile() error = %v", err)
	}
	if prompt != "delegated brief" {
		t.Fatalf("prompt = %q", prompt)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("prompt file still exists: %v", err)
	}
}

func TestWorkspaceContextCheckoutPathReturnsCheckoutPath(t *testing.T) {
	c := &fakeWorkspaceContextCheckoutClient{
		failures: 1,
		path:     "/tmp/context.md",
	}
	path, err := workspaceContextCheckoutPath(c, "session-1", 2, 0)
	if err != nil {
		t.Fatalf("workspaceContextCheckoutPath error: %v", err)
	}
	if path != "/tmp/context.md" {
		t.Fatalf("path = %q, want /tmp/context.md", path)
	}
}

func TestWorkspaceContextGuidanceProvidedAtLaunch(t *testing.T) {
	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "developer_instructions")
	t.Setenv("ATTN_CHIEF_GUIDANCE", "")
	if !workspaceContextGuidanceProvidedAtLaunch() {
		t.Fatal("workspace launch guidance should suppress hook guidance output")
	}

	// A chief launch injects chief guidance (not workspace context); its
	// marker must equally suppress the SessionStart hook's workspace guidance.
	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "")
	t.Setenv("ATTN_CHIEF_GUIDANCE", "append_system_prompt")
	if !workspaceContextGuidanceProvidedAtLaunch() {
		t.Fatal("chief launch guidance should suppress hook guidance output")
	}

	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "")
	t.Setenv("ATTN_CHIEF_GUIDANCE", "")
	if workspaceContextGuidanceProvidedAtLaunch() {
		t.Fatal("missing launch guidance should preserve hook fallback output")
	}
}

type fakeNotebookGuideClient struct {
	result *protocol.NotebookGuideResult
	err    error
	gotID  string
}

func (f *fakeNotebookGuideClient) NotebookGuide(sessionID string) (*protocol.NotebookGuideResult, error) {
	f.gotID = sessionID
	return f.result, f.err
}

func TestResolveChiefNotebookRoot(t *testing.T) {
	t.Run("chief returns root", func(t *testing.T) {
		c := &fakeNotebookGuideClient{result: &protocol.NotebookGuideResult{Root: "/nb", SessionIsChief: true}}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "/nb" {
			t.Fatalf("root = %q, want /nb", got)
		}
		if c.gotID != "s1" {
			t.Fatalf("session id = %q, want s1", c.gotID)
		}
	})
	t.Run("non-chief returns empty", func(t *testing.T) {
		c := &fakeNotebookGuideClient{result: &protocol.NotebookGuideResult{Root: "/nb", SessionIsChief: false}}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "" {
			t.Fatalf("root = %q, want empty for non-chief", got)
		}
	})
	t.Run("lookup error returns empty (falls back to workspace context)", func(t *testing.T) {
		c := &fakeNotebookGuideClient{err: errors.New("daemon down")}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "" {
			t.Fatalf("root = %q, want empty on error", got)
		}
	})
}

func TestWorkspaceContextSessionStartOutputReturnsLastCheckoutError(t *testing.T) {
	c := &fakeWorkspaceContextCheckoutClient{failures: 2}
	output, err := workspaceContextSessionStartOutput(c, "session-1", 2, 0)
	if err == nil || !strings.Contains(err.Error(), "source session not found") {
		t.Fatalf("workspaceContextSessionStartOutput error = %v", err)
	}
	if output != "" {
		t.Fatalf("hook output = %q, want empty", output)
	}
}

func TestParseDelegateArgsDefaultsToCurrentWorkspace(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "source-session")

	parsed, err := parseDelegateArgs([]string{"--brief", "Investigate this"})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.sourceSessionID != "source-session" || parsed.brief != "Investigate this" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if parsed.options.Placement != "current_workspace" {
		t.Fatalf("placement = %q", parsed.options.Placement)
	}
	if parsed.options.NoWorktree {
		t.Fatal("plain delegation unexpectedly disabled the default worktree")
	}
}

func TestParseDelegateArgsAdoptsTicket(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--ticket", " planned-work ",
		"--confirm",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.brief != "" || parsed.options.TicketID != "planned-work" || !parsed.options.Confirm {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseDelegateArgsRejectsMultipleTaskSources(t *testing.T) {
	for _, args := range [][]string{
		{"--brief", "text", "--ticket", "planned"},
		{"--brief-file", "brief.md", "--ticket", "planned"},
		{"--brief", "text", "--brief-file", "brief.md"},
	} {
		_, err := parseDelegateArgs(append([]string{"--source-session", "source-session"}, args...))
		if err == nil || !strings.Contains(err.Error(), "pass only one") {
			t.Fatalf("parseDelegateArgs(%v) error = %v", args, err)
		}
	}
}

func TestParseDelegateArgsConfirmRequiresTicket(t *testing.T) {
	_, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "text",
		"--confirm",
	})
	if err == nil || !strings.Contains(err.Error(), "--confirm requires --ticket") {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
}

func TestParseDelegateArgsNoWorktree(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Continue in this checkout",
		"--no-worktree",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if !parsed.options.NoWorktree {
		t.Fatal("options.NoWorktree = false, want true")
	}
}

func TestParseDelegateArgsWorkspaceNoWorktreeKeepsConceptsSeparate(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Join this workspace from my current checkout",
		"--workspace", "workspace-mixed",
		"--no-worktree",
		"--allow-worktree-reuse",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Placement != "existing_workspace" || parsed.options.WorkspaceID != "workspace-mixed" || !parsed.options.NoWorktree || !parsed.options.AllowWorktreeReuse {
		t.Fatalf("options = %+v", parsed.options)
	}
}

func TestParseDelegateArgsRejectsNoWorktreeOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"--no-worktree", "--worktree", "feat/parser"},
		{"--no-worktree", "--repo", "/tmp/repo"},
		{"--no-worktree", "--from", "main"},
		{"--no-worktree", "--worktree-path", "/tmp/worktree"},
	} {
		_, err := parseDelegateArgs(append([]string{
			"--source-session", "source-session",
			"--brief", "Conflicting placement",
		}, args...))
		if err == nil {
			t.Fatalf("parseDelegateArgs(%v) error = %v", args, err)
		}
	}
}

func TestParseDelegateArgsNameSetsLabel(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Investigate this",
		"--name", "  launcher  ",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Label != "launcher" {
		t.Fatalf("options.Label = %q, want %q", parsed.options.Label, "launcher")
	}
}

func TestParseDelegateArgsModelAndEffort(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Investigate this",
		"--model", " claude-fable-5 ",
		"--effort", " low ",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Model != "claude-fable-5" || parsed.options.Effort != "low" {
		t.Fatalf("options model/effort = %q/%q", parsed.options.Model, parsed.options.Effort)
	}
}

func TestParseDelegateArgsWorktreeUsesCurrentWorkspace(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Implement the parser",
		"--agent", "codex",
		"--worktree", "feat/parser",
		"--repo", "/tmp/repo",
		"--from", "main",
		"--worktree-path", "/tmp/repo--feat-parser",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Placement != "current_workspace" ||
		parsed.options.Agent != "codex" ||
		parsed.options.Worktree != "feat/parser" ||
		parsed.options.WorktreeRepo != "/tmp/repo" ||
		parsed.options.StartingFrom != "main" ||
		parsed.options.WorktreePath != "/tmp/repo--feat-parser" {
		t.Fatalf("options = %+v", parsed.options)
	}
}

func TestParseDelegateArgsWorktreeUsesExplicitNewWorkspace(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Implement the parser",
		"--new-workspace",
		"--worktree", "feat/parser",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Placement != "new_workspace" ||
		parsed.options.Worktree != "feat/parser" {
		t.Fatalf("options = %+v", parsed.options)
	}
}

func TestParseDelegateArgsRejectsAmbiguousPlacement(t *testing.T) {
	for _, args := range [][]string{
		{"--workspace", "workspace-target", "--new-workspace"},
		{"--workspace", "workspace-target", "--cwd", "/some/dir"},
	} {
		_, err := parseDelegateArgs(append([]string{
			"--source-session", "source-session",
			"--brief", "Investigate this",
		}, args...))
		if err == nil || !strings.Contains(err.Error(), "--workspace cannot be combined") {
			t.Fatalf("parseDelegateArgs(%v) error = %v", args, err)
		}
	}
}

func TestParseDelegateArgsAcceptsWorkspaceWithWorktree(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Work in an existing workspace with a worktree",
		"--workspace", "workspace-target",
		"--worktree", "feat/parser",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs error = %v", err)
	}
	if parsed.options.Placement != "existing_workspace" ||
		parsed.options.WorkspaceID != "workspace-target" ||
		parsed.options.Worktree != "feat/parser" {
		t.Fatalf("options = %+v", parsed.options)
	}
}

// Dispatch-at-plot is a placement-independent aim: it says what the delegate
// tends, not where it runs, so it composes with every placement.
func TestParseDelegateArgsAcceptsAPlot(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Tend this plot",
		"--plot", "s-7k3f9m",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs error = %v", err)
	}
	if parsed.options.Plot != "s-7k3f9m" || parsed.options.Placement != "current_workspace" {
		t.Fatalf("options = %+v", parsed.options)
	}

	bare, err := parseDelegateArgs([]string{
		"--source-session", "source-session", "--brief", "Just work",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs error = %v", err)
	}
	if bare.options.Plot != "" {
		t.Fatalf("a delegation with no --plot was aimed at %q", bare.options.Plot)
	}
}

func TestParseDelegateArgsAcceptsCwdWithWorktree(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{
		"--source-session", "source-session",
		"--brief", "Work in a worktree of the repo at this directory",
		"--cwd", "/some/repo",
		"--worktree", "feat/parser",
	})
	if err != nil {
		t.Fatalf("parseDelegateArgs() error = %v", err)
	}
	if parsed.options.Placement != "new_workspace" ||
		parsed.options.CWD != "/some/repo" ||
		parsed.options.Worktree != "feat/parser" {
		t.Fatalf("options = %+v", parsed.options)
	}
}

func TestWorkspaceContextSourceSessionDefaultsToEnvironment(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "session-1")
	sessionID, force, err := workspaceContextSourceSession([]string{"--force"}, true)
	if err != nil {
		t.Fatalf("workspaceContextSourceSession error: %v", err)
	}
	if sessionID != "session-1" || !force {
		t.Fatalf("workspaceContextSourceSession = (%q, %v)", sessionID, force)
	}
}

func TestWorkspaceContextSourceSessionRejectsForceForStatus(t *testing.T) {
	_, _, err := workspaceContextSourceSession([]string{"--session", "session-1", "--force"}, false)
	if err == nil || !strings.Contains(err.Error(), "--force is only valid") {
		t.Fatalf("workspaceContextSourceSession error = %v", err)
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

func TestParseOpenArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantPath    string
		wantSession string
		wantErr     bool
	}{
		// The documented `attn open <file.md> [--session <id>]` trailing form
		// must honor --session — this is the regression the reviewers flagged:
		// Go's flag parser stops at the first positional, so a naive Parse would
		// silently drop a trailing --session.
		{name: "session after path", args: []string{"README.md", "--session", "sess-1"}, wantPath: "README.md", wantSession: "sess-1"},
		{name: "session=after path", args: []string{"README.md", "--session=sess-2"}, wantPath: "README.md", wantSession: "sess-2"},
		{name: "session before path", args: []string{"--session", "sess-3", "README.md"}, wantPath: "README.md", wantSession: "sess-3"},
		{name: "path only", args: []string{"README.md"}, wantPath: "README.md", wantSession: ""},
		{name: "no path", args: []string{"--session", "sess-4"}, wantErr: true},
		{name: "empty", args: []string{}, wantErr: true},
		{name: "extra positional", args: []string{"a.md", "b.md"}, wantErr: true},
		{name: "unknown flag", args: []string{"README.md", "--nope"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, session, err := parseOpenArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseOpenArgs(%v) = (%q, %q, nil), want error", tc.args, path, session)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOpenArgs(%v) error = %v", tc.args, err)
			}
			if path != tc.wantPath || session != tc.wantSession {
				t.Fatalf("parseOpenArgs(%v) = (%q, %q), want (%q, %q)", tc.args, path, session, tc.wantPath, tc.wantSession)
			}
		})
	}
}

// TestStopFacts locks what the Stop hook reports to the daemon. The payloads are
// real Claude Code Stop-hook stdin bodies (trimmed to the fields attn parses),
// captured from live background Workflow runs and CronCreate/CronDelete probes.
// Statuses pass through verbatim — the rule that reads them lives in the daemon
// (see nonTerminalStopState there), so the hook's job is extraction only.
func TestStopFacts(t *testing.T) {
	cases := []struct {
		name         string
		payload      string
		wantStatuses []string
		wantCrons    int
	}{
		{
			name:         "workflow running (parent yields mid-run)",
			payload:      `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}`,
			wantStatuses: []string{"running"},
		},
		{
			name:         "workflow plus background shells running",
			payload:      `{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}`,
			wantStatuses: []string{"running", "running", "running"},
		},
		{
			name:    "empty background_tasks (workflow finished)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}`,
		},
		{
			name:    "fields absent (e.g. another agent)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
		},
		{
			name:         "task present but not running is still reported",
			payload:      `{"background_tasks":[{"type":"workflow","status":"completed"}]}`,
			wantStatuses: []string{"completed"},
		},
		{
			name:      "recurring cron pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo persist-probe"}]}`,
			wantCrons: 1,
		},
		{
			name:      "one-shot reminder pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"session_crons":[{"id":"5e9a0f21","schedule":"18 14 * * *","recurring":false,"prompt":"echo oneshot-fired"}]}`,
			wantCrons: 1,
		},
		{
			name:      "recurring plus one-shot pending",
			payload:   `{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}`,
			wantCrons: 2,
		},
		{
			name:         "background running and cron pending are reported together",
			payload:      `{"background_tasks":[{"type":"shell","status":"running"}],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo x"}]}`,
			wantStatuses: []string{"running"},
			wantCrons:    1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			facts := stopFacts(input)
			if !slices.Equal(facts.BackgroundTaskStatuses, tc.wantStatuses) {
				t.Fatalf("BackgroundTaskStatuses = %q, want %q", facts.BackgroundTaskStatuses, tc.wantStatuses)
			}
			if facts.PendingSessionCrons != tc.wantCrons {
				t.Fatalf("PendingSessionCrons = %d, want %d", facts.PendingSessionCrons, tc.wantCrons)
			}
		})
	}
}

// parseTicketStatusArgs must accept flags on either side of the work-state
// positional. Go's flag parser stops at the first positional, so the regression
// here is the documented `ticket status <state> --comment ...` form (flags after
// the state) silently dropping the flags.
func TestParseTicketStatusArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketStatusArgs
	}{
		{
			name: "flags after state (the documented form)",
			args: []string{"ready_for_review", "--comment", "ready for a look", "--session", "s1"},
			want: ticketStatusArgs{WorkState: "ready_for_review", Comment: "ready for a look", Session: "s1"},
		},
		{
			name: "flags before state",
			args: []string{"--session", "s1", "--comment", "wip", "in_progress"},
			want: ticketStatusArgs{WorkState: "in_progress", Comment: "wip", Session: "s1"},
		},
		{
			name: "flags around state",
			args: []string{"--session", "s1", "completed", "--json"},
			want: ticketStatusArgs{WorkState: "completed", Session: "s1", JSON: true},
		},
		{
			name: "bare state",
			args: []string{"failed"},
			want: ticketStatusArgs{WorkState: "failed"},
		},
		{
			name: "--ticket before the positional",
			args: []string{"--ticket", "store-migration", "ready_for_review"},
			want: ticketStatusArgs{WorkState: "ready_for_review", TicketID: "store-migration"},
		},
		{
			name: "--ticket after the positional",
			args: []string{"in_progress", "--ticket", "store-migration", "--comment", "moving it along"},
			want: ticketStatusArgs{WorkState: "in_progress", TicketID: "store-migration", Comment: "moving it along"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketStatusArgs(tc.args)
			if err != nil {
				t.Fatalf("parseTicketStatusArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketStatusArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketStatusArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"no state":     {"--comment", "hi"},
		"two states":   {"working", "failed"},
		"unknown flag": {"working", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketStatusArgs(args); err == nil {
				t.Fatalf("parseTicketStatusArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseTicketAttachArgs(t *testing.T) {
	got, err := parseTicketAttachArgs([]string{"--file", "report.md", "--file", "rollout.md", "--ticket", "tk", "--state", "ready_for_review", "--comment", "for review", "--session", "s1", "--json"})
	if err != nil {
		t.Fatalf("parseTicketAttachArgs: %v", err)
	}
	want := ticketAttachArgs{Files: []string{"report.md", "rollout.md"}, Ticket: "tk", State: "ready_for_review", Comment: "for review", Session: "s1", JSON: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTicketAttachArgs = %+v, want %+v", got, want)
	}
}

func TestParseTicketAttachArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"missing file":          {"--comment", "hi"},
		"unexpected positional": {"--file", "a.md", "extra"},
		"unknown flag":          {"--file", "a.md", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketAttachArgs(args); err == nil {
				t.Fatalf("parseTicketAttachArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseTicketNewArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketNewArgs
	}{
		{
			name: "title only",
			args: []string{"--title", "Migrate store to X"},
			want: ticketNewArgs{Title: "Migrate store to X"},
		},
		{
			name: "title, description, and id",
			args: []string{"--title", "Migrate store", "--description", "the brief", "--id", "store-migration"},
			want: ticketNewArgs{Title: "Migrate store", Description: "the brief", ID: "store-migration"},
		},
		{
			name: "json",
			args: []string{"--title", "Migrate store", "--json"},
			want: ticketNewArgs{Title: "Migrate store", JSON: true},
		},
		{
			name: "session",
			args: []string{"--title", "Migrate store", "--session", "s1"},
			want: ticketNewArgs{Title: "Migrate store", Session: "s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketNewArgs(tc.args)
			if err != nil {
				t.Fatalf("parseTicketNewArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketNewArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketNewArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"missing title":         {"--description", "hi"},
		"unknown flag":          {"--title", "x", "--bogus"},
		"unexpected positional": {"--title", "x", "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketNewArgs(args); err == nil {
				t.Fatalf("parseTicketNewArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseTicketCommentArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketCommentArgs
	}{
		{
			name: "id and message",
			args: []string{"store-migration", "--message", "lgtm"},
			want: ticketCommentArgs{TicketID: "store-migration", Comment: "lgtm"},
		},
		{
			name: "short -m flag",
			args: []string{"tk", "-m", "looks good"},
			want: ticketCommentArgs{TicketID: "tk", Comment: "looks good"},
		},
		{
			// The footgun the design avoids: flags written AFTER the id still parse,
			// because the interleave parser peels the id and keeps parsing — a
			// trailing --session would otherwise be swallowed into the comment.
			name: "flags after the id still parse",
			args: []string{"tk", "-m", "the note", "--session", "s1", "--json"},
			want: ticketCommentArgs{TicketID: "tk", Comment: "the note", Session: "s1", JSON: true},
		},
		{
			name: "flags before the id",
			args: []string{"--session", "s1", "-m", "the note", "tk"},
			want: ticketCommentArgs{TicketID: "tk", Comment: "the note", Session: "s1"},
		},
		{
			// Dashes inside the quoted message value are safe — it is a flag value, not
			// re-parsed as flags.
			name: "dashes inside the message are literal",
			args: []string{"tk", "-m", "--watch out for the race"},
			want: ticketCommentArgs{TicketID: "tk", Comment: "--watch out for the race"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketCommentArgs(tc.args)
			if err != nil {
				t.Fatalf("parseTicketCommentArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketCommentArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketCommentArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":            {},
		"id without message": {"tk"},
		"message without id": {"-m", "hi"},
		"two positionals":    {"tk", "extra", "-m", "hi"},
		"unknown flag":       {"--bogus", "tk", "-m", "hi"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketCommentArgs(args); err == nil {
				t.Fatalf("parseTicketCommentArgs(%v) = nil error, want error", args)
			}
		})
	}
}

// The common mistake `comment tk "looks good"` (comment as a bare positional,
// no -m) should produce an error that points at -m, not the opaque "got 2".
func TestParseTicketCommentArgsBareCommentHintsMessageFlag(t *testing.T) {
	_, err := parseTicketCommentArgs([]string{"tk", "looks good"})
	if err == nil {
		t.Fatal("parseTicketCommentArgs with bare comment = nil error, want error")
	}
	if !strings.Contains(err.Error(), "-m") {
		t.Fatalf("error = %q, want it to mention -m", err.Error())
	}
}

// parseTicketIDArgs (subscribe/unsubscribe) takes exactly one id positional with the
// session/json flags interleavable on either side.
func TestParseTicketIDArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketIDArgs
	}{
		{name: "id only", args: []string{"tk"}, want: ticketIDArgs{TicketID: "tk"}},
		{
			name: "flags after id",
			args: []string{"tk", "--session", "s1", "--json"},
			want: ticketIDArgs{TicketID: "tk", Session: "s1", JSON: true},
		},
		{
			name: "flags before id",
			args: []string{"--session", "s1", "tk"},
			want: ticketIDArgs{TicketID: "tk", Session: "s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketIDArgs("ticket subscribe", tc.args)
			if err != nil {
				t.Fatalf("parseTicketIDArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketIDArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketIDArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":         {},
		"two positionals": {"tk", "extra"},
		"unknown flag":    {"tk", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketIDArgs("ticket subscribe", args); err == nil {
				t.Fatalf("parseTicketIDArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseTicketTakeArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketTakeArgs
	}{
		{name: "id only", args: []string{"tk"}, want: ticketTakeArgs{TicketID: "tk"}},
		{
			name: "confirm before id",
			args: []string{"--confirm", "tk"},
			want: ticketTakeArgs{TicketID: "tk", Confirm: true},
		},
		{
			name: "all flags after id",
			args: []string{"tk", "--confirm", "--session", "s1", "--json"},
			want: ticketTakeArgs{TicketID: "tk", Session: "s1", Confirm: true, JSON: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketTakeArgs(tc.args)
			if err != nil {
				t.Fatalf("parseTicketTakeArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketTakeArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketTakeArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":         {},
		"two positionals": {"tk", "extra"},
		"unknown flag":    {"tk", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketTakeArgs(args); err == nil {
				t.Fatalf("parseTicketTakeArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseJournalAppendArgsEntryAndFileMutuallyExclusive(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "entry.md")
	if err := os.WriteFile(tmp, []byte("from file"), 0o644); err != nil {
		t.Fatalf("write temp entry file: %v", err)
	}
	_, err := parseJournalAppendArgs([]string{"--entry", "inline", "--entry-file", tmp})
	if err == nil || !strings.Contains(err.Error(), "only one of --entry or --entry-file") {
		t.Fatalf("parseJournalAppendArgs() error = %v, want mutually-exclusive error", err)
	}
}

func TestParseJournalAppendArgsMissingEntryErrors(t *testing.T) {
	_, err := parseJournalAppendArgs(nil)
	if err == nil || !strings.Contains(err.Error(), "--entry or --entry-file is required") {
		t.Fatalf("parseJournalAppendArgs() error = %v, want missing-entry error", err)
	}
}

func TestParseJournalAppendArgsEntryFileReadsAndTrims(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "entry.md")
	if err := os.WriteFile(tmp, []byte("  from file  \n"), 0o644); err != nil {
		t.Fatalf("write temp entry file: %v", err)
	}
	parsed, err := parseJournalAppendArgs([]string{"--entry-file", tmp})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.entry != "from file" {
		t.Fatalf("entry = %q, want %q", parsed.entry, "from file")
	}
}

func TestParseJournalAppendArgsDatePassthrough(t *testing.T) {
	parsed, err := parseJournalAppendArgs([]string{"--entry", "hi", "--date", "2026-07-05", "--session", "s1", "--json"})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.date != "2026-07-05" || parsed.sessionID != "s1" || !parsed.jsonOut || parsed.entry != "hi" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseJournalAppendArgsSessionDefaultsToEnv(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "env-session")
	parsed, err := parseJournalAppendArgs([]string{"--entry", "hi"})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.sessionID != "env-session" {
		t.Fatalf("sessionID = %q, want env-session", parsed.sessionID)
	}
}
