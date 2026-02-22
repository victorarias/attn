package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/wrapper"
)

// hookInput represents the JSON input from Claude Code hooks
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

func main() {
	if len(os.Args) < 2 {
		runWrapper()
		return
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "pty-worker":
		runPTYWorker()
	case "list":
		runList()
	case "_hook-stop":
		runHookStop()
	case "_hook-state":
		runHookState()
	case "_hook-todo":
		runHookTodo()
	default:
		// Check if it's a flag (starts with -)
		if len(os.Args[1]) > 0 && os.Args[1][0] == '-' {
			runWrapper()
		} else {
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			os.Exit(1)
		}
	}
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
	fs.BoolVar(&cfg.ForkSession, "fork-session", false, "fork session")
	fs.StringVar(&cfg.Executable, "executable", "", "selected agent executable override")
	fs.StringVar(&cfg.ClaudeExecutable, "claude-executable", "", "claude executable override")
	fs.StringVar(&cfg.CodexExecutable, "codex-executable", "", "codex executable override")
	fs.StringVar(&cfg.CopilotExecutable, "copilot-executable", "", "copilot executable override")
	fs.StringVar(&cfg.PiExecutable, "pi-executable", "", "pi executable override")
	fs.StringVar(&cfg.RegistryPath, "registry-path", "", "registry path")
	fs.StringVar(&cfg.SocketPath, "socket-path", "", "socket path")
	fs.StringVar(&cfg.ControlToken, "control-token", "", "control token")
	fs.IntVar(&cfg.OwnerPID, "owner-pid", 0, "daemon owner pid")
	fs.StringVar(&cfg.OwnerStartedAt, "owner-started-at", "", "daemon owner started-at timestamp")
	fs.StringVar(&cfg.OwnerNonce, "owner-nonce", "", "daemon owner nonce")

	_ = fs.Parse(os.Args[2:])
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
	cfg.Logf = func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[pty-worker] "+format+"\n", args...)
	}

	if err := ptyworker.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-worker error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon() {
	d := daemon.New(config.SocketPath())
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runList() {
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
	label        string
	resumeID     string
	resumePicker bool
	forkSession  bool
	agentArgs    []string
}

func parseDirectLaunchArgs(args []string) directLaunchArgs {
	fs := flag.NewFlagSet("attn", flag.ContinueOnError)
	labelFlag := fs.String("s", "", "session label")
	resumeFlag := fs.String("resume", "", "session ID to resume from")
	forkFlag := fs.Bool("fork-session", false, "fork the resumed session")
	resumePicker := false

	var attnArgs []string
	var agentArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s":
			if i+1 < len(args) {
				attnArgs = append(attnArgs, arg, args[i+1])
				i++
			}
		case "--resume":
			if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
				attnArgs = append(attnArgs, arg, args[i+1])
				i++
			} else {
				resumePicker = true
			}
		case "--fork-session":
			attnArgs = append(attnArgs, arg)
		case "--":
			agentArgs = append(agentArgs, args[i+1:]...)
			i = len(args)
		default:
			agentArgs = append(agentArgs, arg)
		}
	}
	_ = fs.Parse(attnArgs)

	label := *labelFlag
	if label == "" {
		label = wrapper.DefaultLabel()
	}
	return directLaunchArgs{
		label:        label,
		resumeID:     *resumeFlag,
		resumePicker: resumePicker,
		forkSession:  *forkFlag,
		agentArgs:    agentArgs,
	}
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
	pathutil.EnsureGUIPath()

	driver := agentdriver.Get(requestedAgent)
	if driver == nil {
		fmt.Fprintf(os.Stderr, "warning: unknown ATTN_AGENT %q, defaulting to codex\n", requestedAgent)
		driver = agentdriver.MustGet("codex")
	}
	caps := agentdriver.EffectiveCapabilities(driver)

	parsed := parseDirectLaunchArgs(os.Args[1:])
	if !caps.HasResume && (parsed.resumeID != "" || parsed.resumePicker) {
		fmt.Fprintf(os.Stderr, "warning: %s resume not supported yet (ignoring --resume)\n", driver.Name())
		parsed.resumeID = ""
		parsed.resumePicker = false
	}
	if parsed.forkSession && !caps.HasFork {
		fmt.Fprintf(os.Stderr, "warning: %s fork not supported yet (ignoring --fork-session)\n", driver.Name())
		parsed.forkSession = false
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
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
		ResumeSessionID: parsed.resumeID,
		ResumePicker:    parsed.resumePicker,
		ForkSession:     parsed.forkSession,
		Executable:      driver.ResolveExecutable(""),
		SocketPath:      config.SocketPath(),
		WrapperPath:     resolveWrapperPath(),
		AgentArgs:       append([]string(nil), parsed.agentArgs...),
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

	// Build deep link URL
	deepLink := fmt.Sprintf("attn://spawn?cwd=%s&label=%s",
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
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop <session_id>\n")
		os.Exit(1)
	}
	sessionID := os.Args[2]

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

func runHookState() {
	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-state <session_id> <state>\n")
		os.Exit(1)
	}
	sessionID := os.Args[2]
	state := os.Args[3]

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
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-todo <session_id>\n")
		os.Exit(1)
	}
	sessionID := os.Args[2]

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

func syncSessionResumeID(c *client.Client, attnSessionID, claudeSessionID string) {
	claudeSessionID = strings.TrimSpace(claudeSessionID)
	if claudeSessionID == "" {
		return
	}
	if err := c.SetSessionResumeID(attnSessionID, claudeSessionID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not sync resume session id: %v\n", err)
	}
}
