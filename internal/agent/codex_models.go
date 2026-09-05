package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Codex) DiscoverDelegationModels(ctx context.Context, executable, cwd string) ([]protocol.DelegationModel, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.ResolveExecutable(executable), "app-server")
	cmd.Dir = cwd
	cmd.Env = headlessEnvironment("codex", cwd)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer stdout.Close()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer stdin.Close()
	cmd.Cancel = func() error { _ = stdout.Close(); return cmd.Process.Kill() }
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Codex model discovery: %w", err)
	}
	defer func() { _ = stdin.Close(); cancel(); _ = cmd.Wait() }()
	result, err := readCodexModels(stdout, stdin)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("Codex model discovery: %w", ctx.Err())
	}
	return result, err
}

func readCodexModels(stdout io.Reader, stdin io.Writer) ([]protocol.DelegationModel, error) {
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	call := func(id int, method string, params any, out any) error {
		if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return err
		}
		for {
			var message struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := decoder.Decode(&message); err != nil {
				return err
			}
			if message.ID == nil || *message.ID != id {
				continue
			}
			if message.Error != nil {
				return fmt.Errorf("Codex %s: %s", method, message.Error.Message)
			}
			if out != nil {
				return json.Unmarshal(message.Result, out)
			}
			return nil
		}
	}
	if err := call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "attn_model_discovery", "version": "1"}}, nil); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]string{"method": "initialized"}); err != nil {
		return nil, err
	}
	result := []protocol.DelegationModel{}
	cursor := ""
	seen := map[string]bool{}
	for id := 2; ; id++ {
		var page struct {
			Data *[]struct {
				ID          string `json:"id"`
				Model       string `json:"model"`
				DisplayName string `json:"displayName"`
				Description string `json:"description"`
				Efforts     *[]struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := call(id, "model/list", params, &page); err != nil {
			return nil, err
		}
		if page.Data == nil {
			return nil, fmt.Errorf("Codex did not supply a model catalog")
		}
		for _, m := range *page.Data {
			modelID := m.Model
			if modelID == "" {
				modelID = m.ID
			}
			if modelID == "" {
				return nil, fmt.Errorf("Codex catalog contains a missing model ID")
			}
			support := protocol.ModelCapabilitySupportUnknown
			levels := []string{}
			if m.Efforts != nil {
				support = protocol.ModelCapabilitySupportUnsupported
				for _, e := range *m.Efforts {
					if e.Effort != "" {
						levels = append(levels, e.Effort)
					}
				}
				if len(levels) > 0 {
					support = protocol.ModelCapabilitySupportSupported
				}
			}
			result = append(result, protocol.DelegationModel{Harness: "codex", ID: modelID, Name: m.DisplayName, Description: m.Description, EffortSupport: support, EffortLevels: levels, Access: protocol.ModelCapabilitySupportUnknown})
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if seen[cursor] {
			return nil, fmt.Errorf("Codex model catalog repeated its pagination cursor")
		}
		seen[cursor] = true
	}
	return result, nil
}
