package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

var profileRoutingOverrides = []string{
	"ATTN_SOCKET_PATH",
	"ATTN_DB_PATH",
	"ATTN_CONFIG_PATH",
	"ATTN_WS_PORT",
	"ATTN_PLUGIN_DIR",
}

// runProfileEnv emits shell commands for sourcing the profile into a shell.
// Usage:
//
//	eval "$(attn profile-env dev)"     # sets ATTN_PROFILE=dev
//	eval "$(attn profile-env --unset)"  # clears ATTN_PROFILE
//
// The output is intentionally POSIX-sh compatible (export/unset) and works
// in bash, zsh, and fish's posix-compat eval. For native fish, use
// `attn profile-env --fish dev` which prints `set -gx ATTN_PROFILE dev`.
func runProfileEnv() {
	runProfileEnvArgs(os.Args[2:])
}

// runProfileEnvArgs emits the shell commands for the given args. Split out so
// both the top-level `attn profile-env …` and the `attn profile env …` alias
// share one implementation.
func runProfileEnvArgs(args []string) {
	fishMode := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--fish":
			fishMode = true
		case "-h", "--help":
			printProfileEnvHelp()
			return
		default:
			filtered = append(filtered, a)
		}
	}

	if len(filtered) == 0 {
		printProfileEnvHelp()
		os.Exit(1)
	}

	arg := strings.TrimSpace(filtered[0])
	if arg == "--unset" || arg == "none" || arg == "default" {
		writeProfileEnv(os.Stdout, "", fishMode)
		return
	}

	// Validate the requested profile name using the same rules the rest
	// of the binary applies, so typos fail here instead of silently
	// mis-routing to the default profile later.
	if err := config.ValidateProfileName(arg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	writeProfileEnv(os.Stdout, arg, fishMode)
}

// writeProfileEnv emits a complete profile selection. Explicit routing
// overrides are cleared first because attn-managed sessions inherit the current
// daemon's ATTN_SOCKET_PATH, and that override otherwise wins over ATTN_PROFILE.
func writeProfileEnv(w io.Writer, profile string, fishMode bool) {
	for _, name := range profileRoutingOverrides {
		if fishMode {
			fmt.Fprintf(w, "set -e %s\n", name)
		} else {
			fmt.Fprintf(w, "unset %s\n", name)
		}
	}
	if fishMode {
		if profile == "" {
			fmt.Fprintln(w, "set -e ATTN_PROFILE")
			return
		}
		fmt.Fprintf(w, "set -gx ATTN_PROFILE %s\n", profile)
		return
	}
	if profile == "" {
		fmt.Fprintln(w, "unset ATTN_PROFILE")
		return
	}
	fmt.Fprintf(w, "export ATTN_PROFILE=%s\n", profile)
}

func printProfileEnvHelp() {
	fmt.Fprintln(os.Stderr, `attn profile-env — emit shell commands to set or clear ATTN_PROFILE

Usage:
  eval "$(attn profile-env dev)"              # bash/zsh: export ATTN_PROFILE=dev
  eval "$(attn profile-env --unset)"           # bash/zsh: unset ATTN_PROFILE
  attn profile-env --fish dev | source         # fish: set -gx ATTN_PROFILE dev
  attn profile-env --fish --unset | source     # fish: set -e ATTN_PROFILE

Profile names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is reserved for the
development sibling install (port 29849, data dir ~/.attn-dev). Selecting or
clearing a profile also clears inherited ATTN socket, database, config, websocket
port, and plugin-directory overrides so the selected profile is authoritative.`)
}
