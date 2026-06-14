package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// TestDriverAgentLiveCodexRoundTrip drives ONE real `codex exec` round-trip
// through the driverAgent with a tiny schema and asserts a schema-valid object
// comes back. It SKIPS cleanly when codex auth/network is unavailable so the
// default suite stays green. Opt in by NOT setting ATTN_SKIP_LIVE_CODEX.
//
// It requires a real `attn` binary to host the result-sink subcommand, so it
// builds one into a temp dir (the test binary itself is not `attn`).
func TestDriverAgentLiveCodexRoundTrip(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_RUN_LIVE_CODEX")) == "" {
		t.Skip("set ATTN_RUN_LIVE_CODEX=1 to run the live codex round-trip")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	if _, err := os.Stat(codexAuthPath()); err != nil {
		t.Skip("codex auth.json not found; skipping live round-trip")
	}

	// Build a real attn binary to host `_workflow-result-mcp`.
	tmp := t.TempDir()
	attnPath := filepath.Join(tmp, "attn")
	build := exec.Command("go", "build", "-o", attnPath, "github.com/victorarias/attn/cmd/attn")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build attn: %v", err)
	}

	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:       "codex",
		Executable:     codexPath,
		Model:          codexLiveModel(),
		RunTmpDir:      tmp,
		AttnExecutable: attnPath,
		MaxRetries:     1,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}

	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, runErr := da.runWithSchema(ctx, ordForTest(), "Respond with the word PONG in the answer field.", schema)
	if runErr != nil {
		t.Fatalf("live codex round-trip failed: %v", runErr)
	}
	var obj struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(result, &obj); err != nil {
		t.Fatalf("result is not the schema object: %s (%v)", result, err)
	}
	if strings.TrimSpace(obj.Answer) == "" {
		t.Fatalf("schema object has empty answer: %s", result)
	}
	t.Logf("live codex round-trip returned: %s", result)
}

func codexAuthPath() string {
	if home := os.Getenv("CODEX_HOME"); strings.TrimSpace(home) != "" {
		return filepath.Join(home, "auth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "auth.json")
}

func codexLiveModel() string {
	if m := strings.TrimSpace(os.Getenv("ATTN_LIVE_CODEX_MODEL")); m != "" {
		return m
	}
	return "gpt-5-codex"
}

var _ = agentdriver.Get
