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
	"github.com/victorarias/attn/internal/git"
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

	// workingTree is the writable CWD handed to each subagent (native parity:
	// agent() shares a writable working tree). Empty => fall back to runTmpDir so
	// the agent still has a valid cwd. Scratch (schema/result/last-msg) always
	// lives under runTmpDir, so a working tree here stays clean of attn files.
	workingTree string
	// sessionMCPServers are the workflow session's MCP servers, attached to every
	// subagent IN ADDITION to the return_result sink (native parity).
	sessionMCPServers []agentdriver.MCPServerSpec
	// log is an optional diagnostics sink (e.g. the daemon logger). nil => silent.
	// The worktree lifecycle uses it to record retained worktrees so a kept,
	// mutated worktree can be found after the run.
	log func(format string, args ...interface{})
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
	// WorkingTree is the writable working tree handed to each subagent as its CWD
	// (native parity: agent() shares a writable tree). Empty => RunTmpDir is used
	// as the CWD so subagents still run somewhere valid. Scratch files stay in
	// RunTmpDir regardless, keeping the working tree free of attn artifacts.
	WorkingTree string
	// SessionMCPServers are the workflow session's MCP servers, threaded into each
	// subagent request's ExtraMCPServers so their tools attach in addition to
	// return_result (native parity). Empty is acceptable.
	SessionMCPServers []agentdriver.MCPServerSpec
	// LogFunc is an optional diagnostics sink for the worktree lifecycle (retained
	// worktree paths, cleanup failures). nil => silent.
	LogFunc func(format string, args ...interface{})
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
		runner:            runner,
		provider:          provider,
		executable:        executable,
		model:             strings.TrimSpace(opts.Model),
		attnExec:          attnExec,
		runTmpDir:         runTmpDir,
		maxRetries:        maxRetries,
		workingTree:       strings.TrimSpace(opts.WorkingTree),
		sessionMCPServers: opts.SessionMCPServers,
		log:               opts.LogFunc,
	}, nil
}

// defaultRunCWD is the writable working directory handed to each subagent on the
// non-isolated path: the working tree when set, else runTmpDir so the subagent
// still runs somewhere valid.
func (d *driverAgent) defaultRunCWD() string {
	if d.workingTree != "" {
		return d.workingTree
	}
	return d.runTmpDir
}

// Run implements AgentStub. With a schema it drives the return_result sink and
// returns the validated result bytes; without a schema it returns the child's
// captured final text encoded as a JSON string. Terminal failure returns a Go
// error (engine -> null), never a thrown rejection.
//
// Execution context is per-call:
//   - call.Isolation == "" (default): CWD is the shared writable working tree —
//     byte-identical to E3.
//   - call.Isolation == "worktree": the call runs in a FRESH git worktree as CWD
//     so parallel mutating agents don't collide (see runIsolated). The structured
//     result still returns via return_result; the worktree is the consumed side
//     effect. It is auto-removed iff the agent left it git-clean, else KEPT.
//
// call.Model overrides d.model for this call when non-empty. call.AgentType is
// carried for native parity but currently unused.
func (d *driverAgent) Run(call AgentCall) (json.RawMessage, error) {
	ctx := context.Background()
	model := d.model
	if call.Model != "" {
		model = call.Model
	}
	if call.Isolation == "worktree" {
		return d.runIsolated(ctx, call, model)
	}
	return d.runInCWD(ctx, call, d.defaultRunCWD(), model)
}

// runInCWD dispatches the schema / no-schema path against an explicit CWD and
// model. It is the single body both the default and worktree-isolated paths flow
// through, so the only thing isolation changes is WHERE the subagent runs.
func (d *driverAgent) runInCWD(ctx context.Context, call AgentCall, cwd, model string) (json.RawMessage, error) {
	if len(call.Schema) == 0 {
		return d.runNoSchema(ctx, call.Prompt, cwd, model)
	}
	return d.runWithSchema(ctx, call.Ordinal, call.Prompt, call.Schema, cwd, model)
}

// runIsolated implements isolation:'worktree'. It resolves the repo root from the
// shared working tree, creates a fresh worktree on a unique branch derived from
// the call's ordinal, runs the subagent with that worktree as CWD, and then —
// success OR failure — applies the §6/§7 cleanup rule:
//
//   - the agent left the worktree git-clean  -> remove it (and best-effort prune
//     the branch): a no-op isolated call leaves nothing behind.
//   - the agent made changes                 -> KEEP it: the mutations are the
//     consumed side effect and must not be discarded; the retained path is logged.
//
// On CreateWorktree failure it returns a clear error (engine -> null) rather than
// silently running in the wrong CWD. Scratch (schema/result) files stay under
// runTmpDir as always, so the worktree's cleanliness reflects ONLY the subagent's
// own edits, never attn artifacts.
func (d *driverAgent) runIsolated(ctx context.Context, call AgentCall, model string) (json.RawMessage, error) {
	repoRoot := git.ResolveMainRepoPath(d.defaultRunCWD())
	if repoRoot == "" {
		return nil, fmt.Errorf("worktree isolation: cannot resolve repo root from working tree %q", d.defaultRunCWD())
	}

	branch := worktreeBranchFor(call.Ordinal)
	path := git.GenerateWorktreePath(repoRoot, branch)
	if err := git.CreateWorktree(repoRoot, branch, path); err != nil {
		// Fail closed: never fall back to the shared tree — that would defeat the
		// whole point of isolation (parallel mutators must not collide).
		return nil, fmt.Errorf("worktree isolation: create worktree for %s: %w", call.Ordinal.String(), err)
	}

	result, runErr := d.runInCWD(ctx, call, path, model)

	// Cleanup applies regardless of the run outcome: a failed run that still dirtied
	// the tree keeps its worktree (the user may inspect partial work); a clean run
	// (success or failure) leaves nothing behind.
	clean, cleanErr := git.IsWorktreeClean(path)
	switch {
	case cleanErr != nil:
		// Could not determine cleanliness: keep the worktree to avoid discarding
		// possible mutations, and surface the path so it can be found later.
		d.logf("worktree isolation: could not determine cleanliness of %q (%v); keeping it", path, cleanErr)
	case clean:
		if err := git.DeleteWorktree(repoRoot, path, true); err != nil {
			d.logf("worktree isolation: remove clean worktree %q failed: %v", path, err)
		} else {
			// Best-effort branch prune; a leftover branch is harmless but untidy.
			_ = git.DeleteBranch(repoRoot, branch, true)
		}
	default:
		d.logf("worktree isolation: agent left changes; keeping worktree %q (branch %s)", path, branch)
	}

	return result, runErr
}

// worktreeBranchFor derives a unique, filesystem-safe branch name for an isolated
// call from its ordinal. The ordinal already disambiguates every call site /
// parallel slot / pipeline stage in a run, so a short hash of it gives a stable,
// collision-free branch per call.
func worktreeBranchFor(ordinal OrdinalPath) string {
	sum := sha256.Sum256([]byte(ordinal.String()))
	return "attn-wf/" + hex.EncodeToString(sum[:])[:12]
}

// logf emits a driver diagnostic. It is nil-safe (no logger wired in tests) and
// kept lightweight so the worktree lifecycle leaves a trail without a hard
// dependency on a logging sink.
func (d *driverAgent) logf(format string, args ...interface{}) {
	if d.log == nil {
		return
	}
	d.log(format, args...)
}

// runNoSchema spawns a sink-less read-only agent and returns its final text,
// JSON-encoded so the engine decodes it back to a JS string. cwd is the per-call
// writable working directory (the shared tree, or an isolated worktree); model is
// the per-call model.
func (d *driverAgent) runNoSchema(ctx context.Context, prompt, cwd, model string) (json.RawMessage, error) {
	req := agentdriver.HeadlessTaskRequest{
		Executable:      d.executable,
		Model:           model,
		Prompt:          prompt,
		WorkDir:         d.runTmpDir,
		CWD:             cwd,
		Sandbox:         "workspace-write",
		ExtraMCPServers: d.sessionMCPServers,
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
func (d *driverAgent) runWithSchema(ctx context.Context, ordinal OrdinalPath, prompt string, schema json.RawMessage, cwd, model string) (json.RawMessage, error) {
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
			Executable:       d.executable,
			Model:            model,
			Prompt:           fullPrompt,
			WorkDir:          d.runTmpDir,
			CWD:              cwd,
			Sandbox:          "workspace-write",
			MCPServerName:    "attn_workflow_result",
			ToolName:         resultToolName,
			Schema:           schema,
			ResultPath:       resultPath,
			MCPServerCommand: d.attnExec,
			// Scratch (schema/result) paths are absolute under runTmpDir; keep them
			// absolute so the sink resolves them regardless of the writable CWD.
			MCPServerArgs: []string{
				"_workflow-result-mcp",
				"--tool-name", resultToolName,
				"--schema-file", schemaPath,
				"--result-file", resultPath,
			},
			ExtraMCPServers: d.sessionMCPServers,
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
