package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/present"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/workflowresult"
	"github.com/victorarias/attn/internal/wrapper"
)

var (
	// Backward-compatible ldflags targets for builders that still inject
	// build metadata into the main package instead of internal/buildinfo.
	version           = ""
	buildTime         = ""
	sourceFingerprint = ""
	gitCommit         = ""
)

// hookInput represents the JSON input from agent hooks.
type hookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	ToolInput      json.RawMessage `json:"tool_input"`
	// BackgroundTasks is reported by Claude Code on the Stop payload: the set
	// of asynchronous tasks (background Workflows, background Bash) still
	// outstanding when the turn yields. Agents that do not emit this field
	// simply leave it empty.
	BackgroundTasks []backgroundTask `json:"background_tasks"`
	// SessionCrons is reported by Claude Code on the Stop payload: the set of
	// pending scheduled wakeups (created via CronCreate or /loop) that will
	// auto-resume this session later. It is present-but-empty when nothing is
	// scheduled. Agents that do not emit this field simply leave it empty.
	SessionCrons []sessionCron `json:"session_crons"`
}

// backgroundTask is one entry of hookInput.BackgroundTasks. Only Status is
// load-bearing here; Type is kept for diagnostics.
type backgroundTask struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// sessionCron is one entry of hookInput.SessionCrons. Detection keys only on
// presence (a non-empty list means the session is parked on a schedule); the
// remaining fields are decoded for diagnostics and possible future rendering.
// Verified against Claude Code 2.1.177: items carry exactly id/schedule
// (raw 5-field cron in local time)/recurring/prompt, with no status field —
// a fired or deleted cron drops out of the list entirely rather than lingering.
type sessionCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
	Prompt    string `json:"prompt"`
}

// hasActiveBackgroundTask reports whether the Stop payload still has background
// work in flight. Such a Stop is a yield, not a terminal stop: the agent will
// auto-resume when the work completes, so the session should stay "working"
// rather than be classified (which, mid-run, would read a not-yet-flushed
// transcript and mis-detect the session as unknown/idle).
func hasActiveBackgroundTask(input hookInput) bool {
	for _, t := range input.BackgroundTasks {
		if strings.EqualFold(strings.TrimSpace(t.Status), "running") {
			return true
		}
	}
	return false
}

// hasPendingSessionCron reports whether the Stop payload has a pending
// scheduled wakeup. Such a Stop is not terminal: the session is parked and will
// auto-resume when a cron fires, so it should read as "scheduled" rather than
// be classified (which would treat the parked turn as idle/unknown). Detection
// is presence-only — session_crons carries no per-item status, and a fired or
// deleted cron leaves the list entirely.
func hasPendingSessionCron(input hookInput) bool {
	return len(input.SessionCrons) > 0
}

// nonTerminalStopState returns the runtime state to report for a Stop that is
// not terminal, or "" when the Stop should fall through to normal daemon
// classification. A Stop is non-terminal when the turn yields with background
// work still in flight (auto-resumes on completion) or parked on a pending
// scheduled wakeup (auto-resumes when a cron fires); classifying such a Stop
// reads a not-yet-flushed transcript and mis-detects the session as
// idle/unknown. Running background work outranks a parked schedule, so a Stop
// with both stays "working"; once both drain, the next Stop classifies normally.
//
// relaxBackgroundWork drops the background-work -> "working" rule. It is set for
// the chief of staff: a chief that has merely armed a Monitor to watch its
// delegations (or a poll loop) is async-waiting, not working, and pegging it
// green makes the at-a-glance "is the chief actually working?" signal
// meaningless. With it set, background work no longer forces "working" (the Stop
// falls through to normal classification, settling idle/waiting), while a pending
// scheduled wakeup still parks "scheduled" (quiet/blue, not green).
func nonTerminalStopState(input hookInput, relaxBackgroundWork bool) string {
	switch {
	case !relaxBackgroundWork && hasActiveBackgroundTask(input):
		return protocol.StateWorking
	case hasPendingSessionCron(input):
		return protocol.StateScheduled
	default:
		return ""
	}
}

// sessionIsChiefOfStaff reports whether sessionID currently holds the
// profile-wide chief-of-staff role. The daemon owns the role, so the hook asks
// it (the same decorated session list `attn list` shows). Best-effort: any query
// error reports false, leaving the default (non-relaxed) busy detection intact.
func sessionIsChiefOfStaff(c *client.Client, sessionID string) bool {
	sessions, err := c.Query("")
	if err != nil {
		return false
	}
	for i := range sessions {
		if sessions[i].ID == sessionID {
			return sessions[i].ChiefOfStaff != nil && *sessions[i].ChiefOfStaff
		}
	}
	return false
}

// todoWriteInput represents the tool_input for TodoWrite
type todoWriteInput struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

func init() {
	applyLegacyBuildInfoOverrides()
}

func applyLegacyBuildInfoOverrides() {
	if buildinfo.Version == "dev" {
		if legacyVersion := strings.TrimSpace(version); legacyVersion != "" {
			buildinfo.Version = legacyVersion
		}
	}
	if buildinfo.BuildTime == "unknown" {
		if legacyBuildTime := strings.TrimSpace(buildTime); legacyBuildTime != "" {
			buildinfo.BuildTime = legacyBuildTime
		}
	}
	if buildinfo.SourceFingerprint == "unknown" {
		if legacySourceFingerprint := strings.TrimSpace(sourceFingerprint); legacySourceFingerprint != "" {
			buildinfo.SourceFingerprint = legacySourceFingerprint
		}
	}
	if buildinfo.GitCommit == "unknown" {
		if legacyGitCommit := strings.TrimSpace(gitCommit); legacyGitCommit != "" {
			buildinfo.GitCommit = legacyGitCommit
		}
	}
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "_workflow-result-mcp" {
		runWorkflowResultMCP(os.Args[2:])
		return
	}

	if isProtocolVersionCommand(os.Args) {
		runProtocolVersion()
		return
	}

	if isBuildInfoJSONCommand(os.Args) {
		runBuildInfoJSON()
		return
	}

	if isVersionCommand(os.Args) {
		runVersion()
		return
	}

	// `profile-env` is the self-recovery path: if ATTN_PROFILE is
	// currently typo'd, the user needs `attn profile-env --unset` (or
	// `profile-env <name>`) to fix their shell. Route it *before* the
	// global validation so an invalid env value doesn't trap them.
	if len(os.Args) >= 2 && os.Args[1] == "profile-env" {
		runProfileEnv()
		return
	}

	// Validate ATTN_PROFILE before we act on it. A typo'd profile would
	// silently fall back to default, which is exactly the kind of mistake
	// this whole feature exists to prevent.
	if err := config.ValidateProfile(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		maybePrintProfileBanner()
		runWrapper()
		return
	}

	switch os.Args[1] {
	case "daemon":
		maybePrintProfileBanner()
		runDaemonCommand()
	case "ws-relay":
		runWSRelay()
	case "pty-worker":
		runPTYWorker()
	case "workflow":
		maybePrintProfileBanner()
		runWorkflow()
	case "plugin":
		maybePrintProfileBanner()
		runPluginCommand()
	case "list":
		maybePrintProfileBanner()
		runList()
	case "presence":
		runPresence()
	case "delegate":
		maybePrintProfileBanner()
		runDelegate()
	case "ticket":
		maybePrintProfileBanner()
		runTicket()
	case "session":
		maybePrintProfileBanner()
		runSession()
	case "debug":
		maybePrintProfileBanner()
		runDebug()
	case "db":
		maybePrintProfileBanner()
		runDB()
	case "journal":
		maybePrintProfileBanner()
		runJournal()
	case "vision-check":
		// No banner: output must stay pure (stdout = answer only, or a single
		// --json line) for machine consumption by the calling agent.
		runVisionCheck()
	case "present":
		maybePrintProfileBanner()
		runPresent()
	case "workspace":
		maybePrintProfileBanner()
		runWorkspace()
	case "profile":
		// No banner: `attn profile resolve --field …` must print only the
		// value so the Makefile / harness can consume it cleanly.
		runProfile()
	case "open":
		maybePrintProfileBanner()
		runOpen()
	case "browser":
		maybePrintProfileBanner()
		runBrowser()
	case "help", "-h", "--help":
		runHelp()
	case "_hook-stop":
		runHookStop()
	case "_hook-session-start":
		runHookSessionStart()
	case "_hook-state":
		runHookState()
	case "_hook-todo":
		runHookTodo()
	default:
		// Check if it's a flag (starts with -)
		if len(os.Args[1]) > 0 && os.Args[1][0] == '-' {
			maybePrintProfileBanner()
			runWrapper()
		} else {
			fmt.Fprintf(os.Stderr, "attn %s: unknown command %q\n\n", buildinfo.Version, os.Args[1])
			writeHelp(os.Stderr)
			os.Exit(1)
		}
	}
}

// runWorkflowResultMCP serves the workflow engine's schema-validating result
// sink (one return_result tool). The schema is passed as a file path (not inline
// argv) to avoid shell/argv quoting traps for arbitrary JSON Schemas.
func runWorkflowResultMCP(args []string) {
	fs := flag.NewFlagSet("_workflow-result-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	toolName := fs.String("tool-name", "return_result", "the single MCP tool name")
	schemaPath := fs.String("schema-file", "", "JSON Schema file for the tool inputSchema (empty => permissive)")
	resultPath := fs.String("result-file", "", "atomic result output file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 ||
		strings.TrimSpace(*resultPath) == "" || strings.TrimSpace(*toolName) == "" {
		fmt.Fprintln(os.Stderr, "invalid workflow result MCP arguments")
		os.Exit(2)
	}
	var schema json.RawMessage
	if p := strings.TrimSpace(*schemaPath); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		schema = b
	}
	if err := workflowresult.ServeResultSink(
		context.Background(),
		*toolName,
		schema,
		*resultPath,
		os.Stdin,
		os.Stdout,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// maybePrintProfileBanner prints the profile banner to stderr when a
// non-default ATTN_PROFILE is active. Skipped for hook commands (called
// on every Claude action) and for silent CLI subcommands (ws-relay,
// pty-worker) that have their own protocols on stderr.
func maybePrintProfileBanner() {
	config.PrintProfileBanner(os.Stderr)
}

func isVersionCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "--version", "version":
		return true
	default:
		return false
	}
}

func isProtocolVersionCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--protocol-version"
}

func isBuildInfoJSONCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--build-info-json"
}

func runVersion() {
	applyLegacyBuildInfoOverrides()
	fmt.Println(buildinfo.Version)
}

func runProtocolVersion() {
	fmt.Println(protocol.ProtocolVersion)
}

func runBuildInfoJSON() {
	applyLegacyBuildInfoOverrides()
	printJSON(map[string]string{
		"version":           buildinfo.Version,
		"buildTime":         buildinfo.BuildTime,
		"sourceFingerprint": buildinfo.SourceFingerprint,
		"gitCommit":         buildinfo.GitCommit,
	})
}

func runPTYWorker() {
	fs := flag.NewFlagSet("pty-worker", flag.ExitOnError)
	var cfg ptyworker.Config
	var cols int
	var rows int
	fs.StringVar(&cfg.DaemonInstanceID, "daemon-instance-id", "", "daemon instance id")
	fs.StringVar(&cfg.SessionID, "session-id", "", "session id")
	fs.StringVar(&cfg.Agent, "agent", "", "session agent")
	fs.StringVar(&cfg.CWD, "cwd", "", "working directory")
	fs.IntVar(&cols, "cols", 80, "terminal cols")
	fs.IntVar(&rows, "rows", 24, "terminal rows")
	fs.StringVar(&cfg.Label, "label", "", "session label")
	fs.StringVar(&cfg.ResumeSessionID, "resume-session-id", "", "resume session id")
	fs.BoolVar(&cfg.ResumePicker, "resume-picker", false, "resume picker")
	fs.BoolVar(&cfg.YoloMode, "yolo-mode", false, "launch agent in yolo mode")
	fs.StringVar(&cfg.InitialPromptFile, "initial-prompt-file", "", "file containing the initial agent prompt")
	fs.StringVar(&cfg.ThemeForeground, "theme-foreground", "", "terminal foreground color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeBackground, "theme-background", "", "terminal background color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeCursor, "theme-cursor", "", "terminal cursor color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.Executable, "executable", "", "selected agent executable override")
	fs.StringVar(&cfg.ClaudeExecutable, "claude-executable", "", "claude executable override")
	fs.StringVar(&cfg.CodexExecutable, "codex-executable", "", "codex executable override")
	fs.StringVar(&cfg.CopilotExecutable, "copilot-executable", "", "copilot executable override")
	var externalCommandJSON string
	fs.StringVar(&externalCommandJSON, "external-command-json", "", "external plugin driver argv as JSON")
	fs.StringVar(&cfg.ExternalCWD, "external-cwd", "", "external plugin driver working directory")
	fs.StringVar(&cfg.RegistryPath, "registry-path", "", "registry path")
	fs.StringVar(&cfg.SocketPath, "socket-path", "", "socket path")
	fs.StringVar(&cfg.ControlToken, "control-token", "", "control token")
	fs.IntVar(&cfg.OwnerPID, "owner-pid", 0, "daemon owner pid")
	fs.StringVar(&cfg.OwnerStartedAt, "owner-started-at", "", "daemon owner started-at timestamp")
	fs.StringVar(&cfg.OwnerNonce, "owner-nonce", "", "daemon owner nonce")

	_ = fs.Parse(os.Args[2:])
	if externalCommandJSON != "" {
		if err := json.Unmarshal([]byte(externalCommandJSON), &cfg.ExternalCommand); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --external-command-json: %v\n", err)
			os.Exit(1)
		}
	}
	if externalEnvJSON := os.Getenv("ATTN_PTY_EXTERNAL_ENV"); externalEnvJSON != "" {
		if err := json.Unmarshal([]byte(externalEnvJSON), &cfg.ExternalEnv); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid ATTN_PTY_EXTERNAL_ENV: %v\n", err)
			os.Exit(1)
		}
	}
	if cols > 0 {
		if cols > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --cols must be <= 65535 (got %d)\n", cols)
			os.Exit(1)
		}
		cfg.Cols = uint16(cols)
	}
	if rows > 0 {
		if rows > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --rows must be <= 65535 (got %d)\n", rows)
			os.Exit(1)
		}
		cfg.Rows = uint16(rows)
	}
	cfg.Debug = config.DebugLevel() >= config.LogDebug
	cfg.Logf = func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[pty-worker] "+format+"\n", args...)
	}

	// Belt-and-suspenders: the daemon already scrubs these before spawning the
	// worker, but a worker spawned from any unscrubbed parent self-protects so
	// the leaked per-session agent env never reaches the PTY it spawns.
	if scrubbed := config.ScrubInheritedAgentSessionEnv(); len(scrubbed) > 0 {
		cfg.Logf("scrubbed inherited agent session env before startup: %v", scrubbed)
	}

	if err := ptyworker.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-worker error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemonCommand() {
	if len(os.Args) >= 3 && os.Args[2] == "ensure" {
		runDaemonEnsure()
		return
	}
	runDaemon()
}

func runDaemon() {
	socketPath := config.SocketPath()
	if err := config.ValidateDaemonIsolation(socketPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	d := daemon.New(socketPath)
	// Drop any per-session agent env (e.g. CLAUDE_CODE_SESSION_ID) inherited
	// when attn was launched from inside an agent session, before Start() warms
	// the login-shell env cache or spawns anything.
	d.ScrubInheritedAgentSessionEnv()
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemonEnsure() {
	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon ensure error: resolve executable: %v\n", err)
		os.Exit(1)
	}
	result, err := daemonctl.Ensure(context.Background(), binaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon ensure error: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func runWSRelay() {
	addr := net.JoinHostPort(config.WSBindAddress(), config.WSPort())
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ws-relay connect %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
	}()
	_, _ = io.Copy(os.Stdout, conn)
}

func runList() {
	warnIfDaemonVersionMismatch()
	c := client.New("")
	result, err := c.List("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding list: %v\n", err)
		os.Exit(1)
	}
}

func detectPresence() (sessionID string, present bool) {
	if os.Getenv("ATTN_INSIDE_APP") != "1" {
		return "", false
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID")), true
}

func runPresence() {
	warnIfDaemonVersionMismatch()
	sessionID, present := detectPresence()
	if !present {
		fmt.Println("not running inside attn")
		os.Exit(1)
	}
	if sessionID == "" {
		fmt.Println("running inside attn")
		return
	}
	fmt.Printf("running inside attn (session %s)\n", sessionID)
}

func runHelp() {
	writeHelp(os.Stdout)
}

func writeHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn <command>

commands:
  presence                          check whether the current shell runs inside attn
	  session instructions <id>        answer a question from a session's conversation
  delegate --brief-file <path>      start another agent with a delegated brief
  journal append --entry <text>     serialized append to the daily notebook journal
  workspace context <command>       edit shared workspace context
  open <file.md> [--session <id>]   show a markdown file in attn
  browser <command>                 open and control the in-app browser
  workflow <command>                run, inspect, and resume durable workflows
  list                              list sessions and workspaces
  present <command>                 open a review presentation and read feedback
  debug <command>                   probe debug artifacts (incidents, logs)
  db <command>                      database maintenance (restore from backup)
  vision-check <image> <question>   answer a question about an image (single LLM call)
  daemon <command>                  manage the daemon
  profile <status|resolve|list>     show / resolve the active profile's resources
  profile-env <profile|--unset>     print shell commands for selecting a profile
  version                           print version information
`)
}

func runDelegate() {
	if len(os.Args) == 3 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
		writeDelegateHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	args, err := parseDelegateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").Delegate(args.sourceSessionID, args.brief, args.options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func writeDelegateHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn delegate (--brief <text> | --brief-file <path>) [options]

placement:
  (no flags)                 add a pane to the source session's workspace
  --new-workspace            create a workspace using the source directory
  --workspace <id>           add a pane to an existing workspace
  --cwd <path>               create a workspace at an existing directory
  --worktree <branch>        create a worktree for the delegated session

worktree options:
  combine with any placement (current, --workspace, or --new-workspace);
  combining with --cwd creates a worktree of the repo at that directory
  --repo <path>              main repository (defaults to the workspace repository)
  --from <ref>               branch or ref to start from
  --worktree-path <path>     override the generated sibling path

session options:
  --agent <name>             configured prompt-capable built-in or plugin agent
  --model <name>             pin the agent's model (alias or full id, e.g.
                             "opus" or "claude-opus-4-8"; defaults to the
                             agent's own default)
  --effort <level>           pin the agent's reasoning effort (claude: low,
                             medium, high, xhigh, max; codex: minimal, low,
                             medium, high, xhigh)
  --name <text>              name for the agent and, when a new workspace is
                             created, the workspace (max 16 chars, must be
                             unique; defaults to the directory name)
  --source-session <id>      source session (defaults to ATTN_SESSION_ID)
  --yolo                     bypass agent approval prompts
`)
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

// runTicket routes `attn ticket <command>`: `status` (the agent's forward channel
// onto its own bound ticket), `inbox`, `attach`, `attach-plan`, `new` (mint a standalone, unbound
// backlog ticket without delegating), and `comment` (post a one-shot note onto any
// ticket by id).
func runTicket() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeTicketHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	switch os.Args[2] {
	case "status":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketStatus(os.Args[3:])
	case "inbox":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketInbox(os.Args[3:])
	case "list":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketList(os.Args[3:])
	case "show":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketShow(os.Args[3:])
	case "attach":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketAttach(os.Args[3:])
	case "attach-plan":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketAttachPlan(os.Args[3:])
	case "new":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketNew(os.Args[3:])
	case "comment":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketComment(os.Args[3:])
	case "subscribe":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketSubscribe(os.Args[3:])
	case "unsubscribe":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketUnsubscribe(os.Args[3:])
	case "take":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketTake(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "ticket: unknown command %q\n", os.Args[2])
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
}

type ticketStatusArgs struct {
	WorkState string
	Session   string
	Comment   string
	TicketID  string
	JSON      bool
}

// parseTicketStatusArgs reads `ticket status <work-state> [flags]`. Go's flag
// parser stops at the first positional, so a naive Parse would silently drop any
// flag written after the work state — exactly the documented form. We interleave
// instead: parse, peel one positional, repeat, so flags may sit on either side of
// the state and the single positional is the work state regardless of order.
func parseTicketStatusArgs(args []string) (ticketStatusArgs, error) {
	var result ticketStatusArgs
	fs := flag.NewFlagSet("ticket status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	comment := fs.String("comment", "", "optional note recorded with the status change")
	ticketID := fs.String("ticket", "", "move this ticket by id instead of the session's bound ticket")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")

	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return result, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	if len(positionals) != 1 {
		return result, fmt.Errorf("expected exactly one work state argument, got %d", len(positionals))
	}
	result.WorkState = positionals[0]
	result.Session = *session
	result.Comment = *comment
	result.TicketID = *ticketID
	result.JSON = *jsonOutput
	return result, nil
}

// runTicketStatus reports a work state, moving a ticket to the matching column.
// The work state is the same vocabulary the agent reports to the chief
// (in_progress, needs_input, ready_for_review, completed, failed). Without
// --ticket, the daemon resolves which ticket from the calling session (the
// agent's own bound ticket); with --ticket, it moves that ticket by id instead,
// regardless of who is bound to it.
func runTicketStatus(args []string) {
	parsed, err := parseTicketStatusArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket status: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket status: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").SetTicketStatus(source, parsed.WorkState, parsed.Comment, parsed.TicketID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket status: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("ticket %s → %s\n", result.TicketID, result.Status)
}

type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }
func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type ticketAttachArgs struct {
	Files   []string
	Ticket  string
	State   string
	Comment string
	Session string
	JSON    bool
}

func parseTicketAttachArgs(args []string) (ticketAttachArgs, error) {
	var result ticketAttachArgs
	fs := flag.NewFlagSet("ticket attach", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var files stringListFlag
	fs.Var(&files, "file", "path to an artifact file (repeatable)")
	ticketID := fs.String("ticket", "", "attach to this ticket instead of the session's bound ticket")
	state := fs.String("state", "", "optional resulting work state")
	comment := fs.String("comment", "", "optional context recorded with the attachment")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return result, err
	}
	if fs.NArg() != 0 {
		return result, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if len(files) == 0 {
		return result, errors.New("at least one --file is required")
	}
	for _, file := range files {
		path := strings.TrimSpace(file)
		if path == "" {
			return result, errors.New("--file cannot be empty")
		}
		result.Files = append(result.Files, path)
	}
	result.Ticket = strings.TrimSpace(*ticketID)
	result.State = strings.TrimSpace(*state)
	result.Comment = strings.TrimSpace(*comment)
	result.Session = *session
	result.JSON = *jsonOutput
	return result, nil
}

func runTicketAttach(args []string) {
	parsed, err := parseTicketAttachArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach: %v\n", err)
		os.Exit(2)
	}
	files := make([]protocol.TicketAttachFile, 0, len(parsed.Files))
	for _, path := range parsed.Files {
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			fmt.Fprintf(os.Stderr, "ticket attach: %v\n", absErr)
			os.Exit(2)
		}
		info, statErr := os.Lstat(absPath)
		if statErr != nil {
			fmt.Fprintf(os.Stderr, "ticket attach: %v\n", statErr)
			os.Exit(1)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(os.Stderr, "ticket attach: %q is not a regular file\n", absPath)
			os.Exit(1)
		}
		files = append(files, protocol.TicketAttachFile{SourcePath: absPath, Filename: filepath.Base(absPath)})
	}
	result, err := client.New("").AttachTicket(source, files, parsed.Ticket, parsed.State, parsed.Comment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("attached %d artifact(s) to ticket %s → %s\n", len(result.Artifacts), result.TicketID, result.State)
	for _, artifact := range result.Artifacts {
		fmt.Printf("  %s\n", artifact.Path)
	}
}

type ticketNewArgs struct {
	Title       string
	Description string
	ID          string
	Session     string
	JSON        bool
}

// parseTicketNewArgs reads `ticket new --title <t> [flags]`. --title is required;
// the rest are optional. Like attach there is no positional, so a plain Parse
// suffices.
func parseTicketNewArgs(args []string) (ticketNewArgs, error) {
	var result ticketNewArgs
	fs := flag.NewFlagSet("ticket new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "ticket title (the slug is derived from it)")
	description := fs.String("description", "", "optional brief recorded as the ticket description")
	id := fs.String("id", "", "optional explicit slug (defaults to one derived from the title)")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return result, err
	}
	if fs.NArg() != 0 {
		return result, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	name := strings.TrimSpace(*title)
	if name == "" {
		return result, errors.New("--title is required")
	}
	result.Title = name
	result.Description = strings.TrimSpace(*description)
	result.ID = strings.TrimSpace(*id)
	result.Session = *session
	result.JSON = *jsonOutput
	return result, nil
}

// runTicketNew mints a standalone, unbound backlog ticket in the Todo column —
// distinct from delegation, which mints a working ticket bound to a spawned agent.
// The daemon derives the slug from the title (or pins --id) and may auto-suffix on
// collision, so the success line echoes the resolved id back.
func runTicketNew(args []string) {
	parsed, err := parseTicketNewArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket new: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket new: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").CreateTicket(source, parsed.Title, parsed.Description, parsed.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket new: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("created ticket %s (%s): %s\n", result.TicketID, result.Status, result.Title)
}

type ticketCommentArgs struct {
	TicketID string
	Comment  string
	Session  string
	JSON     bool
}

// parseTicketCommentArgs reads `ticket comment <ticket-id> --message <text> [flags]`.
// The comment text is a flag value (--message / -m), not a trailing positional, so
// flags compose in any order around the single id positional and the comment may
// contain spaces and dashes without being mistaken for a flag (e.g. -m "--watch out
// for X"). This mirrors `ticket status` (one positional, interleaved flags) and the
// rest of the CLI, where freeform text is always a flag — a trailing-positional
// comment would silently swallow a --session/--json written after it, since Go's
// flag parser stops at the first positional. The id and a non-empty message are
// both required.
func parseTicketCommentArgs(args []string) (ticketCommentArgs, error) {
	var result ticketCommentArgs
	fs := flag.NewFlagSet("ticket comment", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("message", "", "the comment text")
	fs.StringVar(message, "m", "", "shorthand for --message")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")

	// Interleave parse: Go's flag parser stops at the first positional, so to allow
	// flags on either side of the id we peel one positional at a time and re-parse.
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return result, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	if len(positionals) != 1 {
		// The most common mistake is writing the comment as a bare argument
		// (`comment tk "looks good"`) instead of behind -m; point at the fix.
		if len(positionals) > 1 && strings.TrimSpace(*message) == "" {
			return result, fmt.Errorf("got %d arguments but no --message; the comment text goes behind -m, e.g. ticket comment %s -m \"<text>\"", len(positionals), positionals[0])
		}
		return result, fmt.Errorf("expected exactly one ticket id argument, got %d", len(positionals))
	}
	result.TicketID = positionals[0]
	result.Comment = strings.TrimSpace(*message)
	if result.Comment == "" {
		return result, errors.New("--message is required")
	}
	result.Session = *session
	result.JSON = *jsonOutput
	return result, nil
}

// runTicketList reads the board — the foundation for the cross-ticket verbs, since
// an agent (typically the chief, coordinating) needs a ticket-id before it can
// comment on a ticket it isn't assigned to. It is a global read, so unlike the other
// ticket commands it does NOT require a session: --session / ATTN_SESSION_ID is
// resolved best-effort and passed only for uniformity (the daemon ignores it). This
// mirrors `attn list` for sessions.
func runTicketList(args []string) {
	fs := flag.NewFlagSet("ticket list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	status := fs.String("status", "", "only tickets in this column (todo|working|blocked|in_review|done|failed|crashed)")
	all := fs.Bool("all", false, "include archived tickets (hidden by default)")
	sessionID := fs.String("session", "", "session id (optional; defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the board as JSON (includes each ticket's description)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "ticket list: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "ticket list: unexpected arguments: %v\n", fs.Args())
		os.Exit(2)
	}
	// Best-effort: a board read works without a session, so resolve quietly rather
	// than erroring the way resolveDispatchSession does.
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	tickets, err := client.New("").TicketList(source, strings.TrimSpace(*status), *all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket list: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		printJSON(tickets)
		return
	}
	printTicketBoard(tickets)
}

// printTicketBoard renders the board as one compact line per ticket: id, column,
// assignee, title. The --json form carries the full rows (including description);
// this human form is a scannable index. An unassigned ticket shows "-".
func printTicketBoard(tickets []protocol.Ticket) {
	if len(tickets) == 0 {
		fmt.Println("no tickets")
		return
	}
	for _, t := range tickets {
		assignee := t.Assignee
		if strings.TrimSpace(assignee) == "" {
			assignee = "-"
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", t.ID, t.Status, assignee, t.Title)
	}
}

// runTicketShow prints one ticket's full record — metadata, description, and the
// complete activity thread with full bodies (comments, status changes, verdicts)
// plus current artifacts. It is a non-consuming read: it never touches any session's
// inbox cursor, so unlike `ticket inbox` it can be re-read at will. Like `ticket
// list` it works without a session (a global read by id), so the session is
// resolved best-effort — flag then ATTN_SESSION_ID — and passed along even if
// empty rather than erroring via resolveDispatchSession.
func runTicketShow(args []string) {
	parsed, err := parseTicketIDArgs("ticket show", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket show: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source := strings.TrimSpace(parsed.Session)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	ticket, err := client.New("").ShowTicket(source, parsed.TicketID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket show: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(ticket)
		return
	}
	fprintTicketShow(os.Stdout, ticket)
}

// fprintTicketShow renders one ticket's full record in the same visual style as
// fprintTicketInbox's activity lines — header block, full description, complete
// activity thread with full bodies (no truncation), then artifacts.
func fprintTicketShow(w io.Writer, t *protocol.Ticket) {
	if t == nil {
		fmt.Fprintln(w, "ticket not found")
		return
	}
	assignee := t.Assignee
	if strings.TrimSpace(assignee) == "" {
		assignee = "-"
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s → %s\n", t.ID, t.Status, assignee, t.CreatedAt, t.UpdatedAt)
	fmt.Fprintln(w, t.Title)
	if strings.TrimSpace(t.Description) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, t.Description)
	}
	fmt.Fprintln(w)
	if len(t.Activity) == 0 {
		fmt.Fprintln(w, "no activity")
	} else {
		fmt.Fprintln(w, "activity:")
		for _, e := range t.Activity {
			line := fmt.Sprintf("  [%s] %s by %s", e.CreatedAt, e.Kind, e.Author)
			if e.FromStatus != nil && e.ToStatus != nil {
				line += fmt.Sprintf(" (%s → %s)", *e.FromStatus, *e.ToStatus)
			}
			fmt.Fprintln(w, line)
			if e.Comment != nil && *e.Comment != "" {
				fmt.Fprintf(w, "    %s\n", *e.Comment)
			}
		}
	}
	if len(t.Artifacts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "artifacts:")
		for _, artifact := range t.Artifacts {
			fmt.Fprintf(w, "  %s (%s)\n", artifact.Filename, artifact.Path)
		}
	}
}

// runTicketComment posts a one-shot comment from the calling session onto any
// ticket by id — the agent-to-agent note channel. Commenting informs the ticket's
// participants but does not subscribe the caller, so it is a way to chime in
// without joining a ticket's future activity.
func runTicketComment(args []string) {
	parsed, err := parseTicketCommentArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket comment: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket comment: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").CommentTicket(source, parsed.TicketID, parsed.Comment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket comment: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("commented on ticket %s\n", result.TicketID)
}

// ticketIDArgs is a single ticket-id positional plus the common session/json flags —
// the shape of `ticket subscribe`/`ticket unsubscribe`, which name a ticket and act
// as the calling session.
type ticketIDArgs struct {
	TicketID string
	Session  string
	JSON     bool
}

// parseTicketIDArgs reads `<command> <ticket-id> [--session <id>] [--json]`. Flags
// may appear on either side of the id (interleave parse, like ticket status/comment).
func parseTicketIDArgs(name string, args []string) (ticketIDArgs, error) {
	var result ticketIDArgs
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")

	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return result, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	if len(positionals) != 1 {
		return result, fmt.Errorf("expected exactly one ticket id argument, got %d", len(positionals))
	}
	result.TicketID = positionals[0]
	result.Session = *session
	result.JSON = *jsonOutput
	return result, nil
}

// runTicketSubscribe opts the calling session into a ticket's notifications — a
// standing interest in a ticket it isn't assigned to. Future activity then nudges it
// and lands in its inbox; the first inbox after subscribing also delivers the
// ticket's history (subscribing does not advance the cursor).
func runTicketSubscribe(args []string) {
	parsed, err := parseTicketIDArgs("ticket subscribe", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket subscribe: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket subscribe: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").SubscribeTicket(source, parsed.TicketID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket subscribe: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("subscribed to ticket %s\n", result.TicketID)
}

// runTicketUnsubscribe opts the calling session back out. It is idempotent —
// unsubscribing when not subscribed still succeeds.
func runTicketUnsubscribe(args []string) {
	parsed, err := parseTicketIDArgs("ticket unsubscribe", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket unsubscribe: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket unsubscribe: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").UnsubscribeTicket(source, parsed.TicketID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket unsubscribe: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("unsubscribed from ticket %s\n", result.TicketID)
}

// ticketTakeArgs is a ticket-id positional plus the common session/json flags and
// the take-over guard `--confirm`.
type ticketTakeArgs struct {
	TicketID string
	Session  string
	Confirm  bool
	JSON     bool
}

// parseTicketTakeArgs reads `ticket take <ticket-id> [--confirm] [--session <id>]
// [--json]`. Flags may appear on either side of the id (interleave parse, like the
// other ticket verbs).
func parseTicketTakeArgs(args []string) (ticketTakeArgs, error) {
	var result ticketTakeArgs
	fs := flag.NewFlagSet("ticket take", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	confirm := fs.Bool("confirm", false, "take the ticket even if it is already assigned to someone else")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")

	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return result, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	if len(positionals) != 1 {
		return result, fmt.Errorf("expected exactly one ticket id argument, got %d", len(positionals))
	}
	result.TicketID = positionals[0]
	result.Session = *session
	result.Confirm = *confirm
	result.JSON = *jsonOutput
	return result, nil
}

// runTicketTake claims a ticket for the calling session, making it the assignee.
// Taking a ticket already assigned to someone else needs --confirm, so an agent
// cannot silently take over another's active work. Taking does not advance the
// cursor, so the first inbox after taking delivers the ticket's history.
func runTicketTake(args []string) {
	parsed, err := parseTicketTakeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket take: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket take: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").TakeTicket(source, parsed.TicketID, parsed.Confirm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket take: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	if result.PreviousAssignee != "" && result.PreviousAssignee != source {
		fmt.Printf("took ticket %s (was assigned to %s)\n", result.TicketID, result.PreviousAssignee)
		return
	}
	fmt.Printf("took ticket %s\n", result.TicketID)
}

// runTicketInbox reads (and consumes) this session's unread ticket events — the
// chief's comments, status changes, and re-briefs it has not yet seen. Reading
// advances the cursor, so a second call returns only what landed since.
func runTicketInbox(args []string) {
	fs := flag.NewFlagSet("ticket inbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the unread bundles as JSON")
	watch := fs.Bool("watch", false, "block and print new ticket activity as it lands (for a harness Monitor); silent until something changes")
	interval := fs.Duration("interval", ticketWatchInterval, "poll interval in --watch mode")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "ticket inbox: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "ticket inbox: unexpected arguments: %v\n", fs.Args())
		os.Exit(2)
	}
	source, err := resolveDispatchSession(*sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket inbox: %v\n", err)
		os.Exit(2)
	}
	if *watch {
		runTicketInboxWatch(source, *interval, *jsonOutput)
		return
	}
	result, err := client.New("").TicketInbox(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket inbox: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		printJSON(result)
		return
	}
	printTicketInbox(result)
}

// ticketWatchInterval is how often `attn ticket inbox --watch` polls the consuming
// inbox. A watch may consume unread activity before the daemon's shared nudge
// countdown fires, but it is not required for delivery.
const ticketWatchInterval = 3 * time.Second

// runTicketInboxWatch blocks and prints new ticket activity as it lands, so a
// harness Monitor can wrap it as a true push for a chief. Whether a runtime is
// guided to use it is separate from daemon nudge eligibility. It polls the
// consuming ticket-inbox: the daemon advances the session's per-ticket cursor on
// each read, so each event prints exactly once and the client tracks no state.
// Silent when nothing is new; exits cleanly on SIGINT/SIGTERM (the harness stops
// the Monitor on session end). A transient daemon error is reported once per outage
// but does not end the watch. The poll loop lives in watchTicketInbox so it can be
// tested without a daemon, signals, or a real ticker.
func runTicketInboxWatch(source string, interval time.Duration, jsonOutput bool) {
	if interval <= 0 {
		interval = ticketWatchInterval
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := client.New("")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	watchTicketInbox(ctx, ticker.C, func() (*protocol.TicketInboxResult, error) {
		return c.TicketInbox(source)
	}, os.Stdout, os.Stderr, jsonOutput)
}

// watchTicketInbox is the poll loop behind `attn ticket inbox --watch`. It prints new
// bundles each tick and stays silent otherwise. A daemon error is reported once per
// outage: a wrapping Monitor treats every printed line as new activity, so repeating
// an unchanged error would nudge the chief every poll — the next success clears the
// suppression so a recovered-then-failed daemon reports again. Returns when ctx is
// cancelled (SIGINT/SIGTERM in production).
func watchTicketInbox(
	ctx context.Context,
	tick <-chan time.Time,
	fetch func() (*protocol.TicketInboxResult, error),
	out, errOut io.Writer,
	jsonOutput bool,
) {
	var lastErr string
	for {
		result, err := fetch()
		if err != nil {
			if msg := err.Error(); msg != lastErr {
				fmt.Fprintf(errOut, "ticket inbox --watch: %s\n", msg)
				lastErr = msg
			}
		} else {
			lastErr = ""
			if result != nil && len(result.Bundles) > 0 {
				if jsonOutput {
					if encErr := fprintJSON(out, result); encErr != nil {
						fmt.Fprintf(errOut, "ticket inbox --watch: %v\n", encErr)
					}
				} else {
					fprintTicketInbox(out, result)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

func printTicketInbox(result *protocol.TicketInboxResult) {
	fprintTicketInbox(os.Stdout, result)
}

// fprintTicketInbox prints the unread bundles, with a leading user-presence
// header line when the daemon has observed the user at the app recently: a
// watching agent can eyeball this without --json, and it's the same signal
// carried on the struct for --json callers.
func fprintTicketInbox(w io.Writer, result *protocol.TicketInboxResult) {
	if result == nil {
		fmt.Fprintln(w, "no unread ticket activity")
		return
	}
	if result.LastUserActivityAt != nil {
		if lastActive, err := time.Parse(time.RFC3339, *result.LastUserActivityAt); err == nil {
			fmt.Fprintf(w, "user: active %s ago\n", humanizeDuration(time.Since(lastActive)))
		}
	}
	bundles := result.Bundles
	if len(bundles) == 0 {
		fmt.Fprintln(w, "no unread ticket activity")
		return
	}
	for _, b := range bundles {
		fmt.Fprintf(w, "%s\n", b.TicketID)
		for _, e := range b.Events {
			line := fmt.Sprintf("  [%s] %s by %s", e.CreatedAt, e.Kind, e.Author)
			if e.FromStatus != nil && e.ToStatus != nil {
				line += fmt.Sprintf(" (%s → %s)", *e.FromStatus, *e.ToStatus)
			}
			fmt.Fprintln(w, line)
			if e.Comment != nil && *e.Comment != "" {
				fmt.Fprintf(w, "    %s\n", *e.Comment)
			}
		}
	}
}

// humanizeDuration renders d as a coarse s/m/h age, rounding down to the
// largest whole unit (e.g. "42s", "5m", "3h") for a one-line presence header.
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
}

func writeTicketHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn ticket <command>

commands:
  status <work-state> [--session <id>] [--comment <text>] [--ticket <id>] [--json]
        move this session's bound ticket to the column for the reported state;
        --ticket <id> moves any ticket by id instead, not just the bound one
  inbox [--session <id>] [--json] [--watch [--interval <dur>]]
        read (and mark read) this session's unread ticket activity;
        --watch blocks and prints new activity as it lands (for a Monitor)
  list [--status <col>] [--all] [--json]
        read the board: every ticket (id, column, assignee, title), newest first;
        --json includes each ticket's description. No session required.
  show <ticket-id> [--session <id>] [--json]
        print one ticket's full record — description, complete activity thread
        with full bodies, current artifacts; non-consuming, does not touch your inbox
        cursor
  attach --file <path> [--file <path> ...] [--ticket <id>]
        [--state <work-state>] [--comment <text>] [--session <id>] [--json]
		copy artifact files into a ticket's canonical Notebook directory,
        record one durable attachment, and optionally change ticket state
  attach-plan --file <path> [--scope <path>]
        [--authority auto|repository|notebook] [--ticket <id>]
        [--state <work-state>] [--comment <text>] [--session <id>] [--json]
        choose one canonical home for a Markdown plan or design: keep committed
        repository plans in Git and attach a Notebook reference, or promote an
        untracked staging file into the Notebook and retire the verified source;
        use --scope for the affected component in a monorepo; a byte-identical
        legacy Notebook copy is retired when replaced by a repository reference
  new --title <t> [--description <d>] [--id <slug>] [--session <id>] [--json]
        create an unbound backlog ticket in todo (no agent, no session)
  comment <ticket-id> --message <text> [--session <id>] [--json]
        post a one-shot comment onto any ticket by id (does not subscribe you);
        -m is shorthand for --message
  subscribe <ticket-id> [--session <id>] [--json]
        opt into a ticket's notifications (future activity nudges you and lands
        in your inbox; the next inbox also delivers the ticket's history)
  unsubscribe <ticket-id> [--session <id>] [--json]
        opt back out of a ticket's notifications (idempotent)
  take <ticket-id> [--confirm] [--session <id>] [--json]
        claim a ticket (become its assignee); --confirm is required to take over
        one already assigned to someone else

work states:
  in_progress       working
  needs_input       blocked
  ready_for_review  in review
  completed         done
  failed            failed

The session defaults to ATTN_SESSION_ID.
`)
}

// runJournal routes `attn journal <command>`. Today there is only one
// subcommand, `append`, mirroring the shape of runTicket for future growth.
func runJournal() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeJournalHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	switch os.Args[2] {
	case "append":
		if hasHelpFlag(os.Args[3:]) {
			writeJournalHelp(os.Stdout)
			return
		}
		runJournalAppend(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "journal: unknown command %q\n", os.Args[2])
		writeJournalHelp(os.Stderr)
		os.Exit(2)
	}
}

type journalAppendArgs struct {
	sessionID string
	date      string
	entry     string
	jsonOut   bool
}

// parseJournalAppendArgs reads `attn journal append (--entry <text> | --entry-file
// <path>) [--date YYYY-MM-DD] [--session <id>] [--json]`. --entry/--entry-file
// are mutually exclusive and one is required, mirroring delegate's
// --brief/--brief-file handling.
func parseJournalAppendArgs(args []string) (journalAppendArgs, error) {
	fs := flag.NewFlagSet("journal append", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	entryText := fs.String("entry", "", "journal entry markdown")
	entryFile := fs.String("entry-file", "", "file containing the journal entry markdown")
	date := fs.String("date", "", "journal date as YYYY-MM-DD (defaults to today)")
	sessionID := fs.String("session", "", "session id (optional; defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return journalAppendArgs{}, err
	}
	if fs.NArg() != 0 {
		return journalAppendArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if strings.TrimSpace(*entryText) != "" && strings.TrimSpace(*entryFile) != "" {
		return journalAppendArgs{}, errors.New("pass only one of --entry or --entry-file")
	}
	entry := strings.TrimSpace(*entryText)
	if strings.TrimSpace(*entryFile) != "" {
		content, err := os.ReadFile(*entryFile)
		if err != nil {
			return journalAppendArgs{}, fmt.Errorf("read entry file: %w", err)
		}
		entry = strings.TrimSpace(string(content))
	}
	if entry == "" {
		return journalAppendArgs{}, errors.New("--entry or --entry-file is required")
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	return journalAppendArgs{
		sessionID: source,
		date:      strings.TrimSpace(*date),
		entry:     entry,
		jsonOut:   *jsonOutput,
	}, nil
}

// runJournalAppend is the contention-safe way an agent writes the daily journal:
// it appends through the daemon's single serialized notebook.Store writer instead
// of editing journal/<date>.md directly, which races the daemon keeper's own
// writes to the same file (nearly always hitting "file modified since read").
func runJournalAppend(args []string) {
	parsed, err := parseJournalAppendArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journal append: %v\n", err)
		writeJournalHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").AppendJournal(parsed.sessionID, parsed.date, parsed.entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journal append: %v\n", err)
		os.Exit(1)
	}
	if parsed.jsonOut {
		printJSON(result)
		return
	}
	fmt.Printf("appended to %s\n", result.RelPath)
}

func writeJournalHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn journal <command>

commands:
  append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]
        serialized append to the notebook's daily journal (journal/<date>.md)
        through the daemon — the contention-safe way an agent writes the
        journal, instead of editing the file directly with its own file-edit
        tools (which races the daemon's own keeper writes to the same file).
        date defaults to today. --json prints rel_path and hash.

The session defaults to ATTN_SESSION_ID.
`)
}

// runPresent dispatches the `attn present` surface: opening a presentation (the
// default form, no subcommand), validating a manifest locally, and reading back
// reviewer feedback.
func runPresent() {
	if len(os.Args) >= 3 {
		switch os.Args[2] {
		case "-h", "--help":
			writePresentHelp(os.Stdout)
			return
		case "validate":
			if hasHelpFlag(os.Args[3:]) {
				writePresentHelp(os.Stdout)
				return
			}
			runPresentValidate(os.Args[3:])
			return
		case "feedback":
			if hasHelpFlag(os.Args[3:]) {
				writePresentHelp(os.Stdout)
				return
			}
			runPresentFeedback(os.Args[3:])
			return
		}
	}
	warnIfDaemonVersionMismatch()
	runPresentOpen(os.Args[2:])
}

type presentOpenArgs struct {
	Manifest       string
	PresentationID string
	Session        string
	JSON           bool
	Wait           bool
}

func parsePresentOpenArgs(args []string) (presentOpenArgs, error) {
	var result presentOpenArgs
	fs := flag.NewFlagSet("present", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifest := fs.String("manifest", ".present.yml", "path to the present manifest")
	presentationID := fs.String("presentation", "", "existing presentation id to add a new round to")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	wait := fs.Bool("wait", false, "block until the reviewer submits this round or closes the presentation, then print its feedback")
	if err := fs.Parse(args); err != nil {
		return result, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return result, fmt.Errorf("unexpected argument %q", rest[0])
	}
	result.Manifest = *manifest
	result.PresentationID = *presentationID
	result.Session = *session
	result.JSON = *jsonOutput
	result.Wait = *wait
	return result, nil
}

// runPresentOpen parses and validates the manifest locally first, for a fast and
// friendly error, then hands the raw YAML to the daemon — the daemon re-parses
// and pins it, since it is the single authority over what a presentation actually
// reviewed.
func runPresentOpen(args []string) {
	parsed, err := parsePresentOpenArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		writePresentHelp(os.Stderr)
		os.Exit(2)
	}
	if _, err := present.ParseManifestFile(parsed.Manifest); err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		os.Exit(1)
	}
	manifestYAML, err := os.ReadFile(parsed.Manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		os.Exit(1)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").PresentOpen(source, string(manifestYAML), parsed.PresentationID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		os.Exit(1)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if parsed.Wait {
		runPresentOpenWait(result, parsed.JSON)
		return
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Printf("presentation %s round %d pinned %s..%s\n",
		shortenID(result.PresentationID), result.Seq, shortenID(result.BaseSHA), shortenID(result.HeadSHA))
	fmt.Printf("feedback will arrive via: attn present feedback %s\n", result.PresentationID)
}

// presentWaitInterval is how often `attn present --wait` polls for the round's
// feedback, mirroring ticketWatchInterval.
const presentWaitInterval = 3 * time.Second

// runPresentOpenWait is the blocking shell behind `attn present --wait`: it prints
// a status line to stderr so the caller can see it's blocking, then polls the
// daemon for the round we just opened until the reviewer submits it or closes the
// presentation without reviewing, printing the outcome to stdout. The poll loop
// lives in waitForPresentFeedback so it can be tested without a daemon, signals,
// or a real ticker.
func runPresentOpenWait(result *protocol.PresentOpenResult, jsonOutput bool) {
	fmt.Fprintf(os.Stderr, "waiting for review of round %d of %q...\n", result.Seq, result.Title)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := client.New("")
	ticker := time.NewTicker(presentWaitInterval)
	defer ticker.Stop()
	err := waitForPresentFeedback(ctx, ticker.C, func() (*protocol.PresentFeedbackResult, error) {
		return c.PresentFeedback(result.PresentationID, result.Seq)
	}, os.Stdout, jsonOutput)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "present --wait: %v\n", err)
		os.Exit(1)
	}
}

// waitForPresentFeedback is the poll loop behind `attn present --wait`. It polls
// fetch on tick, tolerating transient daemon errors the way watchTicketInbox
// does (report and keep polling rather than exit), and returns once the round has
// been submitted or the presentation has been closed without review, having
// rendered the outcome to out exactly once. Returns ctx.Err() if ctx is cancelled
// first (SIGINT/SIGTERM in production).
func waitForPresentFeedback(
	ctx context.Context,
	tick <-chan time.Time,
	fetch func() (*protocol.PresentFeedbackResult, error),
	out io.Writer,
	jsonOutput bool,
) error {
	var lastErr string
	for {
		result, err := fetch()
		if err != nil {
			if msg := err.Error(); msg != lastErr {
				fmt.Fprintf(os.Stderr, "present --wait: %s\n", msg)
				lastErr = msg
			}
		} else {
			lastErr = ""
			if result != nil && result.Submitted {
				if jsonOutput {
					return fprintJSON(out, result)
				}
				fmt.Fprint(out, result.Markdown)
				return nil
			}
			if result != nil && result.PresentationStatus == "closed" {
				// The reviewer closed the presentation instead of reviewing this
				// round — no handback is ever coming for a bare "not submitted
				// yet" poll to catch, so stop here rather than polling forever.
				if jsonOutput {
					return fprintJSON(out, result)
				}
				fmt.Fprintln(out, "presentation closed by the reviewer without feedback — drafts were discarded; open a new round to re-present")
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
		}
	}
}

// runPresentValidate parses and validates a manifest locally, with no daemon
// call — a fast loop for an agent iterating on a manifest before opening it.
// When the manifest has annotations, it also resolves the frame's refs to
// SHAs and checks each anchor locally, the same way the daemon would at open
// time — a manifest with annotations that can't be checked is not validated.
func runPresentValidate(args []string) {
	fs := flag.NewFlagSet("present validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifest := fs.String("manifest", ".present.yml", "path to the present manifest")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "present validate: %v\n", err)
		os.Exit(2)
	}
	m, err := present.ParseManifestFile(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present validate: %v\n", err)
		os.Exit(1)
	}

	if !hasAnyAnnotations(m) {
		fmt.Printf("manifest ok: %s\n", m.Title)
		return
	}

	_, headSHA, err := present.Pin(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present validate: could not resolve refs to check annotations: %v\n", err)
		os.Exit(1)
	}
	_, issues := present.ResolveAnnotations(m, m.Frame.Repo, headSHA)
	hasError := false
	for _, issue := range issues {
		level := "error"
		if issue.Warning {
			level = "warning"
		} else {
			hasError = true
		}
		if issue.Index < 0 {
			fmt.Fprintf(os.Stderr, "%s: %s: %s\n", level, issue.Path, issue.Message)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %s[%d]: %s\n", level, issue.Path, issue.Index, issue.Message)
		}
	}
	if hasError {
		os.Exit(1)
	}
	fmt.Printf("manifest ok: %s\n", m.Title)
}

// hasAnyAnnotations reports whether any file entry in the manifest carries
// annotations.
func hasAnyAnnotations(m *present.Manifest) bool {
	for _, f := range m.Files {
		if len(f.Annotations) > 0 {
			return true
		}
	}
	return false
}

type presentFeedbackArgs struct {
	PresentationID string
	Round          int
	JSON           bool
}

// parsePresentFeedbackArgs reads `present feedback <presentation-id> [--round
// <n>] [--json]`, interleaving flag and positional parsing like `ticket
// comment` so flags may sit on either side of the id.
func parsePresentFeedbackArgs(args []string) (presentFeedbackArgs, error) {
	var result presentFeedbackArgs
	fs := flag.NewFlagSet("present feedback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	round := fs.Int("round", 0, "round seq (defaults to the latest round)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")

	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return result, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	if len(positionals) != 1 {
		return result, fmt.Errorf("expected exactly one presentation id argument, got %d", len(positionals))
	}
	result.PresentationID = positionals[0]
	result.Round = *round
	result.JSON = *jsonOutput
	return result, nil
}

func runPresentFeedback(args []string) {
	parsed, err := parsePresentFeedbackArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present feedback: %v\n", err)
		writePresentHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").PresentFeedback(parsed.PresentationID, parsed.Round)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present feedback: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	fmt.Print(result.Markdown)
}

// shortenID renders the first 7 characters of an id or SHA for compact display
// (the CLI's actionable hints always echo the full id, never this form).
func shortenID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func writePresentHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn present [command] [flags]

commands:
  (none)                            open a presentation (or a new round) from a
                                     manifest and pin it to its current git refs
  validate                          parse and validate a manifest locally, with
                                     no daemon call
  feedback <presentation-id>        print a round's reviewer feedback as markdown

flags for the default (open) form:
  --manifest <path>                 manifest path (default .present.yml)
  --presentation <id>               add a new round to an existing presentation
  --session <id>                    session id (defaults to ATTN_SESSION_ID)
  --json                            print the result as JSON
  --wait                            block until the round is reviewed or the
                                     presentation is closed, then print the
                                     outcome to stdout instead of the
                                     "pinned"/"feedback will arrive via" hint
                                     lines

flags for validate:
  --manifest <path>                 manifest path (default .present.yml)

flags for feedback:
  --round <n>                       round seq (defaults to the latest round)
  --json                            print the result as JSON
`)
}

func resolveDispatchSession(value string) (string, error) {
	source := strings.TrimSpace(value)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return "", errors.New("no session; run inside attn or pass --session")
	}
	return source, nil
}

func readOptionalFlagContent(message, path string, required bool) (string, error) {
	message = strings.TrimSpace(message)
	path = strings.TrimSpace(path)
	if message != "" && path != "" {
		return "", errors.New("pass only one of --message or --file")
	}
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		message = strings.TrimSpace(string(content))
	}
	if required && message == "" {
		return "", errors.New("--message or --file is required")
	}
	return message, nil
}

func runWorkspace() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeWorkspaceHelp(os.Stdout)
		return
	}
	if os.Args[2] != "context" {
		fmt.Fprintf(os.Stderr, "workspace: unknown command %q\n\n", os.Args[2])
		writeWorkspaceHelp(os.Stderr)
		os.Exit(2)
	}
	runWorkspaceContext(os.Args[3:])
}

func writeWorkspaceHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn workspace context <command>

commands:
  show [--session <id>] [--force]  print the editable context file path
  checkout                         alias for show
  update [--session <id>]          publish local edits if the revision matches
  status [--session <id>]          show local and canonical revision state
  compact [--session <id>]         compact now with the configured keeper
  rollback [--session <id>]        restore the latest pre-compaction snapshot
`)
}

// notebookGuideClient is the slice of the daemon client the launch-guidance
// decision needs; narrowed so it can be faked in tests.
type notebookGuideClient interface {
	NotebookGuide(sessionID string) (*protocol.NotebookGuideResult, error)
}

// resolveChiefNotebookRoot returns the notebook root to use as chief-of-staff
// launch guidance for sessionID, or "" when the session is not the chief or the
// lookup fails — callers then fall back to the workspace-context checkout. A
// lookup error is deliberately treated as "not chief" so a transient daemon
// hiccup degrades to workspace guidance rather than failing the launch.
func resolveChiefNotebookRoot(c notebookGuideClient, sessionID string) string {
	guide, err := c.NotebookGuide(sessionID)
	if err != nil || guide == nil || !guide.SessionIsChief {
		return ""
	}
	return guide.Root
}

func workspaceContextSourceSession(args []string, allowForce bool) (string, bool, error) {
	fs := flag.NewFlagSet("workspace context", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "source session id (defaults to ATTN_SESSION_ID)")
	force := fs.Bool("force", false, "discard local edits and replace the checkout")
	if err := fs.Parse(args); err != nil {
		return "", false, err
	}
	if fs.NArg() != 0 {
		return "", false, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if !allowForce && *force {
		return "", false, errors.New("--force is only valid with show or checkout")
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return "", false, errors.New("no source session; run inside attn or pass --session")
	}
	return source, *force, nil
}

func runWorkspaceContext(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeWorkspaceHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	action := args[0]
	switch action {
	case "show", "checkout", "update", "status", "compact", "rollback":
	default:
		fmt.Fprintf(os.Stderr, "workspace context: unknown command %q\n\n", action)
		writeWorkspaceHelp(os.Stderr)
		os.Exit(2)
	}
	sourceSessionID, force, err := workspaceContextSourceSession(args[1:], action == "show" || action == "checkout")
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace context: %v\n", err)
		os.Exit(2)
	}
	c := client.New("")
	var result *protocol.WorkspaceContextResult
	var maintenanceResult *protocol.WorkspaceContextMaintenanceResult
	switch action {
	case "show", "checkout":
		result, err = c.CheckoutWorkspaceContext(sourceSessionID, force)
		if err == nil {
			fmt.Println(result.Path)
			return
		}
	case "update":
		result, err = c.UpdateWorkspaceContext(sourceSessionID)
	case "status":
		result, err = c.WorkspaceContextStatus(sourceSessionID)
	case "compact":
		maintenanceResult, err = c.CompactWorkspaceContext(sourceSessionID)
	case "rollback":
		maintenanceResult, err = c.RollbackWorkspaceContext(sourceSessionID)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace context %s: %v\n", action, err)
		os.Exit(1)
	}
	if maintenanceResult != nil {
		printJSON(maintenanceResult)
		return
	}
	printJSON(result)
}

type delegateCLIArgs struct {
	sourceSessionID string
	brief           string
	options         client.DelegateOptions
}

func parseDelegateArgs(args []string) (delegateCLIArgs, error) {
	fs := flag.NewFlagSet("delegate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	briefText := fs.String("brief", "", "delegated task brief")
	briefFile := fs.String("brief-file", "", "file containing the delegated task brief")
	agentName := fs.String("agent", "", "target agent (defaults to the source session agent)")
	model := fs.String("model", "", "pin the delegated agent's model (alias or full id)")
	effort := fs.String("effort", "", "pin the delegated agent's reasoning effort")
	name := fs.String("name", "", "name for the agent and, when a new workspace is created, the workspace")
	sourceSessionID := fs.String("source-session", "", "source session id (defaults to ATTN_SESSION_ID)")
	yolo := fs.Bool("yolo", false, "launch the target agent in yolo mode")
	newWorkspace := fs.Bool("new-workspace", false, "create a new workspace for the delegated agent")
	workspaceID := fs.String("workspace", "", "place the delegated agent in an existing workspace")
	cwd := fs.String("cwd", "", "use an existing directory in a new workspace")
	worktreeBranch := fs.String("worktree", "", "create a worktree with this branch for the delegated session")
	worktreeRepo := fs.String("repo", "", "main repository for --worktree (defaults to source repository)")
	worktreeStart := fs.String("from", "", "starting ref for --worktree")
	worktreePath := fs.String("worktree-path", "", "custom path for --worktree")
	if err := fs.Parse(args); err != nil {
		return delegateCLIArgs{}, err
	}
	if fs.NArg() != 0 {
		return delegateCLIArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source := strings.TrimSpace(*sourceSessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return delegateCLIArgs{}, errors.New("no source session; run inside attn or pass --source-session")
	}
	if strings.TrimSpace(*briefText) != "" && strings.TrimSpace(*briefFile) != "" {
		return delegateCLIArgs{}, errors.New("pass only one of --brief or --brief-file")
	}
	brief := strings.TrimSpace(*briefText)
	if strings.TrimSpace(*briefFile) != "" {
		content, err := os.ReadFile(*briefFile)
		if err != nil {
			return delegateCLIArgs{}, fmt.Errorf("read brief file: %w", err)
		}
		brief = strings.TrimSpace(string(content))
	}
	if brief == "" {
		return delegateCLIArgs{}, errors.New("--brief or --brief-file is required")
	}

	explicitWorkspace := strings.TrimSpace(*workspaceID)
	customCWD := strings.TrimSpace(*cwd)
	branch := strings.TrimSpace(*worktreeBranch)
	repo := strings.TrimSpace(*worktreeRepo)
	startingFrom := strings.TrimSpace(*worktreeStart)
	customWorktreePath := strings.TrimSpace(*worktreePath)
	if explicitWorkspace != "" && (*newWorkspace || customCWD != "") {
		return delegateCLIArgs{}, errors.New("--workspace cannot be combined with --new-workspace or --cwd")
	}
	if branch == "" && (repo != "" || startingFrom != "" || customWorktreePath != "") {
		return delegateCLIArgs{}, errors.New("--repo, --from, and --worktree-path require --worktree")
	}

	placement := "current_workspace"
	if explicitWorkspace != "" {
		placement = "existing_workspace"
	} else if *newWorkspace || customCWD != "" {
		placement = "new_workspace"
	}

	return delegateCLIArgs{
		sourceSessionID: source,
		brief:           brief,
		options: client.DelegateOptions{
			Agent:        strings.TrimSpace(*agentName),
			Model:        strings.TrimSpace(*model),
			Effort:       strings.TrimSpace(*effort),
			Label:        strings.TrimSpace(*name),
			Yolo:         *yolo,
			Placement:    placement,
			WorkspaceID:  explicitWorkspace,
			CWD:          customCWD,
			WorktreeRepo: repo,
			Worktree:     branch,
			WorktreePath: customWorktreePath,
			StartingFrom: startingFrom,
		},
	}, nil
}

// parseOpenArgs parses the args for `attn open <file.md> [--session <id>]`.
// Go's flag parser stops at the first non-flag argument, so a naive Parse would
// silently ignore `--session` when it trails the path. We parse interspersed
// flags and positionals so the documented trailing form works exactly like the
// flag-first form. Returns the raw path and the (untrimmed-of-env) session flag.
func parseOpenArgs(args []string) (rawPath string, sessionFlag string, err error) {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID, then the selected session)")

	var positionals []string
	rest := args
	for {
		if perr := fs.Parse(rest); perr != nil {
			return "", "", perr
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		// Consume one positional, then keep parsing flags that follow it.
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}

	if len(positionals) == 0 {
		return "", "", fmt.Errorf("missing <file.md> argument")
	}
	if len(positionals) > 1 {
		return "", "", fmt.Errorf("unexpected extra arguments: %v", positionals[1:])
	}
	return strings.TrimSpace(positionals[0]), strings.TrimSpace(*sessionID), nil
}

// runOpen handles `attn open <file.md> [--session <id>]`, docking a
// live-reloading markdown tile into a workspace. The session defaults to
// ATTN_SESSION_ID (set inside attn-managed agents), then the daemon's currently
// selected session.
func runOpen() {
	warnIfDaemonVersionMismatch()
	rawPath, sessionFlag, err := parseOpenArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn open: %v\nusage: attn open <file.md> [--session <id>]\n", err)
		os.Exit(1)
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: resolve path: %v\n", err)
		os.Exit(1)
	}

	resolvedSession := sessionFlag
	if resolvedSession == "" {
		resolvedSession = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.OpenMarkdown(absPath, resolvedSession); err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("opened %s\n", absPath)
}

func parseInterspersedFlagArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
}

func browserSessionID(sessionFlag string) string {
	if sessionID := strings.TrimSpace(sessionFlag); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func printBrowserUsage(w io.Writer) {
	fmt.Fprint(w, `usage: attn browser <command>

commands:
  open <url> [--session <id>]                 open or navigate the browser tile
  snapshot [--session <id>]                   print a semantic page snapshot
  find --using <strategy> --value <value>      find an element and return its reference
  wait --using <strategy> --value <value>      wait for attached/visible/hidden/detached
  click --selector <css>|--element <id>        click an element
  type --selector <css>|--element <id> --text  replace an input's value
  back | forward | reload                     navigate browser history
  press --text <key>                          send a keyboard key
  scroll [--x <px>] [--y <px>]                scroll the page
  cookies                                     list cookies for the current page
  command <action> [--params <json>]           call the WebDriver-shaped API directly
  screenshot [path] [--session <id>]          save a PNG (default: attn-browser.png)
  pdf [path] [--params <json>]                 save a PDF (default: attn-browser.pdf)
`)
}

func encodeBrowserParams(params map[string]interface{}) string {
	data, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func runBrowser() {
	warnIfDaemonVersionMismatch()
	if len(os.Args) < 3 {
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}

	subcommand := os.Args[2]
	fs := flag.NewFlagSet("browser "+subcommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionFlag := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID, then the selected session)")
	selector := fs.String("selector", "", "CSS selector")
	text := fs.String("text", "", "text to enter")
	paramsJSON := fs.String("params", "{}", "JSON object with action parameters")
	using := fs.String("using", "", "locator strategy")
	value := fs.String("value", "", "locator or form value")
	name := fs.String("name", "", "accessible name or cookie name")
	element := fs.String("element", "", "WebDriver element reference id")
	state := fs.String("state", "attached", "wait state")
	timeout := fs.Int("timeout", 5000, "timeout in milliseconds")
	all := fs.Bool("all", false, "return all matching elements")
	deltaX := fs.Int("x", 0, "horizontal scroll delta")
	deltaY := fs.Int("y", 0, "vertical scroll delta")
	positionals, err := parseInterspersedFlagArgs(fs, os.Args[3:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn browser %s: %v\n", subcommand, err)
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}

	sessionID := browserSessionID(*sessionFlag)
	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	textSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "text" {
			textSet = true
		}
	})
	switch subcommand {
	case "open":
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "attn browser open: expected exactly one <url>")
			os.Exit(1)
		}
		if err := c.OpenBrowser(positionals[0], sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "browser open: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("opened %s\n", positionals[0])
	case "snapshot":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser snapshot: unexpected arguments")
			os.Exit(1)
		}
		data, err := c.BrowserControl("snapshot", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser snapshot: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "find":
		if len(positionals) != 0 || strings.TrimSpace(*using) == "" || strings.TrimSpace(*value) == "" {
			fmt.Fprintln(os.Stderr, "attn browser find: --using and --value are required")
			os.Exit(1)
		}
		params := map[string]interface{}{"using": *using, "value": *value}
		if *name != "" {
			params["name"] = *name
		}
		action := "find_element"
		if *all {
			action = "find_elements"
		}
		data, err := c.BrowserCommand(action, encodeBrowserParams(params), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser find: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "wait":
		if len(positionals) != 0 || strings.TrimSpace(*using) == "" || strings.TrimSpace(*value) == "" {
			fmt.Fprintln(os.Stderr, "attn browser wait: --using and --value are required")
			os.Exit(1)
		}
		params := map[string]interface{}{"using": *using, "value": *value, "state": *state, "timeout": *timeout}
		if *name != "" {
			params["name"] = *name
		}
		data, err := c.BrowserCommand("wait_for", encodeBrowserParams(params), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser wait: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "click":
		if len(positionals) != 0 || (strings.TrimSpace(*selector) == "" && strings.TrimSpace(*element) == "") {
			fmt.Fprintln(os.Stderr, "attn browser click: --selector <css> or --element <id> is required")
			os.Exit(1)
		}
		var data string
		if strings.TrimSpace(*element) != "" {
			data, err = c.BrowserCommand("click_element", encodeBrowserParams(map[string]interface{}{"element": *element}), "", "", sessionID)
		} else {
			data, err = c.BrowserControl("click", strings.TrimSpace(*selector), "", sessionID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser click: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "type":
		if len(positionals) != 0 || (strings.TrimSpace(*selector) == "" && strings.TrimSpace(*element) == "") || !textSet {
			fmt.Fprintln(os.Stderr, "attn browser type: --selector <css> or --element <id>, plus --text, are required")
			os.Exit(1)
		}
		var data string
		if strings.TrimSpace(*element) != "" {
			params := encodeBrowserParams(map[string]interface{}{"element": *element})
			if _, err = c.BrowserCommand("clear_element", params, "", "", sessionID); err == nil {
				data, err = c.BrowserCommand("send_keys_to_element", encodeBrowserParams(map[string]interface{}{"element": *element, "text": *text}), "", "", sessionID)
			}
		} else {
			data, err = c.BrowserControl("type", strings.TrimSpace(*selector), *text, sessionID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser type: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "reload", "back", "forward":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser reload: unexpected arguments")
			os.Exit(1)
		}
		data, err := c.BrowserControl(subcommand, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser reload: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "press":
		if len(positionals) != 0 || !textSet || *text == "" {
			fmt.Fprintln(os.Stderr, "attn browser press: --text <key> is required")
			os.Exit(1)
		}
		actions := []map[string]interface{}{{"type": "key", "id": "keyboard", "actions": []map[string]interface{}{{"type": "keyDown", "value": *text}, {"type": "keyUp", "value": *text}}}}
		data, err := c.BrowserCommand("perform_actions", encodeBrowserParams(map[string]interface{}{"actions": actions}), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser press: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "scroll":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser scroll: unexpected arguments")
			os.Exit(1)
		}
		actions := []map[string]interface{}{{"type": "wheel", "id": "wheel", "actions": []map[string]interface{}{{"type": "scroll", "deltaX": *deltaX, "deltaY": *deltaY}}}}
		data, err := c.BrowserCommand("perform_actions", encodeBrowserParams(map[string]interface{}{"actions": actions}), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser scroll: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "cookies":
		data, err := c.BrowserCommand("get_all_cookies", "{}", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser cookies: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "command":
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "attn browser command: expected exactly one <action>")
			os.Exit(1)
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(*paramsJSON), &params); err != nil || params == nil {
			fmt.Fprintln(os.Stderr, "attn browser command: --params must be a JSON object")
			os.Exit(1)
		}
		data, err := c.BrowserCommand(positionals[0], *paramsJSON, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser command: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "screenshot":
		if len(positionals) > 1 {
			fmt.Fprintln(os.Stderr, "attn browser screenshot: expected at most one [path]")
			os.Exit(1)
		}
		path := "attn-browser.png"
		if len(positionals) == 1 {
			path = positionals[0]
		}
		data, err := c.BrowserControl("screenshot", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: %v\n", err)
			os.Exit(1)
		}
		png, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: decode PNG: %v\n", err)
			os.Exit(1)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: resolve path: %v\n", err)
			os.Exit(1)
		}
		if err := writePrivateFile(absPath, png); err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: write %s: %v\n", absPath, err)
			os.Exit(1)
		}
		fmt.Println(absPath)
	case "pdf":
		if len(positionals) > 1 {
			fmt.Fprintln(os.Stderr, "attn browser pdf: expected at most one [path]")
			os.Exit(1)
		}
		path := "attn-browser.pdf"
		if len(positionals) == 1 {
			path = positionals[0]
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(*paramsJSON), &params); err != nil || params == nil {
			fmt.Fprintln(os.Stderr, "attn browser pdf: --params must be a JSON object")
			os.Exit(1)
		}
		data, err := c.BrowserCommand("print_page", *paramsJSON, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: %v\n", err)
			os.Exit(1)
		}
		pdf, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: decode PDF: %v\n", err)
			os.Exit(1)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: resolve path: %v\n", err)
			os.Exit(1)
		}
		if err := writePrivateFile(absPath, pdf); err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: write %s: %v\n", absPath, err)
			os.Exit(1)
		}
		fmt.Println(absPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown browser command: %s\n", subcommand)
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}
}

func printJSON(v interface{}) {
	if err := fprintJSON(os.Stdout, v); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
		os.Exit(1)
	}
}

func fprintJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runWrapper() {
	// If running inside the app, run the selected agent directly.
	if os.Getenv("ATTN_INSIDE_APP") == "1" {
		agentName := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_AGENT")))
		if agentName == "" {
			agentName = "codex"
		}
		runAgentDirectly(agentName)
		return
	}

	// Otherwise, open the app via deep link
	openAppWithDeepLink()
}

type directLaunchArgs struct {
	label             string
	resumeID          string
	resumePicker      bool
	yoloMode          bool
	initialPromptFile string
}

func readInitialPromptFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// parseDirectLaunchArgs parses the wrapper launch flags. attn understands only
// -s, --resume, --yolo, and the internal --initial-prompt-file flag; any other
// argument is an error. We deliberately do not forward unrecognized args to the underlying agent — that implicit
// passthrough was never used by attn itself and only created confusion (e.g.
// `attn --help` printing the agent's help instead of attn's).
func parseDirectLaunchArgs(args []string) (directLaunchArgs, error) {
	parsed := directLaunchArgs{}
	label := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag -s needs a value")
			}
			label = args[i+1]
			i++
		case "--resume":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				parsed.resumeID = args[i+1]
				i++
			} else {
				parsed.resumePicker = true
			}
		case "--yolo":
			parsed.yoloMode = true
		case "--initial-prompt-file":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag --initial-prompt-file needs a value")
			}
			parsed.initialPromptFile = args[i+1]
			i++
		default:
			return directLaunchArgs{}, fmt.Errorf("unknown flag %q", arg)
		}
	}
	if label == "" {
		label = wrapper.DefaultLabel()
	}
	parsed.label = label
	return parsed, nil
}

func mergeEnv(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	idx := map[string]int{}
	merged := make([]string, 0, len(base)+len(extra))
	add := func(entry string) {
		key := entry
		if split := strings.Index(entry, "="); split >= 0 {
			key = entry[:split]
		}
		if pos, ok := idx[key]; ok {
			merged[pos] = entry
			return
		}
		idx[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range extra {
		add(entry)
	}
	return merged
}

func runAgentDirectly(requestedAgent string) {
	parsed, err := parseDirectLaunchArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn: %v\n\n", err)
		writeHelp(os.Stderr)
		os.Exit(1)
	}

	pathutil.EnsureGUIPath()

	driver := agentdriver.Get(requestedAgent)
	if driver == nil {
		fmt.Fprintf(os.Stderr, "warning: unknown ATTN_AGENT %q, defaulting to codex\n", requestedAgent)
		driver = agentdriver.MustGet("codex")
	}
	caps := agentdriver.EffectiveCapabilities(driver)

	if !caps.HasResume && (parsed.resumeID != "" || parsed.resumePicker) {
		fmt.Fprintf(os.Stderr, "warning: %s resume not supported yet (ignoring --resume)\n", driver.Name())
		parsed.resumeID = ""
		parsed.resumePicker = false
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}
	initialPrompt := ""
	if parsed.initialPromptFile != "" {
		content, readErr := readInitialPromptFile(parsed.initialPromptFile)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "error reading initial prompt: %v\n", readErr)
			os.Exit(1)
		}
		initialPrompt = content
	}

	c := client.New("")
	managedMode := os.Getenv("ATTN_DAEMON_MANAGED") == "1"
	if !managedMode && !c.IsRunning() {
		if err := startDaemonBackground(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start daemon: %v\n", err)
		}
	}

	sessionID := os.Getenv("ATTN_SESSION_ID")
	if sessionID == "" {
		sessionID = wrapper.GenerateSessionID()
	}
	if !managedMode {
		if err := c.RegisterWithAgent(sessionID, parsed.label, cwd, driver.Name()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not register session: %v\n", err)
		}
	}

	opts := agentdriver.SpawnOpts{
		SessionID:       sessionID,
		CWD:             cwd,
		Label:           parsed.label,
		InitialPrompt:   initialPrompt,
		ResumeSessionID: parsed.resumeID,
		ResumePicker:    parsed.resumePicker,
		YoloMode:        parsed.yoloMode,
		Executable:      driver.ResolveExecutable(""),
		SocketPath:      config.SocketPath(),
		WrapperPath:     resolveWrapperPath(),
	}

	if preparer, ok := driver.(agentdriver.LaunchPreparer); ok {
		if err := preparer.PrepareLaunch(opts); err != nil {
			fmt.Fprintf(os.Stderr, "warning: launch preparation failed for %s: %v\n", driver.Name(), err)
		}
	}

	cleanupFns := []func(){}
	cleanup := func() {
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			cleanupFns[i]()
		}
		if !managedMode {
			c.Unregister(sessionID)
		}
	}

	hasHooks := false
	if agentdriver.EffectiveCapabilities(driver).HasWorkspaceContext {
		// A chief-of-staff session gets Notebook guidance (its profile-wide
		// durable home) in place of the workspace-context checkout. A fresh
		// session is never the chief, so this only fires on relaunch/recovery of
		// an already-chief session; otherwise (incl. a lookup error) we fall
		// through to the workspace-context checkout.
		if root := resolveChiefNotebookRoot(c, sessionID); root != "" {
			opts.NotebookRoot = root
		} else {
			contextPath, checkoutErr := workspaceContextCheckoutPath(c, sessionID, 40, 25*time.Millisecond)
			if checkoutErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not prepare workspace context guidance: %v\n", checkoutErr)
			} else {
				opts.WorkspaceContextPath = contextPath
			}
		}
	}
	// The daemon's worker exports ATTN_WORKFLOW_GUIDANCE_ENABLED when the
	// workflows_enabled setting is on. This launch path is the worker process, so
	// the env var (not a store read) carries the gate here.
	opts.InjectWorkflowGuidance = strings.TrimSpace(os.Getenv("ATTN_WORKFLOW_GUIDANCE_ENABLED")) == "1"
	// Likewise the worker exports ATTN_AUTO_APPROVE when the auto_approve_enabled
	// setting is on, so the launched agent starts in its native auto-approve mode.
	opts.AutoApprove = strings.TrimSpace(os.Getenv("ATTN_AUTO_APPROVE")) == "1"
	// ATTN_MODEL pins the launch's model (chief_model_<agent> for chief
	// launches, delegate --model for delegations); ATTN_EFFORT pins the
	// reasoning effort (delegate --effort).
	opts.Model = strings.TrimSpace(os.Getenv("ATTN_MODEL"))
	opts.Effort = strings.TrimSpace(os.Getenv("ATTN_EFFORT"))
	// ATTN_CHIEF_AUTO_COMPACT_WINDOW caps the chief's context window. The worker
	// exports it only for chief launches, so a delegated agent never sees it.
	if window := strings.TrimSpace(os.Getenv("ATTN_CHIEF_AUTO_COMPACT_WINDOW")); window != "" {
		if n, err := strconv.Atoi(window); err == nil && n > 0 {
			opts.AutoCompactWindow = n
		}
	}
	if cp, ok := agentdriver.GetConfigOverrideProvider(driver); ok {
		opts.ConfigOverrides = cp.GenerateConfigOverrides(opts)
	}
	if hp, ok := agentdriver.GetHookProvider(driver); ok {
		content := hp.GenerateHooksConfig(sessionID, opts.SocketPath, opts.WrapperPath)
		settingsPath, err := wrapper.WriteSettingsConfig(os.TempDir(), sessionID, content)
		if err != nil {
			cleanup()
			fmt.Fprintf(os.Stderr, "error writing hooks config: %v\n", err)
			os.Exit(1)
		}
		opts.SettingsPath = settingsPath
		hasHooks = true
		cleanupFns = append(cleanupFns, func() { wrapper.CleanupHooksConfig(settingsPath) })
	}

	cmd := driver.BuildCommand(opts)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// If this wrapper was launched from inside another agent's session (e.g. a
	// terminal that is itself a Claude Code session), drop that session's
	// identity so the agent we launch gets a fresh one. Only the identity vars
	// are scrubbed here: this path inherits the live shell env directly, so
	// tuning vars the user exported in their profile must be left intact.
	config.ScrubAgentSessionIdentityEnv()
	cmd.Env = mergeEnv(os.Environ(), driver.BuildEnv(opts))

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "error starting %s: %v\n", driver.Name(), err)
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	err = cmd.Wait()

	if !hasHooks {
		transcriptPath := ""
		if tf, ok := agentdriver.GetTranscriptFinder(driver); ok {
			if opts.ResumeSessionID != "" {
				transcriptPath = tf.FindTranscriptForResume(opts.ResumeSessionID)
			}
			if transcriptPath == "" {
				transcriptPath = tf.FindTranscript(sessionID, cwd, startedAt)
			}
		}
		if sendErr := c.SendStop(sessionID, transcriptPath); sendErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not send stop: %v\n", sendErr)
		}
	}

	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func resolveWrapperPath() string {
	if value := strings.TrimSpace(os.Getenv("ATTN_WRAPPER_PATH")); value != "" {
		return value
	}
	if exePath, err := os.Executable(); err == nil && strings.TrimSpace(exePath) != "" {
		return exePath
	}
	return "attn"
}

// openAppWithDeepLink opens the Tauri app with a deep link to spawn a session
func openAppWithDeepLink() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}

	// Parse -s flag for label
	label := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			label = args[i+1]
			break
		}
	}
	if label == "" {
		label = filepath.Base(cwd)
	}

	// Build deep link URL. Scheme is profile-scoped so `attn` in a
	// dev-scoped shell (ATTN_PROFILE=dev) opens attn-dev.app via its
	// `attn-dev://` registration instead of the prod app.
	deepLink := fmt.Sprintf("%s://spawn?cwd=%s&label=%s",
		config.DeepLinkScheme(),
		url.QueryEscape(cwd),
		url.QueryEscape(label))

	// Open via system handler
	cmd := exec.Command("open", deepLink)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error opening app: %v\n", err)
		os.Exit(1)
	}
}

func startDaemonBackground() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	return cmd.Start()
}

func runHookStop() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop [session_id]\n")
		os.Exit(1)
	}

	// Parse hook input from stdin to extract transcript path
	var input hookInput
	transcriptPath := ""
	if err := json.NewDecoder(os.Stdin).Decode(&input); err == nil {
		transcriptPath = input.TranscriptPath
	}
	// Note: We gracefully handle stdin parse errors by sending stop without transcript

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	syncSessionResumeID(c, sessionID, input.SessionID)

	// A Stop is not always terminal: if the turn yields with background work in
	// flight or parked on a scheduled wakeup, report that non-terminal state and
	// skip classification (see nonTerminalStopState for the precedence/rationale).
	// For the chief of staff we relax the background-work -> "working" rule so a
	// chief merely watching its delegations is not pegged green; only resolve the
	// (daemon-owned) chief role when there is background work to relax, so normal
	// stops pay nothing.
	relaxBackgroundWork := false
	if hasActiveBackgroundTask(input) {
		relaxBackgroundWork = sessionIsChiefOfStaff(c, sessionID)
	}
	if state := nonTerminalStopState(input, relaxBackgroundWork); state != "" {
		if err := c.UpdateState(sessionID, state); err != nil {
			fmt.Fprintf(os.Stderr, "error sending %s state: %v\n", state, err)
			os.Exit(1)
		}
		return
	}

	// Send stop event to daemon for classification
	if err := c.SendStop(sessionID, transcriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "error sending stop: %v\n", err)
		os.Exit(1)
	}
}

func runHookSessionStart() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-session-start [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	// The SessionStart hook syncs the agent's native session ID back to attn for
	// resume, then emits workspace-context guidance as a fallback for sessions that
	// did not receive it at launch (--append-system-prompt / developer_instructions).
	// The launch path sets ATTN_WORKSPACE_CONTEXT_GUIDANCE / ATTN_CHIEF_GUIDANCE so
	// workspaceContextGuidanceProvidedAtLaunch suppresses the fallback when guidance
	// was already injected.
	syncSessionResumeID(c, sessionID, input.SessionID)
	output, err := workspaceContextSessionStartOutput(c, sessionID, 40, 25*time.Millisecond)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load workspace context guidance: %v\n", err)
		return
	}
	if output != "" && !workspaceContextGuidanceProvidedAtLaunch() {
		fmt.Fprintln(os.Stdout, output)
	}
}

// workspaceContextSessionStartOutput checks out this session's workspace context
// and wraps it as SessionStart hook JSON, used as the launch-independent fallback
// path. Returns "" with no error when the session has no checkout.
func workspaceContextSessionStartOutput(
	c workspaceContextCheckoutClient,
	sessionID string,
	attempts int,
	retryDelay time.Duration,
) (string, error) {
	path, err := workspaceContextCheckoutPath(c, sessionID, attempts, retryDelay)
	if err != nil {
		return "", err
	}
	return hooks.WorkspaceContextSessionStartOutput(path), nil
}

func workspaceContextGuidanceProvidedAtLaunch() bool {
	// Either marker means launch-time guidance was already injected, so the
	// SessionStart hook must not also emit workspace-context guidance. A chief
	// session is launched with chief guidance in place of workspace context.
	return strings.TrimSpace(os.Getenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE")) != "" ||
		strings.TrimSpace(os.Getenv("ATTN_CHIEF_GUIDANCE")) != ""
}

type workspaceContextCheckoutClient interface {
	CheckoutWorkspaceContext(sourceSessionID string, force bool) (*protocol.WorkspaceContextResult, error)
}

func workspaceContextCheckoutPath(
	c workspaceContextCheckoutClient,
	sessionID string,
	attempts int,
	retryDelay time.Duration,
) (string, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := c.CheckoutWorkspaceContext(sessionID, false)
		if err == nil {
			return result.Path, nil
		}
		lastErr = err
		if attempt+1 < attempts && retryDelay > 0 {
			time.Sleep(retryDelay)
		}
	}
	return "", lastErr
}

func runHookState() {
	sessionID, state := parseHookStateArgs()
	if sessionID == "" || state == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-state [session_id] <state>\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	syncSessionResumeID(c, sessionID, input.SessionID)
	if err := c.UpdateState(sessionID, state); err != nil {
		fmt.Fprintf(os.Stderr, "error updating state: %v\n", err)
		os.Exit(1)
	}
}

func runHookTodo() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-todo [session_id]\n")
		os.Exit(1)
	}

	// Parse hook input from stdin
	var input hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return // Silently fail if no input
	}
	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	syncSessionResumeID(c, sessionID, input.SessionID)

	// Parse tool_input to extract todos
	var todoInput todoWriteInput
	if err := json.Unmarshal(input.ToolInput, &todoInput); err != nil {
		return // Silently fail if parse error
	}

	// Format todos with status markers
	var todos []string
	for _, t := range todoInput.Todos {
		var marker string
		switch t.Status {
		case "completed":
			marker = "[✓]"
		case "in_progress":
			marker = "[→]"
		default:
			marker = "[ ]"
		}
		todos = append(todos, fmt.Sprintf("%s %s", marker, t.Content))
	}

	if err := c.UpdateTodos(sessionID, todos); err != nil {
		fmt.Fprintf(os.Stderr, "error updating todos: %v\n", err)
		os.Exit(1)
	}
}

func hookSessionIDFromArgOrEnv(index int) string {
	if len(os.Args) > index {
		return strings.TrimSpace(os.Args[index])
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func parseHookStateArgs() (sessionID string, state string) {
	switch {
	case len(os.Args) >= 4:
		return strings.TrimSpace(os.Args[2]), strings.TrimSpace(os.Args[3])
	case len(os.Args) >= 3:
		return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID")), strings.TrimSpace(os.Args[2])
	default:
		return "", ""
	}
}

func syncSessionResumeID(c *client.Client, attnSessionID, agentSessionID string) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return
	}
	if err := c.SetSessionResumeID(attnSessionID, agentSessionID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not sync resume session id: %v\n", err)
	}
}
