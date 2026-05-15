package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunGitOutputTimesOut(t *testing.T) {
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nexec sleep 5\n"), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cleanup := setTimeoutForTesting(OpMetadata, 25*time.Millisecond)
	defer cleanup()

	_, err := runGitOutput(OpMetadata, t.TempDir(), "status")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v, want timed out", err)
	}
}

func TestRunGitOutputLogsSlowCommand(t *testing.T) {
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nsleep 3\nprintf ok\n"), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var logs []string
	SetLogFunc(func(format string, args ...interface{}) {
		logs = append(logs, format)
	})
	defer SetLogFunc(nil)

	out, err := runGitOutput(OpMetadata, t.TempDir(), "status")
	if err != nil {
		t.Fatalf("runGitOutput failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
	if len(logs) == 0 {
		t.Fatal("expected slow command log")
	}
}
