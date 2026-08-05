package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The document store's facts are a contract: extensions read this log, and a
// consumer that parses `document.changed` must keep working across releases.
// These pin the name, the subject's shape, the payload, and — the part that is
// not visible from either side alone — that the fact and the write it describes
// are one commit.

func factsOf(t *testing.T, d *Daemon) []store.BusEvent {
	t.Helper()
	events, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

func docFacts(t *testing.T, d *Daemon, name string) []store.BusEvent {
	t.Helper()
	var out []store.BusEvent
	for _, e := range factsOf(t, d) {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// The wire contract of a document write's fact, asserted literally rather than
// through what it happens to wake up.
func TestAWriteAppendsTheDocumentChangedFact(t *testing.T) {
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

	facts := docFacts(t, d, "document.changed")
	if len(facts) != 1 {
		t.Fatalf("one write appended %d fact(s)", len(facts))
	}
	fact := facts[0]
	if fact.Subject != testDocNS+"/"+testDocColl+"/a" {
		t.Fatalf("subject = %q, want the document's address", fact.Subject)
	}

	var payload struct {
		Namespace  string `json:"namespace"`
		Collection string `json:"collection"`
		ID         string `json:"id"`
		Deleted    bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(fact.Payload), &payload); err != nil {
		t.Fatalf("payload %q does not decode: %v", fact.Payload, err)
	}
	if payload.Namespace != testDocNS || payload.Collection != testDocColl || payload.ID != "a" {
		t.Fatalf("payload names %+v", payload)
	}
	if payload.Deleted {
		t.Fatal("a write was announced as a removal")
	}

	// The write's reported position is where its fact landed, which is what
	// makes a caller able to tell whether a later read already includes it.
	if resp.DocPutResult.Seq != int(fact.Seq) {
		t.Fatalf("the put reported seq %d, the fact is at %d", resp.DocPutResult.Seq, fact.Seq)
	}
}

// A removal is the same fact with deleted set, so a consumer can tell one from
// the other without reading the store to find nothing there.
func TestARemovalIsAnnouncedAsADeletion(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	if !deleteDoc(t, d, "a") {
		t.Fatal("delete reported the document was not there")
	}

	facts := docFacts(t, d, "document.changed")
	if len(facts) != 2 {
		t.Fatalf("a write and a removal appended %d fact(s), want 2", len(facts))
	}
	var payload struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(facts[1].Payload), &payload); err != nil {
		t.Fatalf("payload does not decode: %v", err)
	}
	if payload.ID != "a" || !payload.Deleted {
		t.Fatalf("removal announced as %+v", payload)
	}
}

// The behavior that must survive the move to a composite write: a write that
// changed nothing wakes nobody, because it appended nothing.
func TestAWriteThatChangedNothingAppendsNoFact(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	before := len(docFacts(t, d, "document.changed"))

	if deleteDoc(t, d, "gone-already") {
		t.Fatal("deleting a document that was never there reported it existed")
	}

	stale := 99
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"approved"}`, ExpectedRev: &stale,
		})
	})
	if resp.Ok {
		t.Fatal("a stale conditional write was accepted")
	}

	if after := len(docFacts(t, d, "document.changed")); after != before {
		t.Fatalf("writes that changed nothing appended %d fact(s)", after-before)
	}
}

// Undefining a collection announces the collection, not a document. The old
// code reached for document.changed with an empty id, which is a subject no
// consumer can address and no compaction can group.
func TestUndefiningACollectionAnnouncesTheCollection(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"pending"}`)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocUndefine(c, &protocol.DocUndefineMessage{
			Cmd: protocol.CmdDocUndefine, Namespace: testDocNS, Collection: testDocColl,
		})
	})
	if !resp.Ok {
		t.Fatalf("undefine: %v", protocol.Deref(resp.Error))
	}

	removals := docFacts(t, d, "document.collection.removed")
	if len(removals) != 1 {
		t.Fatalf("undefine appended %d removal fact(s), want 1", len(removals))
	}
	if removals[0].Subject != testDocNS+"/"+testDocColl {
		t.Fatalf("subject = %q, want the collection's address", removals[0].Subject)
	}
	var payload struct {
		Namespace  string `json:"namespace"`
		Collection string `json:"collection"`
		Documents  int    `json:"documents"`
	}
	if err := json.Unmarshal([]byte(removals[0].Payload), &payload); err != nil {
		t.Fatalf("payload %q does not decode: %v", removals[0].Payload, err)
	}
	if payload.Namespace != testDocNS || payload.Collection != testDocColl || payload.Documents != 2 {
		t.Fatalf("removal payload = %+v", payload)
	}

	for _, fact := range docFacts(t, d, "document.changed") {
		if fact.Subject == testDocNS+"/"+testDocColl+"/" {
			t.Fatalf("undefine announced a document with no id: %+v", fact)
		}
	}
}

// A failure the caller has to branch on carries a code, so nobody matches on
// message text. The conflict's code brings the structured detail with it: what
// the caller expected, what is actually there, and whether anything is there
// at all — enough to retry without a second round trip.
func TestARefusedWriteCarriesAConflictCode(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	stale := 99
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"approved"}`, ExpectedRev: &stale,
		})
	})
	if resp.Ok {
		t.Fatal("a stale conditional write was accepted")
	}
	if got := protocol.Deref(resp.ErrorCode); got != protocol.ErrorCodeConflict {
		t.Fatalf("error code = %q, want %q", got, protocol.ErrorCodeConflict)
	}
	if resp.ErrorConflict == nil {
		t.Fatal("a conflict carried no structured detail")
	}
	c := resp.ErrorConflict
	if c.Namespace != testDocNS || c.Collection != testDocColl || c.ID != "a" {
		t.Fatalf("conflict names %+v", c)
	}
	if c.Expected != stale || !c.Found || c.Actual != 1 {
		t.Fatalf("conflict reports expected=%d actual=%d found=%v, want 99/1/true",
			c.Expected, c.Actual, c.Found)
	}
	if protocol.Deref(resp.Error) == "" {
		t.Fatal("a coded error dropped its human text")
	}
}

// The other two codes a caller branches on. Reading an undeclared collection is
// a different problem from writing a query the declaration cannot answer, and
// the difference must be readable without parsing prose.
func TestDocumentFailuresCarryTheirOwnCodes(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	undeclared := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{
			Cmd: protocol.CmdDocQuery,
			Query: protocol.DocumentQuery{Namespace: testDocNS, Collection: "nothing-here"},
		})
	})
	if undeclared.Ok {
		t.Fatal("querying an undeclared collection succeeded")
	}
	if got := protocol.Deref(undeclared.ErrorCode); got != protocol.ErrorCodeUndeclaredCollection {
		t.Fatalf("error code = %q, want %q", got, protocol.ErrorCodeUndeclaredCollection)
	}

	invalid := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{
			Cmd: protocol.CmdDocQuery,
			Query: protocol.DocumentQuery{
				Namespace: testDocNS, Collection: testDocColl,
				Filters: []protocol.DocumentFilter{{Field: "not-declared", Op: "eq", ValueJson: `"x"`}},
			},
		})
	})
	if invalid.Ok {
		t.Fatal("a query against an undeclared field succeeded")
	}
	if got := protocol.Deref(invalid.ErrorCode); got != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("error code = %q, want %q", got, protocol.ErrorCodeInvalidQuery)
	}
}

// doc_count answers "how many" without paying for the bodies, and it answers the
// same question the query would: it compiles the caller's own filters.
func TestCountingMatchesWithoutFetchingThem(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"pending"}`)
	putDoc(t, d, "c", `{"status":"approved"}`)

	count := func(q protocol.DocumentQuery) *protocol.DocCountResult {
		t.Helper()
		resp := docCall(t, func(c net.Conn) {
			d.handleDocCount(c, &protocol.DocCountMessage{Cmd: protocol.CmdDocCount, Query: q})
		})
		if !resp.Ok {
			t.Fatalf("count: %v", protocol.Deref(resp.Error))
		}
		if resp.DocCountResult == nil {
			t.Fatal("count returned no result")
		}
		return resp.DocCountResult
	}

	if got := count(testQuery()); got.Count != 3 {
		t.Fatalf("counted %d, want 3", got.Count)
	}

	filtered := testQuery()
	filtered.Filters = []protocol.DocumentFilter{{Field: "status", Op: "eq", ValueJson: `"pending"`}}
	if got := count(filtered); got.Count != 2 {
		t.Fatalf("counted %d pending, want 2", got.Count)
	}

	// A limit decides which matches come back, never how many there are — a
	// count that respected it would answer a question nobody asked.
	limited := testQuery()
	limited.Limit = protocol.Ptr(1)
	if got := count(limited); got.Count != 3 {
		t.Fatalf("counted %d under a limit of 1, want all 3", got.Count)
	}

	if got := count(testQuery()); got.AsOfSeq <= 0 {
		t.Fatalf("a count over a written collection stands at seq %d", got.AsOfSeq)
	}
}

// Every read says where it stands, so a caller that just wrote can tell whether
// the answer already includes its write.
func TestReadsReportThePositionTheyWereTrueAt(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	put := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"pending"}`,
		})
	})
	if !put.Ok {
		t.Fatalf("put: %v", protocol.Deref(put.Error))
	}
	writtenAt := put.DocPutResult.Seq

	query := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: testQuery()})
	})
	if !query.Ok {
		t.Fatalf("query: %v", protocol.Deref(query.Error))
	}
	if query.DocQueryResult.AsOfSeq < writtenAt {
		t.Fatalf("a query returning the write stands at %d, before the write at %d",
			query.DocQueryResult.AsOfSeq, writtenAt)
	}

	get := docCall(t, func(c net.Conn) {
		d.handleDocGet(c, &protocol.DocGetMessage{
			Cmd: protocol.CmdDocGet, Namespace: testDocNS, Collection: testDocColl, ID: "a",
		})
	})
	if !get.Ok {
		t.Fatalf("get: %v", protocol.Deref(get.Error))
	}
	if get.DocGetResult.AsOfSeq < writtenAt {
		t.Fatalf("a get returning the write stands at %d, before the write at %d",
			get.DocGetResult.AsOfSeq, writtenAt)
	}
}
