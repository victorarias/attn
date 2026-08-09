package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn app logs` and `attn app runtime` — the two commands that are about the
// shared process rather than about one app.
//
// Every installed app's handlers run in one supervised Bun sidecar. That is the
// design, not an implementation detail leaking out: isolation between apps is
// failure attribution, not an OS boundary. So "why is my app not doing
// anything" has two possible answers — the app, or the runtime — and these are
// how a reader tells them apart.

func runAppLogs(args []string) {
	name := ""
	lines := 0
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-h", arg == "--help":
			writeAppHelp(os.Stdout)
			return
		case arg == "--lines":
			i++
			if i >= len(args) {
				appFail("logs", fmt.Errorf("--lines needs a number"))
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				appFail("logs", fmt.Errorf("--lines takes a positive number; got %q", args[i]))
			}
			lines = n
		case strings.HasPrefix(arg, "--lines="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--lines="))
			if err != nil || n <= 0 {
				appFail("logs", fmt.Errorf("--lines takes a positive number; got %q", arg))
			}
			lines = n
		case strings.HasPrefix(arg, "-"):
			appFail("logs", fmt.Errorf("unknown flag %q", arg))
		default:
			if name != "" {
				appFail("logs", fmt.Errorf("takes one app name; got %q and %q", name, arg))
			}
			name = arg
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: attn app logs <name> [--lines N]")
		fmt.Fprintln(os.Stderr, "       attn app logs runtime   # the whole shared log")
		os.Exit(2)
	}
	// `runtime` is not an app and never can be — internal/apps reserves it — so
	// the name check has to let it through here.
	if name != appRuntimeName {
		if err := apps.ValidateName(name); err != nil {
			appFail("logs", err)
		}
	}

	result, err := appClient().AppLogs(name, lines)
	if err != nil {
		appFail("logs", err)
	}
	if len(result.Lines) == 0 {
		// Naming the file is the actionable half: an empty answer can mean the app
		// has never printed anything, or that the runtime has never started.
		fmt.Printf("no output from %s yet (%s)\n", name, result.Path)
		return
	}
	if result.Truncated {
		fmt.Printf("... older lines dropped; %s has the rest\n", result.Path)
	}
	for _, line := range result.Lines {
		fmt.Println(line)
	}
}

// appRuntimeName is the shared runtime's name on every surface: the supervised
// child, the log file, and `attn app logs runtime`.
const appRuntimeName = "runtime"

func runAppRuntime(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: attn app runtime status|restart")
		os.Exit(2)
	}
	rest := args[1:]
	switch args[0] {
	case "status":
		runAppRuntimeStatus(rest)
	case "restart":
		runAppRuntimeRestart(rest)
	case "-h", "--help":
		writeAppHelp(os.Stdout)
	default:
		appFail("runtime", fmt.Errorf("unknown command %q; it is status or restart", args[0]))
	}
}

func runAppRuntimeStatus(args []string) {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "-h", "--help":
			writeAppHelp(os.Stdout)
			return
		default:
			appFail("runtime status", fmt.Errorf("unknown argument %q; runtime status takes no app name — there is one runtime for every app", arg))
		}
	}
	result, err := appClient().AppRuntimeStatus()
	if err != nil {
		appFail("runtime status", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	fmt.Println("app runtime")
	if result.Runtime == nil {
		// Never started is not stopped. Saying "stopped" for a daemon that has had
		// no reason to start one would send the reader looking for a fault.
		fmt.Printf("  state:    %s\n", appRuntimeNeverStarted)
		if result.Apps > 0 && result.AppsEnabled == 0 {
			// The one case where "has not happened yet" is wrong: this daemon will
			// never start a runtime, and nothing about waiting will change that.
			fmt.Printf("            every installed app is disabled, so nothing will start one — `attn app enable <name>`\n")
		}
	} else {
		writeAppRuntimeInfo(*result.Runtime)
	}
	fmt.Printf("  apps:     %d installed, %d enabled\n", result.Apps, result.AppsEnabled)
	if result.HostPath != nil {
		fmt.Printf("  binary:   %s\n", *result.HostPath)
	}
	if result.HostError != nil {
		fmt.Printf("  binary:   NOT FOUND — %s\n", *result.HostError)
	}
	fmt.Printf("  log:      %s\n", result.LogPath)
}

func writeAppRuntimeInfo(info protocol.AppRuntimeInfo) {
	connected := "not connected"
	if info.Connected {
		connected = "connected"
		if info.Pid != nil {
			connected = fmt.Sprintf("connected, pid %d", *info.Pid)
		}
	}
	fmt.Printf("  state:    %s (%s), generation %d\n", info.Phase, connected, info.Generation)
	if info.Phase == "parked" {
		fmt.Printf("            attn gave up restarting it after %d attempts; no app's handlers are running.\n", info.RestartAttempt)
		fmt.Printf("            `attn app runtime restart` tries again.\n")
	} else if info.RestartAttempt > 0 {
		fmt.Printf("            restart attempt %d\n", info.RestartAttempt)
	}
	if info.StartedAt != nil {
		fmt.Printf("  started:  %s\n", *info.StartedAt)
	}
	if info.NextRestartAt != nil {
		fmt.Printf("  retry at: %s\n", *info.NextRestartAt)
	}
	if info.LastExit != nil {
		fmt.Printf("  last exit: %s\n", *info.LastExit)
	}
}

// appRuntimeRestartTakesNoName is the teaching error. Somebody typing an app
// name here believes apps restart individually, and the answer is the design,
// not the syntax — so it says what the runtime is before it says what to type.
func appRuntimeRestartTakesNoName(arg string) error {
	return fmt.Errorf(
		"takes no app name, and %q looks like one. Every app's handlers run in one shared runtime, so restarting it restarts them all — there is nothing per-app to restart. To stop one app, `attn app disable %s`.",
		arg, arg)
}

func runAppRuntimeRestart(args []string) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			writeAppHelp(os.Stdout)
			return
		default:
			appFail("runtime restart", appRuntimeRestartTakesNoName(arg))
		}
	}
	result, err := appClient().AppRuntimeRestart()
	if err != nil {
		appFail("runtime restart", err)
	}
	switch result.Was {
	case "parked":
		fmt.Println("revived the app runtime: it was parked after crash-looping, and is starting again")
	case "stopped":
		fmt.Println("started the app runtime")
	default:
		fmt.Printf("restarted the app runtime (was %s)\n", result.Was)
	}
	writeAppRuntimeInfo(result.Runtime)
}
