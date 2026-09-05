package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestParseAgentPeekArgs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    agentPeekArgs
		wantErr bool
	}{
		{name: "target only", args: []string{"abc"}, want: agentPeekArgs{target: "abc"}},
		{name: "json", args: []string{"abc", "--json"}, want: agentPeekArgs{target: "abc", json: true}},
		{name: "no target", args: nil, wantErr: true},
		{name: "flag first", args: []string{"--json", "abc"}, wantErr: true},
		{name: "two targets", args: []string{"abc", "def"}, wantErr: true},
		{name: "unknown flag", args: []string{"abc", "--nope"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAgentPeekArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAgentListRowsJoinWorkspaceTitlesAndSort(t *testing.T) {
	rows := agentListRows(&client.ListResult{
		Sessions: []protocol.Session{
			{ID: "bbbb2222-1111", Label: "zeta", Agent: "claude", WorkspaceID: "ws-2", State: "idle"},
			{ID: "aaaa1111-2222", Label: "alpha", Agent: "codex", WorkspaceID: "ws-1", State: "working", TurnOwed: protocol.Ptr(true)},
		},
		Workspaces: []protocol.Workspace{
			{ID: "ws-1", Title: "attn"},
			{ID: "ws-2", Title: "notes"},
		},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Workspace != "attn" || rows[0].Label != "alpha" || !rows[0].TurnOwed {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].Workspace != "notes" || rows[1].TurnOwed {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestPrintAgentListShowsShortIDsAndTurn(t *testing.T) {
	var out bytes.Buffer
	printAgentList(&out, []agentListRow{
		{ID: "aaaa1111-2222-3333", Label: "alpha", Agent: "codex", Workspace: "attn", State: "working", TurnOwed: true},
		{ID: "bbbb2222-1111-4444", Label: "zeta", Agent: "claude", Workspace: "notes", State: "idle"},
	})
	text := out.String()
	for _, want := range []string{"aaaa1111", "bbbb2222", "alpha", "working", "owed", "attn agent peek"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "aaaa1111-2222") {
		t.Fatalf("output leaked a full id where the short id belongs:\n%s", text)
	}
}

func TestPrintAgentListEmpty(t *testing.T) {
	var out bytes.Buffer
	printAgentList(&out, nil)
	if !strings.Contains(out.String(), "No sessions") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPrintAgentPeekShowsEverySection(t *testing.T) {
	var out bytes.Buffer
	printAgentPeek(&out, &protocol.AgentPeekResult{
		SessionID:            "aaaa1111-2222",
		Label:                "builder",
		Agent:                "claude",
		WorkspaceID:          "ws-1",
		WorkspaceTitle:       protocol.Ptr("attn"),
		State:                "working",
		StateReason:          protocol.Ptr("classifier_verdict"),
		StateSince:           "2026-08-10T10:00:00Z",
		LastSeen:             "2026-08-10T10:05:00Z",
		TurnOwed:             protocol.Ptr(true),
		Todos:                []string{"[✓] read the plan", "[→] build peek"},
		LastAssistantMessage: protocol.Ptr("working on it\nsecond line"),
		Screen:               &protocol.AgentPeekScreen{Text: "$ go test\nok\n", Cols: 80, Rows: 24},
	})
	text := out.String()
	for _, want := range []string{
		"session aaaa1111-2222 (claude) — builder",
		"workspace: attn",
		"state: working (classifier_verdict)",
		"turn: owed",
		"[→] build peek",
		"working on it",
		"  second line",
		"screen (80x24):",
		"$ go test",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestPrintAgentPeekDegradesWithoutOptionalSections(t *testing.T) {
	var out bytes.Buffer
	printAgentPeek(&out, &protocol.AgentPeekResult{
		SessionID:  "bbbb2222",
		Label:      "quiet",
		Agent:      "codex",
		State:      "idle",
		StateSince: "2026-08-10T10:00:00Z",
		LastSeen:   "2026-08-10T10:00:00Z",
		Todos:      []string{},
	})
	text := out.String()
	if !strings.Contains(text, "screen unavailable") {
		t.Fatalf("output missing the screen degrade line:\n%s", text)
	}
	for _, unwanted := range []string{"todos:", "last assistant message:", "turn:"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("output has %q for an empty section:\n%s", unwanted, text)
		}
	}
}

func TestPrintAgentPeekShowsTheScreenKeptAtExit(t *testing.T) {
	var out bytes.Buffer
	printAgentPeek(&out, &protocol.AgentPeekResult{
		SessionID:   "cccc3333",
		Label:       "dead",
		Agent:       "pi",
		State:       "idle",
		StateReason: protocol.Ptr("process_exited"),
		StateSince:  "2026-08-10T10:00:00Z",
		LastSeen:    "2026-08-10T10:00:00Z",
		Todos:       []string{},
		Exit:        &protocol.AgentPeekExit{Code: 1, At: "2026-08-10T10:00:01Z"},
		Screen:      &protocol.AgentPeekScreen{Text: "Error: Model \"x\" is ambiguous\n", Cols: 80, Rows: 24},
	})
	text := out.String()
	for _, want := range []string{
		"process exited with code 1 at ",
		"screen at exit (80x24):",
		"  Error: Model \"x\" is ambiguous",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "screen unavailable") {
		t.Fatalf("output degrades although an exit screen was kept:\n%s", text)
	}
}

func TestAgentPeekErrorMessagesNameTheTarget(t *testing.T) {
	notFound := agentPeekErrorMessage("abc", errors.New("daemon error: session_not_found"))
	if !strings.Contains(notFound, `"abc"`) || !strings.Contains(notFound, "attn crew list") {
		t.Fatalf("not-found message = %q", notFound)
	}
	ambiguous := agentPeekErrorMessage("a", errors.New("daemon error: ambiguous_session"))
	if !strings.Contains(ambiguous, "more than one session") {
		t.Fatalf("ambiguous message = %q", ambiguous)
	}
	asleep := agentPeekErrorMessage("Goalie", errors.New("daemon error: crew_member_asleep"))
	for _, want := range []string{"Goalie is asleep", "never wakes", "attn crew wake goalie"} {
		if !strings.Contains(asleep, want) {
			t.Fatalf("asleep message %q is missing %q", asleep, want)
		}
	}
	other := agentPeekErrorMessage("abc", errors.New("dial unix: no daemon"))
	if !strings.Contains(other, "no daemon") {
		t.Fatalf("other message = %q", other)
	}
}

func TestParseAgentMsgArgsResolvesTheSenderAndRefusesWhenItCannot(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		want    agentMsgArgs
		wantErr string
	}{
		{
			name: "sender defaults to this session",
			args: []string{"9f2a", "the rebase is done"},
			env:  "9f2a1111-2222-3333-4444-555566667777",
			want: agentMsgArgs{target: "9f2a", content: "the rebase is done", source: "9f2a1111-2222-3333-4444-555566667777"},
		},
		{
			name: "an explicit sender wins over the environment",
			args: []string{"9f2a", "hello", "--source-session", "aa11"},
			env:  "bb22",
			want: agentMsgArgs{target: "9f2a", content: "hello", source: "aa11"},
		},
		{
			name: "--json is carried through",
			args: []string{"9f2a", "hello", "--json"},
			env:  "bb22",
			want: agentMsgArgs{target: "9f2a", content: "hello", source: "bb22", json: true},
		},
		{
			name:    "outside a session with no sender",
			args:    []string{"9f2a", "hello"},
			env:     "",
			wantErr: "--source-session",
		},
		{
			name:    "an unquoted message",
			args:    []string{"9f2a", "the", "rebase", "is", "done"},
			env:     "bb22",
			wantErr: "quote it",
		},
		{
			name:    "no message at all",
			args:    []string{"9f2a"},
			env:     "bb22",
			wantErr: "usage:",
		},
		{
			name:    "a blank message",
			args:    []string{"9f2a", "   "},
			env:     "bb22",
			wantErr: "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentMsgArgs(tt.args, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parsed = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAgentMsgOutcomeLineCarriesTheDaemonsReason(t *testing.T) {
	delivered := agentMsgOutcomeLine(&protocol.AgentMsgResult{
		Status: protocol.AgentMsgStatusNotified, Detail: "notified reviewer", MessageID: "abc",
	})
	if !strings.Contains(delivered, "notified reviewer") || !strings.Contains(delivered, "abc") {
		t.Fatalf("delivered line = %q", delivered)
	}
	refused := agentMsgOutcomeLine(&protocol.AgentMsgResult{
		Status: protocol.AgentMsgStatusRefused, Detail: "you already sent that exact text",
	})
	if !strings.Contains(refused, "refused") || !strings.Contains(refused, "already sent") {
		t.Fatalf("refused line = %q", refused)
	}
	if strings.Contains(refused, "(id ") {
		t.Fatalf("a refusal has no message id to print: %q", refused)
	}
}

func TestAgentMsgErrorMessageSeparatesTargetFromSender(t *testing.T) {
	parsed := agentMsgArgs{target: "zzzz", source: "yyyy"}
	target := agentMsgErrorMessage(parsed, errors.New("daemon error: session_not_found"))
	if !strings.Contains(target, `"zzzz"`) || strings.Contains(target, "yyyy") {
		t.Fatalf("target error = %q", target)
	}
	sender := agentMsgErrorMessage(parsed, errors.New("daemon error: sender_session_not_found"))
	if !strings.Contains(sender, `"yyyy"`) || !strings.Contains(sender, "sender") {
		t.Fatalf("sender error = %q", sender)
	}
	unknown := agentMsgErrorMessage(parsed, errors.New("daemon error: session_or_crew_member_not_found"))
	for _, want := range []string{`"zzzz"`, "attn agent list", "attn crew list"} {
		if !strings.Contains(unknown, want) {
			t.Fatalf("unknown-address error %q does not name %q", unknown, want)
		}
	}
}

func TestParseAgentMsgStatusArgsResolvesIdentity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		env     string
		want    agentMailboxArgs
		wantErr string
	}{
		{
			name: "session defaults to this session", args: []string{"message-id"}, env: "session-id",
			want: agentMailboxArgs{messageID: "message-id", sessionID: "session-id"},
		},
		{
			name: "explicit session and JSON", args: []string{"message-id", "--session", "other", "--json"}, env: "session-id",
			want: agentMailboxArgs{messageID: "message-id", sessionID: "other", json: true},
		},
		{name: "no message", env: "session-id", wantErr: "usage:"},
		{name: "flag first", args: []string{"--json", "message-id"}, env: "session-id", wantErr: "usage:"},
		{name: "no identity", args: []string{"message-id"}, wantErr: "--session"},
		{name: "extra argument", args: []string{"message-id", "other"}, env: "session-id", wantErr: "usage:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentMailboxArgs("msg-status", tt.args, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parsed = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestParseAgentInboxArgsSupportsBatchAndLegacyReads(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		env     string
		want    agentInboxArgs
		wantErr string
	}{
		{
			name: "batch defaults", env: "session-id",
			want: agentInboxArgs{sessionID: "session-id", limit: agentmailbox.DefaultInboxLimit},
		},
		{
			name: "bounded batch", args: []string{"--limit", "7", "--session", "other", "--json"}, env: "session-id",
			want: agentInboxArgs{sessionID: "other", limit: 7, json: true},
		},
		{
			name: "legacy message", args: []string{"message-id"}, env: "session-id",
			want: agentInboxArgs{messageID: "message-id", sessionID: "session-id", limit: agentmailbox.DefaultInboxLimit},
		},
		{
			name: "flags before legacy message", args: []string{"--json", "message-id"}, env: "session-id",
			want: agentInboxArgs{messageID: "message-id", sessionID: "session-id", limit: agentmailbox.DefaultInboxLimit, json: true},
		},
		{name: "no identity", wantErr: "--session"},
		{name: "zero limit", args: []string{"--limit", "0"}, env: "session-id", wantErr: "between 1 and 50"},
		{name: "over limit", args: []string{"--limit", "51"}, env: "session-id", wantErr: "between 1 and 50"},
		{name: "limit with legacy message", args: []string{"message-id", "--limit", "1"}, env: "session-id", wantErr: "cannot be used"},
		{name: "empty message id", args: []string{""}, env: "session-id", wantErr: "usage:"},
		{name: "extra argument", args: []string{"one", "two"}, env: "session-id", wantErr: "usage:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentInboxArgs(tt.args, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parsed = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestPrintAgentInboxKeepsTheBodyBehindTheSecurityBoundary(t *testing.T) {
	var out bytes.Buffer
	printAgentInbox(&out, &protocol.AgentPeerMessage{
		MessageID: "message-id", SenderSessionID: "sender-session-id", SenderLabel: "reviewer",
		TargetSessionID: "target-session-id", Content: "the migration landed", State: protocol.AgentMessageStateRead,
	})
	text := out.String()
	for _, want := range []string{
		"sender-s (reviewer)", "the migration landed", "another agent, not from your user",
		`attn agent msg sender-s "..."`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestPrintAgentInboxBatchKeepsFIFOContentAndPeerBoundary(t *testing.T) {
	var out bytes.Buffer
	printAgentInboxBatch(&out, &protocol.AgentInboxBatchResult{
		Items: []protocol.AgentInboxItem{
			{
				ItemID: "garden-item", Kind: string(agentmailbox.KindGardenSeed),
				Content:   "Garden seed s-first moved: harvested",
				CreatedAt: "2026-09-03T10:00:00Z", NotifiedAt: "2026-09-03T10:00:02Z", ReadAt: "2026-09-03T10:00:03Z",
			},
			{
				ItemID: "ticket-item", Kind: string(agentmailbox.KindMaintenancePrompt),
				Content:   "📋 Read ticket activity with `attn ticket inbox`.",
				CreatedAt: "2026-09-03T10:00:00.500000000Z", NotifiedAt: "2026-09-03T10:00:02Z", ReadAt: "2026-09-03T10:00:03Z",
			},
			{
				ItemID: "peer-item", Kind: string(agentmailbox.KindPeerMessage), Content: "peer body",
				SenderSessionID: protocol.Ptr("sender-session-id"), SenderLabel: protocol.Ptr("reviewer"),
				CreatedAt: "2026-09-03T10:00:01Z", NotifiedAt: "2026-09-03T10:00:02Z", ReadAt: "2026-09-03T10:00:03Z",
			},
		},
		Remaining: 4,
	})
	text := out.String()
	gardenAt := strings.Index(text, "Garden seed s-first moved")
	ticketAt := strings.Index(text, "Read ticket activity")
	peerAt := strings.Index(text, "peer body")
	if gardenAt < 0 || ticketAt < gardenAt || peerAt < ticketAt {
		t.Fatalf("batch lost FIFO order:\n%s", text)
	}
	if strings.Contains(text, "🔔 📋") {
		t.Fatalf("maintenance content gained a second icon:\n%s", text)
	}
	for _, want := range []string{
		"sender-s (reviewer)", "another agent, not from your user",
		`attn agent msg sender-s "..."`, "4 more unread",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestPrintAgentInboxBatchNamesAnEmptyInbox(t *testing.T) {
	var out bytes.Buffer
	printAgentInboxBatch(&out, &protocol.AgentInboxBatchResult{})
	if got := out.String(); got != "inbox empty\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestPrintAgentMsgStatusShowsTheDurableState(t *testing.T) {
	var out bytes.Buffer
	printAgentMsgStatus(&out, &protocol.AgentPeerMessage{
		MessageID: "message-id", TargetSessionID: "target-session-id", State: protocol.AgentMessageStateNotified,
	})
	if got := out.String(); got != "notified: message message-id to session target-s\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestAgentMailboxErrorMessagesDoNotLeakOtherSessionsMessages(t *testing.T) {
	parsed := agentMailboxArgs{messageID: "message-id", sessionID: "session-id"}
	for code, want := range map[string]string{
		"recipient_session_not_found": "session-id",
		"sender_ambiguous_session":    "more than one session",
		"message_not_found":           "not found for this session",
		"message_not_notified":        "still queued",
	} {
		err := &client.DaemonError{Code: code, Message: code}
		if got := agentMailboxErrorMessage(parsed, err); !strings.Contains(got, want) {
			t.Fatalf("%s message = %q, want %q", code, got, want)
		}
	}
}

// A message far past the limit never reaches the daemon refusal: the socket hangs
// up mid-write and the sender sees a broken pipe, so the command answers first.
func TestParseAgentMsgArgsNamesTheSizeLimitBeforeSending(t *testing.T) {
	_, err := parseAgentMsgArgs([]string{"target", strings.Repeat("x", protocol.AgentMessageMaxChars+1)}, "sender-session-id")
	if err == nil {
		t.Fatal("an oversize message was accepted")
	}
	if !strings.Contains(err.Error(), "32769") || !strings.Contains(err.Error(), "32768") {
		t.Fatalf("error names neither the ask nor the limit: %v", err)
	}

	if _, err := parseAgentMsgArgs([]string{"target", strings.Repeat("x", protocol.AgentMessageMaxChars)}, "sender-session-id"); err != nil {
		t.Fatalf("a message exactly at the limit was refused: %v", err)
	}
}

func TestParseAgentMsgArgsTakesADashLeadingMessageAfterTheSeparator(t *testing.T) {
	parsed, err := parseAgentMsgArgs([]string{"--", "target", "-hello", "--json"}, "sender-session-id")
	if err != nil {
		t.Fatalf("a quoted message starting with - was refused: %v", err)
	}
	if parsed.content != "-hello" || parsed.target != "target" || !parsed.json {
		t.Fatalf("parsed = %+v", parsed)
	}

	_, err = parseAgentMsgArgs([]string{"target", "-hello"}, "sender-session-id")
	if err == nil || !strings.Contains(err.Error(), "attn agent msg -- <id>") {
		t.Fatalf("the usage error does not name the way through: %v", err)
	}
}

func TestParseAgentCloseArgsRequiresATargetAReasonAndACaller(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		want    agentCloseArgs
		wantErr string
	}{
		{
			name: "the caller defaults to this session",
			args: []string{"9f2a", "-m", "the PR merged"},
			env:  "9f2a1111-2222-3333-4444-555566667777",
			want: agentCloseArgs{target: "9f2a", reason: "the PR merged", source: "9f2a1111-2222-3333-4444-555566667777"},
		},
		{
			name: "a seed id is a target like any other",
			args: []string{"s-7k3f9m", "-m", "its report landed", "--json"},
			env:  "bb22",
			want: agentCloseArgs{target: "s-7k3f9m", reason: "its report landed", source: "bb22", json: true},
		},
		{
			name: "an explicit caller wins over the environment",
			args: []string{"9f2a", "-m", "done", "--source-session", "aa11"},
			env:  "bb22",
			want: agentCloseArgs{target: "9f2a", reason: "done", source: "aa11"},
		},
		{
			name:    "no reason",
			args:    []string{"9f2a"},
			env:     "bb22",
			wantErr: "a close needs a reason",
		},
		{
			name:    "a blank reason",
			args:    []string{"9f2a", "-m", "   "},
			env:     "bb22",
			wantErr: "a close needs a reason",
		},
		{
			name:    "an unquoted reason",
			args:    []string{"9f2a", "-m", "the", "PR", "merged"},
			env:     "bb22",
			wantErr: "quote the reason",
		},
		{
			name:    "no target",
			args:    []string{"-m", "done"},
			env:     "bb22",
			wantErr: "usage:",
		},
		{
			name:    "outside a session with no caller",
			args:    []string{"9f2a", "-m", "done"},
			env:     "",
			wantErr: "--source-session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentCloseArgs(tt.args, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseAgentCloseArgs() error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgentCloseArgs() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseAgentCloseArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPrintAgentCloseSaysWhatWasClosedAndWhereItWent(t *testing.T) {
	var out strings.Builder
	printAgentClose(&out, &protocol.AgentCloseResult{
		TargetSessionID: "9f2a1111-2222-3333",
		Label:           "sweep child",
		Reason:          "reported back on the seed",
		Rule:            protocol.AgentCloseRuleDispatcher,
		SeedIds:         []string{"s-7k3f9m"},
	})
	text := out.String()
	for _, want := range []string{"9f2a1111", "sweep child", "reported back on the seed", "s-7k3f9m", "attn session show"} {
		if !strings.Contains(text, want) {
			t.Errorf("close output %q is missing %q", text, want)
		}
	}
}
