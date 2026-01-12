package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/status"
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
	case "status":
		runStatus()
	case "list":
		runList()
	case "_hook-stop":
		runHookStop()
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

func runDaemon() {
	d := daemon.New(config.SocketPath())
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runStatus() {
	c := client.New("")
	sessions, err := c.Query("")
	if err != nil {
		fmt.Println("? daemon offline")
		return
	}
	prs, _ := c.QueryPRs("")
	repos, _ := c.QueryRepos()
	fmt.Println(status.FormatWithPRsAndRepos(sessions, prs, repos))
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
	// If running inside the app, run claude directly
	if os.Getenv("ATTN_INSIDE_APP") == "1" {
		agent := os.Getenv("ATTN_AGENT")
		if agent == "" {
			agent = "codex"
		}
		switch strings.ToLower(agent) {
		case "codex":
			runCodexDirectly()
		case "claude":
			runClaudeDirectly()
		default:
			fmt.Fprintf(os.Stderr, "warning: unknown ATTN_AGENT %q, defaulting to codex\n", agent)
			runCodexDirectly()
		}
		return
	}

	// Otherwise, open the app via deep link
	openAppWithDeepLink()
}

func resolveExecutable(envKey, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	return fallback
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

// getClaudeProjectDir returns the Claude project directory for a given working directory.
// Claude uses ~/.claude/projects/<escaped-path>/ format.
func getClaudeProjectDir(cwd string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Claude escapes paths by replacing / and . with -
	escapedPath := strings.ReplaceAll(cwd, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	return filepath.Join(homeDir, ".claude", "projects", escapedPath)
}

// findTranscript searches all Claude project directories for a transcript with the given session ID.
// Returns the full path to the transcript file, or empty string if not found.
func findTranscript(sessionID string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	transcriptName := sessionID + ".jsonl"

	var found string
	filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		// Only look one level deep (project directories)
		if d.IsDir() && path != projectsDir {
			transcriptPath := filepath.Join(path, transcriptName)
			if _, err := os.Stat(transcriptPath); err == nil {
				found = transcriptPath
				return filepath.SkipAll // Stop searching
			}
			return filepath.SkipDir // Don't recurse into subdirectories
		}
		return nil
	})
	return found
}

// copyTranscriptForFork copies the parent transcript to the fork's project directory.
// This is needed because Claude's --resume only looks in the current project directory.
func copyTranscriptForFork(parentSessionID, forkCwd string) error {
	// Find the parent transcript
	srcPath := findTranscript(parentSessionID)
	if srcPath == "" {
		return fmt.Errorf("parent transcript not found for session %s", parentSessionID)
	}

	// Get the destination directory
	destDir := getClaudeProjectDir(forkCwd)
	if destDir == "" {
		return fmt.Errorf("could not determine Claude project directory")
	}

	// Create the destination directory if it doesn't exist
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	// Copy the transcript
	destPath := filepath.Join(destDir, parentSessionID+".jsonl")

	// Don't copy if it's already there (same project directory)
	if srcPath == destPath {
		return nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source transcript: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create destination transcript: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy transcript: %w", err)
	}

	return nil
}

// findCodexTranscript searches Codex session logs for the most recent session
// matching the given cwd and start time. Returns empty string if not found.
func findCodexTranscript(cwd string, startedAt time.Time) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sessionsDir := filepath.Join(homeDir, ".codex", "sessions")

	type codexLine struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}

	var bestPath string
	var bestTime time.Time
	cwdClean := filepath.Clean(cwd)

	filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		// Skip files that are too old to be relevant
		if info.ModTime().Before(startedAt.Add(-5 * time.Minute)) {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}

		reader := bufio.NewReader(f)
		line, readErr := reader.ReadBytes('\n')
		f.Close()
		if readErr != nil && len(line) == 0 {
			return nil
		}

		var entry codexLine
		if json.Unmarshal(bytes.TrimSpace(line), &entry) != nil {
			return nil
		}
		if entry.Type != "session_meta" {
			return nil
		}

		entryCwd := filepath.Clean(entry.Payload.Cwd)
		if entryCwd != cwdClean {
			return nil
		}

		ts := entry.Payload.Timestamp
		if ts == "" {
			ts = entry.Timestamp
		}
		if ts == "" {
			return nil
		}

		sessionTime, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr != nil {
			return nil
		}
		if sessionTime.Before(startedAt.Add(-5 * time.Minute)) {
			return nil
		}

		if bestPath == "" || sessionTime.After(bestTime) {
			bestPath = path
			bestTime = sessionTime
		}

		return nil
	})

	return bestPath
}

// runClaudeDirectly runs claude with hooks (used when inside the app)
func runClaudeDirectly() {
	// Parse flags
	fs := flag.NewFlagSet("attn", flag.ContinueOnError)
	labelFlag := fs.String("s", "", "session label")
	resumeFlag := fs.String("resume", "", "session ID to resume from")
	forkFlag := fs.Bool("fork-session", false, "fork the resumed session")

	// Find where our flags end and claude flags begin
	var attnArgs []string
	var claudeArgs []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--resume" && i+1 < len(args) && args[i+1] != "--" {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--fork-session" {
			attnArgs = append(attnArgs, arg)
		} else if arg == "--" {
			claudeArgs = append(claudeArgs, args[i+1:]...)
			break
		} else {
			claudeArgs = append(claudeArgs, arg)
		}
	}

	fs.Parse(attnArgs)

	// Get label
	label := *labelFlag
	if label == "" {
		label = wrapper.DefaultLabel()
	}

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}

	// Ensure daemon is running
	c := client.New("")
	if !c.IsRunning() {
		if err := startDaemonBackground(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start daemon: %v\n", err)
		}
	}

	// Use session ID from environment if provided (from frontend), otherwise generate
	sessionID := os.Getenv("ATTN_SESSION_ID")
	if sessionID == "" {
		sessionID = wrapper.GenerateSessionID()
	}
	if err := c.Register(sessionID, label, cwd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register session: %v\n", err)
	}

	// Write hooks config
	socketPath := config.SocketPath()
	hooksPath, err := wrapper.WriteHooksConfig(os.TempDir(), sessionID, socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing hooks config: %v\n", err)
		os.Exit(1)
	}

	// Setup cleanup
	cleanup := func() {
		wrapper.CleanupHooksConfig(hooksPath)
		c.Unregister(sessionID)
	}

	// Build claude command
	claudeCmd := []string{"--session-id", sessionID, "--settings", hooksPath}

	// Add fork flags if resuming
	if *resumeFlag != "" {
		// Copy parent transcript to fork directory so --resume can find it
		// (Claude only looks in the current project directory)
		if err := copyTranscriptForFork(*resumeFlag, cwd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not copy transcript for fork: %v\n", err)
			// Continue anyway - Claude will start fresh if transcript not found
		}
		claudeCmd = append(claudeCmd, "--resume", *resumeFlag)
		if *forkFlag {
			claudeCmd = append(claudeCmd, "--fork-session")
		}
	}

	claudeCmd = append(claudeCmd, claudeArgs...)

	claudeExecutable := resolveExecutable("ATTN_CLAUDE_EXECUTABLE", "claude")
	cmd := exec.Command(claudeExecutable, claudeCmd...)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start claude (non-blocking so we can set up signal forwarding)
	if err = cmd.Start(); err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "error starting claude: %v\n", err)
		os.Exit(1)
	}

	// Handle signals - forward to claude subprocess
	// Must be after cmd.Start() so cmd.Process is available
	// Don't os.Exit here - let cmd.Wait() complete so claude can run its hooks
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		// Always send SIGTERM to claude for graceful shutdown (triggers cleanup hooks)
		// SIGHUP from PTY just kills the process without cleanup
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	// Wait for claude to exit
	err = cmd.Wait()
	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

// runCodexDirectly runs codex (used when inside the app)
func runCodexDirectly() {
	// Parse flags
	fs := flag.NewFlagSet("attn", flag.ContinueOnError)
	labelFlag := fs.String("s", "", "session label")
	resumeFlag := fs.String("resume", "", "session ID to resume from")
	forkFlag := fs.Bool("fork-session", false, "fork the resumed session")

	// Find where our flags end and codex flags begin
	var attnArgs []string
	var codexArgs []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--resume" && i+1 < len(args) && args[i+1] != "--" {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++
		} else if arg == "--fork-session" {
			attnArgs = append(attnArgs, arg)
		} else if arg == "--" {
			codexArgs = append(codexArgs, args[i+1:]...)
			break
		} else {
			codexArgs = append(codexArgs, arg)
		}
	}

	fs.Parse(attnArgs)

	// Get label
	label := *labelFlag
	if label == "" {
		label = wrapper.DefaultLabel()
	}

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}

	// Ensure daemon is running
	c := client.New("")
	if !c.IsRunning() {
		if err := startDaemonBackground(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start daemon: %v\n", err)
		}
	}

	// Use session ID from environment if provided (from frontend), otherwise generate
	sessionID := os.Getenv("ATTN_SESSION_ID")
	if sessionID == "" {
		sessionID = wrapper.GenerateSessionID()
	}
	if err := c.Register(sessionID, label, cwd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register session: %v\n", err)
	}

	if *resumeFlag != "" {
		fmt.Fprintf(os.Stderr, "warning: codex resume/fork not supported yet (ignoring --resume/--fork-session)\n")
	}
	if *forkFlag {
		// Keep silent beyond the resume warning; flag is meaningless without resume support.
	}

	// Build codex command
	hasCwd := false
	for i := 0; i < len(codexArgs); i++ {
		if codexArgs[i] == "-C" || codexArgs[i] == "--cd" {
			hasCwd = true
			break
		}
	}
	if !hasCwd {
		codexArgs = append(codexArgs, "-C", cwd)
	}

	codexExecutable := resolveExecutable("ATTN_CODEX_EXECUTABLE", "codex")
	cmd := exec.Command(codexExecutable, codexArgs...)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	startedAt := time.Now()

	// Start codex (non-blocking so we can set up signal forwarding)
	if err = cmd.Start(); err != nil {
		c.Unregister(sessionID)
		fmt.Fprintf(os.Stderr, "error starting codex: %v\n", err)
		os.Exit(1)
	}

	// Handle signals - forward to codex subprocess
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	// Wait for codex to exit
	err = cmd.Wait()

	// Attempt stop/classification before unregistering
	transcriptPath := findCodexTranscript(cwd, startedAt)
	if sendErr := c.SendStop(sessionID, transcriptPath); sendErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not send stop: %v\n", sendErr)
	}

	c.Unregister(sessionID)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
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
	c := client.New("")
	if err := c.SendStop(sessionID, transcriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "error sending stop: %v\n", err)
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

	c := client.New("")
	if err := c.UpdateTodos(sessionID, todos); err != nil {
		fmt.Fprintf(os.Stderr, "error updating todos: %v\n", err)
		os.Exit(1)
	}
}
