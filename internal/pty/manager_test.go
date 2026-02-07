package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestParseNullSeparatedEnv(t *testing.T) {
	input := []byte("FOO=bar\x00EMPTY=\x00INVALID\x00PATH=/bin\x00")
	got := parseNullSeparatedEnv(input)
	want := []string{"FOO=bar", "EMPTY=", "PATH=/bin"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeEnvironment_OverlayWins(t *testing.T) {
	base := []string{"FOO=base", "BAR=1"}
	overlay := []string{"FOO=overlay", "BAZ=2"}
	got := mergeEnvironment(base, overlay)

	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "FOO=overlay") {
		t.Fatalf("expected overlay value for FOO in %v", got)
	}
	if strings.Contains(joined, "FOO=base") {
		t.Fatalf("did not expect base value for FOO in %v", got)
	}
	if !strings.Contains(joined, "BAR=1") || !strings.Contains(joined, "BAZ=2") {
		t.Fatalf("missing expected keys in %v", got)
	}
}

func TestPreferredShellCandidates(t *testing.T) {
	got := preferredShellCandidates("/opt/homebrew/bin/fish")
	if len(got) == 0 {
		t.Fatal("expected non-empty candidate list")
	}
	if got[0] != "/opt/homebrew/bin/fish" {
		t.Fatalf("first candidate = %q, want login shell", got[0])
	}
	seen := map[string]struct{}{}
	for _, shell := range got {
		if _, ok := seen[shell]; ok {
			t.Fatalf("duplicate shell candidate %q in %v", shell, got)
		}
		seen[shell] = struct{}{}
	}
	if _, ok := seen["/bin/sh"]; !ok {
		t.Fatalf("expected /bin/sh fallback in %v", got)
	}
	if runtime.GOOS == "darwin" {
		if _, ok := seen["/bin/zsh"]; !ok {
			t.Fatalf("expected /bin/zsh fallback on darwin in %v", got)
		}
	}
}

func TestShouldFallbackShell(t *testing.T) {
	if !shouldFallbackShell(fmt.Errorf("wrapped: %w", syscall.EPERM)) {
		t.Fatal("expected EPERM to trigger fallback")
	}
	if shouldFallbackShell(syscall.EINVAL) {
		t.Fatal("expected EINVAL to not trigger fallback")
	}
}

func TestShouldSetpgidForPTY(t *testing.T) {
	got := shouldSetpgidForPTY()
	if runtime.GOOS == "darwin" && got {
		t.Fatal("expected shouldSetpgidForPTY=false on darwin")
	}
	if runtime.GOOS != "darwin" && !got {
		t.Fatal("expected shouldSetpgidForPTY=true outside darwin")
	}
}

func TestFirstExecutablePath_PicksFirstValidExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "missing-attn")
	notExec := filepath.Join(tmpDir, "not-exec-attn")
	execPath := filepath.Join(tmpDir, "exec-attn")

	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write notExec: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write execPath: %v", err)
	}

	got, ok := firstExecutablePath([]string{missing, notExec, execPath})
	if !ok {
		t.Fatal("expected executable candidate to be found")
	}
	if got != execPath {
		t.Fatalf("firstExecutablePath = %q, want %q", got, execPath)
	}
}

func TestFirstExecutablePath_ReturnsFalseWhenNoneValid(t *testing.T) {
	tmpDir := t.TempDir()
	notExec := filepath.Join(tmpDir, "not-exec-attn")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write notExec: %v", err)
	}

	got, ok := firstExecutablePath([]string{"", "   ", filepath.Join(tmpDir, "missing"), notExec})
	if ok {
		t.Fatalf("expected no executable candidate, got %q", got)
	}
}
