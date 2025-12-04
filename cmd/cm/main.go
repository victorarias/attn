package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/daemon"
	"github.com/victorarias/claude-manager/internal/dashboard"
	"github.com/victorarias/claude-manager/internal/status"
	"github.com/victorarias/claude-manager/internal/wrapper"
)

// Log levels
const (
	LogError = iota
	LogWarn
	LogInfo
	LogDebug
	LogTrace
)

var logLevel = LogError

func init() {
	// Set log level from environment
	switch os.Getenv("CM_DEBUG") {
	case "trace":
		logLevel = LogTrace
	case "debug":
		logLevel = LogDebug
	case "info":
		logLevel = LogInfo
	case "warn":
		logLevel = LogWarn
	case "1", "true":
		logLevel = LogDebug
	}
}

func logTrace(format string, args ...interface{}) {
	if logLevel >= LogTrace {
		fmt.Fprintf(os.Stderr, "[TRACE] "+format+"\n", args...)
	}
}

func logDebug(format string, args ...interface{}) {
	if logLevel >= LogDebug {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}

func logInfo(format string, args ...interface{}) {
	if logLevel >= LogInfo {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
	}
}

func logWarn(format string, args ...interface{}) {
	if logLevel >= LogWarn {
		fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
	}
}

func logError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
}

// parseArgs extracts cm-specific flags and returns remaining args for claude.
// This allows unknown flags (like -c, -r) to pass through to claude.
func parseArgs(args []string) (label string, yolo bool, dashboard bool, remaining []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s":
			if i+1 < len(args) {
				label = args[i+1]
				i++ // skip the value
			}
		case "-y":
			yolo = true
		case "-d":
			dashboard = true
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		default:
			remaining = append(remaining, args[i])
		}
	}
	return
}

func main() {
	logDebug("cm starting, args: %v", os.Args)
	logTrace("environment CM_DEBUG=%s", os.Getenv("CM_DEBUG"))

	// Parse cm-specific flags manually to allow unknown flags to pass through to claude
	label, yolo, dashboardFlag, args := parseArgs(os.Args[1:])
	logDebug("parsed flags: label=%q, yolo=%v, dashboard=%v", label, yolo, dashboardFlag)
	logDebug("remaining args: %v", args)

	// Handle -d flag
	if dashboardFlag {
		runDashboard()
		return
	}

	// Check for subcommands
	if len(args) > 0 {
		switch args[0] {
		case "dashboard":
			runDashboard()
			return
		case "daemon":
			runDaemon()
			return
		case "restart":
			runRestart()
			return
		case "status":
			runStatus()
			return
		case "list":
			runList()
			return
		case "kill":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: cm kill <id>")
				os.Exit(1)
			}
			runKill(args[1])
			return
		case "_hook-todo":
			if len(args) < 2 {
				os.Exit(1)
			}
			runHookTodo(args[1])
			return
		case "help":
			printHelp()
			return
		}
	}

	// Default: run wrapper with parsed flags and remaining args
	runWrapperWithFlags(label, yolo, args)
}

func printHelp() {
	fmt.Println(`cm - Claude Manager

Usage:
  cm                    Start Claude with tracking (label = directory name)
  cm -s <label>         Start Claude with explicit label
  cm -y                 Yolo mode (skip permissions)
  cm -s <label> -y      Combine flags (order doesn't matter)
  cm -d                 Open dashboard
  cm dashboard          Open dashboard (alias)
  cm daemon             Run daemon in foreground
  cm restart            Restart the daemon
  cm status             Output for tmux status bar
  cm list               List all sessions (JSON)
  cm kill <id>          Unregister a session

Claude flags (-c, -r, etc.) are passed through automatically.

Environment:
  CM_DEBUG=debug    Enable debug logging
  CM_DEBUG=trace    Enable trace logging (verbose)`)
}

func runWrapperWithFlags(label string, yolo bool, claudeArgs []string) {
	logInfo("=== runWrapper starting ===")
	logDebug("label=%q, yolo=%v, claudeArgs=%v", label, yolo, claudeArgs)

	if label == "" {
		label = wrapper.DefaultLabel()
		logDebug("using default label: %s", label)
	}

	sessionID := wrapper.GenerateSessionID()
	logDebug("generated sessionID: %s", sessionID)

	socketPath := client.DefaultSocketPath()
	logDebug("socketPath: %s", socketPath)

	// Ensure daemon is running
	logDebug("checking if daemon is running...")
	c := client.New(socketPath)
	if !c.IsRunning() {
		logInfo("daemon not running, starting in background...")
		startDaemonBackground()
		logDebug("daemon started")
	} else {
		logDebug("daemon already running")
	}

	// Get tmux target
	logTrace("getting tmux target...")
	tmuxTarget := getTmuxTarget()
	logDebug("tmuxTarget: %s", tmuxTarget)

	// Get current directory
	dir, _ := os.Getwd()
	logDebug("working directory: %s", dir)

	// Register with daemon
	logInfo("registering session with daemon...")
	if err := c.Register(sessionID, label, dir, tmuxTarget); err != nil {
		logWarn("could not register with daemon: %v", err)
	} else {
		logDebug("registered successfully")
	}

	// Write hooks config
	logDebug("writing hooks config...")
	tmpDir := os.TempDir()
	logTrace("tmpDir: %s", tmpDir)

	configPath, err := wrapper.WriteHooksConfig(tmpDir, sessionID, socketPath)
	if err != nil {
		logWarn("could not write hooks config: %v", err)
	} else {
		logDebug("hooks config written to: %s", configPath)
		defer wrapper.CleanupHooksConfig(configPath)
	}

	// Log config contents at trace level
	if logLevel >= LogTrace {
		if content, err := os.ReadFile(configPath); err == nil {
			logTrace("hooks config content:\n%s", string(content))
		}
	}

	// Set up cleanup on exit
	cleanup := func() {
		logDebug("cleanup: unregistering session...")
		c.Unregister(sessionID)
		logDebug("cleanup: removing hooks config...")
		wrapper.CleanupHooksConfig(configPath)
		logDebug("cleanup: done")
	}

	// Handle signals
	logTrace("setting up signal handlers...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logInfo("received signal: %v", sig)
		cleanup()
		os.Exit(0)
	}()

	// Build claude command args
	args := []string{"--settings", configPath}

	// Add yolo flag if requested
	if yolo {
		args = append(args, "--dangerously-skip-permissions")
	}

	// Append any additional claude args
	args = append(args, claudeArgs...)

	logInfo("final claude args: %v", args)

	// Find claude executable
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		logError("claude not found in PATH: %v", err)
		os.Exit(1)
	}
	logDebug("claude executable: %s", claudePath)

	logInfo("executing: claude %s", strings.Join(args, " "))

	cmd := exec.Command(claudePath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logDebug("cmd.Run() starting...")
	err = cmd.Run()
	logDebug("cmd.Run() returned, err: %v", err)

	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			logDebug("claude exit code: %d", exitErr.ExitCode())
			os.Exit(exitErr.ExitCode())
		}
		logError("claude execution error: %v", err)
		os.Exit(1)
	}

	logInfo("=== runWrapper completed successfully ===")
}

func getTmuxTarget() string {
	if os.Getenv("TMUX") == "" {
		logTrace("not in tmux session")
		return ""
	}
	cmd := exec.Command("tmux", "display", "-p", "#{session_name}:#{window_index}.#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		logTrace("tmux display failed: %v", err)
		return ""
	}
	result := strings.TrimSpace(string(output))
	logTrace("tmux target: %s", result)
	return result
}

func startDaemonBackground() {
	logDebug("starting daemon in background...")
	cmd := exec.Command(os.Args[0], "daemon")
	err := cmd.Start()
	if err != nil {
		logWarn("failed to start daemon: %v", err)
		return
	}
	logDebug("daemon process started, pid: %d", cmd.Process.Pid)

	// Poll socket to wait for daemon to be ready
	socketPath := client.DefaultSocketPath()
	c := client.New(socketPath)
	logTrace("polling for daemon readiness...")
	for i := 0; i < 50; i++ {
		if c.IsRunning() {
			logDebug("daemon ready after %d ms", i*10)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	logWarn("daemon did not become ready after 500ms")
}

func runDaemon() {
	socketPath := client.DefaultSocketPath()
	d := daemon.New(socketPath)

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		d.Stop()
		os.Exit(0)
	}()

	fmt.Printf("Daemon listening on %s\n", socketPath)
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runRestart() {
	socketPath := client.DefaultSocketPath()

	// Remove existing socket to kill daemon
	os.Remove(socketPath)
	fmt.Println("Stopped existing daemon")

	// Start new daemon in background
	startDaemonBackground()
	fmt.Println("Started new daemon")
}

func runDashboard() {
	c := client.New("")
	m := dashboard.NewModel(c)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}
}

func runStatus() {
	c := client.New("")
	sessions, err := c.Query("")
	if err != nil {
		// Silent failure for status bar
		return
	}
	prs, _ := c.QueryPRs("")   // Ignore error, PRs are optional
	repos, _ := c.QueryRepos() // Ignore error, repos are optional
	output := status.FormatWithPRsAndRepos(sessions, prs, repos)
	if output != "" {
		fmt.Print(output)
	}
}

func runList() {
	c := client.New("")
	sessions, err := c.Query("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(sessions, "", "  ")
	fmt.Println(string(data))
}

func runKill(id string) {
	c := client.New("")
	if err := c.Unregister(id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Session unregistered")
}

// HookInput represents the JSON structure Claude sends to PostToolUse hooks
type HookInput struct {
	ToolInput struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	} `json:"tool_input"`
}

func runHookTodo(sessionID string) {
	logDebug("runHookTodo called for session: %s", sessionID)

	// Read JSON from stdin (Claude sends PostToolUse data here)
	var input HookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		logWarn("failed to parse hook input: %v", err)
		return
	}

	logDebug("parsed %d todos from hook input", len(input.ToolInput.Todos))

	// Convert to string format with status indicators
	var todos []string
	for _, todo := range input.ToolInput.Todos {
		var prefix string
		switch todo.Status {
		case "completed":
			prefix = "[✓]"
		case "in_progress":
			prefix = "[→]"
		default:
			prefix = "[ ]"
		}
		todos = append(todos, fmt.Sprintf("%s %s", prefix, todo.Content))
		logTrace("todo: %s %s (%s)", prefix, todo.Content, todo.Status)
	}

	// Send to daemon
	c := client.New("")
	if err := c.UpdateTodos(sessionID, todos); err != nil {
		logWarn("failed to update todos: %v", err)
	} else {
		logDebug("updated %d todos for session %s", len(todos), sessionID)
	}
}
