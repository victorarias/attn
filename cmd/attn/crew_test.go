package main

import (
	"bytes"
	"encoding/json"
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
	for _, want := range []string{"Keel", "asleep", "Trellis", "awake", "sess-abc", "/home/.attn/crew/keel"} {
		if !strings.Contains(text, want) {
			t.Errorf("crew list output is missing %q:\n%s", want, text)
		}
	}
	// The session column is the short id `attn agent peek` takes, not the full one.
	if strings.Contains(text, "sess-abcdef123456") {
		t.Errorf("crew list printed a full session id:\n%s", text)
	}
}

// The MEMBER column is a name, and the home beside it is the lowercase path the
// id owns — the two never drift into one another.
func TestPrintCrewList_WritesTheMemberAsAName(t *testing.T) {
	var out bytes.Buffer
	printCrewList(&out, []protocol.CrewMember{{ID: "trellis", HomeDir: "/home/.attn/crew/trellis"}})
	text := out.String()
	if !strings.Contains(text, "Trellis") {
		t.Errorf("the MEMBER column is not written as a name:\n%s", text)
	}
	if !strings.Contains(text, "/home/.attn/crew/trellis") {
		t.Errorf("the home path was rewritten:\n%s", text)
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
	// The column is always on the wire, empty rather than absent: a reader of
	// the address book must not have to guard for a missing key.
	encoded, err := json.Marshal(rows[1])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	if !strings.Contains(string(encoded), `"member"`) {
		t.Errorf("an unbound row drops the member key: %s", encoded)
	}

	var out bytes.Buffer
	printAgentList(&out, rows)
	text := out.String()
	if !strings.Contains(text, "MEMBER") || !strings.Contains(text, "Trellis") {
		t.Errorf("agent list does not show the member column:\n%s", text)
	}
	// An unbound session's cell is the same placeholder every empty cell uses,
	// not a blank that shifts the columns.
	if strings.Contains(text, "beta") && !strings.Contains(text, "-") {
		t.Errorf("an unbound row has no placeholder:\n%s", text)
	}
}

func TestParseCrewWakeArgs(t *testing.T) {
	parsed, err := parseCrewWakeArgs([]string{"trellis", "--agent", "codex", "--json"})
	if err != nil {
		t.Fatalf("parseCrewWakeArgs: %v", err)
	}
	if parsed.member != "trellis" || parsed.agent != "codex" || !parsed.json {
		t.Fatalf("parsed = %+v, want trellis on codex as JSON", parsed)
	}
	// The harness is optional: a wake with no --agent takes the daemon's default.
	if parsed, err := parseCrewWakeArgs([]string{"keel"}); err != nil || parsed.agent != "" {
		t.Fatalf("parseCrewWakeArgs(keel) = %+v, %v", parsed, err)
	}
	for _, args := range [][]string{{}, {"one", "two"}, {"--nope", "keel"}} {
		if _, err := parseCrewWakeArgs(args); err == nil {
			t.Errorf("parseCrewWakeArgs(%v) accepted what it should refuse", args)
		}
	}
}

func TestParseCrewSleepArgs(t *testing.T) {
	parsed, err := parseCrewSleepArgs([]string{"trellis", "--json"})
	if err != nil {
		t.Fatalf("parseCrewSleepArgs: %v", err)
	}
	if parsed.member != "trellis" || !parsed.json {
		t.Fatalf("parsed = %+v, want trellis as JSON", parsed)
	}
	for _, args := range [][]string{{}, {"one", "two"}, {"--nope", "keel"}} {
		if _, err := parseCrewSleepArgs(args); err == nil {
			t.Errorf("parseCrewSleepArgs(%v) accepted what it should refuse", args)
		}
	}
}

func TestCrewWakeRepairLine_NamesTheExitedSession(t *testing.T) {
	result := &protocol.CrewWakeResult{ReleasedSessionID: protocol.Ptr("sess-abcdef123456")}
	line := crewWakeRepairLine(result)
	for _, want := range []string{"sess-abc", "had exited", "binding was released"} {
		if !strings.Contains(line, want) {
			t.Errorf("repair line %q is missing %q", line, want)
		}
	}
	if got := crewWakeRepairLine(&protocol.CrewWakeResult{}); got != "" {
		t.Fatalf("ordinary wake invented repair text %q", got)
	}
}

func TestCrewSleepOutcomeLine_NamesDeliveredQueuedAndAlreadyAsleep(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result protocol.CrewSleepResult
		want   []string
	}{
		{
			name: "delivered",
			result: protocol.CrewSleepResult{
				Member: "trellis", SessionID: protocol.Ptr("sess-abcdef123456"),
				DeliveryStatus: protocol.Ptr(protocol.AgentMsgStatusDelivered),
			},
			want: []string{"Asked Trellis", "sess-abc", "attn handoff --sleep"},
		},
		{
			name: "queued",
			result: protocol.CrewSleepResult{
				Member: "keel", SessionID: protocol.Ptr("sess-fedcba654321"),
				DeliveryStatus: protocol.Ptr(protocol.AgentMsgStatusQueued), Detail: "target is waiting on an approval",
			},
			want: []string{"Sleep request for Keel is queued", "sess-fed", "waiting on an approval"},
		},
		{
			name:   "already asleep",
			result: protocol.CrewSleepResult{Member: "keel", AlreadyAsleep: true, Detail: "Keel is already asleep; no sleep request was sent"},
			want:   []string{"Keel is already asleep", "no sleep request was sent"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			line := crewSleepOutcomeLine(&tt.result)
			for _, want := range tt.want {
				if !strings.Contains(line, want) {
					t.Errorf("outcome %q is missing %q", line, want)
				}
			}
		})
	}
}

// --awareness-dir repeats and replaces the whole list; passing it once empty is
// the way out of every dir a member has.
func TestCrewDirList_RepeatsAndClears(t *testing.T) {
	var dirs crewDirList
	for _, value := range []string{"/a", "/b"} {
		if err := dirs.Set(value); err != nil {
			t.Fatalf("Set(%q): %v", value, err)
		}
	}
	if len(dirs.values) != 2 || !dirs.set {
		t.Fatalf("dirs = %+v, want both recorded", dirs)
	}

	var cleared crewDirList
	if err := cleared.Set(""); err != nil {
		t.Fatalf("Set(\"\"): %v", err)
	}
	if !cleared.set || len(cleared.values) != 0 {
		t.Fatalf("an empty value = %+v, want the flag seen with no dirs", cleared)
	}
}
