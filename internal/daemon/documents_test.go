package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	testDocNS   = "ext/approval-gate"
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

// liveQuery subscribes and returns a reader for its deliveries plus a stop that
// disconnects the caller and waits for the handler to finish — the real signal
// that the subscription has been torn down.
type liveQuery struct {
	dec  *json.Decoder
	stop func()
}

func subscribe(t *testing.T, d *Daemon, q protocol.DocumentQuery) *liveQuery {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleDocSubscribe(server, &protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: q})
		_ = server.Close()
	}()
	lq := &liveQuery{dec: json.NewDecoder(client)}
	lq.stop = func() {
		_ = client.Close()
		<-done
	}
	t.Cleanup(lq.stop)
	return lq
}

// next reads one delivery. It blocks until the daemon sends, which is what makes
// these tests wait on a real signal rather than on a duration.
func (lq *liveQuery) next(t *testing.T) *protocol.DocSubscribeResult {
	t.Helper()
	var resp protocol.Response
	if err := lq.dec.Decode(&resp); err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("delivery carried an error: %v", protocol.Deref(resp.Error))
	}
	if resp.DocSubscribeResult == nil {
		t.Fatal("delivery carried no result")
	}
	return resp.DocSubscribeResult
}

func ids(result *protocol.DocSubscribeResult) []string {
	out := make([]string, 0, len(result.Documents))
	for _, doc := range result.Documents {
		out = append(out, doc.ID)
	}
	return out
}

func testQuery() protocol.DocumentQuery {
	return protocol.DocumentQuery{Namespace: testDocNS, Collection: testDocColl}
}

// Subscribing delivers the current result set straight away, in the same round
// trip. An extension UI remounts by re-subscribing, so a subscription that only
// promised future updates would render empty until something happened to change.
func TestSubscribingDeliversTheCurrentResultSetImmediately(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "already-here", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	first := lq.next(t)
	if first.Revision != 1 {
		t.Fatalf("first delivery revision = %d, want 1", first.Revision)
	}
	if got := ids(first); len(got) != 1 || got[0] != "already-here" {
		t.Fatalf("first delivery = %v", got)
	}
}

// A write wakes the subscription, and what arrives is the whole current result
// set rather than a description of what changed.
func TestAWriteWakesTheSubscriptionWithTheWholeResultSet(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	lq := subscribe(t, d, testQuery())
	if got := ids(lq.next(t)); len(got) != 0 {
		t.Fatalf("initial delivery = %v, want empty", got)
	}

	putDoc(t, d, "a", `{"status":"pending"}`)
	second := lq.next(t)
	if second.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Revision)
	}
	if got := ids(second); len(got) != 1 || got[0] != "a" {
		t.Fatalf("after write = %v", got)
	}
	if body := second.Documents[0].Body; body != `{"status":"pending"}` {
		t.Fatalf("body = %s", body)
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
	if next.Revision != 2 {
		t.Fatalf("revision = %d, want 2 — the no-op delete delivered", next.Revision)
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
				Namespace: "ext/other", Collection: testDocColl,
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
			Cmd: protocol.CmdDocPut, Namespace: "ext/other", Collection: testDocColl,
			ID: "theirs", Body: `{"status":"pending"}`,
		})
	}); !r.Ok {
		t.Fatalf("put neighbour: %v", protocol.Deref(r.Error))
	}
	putDoc(t, d, "ours", `{"status":"pending"}`)

	next := lq.next(t)
	if next.Revision != 2 {
		t.Fatalf("revision = %d, want 2 — the neighbouring namespace woke this subscription", next.Revision)
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

// A subscription whose query cannot compile is refused at subscribe time. The
// alternative is a subscription that looks accepted and fails on every delivery.
func TestASubscriptionWithAnUnqueryableFieldIsRefusedUpFront(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "undeclared"}
	resp := docCall(t, func(c net.Conn) {
		d.handleDocSubscribe(c, &protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: q})
	})
	if resp.Ok {
		t.Fatal("subscribing with an undeclared sort field succeeded")
	}
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
	if got := ids(lq.next(t)); len(got) != 0 {
		t.Fatalf("after the collection went = %v, want empty", got)
	}
}
