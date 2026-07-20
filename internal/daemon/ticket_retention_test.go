package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// TestTicketRetentionSweepPassHonoursEnvOverrideTTL pins the wiring this PR
// adds: ticketRetentionSweepPass must actually call SweepExpiredTickets
// (nothing did, before this PR — see the fix brief), and the TTL it passes
// must come from ATTN_TICKET_RETENTION_TTL when set, mirroring every other
// sweep interval/TTL env override in this package.
func TestTicketRetentionSweepPassHonoursEnvOverrideTTL(t *testing.T) {
	t.Setenv("ATTN_TICKET_RETENTION_TTL", "1h")

	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	if _, err := s.CreateTicket(store.Ticket{ID: "open", Title: "live"}, "you", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := s.CreateTicket(store.Ticket{ID: "stale", Title: "stale"}, "you", now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("create stale: %v", err)
	}
	if _, err := s.SetTicketStatus("stale", store.TicketStatusDone, "agent7", "done", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("close stale: %v", err)
	}

	d := &Daemon{store: s}
	d.ticketRetentionSweepPass(now)

	if gone, _ := s.GetTicket("stale"); gone != nil {
		t.Fatal("stale ticket survived the sweep pass")
	}
	if kept, _ := s.GetTicket("open"); kept == nil {
		t.Fatal("open backlog ticket was swept")
	}
}

// TestTicketRetentionSweepPassGuardsNilStore pins the same defensive nil
// check every other sweep pass in this package has (e.g.
// automationRetentionSweepPass), so a daemon mid-startup (store not yet
// wired) can't panic if the ticker somehow fires early.
func TestTicketRetentionSweepPassGuardsNilStore(t *testing.T) {
	d := &Daemon{}
	d.ticketRetentionSweepPass(time.Now())
}
