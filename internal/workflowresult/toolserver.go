// Package workflowresult implements a schema-validating MCP "result sink" used
// by the workflow engine's real agent() path. It exposes exactly ONE tool whose
// inputSchema is the per-call JSON Schema. On tools/call it validates the
// payload against that schema IN-TURN: a mismatch returns a successful JSON-RPC
// response carrying isError:true (so the model self-corrects in the same turn,
// rather than aborting the run); a valid payload is written atomically to the
// result file. Repeated valid calls are last-write-wins — re-emission is normal
// LLM behavior and must not error (unlike the workspace-context janitor, which
// gates read/replace and hard-aborts).
package workflowresult

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
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

// ServeResultSink runs the single-tool result sink over the given streams.
//
//   - toolName is the one advertised tool (e.g. "return_result").
//   - schema is the JSON Schema advertised as the tool inputSchema AND validated
//     against on each call. Empty => a permissive {"type":"object"} default.
//   - resultPath is the atomic write target for a validated payload.
func ServeResultSink(
	ctx context.Context,
	toolName string,
	schema json.RawMessage,
	resultPath string,
	input io.Reader,
	output io.Writer,
) error {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "return_result"
	}

	// The schema object surfaced as inputSchema. Empty schema => permissive
	// object (the no-schema path does not use this sink, so this is defensive).
	schemaObject := map[string]any{"type": "object"}
	if len(schema) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(schema, &parsed); err != nil {
			return fmt.Errorf("parse result schema: %w", err)
		}
		schemaObject = parsed
	}

	// Compile ONCE so every tools/call reuses it.
	compiled, compileErr := compileSchema(schemaObject)

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	encoder := json.NewEncoder(output)
	initialized := false

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
					"name":    "attn-workflow-result",
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
						"name":        toolName,
						"description": "Return the final structured result. Call this exactly once with a JSON object that satisfies the provided schema; the run completes when it is called with a valid payload.",
						"inputSchema": schemaObject,
					},
				},
			}
		case "tools/call":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "server is not initialized"}
				break
			}
			response.Result = callTool(request.Params, toolName, compiled, compileErr, resultPath)
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not found: " + request.Method}
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write MCP response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP request: %w", err)
	}
	return nil
}

// callTool validates the payload against the compiled schema in-turn. A
// validation failure returns isError:true (NOT a JSON-RPC error) so the model
// can self-correct in the same turn; a valid payload is written atomically
// (last-write-wins).
func callTool(
	params map[string]any,
	toolName string,
	compiled *jsonschema.Schema,
	compileErr error,
	resultPath string,
) map[string]any {
	name, _ := params["name"].(string)
	if name != toolName {
		return toolResult("tool not found: "+name, true)
	}
	if compileErr != nil {
		// A misconfigured schema is a server-config problem; surface it as an
		// in-turn tool error rather than silently accepting unvalidated input.
		return toolResult("Result schema is invalid: "+compileErr.Error(), true)
	}

	arguments := params["arguments"]
	if arguments == nil {
		// Some clients omit arguments entirely; treat as an empty object so the
		// schema (e.g. required fields) drives the validation message.
		arguments = map[string]any{}
	}

	// Re-decode the arguments through jsonschema's number-preserving unmarshal so
	// validation matches the wire bytes exactly (json.Number, not float64).
	rawArgs, err := json.Marshal(arguments)
	if err != nil {
		return toolResult("Validation failed: arguments are not valid JSON: "+err.Error(), true)
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(rawArgs)))
	if err != nil {
		return toolResult("Validation failed: arguments are not valid JSON: "+err.Error(), true)
	}

	if err := compiled.Validate(instance); err != nil {
		return toolResult("Validation failed: "+err.Error(), true)
	}

	if err := writeResult(resultPath, rawArgs); err != nil {
		// A write failure is a server-side problem; report it in-turn so the
		// model does not assume success.
		return toolResult("Failed to record result: "+err.Error(), true)
	}
	return toolResult("Result recorded.", false)
}

// compileSchema compiles the schema object once for reuse.
func compileSchema(schemaObject map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	const loc = "mem://result-schema"
	if err := compiler.AddResource(loc, schemaObject); err != nil {
		return nil, err
	}
	return compiler.Compile(loc)
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// writeResult atomically writes the validated payload to path (0600), creating
// the parent directory if needed. Each valid call overwrites the previous one.
func writeResult(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create result directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".result-*.tmp")
	if err != nil {
		return fmt.Errorf("create result: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("protect result: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("write result: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close result: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace result: %w", err)
	}
	return nil
}
