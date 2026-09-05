package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeModelInfo preserves unknown effort support separately from false.
type ClaudeModelInfo struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel,omitempty"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        *bool    `json:"supportsEffort,omitempty"`
	SupportedEffortLevels []string `json:"supportedEffortLevels,omitempty"`
}

// DiscoverModels reads the CLI initialization catalog, like SDK supportedModels().
// It sends no user message; callers decide when discovery is enabled.
func (c *Claude) DiscoverModels(ctx context.Context, executable, workDir string) ([]ClaudeModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.ResolveExecutable(executable),
		"--print", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose",
		"--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--settings", `{"disableAllHooks":true}`, "--tools", "",
	)
	cmd.Dir = workDir
	cmd.Env = headlessEnvironment("claude", workDir)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("Claude model discovery stdout: %w", err)
	}
	defer stdout.Close()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("Claude model discovery stdin: %w", err)
	}
	defer stdin.Close()
	// Closing our pipe unblocks decoding even if a descendant retained stdout.
	cmd.Cancel = func() error {
		_ = stdout.Close()
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Claude model discovery: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()
	const requestID = "attn-model-discovery"
	request := map[string]any{
		"type": "control_request", "request_id": requestID,
		"request": map[string]string{"subtype": "initialize"},
	}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("initialize Claude model discovery: %w", err)
	}
	models, err := readClaudeModels(stdout, requestID)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("Claude model discovery: %w", ctx.Err())
	}
	return models, err
}

func readClaudeModels(reader io.Reader, requestID string) ([]ClaudeModelInfo, error) {
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Response  struct {
					Models *[]ClaudeModelInfo `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := decoder.Decode(&message); err != nil {
			return nil, fmt.Errorf("read Claude model catalog: %w", err)
		}
		if message.Type != "control_response" || message.Response.RequestID != requestID {
			continue
		}
		if message.Response.Subtype != "success" {
			return nil, errors.New("Claude model discovery initialization failed")
		}
		if message.Response.Response.Models == nil {
			return nil, errors.New("Claude initialization did not supply a model catalog")
		}
		models := *message.Response.Response.Models
		for _, model := range models {
			if strings.TrimSpace(model.Value) == "" {
				return nil, errors.New("Claude model catalog contains a missing model ID")
			}
		}
		return models, nil
	}
}
