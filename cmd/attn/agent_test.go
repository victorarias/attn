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
