package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestParseCrewListArgs(t *testing.T) {
	parsed, err := parseCrewListArgs([]string{"--json"})
	if err != nil || !parsed.json {
		t.Fatalf("parseCrewListArgs(--json) = %+v, %v", parsed, err)
	}
	for _, args := range [][]string{{"extra"}, {"--nope"}} {
		if _, err := parseCrewListArgs(args); err == nil {
			t.Errorf("parseCrewListArgs(%v) accepted what it should refuse", args)
		}
	}
}

// The roster shows every member, awake or asleep — a sleeping member is one
// action from waking, never something to go find.
func TestPrintCrewList_ShowsSleepingAndAwakeMembers(t *testing.T) {
	var out bytes.Buffer
	printCrewList(&out, []protocol.CrewMember{
		{ID: "keel", HomeDir: "/home/.attn/crew/keel"},
		{ID: "trellis", HomeDir: "/home/.attn/crew/trellis", BindingSession: protocol.Ptr("sess-abcdef123456")},
	})
	text := out.String()
	for _, want := range []string{"keel", "asleep", "trellis", "awake", "sess-abc", "/home/.attn/crew/keel"} {
		if !strings.Contains(text, want) {
			t.Errorf("crew list output is missing %q:\n%s", want, text)
		}
	}
	// The session column is the short id `attn agent peek` takes, not the full one.
	if strings.Contains(text, "sess-abcdef123456") {
		t.Errorf("crew list printed a full session id:\n%s", text)
	}
}

// An empty roster says how a member joins it, rather than leaving the reader
// with nothing to act on.
func TestPrintCrewList_EmptyRosterSaysHowToJoinIt(t *testing.T) {
	var out bytes.Buffer
	printCrewList(&out, nil)
	if !strings.Contains(out.String(), "CHARTER.md") {
		t.Errorf("an empty roster does not say what makes a home:\n%s", out.String())
	}
}

// `agent list` is the address book, and a bound session's row says who it is
// today.
func TestAgentListRows_CarryTheCrewMember(t *testing.T) {
	rows := agentListRows(&client.ListResult{
		Sessions: []protocol.Session{
			{ID: "aaaa1111", Label: "alpha", Agent: "claude", WorkspaceID: "ws-1", State: "idle", CrewMember: protocol.Ptr("trellis")},
			{ID: "bbbb2222", Label: "beta", Agent: "codex", WorkspaceID: "ws-1", State: "idle"},
		},
		Workspaces: []protocol.Workspace{{ID: "ws-1", Title: "attn"}},
	})
	if rows[0].Member != "trellis" {
		t.Errorf("bound row member = %q, want trellis", rows[0].Member)
	}
	if rows[1].Member != "" {
		t.Errorf("unbound row member = %q, want empty", rows[1].Member)
	}

	var out bytes.Buffer
	printAgentList(&out, rows)
	text := out.String()
	if !strings.Contains(text, "MEMBER") || !strings.Contains(text, "trellis") {
		t.Errorf("agent list does not show the member column:\n%s", text)
	}
	// An unbound session's cell is the same placeholder every empty cell uses,
	// not a blank that shifts the columns.
	if strings.Contains(text, "beta") && !strings.Contains(text, "-") {
		t.Errorf("an unbound row has no placeholder:\n%s", text)
	}
}
