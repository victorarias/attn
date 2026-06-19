package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workflow"
)

// cancelPollInterval is how often the cancel-watcher polls the daemon for a
// canceled status. Cancellation is COOPERATIVE VIA POLLING: the request/response
// socket client cannot read unsolicited control frames, so the engine process
// polls workflow_run_get and cancels its own root context when it observes a
// canceled run. (The daemon's relay registry is a best-effort bonus; this poll is
// the guarantee.)
const cancelPollInterval = 1 * time.Second

// runWorkflow dispatches `attn workflow <subcommand>`.
func runWorkflow() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" || os.Args[2] == "help" {
		writeWorkflowHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "run":
		runWorkflowRun(os.Args[3:])
	case "result":
		runWorkflowResult(os.Args[3:])
	case "show":
		runWorkflowShow(os.Args[3:])
	case "list":
		runWorkflowList(os.Args[3:])
	case "cancel":
		runWorkflowCancel(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "workflow: unknown command %q\n\n", os.Args[2])
		writeWorkflowHelp(os.Stderr)
		os.Exit(1)
	}
}

func writeWorkflowHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn workflow <command>

commands:
  run <script.js> [options]    run a workflow script (engine runs in this process)
  result <runId> [--wait]      print a run's terminal result as JSON
  show <runId>                 monitor a run: current phase, calls done/running,
                               and per-call status incl. the in-flight call
  list [--session <id>]        list runs (default session = ATTN_SESSION_ID)
  cancel <runId>               request cancellation of a run (cooperative)

run options:
  --args <json>                inline JSON args passed to the script
  --args-file <path>           read JSON args from a file (exclusive with --args)
  --wait                       run in the foreground and block until terminal
  --session <id>               attach the run to a session (default ATTN_SESSION_ID)
  --resume <runId>             resume a prior run, replaying its journaled prefix
  --harness <codex|claude>     subagent harness (default codex)
  --model <m>                  subagent model
`)
}

// --- run -------------------------------------------------------------------

// workflowRunArgs is the parsed `workflow run` flag set. It is produced by a pure
// parser so the parsing is unit-testable without touching the filesystem or env.
type workflowRunArgs struct {
	script     string
	argsJSON   string // resolved raw JSON args (from --args or --args-file), "" if none
	argsInline string // value of --args, for re-exec persistence decisions
	argsFile   string
	wait       bool
	session    string
	resume     string
	harness    string
	model      string
	runID      string // hidden: the detached child reuses the parent's runId
}

// parseWorkflowRunArgs parses `workflow run` flags. The positional <script.js> may
// appear before OR after the flags: the canonical form is `run <script.js>
// [flags]`, but Go's flag package stops at the first non-flag token, so we lift the
// script out of argv first and parse the remaining flags. envSession is the
// ATTN_SESSION_ID fallback (passed in so the parser stays pure/testable).
func parseWorkflowRunArgs(argv []string, envSession string) (workflowRunArgs, error) {
	script, rest, err := extractScriptArg(argv)
	if err != nil {
		return workflowRunArgs{}, err
	}

	fs := flag.NewFlagSet("workflow run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	argsInline := fs.String("args", "", "inline JSON args")
	argsFile := fs.String("args-file", "", "read JSON args from a file")
	wait := fs.Bool("wait", false, "run in the foreground and block until terminal")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	resume := fs.String("resume", "", "resume a prior run id")
	harness := fs.String("harness", "codex", "subagent harness (codex|claude)")
	model := fs.String("model", "", "subagent model")
	runID := fs.String("run-id", "", "") // hidden: reused by the detached child

	if err := fs.Parse(rest); err != nil {
		return workflowRunArgs{}, err
	}
	if fs.NArg() > 0 {
		return workflowRunArgs{}, fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}
	if strings.TrimSpace(*argsInline) != "" && strings.TrimSpace(*argsFile) != "" {
		return workflowRunArgs{}, errors.New("--args and --args-file are mutually exclusive")
	}

	out := workflowRunArgs{
		script:     script,
		argsInline: *argsInline,
		argsFile:   *argsFile,
		wait:       *wait,
		session:    strings.TrimSpace(*session),
		resume:     strings.TrimSpace(*resume),
		harness:    strings.TrimSpace(*harness),
		model:      strings.TrimSpace(*model),
		runID:      strings.TrimSpace(*runID),
	}
	if out.session == "" {
		out.session = strings.TrimSpace(envSession)
	}
	if out.harness == "" {
		out.harness = "codex"
	}
	return out, nil
}

// extractScriptArg pulls the single positional <script.js> out of argv (wherever
// it sits relative to the flags) and returns it plus the remaining flag args. The
// script is the first token that is not a flag and is not the value of a preceding
// value-taking flag. Errors when no positional or more than one is present.
func extractScriptArg(argv []string) (script string, rest []string, err error) {
	// Flags that consume the following token as their value (so that value is not
	// mistaken for the positional script).
	valueFlags := map[string]bool{
		"--args": true, "--args-file": true, "--session": true,
		"--resume": true, "--harness": true, "--model": true, "--run-id": true,
	}
	found := false
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if strings.HasPrefix(tok, "-") {
			rest = append(rest, tok)
			// Skip the value of a `--flag value` pair (but not `--flag=value`).
			if valueFlags[tok] && i+1 < len(argv) {
				rest = append(rest, argv[i+1])
				i++
			}
			continue
		}
		if found {
			return "", nil, fmt.Errorf("unexpected extra argument: %q", tok)
		}
		script = tok
		found = true
	}
	if !found {
		return "", nil, errors.New("missing <script.js> argument")
	}
	return script, rest, nil
}

// resolveWorkflowArgsJSON loads the raw JSON args from --args or --args-file. It
// returns "" when neither is set. A non-empty payload is validated as JSON.
func resolveWorkflowArgsJSON(a workflowRunArgs) (string, error) {
	raw := strings.TrimSpace(a.argsInline)
	if a.argsFile != "" {
		b, err := os.ReadFile(a.argsFile)
		if err != nil {
			return "", fmt.Errorf("read --args-file: %w", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" {
		return "", nil
	}
	if !json.Valid([]byte(raw)) {
		return "", errors.New("args is not valid JSON")
	}
	return raw, nil
}

func runWorkflowRun(argv []string) {
	parsed, err := parseWorkflowRunArgs(argv, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n\n", err)
		writeWorkflowHelp(os.Stderr)
		os.Exit(2)
	}

	argsJSON, err := resolveWorkflowArgsJSON(parsed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(2)
	}
	parsed.argsJSON = argsJSON

	source, err := os.ReadFile(parsed.script)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: read script: %v\n", err)
		os.Exit(1)
	}
	scriptHash := sha256Hex(source)

	c := client.New("")

	// Determine the run id: a detached child carries --run-id from the parent; a
	// resume reuses the resumed run id; otherwise mint a fresh one.
	runID := parsed.runID
	if runID == "" {
		if parsed.resume != "" {
			runID = parsed.resume
		} else {
			runID = newWorkflowRunID()
		}
	}

	// The hidden --run-id flag marks a detached child: it reuses the parent's run
	// id AND skips re-creating the initial upsert (the parent already created it).
	isDetachedChild := parsed.runID != ""

	if !parsed.wait {
		// Foreground caller without --wait: detach a self re-exec that runs the
		// engine to completion in the background, print the run id, and return.
		if err := detachWorkflowChild(c, parsed, runID, scriptHash, argsJSON); err != nil {
			fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(runID)
		return
	}

	exitCode := executeWorkflowRun(c, parsed, runID, string(source), scriptHash, argsJSON, isDetachedChild)
	os.Exit(exitCode)
}

// buildInitialWorkflowRun constructs the running-status run row sent at run start.
// The store's ON CONFLICT upsert replaces every column, so this carries the full
// header. Shared by the foreground run, the detached child, and the engine test so
// the initial-row shape has a single source of truth.
func buildInitialWorkflowRun(parsed workflowRunArgs, runID, scriptHash, argsJSON string) *protocol.WorkflowRun {
	now := string(protocol.TimestampNow())
	run := &protocol.WorkflowRun{
		RunID:      runID,
		ScriptPath: parsed.script,
		ScriptHash: scriptHash,
		Status:     protocol.WorkflowRunStatusRunning,
		Resumable:  true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	applyOptionalRunFields(run, parsed, argsJSON)
	return run
}

// executeWorkflowRun runs the engine to completion in the FOREGROUND, reporting
// progress to the daemon (initial + final run upserts, per-call upserts via the
// IPC journal), and prints the terminal result. It returns the process exit code.
func executeWorkflowRun(
	c workflowClient,
	parsed workflowRunArgs,
	runID, source, scriptHash, argsJSON string,
	skipInitialUpsert bool,
) int {
	if !skipInitialUpsert {
		if _, err := c.WorkflowRunUpsert(buildInitialWorkflowRun(parsed, runID, scriptHash, argsJSON)); err != nil {
			fmt.Fprintf(os.Stderr, "workflow run: report run start: %v\n", err)
			return 1
		}
	}

	stub, err := buildWorkflowStub(parsed)
	if err != nil {
		finishWorkflowRunFailure(c, runID, err.Error())
		return 1
	}

	return runWorkflowEngine(c, parsed, runID, source, argsJSON, stub)
}

// runWorkflowEngine drives the engine to completion against an already-built stub:
// it decodes args, wires the IPC journal + cancel watcher, runs (or resumes), sends
// the terminal upsert, prints the result, and returns the exit code. executeWorkflowRun
// supplies the real driver stub; tests inject a fake — so neither the engine wiring
// nor the finalize path is reimplemented outside this function.
func runWorkflowEngine(c workflowClient, parsed workflowRunArgs, runID, source, argsJSON string, stub workflow.AgentStub) int {
	// Parse args JSON into an `any` for the engine; keep argsJSON as the raw form.
	var argsAny any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &argsAny); err != nil {
			finishWorkflowRunFailure(c, runID, fmt.Sprintf("decode args: %v", err))
			return 1
		}
	}

	journal := NewIPCJournal(c, runID)
	engine := workflow.New(workflow.Config{
		Stub:    stub,
		Journal: journal,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopWatcher := startCancelWatcher(ctx, cancel, c, runID, cancelPollInterval)
	defer stopWatcher()

	var result workflow.RunResult
	if parsed.resume != "" {
		result, _ = engine.Resume(ctx, source, argsAny)
	} else {
		result, _ = engine.Run(ctx, source, argsAny)
	}

	final := finishWorkflowRun(c, runID, result)
	out := buildWorkflowResultOutput(final)
	printJSON(out)
	return workflowResultExitCode(final.Status)
}

// finishWorkflowRun sends the terminal run upsert derived from the engine result
// and returns the daemon's hydrated view. It read-modify-writes the existing run:
// the store's ON CONFLICT upsert replaces EVERY column with the supplied row, so
// the terminal upsert must carry the full header (script_path, created_at, etc.)
// to avoid clobbering it. Falls back to a synthesized run if the daemon round-trip
// fails, so the CLI still prints a result.
func finishWorkflowRun(c workflowClient, runID string, result workflow.RunResult) *protocol.WorkflowRun {
	run := loadRunForFinalize(c, runID)
	run.Status = mapRunStatus(result)
	now := string(protocol.TimestampNow())
	run.UpdatedAt = now
	run.CompletedAt = protocol.Ptr(now)

	if result.Value != nil {
		if b, err := json.Marshal(result.Value); err == nil {
			run.ResultJson = protocol.Ptr(string(b))
		}
	}
	if result.Err != nil {
		run.LastError = protocol.Ptr(result.Err.Error())
	}

	hydrated, err := c.WorkflowRunUpsert(run)
	if err == nil && hydrated != nil {
		return hydrated
	}
	return run
}

// finishWorkflowRunFailure reports a setup/decoding failure as a failed run,
// preserving the existing run header (see finishWorkflowRun on the full-row upsert
// requirement).
func finishWorkflowRunFailure(c workflowClient, runID, message string) {
	run := loadRunForFinalize(c, runID)
	run.Status = protocol.WorkflowRunStatusFailed
	now := string(protocol.TimestampNow())
	run.UpdatedAt = now
	run.CompletedAt = protocol.Ptr(now)
	run.LastError = protocol.Ptr(message)
	_, _ = c.WorkflowRunUpsert(run)
	fmt.Fprintf(os.Stderr, "workflow run: %s\n", message)
}

// loadRunForFinalize fetches the current run so a terminal upsert can preserve the
// header. A get failure or absent run yields a minimal run carrying just the id, so
// finalization still proceeds (the worst case re-writes only the id + terminal
// fields, never silently dropping a run we know about).
func loadRunForFinalize(c workflowClient, runID string) *protocol.WorkflowRun {
	existing, err := c.WorkflowRunGet(runID)
	if err == nil && existing != nil {
		// Drop the hydrated agent calls from the upsert payload: the daemon
		// re-persists every embedded call on a run upsert, which is redundant here
		// (calls are already journaled) and would re-broadcast them.
		existing.AgentCalls = nil
		return existing
	}
	return &protocol.WorkflowRun{RunID: runID, Resumable: true, CreatedAt: string(protocol.TimestampNow())}
}

// applyOptionalRunFields sets the optional run header fields shared by the
// initial upsert.
func applyOptionalRunFields(run *protocol.WorkflowRun, parsed workflowRunArgs, argsJSON string) {
	if strings.TrimSpace(argsJSON) != "" {
		run.ArgsJson = protocol.Ptr(argsJSON)
	}
	if parsed.session != "" {
		run.SessionID = protocol.Ptr(parsed.session)
	}
	if parsed.harness != "" {
		run.Harness = protocol.Ptr(parsed.harness)
	}
}

// mapRunStatus maps an engine RunResult to the persisted run status. An
// interruption is reported as canceled ONLY when it was a real cancellation (the
// cancel watcher tripping ctx); a watchdog-timeout interruption is a failure. The
// engine tags ctx-cancellation with the "cancelled" reason, so we key on that.
func mapRunStatus(result workflow.RunResult) protocol.WorkflowRunStatus {
	switch result.Status {
	case workflow.StatusCompleted:
		return protocol.WorkflowRunStatusCompleted
	case workflow.StatusInterrupted:
		if interruptedByCancel(result.Err) {
			return protocol.WorkflowRunStatusCanceled
		}
		return protocol.WorkflowRunStatusFailed
	default:
		return protocol.WorkflowRunStatusFailed
	}
}

// interruptedByCancel reports whether an interruption error came from ctx
// cancellation (the cancel watcher) rather than a watchdog timeout. The engine
// sets the "workflow cancelled" reason for ctx cancellation.
func interruptedByCancel(err error) bool {
	var ie *workflow.ErrInterrupted
	if errors.As(err, &ie) {
		return strings.Contains(strings.ToLower(ie.Reason), "cancel")
	}
	return false
}

// buildWorkflowStub constructs the real driver AgentStub for a run.
func buildWorkflowStub(parsed workflowRunArgs) (workflow.AgentStub, error) {
	tmpDir, err := os.MkdirTemp("", "attn-workflow-run-*")
	if err != nil {
		return nil, fmt.Errorf("create run temp dir: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	stub, err := workflow.NewDriverAgent(workflow.DriverAgentOptions{
		Provider:    parsed.harness,
		Model:       parsed.model,
		RunTmpDir:   tmpDir,
		WorkingTree: cwd,
		// Surface worktree-isolation lifecycle diagnostics (notably retained,
		// mutated worktrees) on the engine process's stderr so the operator can
		// find kept worktrees after the run.
		LogFunc: func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, "workflow run: "+format+"\n", args...)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build driver agent: %w", err)
	}
	return stub, nil
}

// startCancelWatcher launches the cooperative cancellation poller. Every interval
// it fetches the run; when the daemon reports Status=="canceled" it cancels the
// engine's root context, which propagates to in-flight subagent contexts. It
// returns a stop func that tears the watcher down. The interval is injectable so
// tests can poll fast and deterministically.
func startCancelWatcher(ctx context.Context, cancel context.CancelFunc, c workflowClient, runID string, interval time.Duration) func() {
	if interval <= 0 {
		interval = cancelPollInterval
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if observeCanceled(c, runID) {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// observeCanceled reports whether the daemon currently considers runID canceled.
// A get error is treated as not-canceled (transient socket failures must not abort
// a healthy run; the watcher retries on the next tick).
func observeCanceled(c workflowClient, runID string) bool {
	run, err := c.WorkflowRunGet(runID)
	if err != nil || run == nil {
		return false
	}
	return run.Status == protocol.WorkflowRunStatusCanceled
}

// detachWorkflowChild re-execs this binary as a foregrounded `workflow run ...
// --wait --run-id <runID>` child in a new session (Setsid) so the engine runs to
// completion after the parent returns. The parent creates the initial run upsert
// so the run is visible immediately, then leaves; the child skips re-creating it.
func detachWorkflowChild(c workflowClient, parsed workflowRunArgs, runID, scriptHash, argsJSON string) error {
	if _, err := c.WorkflowRunUpsert(buildInitialWorkflowRun(parsed, runID, scriptHash, argsJSON)); err != nil {
		return fmt.Errorf("report run start: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	childArgs := []string{"workflow", "run", parsed.script, "--wait", "--run-id", runID, "--harness", parsed.harness}
	if parsed.session != "" {
		childArgs = append(childArgs, "--session", parsed.session)
	}
	if parsed.model != "" {
		childArgs = append(childArgs, "--model", parsed.model)
	}
	if parsed.resume != "" {
		childArgs = append(childArgs, "--resume", parsed.resume)
	}
	// Persist args to a temp file for the child to avoid argv quoting traps for
	// arbitrary JSON. The child reads it back via --args-file.
	if strings.TrimSpace(argsJSON) != "" {
		f, err := os.CreateTemp("", "attn-workflow-args-*.json")
		if err != nil {
			return fmt.Errorf("persist args for child: %w", err)
		}
		if _, err := f.WriteString(argsJSON); err != nil {
			_ = f.Close()
			return fmt.Errorf("persist args for child: %w", err)
		}
		_ = f.Close()
		childArgs = append(childArgs, "--args-file", f.Name())
	}

	cmd := exec.Command(executable, childArgs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// --- result ----------------------------------------------------------------

// workflowResultOutput is the frozen `workflow result` JSON shape. Field order is
// part of the agent-facing contract; keep it stable.
type workflowResultOutput struct {
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        string          `json:"error,omitempty"`
	Phase        string          `json:"phase,omitempty"`
	CallsTotal   int             `json:"calls_total"`
	CallsDone    int             `json:"calls_done"`
	CallsRunning int             `json:"calls_running"`
}

func runWorkflowResult(argv []string) {
	fs := flag.NewFlagSet("workflow result", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wait := fs.Bool("wait", false, "poll until the run reaches a terminal status")
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(os.Stderr, "workflow result: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: attn workflow result <runId> [--wait]")
		os.Exit(2)
	}
	runID := fs.Arg(0)
	c := client.New("")

	run, err := fetchWorkflowRun(c, runID, *wait)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow result: %v\n", err)
		os.Exit(1)
	}
	if run == nil {
		fmt.Fprintf(os.Stderr, "workflow result: run %q not found\n", runID)
		os.Exit(1)
	}

	out := buildWorkflowResultOutput(run)
	printJSON(out)
	os.Exit(workflowResultExitCode(run.Status))
}

// fetchWorkflowRun gets a run, optionally polling until it reaches a terminal
// status when wait is set.
func fetchWorkflowRun(c workflowClient, runID string, wait bool) (*protocol.WorkflowRun, error) {
	run, err := c.WorkflowRunGet(runID)
	if err != nil {
		return nil, err
	}
	if !wait || run == nil || isTerminalRunStatus(run.Status) {
		return run, nil
	}
	for {
		time.Sleep(cancelPollInterval)
		run, err = c.WorkflowRunGet(runID)
		if err != nil {
			return nil, err
		}
		if run == nil || isTerminalRunStatus(run.Status) {
			return run, nil
		}
	}
}

func isTerminalRunStatus(s protocol.WorkflowRunStatus) bool {
	switch s {
	case protocol.WorkflowRunStatusCompleted,
		protocol.WorkflowRunStatusFailed,
		protocol.WorkflowRunStatusCanceled:
		return true
	default:
		return false
	}
}

// buildWorkflowResultOutput projects a hydrated run into the frozen result shape.
func buildWorkflowResultOutput(run *protocol.WorkflowRun) workflowResultOutput {
	out := workflowResultOutput{
		Status: string(run.Status),
		Phase:  protocol.Deref(run.Phase),
	}
	if run.ResultJson != nil {
		out.Result = json.RawMessage(*run.ResultJson)
	}
	if run.LastError != nil {
		out.Error = *run.LastError
	}
	total, done, running := countWorkflowCalls(run.AgentCalls)
	out.CallsTotal = total
	out.CallsDone = done
	out.CallsRunning = running
	return out
}

// countWorkflowCalls returns (total, done, running) where done counts calls that
// have reached a terminal status (ok | errored | skipped) and running counts the
// in-flight calls. A running call is not done; the running count is what lets a
// polling agent tell "still progressing" from "not yet dispatched".
func countWorkflowCalls(calls []protocol.WorkflowAgentCall) (total, done, running int) {
	total = len(calls)
	for _, call := range calls {
		switch call.Status {
		case protocol.WorkflowAgentCallStatusOk,
			protocol.WorkflowAgentCallStatusErrored,
			protocol.WorkflowAgentCallStatusSkipped:
			done++
		case protocol.WorkflowAgentCallStatusRunning:
			running++
		}
	}
	return total, done, running
}

// workflowResultExitCode maps a terminal run status to a process exit code:
// completed -> 0, anything else -> 1. A pure function so the run/result paths
// share the exit decision and tests can assert it without os.Exit.
func workflowResultExitCode(status protocol.WorkflowRunStatus) int {
	if status == protocol.WorkflowRunStatusCompleted {
		return 0
	}
	return 1
}

// --- show ------------------------------------------------------------------

func runWorkflowShow(argv []string) {
	fs := flag.NewFlagSet("workflow show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(os.Stderr, "workflow show: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: attn workflow show <runId>")
		os.Exit(2)
	}
	runID := fs.Arg(0)
	c := client.New("")

	run, err := c.WorkflowRunGet(runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow show: %v\n", err)
		os.Exit(1)
	}
	if run == nil {
		fmt.Fprintf(os.Stderr, "workflow show: run %q not found\n", runID)
		os.Exit(1)
	}
	printJSON(buildWorkflowShowOutput(run))
}

// workflowShowOutput is the `workflow show` projection: a progress-forward view a
// polling agent can read to tell a healthy long run from a stalled one. The
// in-flight call appears as a first-class row with a climbing elapsed_seconds, so
// back-to-back polls visibly advance even while a single call runs for minutes.
type workflowShowOutput struct {
	RunID     string             `json:"run_id"`
	Status    string             `json:"status"`
	Phase     string             `json:"phase,omitempty"`
	Script    string             `json:"script"`
	Progress  workflowProgress   `json:"progress"`
	Calls     []workflowCallView `json:"calls"`
	Resumable bool               `json:"resumable"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	Error     string             `json:"error,omitempty"`
}

type workflowProgress struct {
	CallsTotal   int    `json:"calls_total"`
	CallsDone    int    `json:"calls_done"`
	CallsRunning int    `json:"calls_running"`
	Phase        string `json:"phase,omitempty"`
	Summary      string `json:"summary"`
}

type workflowCallView struct {
	Ordinal        string `json:"ordinal"`
	Status         string `json:"status"`
	Label          string `json:"label,omitempty"`
	Phase          string `json:"phase,omitempty"`
	Model          string `json:"model,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	ElapsedSeconds *int   `json:"elapsed_seconds,omitempty"`
	Error          string `json:"error,omitempty"`
}

// buildWorkflowShowOutput projects a hydrated run into the agent-legible progress
// view. Pure (time.Now only for a running call's elapsed) so it is unit-testable.
func buildWorkflowShowOutput(run *protocol.WorkflowRun) workflowShowOutput {
	total, done, running := countWorkflowCalls(run.AgentCalls)
	phase := protocol.Deref(run.Phase)
	calls := make([]workflowCallView, 0, len(run.AgentCalls))
	for _, call := range run.AgentCalls {
		calls = append(calls, workflowCallView{
			Ordinal:        call.Ordinal,
			Status:         string(call.Status),
			Label:          protocol.Deref(call.Label),
			Phase:          protocol.Deref(call.Phase),
			Model:          protocol.Deref(call.ResolvedModel),
			StartedAt:      protocol.Deref(call.StartedAt),
			ElapsedSeconds: workflowCallElapsedSeconds(call),
			Error:          protocol.Deref(call.Error),
		})
	}
	return workflowShowOutput{
		RunID:  run.RunID,
		Status: string(run.Status),
		Phase:  phase,
		Script: run.ScriptPath,
		Progress: workflowProgress{
			CallsTotal:   total,
			CallsDone:    done,
			CallsRunning: running,
			Phase:        phase,
			Summary:      workflowProgressSummary(total, done, running, phase),
		},
		Calls:     calls,
		Resumable: run.Resumable,
		CreatedAt: run.CreatedAt,
		UpdatedAt: run.UpdatedAt,
		Error:     protocol.Deref(run.LastError),
	}
}

// workflowProgressSummary is a one-line human/agent-legible progress string.
func workflowProgressSummary(total, done, running int, phase string) string {
	s := fmt.Sprintf("%d/%d done", done, total)
	if running > 0 {
		s += fmt.Sprintf(", %d running", running)
	}
	if phase != "" {
		s += fmt.Sprintf(" (phase: %s)", phase)
	}
	return s
}

// workflowCallElapsedSeconds returns the seconds a call has run: started->completed
// for a finished call, started->now for an in-flight one. It returns nil when no
// usable started_at is present, so a missing timestamp never renders a bogus value.
func workflowCallElapsedSeconds(call protocol.WorkflowAgentCall) *int {
	started := protocol.Timestamp(protocol.Deref(call.StartedAt)).Time()
	if started.IsZero() {
		return nil
	}
	end := protocol.Timestamp(protocol.Deref(call.CompletedAt)).Time()
	if end.IsZero() {
		if call.Status != protocol.WorkflowAgentCallStatusRunning {
			return nil
		}
		end = time.Now()
	}
	secs := int(end.Sub(started).Seconds())
	if secs < 0 {
		secs = 0
	}
	return &secs
}

// --- cancel ----------------------------------------------------------------

// runWorkflowCancel requests cancellation of a run. The daemon marks the run
// canceled and relays to the engine; the engine process (which polls
// workflow_run_get every cancelPollInterval) cancels its root context and any
// in-flight subagent contexts. This is the "moved-on agent halts a run" path
// from the design — the engine need not be this process. Prints the daemon's
// post-cancel view of the run.
func runWorkflowCancel(argv []string) {
	fs := flag.NewFlagSet("workflow cancel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(os.Stderr, "workflow cancel: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: attn workflow cancel <runId>")
		os.Exit(2)
	}
	runID := fs.Arg(0)
	c := client.New("")

	run, err := c.WorkflowRunCancel(runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow cancel: %v\n", err)
		os.Exit(1)
	}
	if run == nil {
		fmt.Fprintf(os.Stderr, "workflow cancel: run %q not found\n", runID)
		os.Exit(1)
	}
	printJSON(run)
}

// --- list ------------------------------------------------------------------

// workflowListEntry is the per-run summary printed by `workflow list`.
type workflowListEntry struct {
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	Script    string `json:"script"`
	CreatedAt string `json:"created_at"`
	Resumable bool   `json:"resumable"`
}

func runWorkflowList(argv []string) {
	fs := flag.NewFlagSet("workflow list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID; empty lists all)")
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(os.Stderr, "workflow list: %v\n", err)
		os.Exit(2)
	}
	sessionID := strings.TrimSpace(*session)
	if sessionID == "" {
		sessionID = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	c := client.New("")

	runs, err := c.WorkflowRunList(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow list: %v\n", err)
		os.Exit(1)
	}
	printJSON(buildWorkflowListEntries(runs))
}

func buildWorkflowListEntries(runs []protocol.WorkflowRun) []workflowListEntry {
	out := make([]workflowListEntry, 0, len(runs))
	for i := range runs {
		out = append(out, workflowListEntry{
			RunID:     runs[i].RunID,
			Status:    string(runs[i].Status),
			Phase:     protocol.Deref(runs[i].Phase),
			Script:    runs[i].ScriptPath,
			CreatedAt: runs[i].CreatedAt,
			Resumable: runs[i].Resumable,
		})
	}
	return out
}

// --- small helpers ---------------------------------------------------------

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newWorkflowRunID mints a fresh run id. It mirrors the crypto/rand hex scheme
// other attn ids use, prefixed for readability in logs/lists.
func newWorkflowRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic; fall back to a timestamp-free
		// unique-ish value. This path is effectively unreachable.
		return "wf-" + hex.EncodeToString([]byte(fmt.Sprintf("%p", &b)))
	}
	return "wf-" + hex.EncodeToString(b[:])
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func rawResultToPtr(r json.RawMessage) *string {
	if len(r) == 0 {
		return nil
	}
	s := string(r)
	return &s
}

func ptrToRawResult(s *string) json.RawMessage {
	if s == nil || *s == "" {
		return nil
	}
	return json.RawMessage(*s)
}
