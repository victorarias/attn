package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// headlessRunner is the seam over agent.RunHeadlessTask so driverAgent is
// testable without spawning real binaries. The production implementation wraps
// the registered driver's RunHeadlessTask; tests inject a fake.
type headlessRunner interface {
	Run(ctx context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error)
}

// resultToolName is the single MCP tool the result sink exposes.
const resultToolName = "return_result"

// defaultDriverAgentRetries is the OUTER (engine-level) re-spawn bound, beyond
// the sink's in-turn isError self-correction. It handles a turn that ENDED
// without ever writing a valid result (missing call), not a malformed call.
const defaultDriverAgentRetries = 2

// driverAgent is the real AgentStub: it spawns a headless subagent per agent()
// call. With a schema it wires the schema-validating return_result sink and
// reads the written result file; without a schema it returns the child's
// captured final text. It honors the engine's error->null contract: a terminal
// failure (no result after retries, or persistent non-zero exit) returns a Go
// error, which the engine converts to a null resolution — it never throws past
// the engine boundary.
type driverAgent struct {
	runner     headlessRunner
	provider   string // "codex" | "claude" — diagnostics only
	executable string // resolved agent binary path
	model      string
	attnExec   string // path to the attn binary (hosts the result-sink subcommand)
	runTmpDir  string // per-call schema/result files live here
	maxRetries int
}

// DriverAgentOptions configures NewDriverAgent.
type DriverAgentOptions struct {
	// Provider is the agent name ("codex" or "claude").
	Provider string
	// Executable optionally overrides the resolved agent binary path.
	Executable string
	// Model is the model passed to the headless agent.
	Model string
	// RunTmpDir is the directory for per-call schema/result scratch files. It is
	// created if missing. Empty => a fresh os.MkdirTemp dir owned by the agent.
	RunTmpDir string
	// AttnExecutable optionally overrides the attn binary path (defaults to the
	// current process executable).
	AttnExecutable string
	// MaxRetries is the OUTER re-spawn bound. 0 => defaultDriverAgentRetries.
	MaxRetries int
	// Runner optionally injects a headlessRunner (tests). nil => the real driver.
	Runner headlessRunner
}

var _ AgentStub = (*driverAgent)(nil)

// NewDriverAgent constructs a driverAgent that spawns real subagents. No
// production caller wires it yet (that is E4's `attn workflow run`); E2 delivers
// the constructable agent + its tests.
func NewDriverAgent(opts DriverAgentOptions) (*driverAgent, error) {
	provider := strings.TrimSpace(opts.Provider)
	if provider == "" {
		return nil, errors.New("driver agent: provider is required")
	}

	runner := opts.Runner
	if runner == nil {
		driver := agentdriver.Get(provider)
		if driver == nil {
			return nil, fmt.Errorf("driver agent: unknown provider %q", provider)
		}
		hp, ok := driver.(agentdriver.HeadlessTaskProvider)
		if !ok {
			return nil, fmt.Errorf("driver agent: provider %q does not support headless tasks", provider)
		}
		runner = headlessProviderRunner{provider: hp}
	}

	executable := strings.TrimSpace(opts.Executable)
	if executable == "" && opts.Runner == nil {
		// Resolve a real binary only when using the real runner; tests inject a
		// fake runner and may pass any (or no) executable.
		driver := agentdriver.Get(provider)
		resolved := driver.ResolveExecutable("")
		path, err := exec.LookPath(resolved)
		if err != nil {
			return nil, fmt.Errorf("driver agent: resolve %s executable: %w", provider, err)
		}
		executable = path
	}

	attnExec := strings.TrimSpace(opts.AttnExecutable)
	if attnExec == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("driver agent: resolve attn executable: %w", err)
		}
		attnExec = self
	}

	runTmpDir := strings.TrimSpace(opts.RunTmpDir)
	if runTmpDir == "" {
		dir, err := os.MkdirTemp("", "attn-workflow-agent-*")
		if err != nil {
			return nil, fmt.Errorf("driver agent: create run temp dir: %w", err)
		}
		runTmpDir = dir
	} else if err := os.MkdirAll(runTmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("driver agent: create run temp dir: %w", err)
	}

	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultDriverAgentRetries
	}

	return &driverAgent{
		runner:     runner,
		provider:   provider,
		executable: executable,
		model:      strings.TrimSpace(opts.Model),
		attnExec:   attnExec,
		runTmpDir:  runTmpDir,
		maxRetries: maxRetries,
	}, nil
}

// Run implements AgentStub. With a schema it drives the return_result sink and
// returns the validated result bytes; without a schema it returns the child's
// captured final text encoded as a JSON string. Terminal failure returns a Go
// error (engine -> null), never a thrown rejection.
func (d *driverAgent) Run(ordinal OrdinalPath, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	ctx := context.Background()
	if len(schema) == 0 {
		return d.runNoSchema(ctx, prompt)
	}
	return d.runWithSchema(ctx, ordinal, prompt, schema)
}

// runNoSchema spawns a sink-less read-only agent and returns its final text,
// JSON-encoded so the engine decodes it back to a JS string.
func (d *driverAgent) runNoSchema(ctx context.Context, prompt string) (json.RawMessage, error) {
	req := agentdriver.HeadlessTaskRequest{
		Executable: d.executable,
		Model:      d.model,
		Prompt:     prompt,
		WorkDir:    d.runTmpDir,
	}
	res, err := d.runner.Run(ctx, req)
	if err != nil {
		// Terminal failure -> null (the engine maps a non-nil error to null).
		return nil, fmt.Errorf("headless agent failed: %s", diagnosticsOf(res, err))
	}
	encoded, encErr := json.Marshal(res.Text)
	if encErr != nil {
		return nil, fmt.Errorf("encode agent text: %w", encErr)
	}
	return encoded, nil
}

// runWithSchema wires the return_result sink, spawns the agent, and reads the
// validated result file. On a missing result (the model never called the tool)
// or a non-zero exit with no file, it re-spawns with a corrective prompt up to
// maxRetries. Retries exhausted with no file -> error (engine -> null).
func (d *driverAgent) runWithSchema(ctx context.Context, ordinal OrdinalPath, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	base := ordinalFileBase(ordinal)
	schemaPath := filepath.Join(d.runTmpDir, base+".schema.json")
	resultPath := filepath.Join(d.runTmpDir, base+".result.json")
	defer os.Remove(schemaPath)
	defer os.Remove(resultPath)

	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return nil, fmt.Errorf("write result schema: %w", err)
	}
	// A stale result file from a prior call at the same ordinal would be read as a
	// false success; clear it before the first spawn.
	_ = os.Remove(resultPath)

	var lastDiag string
	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		fullPrompt := prompt + schemaCallInstruction
		if attempt > 0 {
			fullPrompt = prompt + correctiveInstruction
		}

		req := agentdriver.HeadlessTaskRequest{
			Executable:    d.executable,
			Model:         d.model,
			Prompt:        fullPrompt,
			WorkDir:       d.runTmpDir,
			MCPServerName: "attn_workflow_result",
			ToolName:      resultToolName,
			Schema:        schema,
			ResultPath:    resultPath,
			MCPServerCommand: d.attnExec,
			MCPServerArgs: []string{
				"_workflow-result-mcp",
				"--tool-name", resultToolName,
				"--schema-file", schemaPath,
				"--result-file", resultPath,
			},
		}

		res, runErr := d.runner.Run(ctx, req)
		if runErr != nil {
			lastDiag = diagnosticsOf(res, runErr)
		} else {
			lastDiag = ""
		}

		// A written, valid result file is success regardless of the exit code: the
		// sink validated it in-turn, so it is schema-valid by construction. A
		// non-zero exit AFTER a valid write is not a failure.
		if bytes, ok := readResultFile(resultPath); ok {
			return bytes, nil
		}
		// No file: detect-missing. Loop to re-spawn with a corrective prompt.
	}

	if lastDiag == "" {
		lastDiag = "agent never produced a schema-valid result"
	}
	return nil, fmt.Errorf("headless agent produced no result after %d attempts: %s", d.maxRetries+1, lastDiag)
}

const schemaCallInstruction = "\n\nWhen you have the final answer, you MUST call the `return_result` tool exactly once with a JSON object that satisfies the provided schema. Do not reply in plain text; the run only completes when `return_result` is called with a schema-valid object."

const correctiveInstruction = "\n\nYour previous attempt did not produce a result: you did not call `return_result` with a schema-valid object. Call the `return_result` tool now, exactly once, with a JSON object matching the provided schema."

// readResultFile reads a written result file and returns its bytes when present
// and non-empty. A missing file (the model never called return_result) returns
// ok=false — the detect-missing signal.
func readResultFile(path string) (json.RawMessage, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil, false
	}
	return json.RawMessage(b), true
}

// ordinalFileBase derives a filesystem-safe base name from an ordinal (which
// contains '/', ':', '#', '@'). A short hash keeps it unique and bounded.
func ordinalFileBase(ordinal OrdinalPath) string {
	sum := sha256.Sum256([]byte(ordinal.String()))
	return "call-" + hex.EncodeToString(sum[:])[:16]
}

func diagnosticsOf(res agentdriver.HeadlessTaskResult, err error) string {
	if d := strings.TrimSpace(res.Diagnostics); d != "" {
		return d
	}
	if err != nil {
		return err.Error()
	}
	return "unknown failure"
}

// headlessProviderRunner adapts a HeadlessTaskProvider to headlessRunner.
type headlessProviderRunner struct {
	provider agentdriver.HeadlessTaskProvider
}

func (r headlessProviderRunner) Run(ctx context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
	return r.provider.RunHeadlessTask(ctx, req)
}
