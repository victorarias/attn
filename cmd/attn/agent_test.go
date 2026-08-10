package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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

func TestAgentPeekErrorMessagesNameTheTarget(t *testing.T) {
	notFound := agentPeekErrorMessage("abc", errors.New("daemon error: session_not_found"))
	if !strings.Contains(notFound, `"abc"`) || !strings.Contains(notFound, "attn agent list") {
		t.Fatalf("not-found message = %q", notFound)
	}
	ambiguous := agentPeekErrorMessage("a", errors.New("daemon error: ambiguous_session"))
	if !strings.Contains(ambiguous, "more than one session") {
		t.Fatalf("ambiguous message = %q", ambiguous)
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
			// The escape hatch for a human at a plain shell: say who is speaking.
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

// The sender reads one line and has to know what happened. Queued and refused
// both carry the daemon's reason, because both mean "not delivered yet" and the
// next move differs.
func TestAgentMsgOutcomeLineCarriesTheDaemonsReason(t *testing.T) {
	delivered := agentMsgOutcomeLine(&protocol.AgentMsgResult{
		Status: protocol.AgentMsgStatusDelivered, Detail: "delivered to reviewer", MessageID: "abc",
	})
	if !strings.Contains(delivered, "delivered to reviewer") || !strings.Contains(delivered, "abc") {
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

// A send names two sessions, so a failure has to say which one it could not
// find — otherwise the sender re-checks the wrong id.
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
}

// A message far past the limit never reaches the daemon's refusal: the socket
// hangs up mid-write and the sender sees a broken pipe. The command answers
// before it sends, so the size is always named.
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
