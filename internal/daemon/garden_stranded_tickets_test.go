package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A garden with nothing stranded carries no count at all, so every surface's
// "is there a notice" test is the field being absent.
func TestSeedListCarriesNoStrandedCountWhenTheBoardIsDrained(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "shipped", "Shipped", "", store.TicketStatusDone, "sess-1")
	seedBacklogTicket(t, d, "in-flight", "In flight", "", store.TicketStatusWorking, "sess-1")

	if got := list(t, d, protocol.SeedListMessage{}).StrandedTickets; got != nil {
		t.Fatalf("stranded count = %d on a board with nothing stranded, want the field absent", *got)
	}
}

// The count the listing prints is the real one: every crashed and failed ticket
// still on the board, never a sample and never a cap.
func TestSeedListCountsEveryStrandedTicket(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "died-one", "Died one", "", store.TicketStatusCrashed, "sess-1")
	seedBacklogTicket(t, d, "died-two", "Died two", "", store.TicketStatusCrashed, "sess-2")
	seedBacklogTicket(t, d, "gave-up", "Gave up", "", store.TicketStatusFailed, "sess-3")
	seedBacklogTicket(t, d, "shipped", "Shipped", "", store.TicketStatusDone, "sess-4")

	got := list(t, d, protocol.SeedListMessage{}).StrandedTickets
	if got == nil || *got != 3 {
		t.Fatalf("stranded count = %v, want 3", got)
	}
}

// A crash stamped on a live daemon moves the count, and the garden push is what
// carries it — the board itself renders nowhere, so without this projection the
// panel's notice would only appear after the next unrelated seed change.
func TestCrashingATicketRepushesTheGardenWithTheStrandedCount(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "will-die", "Will die", "", store.TicketStatusWorking, "sess-1")

	pushed := make(chan *int, 8)
	d.gardenBroadcastHook = func([]protocol.Seed, int) {
		pushed <- d.strandedTicketsField()
	}

	if !d.crashTicket("will-die", "sess-1", protocol.StateWorking) {
		t.Fatal("crashTicket refused a working ticket")
	}

	select {
	case got := <-pushed:
		if got == nil || *got != 1 {
			t.Fatalf("garden push carried stranded count %v, want 1", got)
		}
	default:
		t.Fatal("crashing a ticket pushed no garden; the panel's notice would go stale")
	}
}
