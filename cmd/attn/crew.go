package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
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
	case "wake":
		runCrewWake(os.Args[3:])
	case "sleep":
		runCrewSleep(os.Args[3:])
	case "set":
		runCrewSet(os.Args[3:])
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

  wake <member> [--agent <name>] [--json]
        start a member's day: a session bound to it, launched in the member's
        own cwd with its awareness dirs, on the pinned crew model, primed with
        where to read its charter, the freshest letter left for it, and how its
        home works. A member that is already
        awake is not woken twice — the answer names the session it is living.

  sleep <member> [--json]
        ask an awake member to write its handoff letter and file it with
        attn handoff --sleep. This requests consented closure; it does not
        kill the member. An already-asleep member is a named no-op.

  set <member> [--cwd <dir>] [--awareness-dir <dir>]...
        record where the member's sessions launch and which directories its
        charter is about. Registry state; the home's markdown is never
        rewritten. --awareness-dir repeats and replaces the whole list; pass it
        once with an empty value to clear it.
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

type crewWakeArgs struct {
	member string
	agent  string
	json   bool
}

func parseCrewWakeArgs(args []string) (crewWakeArgs, error) {
	fs := flag.NewFlagSet("crew wake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agent := fs.String("agent", "", "the harness to launch (default claude)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew wake")
	if err != nil {
		return crewWakeArgs{}, err
	}
	return crewWakeArgs{member: member, agent: strings.TrimSpace(*agent), json: *jsonOut}, nil
}

// parseMemberAndFlags reads `<member>` and its flags in either order. Go's flag
// package stops at the first positional, and `attn crew wake trellis --agent
// codex` is how anyone types it — so the member is lifted out and the rest
// parsed behind it.
func parseMemberAndFlags(fs *flag.FlagSet, args []string, verb string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", errors.New(verb + " takes one member name")
	}
	member, rest := fs.Arg(0), fs.Args()[1:]
	if len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return "", err
		}
		if fs.NArg() != 0 {
			return "", fmt.Errorf("%s takes one member name, not %q", verb, strings.Join(fs.Args(), " "))
		}
	}
	return member, nil
}

func runCrewWake(args []string) {
	parsed, err := parseCrewWakeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew wake: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").CrewWake(parsed.member, parsed.agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew wake: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if result.AlreadyAwake {
		fmt.Printf("%s is already awake in session %s — nothing was launched.\n", crew.DisplayName(result.Member), agentShortID(result.SessionID))
		return
	}
	if repair := crewWakeRepairLine(result); repair != "" {
		fmt.Fprintln(os.Stdout, repair)
	}
	fmt.Printf("%s is awake in session %s. `attn agent peek %[2]s` watches the day; the priming size is in the daemon log (grep `crew: priming`).\n",
		crew.DisplayName(result.Member), agentShortID(result.SessionID))
}

func crewWakeRepairLine(result *protocol.CrewWakeResult) string {
	released := strings.TrimSpace(protocol.Deref(result.ReleasedSessionID))
	if released == "" {
		return ""
	}
	return fmt.Sprintf("Previous session %s had exited; its binding was released.", agentShortID(released))
}

type crewSleepArgs struct {
	member string
	json   bool
}

func parseCrewSleepArgs(args []string) (crewSleepArgs, error) {
	fs := flag.NewFlagSet("crew sleep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew sleep")
	if err != nil {
		return crewSleepArgs{}, err
	}
	return crewSleepArgs{member: member, json: *jsonOut}, nil
}

func runCrewSleep(args []string) {
	parsed, err := parseCrewSleepArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew sleep: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").CrewSleep(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew sleep: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fmt.Fprintln(os.Stdout, crewSleepOutcomeLine(result))
}

func crewSleepOutcomeLine(result *protocol.CrewSleepResult) string {
	name := crew.DisplayName(result.Member)
	if result.AlreadyAsleep {
		if detail := strings.TrimSpace(result.Detail); detail != "" {
			return detail + "."
		}
		return fmt.Sprintf("%s is already asleep — no sleep request was sent.", name)
	}
	if result.DeliveryStatus != nil && *result.DeliveryStatus == protocol.AgentMsgStatusQueued {
		detail := strings.TrimSpace(result.Detail)
		if detail == "" {
			detail = "the member is not taking input right now"
		}
		return fmt.Sprintf("Sleep request for %s is queued in session %s — %s", name, agentShortID(protocol.Deref(result.SessionID)), detail)
	}
	return fmt.Sprintf("Asked %s in session %s to write its handoff and file it with `attn handoff --sleep`.", name, agentShortID(protocol.Deref(result.SessionID)))
}

// crewDirList collects a repeatable flag. An explicit empty value clears the
// list — the way out of every awareness dir the member has.
type crewDirList struct {
	values []string
	set    bool
}

func (l *crewDirList) String() string { return strings.Join(l.values, ",") }

func (l *crewDirList) Set(value string) error {
	l.set = true
	if strings.TrimSpace(value) != "" {
		l.values = append(l.values, value)
	}
	return nil
}

func runCrewSet(args []string) {
	fs := flag.NewFlagSet("crew set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cwd := fs.String("cwd", "", "where the member's sessions launch")
	var dirs crewDirList
	fs.Var(&dirs, "awareness-dir", "a directory the member's charter is about; repeat for several")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew set")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew set: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	var cwdArg *string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "cwd" {
			cwdArg = cwd
		}
	})
	if cwdArg == nil && !dirs.set {
		fmt.Fprintln(os.Stderr, "crew set: nothing to set — pass --cwd, --awareness-dir, or both")
		os.Exit(2)
	}
	var awareness []string
	if dirs.set {
		awareness = dirs.values
		if awareness == nil {
			awareness = []string{}
		}
	}
	result, err := client.New("").CrewSet(member, cwdArg, awareness)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew set: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(result.Member)
		return
	}
	record := result.Member
	fmt.Printf("%s launches in %s\n", crew.DisplayName(record.ID), valueOrDash(protocol.Deref(record.Cwd)))
	fmt.Printf("awareness dirs: %s\n", valueOrDash(strings.Join(record.AwarenessDirs, ", ")))
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
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
		fmt.Fprintf(w, "%-12s  %-8s  %-10s  %s\n", crew.DisplayName(member.ID), state, session, member.HomeDir)
	}
	fmt.Fprintf(w, "\nAn awake member's SESSION is what `attn agent peek <id>` takes.\n")
}
