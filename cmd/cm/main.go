package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/daemon"
	"github.com/victorarias/claude-manager/internal/dashboard"
	"github.com/victorarias/claude-manager/internal/status"
	"github.com/victorarias/claude-manager/internal/wrapper"
)

func main() {
	if len(os.Args) < 2 {
		runWrapper("")
		return
	}

	switch os.Args[1] {
	case "-s":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cm -s <label>")
			os.Exit(1)
		}
		runWrapper(os.Args[2])
	case "-d", "dashboard":
		runDashboard()
	case "daemon":
		runDaemon()
	case "status":
		runStatus()
	case "list":
		runList()
	case "kill":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cm kill <id>")
			os.Exit(1)
		}
		runKill(os.Args[2])
	case "_hook-todo":
		if len(os.Args) < 3 {
			os.Exit(1)
		}
		runHookTodo(os.Args[2])
	case "--help", "-h", "help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`cm - Claude Manager

Usage:
  cm                Start Claude with tracking (label = directory name)
  cm -s <label>     Start Claude with explicit label
  cm -d             Open dashboard
  cm dashboard      Open dashboard (alias)
  cm daemon         Run daemon in foreground
  cm status         Output for tmux status bar
  cm list           List all sessions (JSON)
  cm kill <id>      Unregister a session`)
}

func runWrapper(label string) {
	if label == "" {
		label = wrapper.DefaultLabel()
	}

	sessionID := wrapper.GenerateSessionID()
	socketPath := client.DefaultSocketPath()

	// Ensure daemon is running
	c := client.New(socketPath)
	if !c.IsRunning() {
		startDaemonBackground()
	}

	// Get tmux target
	tmuxTarget := getTmuxTarget()

	// Get current directory
	dir, _ := os.Getwd()

	// Register with daemon
	if err := c.Register(sessionID, label, dir, tmuxTarget); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not register with daemon: %v\n", err)
	}

	// Write hooks config
	tmpDir := os.TempDir()
	configPath, err := wrapper.WriteHooksConfig(tmpDir, sessionID, socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write hooks config: %v\n", err)
	}
	defer wrapper.CleanupHooksConfig(configPath)

	// Set up cleanup on exit
	cleanup := func() {
		c.Unregister(sessionID)
		wrapper.CleanupHooksConfig(configPath)
	}

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	// Run claude with hooks
	args := []string{"--hooks", configPath}
	args = append(args, os.Args[1:]...)

	cmd := exec.Command("claude", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func getTmuxTarget() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.Command("tmux", "display", "-p", "#{session_name}:#{window_index}.#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func startDaemonBackground() {
	cmd := exec.Command(os.Args[0], "daemon")
	cmd.Start()
	// Give daemon time to start
	// In production, would poll socket
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
	output := status.Format(sessions)
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

func runHookTodo(sessionID string) {
	// Read TodoWrite output from stdin
	var todos []string
	// Parse stdin for todo items - this is called from hook
	// For now, just touch the session
	c := client.New("")
	c.UpdateTodos(sessionID, todos)
}
