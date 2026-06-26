package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ticketBase is a fixed clock for deterministic, injected-time tests.
var ticketBase = time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)

func TestTicketCRUDRoundTrip(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	// Migration smoke: the ticket tables migration is part of the latest chain.
	var maxVersion int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if maxVersion != latestSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", maxVersion, latestSchemaVersion())
	}

	created, err := s.CreateTicket(Ticket{
		ID:          "store-migration",
		Title:       "Migrate store to X",
		Description: "Move the store onto the new backend.",
		Assignee:    "agent7",
		Cwd:         "/tmp/project",
		LastAgentID: "agent7",
	}, ticketBase)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.Status != TicketStatusTodo {
		t.Fatalf("default status = %q, want todo", created.Status)
	}
	if created.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v, want nil for a fresh todo", created.ClosedAt)
	}

	got, err := s.GetTicket("store-migration")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got == nil {
		t.Fatal("GetTicket = nil, want ticket")
	}
	if got.Title != "Migrate store to X" || got.Description != "Move the store onto the new backend." {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Assignee != "agent7" || got.Cwd != "/tmp/project" || got.LastAgentID != "agent7" {
		t.Fatalf("round-trip mismatch on session fields: %+v", got)
	}
	if !got.CreatedAt.Equal(ticketBase) || !got.UpdatedAt.Equal(ticketBase) {
		t.Fatalf("timestamps = created %v / updated %v, want %v", got.CreatedAt, got.UpdatedAt, ticketBase)
	}

	// Missing ticket reads as (nil, nil).
	missing, err := s.GetTicket("nope")
	if err != nil || missing != nil {
		t.Fatalf("GetTicket(missing) = %v, %v; want nil, nil", missing, err)
	}
}

func TestTicketValidation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "Has Spaces", Title: "x"}, ticketBase); !errors.Is(err, ErrInvalidTicketID) {
		t.Fatalf("invalid id err = %v, want ErrInvalidTicketID", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "no-title"}, ticketBase); !errors.Is(err, ErrTicketTitleRequired) {
		t.Fatalf("missing title err = %v, want ErrTicketTitleRequired", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "bad-status", Title: "x", Status: "weird"}, ticketBase); !errors.Is(err, ErrInvalidTicketStatus) {
		t.Fatalf("bad status err = %v, want ErrInvalidTicketStatus", err)
	}
}

func TestTicketIDCollision(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "dup", Title: "first"}, ticketBase); err != nil {
		t.Fatalf("first CreateTicket: %v", err)
	}
	_, err := s.CreateTicket(Ticket{ID: "dup", Title: "second"}, ticketBase)
	if !errors.Is(err, ErrTicketIDTaken) {
		t.Fatalf("collision err = %v, want ErrTicketIDTaken", err)
	}
	// The message guides the agent to a fix.
	if msg := err.Error(); !strings.Contains(msg, "pick a new name") || !strings.Contains(msg, "dup-2") {
		t.Fatalf("collision message lacks guidance: %q", msg)
	}
	// The original ticket is untouched.
	got, _ := s.GetTicket("dup")
	if got == nil || got.Title != "first" {
		t.Fatalf("original overwritten: %+v", got)
	}
}

func TestTicketStatusTransitions(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t1 := ticketBase.Add(1 * time.Minute)
	if _, err := s.SetTicketStatus("tk", TicketStatusWorking, "agent7", "picking it up", t1); err != nil {
		t.Fatalf("SetTicketStatus working: %v", err)
	}

	// Into a terminal status stamps closed_at.
	t2 := ticketBase.Add(2 * time.Minute)
	got, err := s.SetTicketStatus("tk", TicketStatusDone, "agent7", "shipped", t2)
	if err != nil {
		t.Fatalf("SetTicketStatus done: %v", err)
	}
	if got.ClosedAt == nil || !got.ClosedAt.Equal(t2) {
		t.Fatalf("ClosedAt = %v, want %v", got.ClosedAt, t2)
	}

	// Reopening clears closed_at — durable again.
	t3 := ticketBase.Add(3 * time.Minute)
	got, err = s.SetTicketStatus("tk", TicketStatusWorking, "you", "more to do", t3)
	if err != nil {
		t.Fatalf("SetTicketStatus reopen: %v", err)
	}
	if got.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v, want nil after reopen", got.ClosedAt)
	}

	// The activity thread captured every move, in order, with from/to + comment.
	full, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if len(full.Activity) != 3 {
		t.Fatalf("activity len = %d, want 3", len(full.Activity))
	}
	wantFrom := []TicketStatus{TicketStatusTodo, TicketStatusWorking, TicketStatusDone}
	wantTo := []TicketStatus{TicketStatusWorking, TicketStatusDone, TicketStatusWorking}
	for i, a := range full.Activity {
		if a.Kind != TicketActivityStatusChange {
			t.Fatalf("activity[%d].Kind = %q, want status_change", i, a.Kind)
		}
		if a.FromStatus != wantFrom[i] || a.ToStatus != wantTo[i] {
			t.Fatalf("activity[%d] = %s->%s, want %s->%s", i, a.FromStatus, a.ToStatus, wantFrom[i], wantTo[i])
		}
	}
	if full.Activity[1].Comment != "shipped" {
		t.Fatalf("done comment = %q, want shipped", full.Activity[1].Comment)
	}

	// Unknown ticket / invalid status are errors.
	if _, err := s.SetTicketStatus("ghost", TicketStatusDone, "", "", t3); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("status on missing = %v, want ErrTicketNotFound", err)
	}
	if _, err := s.SetTicketStatus("tk", "bogus", "", "", t3); !errors.Is(err, ErrInvalidTicketStatus) {
		t.Fatalf("bad status = %v, want ErrInvalidTicketStatus", err)
	}
}

func TestTicketCommentsAndEdits(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t1 := ticketBase.Add(1 * time.Minute)
	if _, err := s.AddTicketComment("tk", "you", "any update?", t1); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}
	t2 := ticketBase.Add(2 * time.Minute)
	if _, err := s.AddTicketComment("tk", "agent7", "almost there", t2); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}
	if err := s.EditTicketDescription("tk", "Revised brief.", t2); err != nil {
		t.Fatalf("EditTicketDescription: %v", err)
	}
	if err := s.AssignTicket("tk", "agent9", t2); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	if err := s.SetTicketSession("tk", "/repo", "agent9", t2); err != nil {
		t.Fatalf("SetTicketSession: %v", err)
	}

	got, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Description != "Revised brief." || got.Assignee != "agent9" || got.Cwd != "/repo" || got.LastAgentID != "agent9" {
		t.Fatalf("edits not applied: %+v", got)
	}
	if !got.UpdatedAt.Equal(t2) {
		t.Fatalf("UpdatedAt = %v, want %v (bumped by edits)", got.UpdatedAt, t2)
	}
	if len(got.Activity) != 2 {
		t.Fatalf("activity len = %d, want 2 comments", len(got.Activity))
	}
	if got.Activity[0].Kind != TicketActivityComment || got.Activity[0].Comment != "any update?" {
		t.Fatalf("activity[0] = %+v, want first comment", got.Activity[0])
	}
	if got.Activity[1].Author != "agent7" {
		t.Fatalf("activity[1].Author = %q, want agent7", got.Activity[1].Author)
	}

	// Edits / comments on a missing ticket fail rather than orphan.
	if err := s.EditTicketDescription("ghost", "x", t2); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("edit missing = %v, want ErrTicketNotFound", err)
	}
	if _, err := s.AddTicketComment("ghost", "", "x", t2); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("comment missing = %v, want ErrTicketNotFound", err)
	}
}

func TestTicketAttachments(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	t1 := ticketBase.Add(1 * time.Minute)
	att, err := s.AddTicketAttachment(TicketAttachment{
		TicketID: "tk",
		Filename: "results.json",
		Path:     "/handover/results.json",
		Note:     "benchmark output",
	}, t1)
	if err != nil {
		t.Fatalf("AddTicketAttachment: %v", err)
	}
	if att.ID == 0 {
		t.Fatal("attachment id = 0, want assigned id")
	}

	got, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(got.Attachments))
	}
	if got.Attachments[0].Filename != "results.json" || got.Attachments[0].Note != "benchmark output" {
		t.Fatalf("attachment round-trip = %+v", got.Attachments[0])
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("UpdatedAt = %v, want %v (bumped by attach)", got.UpdatedAt, t1)
	}

	// A filename is required; a missing ticket is rejected.
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "tk"}, t1); err == nil {
		t.Fatal("AddTicketAttachment with no filename: want error")
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "ghost", Filename: "x"}, t1); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("attach to missing = %v, want ErrTicketNotFound", err)
	}
}

func TestTicketListAndArchive(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	// Three tickets across columns.
	if _, err := s.CreateTicket(Ticket{ID: "backlog", Title: "later"}, ticketBase); err != nil {
		t.Fatalf("create backlog: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "active", Title: "now", Status: TicketStatusWorking}, ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "shipped", Title: "old", Status: TicketStatusDone}, ticketBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("create shipped: %v", err)
	}

	all, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list len = %d, want 3", len(all))
	}
	// Newest first.
	if all[0].ID != "shipped" || all[2].ID != "backlog" {
		t.Fatalf("ordering = %s..%s, want shipped..backlog", all[0].ID, all[2].ID)
	}

	// Status filter.
	working, err := s.ListTickets(TicketListFilter{Status: TicketStatusWorking})
	if err != nil {
		t.Fatalf("ListTickets(working): %v", err)
	}
	if len(working) != 1 || working[0].ID != "active" {
		t.Fatalf("working filter = %+v, want [active]", working)
	}

	// Archiving an open ticket is refused.
	if err := s.ArchiveTicket("active", ticketBase.Add(3*time.Minute)); !errors.Is(err, ErrTicketNotClosed) {
		t.Fatalf("archive open = %v, want ErrTicketNotClosed", err)
	}
	// Archiving a closed ticket clears it from the default board.
	if err := s.ArchiveTicket("shipped", ticketBase.Add(3*time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	board, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets after archive: %v", err)
	}
	if len(board) != 2 {
		t.Fatalf("board len = %d, want 2 (archived hidden)", len(board))
	}
	withArchived, err := s.ListTickets(TicketListFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListTickets(IncludeArchived): %v", err)
	}
	if len(withArchived) != 3 {
		t.Fatalf("IncludeArchived len = %d, want 3", len(withArchived))
	}
}

// Reopening an archived ticket un-archives it: a closed+archived ticket moved back
// to an open status must return to the default board and shed its archived_at, not
// linger as an invisible zombie immune to the TTL sweep.
func TestArchivedTicketReopenedBecomesVisible(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "zombie", Title: "shipped", Status: TicketStatusDone}, ticketBase); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.ArchiveTicket("zombie", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	// Archived: hidden from the default board.
	if board, err := s.ListTickets(TicketListFilter{}); err != nil || len(board) != 0 {
		t.Fatalf("board after archive = %d (err %v), want 0", len(board), err)
	}

	// Reopen it to an open status.
	reopened, err := s.SetTicketStatus("zombie", TicketStatusWorking, "you", "back to it", ticketBase.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	if reopened.ArchivedAt != nil {
		t.Fatalf("reopened ArchivedAt = %v, want nil (un-archived)", reopened.ArchivedAt)
	}
	if reopened.ClosedAt != nil {
		t.Fatalf("reopened ClosedAt = %v, want nil", reopened.ClosedAt)
	}

	// It is visible on the default board again.
	board, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(board) != 1 || board[0].ID != "zombie" {
		t.Fatalf("board after reopen = %+v, want [zombie]", board)
	}
	if board[0].ArchivedAt != nil {
		t.Fatalf("persisted ArchivedAt = %v, want nil", board[0].ArchivedAt)
	}
}

func TestTicketTTLSweep(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const ttl = 30 * 24 * time.Hour
	now := ticketBase

	// An open backlog ticket — never swept.
	if _, err := s.CreateTicket(Ticket{ID: "open", Title: "live"}, now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open: %v", err)
	}
	// A recently-closed ticket — within TTL, kept.
	if _, err := s.CreateTicket(Ticket{ID: "recent", Title: "recent", Status: TicketStatusDone}, now.Add(-5*24*time.Hour)); err != nil {
		t.Fatalf("create recent: %v", err)
	}
	// A long-closed ticket with activity + an attachment — swept, cascading.
	if _, err := s.CreateTicket(Ticket{ID: "stale", Title: "stale"}, now.Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("create stale: %v", err)
	}
	closedAt := now.Add(-60 * 24 * time.Hour)
	if _, err := s.SetTicketStatus("stale", TicketStatusDone, "agent7", "done long ago", closedAt); err != nil {
		t.Fatalf("close stale: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "stale", Filename: "old.txt"}, closedAt); err != nil {
		t.Fatalf("attach stale: %v", err)
	}

	removed, err := s.SweepExpiredTickets(now, ttl)
	if err != nil {
		t.Fatalf("SweepExpiredTickets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only stale)", removed)
	}

	if gone, _ := s.GetTicket("stale"); gone != nil {
		t.Fatal("stale ticket survived the sweep")
	}
	if kept, _ := s.GetTicket("recent"); kept == nil {
		t.Fatal("recent ticket was swept early")
	}
	if kept, _ := s.GetTicket("open"); kept == nil {
		t.Fatal("open backlog ticket was swept")
	}

	// Cascade: the swept ticket's activity + attachment rows are gone too.
	var orphanActivity, orphanAttachments int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ticket_activity WHERE ticket_id = 'stale'`).Scan(&orphanActivity); err != nil {
		t.Fatalf("count orphan activity: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ticket_attachments WHERE ticket_id = 'stale'`).Scan(&orphanAttachments); err != nil {
		t.Fatalf("count orphan attachments: %v", err)
	}
	if orphanActivity != 0 || orphanAttachments != 0 {
		t.Fatalf("cascade leak: %d activity, %d attachments left", orphanActivity, orphanAttachments)
	}
}

func TestTicketPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "persist", Title: "survives restart"}, ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("persist", TicketStatusInReview, "agent7", "ready", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, err := reopened.GetTicket("persist")
	if err != nil {
		t.Fatalf("GetTicket after reopen: %v", err)
	}
	if got == nil || got.Status != TicketStatusInReview {
		t.Fatalf("persisted ticket = %+v, want in_review", got)
	}
	if len(got.Activity) != 1 || got.Activity[0].ToStatus != TicketStatusInReview {
		t.Fatalf("persisted activity = %+v", got.Activity)
	}
}
