package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// fprintTicketShow must never truncate a comment body — that is the whole point
// of `ticket show` over the consuming `ticket inbox` read. This exercises a
// long multi-line comment plus a status-transition activity line and checks
// both render verbatim.
func TestFprintTicketShowFullBodyNoTruncation(t *testing.T) {
	longComment := strings.Repeat("this is a long verdict line. ", 20) + "\nsecond line\nthird line: the conclusion"
	from := protocol.TicketStatusWorking
	to := protocol.TicketStatusInReview
	ticket := &protocol.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: "Move to X",
		Status:      protocol.TicketStatusInReview,
		Assignee:    "sess-1",
		CreatedAt:   "2026-07-01T00:00:00Z",
		UpdatedAt:   "2026-07-02T00:00:00Z",
		Activity: []protocol.TicketActivity{
			{
				ID:         1,
				Kind:       protocol.TicketActivityKind("status_change"),
				Author:     "sess-1",
				CreatedAt:  "2026-07-01T00:01:00Z",
				FromStatus: &from,
				ToStatus:   &to,
			},
			{
				ID:        2,
				Kind:      protocol.TicketActivityKind("comment"),
				Author:    "chief-1",
				CreatedAt: "2026-07-01T00:02:00Z",
				Comment:   &longComment,
			},
		},
		Artifacts: []protocol.TicketArtifact{
			{Filename: "report.md", Path: "/repo/report.md", NotebookPath: "tickets/store-migration/report.md"},
		},
	}

	var buf bytes.Buffer
	fprintTicketShow(&buf, ticket)
	out := buf.String()

	if !strings.Contains(out, longComment) {
		t.Fatalf("comment body was not rendered verbatim; out:\n%s", out)
	}
	if !strings.Contains(out, "working → in_review") {
		t.Fatalf("status transition not rendered; out:\n%s", out)
	}
	if !strings.Contains(out, "Move to X") {
		t.Fatalf("description not rendered; out:\n%s", out)
	}
	if !strings.Contains(out, "report.md") {
		t.Fatalf("artifact not rendered; out:\n%s", out)
	}
}

// An empty-activity ticket (freshly created, unbound) should render sanely
// with no artifact/activity noise instead of blowing up.
func TestFprintTicketShowEmptyActivity(t *testing.T) {
	ticket := &protocol.Ticket{
		ID:        "fresh-ticket",
		Title:     "A fresh backlog item",
		Status:    protocol.TicketStatusTodo,
		CreatedAt: "2026-07-01T00:00:00Z",
		UpdatedAt: "2026-07-01T00:00:00Z",
	}

	var buf bytes.Buffer
	fprintTicketShow(&buf, ticket)
	out := buf.String()

	if !strings.Contains(out, "no activity") {
		t.Fatalf("expected 'no activity' for empty thread; out:\n%s", out)
	}
	if !strings.Contains(out, "fresh-ticket") || !strings.Contains(out, "A fresh backlog item") {
		t.Fatalf("expected header with id/title; out:\n%s", out)
	}
	if strings.Contains(out, "artifacts:") {
		t.Fatalf("should not print artifacts section when there are none; out:\n%s", out)
	}
}
