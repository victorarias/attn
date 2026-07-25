package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn state explain <id>` answers "why is this session that color?".
//
// The daemon logs the transitions it accepts, which makes a stuck color
// invisible: a stuck session is one where nothing is being accepted. The daemon
// keeps a capped ring of every state observation and what became of it, and this
// command prints it — including the observations that were vetoed before the
// store door or discarded by it, which is where a stuck color's explanation
// actually lives.

type stateExplainArgs struct {
	target string
	json   bool
}

func runState() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeStateHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "explain":
		if hasHelpFlag(os.Args[3:]) {
			writeStateHelp(os.Stdout)
			return
		}
		runStateExplain(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "state: unknown command %q\n", os.Args[2])
		writeStateHelp(os.Stderr)
		os.Exit(2)
	}
}

func parseStateExplainArgs(args []string) (stateExplainArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return stateExplainArgs{}, errors.New("exactly one target session id is required")
	}
	target := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("state explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return stateExplainArgs{}, err
	}
	if target == "" || fs.NArg() != 0 {
		return stateExplainArgs{}, errors.New("exactly one target session id is required")
	}
	return stateExplainArgs{target: target, json: *jsonOut}, nil
}

func runStateExplain(args []string) {
	parsed, err := parseStateExplainArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state explain: %v\n", err)
		writeStateHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").StateExplain(parsed.target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state explain: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printStateExplain(os.Stdout, result)
}

func printStateExplain(w io.Writer, result *protocol.StateExplainResult) {
	fmt.Fprintf(w, "session %s (%s)\n", result.SessionID, result.Agent)
	fmt.Fprintf(w, "state:   %s", result.State)
	if result.StateSince != nil && strings.TrimSpace(*result.StateSince) != "" {
		fmt.Fprintf(w, " since %s", formatStateExplainTime(*result.StateSince))
	}
	fmt.Fprintln(w)

	if len(result.Observations) == 0 {
		fmt.Fprintln(w, "\nNo state observations recorded. The daemon has seen no state evidence")
		fmt.Fprintln(w, "for this session since it started — a state shown now predates the trace.")
		return
	}

	// A full ring means the oldest observations have already been evicted, so the
	// trace is a tail and not the whole story. Saying so beats silently showing a
	// partial history as if it were complete.
	if result.Capacity > 0 && len(result.Observations) >= result.Capacity {
		fmt.Fprintf(w, "\n(showing the most recent %d observations; older ones were evicted)\n", result.Capacity)
	}

	fmt.Fprintf(w, "\n%-24s  %-18s  %-16s  %-9s  %s\n", "OBSERVED", "SOURCE", "CLAIM", "OUTCOME", "WHY")
	for _, obs := range result.Observations {
		fmt.Fprintf(
			w,
			"%-24s  %-18s  %-16s  %-9s  %s\n",
			formatStateExplainTime(obs.ObservedAt),
			orPlaceholder(obs.Source),
			orPlaceholder(obs.Claim),
			obs.Outcome,
			stateExplainWhy(obs),
		)
	}
}

// stateExplainWhy is the human column: the rejection reason if there is one,
// then the source's own detail, then the commit rule it travelled under.
func stateExplainWhy(obs protocol.StateExplainEntry) string {
	parts := make([]string, 0, 3)
	if reason := derefTrimmed(obs.Reason); reason != "" {
		parts = append(parts, reason)
	}
	if detail := derefTrimmed(obs.Detail); detail != "" {
		parts = append(parts, fmt.Sprintf("%q", detail))
	}
	if cause := derefTrimmed(obs.Cause); cause != "" {
		parts = append(parts, "cause="+cause)
	}
	return strings.Join(parts, "  ")
}

func derefTrimmed(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func orPlaceholder(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

// formatStateExplainTime shows wall-clock time to the millisecond. The trace is
// read while staring at a session, so the date is noise and sub-second ordering
// is the whole point.
func formatStateExplainTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return orPlaceholder(value)
	}
	return parsed.Local().Format("15:04:05.000")
}

func writeStateHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn state <command>

commands:
  explain <id> [--json]
        replay the daemon's recorded state observations for one session: which
        source claimed what, and whether the claim was applied, vetoed before
        the store, discarded by it, or skipped by its own source. Read-only.
`)
}
