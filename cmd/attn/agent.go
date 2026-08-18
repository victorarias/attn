package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn agent` is how one attn-living agent reaches another. `list` is the
// address book — session ids are exposed nowhere else an agent looks — `peek`
// reads a session without interrupting it, and `msg` speaks to one.

const agentShortIDLength = 8

func runAgent() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeAgentHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "list":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentList(os.Args[3:])
	case "peek":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentPeek(os.Args[3:])
	case "msg":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentMsg(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "agent: unknown command %q\n", os.Args[2])
		writeAgentHelp(os.Stderr)
		os.Exit(2)
	}
}

type agentListArgs struct {
	json bool
}

func parseAgentListArgs(args []string) (agentListArgs, error) {
	fs := flag.NewFlagSet("agent list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args); err != nil {
		return agentListArgs{}, err
	}
	if fs.NArg() != 0 {
		return agentListArgs{}, errors.New("agent list takes no arguments")
	}
	return agentListArgs{json: *jsonOut}, nil
}

// agentListRow is the address-book projection: everything another agent needs
// to pick a target and address it, nothing more.
type agentListRow struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Agent     string `json:"agent"`
	Workspace string `json:"workspace"`
	Directory string `json:"directory"`
	State     string `json:"state"`
	TurnOwed  bool   `json:"turn_owed"`
	// Member is the crew member this session is bound as — "this session is
	// trellis today" — empty for the unbound majority. Always written, like
	// every other column: a key that disappears makes every reader of this
	// address book guard for it.
	Member string `json:"member"`
}

func runAgentList(args []string) {
	parsed, err := parseAgentListArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent list: %v\n", err)
		writeAgentHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").List("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent list: %v\n", err)
		os.Exit(1)
	}
	rows := agentListRows(result)
	if parsed.json {
		printJSON(rows)
		return
	}
	printAgentList(os.Stdout, rows)
}

func agentListRows(result *client.ListResult) []agentListRow {
	workspaceTitles := make(map[string]string, len(result.Workspaces))
	for _, workspace := range result.Workspaces {
		workspaceTitles[workspace.ID] = workspace.Title
	}
	rows := make([]agentListRow, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		rows = append(rows, agentListRow{
			ID:        session.ID,
			Label:     session.Label,
			Agent:     string(session.Agent),
			Workspace: workspaceTitles[session.WorkspaceID],
			Directory: session.Directory,
			State:     string(session.State),
			TurnOwed:  protocol.Deref(session.TurnOwed),
			Member:    protocol.Deref(session.CrewMember),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Workspace != rows[j].Workspace {
			return rows[i].Workspace < rows[j].Workspace
		}
		return rows[i].Label < rows[j].Label
	})
	return rows
}

func printAgentList(w io.Writer, rows []agentListRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No sessions on this daemon.")
		return
	}
	fmt.Fprintf(w, "%-*s  %-18s  %-8s  %-10s  %-20s  %-16s  %s\n", agentShortIDLength, "ID", "NAME", "AGENT", "MEMBER", "WORKSPACE", "STATE", "TURN")
	for _, row := range rows {
		turn := "-"
		if row.TurnOwed {
			turn = "owed"
		}
		fmt.Fprintf(
			w,
			"%-*s  %-18s  %-8s  %-10s  %-20s  %-16s  %s\n",
			agentShortIDLength, agentShortID(row.ID),
			agentListCell(row.Label, 18),
			agentListCell(row.Agent, 8),
			agentListCell(crew.DisplayName(row.Member), 10),
			agentListCell(row.Workspace, 20),
			agentListCell(row.State, 16),
			turn,
		)
	}
	fmt.Fprintf(w, "\nAn ID here is what `attn agent peek <id>` takes; --json carries full ids.\n")
}

func agentShortID(id string) string {
	if len(id) <= agentShortIDLength {
		return id
	}
	return id[:agentShortIDLength]
}

// agentListCell truncates a value to keep columns aligned; the full value is
// always available via --json.
func agentListCell(value string, width int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width-1]) + "…"
}

type agentPeekArgs struct {
	target string
	json   bool
}

func parseAgentPeekArgs(args []string) (agentPeekArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return agentPeekArgs{}, errors.New("exactly one target session id is required")
	}
	target := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("agent peek", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return agentPeekArgs{}, err
	}
	if target == "" || fs.NArg() != 0 {
		return agentPeekArgs{}, errors.New("exactly one target session id is required")
	}
	return agentPeekArgs{target: target, json: *jsonOut}, nil
}

func runAgentPeek(args []string) {
	parsed, err := parseAgentPeekArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent peek: %v\n", err)
		writeAgentHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").AgentPeek(parsed.target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent peek: %s\n", agentPeekErrorMessage(parsed.target, err))
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printAgentPeek(os.Stdout, result)
}

// agentPeekErrorMessage names the session the caller asked for: the reader is
// an agent that must fix its own command line, not a human staring at a UI.
func agentPeekErrorMessage(target string, err error) string {
	message := strings.TrimSpace(err.Error())
	switch strings.TrimSpace(strings.TrimPrefix(message, "daemon error: ")) {
	case "session_not_found":
		return fmt.Sprintf("no session matches %q; `attn agent list` names the sessions on this daemon", target)
	case "ambiguous_session":
		return fmt.Sprintf("%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", target)
	}
	return message
}

func printAgentPeek(w io.Writer, result *protocol.AgentPeekResult) {
	fmt.Fprintf(w, "session %s (%s) — %s\n", result.SessionID, result.Agent, result.Label)
	if member := strings.TrimSpace(protocol.Deref(result.CrewMember)); member != "" {
		fmt.Fprintf(w, "crew member: this session is %s today\n", crew.DisplayName(member))
	}
	workspace := strings.TrimSpace(protocol.Deref(result.WorkspaceTitle))
	if workspace != "" {
		fmt.Fprintf(w, "workspace: %s\n", workspace)
	}
	fmt.Fprintf(w, "state: %s", result.State)
	if reason := strings.TrimSpace(protocol.Deref(result.StateReason)); reason != "" {
		fmt.Fprintf(w, " (%s)", reason)
	}
	if since := formatAgentPeekTime(result.StateSince); since != "" {
		fmt.Fprintf(w, " since %s", since)
	}
	fmt.Fprintln(w)
	if protocol.Deref(result.TurnOwed) {
		fmt.Fprintln(w, "turn: owed to this session")
	}
	if len(result.Todos) > 0 {
		fmt.Fprintln(w, "\ntodos:")
		for _, todo := range result.Todos {
			fmt.Fprintf(w, "  %s\n", todo)
		}
	}
	if message := strings.TrimSpace(protocol.Deref(result.LastAssistantMessage)); message != "" {
		fmt.Fprintln(w, "\nlast assistant message:")
		for _, line := range strings.Split(message, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	if result.Screen != nil {
		fmt.Fprintf(w, "\nscreen (%dx%d):\n", result.Screen.Cols, result.Screen.Rows)
		for _, line := range strings.Split(strings.TrimRight(result.Screen.Text, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	} else {
		fmt.Fprintln(w, "\nscreen unavailable")
	}
}

// formatAgentPeekTime shows local wall-clock time; peek is read while looking
// at a live daemon, so the date matters only when it is not today.
func formatAgentPeekTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	local := parsed.Local()
	if local.Format("2006-01-02") == time.Now().Format("2006-01-02") {
		return local.Format("15:04:05")
	}
	return local.Format("2006-01-02 15:04:05")
}

type agentMsgArgs struct {
	target  string
	content string
	source  string
	json    bool
}

func parseAgentMsgArgs(args []string, envSessionID string) (agentMsgArgs, error) {
	const usage = "usage: attn agent msg <session-or-member-or-seed> \"text\" [--source-session <id>]"
	literal := len(args) > 0 && args[0] == "--"
	if literal {
		args = args[1:]
	}
	if len(args) < 2 {
		return agentMsgArgs{}, errors.New(usage)
	}
	// Without the separator a leading dash is a mistyped flag far more often
	// than it is the message, and reading `--json` as text would send it.
	if !literal && (strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-")) {
		return agentMsgArgs{}, errors.New(
			usage + `; a message that starts with - goes after --, as: attn agent msg -- <id> "-text"`)
	}
	fs := flag.NewFlagSet("agent msg", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source-session", "", "sender session id (defaults to ATTN_SESSION_ID)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[2:]); err != nil {
		return agentMsgArgs{}, err
	}
	if fs.NArg() != 0 {
		return agentMsgArgs{}, errors.New("the message must be one argument; quote it")
	}
	parsed := agentMsgArgs{
		target:  strings.TrimSpace(args[0]),
		content: args[1],
		source:  strings.TrimSpace(*source),
		json:    *jsonOut,
	}
	if parsed.source == "" {
		parsed.source = strings.TrimSpace(envSessionID)
	}
	if parsed.source == "" {
		return agentMsgArgs{}, errors.New(
			"no sender: this shell is not an attn session, so pass --source-session <id> (`attn agent list` names the sessions)")
	}
	if strings.TrimSpace(parsed.content) == "" {
		return agentMsgArgs{}, errors.New("the message is empty")
	}
	// The daemon refuses this too, and is the authority. It is checked here
	// because a message far past the limit dies as a broken pipe on the way
	// there — the socket hangs up mid-write and the refusal never comes back.
	if size := len(strings.TrimSpace(parsed.content)); size > protocol.AgentMessageMaxChars {
		return agentMsgArgs{}, fmt.Errorf(
			"message is %d bytes and the limit is %d; send the gist and point at the rest",
			size, protocol.AgentMessageMaxChars)
	}
	return parsed, nil
}

func runAgentMsg(args []string) {
	parsed, err := parseAgentMsgArgs(args, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent msg: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").AgentMsg(parsed.target, parsed.source, parsed.content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent msg: %s\n", agentMsgErrorMessage(parsed, err))
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
	} else {
		fmt.Fprintln(os.Stdout, agentMsgOutcomeLine(result))
	}
	// A refusal that exits 0 reads as sent. The reason is on stdout either way.
	if result.Status == protocol.AgentMsgStatusRefused {
		os.Exit(1)
	}
}

func agentMsgOutcomeLine(result *protocol.AgentMsgResult) string {
	if result.MessageID == "" {
		return fmt.Sprintf("%s: %s", result.Status, result.Detail)
	}
	return fmt.Sprintf("%s: %s (id %s)", result.Status, result.Detail, result.MessageID)
}

// agentMsgErrorMessage separates the two ids a send names, so a caller that
// mistyped one knows which.
func agentMsgErrorMessage(parsed agentMsgArgs, err error) string {
	message := strings.TrimSpace(err.Error())
	code := client.ErrorCode(err)
	if code == "" {
		code = strings.TrimSpace(strings.TrimPrefix(message, "daemon error: "))
	}
	switch code {
	case "session_or_crew_member_not_found":
		return fmt.Sprintf("no session or crew member matches %q; `attn agent list` names sessions and `attn crew list` names members", parsed.target)
	case "session_not_found":
		return fmt.Sprintf("no session matches %q; `attn agent list` names the sessions on this daemon", parsed.target)
	case "ambiguous_session":
		return fmt.Sprintf("%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", parsed.target)
	case "sender_session_not_found":
		return fmt.Sprintf("the sender %q is not a session on this daemon", parsed.source)
	case "sender_ambiguous_session":
		return fmt.Sprintf("the sender %q matches more than one session; give more of the id", parsed.source)
	}
	return message
}

func writeAgentHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn agent <command>

commands:
  list [--json]
        the address book: every session on this daemon with its short id,
        name, workspace, state, and whether a turn is owed. Read-only.
  peek <id> [--json]
        observe a session without interrupting it: state, todos, last
        assistant message, and the rendered screen. Passive — the observed
        agent never notices. <id> is a full session id or a unique prefix.
  msg <session-or-member-or-seed> "text" [--source-session <id>] [--json]
        send a session or crew member a message. It lands in the live session,
        or wakes a sleeping member and becomes its first prompt after priming.
        It is attributed to you and carries the command to reply with. A target that
        cannot take input right now has it queued and delivered when it can;
        the result always says which. The sender defaults to this session
        (ATTN_SESSION_ID); pass --source-session when running outside one.
        A seed id reaches whoever is tending it.
        A message that starts with - goes after --, as: agent msg -- <target> "-text"
`)
}
