package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
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

// parseDirectLaunchArgs understands only -s/--resume/--yolo. Everything else is
// rejected rather than silently forwarded to the underlying agent.
func TestParseDirectLaunchArgs_RejectsUnrecognizedArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "foo"}, // arbitrary agent flag
		{"--"},             // the old passthrough separator
		{"--help"},         // must not reach the agent
		{"random"},         // bare positional
		{"-s"},             // missing label value
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

func TestWorkspaceContextSessionStartOutputRetriesUntilSessionIsRegistered(t *testing.T) {
	c := &fakeWorkspaceContextCheckoutClient{
		failures: 2,
		path:     "/tmp/context.md",
	}
	output, err := workspaceContextSessionStartOutput(c, "session-1", 3, 0)
	if err != nil {
		t.Fatalf("workspaceContextSessionStartOutput error: %v", err)
	}
	if c.calls != 3 {
		t.Fatalf("checkout calls = %d, want 3", c.calls)
	}
	if !strings.Contains(output, "/tmp/context.md") {
		t.Fatalf("hook output = %q", output)
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
	t.Setenv("ATTN_NOTEBOOK_GUIDANCE", "")
	if !workspaceContextGuidanceProvidedAtLaunch() {
		t.Fatal("workspace launch guidance should suppress hook guidance output")
	}

	// A chief launch injects Notebook guidance (not workspace context); its
	// marker must equally suppress the SessionStart hook's workspace guidance.
	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "")
	t.Setenv("ATTN_NOTEBOOK_GUIDANCE", "append_system_prompt")
	if !workspaceContextGuidanceProvidedAtLaunch() {
		t.Fatal("notebook launch guidance should suppress hook guidance output")
	}

	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "")
	t.Setenv("ATTN_NOTEBOOK_GUIDANCE", "")
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

func TestParseNotebookJournalArgs(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got, err := parseNotebookJournalArgs([]string{"append", "--text", "hi", "--date", "2026-06-13"})
		if err != nil || got.text != "hi" || got.date != "2026-06-13" {
			t.Fatalf("got %+v err %v", got, err)
		}
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wrong subverb", []string{"appendx", "--text", "hi"}},
		{"missing subverb", []string{"--text", "hi"}},
		{"missing text", []string{"append", "--date", "2026-06-13"}},
		{"empty text", []string{"append", "--text", "   "}},
		{"stray positional", []string{"append", "--text", "hi", "extra"}},
		{"unknown flag", []string{"append", "--nope", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseNotebookJournalArgs(tc.args); err == nil {
				t.Fatalf("args %v: expected error", tc.args)
			}
		})
	}
}

func TestPrintDreamStatus(t *testing.T) {
	var buf bytes.Buffer
	printDreamStatus(&buf, &protocol.NotebookDreamStatusResult{
		Enabled:           false,
		CandidateCount:    3,
		MultiContextCount: 1,
		PersistedCount:    2,
		Schedule:          protocol.Ptr("0 3 * * *"),
		Timezone:          protocol.Ptr("America/New_York"),
		NextRunAt:         protocol.Ptr("2026-06-15T07:00:00Z"),
		LastRunAt:         protocol.Ptr("2026-06-14T07:00:00Z"),
		SourceCounts: []protocol.NotebookDreamSourceCount{
			{Source: "context", Count: 1},
			{Source: "journal", Count: 2},
		},
		Top: []protocol.NotebookDreamCandidate{
			{Source: "context", Title: protocol.Ptr("ws-a"), Snippet: "Daemon owns every notebook write.", Occurrences: 2, Contexts: []string{"workspace:ws-a", "workspace:ws-b"}},
		},
	})
	out := buf.String()
	for _, want := range []string{
		"dreaming: disabled",
		"schedule: 0 3 * * * (America/New_York), next run ",
		"last run: ",
		"candidates: 3 (1 across multiple contexts), 2 persisted",
		"context:", "journal:", "[2× ·2 ctx]", "Daemon owns every notebook write.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintDreamRunPreviewOnly(t *testing.T) {
	var buf bytes.Buffer
	printDreamRun(&buf, &protocol.NotebookDreamRunResult{
		Applied:           false,
		CandidateCount:    1,
		MultiContextCount: 0,
		SourceCounts:      []protocol.NotebookDreamSourceCount{{Source: "journal", Count: 1}},
		Candidates: []protocol.NotebookDreamCandidate{
			{Source: "journal", Snippet: "A durable fact.", Occurrences: 1, Contexts: []string{"journal:2026-06-13"}},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "preview only, nothing written") {
		t.Fatalf("run output should state it is preview-only:\n%s", out)
	}
	if !strings.Contains(out, "A durable fact.") {
		t.Fatalf("run output missing the candidate snippet:\n%s", out)
	}
}

func TestParseNotebookMemoryArgs(t *testing.T) {
	t.Run("create (no base-hash, stdin)", func(t *testing.T) {
		got, err := parseNotebookMemoryArgs([]string{"write", "--path", "/memory/decisions/x.md"})
		if err != nil || got.path != "/memory/decisions/x.md" || got.baseHash != "" || got.file != "" {
			t.Fatalf("got %+v err %v", got, err)
		}
	})
	t.Run("edit with base-hash and file", func(t *testing.T) {
		got, err := parseNotebookMemoryArgs([]string{"write", "--path", "/m/x.md", "--base-hash", "abc", "--file", "/tmp/x"})
		if err != nil || got.baseHash != "abc" || got.file != "/tmp/x" {
			t.Fatalf("got %+v err %v", got, err)
		}
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wrong subverb", []string{"writex", "--path", "/m/x.md"}},
		{"missing subverb", []string{"--path", "/m/x.md"}},
		{"missing path", []string{"write", "--base-hash", "abc"}},
		{"empty path", []string{"write", "--path", "  "}},
		{"stray positional", []string{"write", "--path", "/m/x.md", "extra"}},
		{"unknown flag", []string{"write", "--nope", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseNotebookMemoryArgs(tc.args); err == nil {
				t.Fatalf("args %v: expected error", tc.args)
			}
		})
	}
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
		{"--workspace", "workspace-target", "--worktree", "feat/parser"},
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

func TestParseDispatchReportArgsReadsFile(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "worker-1")
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("Implemented the fix.\nTests pass.\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	sessionID, report, structuredReport, err := parseDispatchReportArgs([]string{"--file", path})
	if err != nil {
		t.Fatalf("parseDispatchReportArgs() error = %v", err)
	}
	if sessionID != "worker-1" || report != "Implemented the fix.\nTests pass." || structuredReport != nil {
		t.Fatalf("dispatch report = (%q, %q, %+v)", sessionID, report, structuredReport)
	}
}

func TestParseDispatchReportArgsRejectsAmbiguousContent(t *testing.T) {
	_, _, _, err := parseDispatchReportArgs([]string{
		"--session", "worker-1",
		"--message", "done",
		"--file", "/tmp/report.md",
	})
	if err == nil || !strings.Contains(err.Error(), "only one of --message or --file") {
		t.Fatalf("parseDispatchReportArgs() error = %v", err)
	}
}

func TestParseDispatchReportArgsReadsCoordinationFile(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "worker-1")
	path := filepath.Join(t.TempDir(), "coordination.json")
	if err := os.WriteFile(path, []byte(`{
		"report_type": "blocker",
		"summary": "Core implementation ready locally",
		"work_state": "needs_input",
		"next_actor": "team",
		"request": {
			"question": "Which event contract should be used?",
			"expected_responder": "team",
			"status": "pending"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write coordination file: %v", err)
	}
	sessionID, report, structured, err := parseDispatchReportArgs([]string{
		"--message", "Waiting for the event contract decision.",
		"--coordination-file", path,
	})
	if err != nil {
		t.Fatalf("parseDispatchReportArgs() error = %v", err)
	}
	if sessionID != "worker-1" || report != "Waiting for the event contract decision." {
		t.Fatalf("dispatch report = (%q, %q)", sessionID, report)
	}
	if structured == nil ||
		structured.ReportType != protocol.DispatchReportTypeBlocker ||
		structured.WorkState != protocol.DispatchWorkStateNeedsInput ||
		structured.Request == nil {
		t.Fatalf("structured report = %+v", structured)
	}
}

func TestParseDispatchResolveArgsReadsResponseFile(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "chief-1")
	path := filepath.Join(t.TempDir(), "response.md")
	if err := os.WriteFile(path, []byte("Use AisNoOperationV1.\n"), 0o600); err != nil {
		t.Fatalf("write response file: %v", err)
	}
	sessionID, dispatchID, response, link, err := parseDispatchResolveArgs([]string{
		"--dispatch", "dispatch-1",
		"--file", path,
		"--link", "https://example.test/decision",
	})
	if err != nil {
		t.Fatalf("parseDispatchResolveArgs() error = %v", err)
	}
	if sessionID != "chief-1" ||
		dispatchID != "dispatch-1" ||
		response != "Use AisNoOperationV1." ||
		link != "https://example.test/decision" {
		t.Fatalf("dispatch resolve = (%q, %q, %q, %q)", sessionID, dispatchID, response, link)
	}
}

func TestParseDispatchMessageArgsReadsFile(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "chief-1")
	path := filepath.Join(t.TempDir(), "message.md")
	if err := os.WriteFile(path, []byte("Re-check the current branch.\n"), 0o600); err != nil {
		t.Fatalf("write message: %v", err)
	}
	sessionID, dispatchID, message, err := parseDispatchMessageArgs([]string{
		"--dispatch", "dispatch-1",
		"--file", path,
	})
	if err != nil {
		t.Fatalf("parseDispatchMessageArgs() error = %v", err)
	}
	if sessionID != "chief-1" || dispatchID != "dispatch-1" || message != "Re-check the current branch." {
		t.Fatalf("dispatch message = (%q, %q, %q)", sessionID, dispatchID, message)
	}
}

func TestParseDispatchInboxArgsDefaultsToCurrentSession(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "worker-1")
	sessionID, unreadOnly, err := parseDispatchInboxArgs([]string{"--unread"})
	if err != nil {
		t.Fatalf("parseDispatchInboxArgs() error = %v", err)
	}
	if sessionID != "worker-1" || !unreadOnly {
		t.Fatalf("dispatch inbox = (%q, %t)", sessionID, unreadOnly)
	}
}

func TestParseDispatchMessagesArgsRequiresDispatch(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "chief-1")
	sessionID, dispatchID, err := parseDispatchMessagesArgs([]string{"--dispatch", "dispatch-1"})
	if err != nil {
		t.Fatalf("parseDispatchMessagesArgs() error = %v", err)
	}
	if sessionID != "chief-1" || dispatchID != "dispatch-1" {
		t.Fatalf("dispatch messages = (%q, %q)", sessionID, dispatchID)
	}
}

func TestParseDispatchAckArgsReadsAcknowledgement(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "worker-1")
	sessionID, messageID, acknowledgement, err := parseDispatchAckArgs([]string{
		"--message-id", "message-1",
		"--message", "Re-check complete.",
	})
	if err != nil {
		t.Fatalf("parseDispatchAckArgs() error = %v", err)
	}
	if sessionID != "worker-1" || messageID != "message-1" || acknowledgement != "Re-check complete." {
		t.Fatalf("dispatch ack = (%q, %q, %q)", sessionID, messageID, acknowledgement)
	}
}

func TestWriteHelpMentionsPresenceAndOpen(t *testing.T) {
	var output bytes.Buffer
	writeHelp(&output)

	text := output.String()
	for _, expected := range []string{"presence", "delegate --brief-file <path>", "dispatch <command>", "workspace context <command>", "open <file.md> [--session <id>]", "review-loop <command>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help output missing %q: %q", expected, text)
		}
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

func TestWriteWorkspaceHelpMentionsMaintenanceCommands(t *testing.T) {
	var output bytes.Buffer
	writeWorkspaceHelp(&output)

	text := output.String()
	for _, expected := range []string{
		"compact [--session <id>]",
		"rollback [--session <id>]",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("workspace help missing %q: %q", expected, text)
		}
	}
}

func TestWriteDelegateHelpMentionsPlacementOptions(t *testing.T) {
	var output bytes.Buffer
	writeDelegateHelp(&output)

	text := output.String()
	for _, expected := range []string{
		"--new-workspace",
		"--workspace <id>",
		"--cwd <path>",
		"--worktree <branch>",
		"combine with --new-workspace to place the worktree in a new workspace",
		"--agent <name>",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("delegate help missing %q: %q", expected, text)
		}
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

func TestHasActiveBackgroundTask(t *testing.T) {
	cases := []struct {
		name string
		// payload is a real Claude Code 2.1.177 Stop-hook stdin body (trimmed to
		// the fields attn parses), captured from a live background Workflow run.
		payload string
		want    bool
	}{
		{
			name:    "workflow running (parent yields mid-run)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}`,
			want:    true,
		},
		{
			name:    "workflow plus background shells running",
			payload: `{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}`,
			want:    true,
		},
		{
			name:    "empty background_tasks (workflow finished)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}`,
			want:    false,
		},
		{
			name:    "field absent (e.g. another agent)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
			want:    false,
		},
		{
			name:    "task present but not running",
			payload: `{"background_tasks":[{"type":"workflow","status":"completed"}]}`,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if got := hasActiveBackgroundTask(input); got != tc.want {
				t.Fatalf("hasActiveBackgroundTask() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasPendingSessionCron(t *testing.T) {
	cases := []struct {
		name string
		// payload is a real Claude Code 2.1.177 Stop-hook stdin body, captured
		// from live CronCreate/CronDelete and one-shot-fire probes.
		payload string
		want    bool
	}{
		{
			name:    "recurring cron pending",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo persist-probe"}]}`,
			want:    true,
		},
		{
			name:    "one-shot reminder pending",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"session_crons":[{"id":"5e9a0f21","schedule":"18 14 * * *","recurring":false,"prompt":"echo oneshot-fired"}]}`,
			want:    true,
		},
		{
			name:    "recurring plus one-shot pending",
			payload: `{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}`,
			want:    true,
		},
		{
			name:    "empty session_crons (nothing scheduled, or all crons fired/deleted)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}`,
			want:    false,
		},
		{
			name:    "field absent (e.g. another agent)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if got := hasPendingSessionCron(input); got != tc.want {
				t.Fatalf("hasPendingSessionCron() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDecideStop locks the non-terminal-Stop precedence: running background
// work outranks a parked schedule, and either outranks classification.
func TestNonTerminalStopState(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "background running and cron pending -> working wins",
			payload: `{"background_tasks":[{"type":"shell","status":"running"}],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo x"}]}`,
			want:    protocol.StateWorking,
		},
		{
			name:    "cron pending, no background -> scheduled",
			payload: `{"background_tasks":[],"session_crons":[{"id":"5e9a0f21","schedule":"18 14 * * *","recurring":false,"prompt":"echo x"}]}`,
			want:    protocol.StateScheduled,
		},
		{
			name:    "background running, no cron -> working",
			payload: `{"background_tasks":[{"type":"workflow","status":"running"}],"session_crons":[]}`,
			want:    protocol.StateWorking,
		},
		{
			name:    "completed background and cron pending -> scheduled (completed is not running)",
			payload: `{"background_tasks":[{"type":"workflow","status":"completed"}],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo x"}]}`,
			want:    protocol.StateScheduled,
		},
		{
			name:    "nothing pending -> classify (empty)",
			payload: `{"background_tasks":[],"session_crons":[]}`,
			want:    "",
		},
		{
			name:    "fields absent -> classify (empty)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if got := nonTerminalStopState(input); got != tc.want {
				t.Fatalf("nonTerminalStopState() = %q, want %q", got, tc.want)
			}
		})
	}
}
