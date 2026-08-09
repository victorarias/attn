package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn activity` is how session activity is seen and unstuck from a terminal.
//
// The lines themselves ride along on `attn list --json` with every other session
// field. What only lives here is why there are none — the feature off, no agent
// chosen, or nobody watching — and the way to forget a line that is wrong.
func runActivity() {
	args := os.Args[2:]
	if len(args) == 0 {
		activityStatus(false)
		return
	}
	switch args[0] {
	case "--json":
		activityStatus(true)
	case "clear":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(os.Stderr, "usage: attn activity clear <session-id>")
			os.Exit(1)
		}
		activityClear(strings.TrimSpace(args[1]))
	default:
		fmt.Fprintf(os.Stderr, "unknown activity command: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, activityUsage)
		os.Exit(1)
	}
}

const activityUsage = `usage: attn activity [--json]
       attn activity clear <session-id>

  attn activity          what each agent is doing, and why nothing is generated
  attn activity clear    forget one session's line and its read position
`

func activityStatus(asJSON bool) {
	warnIfDaemonVersionMismatch()
	status, err := client.New("").ActivityStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding activity status: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// The tier first, because it is the answer to "why is nothing appearing"
	// far more often than anything else here.
	fmt.Printf("presence: %s\n", status.PresenceTier)
	if status.PresenceTier == "away" {
		fmt.Println("  nothing is generated while nobody can see it")
	}
	if !status.Enabled {
		fmt.Println("activity: off (Settings › Agents › Session activity)")
		return
	}
	if reason := strings.TrimSpace(protocol.Deref(status.Error)); reason != "" {
		fmt.Printf("activity: on, but not runnable: %s\n", reason)
		return
	}
	fmt.Println("activity: on")

	if len(status.Sessions) == 0 {
		fmt.Println("no sessions generate activity lines")
		return
	}
	for _, session := range status.Sessions {
		line := strings.TrimSpace(protocol.Deref(session.Activity))
		if line == "" {
			line = "—"
		}
		fmt.Printf("  %-24s %s%s\n", session.Label, line, activityAgeSuffix(session.ActivityAt))
		// Under the line rather than instead of it: the last good line is still
		// worth showing, and the failure is why it has stopped moving.
		if reason := strings.TrimSpace(protocol.Deref(session.Error)); reason != "" {
			fmt.Printf("  %-24s   last run failed: %s\n", "", reason)
		}
	}
}

// activityAgeSuffix renders how old a line is. A line stops being refreshed the
// moment nobody is watching, so its age is what separates what an agent is doing
// from what it was doing when someone last looked.
func activityAgeSuffix(stamp *string) string {
	raw := strings.TrimSpace(protocol.Deref(stamp))
	if raw == "" {
		return ""
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	age := time.Since(at)
	switch {
	case age < time.Minute:
		return " (just now)"
	case age < time.Hour:
		return fmt.Sprintf(" (%dm ago)", int(age.Minutes()))
	default:
		return fmt.Sprintf(" (%dh ago)", int(age.Hours()))
	}
}

func activityClear(sessionID string) {
	warnIfDaemonVersionMismatch()
	if err := client.New("").ClearSessionActivity(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("cleared the activity line for %s\n", sessionID)
}
