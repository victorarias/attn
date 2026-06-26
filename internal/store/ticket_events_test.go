package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var eventBase = time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)

// kindsOf counts events per kind for a ticket.
func kindsOf(t *testing.T, s *Store, ticketID string) map[TicketEventKind]int {
	t.Helper()
	events, err := s.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	counts := map[TicketEventKind]int{}
	for _, e := range events {
		if e.TicketID == ticketID {
			counts[e.Kind]++
		}
	}
	return counts
}

// Every mutator emits exactly one domain event, with the right kind / author /
// detail. This pins emission for all six kinds — three of which the notification
// harness never drives.
func TestTicketEventEmissionAllKinds(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	tick := eventBase
	next := func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "chief", next()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("tk", TicketStatusWorking, "agent7", "", next()); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	if _, err := s.AddTicketComment("tk", "agent7", "a note", next()); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}
	if err := s.AssignTicket("tk", "agent9", "chief", next()); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	if err := s.EditTicketDescription("tk", "new brief", "chief", next()); err != nil {
		t.Fatalf("EditTicketDescription: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "tk", Filename: "out.txt"}, "agent9", next()); err != nil {
		t.Fatalf("AddTicketAttachment: %v", err)
	}

	counts := kindsOf(t, s, "tk")
	for _, k := range []TicketEventKind{
		TicketEventCreated, TicketEventStatusChanged, TicketEventCommented,
		TicketEventAssigned, TicketEventDescriptionEdited, TicketEventAttachmentAdded,
	} {
		if counts[k] != 1 {
			t.Fatalf("event kind %q count = %d, want 1 (all: %+v)", k, counts[k], counts)
		}
	}

	// Detail is set on the kinds that carry a salient value.
	events, _ := s.TicketEventsSince(0)
	byKind := map[TicketEventKind]TicketEvent{}
	for _, e := range events {
		byKind[e.Kind] = e
	}
	if byKind[TicketEventAssigned].Detail != "agent9" {
		t.Fatalf("assigned Detail = %q, want agent9", byKind[TicketEventAssigned].Detail)
	}
	if byKind[TicketEventDescriptionEdited].Detail != "new brief" {
		t.Fatalf("description_edited Detail = %q, want the new brief", byKind[TicketEventDescriptionEdited].Detail)
	}
	if byKind[TicketEventAttachmentAdded].Detail != "out.txt" {
		t.Fatalf("attachment_added Detail = %q, want out.txt", byKind[TicketEventAttachmentAdded].Detail)
	}
}

// Dedup is "vs the ticket's PREVIOUS event" only. A re-brief with distinct text is
// kept (the slice-2 fix); an (A, B, A) sequence keeps all three; a true
// back-to-back repeat is dropped.
func TestTicketEventDedupSemantics(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	tick := eventBase
	next := func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "chief", next()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// Distinct re-briefs are all kept (regression guard for the dedup-drops-rebrief bug).
	for _, d := range []string{"brief one", "brief two", "brief three"} {
		if err := s.EditTicketDescription("tk", d, "chief", next()); err != nil {
			t.Fatalf("EditTicketDescription %q: %v", d, err)
		}
	}
	if got := kindsOf(t, s, "tk")[TicketEventDescriptionEdited]; got != 3 {
		t.Fatalf("description_edited events = %d, want 3 distinct re-briefs kept", got)
	}

	// (A, B, A) on comments — dedup is only vs the immediately-previous event, so
	// the second A is NOT deduped.
	for _, c := range []string{"A", "B", "A"} {
		if _, err := s.AddTicketComment("tk", "agent7", c, next()); err != nil {
			t.Fatalf("AddTicketComment %q: %v", c, err)
		}
	}
	if got := kindsOf(t, s, "tk")[TicketEventCommented]; got != 3 {
		t.Fatalf("comment events after A,B,A = %d, want 3", got)
	}

	// A true back-to-back repeat (another A right after A) IS deduped.
	if _, err := s.AddTicketComment("tk", "agent7", "A", next()); err != nil {
		t.Fatalf("AddTicketComment repeat: %v", err)
	}
	if got := kindsOf(t, s, "tk")[TicketEventCommented]; got != 3 {
		t.Fatalf("comment events after repeat A = %d, want still 3 (deduped)", got)
	}
}

// A cursor only ever moves forward. A stale or overlapping consume that writes a
// lower seq must not rewind it — otherwise already-consumed events resurface as
// unread (double-delivery). Writing 3 then a stale 2 must leave it at 3.
func TestTicketCursorMonotonic(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SetTicketCursor("agent7", "tk", 3, eventBase); err != nil {
		t.Fatalf("SetTicketCursor 3: %v", err)
	}
	// A stale writer tries to move it backwards.
	if err := s.SetTicketCursor("agent7", "tk", 2, eventBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketCursor 2: %v", err)
	}
	if got, _ := s.GetTicketCursor("agent7", "tk"); got != 3 {
		t.Fatalf("cursor after stale write = %d, want 3 (no rewind)", got)
	}
	// A genuine forward write still advances.
	if err := s.SetTicketCursor("agent7", "tk", 5, eventBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("SetTicketCursor 5: %v", err)
	}
	if got, _ := s.GetTicketCursor("agent7", "tk"); got != 5 {
		t.Fatalf("cursor after forward write = %d, want 5", got)
	}
	// Cursors are scoped per (identity, ticket) — another identity is independent.
	if got, _ := s.GetTicketCursor("agent9", "tk"); got != 0 {
		t.Fatalf("unrelated identity cursor = %d, want 0", got)
	}
}

// The event log and per-(identity, ticket) cursors survive a daemon restart.
func TestTicketEventCursorPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "chief", eventBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("tk", TicketStatusWorking, "agent7", "on it", eventBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	latest, err := s.LatestTicketEventSeq()
	if err != nil || latest == 0 {
		t.Fatalf("LatestTicketEventSeq = %d (err %v), want > 0", latest, err)
	}
	if err := s.SetTicketCursor("chief", "tk", latest, eventBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("SetTicketCursor: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	events, err := reopened.TicketEventsSince(0)
	if err != nil || len(events) != 2 {
		t.Fatalf("events after reopen = %d (err %v), want 2", len(events), err)
	}
	cursor, err := reopened.GetTicketCursor("chief", "tk")
	if err != nil || cursor != latest {
		t.Fatalf("cursor after reopen = %d (err %v), want %d", cursor, err, latest)
	}
	// AUTOINCREMENT seq keeps climbing after restart — no reuse.
	if _, err := reopened.AddTicketComment("tk", "agent7", "more", eventBase.Add(3*time.Minute)); err != nil {
		t.Fatalf("AddTicketComment after reopen: %v", err)
	}
	if next, _ := reopened.LatestTicketEventSeq(); next <= latest {
		t.Fatalf("seq after reopen = %d, want > %d (monotonic)", next, latest)
	}
}

// TicketParticipants is the inverse of involvement: the assignee plus everyone who
// authored an event on the ticket, deduped and sorted, with empties excluded.
func TestTicketParticipants(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	tick := eventBase
	next := func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work", Assignee: "agent7"}, "chief", next()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.AddTicketComment("tk", "agent9", "a note", next()); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}

	got, err := s.TicketParticipants("tk")
	if err != nil {
		t.Fatalf("TicketParticipants: %v", err)
	}
	// assignee agent7 + authors {chief, agent9}, deduped + sorted.
	want := []string{"agent7", "agent9", "chief"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("participants = %v, want %v", got, want)
	}

	if ids, err := s.TicketParticipants("missing"); err != nil || len(ids) != 0 {
		t.Fatalf("participants of unknown ticket = (%v, %v), want (nil, nil)", ids, err)
	}
}
