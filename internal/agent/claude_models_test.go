package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeModelCatalog(t *testing.T) {
	stream := `{"type":"keep_alive"}
{"type":"control_response","response":{"subtype":"error","request_id":"unrelated"}}
{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[
{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet","supportsEffort":true,"supportedEffortLevels":["low","high","future-level"]},
{"value":"no-effort","supportsEffort":false},
{"value":"unknown"}
]}}}`
	models, err := readClaudeModels(strings.NewReader(stream), "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || models[0].ResolvedModel != "claude-sonnet-5" {
		t.Fatalf("models = %#v", models)
	}
	if models[0].SupportsEffort == nil || !*models[0].SupportsEffort || models[0].SupportedEffortLevels[2] != "future-level" {
		t.Fatalf("effort metadata = %#v", models[0])
	}
	if models[1].SupportsEffort == nil || *models[1].SupportsEffort || models[2].SupportsEffort != nil {
		t.Fatalf("unknown and unsupported collapsed: %#v", models)
	}
}

func TestClaudeModelCatalogFailures(t *testing.T) {
	for _, test := range []struct{ name, stream, want string }{
		{"EOF", "", "EOF"},
		{"invalid JSON", "not json", "read Claude model catalog"},
		{"error", `{"type":"control_response","response":{"subtype":"error","request_id":"catalog","error":"private child output"}}`, "initialization failed"},
		{"missing models", `{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{}}}`, "did not supply"},
		{"missing ID", `{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[{}]}}}`, "missing model ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readClaudeModels(strings.NewReader(test.stream), "catalog")
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "private child output") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	models, err := readClaudeModels(strings.NewReader(`{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[]}}}`), "catalog")
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("empty catalog = %#v, %v", models, err)
	}
	_, err = readClaudeModels(strings.NewReader(`{"type":"keep_alive"}`), "catalog")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestClaudeDiscoverModelsProcess(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" > args
IFS= read -r request
printf '%s\n' "$request" > request.json
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"attn-model-discovery","response":{"models":[{"value":"sonnet","supportsEffort":true,"supportedEffortLevels":["medium","high"]}]}}}'
cat > extra-input
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	models, err := (&Claude{}).DiscoverModels(context.Background(), executable, dir)
	if err != nil || len(models) != 1 {
		t.Fatalf("discovery = %#v, %v", models, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request["type"] != "control_request" || len(request) != 3 || request["request"].(map[string]any)["subtype"] != "initialize" {
		t.Fatalf("request = %#v", request)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil || !strings.Contains(string(args), "--no-session-persistence") || !strings.Contains(string(args), `{"disableAllHooks":true}`) {
		t.Fatalf("args = %s, error = %v", args, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Claude{}).DiscoverModels(ctx, executable, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery = %v", err)
	}
}
