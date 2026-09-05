package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionListArgs struct {
	closed bool
	all    bool
	limit  int
	before string
	json   bool
}

func parseSessionListArgs(args []string) (sessionListArgs, error) {
	fs := flag.NewFlagSet("session list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	closed := fs.Bool("closed", false, "list closed sessions instead of live ones")
	all := fs.Bool("all", false, "list live and closed sessions together")
	limit := fs.Int("limit", 0, "rows in one page (default 20)")
	before := fs.String("before", "", "start after this session id, from a previous page's notice")
	jsonOut := fs.Bool("json", false, "print the page as JSON")
	if err := fs.Parse(args); err != nil {
		return sessionListArgs{}, err
	}
	if fs.NArg() != 0 {
		return sessionListArgs{}, errors.New("session list takes no positional arguments")
	}
	if *closed && *all {
		return sessionListArgs{}, errors.New("--closed and --all ask for different lists; pass one")
	}
	if *limit < 0 {
		return sessionListArgs{}, fmt.Errorf("--limit %d is not a number of rows", *limit)
	}
	return sessionListArgs{closed: *closed, all: *all, limit: *limit, before: strings.TrimSpace(*before), json: *jsonOut}, nil
}

func runSessionList(args []string) {
	parsed, err := parseSessionListArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session list: %v\n", err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}

	result, err := client.New("").SessionList(client.SessionListOptions{
		Closed: parsed.closed,
		All:    parsed.all,
		Limit:  parsed.limit,
		Before: parsed.before,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session list: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fprintSessionList(os.Stdout, result, parsed)
}

func fprintSessionList(w io.Writer, result *protocol.SessionListResult, args sessionListArgs) {
	if len(result.Entries) == 0 {
		fmt.Fprintln(w, emptySessionListMessage(args))
		return
	}

	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tAGENT\tSTATE\tWHEN\tCLOSED BY\tLABEL")
	for _, entry := range result.Entries {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			entry.Agent,
			sessionLedgerState(entry),
			shortStamp(sessionLedgerWhen(entry)),
			orDash(protocol.Deref(entry.ClosedBy)),
			entry.Label)
	}
	table.Flush()

	for _, entry := range result.Entries {
		if reason := strings.TrimSpace(protocol.Deref(entry.CloseReason)); reason != "" {
			fmt.Fprintf(w, "\n%s closed because: %s\n", entry.ID, reason)
		}
	}

	if result.Omitted > 0 {
		fmt.Fprintf(w, "\nshowing %d, %d omitted, paginate with --before %s\n",
			len(result.Entries), result.Omitted, protocol.Deref(result.NextBefore))
	}
}

func emptySessionListMessage(args sessionListArgs) string {
	switch {
	case args.before != "":
		return "no sessions past that page"
	case args.closed:
		return "no closed sessions yet — closing one records it here"
	case args.all:
		return "no sessions in the ledger yet"
	default:
		return "no live sessions — `attn session list --closed` reads the ones that ended"
	}
}

// A closed row keeps the state it held when it closed, which would read as live.
func sessionLedgerState(entry protocol.SessionLedgerEntry) string {
	if protocol.Deref(entry.ClosedAt) != "" {
		return "closed"
	}
	return string(entry.State)
}

func sessionLedgerWhen(entry protocol.SessionLedgerEntry) string {
	if closedAt := protocol.Deref(entry.ClosedAt); closedAt != "" {
		return closedAt
	}
	return entry.LastSeen
}

func parseSessionShowArgs(args []string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("exactly one session id is required")
	}
	return strings.TrimSpace(args[0]), nil
}

func runSessionShow(args []string) {
	target, err := parseSessionShowArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session show: %v\n", err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}

	result, err := client.New("").SessionShow(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session show: %v\n", err)
		os.Exit(1)
	}
	fprintSessionShow(os.Stdout, result.Entry)
}

func fprintSessionShow(w io.Writer, entry protocol.SessionLedgerEntry) {
	fmt.Fprintf(w, "%s  %s\n", entry.ID, entry.Label)
	fmt.Fprintf(w, "agent      %s\n", entry.Agent)
	fmt.Fprintf(w, "state      %s\n", sessionLedgerState(entry))
	fmt.Fprintf(w, "directory  %s\n", entry.Directory)
	if branch := protocol.Deref(entry.Branch); branch != "" {
		fmt.Fprintf(w, "branch     %s\n", branch)
	}
	if protocol.Deref(entry.IsWorktree) {
		fmt.Fprintf(w, "worktree   yes, of %s\n", orDash(protocol.Deref(entry.MainRepo)))
	}
	fmt.Fprintf(w, "workspace  %s\n", orDash(entry.WorkspaceID))
	fmt.Fprintf(w, "last seen  %s\n", shortStamp(entry.LastSeen))
	if closedAt := protocol.Deref(entry.ClosedAt); closedAt != "" {
		fmt.Fprintf(w, "closed     %s by %s\n", shortStamp(closedAt), orDash(protocol.Deref(entry.ClosedBy)))
		if reason := strings.TrimSpace(protocol.Deref(entry.CloseReason)); reason != "" {
			fmt.Fprintf(w, "because    %s\n", reason)
		}
	}
}
