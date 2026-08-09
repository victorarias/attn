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

// headlessRunner is the seam over agent.RunHeadlessTask, so driverAgent is
// testable without spawning real binaries.
type headlessRunner interface {
	Run(ctx context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error)
}

const resultToolName = "return_result"

// defaultDriverAgentRetries is the OUTER (engine-level) re-spawn bound: a turn
// that ENDED without a valid result, not a malformed call (the sink
// self-corrects those in-turn).
const defaultDriverAgentRetries = 2

// driverAgent is the real AgentStub: one headless subagent per agent() call.
// With a schema it wires the return_result sink and reads the result file;
// without one it returns the child's final text.
type driverAgent struct {
	runner     headlessRunner
	executable string
	model      string
	attnExec   string // hosts the result-sink subcommand
	runTmpDir  string // per-call schema/result files live here
	maxRetries int

	// workingTree is the writable CWD handed to each subagent (empty =>
	// runTmpDir). Scratch always lives under runTmpDir, so the working tree
	// stays clean of attn files.
	workingTree string
	// sessionMCPServers attach IN ADDITION to the return_result sink.
	sessionMCPServers []agentdriver.MCPServerSpec
	// log records retained worktrees so a kept, mutated one can be found later.
	log func(format string, args ...interface{})
}

// DriverAgentOptions configures NewDriverAgent; zero fields take the defaults.
type DriverAgentOptions struct {
	Provider   string // "codex" or "claude"
	Executable string
	Model      string
	// RunTmpDir is the per-call scratch dir, created if missing.
	RunTmpDir      string
	AttnExecutable string
	MaxRetries     int
	// Runner injects a headlessRunner for tests; nil => the real driver.
	Runner headlessRunner
	// WorkingTree is the writable CWD handed to each subagent; scratch files
	// stay in RunTmpDir regardless.
	WorkingTree       string
	SessionMCPServers []agentdriver.MCPServerSpec
	LogFunc           func(format string, args ...interface{})
}

var _ AgentStub = (*driverAgent)(nil)

// NewDriverAgent constructs a driverAgent that spawns real subagents.
func NewDriverAgent(opts DriverAgentOptions) (*driverAgent, error) {
	provider := strings.TrimSpace(opts.Provider)
	if provider == "" {
		return nil, errors.New("driver agent: provider is required")
	}

	runner := opts.Runner
	executable := strings.TrimSpace(opts.Executable)
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

		// Resolve a real binary only for the real runner; a fake may pass none.
		if executable == "" {
			resolved := driver.ResolveExecutable("")
			path, err := exec.LookPath(resolved)
			if err != nil {
				return nil, fmt.Errorf("driver agent: resolve %s executable: %w", provider, err)
			}
			executable = path
		}
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

func (d *driverAgent) defaultRunCWD() string {
	if d.workingTree != "" {
		return d.workingTree
	}
	return d.runTmpDir
}

// Run implements AgentStub. Terminal failure returns a Go error (engine -> null),
// never a thrown rejection. Isolation "" runs in the shared working tree,
// "worktree" in a fresh git worktree so parallel mutating agents don't collide.
func (d *driverAgent) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	model := d.model
	if call.Model != "" {
		model = call.Model
	}
	if call.Isolation == "worktree" {
		return d.runIsolated(ctx, call, model)
	}
	return d.runInCWD(ctx, call, d.defaultRunCWD(), model)
}

// runInCWD is the single body both isolation modes flow through, so isolation
// only changes WHERE the subagent runs.
func (d *driverAgent) runInCWD(ctx context.Context, call AgentCall, cwd, model string) (json.RawMessage, error) {
	if len(call.Schema) == 0 {
		return d.runNoSchema(ctx, call.Prompt, cwd, model)
	}
	return d.runWithSchema(ctx, call.Ordinal, call.Prompt, call.Schema, cwd, model)
}

// runIsolated runs in a fresh worktree branched off the ordinal: a git-clean
// worktree is removed afterward, a dirtied one is KEPT and its path logged.
// Scratch stays under runTmpDir, so cleanliness reflects only the agent's edits.
func (d *driverAgent) runIsolated(ctx context.Context, call AgentCall, model string) (json.RawMessage, error) {
	repoRoot := git.ResolveMainRepoPath(d.defaultRunCWD())
	if repoRoot == "" {
		return nil, fmt.Errorf("worktree isolation: cannot resolve repo root from working tree %q", d.defaultRunCWD())
	}

	branch := worktreeBranchFor(call.Ordinal)
	path := git.GenerateWorktreePath(repoRoot, branch)
	if err := git.CreateWorktree(repoRoot, branch, path); err != nil {
		// Fail closed: falling back to the shared tree would let parallel
		// mutators collide.
		return nil, fmt.Errorf("worktree isolation: create worktree for %s: %w", call.Ordinal.String(), err)
	}

	result, runErr := d.runInCWD(ctx, call, path, model)

	// Cleanup applies regardless of the run outcome; a failed run that dirtied
	// the tree still keeps its worktree.
	clean, cleanErr := git.IsWorktreeClean(path)
	switch {
	case cleanErr != nil: // keep it rather than discard possible mutations
		d.logf("worktree isolation: could not determine cleanliness of %q (%v); keeping it", path, cleanErr)
	case clean:
		if err := git.DeleteWorktree(repoRoot, path, true); err != nil {
			d.logf("worktree isolation: remove clean worktree %q failed: %v", path, err)
		} else {
			_ = git.DeleteBranch(repoRoot, branch, true)
		}
	default:
		d.logf("worktree isolation: agent left changes; keeping worktree %q (branch %s)", path, branch)
	}

	return result, runErr
}

// worktreeBranchFor derives a filesystem-safe branch name from the ordinal,
// which already disambiguates every call.
func worktreeBranchFor(ordinal OrdinalPath) string {
	sum := sha256.Sum256([]byte(ordinal.String()))
	return "attn-wf/" + hex.EncodeToString(sum[:])[:12]
}

func (d *driverAgent) logf(format string, args ...interface{}) {
	if d.log == nil {
		return
	}
	d.log(format, args...)
}

// runNoSchema returns the agent's final text JSON-encoded, so the engine decodes
// it back to a JS string.
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
		return nil, fmt.Errorf("headless agent failed: %s", diagnosticsOf(res, err))
	}
	encoded, encErr := json.Marshal(res.Text)
	if encErr != nil {
		return nil, fmt.Errorf("encode agent text: %w", encErr)
	}
	return encoded, nil
}

// runWithSchema wires the return_result sink and reads the validated result
// file, re-spawning with a corrective prompt up to maxRetries when none was
// written; exhausted retries return an error.
func (d *driverAgent) runWithSchema(ctx context.Context, ordinal OrdinalPath, prompt string, schema json.RawMessage, cwd, model string) (json.RawMessage, error) {
	base := ordinalFileBase(ordinal)
	schemaPath := filepath.Join(d.runTmpDir, base+".schema.json")
	resultPath := filepath.Join(d.runTmpDir, base+".result.json")
	defer os.Remove(schemaPath)
	defer os.Remove(resultPath)

	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return nil, fmt.Errorf("write result schema: %w", err)
	}
	// A stale result file at the same ordinal would read as a false success.
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
			// Scratch paths stay absolute so the sink resolves them from any CWD.
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

		// A written result file is success regardless of exit code: the sink
		// validated it in-turn.
		if bytes, ok := readResultFile(resultPath); ok {
			return bytes, nil
		}
	}

	if lastDiag == "" {
		lastDiag = "agent never produced a schema-valid result"
	}
	return nil, fmt.Errorf("headless agent produced no result after %d attempts: %s", d.maxRetries+1, lastDiag)
}

const schemaCallInstruction = "\n\nWhen you have the final answer, you MUST call the `return_result` tool exactly once with a JSON object that satisfies the provided schema. Do not reply in plain text; the run only completes when `return_result` is called with a schema-valid object."

const correctiveInstruction = "\n\nYour previous attempt did not produce a result: you did not call `return_result` with a schema-valid object. Call the `return_result` tool now, exactly once, with a JSON object matching the provided schema."

// readResultFile reports ok=false when the file is missing or blank.
func readResultFile(path string) (json.RawMessage, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil, false
	}
	return json.RawMessage(b), true
}

// ordinalFileBase hashes an ordinal, which contains '/', ':', '#', '@', into a
// filesystem-safe base name.
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
