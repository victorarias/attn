package contextjanitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// ServeToolServer exposes one captured context through exactly two MCP tools.
// The final replace_context call wins and is written atomically to candidatePath.
func ServeToolServer(
	ctx context.Context,
	sourcePath string,
	candidatePath string,
	input io.Reader,
	output io.Writer,
) error {
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source context: %w", err)
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	encoder := json.NewEncoder(output)
	initialized := false
	read := false
	replaced := false
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			if encodeErr := encoder.Encode(rpcResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "failed to parse request"},
			}); encodeErr != nil {
				return encodeErr
			}
			continue
		}
		if request.ID == nil {
			continue
		}

		response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
		switch request.Method {
		case "initialize":
			if initialized {
				response.Error = &rpcError{Code: -32600, Message: "server is already initialized"}
				break
			}
			initialized = true
			response.Result = map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{
					"name":    "attn-context-janitor",
					"version": "1",
				},
			}
		case "ping":
			response.Result = map[string]any{}
		case "tools/list":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "server is not initialized"}
				break
			}
			response.Result = map[string]any{
				"tools": []map[string]any{
					{
						"name":        "read_context",
						"description": "Read the captured canonical workspace context.",
						"inputSchema": map[string]any{
							"type":                 "object",
							"properties":           map[string]any{},
							"additionalProperties": false,
						},
					},
					{
						"name":        "replace_context",
						"description": "After read_context, store the complete compacted workspace context exactly once.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"content": map[string]any{"type": "string"},
							},
							"required":             []string{"content"},
							"additionalProperties": false,
						},
					},
				},
			}
		case "tools/call":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "server is not initialized"}
				break
			}
			result, callErr := callTool(
				request.Params,
				string(source),
				candidatePath,
				&read,
				&replaced,
			)
			if callErr != nil {
				response.Result = toolResult("Error: "+callErr.Error(), true)
			} else {
				response.Result = result
			}
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not found: " + request.Method}
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write MCP response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read MCP request: %w", err)
	}
	return nil
}

func callTool(
	params map[string]any,
	source string,
	candidatePath string,
	read *bool,
	replaced *bool,
) (map[string]any, error) {
	name, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]any)
	switch name {
	case "read_context":
		if *replaced {
			return nil, errors.New("context was already replaced")
		}
		*read = true
		return toolResult(source, false), nil
	case "replace_context":
		if !*read {
			return nil, errors.New("read_context must be called before replace_context")
		}
		if *replaced {
			return nil, errors.New("replace_context may be called only once")
		}
		content, ok := arguments["content"].(string)
		if !ok {
			return nil, errors.New("content must be a string")
		}
		if err := writeCandidate(candidatePath, []byte(content)); err != nil {
			return nil, err
		}
		*replaced = true
		return toolResult("Candidate stored.", false), nil
	default:
		return nil, fmt.Errorf("tool not found: %s", name)
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func writeCandidate(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create candidate directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".candidate-*.tmp")
	if err != nil {
		return fmt.Errorf("create candidate: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("protect candidate: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("write candidate: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close candidate: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace candidate: %w", err)
	}
	return nil
}
