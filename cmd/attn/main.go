package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/config"
	"github.com/victorarias/claude-manager/internal/daemon"
	"github.com/victorarias/claude-manager/internal/status"
)

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
	// Placeholder - implemented in Task 3
	fmt.Println("wrapper not implemented")
}

func runHookStop() {
	// Placeholder - implemented in Task 4
}

func runHookTodo() {
	// Placeholder - implemented in Task 5
}
