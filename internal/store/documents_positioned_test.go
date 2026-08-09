package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// A document write and the fact describing it are one commit, and the fact's
// seq is the write's position. These tests pin both halves of that, including
// the failure direction — which is the inverse of what the code did before:
// a fact that cannot be made durable now fails the write instead of being
// logged and forgotten.

func changeFact(id string, deleted bool) BusEvent {
	payload := fmt.Sprintf(`{"namespace":"ext/approval-gate","collection":"requests","id":%q,"deleted":%t}`, id, deleted)
	return BusEvent{
		Name:    "document.changed",
		Subject: "ext/approval-gate/requests/" + id,
		Payload: payload,
	}
}

func factsOnLog(t *testing.T, s *Store) []BusEvent {
	t.Helper()
	events, err := s.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

// The write's position is the seq its fact landed at, and the fact is really on
// the log — not a number the store made up for the caller.
func TestACommittedWriteReportsWhereItsFactLanded(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`)},
		changeFact("a", false), base)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if written.Rev != docstore.FirstRev {
		t.Fatalf("rev = %d, want %d", written.Rev, docstore.FirstRev)
	}
	if written.Seq == 0 || !written.Changed {
		t.Fatalf("write reported no position: %+v", written)
	}

	events := factsOnLog(t, s)
	if len(events) != 1 {
		t.Fatalf("log holds %d event(s), want 1", len(events))
	}
	if events[0].Seq != written.Seq {
		t.Fatalf("fact is at seq %d, write reported %d", events[0].Seq, written.Seq)
	}
	if events[0].Subject != "ext/approval-gate/requests/a" {
		t.Fatalf("subject = %q", events[0].Subject)
	}
}

// The inverse of the old behavior, and the reason the composite exists: a fact
// that cannot be made durable fails the whole write. The caller gets an error
// and a retry rather than a store nobody was told about.
//
// The failure is injected through the real path — a trigger that refuses every
// insert into bus_events — rather than through a seam, so what is proven is
// that the two statements share a transaction rather than that a fake said so.
func TestAWriteDoesNotSurviveTheFactItCouldNotAppend(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_facts BEFORE INSERT ON bus_events
	                        BEGIN SELECT RAISE(ABORT, 'the log had a bad night'); END`); err != nil {
		t.Fatalf("installing the failing append: %v", err)
	}

	_, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"approved"}`)},
		changeFact("a", false), base.Add(time.Second))
	if err == nil {
		t.Fatal("a write whose fact could not be appended reported success")
	}
	if !strings.Contains(err.Error(), "bad night") {
		t.Fatalf("error does not name the append failure: %v", err)
	}

	doc, found, err := s.GetDocument(schema, "a")
	if err != nil || !found {
		t.Fatalf("re-reading a: found=%v err=%v", found, err)
	}
	if got := string(doc.Body); got != `{"status":"pending"}` {
		t.Fatalf("the document survived a write whose fact did not: body = %s", got)
	}
	if doc.Rev != docstore.FirstRev {
		t.Fatalf("rev = %d, want the write to have left it at %d", doc.Rev, docstore.FirstRev)
	}
}

// The delete half of the same contract: a removal that failed to announce must
// leave the document there.
func TestADeleteDoesNotSurviveTheFactItCouldNotAppend(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_facts BEFORE INSERT ON bus_events
	                        BEGIN SELECT RAISE(ABORT, 'the log had a bad night'); END`); err != nil {
		t.Fatalf("installing the failing append: %v", err)
	}

	if _, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Delete: true},
		changeFact("a", true), base.Add(time.Second)); err == nil {
		t.Fatal("a delete whose fact could not be appended reported success")
	}
	if _, found, err := s.GetDocument(schema, "a"); err != nil || !found {
		t.Fatalf("the document went with a removal that was never announced: found=%v err=%v", found, err)
	}
}

// "Changed" is the whole meaning of the fact, so a write that changed nothing
// must not append one. Two ways to change nothing: remove what is not there,
// and be refused.
func TestAWriteThatChangedNothingAnnouncesNothing(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "never-existed", Delete: true},
		changeFact("never-existed", true), base.Add(time.Second))
	if err != nil {
		t.Fatalf("deleting a missing document: %v", err)
	}
	if written.Changed || written.Seq != 0 {
		t.Fatalf("a delete that removed nothing reported %+v", written)
	}

	stale := docstore.FirstRev + 7
	if _, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"approved"}`), Expected: &stale},
		changeFact("a", false), base.Add(2*time.Second)); !docstore.IsConflict(err) {
		t.Fatalf("a refused write returned %v, want a conflict", err)
	}

	if events := factsOnLog(t, s); len(events) != 0 {
		t.Fatalf("the log holds %d event(s) for writes that changed nothing", len(events))
	}
}

// A read says where it stands. as_of_seq is at least the position of every
// write it includes, which is the comparison a caller makes to know its own
// write is in the answer.
func TestAnAnswerCarriesThePositionItWasTrueAt(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	// An empty log is position 0 — "before everything", a valid answer rather
	// than a missing one.
	read, found, err := s.ReadQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil || !found {
		t.Fatalf("query: found=%v err=%v", found, err)
	}
	if read.AsOfSeq != 0 {
		t.Fatalf("as_of_seq over an empty log = %d, want 0", read.AsOfSeq)
	}

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`)},
		changeFact("a", false), base)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	read, _, err = s.ReadQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if read.AsOfSeq < written.Seq {
		t.Fatalf("a query that returned the write stands at %d, before the write at %d", read.AsOfSeq, written.Seq)
	}
	if len(read.Documents) != 1 {
		t.Fatalf("query returned %d document(s)", len(read.Documents))
	}

	got, declared, err := s.ReadDocument(schema.Namespace, schema.Collection, "a")
	if err != nil || !declared {
		t.Fatalf("get: declared=%v err=%v", declared, err)
	}
	if !got.Found || got.AsOfSeq < written.Seq {
		t.Fatalf("get stands at %d for a write at %d (found=%v)", got.AsOfSeq, written.Seq, got.Found)
	}

	count, declared, err := s.CountQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil || !declared {
		t.Fatalf("count: declared=%v err=%v", declared, err)
	}
	if count.Count != 1 || count.AsOfSeq < written.Seq {
		t.Fatalf("count = %d as of %d, for a write at %d", count.Count, count.AsOfSeq, written.Seq)
	}
}

// Reading an undeclared collection is not a failure to read, it is an answer:
// the collection is not there.
func TestReadingAnUndeclaredCollectionSaysSo(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{})

	if _, declared, err := s.ReadDocument("ext/approval-gate", "nothing-here", "a"); err != nil || declared {
		t.Fatalf("get on an undeclared collection: declared=%v err=%v", declared, err)
	}
	if _, declared, err := s.CountQuery(docstore.Query{
		Namespace: "ext/approval-gate", Collection: "nothing-here",
	}); err != nil || declared {
		t.Fatalf("count on an undeclared collection: declared=%v err=%v", declared, err)
	}
}

// A count is the query's own compile against COUNT(*), so it has to agree with
// the query on every value class the store compares — including the ones where
// the encoding could disagree with the meaning.
//
// The fixtures are chosen for exactly that: stamps inside one second (whose
// stored text sorts wrongly under a naive encoding), a numeric string beside a
// number under a declared number field, and a boolean beside the string "true".
func TestACountAgreesWithTheQueryItCounts(t *testing.T) {
	s := New()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := requestsDecl(t, s)

	// Written inside one second, at fraction widths that a variable-width
	// encoding renders in the wrong order.
	stamps := []time.Duration{0, 123400 * time.Microsecond, 123450 * time.Microsecond, 500 * time.Millisecond}
	bodies := []string{
		`{"status":"pending","attempts":5,"urgent":true}`,
		`{"status":"pending","attempts":"5","urgent":"true"}`,
		`{"status":"approved","attempts":10,"urgent":false}`,
		`{"status":"pending","attempts":[1,2],"urgent":true}`,
	}
	for i, body := range bodies {
		if _, err := s.PutDocument(schema, fmt.Sprintf("d%d", i), []byte(body), base.Add(stamps[i]), nil); err != nil {
			t.Fatalf("put d%d: %v", i, err)
		}
	}

	queries := []docstore.Query{
		{Namespace: schema.Namespace, Collection: schema.Collection},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "attempts", Op: docstore.OpGte, Value: float64(5)}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "urgent", Op: docstore.OpEq, Value: true}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{
				Field: docstore.FieldCreatedAt, Op: docstore.OpGte,
				Value: base.Add(123400 * time.Microsecond).Format(time.RFC3339Nano),
			}}},
	}
	for i, q := range queries {
		// The query is asked for everything it matches, so the two answers are
		// comparable: a limit would make the query's length the limit, not the
		// number of matches.
		q.Limit = docstore.MaxLimit
		read, found, err := s.ReadQuery(q)
		if err != nil || !found {
			t.Fatalf("query %d: found=%v err=%v", i, found, err)
		}
		count, found, err := s.CountQuery(q)
		if err != nil || !found {
			t.Fatalf("count %d: found=%v err=%v", i, found, err)
		}
		if count.Count != len(read.Documents) {
			t.Fatalf("query %d matched %d document(s) but counted %d", i, len(read.Documents), count.Count)
		}
	}
}

// Counting after a cursor counts what paging through the rest of the answer
// would find, which is the only reading of "how many are left" that a caller
// walking pages can use.
func TestACountAfterACursorCountsWhatIsLeft(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	q := docstore.Query{
		Namespace: "ext/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, After: "a", Limit: docstore.MaxLimit,
	}
	count, found, err := s.CountQuery(q)
	if err != nil || !found {
		t.Fatalf("count: found=%v err=%v", found, err)
	}
	if count.Count != 2 {
		t.Fatalf("count after a = %d, want 2", count.Count)
	}
}
