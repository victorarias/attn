package daemon

import (
	"path/filepath"
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
	id, err := d.createDelegatedTicket("chief", session, "the brief", "Migrate store to X", "codex")
	if err != nil {
		t.Fatalf("createDelegatedTicket: %v", err)
	}
	if id != "migrate-store-to-x-2" {
		t.Fatalf("collision id = %q, want migrate-store-to-x-2", id)
	}
}
