package ticketnotify

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// ConsumeAll, UnreadAny, and NotifyAny exist for one reason: a session that
// observes through more than one identity. Their example-based coverage used to
// live three layers up in internal/daemon, which meant this package's own tests
// only ever built single-identity observers. These pin the multi-identity contract
// where the code lives.
//
// The concrete case is a chief of staff. It reads through its session identity AND
// the durable role identity, whose per-ticket cursors survive the role moving to
// another session. Since every delegation binds a ticket, a chief that delegates
// holds both on the same ticket as a matter of course.

const (
	mixChiefSession = "chief-session-1"
	mixWorker       = "worker-1"
)

var mixChiefRole = store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)

// mixObservers is the two-identity view a chief session reads through: the role's
// cursor, the session's authorship, the session as the nudge target.
func mixObservers(sessionID string) []Observer {
	return []Observer{
		{ID: sessionID, AuthorID: sessionID, DeliveryID: sessionID},
		{ID: mixChiefRole, AuthorID: sessionID, DeliveryID: sessionID},
	}
}

// mixWorld sets up a delegation that reaches the chief through BOTH identities at
// once: the chief role durably owns the ticket, and the chief session is also
// subscribed to it — the shape an ordinary session's delegation produces when the
// delegator happens to be the chief.
func mixWorld(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if _, err := h.s.CreateTicketWithSubscribers(
		store.Ticket{ID: "alpha", Title: "alpha", Assignee: mixWorker},
		mixChiefSession,
		store.TicketRoleChiefOfStaff,
		[]string{mixChiefSession},
		h.tick(),
	); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	return h
}

func flatten(bundles []Bundle) []int64 {
	var seqs []int64
	for _, b := range bundles {
		for _, e := range b.Events {
			seqs = append(seqs, e.Seq)
		}
	}
	return seqs
}

// TestConsumeAllDeliversOverlapOnce is the load-bearing property: an event visible
// through two of a session's identities is delivered once, and both cursors move.
func TestConsumeAllDeliversOverlapOnce(t *testing.T) {
	h := mixWorld(t)
	h.status("alpha", store.TicketStatusInReview, mixWorker, "ready for review")

	// Both identities independently see the worker's report.
	observers := mixObservers(mixChiefSession)
	for _, obs := range observers {
		n, err := Unread(h.s, obs)
		if err != nil {
			t.Fatalf("Unread(%s): %v", obs.ID, err)
		}
		if n == 0 {
			t.Fatalf("%s saw nothing; the overlap this test needs does not exist", obs.ID)
		}
	}

	bundles, err := ConsumeAll(h.s, observers, h.tick())
	if err != nil {
		t.Fatalf("ConsumeAll: %v", err)
	}
	seqs := flatten(bundles)
	seen := map[int64]bool{}
	for _, seq := range seqs {
		if seen[seq] {
			t.Fatalf("seq %d delivered twice: %v", seq, seqs)
		}
		seen[seq] = true
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("bundles = %+v, want one for alpha", bundles)
	}

	// BOTH cursors advanced — not just the one that happened to be drained first.
	for _, obs := range observers {
		if n, err := Unread(h.s, obs); err != nil || n != 0 {
			t.Fatalf("%s has %d unread after ConsumeAll (err %v), want 0", obs.ID, n, err)
		}
	}
	if n, err := UnreadAny(h.s, observers); err != nil || n != 0 {
		t.Fatalf("UnreadAny after ConsumeAll = %d (err %v), want 0", n, err)
	}
}

// TestConsumeAllExcludesSelfAuthoredAcrossIdentities pins the AuthorID split: the
// role's cursor is read, but the SESSION's authorship is what gets excluded, so a
// chief is never delivered its own activity through the role identity.
func TestConsumeAllExcludesSelfAuthoredAcrossIdentities(t *testing.T) {
	h := mixWorld(t)
	h.comment("alpha", mixChiefSession, "steering the worker")

	bundles, err := ConsumeAll(h.s, mixObservers(mixChiefSession), h.tick())
	if err != nil {
		t.Fatalf("ConsumeAll: %v", err)
	}
	for _, b := range bundles {
		for _, e := range b.Events {
			if e.Author == mixChiefSession {
				t.Fatalf("chief was delivered its own event through a second identity: %+v", e)
			}
		}
	}
}

// TestRoleIdentitySurvivesSessionChange is why the role identity exists: its cursor
// belongs to the role, so a new session filling it is neither replayed the history
// the previous session already consumed nor cut off from new activity.
func TestRoleIdentitySurvivesSessionChange(t *testing.T) {
	h := mixWorld(t)
	h.status("alpha", store.TicketStatusWorking, mixWorker, "starting")

	if _, err := ConsumeAll(h.s, mixObservers(mixChiefSession), h.tick()); err != nil {
		t.Fatalf("first ConsumeAll: %v", err)
	}

	// The role moves. The role identity is unchanged; only the session differs.
	const successor = "chief-session-2"
	if n, err := UnreadAny(h.s, mixObservers(successor)); err != nil || n != 0 {
		t.Fatalf("successor unread = %d (err %v), want 0 — history was replayed", n, err)
	}

	h.status("alpha", store.TicketStatusInReview, mixWorker, "ready")
	bundles, err := ConsumeAll(h.s, mixObservers(successor), h.tick())
	if err != nil {
		t.Fatalf("second ConsumeAll: %v", err)
	}
	if len(flatten(bundles)) != 1 {
		t.Fatalf("successor consumed %v, want exactly the one new event", flatten(bundles))
	}
}

// TestNotifyAnyNudgesTheSessionNotTheRole pins the delivery target: a role identity
// has no PTY, so the doorbell must land on the session currently filling it.
func TestNotifyAnyNudgesTheSessionNotTheRole(t *testing.T) {
	h := mixWorld(t)
	h.status("alpha", store.TicketStatusInReview, mixWorker, "ready")

	observers := mixObservers(mixChiefSession)
	delivery := observers[0]
	d, err := NotifyAny(h.s, observers, delivery, true, h, time.Now())
	if err != nil || d != DeliveryNudge {
		t.Fatalf("NotifyAny = %v (err %v), want Nudge", d, err)
	}
	if len(h.nudges) != 1 || h.nudges[0] != mixChiefSession {
		t.Fatalf("nudges = %v, want [%s]", h.nudges, mixChiefSession)
	}

	// Notify does not consume, so the queue is still there for ConsumeAll — and one
	// nudge per session, not one per identity.
	if n, err := UnreadAny(h.s, observers); err != nil || n == 0 {
		t.Fatalf("UnreadAny after a nudge = %d (err %v), want non-zero", n, err)
	}
}

// TestUnreadAnyIsUnreadAcrossIdentities pins UnreadAny as a delivery PREDICATE:
// unread on any one identity is enough to reach the session, and it stops
// reporting only once every identity is drained.
func TestUnreadAnyIsUnreadAcrossIdentities(t *testing.T) {
	h := newHarness(t)
	// Role-owned, and NOT subscribed by the session: only the role identity is a
	// participant, so the session identity alone would report nothing.
	if _, err := h.s.CreateRoleOwnedTicket(
		store.Ticket{ID: "beta", Title: "beta", Assignee: mixWorker},
		mixChiefSession, store.TicketRoleChiefOfStaff, h.tick(),
	); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	h.status("beta", store.TicketStatusInReview, mixWorker, "ready")

	observers := mixObservers(mixChiefSession)
	if n, err := Unread(h.s, observers[0]); err != nil || n != 0 {
		t.Fatalf("session identity unread = %d (err %v), want 0", n, err)
	}
	if n, err := UnreadAny(h.s, observers); err != nil || n == 0 {
		t.Fatalf("UnreadAny = %d (err %v), want non-zero via the role identity", n, err)
	}
	if _, err := ConsumeAll(h.s, observers, h.tick()); err != nil {
		t.Fatalf("ConsumeAll: %v", err)
	}
	if n, err := UnreadAny(h.s, observers); err != nil || n != 0 {
		t.Fatalf("UnreadAny after drain = %d (err %v), want 0", n, err)
	}
}
