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

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/present"
	"github.com/victorarias/attn/internal/probetui"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/workflowresult"
	"github.com/victorarias/attn/internal/wrapper"
	"golang.org/x/sys/unix"
)

var (
	// Kept for builders that inject build metadata into the main package instead of internal/buildinfo.
	version           = ""
	buildTime         = ""
	sourceFingerprint = ""
	gitCommit         = ""
)

type hookInput struct {
	SessionID        string           `json:"session_id"`
	TranscriptPath   string           `json:"transcript_path"`
	Prompt           string           `json:"prompt"`
	ToolName         string           `json:"tool_name"`
	ToolInput        json.RawMessage  `json:"tool_input"`
	ToolResponse     json.RawMessage  `json:"tool_response"`
	CWD              string           `json:"cwd"`
	BackgroundTasks  []backgroundTask `json:"background_tasks"`
	SessionCrons     []sessionCron    `json:"session_crons"`
	PermissionMode   string           `json:"permission_mode"`
	Message          string           `json:"message"`
	NotificationType string           `json:"notification_type"`
	AgentID          string           `json:"agent_id"`
	ErrorType        string           `json:"error_type"`
	ErrorMessage     string           `json:"error_message"`
	Trigger          string           `json:"trigger"`
}

// Verified against Claude Code 2.1.257: type is shell or subagent, status running, and
// description is the human-readable label (the docs' name field is not sent).
type backgroundTask struct {
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// Verified against Claude Code 2.1.177: there is no status field — a fired or deleted cron drops out of the list.
type sessionCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
	Prompt    string `json:"prompt"`
}

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

	if len(os.Args) >= 2 && os.Args[1] == "profile-env" {
		runProfileEnv()
		return
	}

	if err := config.ValidateProfile(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if !isProfileGroupCommand(os.Args) {
		if err := config.ValidateProfileRouting(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
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
	case "client-token":
		runClientToken()
	case "pty-worker":
		runPTYWorker()
	case "workflow":
		maybePrintProfileBanner()
		runWorkflow()
	case "automation":
		maybePrintProfileBanner()
		runAutomationCommand()
	case "preflight":
		maybePrintProfileBanner()
		runPreflight()
	case "pr":
		runPRCommand()
	case "plugin":
		maybePrintProfileBanner()
		runPluginCommand()
	case "list":
		maybePrintProfileBanner()
		runList()
	case "presence":
		runPresence()
	case "skill":
		runSkill()
	case "prompts":
		runPrompts()
	case "activity":
		maybePrintProfileBanner()
		runActivity()
	case "delegate":
		maybePrintProfileBanner()
		runDelegate()
	case "ticket":
		maybePrintProfileBanner()
		runTicket()
	case "session":
		maybePrintProfileBanner()
		runSession()
	case "agent":
		maybePrintProfileBanner()
		runAgent()
	case "state":
		maybePrintProfileBanner()
		runState()
	case "debug":
		maybePrintProfileBanner()
		runDebug()
	case "db":
		maybePrintProfileBanner()
		runDB()
	case "bus":
		maybePrintProfileBanner()
		runBus()
	case "enrollment":
		maybePrintProfileBanner()
		runEnrollment()
	case "seed":
		maybePrintProfileBanner()
		runSeed()
	case "crew":
		maybePrintProfileBanner()
		runCrew()
	case "handoff":
		maybePrintProfileBanner()
		runHandoff(os.Args[2:])
	case "doc":
		maybePrintProfileBanner()
		runDoc()
	case "app":
		maybePrintProfileBanner()
		runApp()
	case "automode":
		maybePrintProfileBanner()
		runAutoMode()
	case "journal":
		maybePrintProfileBanner()
		runJournal()
	case "vision-check":
		// No banner: stdout must stay pure for machine consumption by the caller.
		runVisionCheck()
	case "present":
		maybePrintProfileBanner()
		runPresent()
	case "workspace":
		maybePrintProfileBanner()
		runWorkspace()
	case "profile":
		// No banner: `attn profile resolve --field …` must print only the value for the Makefile / harness.
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
	case "_hook-notification":
		runHookNotification()
	case "_hook-stop-failure":
		runHookStopFailure()
	case "_hook-compact":
		runHookCompact()
	case "_hook-state":
		runHookState()
	case "_hook-todo":
		runHookTodo()
	case "_hook-tool-use":
		runHookToolUse()
	case "_probe-tui":
		runProbeTUI()
	default:
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

func maybePrintProfileBanner() {
	config.PrintProfileBanner(os.Stderr)
}

func isProfileGroupCommand(args []string) bool {
	return len(args) >= 2 && args[1] == "profile"
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
	var approvalRoute string
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
	fs.StringVar(&approvalRoute, "approval-route", "", "effective approval route recorded for daemon recovery")
	fs.StringVar(&cfg.InitialPromptFile, "initial-prompt-file", "", "file containing the initial agent prompt")
	fs.StringVar(&cfg.ThemeForeground, "theme-foreground", "", "terminal foreground color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeBackground, "theme-background", "", "terminal background color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeCursor, "theme-cursor", "", "terminal cursor color seeded for OSC 10/11/12 queries")
	var themeANSIPaletteJSON string
	fs.StringVar(&themeANSIPaletteJSON, "theme-ansi-palette-json", "", "JSON array of the 16 terminal ANSI palette colors")
	fs.StringVar(&cfg.Executable, "executable", "", "selected agent executable override")
	fs.StringVar(&cfg.ClaudeExecutable, "claude-executable", "", "claude executable override")
	fs.StringVar(&cfg.CodexExecutable, "codex-executable", "", "codex executable override")
	fs.StringVar(&cfg.CopilotExecutable, "copilot-executable", "", "copilot executable override")
	var externalCommandJSON string
	var unattendedLaunchJSON string
	fs.StringVar(&externalCommandJSON, "external-command-json", "", "external plugin driver argv as JSON")
	fs.StringVar(&unattendedLaunchJSON, "unattended-launch-json", "", "immutable unattended launch contract as JSON")
	fs.StringVar(&cfg.ExternalCWD, "external-cwd", "", "external plugin driver working directory")
	fs.StringVar(&cfg.AdoptHandoff, "adopt-handoff", "", "handoff file left by the worker image this one replaces")
	fs.IntVar(&cfg.AdoptPtmxFD, "adopt-ptmx-fd", 0, "inherited pty master descriptor")
	fs.IntVar(&cfg.AdoptListenerFD, "adopt-listener-fd", 0, "inherited unix listener descriptor")
	fs.StringVar(&cfg.RegistryPath, "registry-path", "", "registry path")
	fs.StringVar(&cfg.SocketPath, "socket-path", "", "socket path")
	fs.StringVar(&cfg.ControlToken, "control-token", "", "control token")
	fs.IntVar(&cfg.OwnerPID, "owner-pid", 0, "daemon owner pid")
	fs.StringVar(&cfg.OwnerStartedAt, "owner-started-at", "", "daemon owner started-at timestamp")
	fs.StringVar(&cfg.OwnerNonce, "owner-nonce", "", "daemon owner nonce")

	_ = fs.Parse(os.Args[2:])
	if approvalRoute != "" {
		cfg.ApprovalRoute = launchcontract.ApprovalRoute(approvalRoute)
		if !cfg.ApprovalRoute.Valid() {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --approval-route %q\n", approvalRoute)
			os.Exit(1)
		}
	}
	if themeANSIPaletteJSON != "" {
		if err := json.Unmarshal([]byte(themeANSIPaletteJSON), &cfg.ThemeANSIPalette); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --theme-ansi-palette-json: %v\n", err)
			os.Exit(1)
		}
	}
	if externalCommandJSON != "" {
		if err := json.Unmarshal([]byte(externalCommandJSON), &cfg.ExternalCommand); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --external-command-json: %v\n", err)
			os.Exit(1)
		}
	}
	if unattendedLaunchJSON != "" {
		if err := json.Unmarshal([]byte(unattendedLaunchJSON), &cfg.UnattendedLaunch); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			os.Exit(1)
		}
		if err := cfg.UnattendedLaunch.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			os.Exit(1)
		}
	}
	if externalEnvJSON := os.Getenv("ATTN_PTY_EXTERNAL_ENV"); externalEnvJSON != "" {
		if err := json.Unmarshal([]byte(externalEnvJSON), &cfg.ExternalEnv); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid ATTN_PTY_EXTERNAL_ENV: %v\n", err)
			os.Exit(1)
		}
	}
	if daemonEnvJSON := os.Getenv("ATTN_PTY_DAEMON_ENV"); daemonEnvJSON != "" {
		if err := json.Unmarshal([]byte(daemonEnvJSON), &cfg.DaemonEnv); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid ATTN_PTY_DAEMON_ENV: %v\n", err)
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

	// The daemon scrubs these too; a worker spawned from an unscrubbed parent self-protects.
	if scrubbed := config.ScrubInheritedAgentSessionEnv(); len(scrubbed) > 0 {
		cfg.Logf("scrubbed inherited agent session env before startup: %v", scrubbed)
	}

	if err := ptyworker.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-worker error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemonCommand() {
	if len(os.Args) < 3 {
		runDaemon()
		return
	}
	switch os.Args[2] {
	case "ensure":
		runDaemonEnsure()
	case "stop":
		runDaemonStop()
	default:
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand %q (expected: ensure, stop)\n", os.Args[2])
		os.Exit(1)
	}
}

func runDaemon() {
	// Routing fence again, before a PID lock and a DB migration: never boot into another profile's data dir.
	if err := config.ValidateProfileRouting(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	socketPath := config.SocketPath()
	if err := config.ValidateDaemonIsolation(socketPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	d := daemon.New(socketPath)
	// Must precede Start(), which warms the login-shell env cache.
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

func runDaemonStop() {
	result, err := daemonctl.Stop(config.PIDPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon stop error: %v\n", err)
		os.Exit(1)
	}
	if result.Stopped {
		if result.Forced {
			fmt.Printf("stopped daemon (pid %d) (force-killed)\n", result.PID)
		} else {
			fmt.Printf("stopped daemon (pid %d)\n", result.PID)
		}
		return
	}
	fmt.Printf("daemon %s\n", result.Note)
}

func runClientToken() {
	token := config.ClientToken()
	if token == "" {
		fmt.Fprintf(os.Stderr, "no client token at %s — the daemon mints it at startup; start it first\n", config.ClientTokenPath())
		os.Exit(1)
	}
	fmt.Println(token)
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
  agent list                        name every session running here
  agent peek <session-or-member>    read a session without interrupting it
  agent msg <target> "text"            notify a live session, crew member or seed's tender
  agent inbox [message-id]              read durable agent notifications
  agent msg-status <message-id>         inspect a sent peer message
	  session <command>                 inspect a session's conversation
  state explain <id>                replay why a session's state is what it is
  delegate --brief-file <path> --model <name>  start another agent with a delegated brief
  delegate --plot <seed-id> --model <name>     start another agent at an existing seed
  journal append --entry <text>     serialized append to the daily notebook journal
  workspace context <command>       edit shared workspace context
  open <file.md|seed-id> [--session <id>]   show a document in attn
  browser <command>                 open and control the in-app browser
  workflow <command>                run, inspect, and resume durable workflows
  automation <command>              manage and run durable automations
  preflight                         diagnose tools, paths, routing, and launch settings
  pr wait-ready <pr>                wait for exact-head checks and approval
  pr record|ls|forget <url>         pull requests this session opened
  list                              list sessions and workspaces
  activity [clear <id>]             what each agent is doing right now
  present <command>                 open a review presentation and read feedback
  debug <command>                   probe debug artifacts (incidents, logs)
  db <command>                      database maintenance (restore from backup)
  bus <command>                     event bus: consumer cursors, lag, kill switch
  enrollment <command>              this daemon's home: status, enroll, leave
  seed <command>                    the garden: plant, tend, harvest, note, ls
  crew <command>                    the crew roster: who exists, who is awake
  handoff -m "<letter>"             file this crew member's letter; the day turns over
  doc <command>                     document store: collections, documents, live queries
  app <command>                     apps: list, status, enable, disable, remove
  vision-check <image> <question>   answer a question about an image (single LLM call)
  daemon <command>                  manage the daemon
	  daemon ensure|stop                ensure the daemon is running, or stop it
  profile <status|resolve|list>     show / resolve the active profile's resources
  profile-env <profile|--unset>     print shell commands for selecting a profile
  skill [--reference <name>|--list] print the bundled agent skill and its references
  prompts <command>                inspect prompt sources and scenario composition
  version                           print version information
`)
}

func runDelegate() {
	if len(os.Args) >= 3 && os.Args[2] == "roles" {
		if len(os.Args) > 4 || (len(os.Args) == 4 && os.Args[3] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: attn delegate roles [--json]")
			os.Exit(2)
		}
		result, err := client.New("").DelegationRoles()
		if err != nil {
			fmt.Fprintf(os.Stderr, "delegate roles: %v\n", err)
			os.Exit(1)
		}
		if len(os.Args) == 4 {
			printJSON(result)
		} else {
			fmt.Println(prompts.DelegationRolesText(*result))
		}
		return
	}
	if len(os.Args) == 3 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
		writeDelegateHelp(os.Stdout)
		return
	}
	if len(os.Args) >= 3 && os.Args[2] == "status" {
		if len(os.Args) != 4 || strings.TrimSpace(os.Args[3]) == "" {
			fmt.Fprintln(os.Stderr, "delegate status: usage: attn delegate status <request-or-operation-id>")
			os.Exit(2)
		}
		result, err := client.New("").DelegationStatus(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "delegate status: %v\n", err)
			os.Exit(1)
		}
		printJSON(result)
		return
	}
	args, err := parseDelegateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(2)
	}
	warnIfDaemonVersionMismatch()
	c := client.New("")
	// Must print before crossing the transport: the daemon may durably accept the request even if the response never arrives.
	fmt.Fprintf(os.Stderr, "delegation request: request_id=%s\n", args.options.RequestID)
	operation, err := c.StartDelegation(args.sourceSessionID, args.brief, args.options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "delegation accepted: request_id=%s operation_id=%s session_id=%s\n",
		operation.RequestID, operation.OperationID, operation.SessionID)
	operation, err = waitDelegationCLI(c, operation, args.options.RequestID, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(1)
	}
	if operation.Result != nil && operation.Result.FirstTurnUnconfirmed != nil {
		fmt.Fprintf(os.Stderr, "delegate: %s\n", *operation.Result.FirstTurnUnconfirmed)
	}
	printJSON(operation.Result)
}

func waitDelegationCLI(
	c *client.Client,
	operation *protocol.DelegationOperation,
	requestID string,
	progress io.Writer,
) (*protocol.DelegationOperation, error) {
	lastProgress := ""
	for operation.State == protocol.DelegationOperationStateAccepted || operation.State == protocol.DelegationOperationStatePreparing {
		if operation.Progress != "" && operation.Progress != lastProgress {
			fmt.Fprintf(progress, "delegation progress: %s\n", operation.Progress)
			lastProgress = operation.Progress
		}
		time.Sleep(250 * time.Millisecond)
		next, err := c.DelegationStatus(operation.OperationID)
		if err != nil {
			return nil, fmt.Errorf("outcome unknown; inspect or retry with --request-id %s: %w", requestID, err)
		}
		operation = next
	}
	if operation.State == protocol.DelegationOperationStateFailed {
		return nil, fmt.Errorf("%s (request_id=%s)", protocol.Deref(operation.Error), operation.RequestID)
	}
	if operation.Result == nil {
		return nil, errors.New("completed operation has no result")
	}
	return operation, nil
}

func writeDelegateHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn delegate (--brief <text> | --brief-file <path>) [--model <name> | --role <id> | --fallback] [options]

A delegation binds a seed: the brief is its body, the delegate its tender, and
the seed is where the delegate reports. Tickets retired.

task source:
  --brief <text>              delegate a short task; it becomes the seed's body
  --brief-file <path>         delegate a task file; it becomes the seed's body

workspace placement (where the pane appears):
  (no flags)                 add a pane to the source workspace
  --new-workspace            create a workspace for the delegated pane
  --workspace <id>           add a pane to an existing workspace; this does not
                             choose that workspace's repository
  --cwd <path>               create a workspace and use this checkout/repository

repository placement (where the agent runs):
  (no flags)                 create a worktree of the source checkout's
                             repository (with --workspace: the repository that
                             workspace's sessions are in); a non-repository
                             source is refused, pass --cwd or --no-worktree
  --no-worktree              reuse the source checkout; with --workspace, only
                             the pane moves to the target workspace
  --worktree <branch>        choose the new worktree's branch
  --repo <path>              main repository; required when the target
                             workspace's sessions span several
  --from <ref>               branch or ref to start from
  --worktree-path <path>     override the generated sibling path

session options:
	--request-id <id>          stable retry key (generated and printed when omitted; op- is reserved)
  --agent <name>             configured prompt-capable built-in or plugin agent
  --model <name>             pin the model; required for a manual launch
  --role <id>                use a configured role and its default choice
  --choice <id>              use an alternative within --role
  --fallback                 use the configured unmatched-work fallback
  --preferences-revision <n> require the revision returned by delegate roles
  --provider <id>            provider for a plugin harness model
  --effort <level>           pin the agent's reasoning effort (claude: low,
                             medium, high, xhigh, max; codex: minimal, low,
                             medium, high, xhigh); defaults to medium for agents
                             that support reasoning effort
  --name <text>              name for the agent and, when a new workspace is
                             created, the workspace (max 16 chars, must be
                             unique; defaults to the directory name)
  --source-session <id>      source session (defaults to ATTN_SESSION_ID)
  --yolo                     bypass agent approval prompts
  --plot <seed>              dispatch the delegate at an existing seed instead of
                             planting a new one. The delegate becomes that seed's
                             tender, and the dispatch refuses before creating
                             anything if a live session already holds it. Aimed
                             at a plot it launches knowing that plan, and a
                             flag-free "attn seed ready" inside it answers with
                             the plot's ready seeds. Beyond that seed it is
                             scope, not a fence: who holds each child stays that
                             seed's tender, and --all steps back out to the
                             whole garden.
	--allow-worktree-reuse     explicitly allow another active session to share the worktree

discovery:
  attn delegate roles [--json]  complete active roles, choices, and fallback

inspection:
  attn delegate status <request-or-operation-id>
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

func runTicket() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeTicketHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	switch os.Args[2] {
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
	case "inbox":
		if hasHelpFlag(os.Args[3:]) {
			writeTicketHelp(os.Stdout)
			return
		}
		runTicketInbox(os.Args[3:])
	case "status", "attach", "attach-plan", "new", "comment", "subscribe", "unsubscribe", "take":
		signpostTicketVerb(os.Args[2])
	default:
		fmt.Fprintf(os.Stderr, "ticket: unknown command %q\n", os.Args[2])
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
}

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

type ticketIDArgs struct {
	TicketID string
	Session  string
	JSON     bool
}

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

func runTicketInbox(args []string) {
	fs := flag.NewFlagSet("ticket inbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the unread bundles as JSON")
	watch := fs.Bool("watch", false, "block and print new ticket activity as it lands; silent until something changes")
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

const ticketWatchInterval = 3 * time.Second

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
		return c.TicketInboxWatch(source, interval)
	}, os.Stdout, os.Stderr, jsonOutput)
}

// Report a daemon error once per outage: a wrapping Monitor treats every printed line as new activity.
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
	fmt.Fprintf(w, `usage: attn ticket <command>

Tickets retired with the garden era. Work lives in the garden now: a seed is
the unit of work, and `+"`attn seed --help`"+` is the whole surface. These three read
verbs remain for tickets that predate the garden:

  list [--status <col>] [--all] [--json]
        read the archived board: every ticket (id, column, assignee, title),
        newest first; --json includes each ticket's description. No session
        required.
  show <ticket-id> [--session <id>] [--json]
        print one ticket's full record — description, complete activity thread
        with full bodies, current artifacts; non-consuming
  inbox [--session <id>] [--json] [--watch [--interval <dur>]]
        read and mark read this session's unread legacy ticket activity;
        --watch blocks and prints new activity as it lands

Every other verb (%s) is a signpost: run it
to be told which garden command replaced it, or read `+"`attn skill --reference garden`"+`.
`, ticketSignpostVerbList())
}

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

// Appends through the daemon's single serialized notebook writer; editing journal/<date>.md directly races the keeper.
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

const presentWaitInterval = 3 * time.Second

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

type notebookGuideClient interface {
	NotebookGuide(sessionID string) (*protocol.NotebookGuideResult, error)
}

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
	ticketID := fs.String("ticket", "", "retired: delegations bind a seed, not a ticket")
	confirm := fs.Bool("confirm", false, "retired: went with --ticket")
	agentName := fs.String("agent", "", "target agent (defaults to the source session agent)")
	role := fs.String("role", "", "configured delegation role")
	choice := fs.String("choice", "", "choice within the role")
	fallback := fs.Bool("fallback", false, "configured unmatched-work fallback")
	revision := fs.Int("preferences-revision", 0, "expected configuration revision")
	provider := fs.String("provider", "", "plugin model provider")
	model := fs.String("model", "", "pin the delegated agent's model (alias or full id)")
	effort := fs.String("effort", "", "pin the delegated agent's reasoning effort")
	name := fs.String("name", "", "name for the agent and, when a new workspace is created, the workspace")
	sourceSessionID := fs.String("source-session", "", "source session id (defaults to ATTN_SESSION_ID)")
	yolo := fs.Bool("yolo", false, "launch the target agent in yolo mode")
	plot := fs.String("plot", "", "dispatch the delegate at a plot: it is what flag-free `attn seed ready` answers with")
	newWorkspace := fs.Bool("new-workspace", false, "create a new workspace for the delegated agent")
	workspaceID := fs.String("workspace", "", "place the delegated agent in an existing workspace")
	cwd := fs.String("cwd", "", "use an existing directory in a new workspace")
	worktreeBranch := fs.String("worktree", "", "create a worktree with this branch for the delegated session")
	worktreeRepo := fs.String("repo", "", "main repository for --worktree (defaults to the target's session repository)")
	worktreeStart := fs.String("from", "", "starting ref for --worktree")
	worktreePath := fs.String("worktree-path", "", "custom path for --worktree")
	noWorktree := fs.Bool("no-worktree", false, "reuse the resolved checkout instead of creating a worktree")
	requestID := fs.String("request-id", "", "stable delegation request id")
	allowWorktreeReuse := fs.Bool("allow-worktree-reuse", false, "allow active sessions to share a worktree")
	if err := fs.Parse(args); err != nil {
		return delegateCLIArgs{}, err
	}
	if fs.NArg() != 0 {
		return delegateCLIArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	modelPin := strings.TrimSpace(*model)
	if modelPin == "" && strings.TrimSpace(*role) == "" && !*fallback {
		return delegateCLIArgs{}, errors.New("--model is required (for example, --model gpt-5.6-sol or --model opus); --effort defaults to medium when supported")
	}

	present := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { present[f.Name] = true })
	var expectedRevision *int
	if present["preferences-revision"] {
		if *revision < 0 {
			return delegateCLIArgs{}, errors.New("--preferences-revision must not be negative")
		}
		expectedRevision = revision
	}
	if *fallback && *role != "" {
		return delegateCLIArgs{}, errors.New("--role and --fallback cannot be combined")
	}
	if *choice != "" && *role == "" {
		return delegateCLIArgs{}, errors.New("--choice requires --role")
	}
	if expectedRevision != nil && *role == "" && !*fallback {
		return delegateCLIArgs{}, errors.New("--preferences-revision requires --role or --fallback")
	}
	var modelOverride, effortOverride, providerOverride *string
	if present["model"] {
		value := strings.TrimSpace(*model)
		if value == "default" {
			value = ""
		}
		modelOverride = &value
	}
	if present["effort"] {
		value := strings.TrimSpace(*effort)
		if value == "default" {
			value = ""
		}
		effortOverride = &value
	}
	if present["provider"] && *role == "" && !*fallback {
		return delegateCLIArgs{}, errors.New("--provider requires --role or --fallback; direct plugin delegation uses provider/model")
	}
	if present["provider"] {
		value := strings.TrimSpace(*provider)
		providerOverride = &value
	}
	source := strings.TrimSpace(*sourceSessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return delegateCLIArgs{}, errors.New("no source session; run inside attn or pass --source-session")
	}
	// Retired flags stay parseable so the answer is the signpost, not "flag provided but not defined".
	if ticket := strings.TrimSpace(*ticketID); ticket != "" {
		return delegateCLIArgs{}, fmt.Errorf(
			"--ticket retired: plant the work and dispatch at it — `attn seed plant %q -m \"<brief>\"`, then `attn delegate --brief \"<brief>\" --plot <seed-id>`", ticket)
	}
	if *confirm {
		return delegateCLIArgs{}, errors.New("--confirm retired: it went with --ticket, and a seed is claimed by its tender (`attn seed tend <seed-id>`)")
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
	stableRequestID := strings.TrimSpace(*requestID)
	if stableRequestID == "" {
		stableRequestID = uuid.NewString()
	}
	if explicitWorkspace != "" && (*newWorkspace || customCWD != "") {
		return delegateCLIArgs{}, errors.New("--workspace cannot be combined with --new-workspace or --cwd")
	}
	if *noWorktree && (branch != "" || repo != "" || startingFrom != "" || customWorktreePath != "") {
		return delegateCLIArgs{}, errors.New("--no-worktree cannot be combined with --worktree, --repo, --from, or --worktree-path")
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
			Role: strings.TrimSpace(*role), Choice: strings.TrimSpace(*choice), Fallback: *fallback, PreferencesRevision: expectedRevision, Provider: providerOverride, ModelOverride: modelOverride, EffortOverride: effortOverride,
			RequestID:          stableRequestID,
			Agent:              strings.TrimSpace(*agentName),
			Model:              modelPin,
			Effort:             strings.TrimSpace(*effort),
			Label:              strings.TrimSpace(*name),
			Yolo:               *yolo,
			Placement:          placement,
			Plot:               strings.TrimSpace(*plot),
			WorkspaceID:        explicitWorkspace,
			CWD:                customCWD,
			WorktreeRepo:       repo,
			Worktree:           branch,
			WorktreePath:       customWorktreePath,
			StartingFrom:       startingFrom,
			NoWorktree:         *noWorktree,
			AllowWorktreeReuse: *allowWorktreeReuse,
		},
	}, nil
}

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
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}

	if len(positionals) == 0 {
		return "", "", fmt.Errorf("missing <file.md|seed-id> argument")
	}
	if len(positionals) > 1 {
		return "", "", fmt.Errorf("unexpected extra arguments: %v", positionals[1:])
	}
	return strings.TrimSpace(positionals[0]), strings.TrimSpace(*sessionID), nil
}

func isSeedOpenTarget(target string) bool {
	return strings.HasPrefix(strings.TrimSpace(target), "s-")
}

func runOpen() {
	warnIfDaemonVersionMismatch()
	rawPath, sessionFlag, err := parseOpenArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn open: %v\nusage: attn open <file.md|seed-id> [--session <id>]\n", err)
		os.Exit(1)
	}

	resolvedSession := sessionFlag
	if resolvedSession == "" {
		resolvedSession = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if isSeedOpenTarget(rawPath) {
		if err := c.OpenSeed(rawPath, resolvedSession); err != nil {
			fmt.Fprintf(os.Stderr, "open: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("opened %s\n", rawPath)
		return
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: resolve path: %v\n", err)
		os.Exit(1)
	}
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
	if os.Getenv("ATTN_INSIDE_APP") == "1" {
		agentName := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_AGENT")))
		if agentName == "" {
			agentName = "codex"
		}
		runAgentDirectly(agentName)
		return
	}

	openAppWithDeepLink()
}

type directLaunchArgs struct {
	label             string
	resumeID          string
	resumePicker      bool
	yoloMode          bool
	initialPromptFile string
	member            string
}

func readInitialPromptFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

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
		case "--member":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag --member needs a value: the crew member this session launches as (`attn crew list` names the roster)")
			}
			parsed.member = args[i+1]
			i++
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
		if parsed.member != "" {
			label = crew.DisplayName(parsed.member)
		} else {
			label = wrapper.DefaultLabel()
		}
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
	if managedMode && parsed.member != "" {
		fmt.Fprintf(os.Stderr, "attn: --member names a member to launch as; a daemon-managed launch is already bound by `attn crew wake`\n")
		os.Exit(1)
	}
	if !managedMode {
		err := c.RegisterAsMember(sessionID, parsed.label, cwd, driver.Name(), parsed.member)
		switch {
		case err != nil && parsed.member != "":
			fmt.Fprintf(os.Stderr, "attn: %v\n", err)
			os.Exit(1)
		case err != nil:
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
	if agentdriver.EffectiveCapabilities(driver).HasLaunchInstructions {
		opts.NotebookRoot = resolveChiefNotebookRoot(c, sessionID)
	}
	if _, err := c.SeedReady(sessionID, "", false); err == nil {
		opts.Garden = true
	}
	opts.SelfReportPullRequests = !caps.HasHooks
	if prime, err := c.CrewPrime(sessionID); err == nil {
		opts.CrewPriming = protocol.Deref(prime.Guidance)
		opts.AwarenessDirs = prime.AwarenessDirs
	}
	opts.InjectWorkflowGuidance = consumeOneShotBoolEnv("ATTN_WORKFLOW_GUIDANCE_ENABLED")
	opts.AutoApprove = consumeOneShotBoolEnv("ATTN_AUTO_APPROVE")
	opts.TrustWorkingDirectory = consumeOneShotBoolEnv("ATTN_TRUST_WORKING_DIRECTORY")
	opts.Model = consumeOneShotEnv("ATTN_MODEL")
	opts.Effort = consumeOneShotEnv("ATTN_EFFORT")
	// The old env name is read as a fallback so a not-yet-restarted daemon still caps its chief.
	window := consumeOneShotEnv("ATTN_AUTO_COMPACT_WINDOW")
	if window == "" {
		window = consumeOneShotEnv("ATTN_CHIEF_AUTO_COMPACT_WINDOW")
	}
	if window != "" {
		if n, err := strconv.Atoi(window); err == nil && n > 0 {
			opts.AutoCompactWindow = n
		}
	}
	if cp, ok := agentdriver.GetConfigOverrideProvider(driver); ok {
		opts.ConfigOverrides = cp.GenerateConfigOverrides(opts)
	}
	if hp, ok := agentdriver.GetHookProvider(driver); ok {
		content := hp.GenerateHooksConfig(opts)
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
	if ip, ok := agentdriver.GetInstructionsFileProvider(driver); ok {
		name, content := ip.GenerateInstructionsFile(opts)
		if strings.TrimSpace(content) != "" {
			dir, err := wrapper.WriteInstructionsDir(os.TempDir(), sessionID, name, content)
			if err != nil {
				cleanup()
				fmt.Fprintf(os.Stderr, "error writing launch instructions: %v\n", err)
				os.Exit(1)
			}
			opts.InstructionsDir = dir
			cleanupFns = append(cleanupFns, func() { wrapper.CleanupInstructionsDir(dir) })
		}
	}

	cmd := driver.BuildCommand(opts)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Identity vars only: this path inherits the live shell env, so the user's exported tuning vars must survive.
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
		if sendErr := c.SendStop(sessionID, transcriptPath, client.StopFacts{}); sendErr != nil {
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

func consumeOneShotBoolEnv(key string) bool {
	return consumeOneShotEnv(key) == "1"
}

func consumeOneShotEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	_ = os.Unsetenv(key)
	return value
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

func openAppWithDeepLink() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}

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

	deepLink := fmt.Sprintf("%s://spawn?cwd=%s&label=%s",
		config.DeepLinkScheme(),
		url.QueryEscape(cwd),
		url.QueryEscape(label))

	if err := launchDeepLink(deepLink); err != nil {
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

	var input hookInput
	transcriptPath := ""
	if err := json.NewDecoder(os.Stdin).Decode(&input); err == nil {
		transcriptPath = input.TranscriptPath
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.SendStop(sessionID, transcriptPath, stopFacts(input)); err != nil {
		fmt.Fprintf(os.Stderr, "error sending stop: %v\n", err)
		os.Exit(1)
	}
}

func stopFacts(input hookInput) client.StopFacts {
	facts := client.StopFacts{PendingSessionCrons: len(input.SessionCrons)}
	for _, t := range input.BackgroundTasks {
		facts.BackgroundTasks = append(facts.BackgroundTasks, protocol.StopBackgroundTask{
			Type:   t.Type,
			Status: t.Status,
			Name:   protocol.Ptr(t.Description),
		})
	}
	return facts
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
	output, primeErr := sessionStartHookOutput(c, sessionID, input)
	if primeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load garden status: %v\n", primeErr)
	}

	if output != "" {
		fmt.Fprintln(os.Stdout, output)
	}
}

type sessionStartHookClient interface {
	agentConversationObserver
	sessionStartClient
}

func sessionStartHookOutput(c sessionStartHookClient, sessionID string, input hookInput) (output string, primeErr error) {
	observeAgentConversation(c, sessionID, input.SessionID, input.TranscriptPath)
	contexts, primeErr := sessionStartContexts(c, sessionID)
	return hooks.SessionStartOutput(contexts...), primeErr
}

type sessionStartClient interface {
	SeedReady(sessionID, plot string, all bool) (*protocol.SeedReadyResult, error)
}

// A bare harness launched outside attn's wrapper still gets the agent guidance here.
func sessionStartContexts(c sessionStartClient, sessionID string) (contexts []string, primeErr error) {
	if !launchGuidanceProvided() {
		contexts = append(contexts, hooks.AgentGuidance)
	}

	ready, primeErr := c.SeedReady(sessionID, "", false)
	if primeErr == nil {
		contexts = append(contexts, seedPrimeTailFromReady(ready))
	}
	return contexts, primeErr
}

func launchGuidanceProvided() bool {
	return strings.TrimSpace(os.Getenv("ATTN_AGENT_GUIDANCE")) != "" ||
		strings.TrimSpace(os.Getenv("ATTN_CHIEF_GUIDANCE")) != ""
}

func runHookState() {
	sessionID, state, hookEvent := parseHookStateArgs()
	if sessionID == "" || state == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-state [session_id] <state>\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	observePromptConversation(c, sessionID, hookEvent, input)
	if err := c.UpdateStateFromHookEvidence(sessionID, state, input.PermissionMode, hookEvent, input.Prompt); err != nil {
		fmt.Fprintf(os.Stderr, "error updating state: %v\n", err)
		os.Exit(1)
	}
}

func runHookNotification() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-notification [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	if strings.TrimSpace(input.NotificationType) == "" {
		return
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordNotification(sessionID, input.NotificationType, input.Message); err != nil {
		fmt.Fprintf(os.Stderr, "error recording notification: %v\n", err)
		os.Exit(1)
	}
}

func runHookStopFailure() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop-failure [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	errorType := strings.TrimSpace(input.ErrorType)
	if errorType == "" {
		return
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordStopFailure(sessionID, errorType, input.ErrorMessage); err != nil {
		fmt.Fprintf(os.Stderr, "error recording stop failure: %v\n", err)
		os.Exit(1)
	}
}

func runHookCompact() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" || len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-compact <session_id> <start|end>\n")
		os.Exit(1)
	}
	active := strings.TrimSpace(os.Args[3]) == "start"

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordCompaction(sessionID, active, input.Trigger); err != nil {
		fmt.Fprintf(os.Stderr, "error recording compaction: %v\n", err)
		os.Exit(1)
	}
}

func runHookToolUse() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-tool-use [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	// A subagent's completions must not report working: that would retire the approval the user is being asked to answer.
	if strings.TrimSpace(input.AgentID) == "" {
		if err := c.UpdateState(sessionID, protocol.StateWorking); err != nil {
			fmt.Fprintf(os.Stderr, "error updating state: %v\n", err)
			os.Exit(1)
		}
	}

	// A failure here must not fail the hook and stall the agent.
	if edited := hooks.MarkdownEdits(input.ToolName, input.ToolInput, input.CWD); len(edited) > 0 {
		if err := c.RecordFilesEdited(sessionID, edited); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record edited files: %v\n", err)
		}
	}

	// Same: a daemon that is down or gated off must not fail the hook.
	if sent := hooks.SentFiles(input.ToolName, input.ToolInput, input.CWD); len(sent) > 0 {
		if err := c.OpenSentFiles(sessionID, sent); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open sent files: %v\n", err)
		}
	}

	for _, url := range hooks.PullRequestCreated(input.ToolName, input.ToolInput, input.ToolResponse) {
		if err := c.RecordPullRequestCreated(sessionID, url); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record pull request %s: %v\n", url, err)
		}
	}
}

func runHookTodo() {
	sessionID := hookSessionIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-todo [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return
	}
	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	var todoInput todoWriteInput
	if err := json.Unmarshal(input.ToolInput, &todoInput); err != nil {
		return
	}

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

func runProbeTUI() {
	fs := flag.NewFlagSet("_probe-tui", flag.ExitOnError)
	styleFlag := fs.String("style", "", "agent vocabulary to mirror: codex or claude")
	interval := fs.Duration("interval", 500*time.Millisecond, "frame repaint interval")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	style, err := probetui.ParseStyle(*styleFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn _probe-tui: %v\n", err)
		os.Exit(2)
	}

	size := func() (int, int, error) {
		ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
		if err != nil {
			return 0, 0, fmt.Errorf("get terminal size: %w", err)
		}
		return int(ws.Col), int(ws.Row), nil
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	ctx, cancel := context.WithCancel(context.Background())
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-term
		cancel()
	}()
	defer signal.Stop(term)

	if err := probetui.Run(ctx, os.Stdout, style, size, winch, *interval); err != nil {
		fmt.Fprintf(os.Stderr, "attn _probe-tui: %v\n", err)
		os.Exit(1)
	}
}

func hookSessionIDFromArgOrEnv(index int) string {
	if len(os.Args) > index {
		return strings.TrimSpace(os.Args[index])
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func parseHookStateArgs() (sessionID string, state string, hookEvent string) {
	if len(os.Args) < 3 {
		return "", "", ""
	}
	first := strings.TrimSpace(os.Args[2])
	if hookStateValue(first) {
		event := ""
		if len(os.Args) >= 4 {
			event = strings.TrimSpace(os.Args[3])
		}
		return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID")), first, event
	}
	if len(os.Args) < 4 {
		return "", "", ""
	}
	event := ""
	if len(os.Args) >= 5 {
		event = strings.TrimSpace(os.Args[4])
	}
	return first, strings.TrimSpace(os.Args[3]), event
}

func hookStateValue(value string) bool {
	switch value {
	case protocol.StateLaunching, protocol.StateWorking, protocol.StatePendingApproval,
		protocol.StateWaitingInput, protocol.StateIdle, protocol.StateUnknown,
		protocol.StateScheduled, "recoverable":
		return true
	default:
		return false
	}
}

type agentConversationObserver interface {
	ObserveAgentConversation(attnSessionID, agentSessionID, transcriptPath string) error
}

func observePromptConversation(c agentConversationObserver, attnSessionID, hookEvent string, input hookInput) {
	if !strings.EqualFold(strings.TrimSpace(hookEvent), "user_prompt_submit") {
		return
	}
	observeAgentConversation(c, attnSessionID, input.SessionID, input.TranscriptPath)
}

func observeAgentConversation(c agentConversationObserver, attnSessionID, agentSessionID, transcriptPath string) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if agentSessionID == "" || transcriptPath == "" {
		return
	}
	if err := c.ObserveAgentConversation(attnSessionID, agentSessionID, transcriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not observe agent conversation: %v\n", err)
	}
}
