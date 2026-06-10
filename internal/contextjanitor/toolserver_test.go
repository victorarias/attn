package contextjanitor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeToolServerReadsAndReplacesContext(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.md")
	candidatePath := filepath.Join(dir, "candidate.md")
	if err := os.WriteFile(sourcePath, []byte("# Workspace Context\n\n## Area\n\nShared work.\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_context","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"replace_context","arguments":{"content":"# Workspace Context\n\n## Area\n\nCompacted.\n"}}}`,
	}, "\n") + "\n"

	var output bytes.Buffer
	if err := ServeToolServer(context.Background(), sourcePath, candidatePath, strings.NewReader(requests), &output); err != nil {
		t.Fatalf("ServeToolServer error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("responses = %d, want 4:\n%s", len(lines), output.String())
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(listed.Result.Tools) != 2 ||
		listed.Result.Tools[0].Name != "read_context" ||
		listed.Result.Tools[1].Name != "replace_context" {
		t.Fatalf("tools = %+v", listed.Result.Tools)
	}
	if !strings.Contains(lines[2], "Shared work.") {
		t.Fatalf("read_context response missing source: %s", lines[2])
	}
	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if string(candidate) != "# Workspace Context\n\n## Area\n\nCompacted.\n" {
		t.Fatalf("candidate = %q", candidate)
	}
}

func TestServeToolServerEnforcesInitializationAndToolOrder(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.md")
	candidatePath := filepath.Join(dir, "candidate.md")
	if err := os.WriteFile(sourcePath, []byte("# Workspace Context\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_context","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"replace_context","arguments":{"content":"early"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_context","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"replace_context","arguments":{"content":"final"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"replace_context","arguments":{"content":"second"}}}`,
	}, "\n") + "\n"

	var output bytes.Buffer
	if err := ServeToolServer(context.Background(), sourcePath, candidatePath, strings.NewReader(requests), &output); err != nil {
		t.Fatalf("ServeToolServer error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("responses = %d, want 6:\n%s", len(lines), output.String())
	}
	if !strings.Contains(lines[0], "server is not initialized") {
		t.Fatalf("pre-initialize response = %s", lines[0])
	}
	if !strings.Contains(lines[2], "read_context must be called") {
		t.Fatalf("early replace response = %s", lines[2])
	}
	if !strings.Contains(lines[5], "replace_context may be called only once") {
		t.Fatalf("second replace response = %s", lines[5])
	}
	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if string(candidate) != "final" {
		t.Fatalf("candidate = %q, want final", candidate)
	}
}
