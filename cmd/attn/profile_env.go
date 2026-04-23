package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

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
	args := os.Args[2:]
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
		if fishMode {
			fmt.Println("set -e ATTN_PROFILE")
		} else {
			fmt.Println("unset ATTN_PROFILE")
		}
		return
	}

	// Validate the requested profile name using the same rules the rest
	// of the binary applies, so typos fail here instead of silently
	// mis-routing to the default profile later.
	if err := validateRequestedProfile(arg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if fishMode {
		fmt.Printf("set -gx ATTN_PROFILE %s\n", arg)
	} else {
		fmt.Printf("export ATTN_PROFILE=%s\n", arg)
	}
}

func validateRequestedProfile(name string) error {
	// Reuse the package-level validation by temporarily setting and
	// clearing the env var. config.ValidateProfile reads ATTN_PROFILE,
	// so we need to scope this carefully.
	prev, hadPrev := os.LookupEnv("ATTN_PROFILE")
	os.Setenv("ATTN_PROFILE", name)
	err := config.ValidateProfile()
	if hadPrev {
		os.Setenv("ATTN_PROFILE", prev)
	} else {
		os.Unsetenv("ATTN_PROFILE")
	}
	return err
}

func printProfileEnvHelp() {
	fmt.Fprintln(os.Stderr, `attn profile-env — emit shell commands to set or clear ATTN_PROFILE

Usage:
  eval "$(attn profile-env dev)"              # bash/zsh: export ATTN_PROFILE=dev
  eval "$(attn profile-env --unset)"           # bash/zsh: unset ATTN_PROFILE
  attn profile-env --fish dev | source         # fish: set -gx ATTN_PROFILE dev
  attn profile-env --fish --unset | source     # fish: set -e ATTN_PROFILE

Profile names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is reserved for the
development sibling install (port 29849, data dir ~/.attn-dev).`)
}
