package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/classifier"
)

// fakeClaudeClassifierCLI writes a stub `claude` that records its argv and emits
// the given stdout, then points the driver's executable env var at it.
func fakeClaudeClassifierCLI(t *testing.T, stdout string, exitCode int) (argsPath string) {
	t.Helper()
	dir := t.TempDir()
	argsPath = filepath.Join(dir, "args.log")
	scriptPath := filepath.Join(dir, "claude")
	// Args are recorded NUL-separated: the prompt and the schema are multi-line,
	// so a newline separator would shred them.
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  printf '%%s\034' "$arg" >> '%s'
done
cat <<'STDOUT'
%s
STDOUT
exit %d
`, argsPath, stdout, exitCode)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", scriptPath)
	return argsPath
}

func readArgs(t *testing.T, argsPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x1c"), "\x1c")
}

// argAfter returns the value following flag in a recorded argv.
func argAfter(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("flag %q not found in %q", flag, args)
	return ""
}

// The classifier runs through the shared headless seam: capped, tool-less,
// schema-enforced, and isolated from the user's MCP servers and settings.
func TestClaudeClassifyRunsBoundedToolLessHeadlessQuery(t *testing.T) {
	argsPath := fakeClaudeClassifierCLI(t,
		`{"type":"result","structured_output":{"verdict":"WAITING"},"num_turns":1,"total_cost_usd":0.0012}`, 0)
	t.Setenv("ATTN_CLAUDE_CLASSIFIER_MODEL", "test-haiku")

	state, err := (&Claude{}).Classify("Should I continue?", 30*time.Second)
	if err != nil {
		t.Fatalf("Classify unexpected err: %v", err)
	}
	if state != "waiting_input" {
		t.Fatalf("Classify() = %q, want waiting_input", state)
	}

	args := readArgs(t, argsPath)
	assertContainsAll(t, "Claude classifier args", args,
		"--print",
		"--strict-mcp-config",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--model", "test-haiku",
		"--max-turns", "2",
		"--permission-mode", "dontAsk",
		"--output-format", "json",
	)
	if got := argAfter(t, args, "--json-schema"); got != classifier.ClaudeVerdictSchema {
		t.Fatalf("--json-schema = %q, want the verdict schema", got)
	}
	// Tool-less: the run judges inlined text and must never touch disk.
	if got := argAfter(t, args, "--allowedTools"); got != "" {
		t.Fatalf("--allowedTools = %q, want empty (no tools)", got)
	}
	if prompt := args[len(args)-1]; !strings.Contains(prompt, "Should I continue?") {
		t.Fatalf("prompt arg missing the classified text: %q", prompt)
	}
	assertContainsNone(t, "Claude classifier args", args, "--mcp-config", "Read,Write,Edit,Grep,Glob")
}

// A run that ended before the structured-output turn still classifies from its
// final text.
func TestClaudeClassifyFallsBackToFinalText(t *testing.T) {
	fakeClaudeClassifierCLI(t, `{"type":"result","result":"DONE"}`, 0)

	state, err := (&Claude{}).Classify("I finished the task.", 30*time.Second)
	if err != nil {
		t.Fatalf("Classify unexpected err: %v", err)
	}
	if state != "idle" {
		t.Fatalf("Classify() = %q, want idle", state)
	}
}

func TestClaudeClassifyVerdictlessRunIsUnknown(t *testing.T) {
	fakeClaudeClassifierCLI(t, `{"type":"result","result":"I'll keep working on it."}`, 0)

	state, err := (&Claude{}).Classify("some text", 30*time.Second)
	if err != nil {
		t.Fatalf("Classify unexpected err: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("Classify() = %q, want unknown", state)
	}
}

func TestClaudeClassifyFailedRunIsUnknownWithError(t *testing.T) {
	fakeClaudeClassifierCLI(t, `{"type":"result","is_error":true,"result":"Not logged in"}`, 1)

	state, err := (&Claude{}).Classify("some text", 30*time.Second)
	if err == nil {
		t.Fatal("expected an error from a failed claude run")
	}
	if state != "unknown" {
		t.Fatalf("Classify() = %q, want unknown", state)
	}
}

func TestClaudeClassifyEmptyTextSkipsTheCLI(t *testing.T) {
	argsPath := fakeClaudeClassifierCLI(t, `{"type":"result","result":"WAITING"}`, 0)

	state, err := (&Claude{}).Classify("   ", 0)
	if err != nil {
		t.Fatalf("Classify unexpected err: %v", err)
	}
	if state != "idle" {
		t.Fatalf("Classify() = %q, want idle", state)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("expected no claude invocation for empty text")
	}
}
