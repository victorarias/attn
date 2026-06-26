package ticketnotify

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// The simulation harness drives the real store as the event producer and exercises
// both notification handlers (watch-consume + nudge) without any live session or
// real Monitor. It proves the slice-2 mechanics end to end: emit -> consume ->
// unread cursor, dedup, bundle-by-ticket, the nudge path, and self-authored
// exclusion.

// harness wires a real store to a recording Nudger. It also satisfies Nudger, so
// the nudge path is observable. Note the Nudger contract takes only an observer id
// — there is structurally no channel for event content, which is the
// never-stream-content-to-a-PTY rule, enforced by shape.
type harness struct {
	t      *testing.T
	s      *store.Store
	now    time.Time
	nudges []string
}

func newHarness(t *testing.T) *harness {
	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	return &harness{t: t, s: s, now: time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)}
}

// tick advances and returns the harness clock, so each emit has a distinct stamp.
func (h *harness) tick() time.Time {
	h.now = h.now.Add(time.Minute)
	return h.now
}

func (h *harness) Nudge(observerID string) error {
	h.nudges = append(h.nudges, observerID)
	return nil
}

func hasComment(events []store.TicketEvent, comment string) bool {
	for _, e := range events {
		if e.Comment == comment {
			return true
		}
	}
	return false
}

func (h *harness) create(id, assignee, author string) {
	h.t.Helper()
	if _, err := h.s.CreateTicket(store.Ticket{ID: id, Title: id, Assignee: assignee}, author, h.tick()); err != nil {
		h.t.Fatalf("create %s: %v", id, err)
	}
}

func (h *harness) status(id string, to store.TicketStatus, author, comment string) {
	h.t.Helper()
	if _, err := h.s.SetTicketStatus(id, to, author, comment, h.tick()); err != nil {
		h.t.Fatalf("status %s: %v", id, err)
	}
}

func (h *harness) comment(id, author, comment string) {
	h.t.Helper()
	if _, err := h.s.AddTicketComment(id, author, comment, h.tick()); err != nil {
		h.t.Fatalf("comment %s: %v", id, err)
	}
}

func TestHarnessEmitConsumeCursor(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	// Chief delegates a ticket; the agent picks it up and reports.
	h.create("alpha", "agent7", ObserverChief)
	h.status("alpha", store.TicketStatusWorking, "agent7", "on it")

	// Chief consumes: it sees the agent's status move, but NOT its own created event.
	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("bundles = %+v, want one for alpha", bundles)
	}
	if len(bundles[0].Events) != 1 || bundles[0].Events[0].Kind != store.TicketEventStatusChanged {
		t.Fatalf("events = %+v, want one status_changed", bundles[0].Events)
	}
	if a := bundles[0].Events[0].Author; a == ObserverChief {
		t.Fatalf("chief consumed its own event (author=%s)", a)
	}

	// The cursor advanced: a second consume is empty.
	if n, err := Unread(h.s, chief); err != nil || n != 0 {
		t.Fatalf("unread after consume = %d (err %v), want 0", n, err)
	}
	again, err := Consume(h.s, chief, h.tick())
	if err != nil || len(again) != 0 {
		t.Fatalf("second Consume = %+v (err %v), want empty", again, err)
	}

	// A fresh event becomes unread; the next consume returns only that one.
	h.status("alpha", store.TicketStatusInReview, "agent7", "ready for review")
	if n, _ := Unread(h.s, chief); n != 1 {
		t.Fatalf("unread after new event = %d, want 1", n)
	}
	final, _ := Consume(h.s, chief, h.tick())
	if len(final) != 1 || final[0].Events[0].ToStatus != store.TicketStatusInReview {
		t.Fatalf("final consume = %+v, want one in_review", final)
	}
}

func TestHarnessBundleByTicket(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	h.create("beta", "agent9", ObserverChief)

	// Interleave events across the two tickets; consume should group by ticket.
	h.status("alpha", store.TicketStatusWorking, "agent7", "")
	h.status("beta", store.TicketStatusWorking, "agent9", "")
	h.comment("alpha", "agent7", "halfway")

	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 2 {
		t.Fatalf("bundles = %d, want 2 (one per ticket)", len(bundles))
	}
	// First-seen ticket order: alpha's status moved first.
	if bundles[0].TicketID != "alpha" || bundles[1].TicketID != "beta" {
		t.Fatalf("bundle order = %s,%s, want alpha,beta", bundles[0].TicketID, bundles[1].TicketID)
	}
	if len(bundles[0].Events) != 2 { // status + comment, grouped
		t.Fatalf("alpha events = %d, want 2", len(bundles[0].Events))
	}
	if len(bundles[1].Events) != 1 {
		t.Fatalf("beta events = %d, want 1", len(bundles[1].Events))
	}
}

func TestHarnessDedup(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	// The same comment twice in a row is one logical event (a retry) — deduped.
	h.comment("alpha", "agent7", "ping")
	h.comment("alpha", "agent7", "ping")

	events, err := h.s.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	commentEvents := 0
	for _, e := range events {
		if e.Kind == store.TicketEventCommented {
			commentEvents++
		}
	}
	if commentEvents != 1 {
		t.Fatalf("comment events = %d, want 1 (deduped)", commentEvents)
	}

	// Idempotent at the append boundary too: a re-append returns appended=false.
	if _, appended, err := h.s.AppendTicketEvent(store.TicketEvent{
		TicketID: "alpha", Kind: store.TicketEventCommented, Author: "agent7", Comment: "ping",
	}, h.tick()); err != nil || appended {
		t.Fatalf("re-append appended=%v (err %v), want false", appended, err)
	}

	// The chief still sees a single comment.
	bundles, _ := Consume(h.s, chief, h.tick())
	got := 0
	for _, b := range bundles {
		for _, e := range b.Events {
			if e.Kind == store.TicketEventCommented {
				got++
			}
		}
	}
	if got != 1 {
		t.Fatalf("chief saw %d comments, want 1", got)
	}
}

func TestHarnessNudgePath(t *testing.T) {
	h := newHarness(t)

	// A codex agent can't self-monitor; a Claude agent can.
	codex := AgentObserver("codexbot", "codex")
	claude := AgentObserver("agent7", "claude")
	if codex.HasSelfMonitor {
		t.Fatal("codex should not self-monitor")
	}
	if !claude.HasSelfMonitor {
		t.Fatal("claude should self-monitor")
	}

	h.create("alpha", "codexbot", ObserverChief)
	h.create("beta", "agent7", ObserverChief)

	// The chief steers each agent (reverse channel).
	h.comment("alpha", ObserverChief, "please rebase")
	h.comment("beta", ObserverChief, "tweak the title")

	// Busy codex: deferred, no nudge yet.
	d, err := Notify(h.s, codex, false, h, h.tick())
	if err != nil || d != DeliveryDeferred {
		t.Fatalf("busy codex delivery = %v (err %v), want Deferred", d, err)
	}
	if len(h.nudges) != 0 {
		t.Fatalf("nudges = %v, want none while busy", h.nudges)
	}

	// Idle codex: nudged (a fixed trigger carrying only the observer id).
	d, err = Notify(h.s, codex, true, h, h.tick())
	if err != nil || d != DeliveryNudge {
		t.Fatalf("idle codex delivery = %v (err %v), want Nudge", d, err)
	}
	if len(h.nudges) != 1 || h.nudges[0] != "codexbot" {
		t.Fatalf("nudges = %v, want [codexbot]", h.nudges)
	}

	// Claude agent: its own watch consumes; never nudged.
	d, err = Notify(h.s, claude, true, h, h.tick())
	if err != nil || d != DeliveryWatch {
		t.Fatalf("claude delivery = %v (err %v), want Watch", d, err)
	}
	if len(h.nudges) != 1 {
		t.Fatalf("nudges = %v, claude must not be nudged", h.nudges)
	}

	// After the nudge, the codex agent consumes its own queue and sees the steer.
	// (It also sees the chief-authored "created" event — the "assigned to you"
	// signal — but never beta, which belongs to another agent.)
	bundles, _ := Consume(h.s, codex, h.tick())
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("codex consume = %+v, want only alpha", bundles)
	}
	if !hasComment(bundles[0].Events, "please rebase") {
		t.Fatalf("codex did not see the rebase steer: %+v", bundles[0].Events)
	}
	// Nothing left unread once consumed; a re-notify is quiet.
	if d, _ := Notify(h.s, codex, true, h, h.tick()); d != DeliveryNone {
		t.Fatalf("re-notify after consume = %v, want None", d)
	}
}

func TestHarnessAgentScopeAndSelfAuthored(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()
	agent7 := AgentObserver("agent7", "claude")

	h.create("alpha", "agent7", ObserverChief) // assigned to agent7
	h.create("beta", "agent9", ObserverChief)  // assigned to someone else

	// agent7 acts on its own ticket; the chief comments on it; an event lands on beta.
	h.status("alpha", store.TicketStatusWorking, "agent7", "starting")
	h.comment("alpha", ObserverChief, "one note")
	h.status("beta", store.TicketStatusWorking, "agent9", "starting")

	// agent7 observes only alpha, and not the event it authored itself.
	bundles, err := Consume(h.s, agent7, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("agent7 bundles = %+v, want only alpha (never beta)", bundles)
	}
	for _, e := range bundles[0].Events {
		if e.Author == "agent7" {
			t.Fatalf("agent7 saw its own event: %+v", e)
		}
	}
	// It sees the chief's steer; it never sees its own "starting" status move.
	if !hasComment(bundles[0].Events, "one note") {
		t.Fatalf("agent7 missing the chief's note: %+v", bundles[0].Events)
	}

	// The chief, by contrast, observes every ticket's events (minus its own note).
	chiefBundles, _ := Consume(h.s, chief, h.tick())
	tickets := map[string]bool{}
	for _, b := range chiefBundles {
		tickets[b.TicketID] = true
		for _, e := range b.Events {
			if e.Author == ObserverChief {
				t.Fatalf("chief saw its own event: %+v", e)
			}
		}
	}
	if !tickets["alpha"] || !tickets["beta"] {
		t.Fatalf("chief tickets = %v, want both alpha and beta", tickets)
	}
}
