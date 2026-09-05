package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

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
	case "close":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentClose(os.Args[3:])
	case "inbox":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentInbox(os.Args[3:])
	case "msg-status":
		if hasHelpFlag(os.Args[3:]) {
			writeAgentHelp(os.Stdout)
			return
		}
		runAgentMsgStatus(os.Args[3:])
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

type agentListRow struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Agent     string `json:"agent"`
	Workspace string `json:"workspace"`
	Directory string `json:"directory"`
	State     string `json:"state"`
	TurnOwed  bool   `json:"turn_owed"`
	// The crew member this session is bound as, empty for the unbound majority.
	// Always written: a key that disappears makes every reader guard for it.
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
	fmt.Fprintf(w, "\nAn ID or awake MEMBER here works with `attn agent peek <target>`; --json carries full ids.\n")
}

func agentShortID(id string) string {
	if len(id) <= agentShortIDLength {
		return id
	}
	return id[:agentShortIDLength]
}

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
		return agentPeekArgs{}, errors.New("exactly one target session or crew member is required")
	}
	target := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("agent peek", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return agentPeekArgs{}, err
	}
	if target == "" || fs.NArg() != 0 {
		return agentPeekArgs{}, errors.New("exactly one target session or crew member is required")
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

func agentPeekErrorMessage(target string, err error) string {
	message := strings.TrimSpace(err.Error())
	switch strings.TrimSpace(strings.TrimPrefix(message, "daemon error: ")) {
	case "session_not_found":
		return fmt.Sprintf("no session or crew member matches %q; `attn agent list` names sessions and `attn crew list` names members", target)
	case "ambiguous_session":
		return fmt.Sprintf("%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", target)
	case "crew_member_asleep":
		member := strings.ToLower(strings.TrimSpace(target))
		return fmt.Sprintf("%s is asleep; `attn agent peek` never wakes crew members. `attn crew wake %s` starts a day", crew.DisplayName(member), member)
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
	screenTitle := "screen"
	if exit := result.Exit; exit != nil {
		fmt.Fprintf(w, "\nprocess exited with code %d", exit.Code)
		if signal := strings.TrimSpace(protocol.Deref(exit.Signal)); signal != "" {
			fmt.Fprintf(w, " (%s)", signal)
		}
		if at := formatAgentPeekTime(exit.At); at != "" {
			fmt.Fprintf(w, " at %s", at)
		}
		fmt.Fprintln(w)
		screenTitle = "screen at exit"
	}
	if result.Screen != nil {
		fmt.Fprintf(w, "\n%s (%dx%d):\n", screenTitle, result.Screen.Cols, result.Screen.Rows)
		for _, line := range strings.Split(strings.TrimRight(result.Screen.Text, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	} else {
		fmt.Fprintln(w, "\nscreen unavailable")
	}
}

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
	// The daemon is the authority, but a message far past the limit dies as a broken
	// pipe on the way there and its refusal never comes back.
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

type agentCloseArgs struct {
	target string
	reason string
	source string
	json   bool
}

func parseAgentCloseArgs(args []string, envSessionID string) (agentCloseArgs, error) {
	const usage = `usage: attn agent close <session-or-seed> -m "reason" [--source-session <id>]`
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return agentCloseArgs{}, errors.New(usage)
	}
	fs := flag.NewFlagSet("agent close", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	reason := fs.String("m", "", "why this session is done")
	source := fs.String("source-session", "", "closing session id (defaults to ATTN_SESSION_ID)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return agentCloseArgs{}, err
	}
	if fs.NArg() != 0 {
		return agentCloseArgs{}, errors.New(usage + "; quote the reason as one argument")
	}
	parsed := agentCloseArgs{
		target: strings.TrimSpace(args[0]),
		reason: strings.TrimSpace(*reason),
		source: strings.TrimSpace(*source),
		json:   *jsonOut,
	}
	if parsed.target == "" {
		return agentCloseArgs{}, errors.New(usage)
	}
	if parsed.reason == "" {
		return agentCloseArgs{}, errors.New(
			`a close needs a reason: -m "why this session is done". The session row stays in the ledger and the reason is what the next reader gets`)
	}
	if parsed.source == "" {
		parsed.source = strings.TrimSpace(envSessionID)
	}
	if parsed.source == "" {
		return agentCloseArgs{}, errors.New(
			"no caller: this shell is not an attn session, so pass --source-session <id> (`attn agent list` names the sessions)")
	}
	return parsed, nil
}

func runAgentClose(args []string) {
	parsed, err := parseAgentCloseArgs(args, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent close: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").AgentClose(parsed.target, parsed.source, parsed.reason)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent close: %s\n", agentCloseErrorMessage(parsed, err))
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printAgentClose(os.Stdout, result)
}

func printAgentClose(w io.Writer, result *protocol.AgentCloseResult) {
	fmt.Fprintf(w, "closed session %s (%s): %s\n", agentShortID(result.TargetSessionID), result.Label, result.Reason)
	for _, seedID := range result.SeedIds {
		fmt.Fprintf(w, "noted on %s, which it was tending\n", seedID)
	}
	fmt.Fprintf(w, "the session is kept: `attn session show %s` reads it back\n", agentShortID(result.TargetSessionID))
}

func agentCloseErrorMessage(parsed agentCloseArgs, err error) string {
	message := strings.TrimSpace(err.Error())
	code := client.ErrorCode(err)
	if code == "" {
		code = strings.TrimSpace(strings.TrimPrefix(message, "daemon error: "))
	}
	switch code {
	case "sender_session_not_found":
		return fmt.Sprintf("the caller %q is not a session on this daemon", parsed.source)
	case "sender_ambiguous_session":
		return fmt.Sprintf("the caller %q matches more than one session; give more of the id", parsed.source)
	}
	return message
}

type agentMailboxArgs struct {
	messageID string
	sessionID string
	json      bool
}

type agentInboxArgs struct {
	messageID string
	sessionID string
	limit     int
	json      bool
}

func parseAgentInboxArgs(args []string, envSessionID string) (agentInboxArgs, error) {
	const usage = "usage: attn agent inbox [message-id] [--limit <count>] [--session <id>] [--json]"
	parsed := agentInboxArgs{limit: agentmailbox.DefaultInboxLimit}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		parsed.messageID = strings.TrimSpace(args[0])
		if parsed.messageID == "" {
			return agentInboxArgs{}, errors.New(usage)
		}
		args = args[1:]
	}
	fs := flag.NewFlagSet("agent inbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "authorized session id (defaults to ATTN_SESSION_ID)")
	fs.IntVar(&parsed.limit, "limit", agentmailbox.DefaultInboxLimit, "maximum unread items to return")
	fs.BoolVar(&parsed.json, "json", false, "print the machine result as JSON")
	if err := fs.Parse(args); err != nil {
		return agentInboxArgs{}, err
	}
	if fs.NArg() > 1 || (parsed.messageID != "" && fs.NArg() != 0) {
		return agentInboxArgs{}, errors.New(usage)
	}
	if parsed.messageID == "" && fs.NArg() == 1 {
		parsed.messageID = strings.TrimSpace(fs.Arg(0))
		if parsed.messageID == "" {
			return agentInboxArgs{}, errors.New(usage)
		}
	}
	if parsed.limit < 1 || parsed.limit > agentmailbox.MaxInboxLimit {
		return agentInboxArgs{}, fmt.Errorf("--limit must be between 1 and %d", agentmailbox.MaxInboxLimit)
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	if parsed.messageID != "" && limitSet {
		return agentInboxArgs{}, errors.New("--limit cannot be used with a message id")
	}
	parsed.sessionID = strings.TrimSpace(*sessionID)
	if parsed.sessionID == "" {
		parsed.sessionID = strings.TrimSpace(envSessionID)
	}
	if parsed.sessionID == "" {
		return agentInboxArgs{}, errors.New("no session identity: run this inside an attn session or pass --session <id>")
	}
	return parsed, nil
}

func parseAgentMailboxArgs(command string, args []string, envSessionID string) (agentMailboxArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return agentMailboxArgs{}, fmt.Errorf("usage: attn agent %s <message-id> [--session <id>] [--json]", command)
	}
	fs := flag.NewFlagSet("agent "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "authorized session id (defaults to ATTN_SESSION_ID)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return agentMailboxArgs{}, err
	}
	if fs.NArg() != 0 || strings.TrimSpace(args[0]) == "" {
		return agentMailboxArgs{}, fmt.Errorf("usage: attn agent %s <message-id> [--session <id>] [--json]", command)
	}
	parsed := agentMailboxArgs{
		messageID: strings.TrimSpace(args[0]), sessionID: strings.TrimSpace(*sessionID), json: *jsonOut,
	}
	if parsed.sessionID == "" {
		parsed.sessionID = strings.TrimSpace(envSessionID)
	}
	if parsed.sessionID == "" {
		return agentMailboxArgs{}, errors.New("no session identity: run this inside an attn session or pass --session <id>")
	}
	return parsed, nil
}

func runAgentInbox(args []string) {
	parsed, err := parseAgentInboxArgs(args, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent inbox: %v\n", err)
		os.Exit(2)
	}
	if parsed.messageID != "" {
		result, err := client.New("").AgentInbox(parsed.messageID, parsed.sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent inbox: %s\n", agentMailboxErrorMessage(agentMailboxArgs{
				messageID: parsed.messageID, sessionID: parsed.sessionID,
			}, err))
			os.Exit(1)
		}
		if parsed.json {
			printJSON(result)
			return
		}
		printAgentInbox(os.Stdout, result)
		return
	}
	result, err := client.New("").AgentInboxBatch(parsed.sessionID, parsed.limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent inbox: %s\n", strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printAgentInboxBatch(os.Stdout, result)
}

func runAgentMsgStatus(args []string) {
	parsed, err := parseAgentMailboxArgs("msg-status", args, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent msg-status: %v\n", err)
		os.Exit(2)
	}
	result, err := client.New("").AgentMsgStatus(parsed.messageID, parsed.sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent msg-status: %s\n", agentMailboxErrorMessage(parsed, err))
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printAgentMsgStatus(os.Stdout, result)
}

func printAgentInbox(w io.Writer, message *protocol.AgentPeerMessage) {
	origin := agentShortID(message.SenderSessionID)
	if label := strings.TrimSpace(message.SenderLabel); label != "" && label != origin {
		origin = fmt.Sprintf("%s (%s)", origin, label)
	}
	fmt.Fprintln(w, prompts.RenderText("session", "peer-message", prompts.Values{"origin": origin, "message": message.Content, "sender_id": agentShortID(message.SenderSessionID)}))
}

func printAgentInboxBatch(w io.Writer, result *protocol.AgentInboxBatchResult) {
	if len(result.Items) == 0 {
		fmt.Fprintln(w, prompts.RenderText("session", "inbox-empty", nil))
		return
	}
	for _, item := range result.Items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			content = item.Kind
			if sourceID := strings.TrimSpace(protocol.Deref(item.SourceID)); sourceID != "" {
				content += " " + sourceID
			}
		}
		if item.Kind != string(agentmailbox.KindPeerMessage) {
			if item.Kind == string(agentmailbox.KindMaintenancePrompt) {
				fmt.Fprintln(w, content)
			} else {
				fmt.Fprintln(w, prompts.RenderText("session", "inbox-item", prompts.Values{"content": content}))
			}
			continue
		}
		printAgentInbox(w, &protocol.AgentPeerMessage{
			SenderSessionID: strings.TrimSpace(protocol.Deref(item.SenderSessionID)),
			SenderLabel:     protocol.Deref(item.SenderLabel), Content: content,
		})
	}
	if result.Remaining > 0 {
		fmt.Fprintln(w, prompts.RenderText("session", "inbox-more", prompts.Values{"remaining": fmt.Sprint(result.Remaining)}))
	}
}

func printAgentMsgStatus(w io.Writer, message *protocol.AgentPeerMessage) {
	fmt.Fprintf(w, "%s: message %s to session %s\n", message.State, message.MessageID, agentShortID(message.TargetSessionID))
}

func agentMailboxErrorMessage(parsed agentMailboxArgs, err error) string {
	code := client.ErrorCode(err)
	switch code {
	case "recipient_session_not_found", "sender_session_not_found":
		return fmt.Sprintf("the session %q is not on this daemon", parsed.sessionID)
	case "recipient_ambiguous_session", "sender_ambiguous_session":
		return fmt.Sprintf("the session %q matches more than one session; give more of the id", parsed.sessionID)
	case "message_not_found":
		return fmt.Sprintf("message %q was not found for this session", parsed.messageID)
	case "message_not_notified":
		return fmt.Sprintf("message %q is still queued; read it after its notification lands", parsed.messageID)
	}
	return strings.TrimSpace(err.Error())
}

func writeAgentHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn agent <command>

commands:
  list [--json]
        the address book: every session on this daemon with its short id,
        name, workspace, state, and whether a turn is owed. Read-only.
  peek <session-or-member> [--json]
        observe a session without interrupting it: state, todos, last
        assistant message, and the rendered screen. Passive — the observed
        agent never notices. The target is a crew name, full session id, or
        unique session id prefix. A sleeping crew member stays asleep.
  msg <session-or-member-or-seed> "text" [--source-session <id>] [--json]
        send a session or crew member a message. The body stays in the mailbox;
        the recipient gets a generic inbox notification. A target that cannot take
        input safely keeps it queued. The result says queued, notified, or refused.
        A sleeping member wakes before the notification is placed. The sender defaults to this session
        (ATTN_SESSION_ID); pass --source-session when running outside one.
        A seed id reaches whoever is tending it.
        A message that starts with - goes after --, as: agent msg -- <target> "-text"
  close <session-or-seed> -m "reason" [--source-session <id>] [--json]
        close a session for good. A session may close itself and the sessions it
        dispatched; the chief of staff may close any. The reason is required: the
        session row stays in the ledger, and the reason is what the next reader
        gets. It is immediate, so say what you have to say first. A seed id closes
        whoever tends it, and the seed keeps its tender with a note about the close.
        The caller defaults to this session (ATTN_SESSION_ID).
  inbox [message-id] [--limit <count>] [--session <id>] [--json]
        read up to 20 unread notifications in FIFO order, or one notified peer
        message by id. Each returned item gets its durable read receipt. The batch
        limit can be 1 through 50. The session defaults to ATTN_SESSION_ID.
  msg-status <message-id> [--session <id>] [--json]
        inspect your sent message as queued, notified, or read. The sender
        session defaults to ATTN_SESSION_ID.
`)
}
