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

	// Two allocations: the fallback must be random, so it neither repeats itself nor
	// resumes counting. The suffix is hex and so is all digits about 6% of the time —
	// its fixed width, not its non-numeric-ness, is what separates it from the walk.
	var ids []string
	for i, sessionID := range []string{"sess-1", "sess-2"} {
		session := &protocol.Session{ID: sessionID, Directory: "/tmp/x"}
		id, err := d.createDelegatedTicket("sess-a", false, session, "the brief", "attn", "codex")
		if err != nil {
			t.Fatalf("createDelegatedTicket %d past sequential range: %v", i, err)
		}
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

// Participation is the routing contract: the delegated agent (assignee), the
// delegator, and the chief of staff each reach the ticket. Who the delegator IS
// decides how it attaches — an ordinary session personally, the chief through
// its durable role and never also as itself, so the attachment transfers with
// the role instead of following the session that held it at delegation time.
func TestCreateDelegatedTicketParticipants(t *testing.T) {
	chiefRole := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	for _, tc := range []struct {
		name             string
		ownedByChiefRole bool
		want             []string
		absent           string
	}{
		{"ordinary delegation", false, []string{"sess-delegated", "sess-creator", chiefRole}, ""},
		{"chief delegation", true, []string{"sess-delegated", chiefRole}, "sess-creator"},
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
			for _, want := range tc.want {
				if !got[want] {
					t.Fatalf("participants = %v, missing %q", participants, want)
				}
			}
			if tc.absent != "" && got[tc.absent] {
				t.Fatalf("participants = %v, want %q attached through the role only", participants, tc.absent)
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

func TestCrewMemberDelegationUsesDurableMemberIdentity(t *testing.T) {
	memberIdentity := store.TicketMemberIdentity("trellis")
	chiefRole := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	for _, tc := range []struct {
		name string
		bind func(t *testing.T, d *Daemon, delegated *protocol.Session) string
	}{
		{
			name: "create",
			bind: func(t *testing.T, d *Daemon, delegated *protocol.Session) string {
				t.Helper()
				id, err := d.createDelegatedTicket("trellis-today", false, delegated, "the brief", "Member work", "codex")
				if err != nil {
					t.Fatalf("createDelegatedTicket: %v", err)
				}
				return id
			},
		},
		{
			name: "adopt",
			bind: func(t *testing.T, d *Daemon, delegated *protocol.Session) string {
				t.Helper()
				if _, err := d.store.CreateTicket(store.Ticket{ID: "member-work", Title: "Member work", Description: "the brief"}, "filer", time.Now()); err != nil {
					t.Fatalf("seed ticket: %v", err)
				}
				id, err := d.adoptDelegatedTicket("trellis-today", false, delegated, "member-work", "codex", false)
				if err != nil {
					t.Fatalf("adoptDelegatedTicket: %v", err)
				}
				return id
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newCrewDaemon(t)
			addSession(t, d, "trellis-today")
			if _, err := d.claimCrewBinding("trellis", "trellis-today"); err != nil {
				t.Fatalf("claim crew binding: %v", err)
			}
			delegated := &protocol.Session{ID: "sess-delegated", Directory: "/tmp/x"}
			ticketID := tc.bind(t, d, delegated)

			participants, err := d.store.TicketParticipants(ticketID)
			if err != nil {
				t.Fatalf("TicketParticipants: %v", err)
			}
			got := map[string]bool{}
			for _, participant := range participants {
				got[participant] = true
			}
			for _, want := range []string{delegated.ID, memberIdentity, chiefRole} {
				if !got[want] {
					t.Errorf("participants = %v, missing %q", participants, want)
				}
			}
			if got["trellis-today"] {
				t.Errorf("participants = %v, disposable member session is attached", participants)
			}

			events, err := d.store.TicketEventsSince(0)
			if err != nil {
				t.Fatalf("TicketEventsSince: %v", err)
			}
			delegationEvents := 0
			for _, event := range events {
				if event.TicketID != ticketID || (event.Kind == store.TicketEventCreated && tc.name == "adopt") {
					continue
				}
				delegationEvents++
				if event.Author != memberIdentity || event.AuthorRole != "" {
					t.Errorf("delegation event = %+v, want author %q without a role", event, memberIdentity)
				}
			}
			wantEvents := 1
			if tc.name == "adopt" {
				wantEvents = 2
			}
			if delegationEvents != wantEvents {
				t.Errorf("delegation events = %d, want %d", delegationEvents, wantEvents)
			}
		})
	}
}

func TestChiefMemberDelegationKeepsChiefRoleAttachment(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "trellis-today")
	if _, err := d.claimCrewBinding("trellis", "trellis-today"); err != nil {
		t.Fatalf("claim crew binding: %v", err)
	}
	delegated := &protocol.Session{ID: "sess-delegated", Directory: "/tmp/x"}
	ticketID, err := d.createDelegatedTicket("trellis-today", true, delegated, "the brief", "Chief work", "codex")
	if err != nil {
		t.Fatalf("createDelegatedTicket: %v", err)
	}
	participants, err := d.store.TicketParticipants(ticketID)
	if err != nil {
		t.Fatalf("TicketParticipants: %v", err)
	}
	got := map[string]bool{}
	for _, participant := range participants {
		got[participant] = true
	}
	chiefRole := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	if !got[delegated.ID] || !got[chiefRole] {
		t.Errorf("participants = %v, want delegated session and %q", participants, chiefRole)
	}
	if got["trellis-today"] || got[store.TicketMemberIdentity("trellis")] {
		t.Errorf("participants = %v, chief delegation also attached its acting member", participants)
	}
	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	created := events[0]
	if created.Author != "trellis-today" || created.AuthorRole != store.TicketRoleChiefOfStaff {
		t.Fatalf("created event = %+v, want concrete audit author carrying the chief role", created)
	}
}
