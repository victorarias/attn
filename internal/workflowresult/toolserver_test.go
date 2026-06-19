package workflowresult

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// answerSchema is a tiny strict schema used across the sink tests.
const answerSchema = `{"type":"object","additionalProperties":false,"required":["answer"],"properties":{"answer":{"type":"string"}}}`

type rpcCallResult struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeCall(t *testing.T, line string) rpcCallResult {
	t.Helper()
	var r rpcCallResult
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatalf("decode call response %q: %v", line, err)
	}
	return r
}

func serve(t *testing.T, schema, resultPath string, requestLines []string) []string {
	t.Helper()
	requests := strings.Join(requestLines, "\n") + "\n"
	var output bytes.Buffer
	if err := ServeResultSink(
		context.Background(),
		"return_result",
		json.RawMessage(schema),
		resultPath,
		strings.NewReader(requests),
		&output,
	); err != nil {
		t.Fatalf("ServeResultSink error: %v", err)
	}
	return strings.Split(strings.TrimSpace(output.String()), "\n")
}

func TestToolsListAdvertisesSchema(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	})
	if len(lines) != 2 {
		t.Fatalf("responses = %d, want 2:\n%v", len(lines), lines)
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(listed.Result.Tools) != 1 || listed.Result.Tools[0].Name != "return_result" {
		t.Fatalf("tools = %+v", listed.Result.Tools)
	}
	// inputSchema must equal the passed schema (semantically; compare normalized).
	if !jsonEqual(t, listed.Result.Tools[0].InputSchema, json.RawMessage(answerSchema)) {
		t.Fatalf("inputSchema = %s, want %s", listed.Result.Tools[0].InputSchema, answerSchema)
	}
}

func TestValidPayloadWritesResult(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"return_result","arguments":{"answer":"42"}}}`,
	})
	call := decodeCall(t, lines[1])
	if call.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", call.Error)
	}
	if call.Result.IsError {
		t.Fatalf("valid payload returned isError: %+v", call.Result)
	}
	b, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !jsonEqual(t, b, json.RawMessage(`{"answer":"42"}`)) {
		t.Fatalf("result file = %s", b)
	}
}

func TestInvalidPayloadIsInTurnErrorNoFile(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		// "answer" must be a string; pass a number -> schema-invalid.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"return_result","arguments":{"answer":7}}}`,
	})
	call := decodeCall(t, lines[1])
	// Key invariant: NOT a JSON-RPC error (so the model can self-correct in-turn),
	// but a successful response carrying isError:true.
	if call.Error != nil {
		t.Fatalf("validation surfaced as a JSON-RPC error (should be in-turn isError): %+v", call.Error)
	}
	if !call.Result.IsError {
		t.Fatalf("schema-invalid payload did not return isError: %+v", call.Result)
	}
	msg := callText(call)
	if !strings.Contains(strings.ToLower(msg), "valid") {
		t.Fatalf("error message does not describe a validation failure: %q", msg)
	}
	// The message should name the offending field (santhosh-tekuri lists the
	// instance location), enabling self-correction.
	if !strings.Contains(msg, "answer") {
		t.Fatalf("error message does not name the offending field: %q", msg)
	}
	if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
		t.Fatalf("result file was written on a validation failure (err=%v)", err)
	}
}

func TestDoubleValidCallLastWriteWins(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"return_result","arguments":{"answer":"first"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"return_result","arguments":{"answer":"second"}}}`,
	})
	for i, line := range lines[1:] {
		call := decodeCall(t, line)
		if call.Error != nil || call.Result.IsError {
			t.Fatalf("call %d returned an error: line=%s", i+1, line)
		}
	}
	b, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !jsonEqual(t, b, json.RawMessage(`{"answer":"second"}`)) {
		t.Fatalf("last-write-wins failed; result = %s", b)
	}
}

func TestInvalidThenValidSelfCorrects(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"return_result","arguments":{"wrong":"x"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"return_result","arguments":{"answer":"ok"}}}`,
	})
	first := decodeCall(t, lines[1])
	if !first.Result.IsError {
		t.Fatalf("invalid first call did not return isError: %+v", first.Result)
	}
	second := decodeCall(t, lines[2])
	if second.Result.IsError {
		t.Fatalf("valid second call returned isError: %+v", second.Result)
	}
	b, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result after self-correction: %v", err)
	}
	if !jsonEqual(t, b, json.RawMessage(`{"answer":"ok"}`)) {
		t.Fatalf("result = %s", b)
	}
}

func TestUnknownToolReturnsIsError(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	lines := serve(t, answerSchema, resultPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	})
	call := decodeCall(t, lines[1])
	if !call.Result.IsError {
		t.Fatalf("unknown tool did not return isError: %+v", call.Result)
	}
	if !strings.Contains(callText(call), "tool not found") {
		t.Fatalf("unexpected message: %q", callText(call))
	}
}

func callText(c rpcCallResult) string {
	if len(c.Result.Content) == 0 {
		return ""
	}
	return c.Result.Content[0].Text
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	return string(an) == string(bn)
}
