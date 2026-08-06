package daemon

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// The windowed subscription's battery.
//
// Every assertion here is an inequality or an equality between two receipts the
// wire itself carries — a write's seq against a delivery's as_of_seq, a delivered
// window against a fresh query's answer. None of them waits on a duration,
// because a subscription test that needs a sleep is a subscription test that has
// stopped proving anything about the case where the machine is slow.
//
// See docs/plans/2026-08-05-ext-a3.4-doc-store-positions-and-windows.md.

// ---------------------------------------------------------------------------
// Receipts the tests below compare
// ---------------------------------------------------------------------------

// putDocSeq writes and returns the position the write landed at.
func putDocSeq(t *testing.T, d *Daemon, id, body string) int64 {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl, ID: id, Body: body,
		})
	})
	if !resp.Ok {
		t.Fatalf("put %s: %v", id, protocol.Deref(resp.Error))
	}
	return int64(resp.DocPutResult.Seq)
}

// deleteDocSeq removes and returns the position, which is 0 when the delete
// removed nothing — nothing changed, so nothing was announced.
func deleteDocSeq(t *testing.T, d *Daemon, id string) int64 {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDelete(c, &protocol.DocDeleteMessage{
			Cmd: protocol.CmdDocDelete, Namespace: testDocNS, Collection: testDocColl, ID: id,
		})
	})
	if !resp.Ok {
		t.Fatalf("delete %s: %v", id, protocol.Deref(resp.Error))
	}
	return int64(resp.DocDeleteResult.Seq)
}

// queryIDs answers the same query one-shot, which is what a delivered window is
// checked against: the subscription must never show a state a fresh read
// disagrees with.
func queryIDs(t *testing.T, d *Daemon, q protocol.DocumentQuery) []string {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
	})
	if !resp.Ok {
		t.Fatalf("query: %v", protocol.Deref(resp.Error))
	}
	out := make([]string, 0, len(resp.DocQueryResult.Documents))
	for _, doc := range resp.DocQueryResult.Documents {
		out = append(out, doc.ID)
	}
	return out
}

// settle reads deliveries until one is at least as current as seq, and returns
// it. This is the whole reason writes report a position: "the subscription has
// caught up" used to be unwritable without waiting on a duration, and is now an
// inequality between two numbers the protocol already carries.
//
// It cannot hang. A delivery is computed after the wake it consumed, and a wake
// is set after the write commits, so a delivery reporting a position below seq
// consumed a wake older than that write — which means that write's own wake is
// still pending and another delivery follows.
func (lq *liveQuery) settle(t *testing.T, seq int64) window {
	t.Helper()
	for {
		w := lq.next(t)
		if w.asOfSeq >= seq {
			return w
		}
	}
}

// ---------------------------------------------------------------------------
// Subscribe refuses a cursor
// ---------------------------------------------------------------------------

// A live query is a window and a cursor is a walk: the document an after cursor
// names moves out from under a subscription, so the page it named is not the
// page it names a write later. The refusal has to name the alternative, because
// the reader fixing it is an agent with only the error to go on.
func TestSubscribingWithAnAfterCursorIsRefused(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	after := "a"
	q := testQuery()
	q.After = &after

	lq := subscribe(t, d, q)
	resp := lq.nextRaw(t)
	if resp.Ok {
		t.Fatal("subscribing with an after cursor was accepted")
	}
	if code := protocol.Deref(resp.ErrorCode); code != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("error code = %q, want %q", code, protocol.ErrorCodeInvalidQuery)
	}
	msg := protocol.Deref(resp.Error)
	for _, want := range []string{"limit", "window"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q does not name the alternative (%q)", msg, want)
		}
	}
	lq.stop()
	if n := d.documentSubscriptionCount(); n != 0 {
		t.Fatalf("a refused subscribe left %d subscriptions behind", n)
	}
}

// The one-shot read keeps its cursor. There is one instant in a single query, so
// nothing moves under the anchor and the walk is correct; the refusal above is
// about subscriptions only, and the two must not be conflated.
func TestOneShotQueryStillTakesAnAfterCursor(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"pending"}`)

	after := "a"
	q := testQuery()
	q.After = &after
	if got := queryIDs(t, d, q); !equalStrings(got, []string{"b"}) {
		t.Fatalf("page after a = %v, want [b]", got)
	}
}

// ---------------------------------------------------------------------------
// Only what changed travels
// ---------------------------------------------------------------------------

// The point of the window: a delivery names every document in order and carries
// only the bodies the subscriber does not hold. A collection where one of many
// documents changed must not re-send the rest.
func TestADeliveryCarriesOnlyTheBodiesThatChanged(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	for _, id := range []string{"a", "b", "c"} {
		putDoc(t, d, id, `{"status":"pending"}`)
	}

	lq := subscribe(t, d, testQuery())
	first := lq.next(t)
	if got := first.changed(); len(got) != 3 {
		t.Fatalf("the first delivery sent %d bodies, want all 3 — a fresh subscriber holds nothing", len(got))
	}

	seq := putDocSeq(t, d, "b", `{"status":"approved"}`)
	next := lq.settle(t, seq)
	if got := ids(next); !equalStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("order = %v, want every document still named", got)
	}
	if got := next.changed(); !equalStrings(got, []string{"b"}) {
		t.Fatalf("bodies sent = %v, want only the one that changed", got)
	}
	// The bodies the subscriber did not receive are the ones it kept, which is
	// what the applied window proves.
	for _, doc := range next.documents {
		want := `{"status":"pending"}`
		if doc.ID == "b" {
			want = `{"status":"approved"}`
		}
		if doc.Body != want {
			t.Fatalf("%s applied as %s, want %s", doc.ID, doc.Body, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Resume by content
// ---------------------------------------------------------------------------

// The four ways a document can differ from what a resuming subscriber holds, each
// checked the same way: the first delivery after resuming carries exactly the
// bodies that changed, and the applied window equals a fresh query's answer. A
// resume that sent one body too few would render a stale document; one body too
// many is the full refetch this design exists to avoid.
func TestResumingSendsExactlyWhatChangedWhileAway(t *testing.T) {
	limit := 2
	sorted := func() protocol.DocumentQuery {
		q := testQuery()
		q.Sort = &protocol.DocumentSort{Field: "status"}
		q.Limit = &limit
		return q
	}

	cases := []struct {
		name string
		// mutate runs while nobody is subscribed, and reports the position of
		// the last write it made.
		mutate func(t *testing.T, d *Daemon) int64
		// order is the window the resumed subscription must show.
		order []string
		// changed is exactly the set of bodies that must travel.
		changed []string
	}{
		{
			name:    "edited inside the window",
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "a", `{"status":"aa"}`) },
			order:   []string{"a", "b"},
			changed: []string{"a"},
		},
		{
			name:    "deleted",
			mutate:  func(t *testing.T, d *Daemon) int64 { return deleteDocSeq(t, d, "a") },
			order:   []string{"b", "c"},
			changed: []string{"c"},
		},
		{
			name: "displaced past the limit",
			// A new document sorting first pushes the last one out of the
			// window. Nothing about the displaced document changed, and no fact
			// names it — it leaves the window only because another arrived,
			// which is the case log replay could never have reconstructed.
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "z", `{"status":"a0"}`) },
			order:   []string{"z", "a"},
			changed: []string{"z"},
		},
		{
			name:    "edited out of the filter",
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "b", `{"status":"zz"}`) },
			order:   []string{"a", "c"},
			changed: []string{"c"},
		},
		{
			name: "nothing changed at all",
			// The degenerate case, and the one that proves the resume is worth
			// having: no body travels, and the window is still complete.
			mutate:  func(t *testing.T, d *Daemon) int64 { return 0 },
			order:   []string{"a", "b"},
			changed: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDaemonForTest(t)
			defineTestCollection(t, d)
			putDoc(t, d, "a", `{"status":"a1"}`)
			putDoc(t, d, "b", `{"status":"b1"}`)
			putDoc(t, d, "c", `{"status":"c1"}`)

			lq := subscribe(t, d, sorted())
			held := lq.next(t).documents
			if got := idsOf(held); !equalStrings(got, []string{"a", "b"}) {
				t.Fatalf("initial window = %v, want [a b]", got)
			}
			lq.stop()

			seq := tc.mutate(t, d)

			resumed := subscribeResuming(t, d, sorted(), held)
			first := resumed.next(t)
			if first.asOfSeq < seq {
				t.Fatalf("resumed at seq %d, below the write at %d", first.asOfSeq, seq)
			}
			if got := ids(first); !equalStrings(got, tc.order) {
				t.Fatalf("resumed window = %v, want %v", got, tc.order)
			}
			if got := first.changed(); !equalStrings(got, tc.changed) {
				t.Fatalf("bodies sent on resume = %v, want %v", got, tc.changed)
			}
			if got, want := ids(first), queryIDs(t, d, sorted()); !equalStrings(got, want) {
				t.Fatalf("resumed window = %v, but a fresh query says %v", got, want)
			}
		})
	}
}

// A resume token the store never issued — a revision from a subscriber that has
// been away so long its claim is meaningless, or simply a wrong one — must
// converge rather than mislead. The claim differs from what is stored, so the
// body travels; the only way to get this wrong is to trust the claim.
func TestResumingWithARevisionTheStoreNeverIssuedStillConverges(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	stale := []protocol.StoredDocument{{ID: "a", Body: `{"status":"whatever this was"}`, Rev: 4242}}
	lq := subscribeResuming(t, d, testQuery(), stale)
	first := lq.next(t)
	if got := first.changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("bodies sent = %v, want the document whose claimed revision was never issued", got)
	}
	if len(first.documents) != 1 || first.documents[0].Body != `{"status":"pending"}` {
		t.Fatalf("applied window = %+v, want the stored body", first.documents)
	}
}

// A resume that names a document the query no longer returns simply does not see
// it: it is absent from order, and the client's forget rule collects it. There is
// no removal field to get wrong.
func TestResumingForgetsWhatIsNoLongerInTheWindow(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	held := lq.next(t).documents
	lq.stop()

	deleteDocSeq(t, d, "a")

	resumed := subscribeResuming(t, d, testQuery(), held)
	first := resumed.next(t)
	if len(first.order) != 0 {
		t.Fatalf("resumed window = %v, want empty", first.order)
	}
	if len(first.upsert) != 0 {
		t.Fatalf("a body travelled for a document that is not in the window: %v", first.changed())
	}
}

// ---------------------------------------------------------------------------
// Currency, coalescing, fan-out
// ---------------------------------------------------------------------------

// Currency under contention. A writer flips one document between two known
// states as fast as it can; every window a subscriber sees must be one of those
// two, and once the writer stops the subscription must reach a delivery at least
// as current as the last write, showing what a fresh query shows.
func TestASubscriptionStaysCurrentUnderContention(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	lq.next(t)

	const flips = 40
	bodies := []string{`{"status":"pending"}`, `{"status":"approved"}`}

	// The writer's last act is a sentinel document, and its arrival in a window
	// is what ends the read loop. Reading "until the writer is done" cannot work
	// from here: the reader would learn that only after consuming the delivery
	// the last write caused, and would then block waiting for one that is never
	// coming. The sentinel makes the stop condition something the wire carries.
	var sentinelSeq int64
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := range flips {
			putDocSeq(t, d, "a", bodies[i%2])
		}
		sentinelSeq = putDocSeq(t, d, "sentinel", `{"status":"pending"}`)
	}()

	// Read alongside the writer. Every window is a state the collection was
	// really in — the enumerable legal set is two bodies — and nothing here
	// waits on a duration to say so.
	var final window
	for {
		w := lq.next(t)
		holds := false
		for _, doc := range w.documents {
			if doc.ID != "a" {
				continue
			}
			holds = true
			if doc.Body != bodies[0] && doc.Body != bodies[1] {
				t.Fatalf("window showed %s, which the collection was never in", doc.Body)
			}
		}
		if !holds {
			t.Fatalf("window = %v, and the flipped document is not in it", ids(w))
		}
		if len(w.order) == 2 {
			final = w
			break
		}
	}
	writer.Wait()

	if final.asOfSeq < sentinelSeq {
		t.Fatalf("the delivery carrying the last write reports seq %d, below the write's own %d",
			final.asOfSeq, sentinelSeq)
	}
	if got, want := ids(final), queryIDs(t, d, testQuery()); !equalStrings(got, want) {
		t.Fatalf("final window = %v, fresh query = %v", got, want)
	}
}

// The one-slot wake channel's receipt. A subscriber blocked mid-encode — net.Pipe
// gives no buffer at all, so the daemon is stuck in Write until this test reads —
// must not accumulate one delivery per write. What arrives instead is the
// delivery it was blocked on plus one more, current.
//
// The bound is exact rather than approximate: the handler can only be in one of
// two places when the writes land, and the slot holds one nudge.
func TestABurstOfWritesCollapsesIntoAFewDeliveries(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	lq := subscribe(t, d, testQuery())
	lq.next(t) // delivery 1, drained; the handler now waits on its wake channel

	const writes = 40
	var last int64
	want := make([]string, 0, writes)
	for i := range writes {
		id := fmt.Sprintf("d%02d", i)
		last = putDocSeq(t, d, id, `{"status":"pending"}`)
		want = append(want, id)
	}

	deliveries := 0
	var final window
	for {
		final = lq.next(t)
		deliveries++
		if final.asOfSeq >= last {
			break
		}
		if deliveries > 4 {
			t.Fatalf("%d writes produced %d deliveries before catching up; the wake slot is not collapsing them",
				writes, deliveries)
		}
	}
	if deliveries > 2 {
		t.Fatalf("%d writes produced %d deliveries, want at most 2 — one in flight plus one current",
			writes, deliveries)
	}
	if got := ids(final); !equalStrings(got, want) {
		t.Fatalf("final window = %v, want every written document", got)
	}
}

// Two subscribers on one collection each get their own window, and each one's
// bodies are diffed against what IT holds. The second subscriber joining late
// must not make the first one re-receive anything.
func TestEverySubscriberGetsItsOwnWindow(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	first := subscribe(t, d, testQuery())
	if got := first.next(t).changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("first subscriber's initial bodies = %v", got)
	}

	second := subscribe(t, d, testQuery())
	if got := second.next(t).changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("second subscriber's initial bodies = %v — a fresh subscriber holds nothing", got)
	}

	seq := putDocSeq(t, d, "b", `{"status":"pending"}`)
	for name, lq := range map[string]*liveQuery{"first": first, "second": second} {
		w := lq.settle(t, seq)
		if got := ids(w); !equalStrings(got, []string{"a", "b"}) {
			t.Fatalf("%s subscriber's window = %v, want [a b]", name, got)
		}
		if got := w.changed(); !equalStrings(got, []string{"b"}) {
			t.Fatalf("%s subscriber received %v, want only the new document", name, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The window invariant, fuzzed
// ---------------------------------------------------------------------------

// windowFuzzBodies is the alphabet the fuzz draws from, and every entry is a case
// where the encoding could disagree with the ordering the query means. Ties on
// the sort field decide whether the id tiebreaker is really applied; a missing
// field and a JSON null are what SQLite sorts as NULL, which compares as nothing
// at all; text that looks numeric and a value with no numeric reading are what
// the column's NUMERIC affinity does and does not convert.
//
// A differential harness validates machinery, never meaning: these are the
// values where a window built from the wrong comparison would still look
// plausible.
var windowFuzzBodies = []string{
	`{"n":1}`,
	`{"n":1}`,
	`{"n":2}`,
	`{"n":"1"}`,
	`{"n":"apple"}`,
	`{"n":null}`,
	`{}`,
	`{"n":[1,2]}`,
	`{"n":{"deep":1}}`,
}

// Fuzzed over a corpus small enough that a limit really bites: every applied
// window must equal what a fresh query answers, and — checked inside the applier
// on every delivery in this package — every id in order must resolve from a body
// this connection was actually sent.
//
// Deterministic by seed, so a failure is reproducible and a green run is not
// luck about which arrangement came up.
func TestTheWindowInvariantHoldsOverAFuzzedCorpus(t *testing.T) {
	const (
		documents = 7
		limit     = 3
		rounds    = 120
	)

	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace: testDocNS, Collection: testDocColl,
				Fields: []protocol.DocumentFieldSpec{{Name: "n", Type: "number"}},
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("define: %v", protocol.Deref(resp.Error))
	}

	ids := make([]string, documents)
	for i := range ids {
		ids[i] = fmt.Sprintf("d%d", i)
	}

	cap := limit
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "n", Desc: protocol.Ptr(true)}
	q.Limit = &cap

	lq := subscribe(t, d, q)
	lq.next(t)

	rng := rand.New(rand.NewSource(20260805))
	for round := range rounds {
		id := ids[rng.Intn(len(ids))]
		var seq int64
		if rng.Intn(4) == 0 {
			seq = deleteDocSeq(t, d, id)
		} else {
			seq = putDocSeq(t, d, id, windowFuzzBodies[rng.Intn(len(windowFuzzBodies))])
		}
		if seq == 0 {
			// A delete that removed nothing changed nothing, so it wakes
			// nobody and there is no delivery to wait for.
			continue
		}
		w := lq.settle(t, seq)
		if got, want := w.order, queryIDs(t, d, q); !equalStrings(got, want) {
			t.Fatalf("round %d: window = %v, fresh query = %v", round, got, want)
		}
		if len(w.order) > limit {
			t.Fatalf("round %d: window holds %d documents, above the limit of %d", round, len(w.order), limit)
		}
	}
}

// The named fixture behind the fuzz's hardest case, pinned on its own because a
// differential run can only tell you the two answers agreed. A document enters a
// limited window because a DIFFERENT document left it: no fact names the one that
// entered, so nothing derived from the change log alone could have found it.
func TestADocumentEntersTheWindowBecauseAnotherLeft(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"1"}`)
	putDoc(t, d, "b", `{"status":"2"}`)
	putDoc(t, d, "c", `{"status":"3"}`)

	limit := 2
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "status"}
	q.Limit = &limit

	lq := subscribe(t, d, q)
	if got := ids(lq.next(t)); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("initial window = %v, want [a b]", got)
	}

	seq := deleteDocSeq(t, d, "a")
	w := lq.settle(t, seq)
	if got := ids(w); !equalStrings(got, []string{"b", "c"}) {
		t.Fatalf("window after the delete = %v, want [b c]", got)
	}
	if got := w.changed(); !equalStrings(got, []string{"c"}) {
		t.Fatalf("bodies sent = %v, want only the document that entered", got)
	}
}

func idsOf(docs []protocol.StoredDocument) []string {
	out := make([]string, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.ID)
	}
	return out
}
