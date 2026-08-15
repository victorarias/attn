package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// The session ↔ identity mapping is used in both directions and in three places
// (observers, delivery target, attention clock). These pin the round trip, which
// is the property that keeps them from silently disagreeing: an identity a session
// observes through must resolve back to that session, or its events are computed
// and then delivered to nobody.

func TestTicketIdentityRoundTripsForOrdinarySession(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "worker", "Worker")

	observers := d.ticketObserversForSession("worker")
	if len(observers) != 1 {
		t.Fatalf("ordinary session observers = %+v, want exactly its own identity", observers)
	}
	if got := d.ticketSessionForIdentity(observers[0].ID); got != "worker" {
		t.Fatalf("inverse of %q = %q, want worker", observers[0].ID, got)
	}
	if got := d.ticketAttentionKey("worker"); got != "worker" {
		t.Fatalf("attention key = %q, want worker", got)
	}
}

func TestTicketIdentityRoundTripsForChiefSession(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	observers := d.ticketObserversForSession("chief")
	if len(observers) != 2 {
		t.Fatalf("chief observers = %+v, want its session identity plus the durable role", observers)
	}
	// Every identity the chief reads through must resolve back to the chief
	// session, and must carry the session as author and delivery target.
	for _, obs := range observers {
		if got := d.ticketSessionForIdentity(obs.ID); got != "chief" {
			t.Fatalf("inverse of %q = %q, want chief", obs.ID, got)
		}
		if obs.AuthorID != "chief" || obs.DeliveryID != "chief" {
			t.Fatalf("observer %+v must author and deliver as the chief session", obs)
		}
	}
	roleIdentity := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	if observers[1].ID != roleIdentity {
		t.Fatalf("second observer = %q, want %q", observers[1].ID, roleIdentity)
	}
	// The interruption clock follows the role, so a transfer does not reset it.
	if got := d.ticketAttentionKey("chief"); got != roleIdentity {
		t.Fatalf("attention key = %q, want %q", got, roleIdentity)
	}
}

// The role identity outlives the session filling it: after a transfer it resolves
// to the new session, and the old session goes back to being ordinary.
func TestTicketIdentityFollowsRoleTransfer(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief-a", "Chief A")
	addChiefOfStaffTestSession(d, "chief-b", "Chief B")
	roleIdentity := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)

	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief-a"); err != nil {
		t.Fatal(err)
	}
	if got := d.ticketSessionForIdentity(roleIdentity); got != "chief-a" {
		t.Fatalf("role delivers to %q, want chief-a", got)
	}

	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief-b"); err != nil {
		t.Fatal(err)
	}
	if got := d.ticketSessionForIdentity(roleIdentity); got != "chief-b" {
		t.Fatalf("after transfer the role delivers to %q, want chief-b", got)
	}
	if len(d.ticketObserversForSession("chief-a")) != 1 {
		t.Fatalf("the former chief still observes through the role identity")
	}
	if got := d.ticketAttentionKey("chief-a"); got != "chief-a" {
		t.Fatalf("former chief attention key = %q, want its own session", got)
	}
}

// An identity nobody currently fills resolves to no session — the notifier must
// skip it rather than nudge a phantom.
func TestTicketIdentityUnfilledRoleHasNoSession(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	if got := d.ticketSessionForIdentity(store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)); got != "" {
		t.Fatalf("unfilled role resolved to %q, want no session", got)
	}
}

func TestTicketIdentityFollowsCrewMemberDayTurnover(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "day-a")
	addSession(t, d, "day-b")
	if _, err := d.claimCrewBinding("trellis", "day-a"); err != nil {
		t.Fatal(err)
	}

	identity := store.TicketMemberIdentity("trellis")
	observers := d.ticketObserversForSession("day-a")
	if len(observers) != 1 || observers[0].ID != identity || observers[0].AuthorID != identity || observers[0].DeliveryID != "day-a" {
		t.Fatalf("day-a observers = %+v, want the durable member delivered to day-a", observers)
	}
	if got := d.ticketSessionForIdentity(identity); got != "day-a" {
		t.Fatalf("member inverse = %q, want day-a", got)
	}
	if got := d.ticketAttentionKey("day-a"); got != identity {
		t.Fatalf("day-a attention = %q, want %q", got, identity)
	}

	if err := d.transferCrewBinding("trellis", "day-a", "day-b"); err != nil {
		t.Fatal(err)
	}
	if got := d.ticketSessionForIdentity(identity); got != "day-b" {
		t.Fatalf("member inverse after turnover = %q, want day-b", got)
	}
	if got := d.ticketAttentionKey("day-b"); got != identity {
		t.Fatalf("day-b attention = %q, want %q", got, identity)
	}
}

func TestStaleCrewBindingTransferDoesNotMigrateTicketIdentity(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "day-a")
	addSession(t, d, "day-b")
	if _, err := d.claimCrewBinding("trellis", "day-b"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "stale-transfer", Title: "Stale transfer"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription("day-a", "stale-transfer", now); err != nil {
		t.Fatal(err)
	}

	if err := d.transferCrewBinding("trellis", "day-a", "next-day"); err == nil {
		t.Fatal("stale transfer succeeded")
	}
	if subscribed, err := d.store.IsTicketSubscribed(store.TicketMemberIdentity("trellis"), "stale-transfer"); err != nil || subscribed {
		t.Fatalf("stale transfer migrated member subscription = %v, err %v", subscribed, err)
	}
	if subscribed, err := d.store.IsTicketSubscribed("day-a", "stale-transfer"); err != nil || !subscribed {
		t.Fatalf("stale transfer removed source subscription = %v, err %v", subscribed, err)
	}
}

func TestCrewStartupSweepMigratesAnExistingLiveBinding(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "day-a")
	if _, err := d.claimCrewBinding("trellis", "day-a"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "upgrade-thread", Title: "Upgrade thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription("day-a", "upgrade-thread", now); err != nil {
		t.Fatal(err)
	}

	if err := d.migrateCrewTicketIdentities(); err != nil {
		t.Fatal(err)
	}
	identity := store.TicketMemberIdentity("trellis")
	if subscribed, err := d.store.IsTicketSubscribed(identity, "upgrade-thread"); err != nil || !subscribed {
		t.Fatalf("startup member subscription = %v, err %v", subscribed, err)
	}
	if subscribed, err := d.store.IsTicketSubscribed("day-a", "upgrade-thread"); err != nil || subscribed {
		t.Fatalf("startup source subscription survived = %v, err %v", subscribed, err)
	}
}
