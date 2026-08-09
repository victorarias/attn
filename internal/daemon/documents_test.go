package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	testDocNS   = "app/approval-gate"
	testDocColl = "requests"
)

// docCall runs a one-shot handler over a pipe and returns its response, the way
// an `attn doc` invocation reaches the daemon.
func docCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func defineTestCollection(t *testing.T, d *Daemon) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace:  testDocNS,
				Collection: testDocColl,
				Fields:     []protocol.DocumentFieldSpec{{Name: "status", Type: "string"}},
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("define: %v", protocol.Deref(resp.Error))
	}
}

func putDoc(t *testing.T, d *Daemon, id, body string) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl, ID: id, Body: body,
		})
	})
	if !resp.Ok {
		t.Fatalf("put %s: %v", id, protocol.Deref(resp.Error))
	}
}

func deleteDoc(t *testing.T, d *Daemon, id string) bool {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDelete(c, &protocol.DocDeleteMessage{
			Cmd: protocol.CmdDocDelete, Namespace: testDocNS, Collection: testDocColl, ID: id,
		})
	})
	if !resp.Ok {
		t.Fatalf("delete %s: %v", id, protocol.Deref(resp.Error))
	}
	return resp.DocDeleteResult.Existed
}

// window is one delivery as the subscriber sees it once the client rule has been
// applied: the wire's own order and upsert, plus the documents those resolve to.
type window struct {
	delivery  int
	asOfSeq   int64
	order     []string
	upsert    []protocol.StoredDocument
	documents []protocol.StoredDocument
}

// liveQuery subscribes and returns a reader for its deliveries plus a stop that
// disconnects the caller and waits for the handler to finish — the real signal
// that the subscription has been torn down.
//
// It applies the client rule itself rather than calling internal/client's
// applier: this is a second implementation of the same three sentences, and two
// implementations disagreeing is the thing the invariant exists to catch. The
// resolution below is also where the invariant is checked — every id in order
// must resolve from the bodies this connection has been sent — so every test in
// this file that reads a delivery is asserting it.
type liveQuery struct {
	dec  *json.Decoder
	stop func()
	held map[string]protocol.StoredDocument
}

func subscribe(t *testing.T, d *Daemon, q protocol.DocumentQuery) *liveQuery {
	t.Helper()
	return subscribeResuming(t, d, q, nil)
}

// subscribeResuming subscribes declaring what the caller already holds. held
// seeds the applier's cache so a resumed subscription's deliveries resolve the
// same way a reconnecting client's would.
func subscribeResuming(t *testing.T, d *Daemon, q protocol.DocumentQuery, held []protocol.StoredDocument) *liveQuery {
	t.Helper()
	have := make([]protocol.DocumentRevision, 0, len(held))
	cache := make(map[string]protocol.StoredDocument, len(held))
	for _, doc := range held {
		have = append(have, protocol.DocumentRevision{ID: doc.ID, Rev: doc.Rev})
		cache[doc.ID] = doc
	}
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleDocSubscribe(server, &protocol.DocSubscribeMessage{
			Cmd: protocol.CmdDocSubscribe, Query: q, Have: have,
		})
		_ = server.Close()
	}()
	lq := &liveQuery{dec: json.NewDecoder(client), held: cache}
	lq.stop = func() {
		_ = client.Close()
		<-done
	}
	t.Cleanup(lq.stop)
	return lq
}

// next reads one delivery and applies it. It blocks until the daemon sends,
// which is what makes these tests wait on a real signal rather than on a
// duration.
func (lq *liveQuery) next(t *testing.T) window {
	t.Helper()
	resp := lq.nextRaw(t)
	if !resp.Ok {
		t.Fatalf("delivery carried an error: %v", protocol.Deref(resp.Error))
	}
	if resp.DocSubscribeResult == nil {
		t.Fatal("delivery carried no result")
	}
	return lq.apply(t, resp.DocSubscribeResult)
}

// apply is the client rule: render order, take each body from upsert if it is
// there and from the cache otherwise, forget everything not in order.
func (lq *liveQuery) apply(t *testing.T, result *protocol.DocSubscribeResult) window {
	t.Helper()
	arrived := make(map[string]protocol.StoredDocument, len(result.Upsert))
	for _, doc := range result.Upsert {
		arrived[doc.ID] = doc
	}
	out := window{
		delivery: result.Delivery,
		asOfSeq:  int64(result.AsOfSeq),
		order:    result.Order,
		upsert:   result.Upsert,
	}
	next := make(map[string]protocol.StoredDocument, len(result.Order))
	for _, id := range result.Order {
		doc, ok := arrived[id]
		if !ok {
			doc, ok = lq.held[id]
		}
		if !ok {
			t.Fatalf("delivery %d ordered %q but neither sent its body nor had this subscriber been given one: %v",
				result.Delivery, id, result.Order)
		}
		next[id] = doc
		out.documents = append(out.documents, doc)
	}
	lq.held = next
	return out
}

// nextRaw reads one delivery without requiring it to have succeeded, for the
// cases where the end of a subscription is the thing under test.
func (lq *liveQuery) nextRaw(t *testing.T) protocol.Response {
	t.Helper()
	var resp protocol.Response
	if err := lq.dec.Decode(&resp); err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	return resp
}

// changed names the ids whose bodies travelled in this delivery.
func (w window) changed() []string {
	out := make([]string, 0, len(w.upsert))
	for _, doc := range w.upsert {
		out = append(out, doc.ID)
	}
	return out
}

func ids(w window) []string { return w.order }

func testQuery() protocol.DocumentQuery {
	return protocol.DocumentQuery{Namespace: testDocNS, Collection: testDocColl}
}

// Subscribing delivers the query's current window straight away, in the same
// round trip. An extension UI remounts by re-subscribing, so a subscription that
// only promised future updates would render empty until something happened to
// change.
func TestSubscribingDeliversTheCurrentWindowImmediately(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "already-here", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	first := lq.next(t)
	if first.delivery != 1 {
		t.Fatalf("first delivery = %d, want 1", first.delivery)
	}
	if got := ids(first); len(got) != 1 || got[0] != "already-here" {
		t.Fatalf("first delivery = %v", got)
	}
}

// A write wakes the subscription, and what arrives is the query's whole current
// order — never a patch the subscriber has to merge — plus the one body it does
// not hold.
func TestAWriteWakesTheSubscriptionWithTheCurrentWindow(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	lq := subscribe(t, d, testQuery())
	if got := ids(lq.next(t)); len(got) != 0 {
		t.Fatalf("initial delivery = %v, want empty", got)
	}

	putDoc(t, d, "a", `{"status":"pending"}`)
	second := lq.next(t)
	if second.delivery != 2 {
		t.Fatalf("delivery = %d, want 2", second.delivery)
	}
	if got := ids(second); len(got) != 1 || got[0] != "a" {
		t.Fatalf("after write = %v", got)
	}
	if body := second.documents[0].Body; body != `{"status":"pending"}` {
		t.Fatalf("body = %s", body)
	}
	if got := second.changed(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("bodies sent = %v, want just the document that changed", got)
	}
}

// A delete that removed nothing changed nothing, so it must not wake anybody.
// Proven without waiting on the absence of a message: the next delivery after a
// no-op delete plus a real write is revision 2, which it could not be if the
// no-op had also delivered.
func TestANoOpDeleteDoesNotWakeSubscribers(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	lq := subscribe(t, d, testQuery())
	lq.next(t)

	if existed := deleteDoc(t, d, "never-existed"); existed {
		t.Fatal("deleting a missing document reported it existed")
	}
	putDoc(t, d, "a", `{"status":"pending"}`)

	next := lq.next(t)
	if next.delivery != 2 {
		t.Fatalf("delivery = %d, want 2 — the no-op delete delivered", next.delivery)
	}
	if got := ids(next); len(got) != 1 || got[0] != "a" {
		t.Fatalf("delivery = %v", got)
	}
}

// A removal reaches a live query: what it renders must never outlive the store.
func TestARemovalReachesTheLiveQuery(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	lq := subscribe(t, d, testQuery())
	if got := ids(lq.next(t)); len(got) != 1 {
		t.Fatalf("initial = %v", got)
	}
	if existed := deleteDoc(t, d, "a"); !existed {
		t.Fatal("delete reported nothing was there")
	}
	if got := ids(lq.next(t)); len(got) != 0 {
		t.Fatalf("after removal = %v, want empty", got)
	}
}

// A subscription belongs to one collection. A write next door must not wake it —
// otherwise every extension pays for every other extension's write traffic, and
// namespace isolation stops meaning anything on the live path.
func TestASubscriptionOnlyWakesForItsOwnCollection(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace: "app/other", Collection: testDocColl,
				Fields: []protocol.DocumentFieldSpec{{Name: "status", Type: "string"}},
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("define neighbour: %v", protocol.Deref(resp.Error))
	}

	lq := subscribe(t, d, testQuery())
	lq.next(t)

	// A write to the neighbouring namespace, then one to ours. If the neighbour
	// had woken us, this delivery would be revision 3.
	if r := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: "app/other", Collection: testDocColl,
			ID: "theirs", Body: `{"status":"pending"}`,
		})
	}); !r.Ok {
		t.Fatalf("put neighbour: %v", protocol.Deref(r.Error))
	}
	putDoc(t, d, "ours", `{"status":"pending"}`)

	next := lq.next(t)
	if next.delivery != 2 {
		t.Fatalf("delivery = %d, want 2 — the neighbouring namespace woke this subscription", next.delivery)
	}
	if got := ids(next); len(got) != 1 || got[0] != "ours" {
		t.Fatalf("delivery = %v — it crossed the namespace boundary", got)
	}
}

// A caller that disconnects takes its subscription with it. A leaked one would
// re-run its query on every write for the life of the daemon.
func TestASubscriptionIsGoneWhenItsCallerDisconnects(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	lq := subscribe(t, d, testQuery())
	lq.next(t)
	if n := d.documentSubscriptionCount(); n != 1 {
		t.Fatalf("subscriptions = %d, want 1", n)
	}
	lq.stop() // closes the caller and waits for the handler to return
	if n := d.documentSubscriptionCount(); n != 0 {
		t.Fatalf("subscriptions after disconnect = %d, want 0", n)
	}
}

// The bus holds its publish lock across fan-out, so a subscriber that is not
// draining must never be able to hold up a writer. This test would hang rather
// than fail if the fan-out ever wrote to the socket itself.
func TestWritesDoNotBlockOnASubscriberThatIsNotReading(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	lq := subscribe(t, d, testQuery())
	lq.next(t)

	// Nothing reads from here on; every one of these must still return.
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		putDoc(t, d, id, `{"status":"pending"}`)
	}
}

// A query against a collection nobody declared names the collection and the way
// out, because the reader fixing it is an agent with only the error to go on.
func TestReadingAnUndeclaredCollectionSaysHowToDeclareIt(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{
			Cmd: protocol.CmdDocQuery, Query: protocol.DocumentQuery{Namespace: testDocNS, Collection: "nope"},
		})
	})
	if resp.Ok {
		t.Fatal("querying an undeclared collection succeeded")
	}
	msg := protocol.Deref(resp.Error)
	for _, want := range []string{testDocNS, "nope", "doc define"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}

// A subscription whose query cannot be answered is refused at subscribe time,
// by the same read that would have served it. The alternative is a subscription
// that looks accepted and fails on every delivery.
//
// The refusal carries invalid_query rather than either subscription-ending code:
// nothing happened to this subscription, the caller asked for something the
// collection does not offer, and resubscribing with a corrected query works.
func TestASubscriptionWithAnUnqueryableFieldIsRefusedUpFront(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "undeclared"}

	lq := subscribe(t, d, q)
	resp := lq.nextRaw(t)
	if resp.Ok {
		t.Fatal("subscribing with an undeclared sort field succeeded")
	}
	if code := protocol.Deref(resp.ErrorCode); code != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("error code = %q, want %q", code, protocol.ErrorCodeInvalidQuery)
	}
	// stop waits for the handler to return, which is when a registered
	// subscription would have been removed. Registering before the first read is
	// deliberate — it is what keeps a write landing mid-subscribe from being
	// missed — so what has to hold is that a refusal still leaves nothing behind.
	lq.stop()
	if n := d.documentSubscriptionCount(); n != 0 {
		t.Fatalf("a refused subscribe left %d subscriptions behind", n)
	}
}

// A filter's bound travels as JSON text and is decoded against the field's
// declared type, so the wire form reaches the same rules the Go form does.
func TestAFilterBoundArrivesAsJSONAndIsTypeChecked(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"approved"}`)

	q := testQuery()
	q.Filters = []protocol.DocumentFilter{{Field: "status", Op: "eq", ValueJson: `"pending"`}}
	resp := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
	})
	if !resp.Ok {
		t.Fatalf("query: %v", protocol.Deref(resp.Error))
	}
	if docs := resp.DocQueryResult.Documents; len(docs) != 1 || docs[0].ID != "a" {
		t.Fatalf("documents = %+v", docs)
	}

	// A number against a string field is refused rather than matching nothing.
	q.Filters = []protocol.DocumentFilter{{Field: "status", Op: "eq", ValueJson: `5`}}
	if r := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
	}); r.Ok {
		t.Fatal("a number bound against a string field was accepted")
	}
}

// Removing a collection tells its live queries, so nothing keeps rendering
// records the store no longer holds.
func TestRemovingACollectionReachesItsLiveQueries(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	lq := subscribe(t, d, testQuery())
	if got := ids(lq.next(t)); len(got) != 1 {
		t.Fatalf("initial = %v", got)
	}

	resp := docCall(t, func(c net.Conn) {
		d.handleDocUndefine(c, &protocol.DocUndefineMessage{
			Cmd: protocol.CmdDocUndefine, Namespace: testDocNS, Collection: testDocColl,
		})
	})
	if !resp.Ok {
		t.Fatalf("undefine: %v", protocol.Deref(resp.Error))
	}
	if n := resp.DocUndefineResult.DocumentsRemoved; n != 1 {
		t.Fatalf("removed %d documents, want 1", n)
	}
	// The watcher is told the collection is gone, not handed an empty list: an
	// empty result set claims the collection is still there holding nothing, and
	// leaves the caller watching an address that can never answer again.
	final := lq.nextRaw(t)
	if final.Ok {
		t.Fatalf("after the collection went, delivery = %+v, want an error", final)
	}
	if msg := protocol.Deref(final.Error); !strings.Contains(msg, "is not declared") {
		t.Fatalf("error does not say the collection is gone: %q", msg)
	}
	// The code is what a UI host branches on: collection_undefined means the
	// tile is dead, not that its query was wrong.
	if code := protocol.Deref(final.ErrorCode); code != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("error code = %q, want %q", code, protocol.ErrorCodeCollectionUndefined)
	}
}

// The after cursor survives the wire and pages a real tie. `attn doc query
// --after` is the reachable form of the only pagination the store offers, so it
// has to be proven from the message in rather than from the compiler alone.
func TestPagingWithTheAfterCursorOverTheWire(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	for _, id := range []string{"a", "b", "c"} {
		putDoc(t, d, id, `{"status":"pending"}`)
	}

	limit := 1
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "status"}
	q.Limit = &limit

	var walked []string
	for range []string{"a", "b", "c"} {
		resp := docCall(t, func(c net.Conn) {
			d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
		})
		if !resp.Ok {
			t.Fatalf("query after %v: %v", q.After, protocol.Deref(resp.Error))
		}
		docs := resp.DocQueryResult.Documents
		if len(docs) != 1 {
			t.Fatalf("page after %v = %d documents, want 1", q.After, len(docs))
		}
		walked = append(walked, docs[0].ID)
		next := docs[0].ID
		q.After = &next
	}
	if strings.Join(walked, ",") != "a,b,c" {
		t.Fatalf("walked %v, want [a b c]", walked)
	}
}

// A cursor to a document that has been deleted fails the query rather than
// returning an empty page that reads as the end of the walk.
func TestAnAfterCursorToADeletedDocumentIsReported(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	gone := "b"
	q := testQuery()
	q.After = &gone
	resp := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
	})
	if resp.Ok {
		t.Fatal("a cursor to a missing document was accepted")
	}
	if err := protocol.Deref(resp.Error); !strings.Contains(err, "b") || !strings.Contains(err, "no longer exists") {
		t.Fatalf("error = %q", err)
	}
}

// The other way a subscription's collection can move under it: a redeclare that
// drops a field the live query filters on. The field's column goes with it, so
// continuing would mean answering a question the collection no longer offers.
// The watcher is told which field, the same way a fresh query would be.
func TestRedeclaringWithoutAFieldEndsTheLiveQueriesUsingIt(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	q := testQuery()
	q.Filters = []protocol.DocumentFilter{{Field: "status", Op: "eq", ValueJson: `"pending"`}}
	lq := subscribe(t, d, q)
	if got := ids(lq.next(t)); !equalStrings(got, []string{"a"}) {
		t.Fatalf("initial = %v, want [a]", got)
	}

	// Redeclared with no queryable fields at all.
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace: testDocNS, Collection: testDocColl,
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("redeclare: %v", protocol.Deref(resp.Error))
	}
	// No write follows: the redeclare itself must wake the subscription and end
	// it. A quiet collection whose declaration moved is exactly the case where
	// waiting for the next document change would mean waiting forever.
	final := lq.nextRaw(t)
	if final.Ok {
		t.Fatalf("delivery after the field went = %+v, want an error", final)
	}
	if msg := protocol.Deref(final.Error); !strings.Contains(msg, "status") {
		t.Fatalf("error does not name the field that went: %q", msg)
	}
	// collection_redeclared rather than collection_undefined: the collection is
	// still there, and it is the query that can never be answered again.
	if code := protocol.Deref(final.ErrorCode); code != protocol.ErrorCodeCollectionRedeclared {
		t.Fatalf("error code = %q, want %q", code, protocol.ErrorCodeCollectionRedeclared)
	}
}

// ---------------------------------------------------------------------------
// Conditional writes over the wire
// ---------------------------------------------------------------------------

// putDocExpecting writes with an expectation and returns the raw response, so a
// test can assert on a refusal rather than fail on one.
func putDocExpecting(t *testing.T, d *Daemon, id, body string, expected int) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: id, Body: body, ExpectedRev: &expected,
		})
	})
}

func getDoc(t *testing.T, d *Daemon, id string) *protocol.StoredDocument {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocGet(c, &protocol.DocGetMessage{
			Cmd: protocol.CmdDocGet, Namespace: testDocNS, Collection: testDocColl, ID: id,
		})
	})
	if !resp.Ok {
		t.Fatalf("get %s: %v", id, protocol.Deref(resp.Error))
	}
	if !resp.DocGetResult.Found {
		return nil
	}
	return resp.DocGetResult.Document
}

// A write reports the revision it produced, and a read reports the revision it
// read, so a caller never has to ask for one separately.
func TestAWriteAndAReadAgreeOnTheRevision(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"pending"}`,
		})
	})
	if !resp.Ok {
		t.Fatalf("put: %v", protocol.Deref(resp.Error))
	}
	if resp.DocPutResult.Rev != 1 {
		t.Fatalf("put reported rev %d, want 1", resp.DocPutResult.Rev)
	}
	if doc := getDoc(t, d, "a"); doc == nil || doc.Rev != 1 {
		t.Fatalf("get reported %+v, want rev 1", doc)
	}
}

// The refusal has to arrive as an error a caller can act on, and the document it
// would have replaced has to be exactly as it was.
func TestAStaleConditionalWriteIsRefusedOverTheWire(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "a", `{"status":"approved"}`)

	resp := putDocExpecting(t, d, "a", `{"status":"rejected"}`, 1)
	if resp.Ok {
		t.Fatal("a write against a replaced revision was accepted")
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, "rev 1") || !strings.Contains(msg, "rev 2") {
		t.Fatalf("refusal does not name both revisions: %s", msg)
	}

	doc := getDoc(t, d, "a")
	if doc == nil || doc.Body != `{"status":"approved"}` || doc.Rev != 2 {
		t.Fatalf("the refused write changed the document: %+v", doc)
	}
}

// A refused write changed nothing, so it must not wake the collection's live
// queries: a delivery means "this result set is new", and re-rendering an
// identical one on every rejected write is the cost the conditional write exists
// to avoid.
func TestARefusedWriteDoesNotWakeSubscribers(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	lq := subscribe(t, d, testQuery())
	lq.next(t)

	if resp := putDocExpecting(t, d, "a", `{"status":"rejected"}`, 99); resp.Ok {
		t.Fatal("a write against a revision that never existed was accepted")
	}
	putDoc(t, d, "b", `{"status":"pending"}`)

	// The next delivery is the one the accepted write caused, so it carries both
	// documents. A delivery holding only the first means the refused write woke
	// the subscription and this is its result set, with the real one still queued.
	next := lq.next(t)
	if got := ids(next); len(got) != 2 {
		t.Fatalf("first delivery after the refused write = %v, want both documents", got)
	}
	if next.delivery != 2 {
		t.Fatalf("delivery = %d, want 2 — something delivered in between", next.delivery)
	}
}

// Create-only, over the wire: the second create loses and the first document
// survives it.
func TestCreateOnlyIsRefusedWhenTheDocumentIsAlreadyThere(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	if resp := putDocExpecting(t, d, "a", `{"status":"first"}`, 0); !resp.Ok {
		t.Fatalf("first create: %v", protocol.Deref(resp.Error))
	}
	resp := putDocExpecting(t, d, "a", `{"status":"second"}`, 0)
	if resp.Ok {
		t.Fatal("a second create was accepted")
	}
	if doc := getDoc(t, d, "a"); doc == nil || doc.Body != `{"status":"first"}` {
		t.Fatalf("the losing create overwrote the winner: %+v", doc)
	}
}

// A conditional delete refuses the same way, and leaves the document behind.
func TestAStaleConditionalDeleteIsRefusedOverTheWire(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "a", `{"status":"approved"}`)

	stale := 1
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDelete(c, &protocol.DocDeleteMessage{
			Cmd: protocol.CmdDocDelete, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", ExpectedRev: &stale,
		})
	})
	if resp.Ok {
		t.Fatal("a delete against a replaced revision was accepted")
	}
	if doc := getDoc(t, d, "a"); doc == nil {
		t.Fatal("the refused delete removed the document")
	}
}
