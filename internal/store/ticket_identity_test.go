package store

import (
	"testing"
	"time"
)

func TestTicketMemberIdentityRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		input    string
		identity string
		member   string
		ok       bool
	}{
		{input: "trellis", identity: "member:trellis", member: "trellis", ok: true},
		{input: "  keel  ", identity: "member:keel", member: "keel", ok: true},
		{input: "", identity: "", member: "", ok: false},
	} {
		identity := TicketMemberIdentity(tc.input)
		if identity != tc.identity {
			t.Errorf("TicketMemberIdentity(%q) = %q, want %q", tc.input, identity, tc.identity)
		}
		member, ok := ParseTicketMemberIdentity(identity)
		if member != tc.member || ok != tc.ok {
			t.Errorf("ParseTicketMemberIdentity(%q) = (%q, %v), want (%q, %v)", identity, member, ok, tc.member, tc.ok)
		}
	}

	for _, identity := range []string{"trellis", "role:trellis", "member:", "member: trellis"} {
		if member, ok := ParseTicketMemberIdentity(identity); ok || member != "" {
			t.Errorf("ParseTicketMemberIdentity(%q) = (%q, %v), want no member", identity, member, ok)
		}
	}
}

func TestMigrateTicketIdentityCarriesParticipationAndMaxProgress(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const from = "session-old"
	to := TicketMemberIdentity("trellis")
	base := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)

	if _, err := s.CreateTicket(Ticket{ID: "subscribed", Title: "Subscribed"}, "other", base); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTicketSubscription(from, "subscribed", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "authored", Title: "Authored"}, from, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "assigned", Title: "Assigned", Assignee: from}, "other", base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "unrelated", Title: "Unrelated"}, "other", base.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Source wins one conflict; target wins the other.
	if err := s.SetTicketCursor(from, "subscribed", 9, base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketCursor(to, "subscribed", 4, base.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketCursor(from, "authored", 3, base.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketCursor(to, "authored", 11, base.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketDeliveryAttention(from, base.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketDeliveryAttention(to, base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	migratedAt := base.Add(11 * time.Minute)
	if err := s.MigrateTicketIdentity(from, to, migratedAt); err != nil {
		t.Fatalf("MigrateTicketIdentity: %v", err)
	}
	// The operation is deliberately safe to repeat after the source was removed.
	if err := s.MigrateTicketIdentity(from, to, migratedAt.Add(time.Minute)); err != nil {
		t.Fatalf("idempotent MigrateTicketIdentity: %v", err)
	}

	for _, ticketID := range []string{"subscribed", "authored", "assigned"} {
		if subscribed, err := s.IsTicketSubscribed(to, ticketID); err != nil || !subscribed {
			t.Errorf("target subscription on %s = %v (err %v), want true", ticketID, subscribed, err)
		}
	}
	if subscribed, err := s.IsTicketSubscribed(to, "unrelated"); err != nil || subscribed {
		t.Errorf("target subscription on unrelated = %v (err %v), want false", subscribed, err)
	}
	if subscribed, err := s.IsTicketSubscribed(from, "subscribed"); err != nil || subscribed {
		t.Errorf("source subscription survived = %v (err %v)", subscribed, err)
	}

	for ticketID, want := range map[string]int64{"subscribed": 9, "authored": 11} {
		if got, err := s.GetTicketCursor(to, ticketID); err != nil || got != want {
			t.Errorf("target cursor on %s = %d (err %v), want %d", ticketID, got, err, want)
		}
		if got, err := s.GetTicketCursor(from, ticketID); err != nil || got != 0 {
			t.Errorf("source cursor on %s = %d (err %v), want removed", ticketID, got, err)
		}
	}

	attention, found, err := s.TicketDeliveryAttention(to)
	if err != nil || !found || !attention.LastAttentionAt.Equal(base.Add(10*time.Minute)) {
		t.Errorf("target attention = %+v, found %v, err %v; want later target value", attention, found, err)
	}
	if _, found, err := s.TicketDeliveryAttention(from); err != nil || found {
		t.Errorf("source attention found = %v (err %v), want removed", found, err)
	}
}

func TestMigrateTicketIdentityDoesNotReplayPredecessorAuthorship(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const from = "session-old"
	to := TicketMemberIdentity("trellis")
	base := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "thread", Title: "Thread"}, from, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketComment("thread", from, "my own note", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTicketStatus("thread", TicketStatusWorking, "other", "", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := s.MigrateTicketIdentity(from, to, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	unread, err := s.UnreadTicketEventsFor(to, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].Kind != TicketEventStatusChanged {
		t.Fatalf("unread = %+v, want only the other author's status event", unread)
	}
	ticket, err := s.GetTicket("thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket.Activity) != 2 || ticket.Activity[0].Author != to || ticket.Activity[1].Author != "other" {
		t.Fatalf("migrated activity = %+v, want durable member author then other", ticket.Activity)
	}
}

func TestMigrateTicketIdentityUsesLaterSourceAttention(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	base := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	if err := s.SetTicketDeliveryAttention("session-old", base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketDeliveryAttention("member:trellis", base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateTicketIdentity("session-old", "member:trellis", base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}

	attention, found, err := s.TicketDeliveryAttention("member:trellis")
	if err != nil || !found || !attention.LastAttentionAt.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("target attention = %+v, found %v, err %v; want later source value", attention, found, err)
	}
}
