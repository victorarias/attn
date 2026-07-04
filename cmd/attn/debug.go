package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const (
	debugIncidentsFile   = "terminal-incidents.jsonl"
	debugDiagnosticsFile = "terminal-diagnostics.jsonl"
	defaultDebugTail     = 50
)

// runDebug routes `attn debug <command>`: single, typo-proof probes over the
// known debug artifacts (frontend disk-based diagnostics under the profile's
// app-support "debug" directory, and the profile's daemon.log) so nobody has
// to hand-roll cd+tail+jq/grep into `~/Library/Application Support/com.attn.manager*`.
func runDebug() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeDebugHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "ls":
		if hasHelpFlag(os.Args[3:]) {
			writeDebugHelp(os.Stdout)
			return
		}
		runDebugLs(os.Args[3:])
	case "incidents":
		if hasHelpFlag(os.Args[3:]) {
			writeDebugHelp(os.Stdout)
			return
		}
		runDebugJSONL(debugIncidentsFile, "debug incidents", os.Args[3:])
	case "diagnostics":
		if hasHelpFlag(os.Args[3:]) {
			writeDebugHelp(os.Stdout)
			return
		}
		runDebugJSONL(debugDiagnosticsFile, "debug diagnostics", os.Args[3:])
	case "daemon-log":
		if hasHelpFlag(os.Args[3:]) {
			writeDebugHelp(os.Stdout)
			return
		}
		runDebugDaemonLog(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "debug: unknown command %q\n", os.Args[2])
		writeDebugHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeDebugHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn debug <command>

commands:
  ls
        list the profile's debug artifacts: files under the frontend's debug
        directory (name, size, mtime) and the daemon.log path/size
  incidents [--tail N] [--grep PATTERN] [--json]
        print lines from terminal-incidents.jsonl (auto-captured render bugs)
  diagnostics [--tail N] [--grep PATTERN] [--json]
        print lines from terminal-diagnostics.jsonl (lifecycle stream)
  daemon-log [--tail N] [--since DUR] [--grep PATTERN]
        print lines from the profile's daemon.log; --since takes a Go
        duration (e.g. 10m, 1h) and filters to lines timestamped within it

flags:
  --tail N        only the last N lines (default 50; 0 means no limit)
  --grep PATTERN  only lines matching this Go regexp
  --json          accepted as a no-op alias on incidents/diagnostics: the
                  JSONL files already hold one machine-readable JSON object
                  per line, so both commands always print raw lines verbatim

All commands honor the active ATTN_PROFILE, the same as the rest of the CLI.
`)
}

// runDebugLs lists the profile's known debug artifacts and prints the
// resolved directory paths so users learn where things live instead of
// hand-rolling the path themselves.
func runDebugLs(args []string) {
	fs := flag.NewFlagSet("debug ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "debug ls: %v\n", err)
		os.Exit(2)
	}

	debugDir := filepath.Join(config.AppSupportDir(), "debug")
	logPath := config.LogPath()

	fmt.Printf("frontend debug dir: %s\n", debugDir)
	entries, err := os.ReadDir(debugDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  (no such directory)")
		} else {
			fmt.Fprintf(os.Stderr, "debug ls: %v\n", err)
			os.Exit(1)
		}
	} else if len(entries) == 0 {
		fmt.Println("  (empty)")
	} else {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fmt.Printf("  %-32s %10d bytes  %s\n", entry.Name(), info.Size(), info.ModTime().Format(time.RFC3339))
		}
	}

	fmt.Printf("daemon.log: %s\n", logPath)
	if info, err := os.Stat(logPath); err == nil {
		fmt.Printf("  %d bytes  %s\n", info.Size(), info.ModTime().Format(time.RFC3339))
	} else if os.IsNotExist(err) {
		fmt.Println("  (no such file)")
	} else {
		fmt.Fprintf(os.Stderr, "debug ls: %v\n", err)
		os.Exit(1)
	}
}

// runDebugJSONL implements the shared shape of `debug incidents` and
// `debug diagnostics`: tail/grep raw JSONL lines. --json is accepted as a
// no-op forward-compat alias, documented in writeDebugHelp — the lines are
// already machine-readable JSON, so there is nothing to re-wrap.
func runDebugJSONL(fileName, cmdName string, args []string) {
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tail := fs.Int("tail", defaultDebugTail, "only the last N lines (0 means no limit)")
	grep := fs.String("grep", "", "only lines matching this Go regexp")
	_ = fs.Bool("json", false, "no-op alias; JSONL lines are printed raw either way")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s: unexpected arguments: %v\n", cmdName, fs.Args())
		os.Exit(2)
	}

	path := filepath.Join(config.AppSupportDir(), "debug", fileName)
	lines, err := readLinesFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lines, err = grepLines(lines, *grep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(2)
	}
	lines = tailLines(lines, *tail)
	for _, line := range lines {
		fmt.Println(line)
	}
}

// runDebugDaemonLog reads the profile's daemon.log with tail/grep/since
// filters. --since parses a Go duration and filters lines whose leading
// "[2006-01-02 15:04:05]" timestamp (see internal/logging/logging.go) is
// within that duration of now; untimestamped continuation lines follow the
// most recent timestamped line's match state (see filterSinceLines).
func runDebugDaemonLog(args []string) {
	fs := flag.NewFlagSet("debug daemon-log", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tail := fs.Int("tail", defaultDebugTail, "only the last N lines (0 means no limit)")
	grep := fs.String("grep", "", "only lines matching this Go regexp")
	since := fs.String("since", "", "only lines within this Go duration of now (e.g. 10m, 1h)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "debug daemon-log: %v\n", err)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "debug daemon-log: unexpected arguments: %v\n", fs.Args())
		os.Exit(2)
	}

	path := config.LogPath()
	lines, err := readLinesFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if sinceStr := *since; sinceStr != "" {
		dur, err := time.ParseDuration(sinceStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "debug daemon-log: invalid --since duration %q: %v\n", sinceStr, err)
			os.Exit(2)
		}
		lines = filterSinceLines(lines, time.Now().Add(-dur))
	}

	lines, err = grepLines(lines, *grep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "debug daemon-log: %v\n", err)
		os.Exit(2)
	}
	lines = tailLines(lines, *tail)
	for _, line := range lines {
		fmt.Println(line)
	}
}
