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
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/contextjanitor"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/tour"
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
	if len(os.Args) >= 2 && os.Args[1] == "_workspace-context-janitor-mcp" {
		runWorkspaceContextJanitorMCP(os.Args[2:])
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
	case "review-loop":
		runReviewLoop()
	case "tour":
		maybePrintProfileBanner()
		runTour()
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
	case "dispatch":
		maybePrintProfileBanner()
		runDispatch()
	case "workspace":
		maybePrintProfileBanner()
		runWorkspace()
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

func runWorkspaceContextJanitorMCP(args []string) {
	fs := flag.NewFlagSet("_workspace-context-janitor-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sourcePath := fs.String("source-file", "", "captured context file")
	candidatePath := fs.String("candidate-file", "", "candidate output file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 ||
		strings.TrimSpace(*sourcePath) == "" || strings.TrimSpace(*candidatePath) == "" {
		fmt.Fprintln(os.Stderr, "invalid workspace context janitor MCP arguments")
		os.Exit(2)
	}
	if err := contextjanitor.ServeToolServer(
		context.Background(),
		*sourcePath,
		*candidatePath,
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
// pty-worker, review-loop) that have their own protocols on stderr.
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
	sessions, err := c.Query("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sessions); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding sessions: %v\n", err)
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
  delegate --brief-file <path>      start another agent with a delegated brief
  dispatch <command>                 list or report chief-of-staff dispatches
  workspace context <command>       edit shared workspace context
  open <file.md> [--session <id>]   show a markdown file in attn
  browser <command>                 open and control the in-app browser
  review-loop <command>             manage an autonomous review loop
  tour <command>                    create and run an interactive code tour
  list                              list sessions
  daemon <command>                  manage the daemon
  profile-env <profile|--unset>     print shell commands for selecting a profile
  version                           print version information
`)
}

func runTour() {
	if len(os.Args) < 3 {
		writeTourHelp(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[2] {
	case "create":
		runTourCreate()
	case "start":
		runTourStart()
	case "status":
		runTourStatus()
	case "refresh":
		runTourRefresh()
	case "reply":
		runTourReply()
	case "help", "-h", "--help":
		writeTourHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "tour: unknown command %q\n\n", os.Args[2])
		writeTourHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeTourHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn tour <command>

commands:
  create  create a guide in the active attn profile directory
  start   open a guide and listen for questions and feedback until End tour
  status  show the current session's active tour
  refresh reload the guide and current working-tree changes
  reply   answer a question from the tour
`)
}

type tourReadyPayload struct {
	TourID           string                       `json:"tour_id"`
	SessionID        string                       `json:"session_id"`
	Name             string                       `json:"name"`
	Status           protocol.TourStatus          `json:"status"`
	ConnectionState  protocol.TourConnectionState `json:"connection_state"`
	BaseRef          string                       `json:"base_ref"`
	GuidePath        string                       `json:"guide_path"`
	ListenerEventSeq int                          `json:"listener_event_seq"`
}

func newTourReadyPayload(run *protocol.TourRun) tourReadyPayload {
	return tourReadyPayload{
		TourID:           run.TourID,
		SessionID:        run.SessionID,
		Name:             run.Name,
		Status:           run.Status,
		ConnectionState:  run.ConnectionState,
		BaseRef:          run.BaseRef,
		GuidePath:        run.GuidePath,
		ListenerEventSeq: run.ListenerEventSeq,
	}
}

func runTourCreate() {
	fs := flag.NewFlagSet("tour create", flag.ExitOnError)
	name := fs.String("name", "tour", "tour name")
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	repoPath := fs.String("repo", "", "repository path (defaults to current directory)")
	_ = fs.Parse(os.Args[3:])
	session := tourSessionID(*sessionID)
	if session == "" {
		fmt.Fprintln(os.Stderr, "tour create: no session; run inside attn or pass --session")
		os.Exit(2)
	}
	repo := strings.TrimSpace(*repoPath)
	if repo == "" {
		var err error
		repo, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tour create: %v\n", err)
			os.Exit(1)
		}
	}
	path, err := tour.CreateGuidePath(repo, session, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour create: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(path)
}

func runTourStart() {
	fs := flag.NewFlagSet("tour start", flag.ExitOnError)
	guidePath := fs.String("guide", "", "system tour guide path")
	name := fs.String("name", "", "tour name")
	baseRef := fs.String("base", "", "base branch or ref")
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	_ = fs.Parse(os.Args[3:])
	session := tourSessionID(*sessionID)
	if session == "" || strings.TrimSpace(*guidePath) == "" {
		fmt.Fprintln(os.Stderr, "tour start: --guide and a session are required")
		os.Exit(2)
	}
	absoluteGuide, err := filepath.Abs(*guidePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour start: resolve guide: %v\n", err)
		os.Exit(1)
	}
	c := client.New("")
	run, err := c.OpenTour(session, absoluteGuide, *name, *baseRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("TOUR_READY %s\n", mustJSON(newTourReadyPayload(run)))
	afterSeq := run.ListenerEventSeq
	for run.Status == protocol.TourStatusActive {
		event, nextRun, err := c.WaitTourEvent(run.TourID, afterSeq)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tour listener: %v\n", err)
			os.Exit(1)
		}
		if nextRun != nil {
			run = nextRun
		}
		if event == nil {
			continue
		}
		afterSeq = event.Seq
		switch event.Kind {
		case "question":
			fmt.Printf("QUESTION_READY %s\n", mustJSON(event))
		case "feedback":
			fmt.Printf("FEEDBACK_READY %s\n", mustJSON(event))
		case "finish":
			fmt.Printf("TOUR_ENDED %s\n", mustJSON(event))
		default:
			fmt.Printf("TOUR_EVENT %s\n", mustJSON(event))
		}
	}
}

func runTourStatus() {
	fs := flag.NewFlagSet("tour status", flag.ExitOnError)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	_ = fs.Parse(os.Args[3:])
	session := tourSessionID(*sessionID)
	if session == "" {
		fmt.Fprintln(os.Stderr, "tour status: no session; run inside attn or pass --session")
		os.Exit(2)
	}
	run, err := client.New("").GetTourState(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour status: %v\n", err)
		os.Exit(1)
	}
	printJSON(run)
}

func runTourRefresh() {
	fs := flag.NewFlagSet("tour refresh", flag.ExitOnError)
	tourID := fs.String("tour", "", "tour id")
	_ = fs.Parse(os.Args[3:])
	if strings.TrimSpace(*tourID) == "" {
		fmt.Fprintln(os.Stderr, "tour refresh: --tour is required")
		os.Exit(2)
	}
	run, err := client.New("").RefreshTour(*tourID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour refresh: %v\n", err)
		os.Exit(1)
	}
	printJSON(run)
}

func runTourReply() {
	fs := flag.NewFlagSet("tour reply", flag.ExitOnError)
	tourID := fs.String("tour", "", "tour id")
	eventID := fs.String("event", "", "question event id")
	body := fs.String("body", "", "answer text")
	bodyFile := fs.String("body-file", "", "file containing the answer")
	_ = fs.Parse(os.Args[3:])
	answer := strings.TrimSpace(*body)
	if strings.TrimSpace(*bodyFile) != "" {
		if answer != "" {
			fmt.Fprintln(os.Stderr, "tour reply: pass only one of --body or --body-file")
			os.Exit(2)
		}
		content, err := os.ReadFile(*bodyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tour reply: %v\n", err)
			os.Exit(1)
		}
		answer = strings.TrimSpace(string(content))
	}
	if strings.TrimSpace(*tourID) == "" || strings.TrimSpace(*eventID) == "" || answer == "" {
		fmt.Fprintln(os.Stderr, "tour reply: --tour, --event, and --body or --body-file are required")
		os.Exit(2)
	}
	run, err := client.New("").ReplyTour(*tourID, *eventID, answer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tour reply: %v\n", err)
		os.Exit(1)
	}
	printJSON(run)
}

func tourSessionID(value string) string {
	if session := strings.TrimSpace(value); session != "" {
		return session
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func mustJSON(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
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
  --worktree <branch>        create a worktree and workspace

worktree options:
  --repo <path>              main repository (defaults to the source repository)
  --from <ref>               branch or ref to start from
  --worktree-path <path>     override the generated sibling path

session options:
  --agent <name>             configured prompt-capable built-in or plugin agent
  --label <text>             session label
  --source-session <id>      source session (defaults to ATTN_SESSION_ID)
  --yolo                     bypass agent approval prompts
`)
}

func runDispatch() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeDispatchHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	switch os.Args[2] {
	case "list":
		sourceSessionID, err := parseDispatchSourceSession(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch list: %v\n", err)
			os.Exit(2)
		}
		dispatches, err := client.New("").ListDispatches(sourceSessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch list: %v\n", err)
			os.Exit(1)
		}
		printJSON(dispatches)
	case "report":
		sourceSessionID, report, structuredReport, err := parseDispatchReportArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch report: %v\n", err)
			os.Exit(2)
		}
		dispatch, err := client.New("").ReportDispatchEnvelope(sourceSessionID, report, structuredReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch report: %v\n", err)
			os.Exit(1)
		}
		printJSON(dispatch)
	case "status":
		sourceSessionID, err := parseDispatchSourceSession(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch status: %v\n", err)
			os.Exit(2)
		}
		dispatch, err := client.New("").GetDispatch(sourceSessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch status: %v\n", err)
			os.Exit(1)
		}
		printJSON(dispatch)
	case "resolve":
		sourceSessionID, dispatchID, response, resolutionLink, err := parseDispatchResolveArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch resolve: %v\n", err)
			os.Exit(2)
		}
		dispatch, err := client.New("").ResolveDispatchRequest(
			sourceSessionID,
			dispatchID,
			response,
			resolutionLink,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch resolve: %v\n", err)
			os.Exit(1)
		}
		printJSON(dispatch)
	case "message":
		sourceSessionID, dispatchID, content, err := parseDispatchMessageArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch message: %v\n", err)
			os.Exit(2)
		}
		message, err := client.New("").SendDispatchMessage(sourceSessionID, dispatchID, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch message: %v\n", err)
			os.Exit(1)
		}
		printJSON(message)
	case "inbox":
		sourceSessionID, unreadOnly, err := parseDispatchInboxArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch inbox: %v\n", err)
			os.Exit(2)
		}
		messages, err := client.New("").ListDispatchMessages(sourceSessionID, "", unreadOnly)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch inbox: %v\n", err)
			os.Exit(1)
		}
		printJSON(messages)
	case "messages":
		sourceSessionID, dispatchID, err := parseDispatchMessagesArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch messages: %v\n", err)
			os.Exit(2)
		}
		messages, err := client.New("").ListDispatchMessages(sourceSessionID, dispatchID, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch messages: %v\n", err)
			os.Exit(1)
		}
		printJSON(messages)
	case "read":
		sourceSessionID, messageID, err := parseDispatchMessageIDArgs("dispatch read", os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch read: %v\n", err)
			os.Exit(2)
		}
		message, err := client.New("").ReadDispatchMessage(sourceSessionID, messageID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch read: %v\n", err)
			os.Exit(1)
		}
		printJSON(message)
	case "ack":
		sourceSessionID, messageID, acknowledgement, err := parseDispatchAckArgs(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch ack: %v\n", err)
			os.Exit(2)
		}
		message, err := client.New("").AcknowledgeDispatchMessage(sourceSessionID, messageID, acknowledgement)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch ack: %v\n", err)
			os.Exit(1)
		}
		printJSON(message)
	default:
		fmt.Fprintf(os.Stderr, "dispatch: unknown command %q\n\n", os.Args[2])
		writeDispatchHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeDispatchHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn dispatch <command>

commands:
  list [--session <id>]                          list work dispatched by this chief
  report (--message <text> | --file <path>)     report progress from a dispatched agent
         [--coordination-file <json>]
  status [--session <id>]                        show this delegated session's report and response
  resolve --dispatch <id>                        answer the active decision request
          (--response <text> | --file <path>) [--link <url>] [--session <id>]
	  message --dispatch <id>                        send durable mail to a delegated agent
	          (--message <text> | --file <path>) [--session <id>]
	  messages --dispatch <id> [--session <id>]      list sent mail and acknowledgement state
	  inbox [--unread] [--session <id>]              list this delegated agent's mail
  read --message-id <id> [--session <id>]        mark one message read
  ack --message-id <id>                          acknowledge one message
      [--message <text> | --file <path>] [--session <id>]

The session defaults to ATTN_SESSION_ID.
`)
}

func parseDispatchSourceSession(args []string) (string, error) {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return "", errors.New("no session; run inside attn or pass --session")
	}
	return source, nil
}

func parseDispatchReportArgs(args []string) (string, string, *protocol.DispatchReport, error) {
	fs := flag.NewFlagSet("dispatch report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	message := fs.String("message", "", "concise progress or completion update")
	reportFile := fs.String("file", "", "file containing a progress or completion update")
	coordinationFile := fs.String("coordination-file", "", "JSON file containing structured coordination fields")
	if err := fs.Parse(args); err != nil {
		return "", "", nil, err
	}
	if fs.NArg() != 0 {
		return "", "", nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return "", "", nil, errors.New("no session; run inside attn or pass --session")
	}
	if strings.TrimSpace(*message) != "" && strings.TrimSpace(*reportFile) != "" {
		return "", "", nil, errors.New("pass only one of --message or --file")
	}
	report := strings.TrimSpace(*message)
	if path := strings.TrimSpace(*reportFile); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", "", nil, fmt.Errorf("read report file: %w", err)
		}
		report = strings.TrimSpace(string(content))
	}
	if report == "" {
		return "", "", nil, errors.New("--message or --file is required")
	}
	var structuredReport *protocol.DispatchReport
	if path := strings.TrimSpace(*coordinationFile); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", "", nil, fmt.Errorf("read coordination file: %w", err)
		}
		var parsed protocol.DispatchReport
		if err := json.Unmarshal(content, &parsed); err != nil {
			return "", "", nil, fmt.Errorf("parse coordination file: %w", err)
		}
		structuredReport = &parsed
	}
	return source, report, structuredReport, nil
}

func parseDispatchResolveArgs(args []string) (string, string, string, string, error) {
	fs := flag.NewFlagSet("dispatch resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "chief session id (defaults to ATTN_SESSION_ID)")
	dispatchID := fs.String("dispatch", "", "dispatch id")
	response := fs.String("response", "", "decision response")
	responseFile := fs.String("file", "", "file containing the decision response")
	resolutionLink := fs.String("link", "", "optional external decision URL")
	if err := fs.Parse(args); err != nil {
		return "", "", "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", "", "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if source == "" {
		return "", "", "", "", errors.New("no session; run inside attn or pass --session")
	}
	if strings.TrimSpace(*dispatchID) == "" {
		return "", "", "", "", errors.New("--dispatch is required")
	}
	if strings.TrimSpace(*response) != "" && strings.TrimSpace(*responseFile) != "" {
		return "", "", "", "", errors.New("pass only one of --response or --file")
	}
	resolvedResponse := strings.TrimSpace(*response)
	if path := strings.TrimSpace(*responseFile); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", "", "", "", fmt.Errorf("read response file: %w", err)
		}
		resolvedResponse = strings.TrimSpace(string(content))
	}
	if resolvedResponse == "" {
		return "", "", "", "", errors.New("--response or --file is required")
	}
	return source, strings.TrimSpace(*dispatchID), resolvedResponse, strings.TrimSpace(*resolutionLink), nil
}

func parseDispatchMessageArgs(args []string) (string, string, string, error) {
	fs := flag.NewFlagSet("dispatch message", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "chief session id (defaults to ATTN_SESSION_ID)")
	dispatchID := fs.String("dispatch", "", "dispatch id")
	message := fs.String("message", "", "message content")
	messageFile := fs.String("file", "", "file containing the message")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source, err := resolveDispatchSession(*sessionID)
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(*dispatchID) == "" {
		return "", "", "", errors.New("--dispatch is required")
	}
	content, err := readOptionalFlagContent(*message, *messageFile, true)
	if err != nil {
		return "", "", "", err
	}
	return source, strings.TrimSpace(*dispatchID), content, nil
}

func parseDispatchInboxArgs(args []string) (string, bool, error) {
	fs := flag.NewFlagSet("dispatch inbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	unreadOnly := fs.Bool("unread", false, "show only unread messages")
	if err := fs.Parse(args); err != nil {
		return "", false, err
	}
	if fs.NArg() != 0 {
		return "", false, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source, err := resolveDispatchSession(*sessionID)
	return source, *unreadOnly, err
}

func parseDispatchMessagesArgs(args []string) (string, string, error) {
	fs := flag.NewFlagSet("dispatch messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "chief session id (defaults to ATTN_SESSION_ID)")
	dispatchID := fs.String("dispatch", "", "dispatch id")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source, err := resolveDispatchSession(*sessionID)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(*dispatchID) == "" {
		return "", "", errors.New("--dispatch is required")
	}
	return source, strings.TrimSpace(*dispatchID), nil
}

func parseDispatchMessageIDArgs(name string, args []string) (string, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	messageID := fs.String("message-id", "", "dispatch message id")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source, err := resolveDispatchSession(*sessionID)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(*messageID) == "" {
		return "", "", errors.New("--message-id is required")
	}
	return source, strings.TrimSpace(*messageID), nil
}

func parseDispatchAckArgs(args []string) (string, string, string, error) {
	fs := flag.NewFlagSet("dispatch ack", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	messageID := fs.String("message-id", "", "dispatch message id")
	message := fs.String("message", "", "optional acknowledgement")
	messageFile := fs.String("file", "", "file containing the acknowledgement")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	source, err := resolveDispatchSession(*sessionID)
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(*messageID) == "" {
		return "", "", "", errors.New("--message-id is required")
	}
	acknowledgement, err := readOptionalFlagContent(*message, *messageFile, false)
	if err != nil {
		return "", "", "", err
	}
	return source, strings.TrimSpace(*messageID), acknowledgement, nil
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
  compact [--session <id>]         compact now with the configured janitor
  rollback [--session <id>]        restore the latest pre-compaction snapshot
`)
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
	label := fs.String("label", "", "target session label")
	sourceSessionID := fs.String("source-session", "", "source session id (defaults to ATTN_SESSION_ID)")
	yolo := fs.Bool("yolo", false, "launch the target agent in yolo mode")
	newWorkspace := fs.Bool("new-workspace", false, "create a new workspace for the delegated agent")
	workspaceID := fs.String("workspace", "", "place the delegated agent in an existing workspace")
	cwd := fs.String("cwd", "", "use an existing directory in a new workspace")
	worktreeBranch := fs.String("worktree", "", "create a worktree with this branch in a new workspace")
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
	if explicitWorkspace != "" && (*newWorkspace || customCWD != "" || branch != "") {
		return delegateCLIArgs{}, errors.New("--workspace cannot be combined with --new-workspace, --cwd, or --worktree")
	}
	if customCWD != "" && branch != "" {
		return delegateCLIArgs{}, errors.New("--cwd cannot be combined with --worktree")
	}
	if branch == "" && (repo != "" || startingFrom != "" || customWorktreePath != "") {
		return delegateCLIArgs{}, errors.New("--repo, --from, and --worktree-path require --worktree")
	}

	placement := "current_workspace"
	if explicitWorkspace != "" {
		placement = "existing_workspace"
	} else if *newWorkspace || customCWD != "" || branch != "" {
		placement = "new_workspace"
	}

	return delegateCLIArgs{
		sourceSessionID: source,
		brief:           brief,
		options: client.DelegateOptions{
			Agent:        strings.TrimSpace(*agentName),
			Label:        strings.TrimSpace(*label),
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

func runReviewLoop() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: attn review-loop <start|stop|status|show|set-iterations|answer> ...")
		os.Exit(1)
	}
	warnIfDaemonVersionMismatch()

	c := client.New("")
	switch os.Args[2] {
	case "start":
		fs := flag.NewFlagSet("review-loop start", flag.ExitOnError)
		sessionID := fs.String("session", "", "session id")
		presetID := fs.String("preset", "", "preset id")
		prompt := fs.String("prompt", "", "prompt text")
		iterations := fs.Int("iterations", 1, "iteration limit")
		handoffFile := fs.String("handoff-file", "", "path to structured handoff JSON")
		_ = fs.Parse(os.Args[3:])
		resolvedSessionID := strings.TrimSpace(*sessionID)
		if resolvedSessionID == "" {
			resolvedSessionID = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
		}
		if resolvedSessionID == "" {
			fmt.Fprintln(os.Stderr, "review-loop start: --session is required")
			os.Exit(1)
		}
		if strings.TrimSpace(*prompt) == "" {
			fmt.Fprintln(os.Stderr, "review-loop start: --prompt is required")
			os.Exit(1)
		}
		var handoffPayloadJSON *string
		if trimmed := strings.TrimSpace(*handoffFile); trimmed != "" {
			data, err := os.ReadFile(trimmed)
			if err != nil {
				fmt.Fprintf(os.Stderr, "review-loop start: read handoff file: %v\n", err)
				os.Exit(1)
			}
			text := strings.TrimSpace(string(data))
			handoffPayloadJSON = &text
		}
		state, err := c.StartReviewLoopWithHandoff(resolvedSessionID, *presetID, *prompt, *iterations, handoffPayloadJSON)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop start error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	case "stop":
		fs := flag.NewFlagSet("review-loop stop", flag.ExitOnError)
		sessionID := fs.String("session", "", "session id")
		_ = fs.Parse(os.Args[3:])
		if strings.TrimSpace(*sessionID) == "" {
			fmt.Fprintln(os.Stderr, "review-loop stop: --session is required")
			os.Exit(1)
		}
		state, err := c.StopReviewLoop(*sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop stop error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	case "status":
		fs := flag.NewFlagSet("review-loop status", flag.ExitOnError)
		sessionID := fs.String("session", "", "session id")
		_ = fs.Parse(os.Args[3:])
		if strings.TrimSpace(*sessionID) == "" {
			fmt.Fprintln(os.Stderr, "review-loop status: --session is required")
			os.Exit(1)
		}
		state, err := c.GetReviewLoopState(*sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop status error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	case "set-iterations":
		fs := flag.NewFlagSet("review-loop set-iterations", flag.ExitOnError)
		sessionID := fs.String("session", "", "session id")
		iterations := fs.Int("iterations", 0, "iteration limit")
		_ = fs.Parse(os.Args[3:])
		if strings.TrimSpace(*sessionID) == "" || *iterations <= 0 {
			fmt.Fprintln(os.Stderr, "review-loop set-iterations: --session and positive --iterations are required")
			os.Exit(1)
		}
		state, err := c.SetReviewLoopIterationLimit(*sessionID, *iterations)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop set-iterations error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	case "show":
		fs := flag.NewFlagSet("review-loop show", flag.ExitOnError)
		loopID := fs.String("loop", "", "loop id")
		sessionID := fs.String("session", "", "session id")
		_ = fs.Parse(os.Args[3:])
		if strings.TrimSpace(*loopID) == "" && strings.TrimSpace(*sessionID) == "" {
			fmt.Fprintln(os.Stderr, "review-loop show: --loop or --session is required")
			os.Exit(1)
		}
		if strings.TrimSpace(*loopID) != "" {
			state, err := c.GetReviewLoopRun(strings.TrimSpace(*loopID))
			if err != nil {
				fmt.Fprintf(os.Stderr, "review-loop show error: %v\n", err)
				os.Exit(1)
			}
			printJSON(state)
			return
		}
		state, err := c.GetReviewLoopState(strings.TrimSpace(*sessionID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop show error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	case "answer":
		fs := flag.NewFlagSet("review-loop answer", flag.ExitOnError)
		loopID := fs.String("loop", "", "loop id")
		interactionID := fs.String("interaction", "", "interaction id")
		answer := fs.String("answer", "", "user answer")
		_ = fs.Parse(os.Args[3:])
		if strings.TrimSpace(*loopID) == "" || strings.TrimSpace(*answer) == "" {
			fmt.Fprintln(os.Stderr, "review-loop answer: --loop and --answer are required")
			os.Exit(1)
		}
		state, err := c.AnswerReviewLoop(*loopID, *interactionID, *answer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-loop answer error: %v\n", err)
			os.Exit(1)
		}
		printJSON(state)

	default:
		fmt.Fprintf(os.Stderr, "unknown review-loop command: %s\n", os.Args[2])
		os.Exit(1)
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
		os.Exit(1)
	}
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
		contextPath, checkoutErr := workspaceContextCheckoutPath(c, sessionID, 40, 25*time.Millisecond)
		if checkoutErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not prepare workspace context guidance: %v\n", checkoutErr)
		} else {
			opts.WorkspaceContextPath = contextPath
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

	// Send stop event to daemon for classification
	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	syncSessionResumeID(c, sessionID, input.SessionID)
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

func workspaceContextGuidanceProvidedAtLaunch() bool {
	return strings.TrimSpace(os.Getenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE")) != ""
}

type workspaceContextCheckoutClient interface {
	CheckoutWorkspaceContext(sourceSessionID string, force bool) (*protocol.WorkspaceContextResult, error)
}

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
