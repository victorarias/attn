package ptyworker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldEnableDebugCapture_DefaultAndOverrides(t *testing.T) {
	// Off by default for every agent: the capture buffer is a debugging aid that
	// otherwise retains ~16-22 MiB per claude/codex worker for its lifetime.
	t.Setenv("ATTN_DEBUG_PTY_CAPTURE", "")
	for _, agent := range []string{"codex", "claude", "copilot"} {
		if shouldEnableDebugCapture(agent) {
			t.Fatalf("expected %s capture disabled by default", agent)
		}
	}

	t.Setenv("ATTN_DEBUG_PTY_CAPTURE", "all")
	if !shouldEnableDebugCapture("copilot") {
		t.Fatal("expected copilot capture enabled with ATTN_DEBUG_PTY_CAPTURE=all")
	}

	// Explicit per-agent opt-in is scoped to that agent only.
	t.Setenv("ATTN_DEBUG_PTY_CAPTURE", "codex")
	if !shouldEnableDebugCapture("codex") {
		t.Fatal("expected codex capture enabled with ATTN_DEBUG_PTY_CAPTURE=codex")
	}
	if shouldEnableDebugCapture("claude") {
		t.Fatal("expected claude capture disabled with ATTN_DEBUG_PTY_CAPTURE=codex")
	}

	t.Setenv("ATTN_DEBUG_PTY_CAPTURE", "claude")
	if !shouldEnableDebugCapture("claude") {
		t.Fatal("expected claude capture enabled with ATTN_DEBUG_PTY_CAPTURE=claude")
	}
	if shouldEnableDebugCapture("codex") {
		t.Fatal("expected codex capture disabled with ATTN_DEBUG_PTY_CAPTURE=claude")
	}

	t.Setenv("ATTN_DEBUG_PTY_CAPTURE", "0")
	if shouldEnableDebugCapture("codex") {
		t.Fatal("expected capture disabled with ATTN_DEBUG_PTY_CAPTURE=0")
	}
}

func TestIsWorkingToStopTransition(t *testing.T) {
	if !isWorkingToStopTransition("working", "waiting_input") {
		t.Fatal("expected working -> waiting_input to trigger dump")
	}
	if !isWorkingToStopTransition("working", "idle") {
		t.Fatal("expected working -> idle to trigger dump")
	}
	if isWorkingToStopTransition("pending_approval", "waiting_input") {
		t.Fatal("unexpected dump trigger for pending_approval -> waiting_input")
	}
}

func TestDebugCaptureDumpCreatesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir captures: %v", err)
	}

	capture := &debugCapture{
		sessionID: "sess-1",
		agent:     "codex",
		dir:       dir,
		window:    90 * time.Second,
		maxEvents: 16,
	}
	capture.recordState("working")
	capture.recordOutput(7, []byte("hello"))
	capture.recordInput([]byte("status"))

	path, err := capture.dump("working_to_waiting_input")
	if err != nil {
		t.Fatalf("dump failed: %v", err)
	}
	if path == "" {
		t.Fatal("dump path should not be empty")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "\"kind\":\"meta\"") {
		t.Fatalf("dump missing meta line: %s", text)
	}
	if !strings.Contains(text, "\"kind\":\"output\"") {
		t.Fatalf("dump missing output event: %s", text)
	}
}
