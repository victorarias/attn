package daemon

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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
// rather than failing the delegation.
func TestCreateDelegatedTicketCollisionSuffix(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if _, err := d.store.CreateTicket(store.Ticket{ID: "migrate-store-to-x", Title: "x"}, "chief", time.Now()); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	session := &protocol.Session{ID: "sess-1", Directory: "/tmp/x"}
	id, err := d.createDelegatedTicket("chief", true, session, "the brief", "Migrate store to X", "codex")
	if err != nil {
		t.Fatalf("createDelegatedTicket: %v", err)
	}
	if id != "migrate-store-to-x-2" {
		t.Fatalf("collision id = %q, want migrate-store-to-x-2", id)
	}
}

// Exhausting the readable sequential range must not fail a delegation whose session
// is already spawned: the allocator falls back to a random suffix on the same base.
// Now that every delegation creates a ticket, one popular base fills up fast.
func TestCreateDelegatedTicketFallsBackPastSequentialRange(t *testing.T) {
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

	session := &protocol.Session{ID: "sess-1", Directory: "/tmp/x"}
	id, err := d.createDelegatedTicket("sess-a", false, session, "the brief", "attn", "codex")
	if err != nil {
		t.Fatalf("createDelegatedTicket past sequential range: %v", err)
	}
	if !strings.HasPrefix(id, "attn-") || id == "attn-2" {
		t.Fatalf("fallback id = %q, want a random attn-<suffix>", id)
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(id, "attn-")); err == nil {
		t.Fatalf("fallback id = %q, want a non-numeric suffix past the sequential range", id)
	}
}

// Participation is the routing contract: the delegated agent (assignee), the
// delegator, and the chief of staff each reach the ticket, for an ordinary
// delegation as much as a chief-initiated one.
func TestCreateDelegatedTicketParticipants(t *testing.T) {
	chiefRole := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	for _, tc := range []struct {
		name             string
		ownedByChiefRole bool
	}{
		{"ordinary delegation", false},
		{"chief delegation", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			session := &protocol.Session{ID: "sess-delegated", Directory: "/tmp/x"}
			id, err := d.createDelegatedTicket("sess-creator", tc.ownedByChiefRole, session, "the brief", "Some work", "codex")
			if err != nil {
				t.Fatalf("createDelegatedTicket: %v", err)
			}
			participants, err := d.store.TicketParticipants(id)
			if err != nil {
				t.Fatalf("TicketParticipants: %v", err)
			}
			got := map[string]bool{}
			for _, p := range participants {
				got[p] = true
			}
			for _, want := range []string{"sess-delegated", "sess-creator", chiefRole} {
				if !got[want] {
					t.Fatalf("participants = %v, missing %q", participants, want)
				}
			}

			// Role ownership stays reserved for chief-initiated delegations: it is what
			// marks a session delegated-from-chief in the sidebar.
			owned, err := d.store.IsTicketRoleOwner(store.TicketRoleChiefOfStaff, id)
			if err != nil {
				t.Fatalf("IsTicketRoleOwner: %v", err)
			}
			if owned != tc.ownedByChiefRole {
				t.Fatalf("chief role owns ticket = %v, want %v", owned, tc.ownedByChiefRole)
			}
		})
	}
}
