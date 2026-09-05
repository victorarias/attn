package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseSessionListArgs(t *testing.T) {
	parsed, err := parseSessionListArgs([]string{"--closed", "--limit", "5", "--before", "sess-9"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.closed || parsed.limit != 5 || parsed.before != "sess-9" {
		t.Fatalf("parsed = %+v", parsed)
	}

	bad := [][]string{
		{"--closed", "--all"},
		{"--limit", "-1"},
		{"stray-positional"},
	}
	for _, args := range bad {
		if _, err := parseSessionListArgs(args); err == nil {
			t.Errorf("parseSessionListArgs(%v) accepted arguments it should refuse", args)
		}
	}
}

func TestSessionListPrintsThePaginationNotice(t *testing.T) {
	result := &protocol.SessionListResult{
		Entries: []protocol.SessionLedgerEntry{
			{
				ID: "sess-2", Label: "second", Agent: "claude", Directory: "/tmp/two",
				State: protocol.SessionStateIdle, LastSeen: "2026-09-05T12:00:00Z",
				ClosedAt: protocol.Ptr("2026-09-05T12:30:00Z"), ClosedBy: protocol.Ptr("user"),
			},
			{
				ID: "sess-1", Label: "first", Agent: "claude", Directory: "/tmp/one",
				State: protocol.SessionStateIdle, LastSeen: "2026-09-05T11:00:00Z",
				ClosedAt: protocol.Ptr("2026-09-05T11:30:00Z"), ClosedBy: protocol.Ptr("sess-boss"),
				CloseReason: protocol.Ptr("brief delivered"),
			},
		},
		Omitted:    137,
		NextBefore: protocol.Ptr("sess-1"),
	}

	var buf bytes.Buffer
	fprintSessionList(&buf, result, sessionListArgs{closed: true})
	out := buf.String()

	if !strings.Contains(out, "showing 2, 137 omitted, paginate with --before sess-1") {
		t.Errorf("output has no pagination notice naming the next cursor:\n%s", out)
	}
	if !strings.Contains(out, "sess-boss") {
		t.Errorf("output does not name who closed the session:\n%s", out)
	}
	if !strings.Contains(out, "brief delivered") {
		t.Errorf("output does not carry the close reason:\n%s", out)
	}
	if strings.Count(out, "closed") < 2 {
		t.Errorf("closed rows must read as closed rather than as their last live state:\n%s", out)
	}
}

func TestSessionListSaysWhereToLookWhenEmpty(t *testing.T) {
	cases := map[string]sessionListArgs{
		"--closed":  {closed: true},
		"live only": {},
		"paged":     {before: "sess-1"},
	}
	for name, args := range cases {
		var buf bytes.Buffer
		fprintSessionList(&buf, &protocol.SessionListResult{Entries: nil}, args)
		if strings.TrimSpace(buf.String()) == "" {
			t.Errorf("%s printed nothing for an empty page", name)
		}
	}
}

func TestSessionShowRendersTheCloseAndItsReason(t *testing.T) {
	var buf bytes.Buffer
	fprintSessionShow(&buf, protocol.SessionShowResult{
		Entry: protocol.SessionLedgerEntry{
			ID: "sess-1", Label: "ledger", Agent: "claude", Directory: "/tmp/one",
			WorkspaceID: "ws-1", State: protocol.SessionStateIdle, LastSeen: "2026-09-05T11:00:00Z",
			Branch: protocol.Ptr("feat/x"), IsWorktree: protocol.Ptr(true), MainRepo: protocol.Ptr("/repo"),
			ClosedAt: protocol.Ptr("2026-09-05T11:30:00Z"), ClosedBy: protocol.Ptr("sess-boss"),
			CloseReason: protocol.Ptr("brief delivered"),
		},
		Reopen: &protocol.SessionReopen{
			Reopenable:     false,
			Reason:         protocol.Ptr("the directory /tmp/one no longer exists"),
			Actions:        []protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen},
			DirectoryState: "missing",
			BranchState:    protocol.Ptr("local"),
			WorkspaceID:    "workspace-sess-1",
			WorkspacePlan:  "create",
			PanePlan:       "add",
		},
	})
	out := buf.String()
	for _, want := range []string{
		"sess-1", "feat/x", "/repo", "sess-boss", "brief delivered", "closed",
		"the directory /tmp/one no longer exists",
		"attn session reopen sess-1 --action recreate_worktree_and_reopen",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("session show output is missing %q:\n%s", want, out)
		}
	}
}
