package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn crew` is the roster's agent surface. A crew member is a durable named
// identity — charter, handoff line, address — whose sessions are its days; the
// registry serves reads over the plain-markdown homes at `~/.attn/crew/`.
// Launching bound is `attn <agent> --member <name>`; this command answers who
// exists and who is awake.

func runCrew() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeCrewHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "list", "ls":
		runCrewList(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "crew: unknown command %q\n", os.Args[2])
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeCrewHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn crew <command>

The crew is the roster of durable named identities. A member's home is plain
markdown at ~/.attn/crew/<name>/; the registry serves reads over it, and a
session becomes a member by launching as one: attn <agent> --member <name>.

The crew lives at the home daemon. On an outpost every command here refuses,
naming the home to run it on.

commands:
  list [--json]
        every registered member, awake or asleep. An awake member names the
        session living its current day.
`)
}

type crewListArgs struct {
	json bool
}

func parseCrewListArgs(args []string) (crewListArgs, error) {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args); err != nil {
		return crewListArgs{}, err
	}
	if fs.NArg() != 0 {
		return crewListArgs{}, errors.New("crew list takes no arguments")
	}
	return crewListArgs{json: *jsonOut}, nil
}

func runCrewList(args []string) {
	parsed, err := parseCrewListArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew list: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").CrewList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew list: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result.Members)
		return
	}
	printCrewList(os.Stdout, result.Members)
}

func printCrewList(w io.Writer, members []protocol.CrewMember) {
	if len(members) == 0 {
		fmt.Fprintln(w, "No crew members are registered. A home at ~/.attn/crew/<name>/CHARTER.md joins the roster at the daemon's next start.")
		return
	}
	fmt.Fprintf(w, "%-12s  %-8s  %-10s  %s\n", "MEMBER", "STATE", "SESSION", "HOME")
	for _, member := range members {
		state, session := "asleep", "-"
		if id := strings.TrimSpace(protocol.Deref(member.BindingSession)); id != "" {
			state, session = "awake", agentShortID(id)
		}
		fmt.Fprintf(w, "%-12s  %-8s  %-10s  %s\n", member.ID, state, session, member.HomeDir)
	}
	fmt.Fprintf(w, "\nAn awake member's SESSION is what `attn agent peek <id>` takes.\n")
}
