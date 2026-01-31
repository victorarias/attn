# CLI Entrypoint Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rebuild the missing `cmd/attn/main.go` CLI entrypoint that wraps Claude Code with session tracking hooks.

**Architecture:** Single main.go file handling CLI subcommands (daemon, status, list, _hook-*) and the default wrapper flow that registers sessions with the daemon, writes temporary hooks config, and executes claude with those hooks.

**Tech Stack:** Go stdlib (flag, os, os/exec, os/signal), internal packages (daemon, client, wrapper, hooks, config, status, protocol)

---

## Task 1: Simplify hooks.go - Remove Separate Hook Commands

**Files:**
- Modify: `internal/hooks/hooks.go:51-83`

**Step 1: Update PreToolUse/AskUserQuestion to use inline nc**

Replace the `_hook-asking` command with inline nc:

```go
"PreToolUse": {
	{
		// PreToolUse fires BEFORE tool executes - set waiting_input when Claude asks a question
		Matcher: "AskUserQuestion",
		Hooks: []Hook{
			{
				Type:    "command",
				Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"waiting_input"}' | nc -U %s`, sessionID, socketPath),
			},
		},
	},
},
```

**Step 2: Update PostToolUse/AskUserQuestion to use inline nc**

Replace the `_hook-answered` command with inline nc:

```go
{
	// PostToolUse fires AFTER user responds - set back to working
	Matcher: "AskUserQuestion",
	Hooks: []Hook{
		{
			Type:    "command",
			Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"working"}' | nc -U %s`, sessionID, socketPath),
		},
	},
},
```

**Step 3: Run existing tests**

Run: `go test ./internal/hooks/...`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/hooks/hooks.go
git commit -m "refactor(hooks): use inline nc for AskUserQuestion state updates"
```

---

## Task 2: Create Main Entrypoint Structure

**Files:**
- Create: `cmd/attn/main.go`

**Step 1: Create directory and basic main.go with subcommand dispatch**

```go
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
		if os.Args[1][0] == '-' {
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
	enc.Encode(sessions)
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
```

**Step 2: Verify it compiles**

Run: `go build -o attn ./cmd/attn`
Expected: Binary created successfully

**Step 3: Test daemon subcommand starts (then kill it)**

Run: `timeout 2 ./attn daemon 2>&1 || true`
Expected: No errors, daemon starts and times out

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat(cli): add main entrypoint with subcommand dispatch"
```

---

## Task 3: Implement Wrapper Flow

**Files:**
- Modify: `cmd/attn/main.go`

**Step 1: Add imports and flag parsing to runWrapper**

```go
import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/config"
	"github.com/victorarias/claude-manager/internal/daemon"
	"github.com/victorarias/claude-manager/internal/status"
	"github.com/victorarias/claude-manager/internal/wrapper"
)
```

**Step 2: Implement runWrapper with full flow**

```go
func runWrapper() {
	// Parse flags
	fs := flag.NewFlagSet("attn", flag.ContinueOnError)
	labelFlag := fs.String("s", "", "session label")

	// Find where our flags end and claude flags begin
	var attnArgs []string
	var claudeArgs []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			attnArgs = append(attnArgs, arg, args[i+1])
			i++ // skip next arg
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

	// Generate session ID and register
	sessionID := wrapper.GenerateSessionID()
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

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	// Build claude command
	claudeCmd := []string{"--settings", hooksPath}
	claudeCmd = append(claudeCmd, claudeArgs...)

	cmd := exec.Command("claude", claudeCmd...)
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
```

**Step 3: Verify it compiles**

Run: `go build -o attn ./cmd/attn`
Expected: Binary created successfully

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat(cli): implement wrapper flow with session registration"
```

---

## Task 4: Implement _hook-stop Command

**Files:**
- Modify: `cmd/attn/main.go`

**Step 1: Add hook input struct**

```go
// hookInput represents the JSON input from Claude Code hooks
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	ToolInput      json.RawMessage `json:"tool_input"`
}
```

**Step 2: Implement runHookStop**

```go
func runHookStop() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop <session_id>\n")
		os.Exit(1)
	}
	sessionID := os.Args[2]

	// Parse hook input from stdin
	var input hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		// If no stdin or parse error, send stop without transcript
		c := client.New("")
		c.SendStop(sessionID, "")
		return
	}

	c := client.New("")
	if err := c.SendStop(sessionID, input.TranscriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "error sending stop: %v\n", err)
		os.Exit(1)
	}
}
```

**Step 3: Verify it compiles**

Run: `go build -o attn ./cmd/attn`
Expected: Binary created successfully

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat(cli): implement _hook-stop command"
```

---

## Task 5: Implement _hook-todo Command

**Files:**
- Modify: `cmd/attn/main.go`

**Step 1: Add TodoWrite input struct**

```go
// todoWriteInput represents the tool_input for TodoWrite
type todoWriteInput struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}
```

**Step 2: Implement runHookTodo**

```go
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
```

**Step 3: Verify it compiles**

Run: `go build -o attn ./cmd/attn`
Expected: Binary created successfully

**Step 4: Commit**

```bash
git add cmd/attn/main.go
git commit -m "feat(cli): implement _hook-todo command"
```

---

## Task 6: Integration Test

**Files:**
- None (manual testing)

**Step 1: Build and install**

Run: `make install`
Expected: Binary installed, daemon restarted

**Step 2: Test status command**

Run: `attn status`
Expected: Either session/PR status or "all clear"

**Step 3: Test list command**

Run: `attn list`
Expected: JSON array (possibly empty)

**Step 4: Test wrapper briefly (Ctrl+C to exit)**

Run: `attn -s test-session`
Expected: Claude Code starts, session visible in app

**Step 5: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix(cli): integration fixes"
```

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Simplify hooks.go inline nc | `internal/hooks/hooks.go` |
| 2 | Create main entrypoint structure | `cmd/attn/main.go` |
| 3 | Implement wrapper flow | `cmd/attn/main.go` |
| 4 | Implement _hook-stop | `cmd/attn/main.go` |
| 5 | Implement _hook-todo | `cmd/attn/main.go` |
| 6 | Integration test | Manual |
