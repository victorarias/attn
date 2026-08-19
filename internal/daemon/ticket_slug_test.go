package daemon

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func TestTicketSlug(t *testing.T) {
	cases := map[string]string{
		"Migrate store to X": "migrate-store-to-x",
		"  Trim/These  ":     "trim-these",
		"already-kebab":      "already-kebab",
		"":                   "ticket",
		"!!!":                "ticket",
	}
	for in, want := range cases {
		if got := ticketSlug(in); got != want {
			t.Errorf("ticketSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// When the derived slug is already taken, the next ticket gets a numeric suffix
// rather than failing outright.
func TestCreateTicketWithUniqueSlugCollisionSuffix(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if _, err := d.store.CreateTicket(store.Ticket{ID: "migrate-store-to-x", Title: "x"}, "chief", time.Now()); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	created, err := d.createTicketWithUniqueSlug(
		store.Ticket{Title: "Migrate store to X"}, ticketSlug("Migrate store to X"), "chief", "", nil, time.Now())
	if err != nil {
		t.Fatalf("createTicketWithUniqueSlug: %v", err)
	}
	if created.ID != "migrate-store-to-x-2" {
		t.Fatalf("collision id = %q, want migrate-store-to-x-2", created.ID)
	}
}

// Exhausting the readable sequential range must not fail the allocation: it falls
// back to a random suffix on the same base.
func TestCreateTicketWithUniqueSlugFallsBackPastSequentialRange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "attn", Title: "x"}, "sess-a", now); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	for i := 2; i <= ticketSlugSequentialAttempts; i++ {
		id := "attn-" + strconv.Itoa(i)
		if _, err := d.store.CreateTicket(store.Ticket{ID: id, Title: "x"}, "sess-a", now); err != nil {
			t.Fatalf("seed ticket %s: %v", id, err)
		}
	}

	// Two allocations: the fallback must be random, so it neither repeats itself nor
	// resumes counting. The suffix is hex and so is all digits about 6% of the time —
	// its fixed width, not its non-numeric-ness, is what separates it from the walk.
	var ids []string
	for i := range 2 {
		created, err := d.createTicketWithUniqueSlug(
			store.Ticket{Title: "attn"}, "attn", "sess-a", "", nil, now)
		if err != nil {
			t.Fatalf("createTicketWithUniqueSlug %d past sequential range: %v", i, err)
		}
		id := created.ID
		suffix, ok := strings.CutPrefix(id, "attn-")
		if !ok || len(suffix) != ticketSlugRandomSuffixLen {
			t.Fatalf("fallback id = %q, want attn- plus a %d-character random suffix, not the sequential walk continuing", id, ticketSlugRandomSuffixLen)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatalf("both fallbacks allocated %q, want distinct random suffixes", ids[0])
	}
}
