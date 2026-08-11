package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func requestsDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Fields: []docstore.FieldSpec{
			{Name: "status", Type: docstore.FieldString},
			{Name: "attempts", Type: docstore.FieldNumber},
			{Name: "urgent", Type: docstore.FieldBool},
		},
	}
}

// storeWithRequests returns a store holding the declaration and the documents,
// each written a second apart so created_at ordering is unambiguous.
func storeWithRequests(t *testing.T, bodies map[string]string) (*Store, time.Time) {
	t.Helper()
	s := New()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	i := 0
	for _, id := range sortedKeys(bodies) {
		if _, err := s.PutDocument(declOf(t, s, "app/approval-gate", "requests"), id, []byte(bodies[id]), base.Add(time.Duration(i)*time.Second), nil); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
		i++
	}
	return s, base
}

// declOf reads a collection's declaration. Every document operation takes one,
// because the declaration is what names the table the documents live in — the
// same read the daemon does before touching a collection.
func declOf(t *testing.T, s *Store, namespace, collection string) docstore.CollectionSchema {
	t.Helper()
	schema, ok, err := s.DocumentCollection(namespace, collection)
	if err != nil || !ok {
		t.Fatalf("declaration for %s/%s: ok=%v err=%v", namespace, collection, ok, err)
	}
	return *schema
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func queryIDs(t *testing.T, s *Store, q docstore.Query) []string {
	t.Helper()
	schema, ok, err := s.DocumentCollection(q.Namespace, q.Collection)
	if err != nil || !ok {
		t.Fatalf("declaration for %s/%s: ok=%v err=%v", q.Namespace, q.Collection, ok, err)
	}
	var anchor *docstore.Document
	if q.After != "" {
		doc, found, err := s.GetDocument(*schema, q.After)
		if err != nil {
			t.Fatalf("anchor %s: %v", q.After, err)
		}
		if found {
			anchor = doc
		}
	}
	c, err := q.Compile(*schema, anchor)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	docs, err := s.queryDocuments(c)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids
}

// The declaration survives a round trip, which is what lets a surface that did
// not write it — the CLI, later an extension apply — compile a query against it.
func TestADeclarationRoundTripsThroughTheDatabase(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), now); err != nil {
		t.Fatalf("define: %v", err)
	}
	got, ok, err := s.DocumentCollection("app/approval-gate", "requests")
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if len(got.Fields) != 3 || got.Fields[0].Name != "status" || got.Fields[1].Type != docstore.FieldNumber {
		t.Fatalf("declaration = %+v", got)
	}
	// An undeclared collection is absent, not an error: that is the difference a
	// query against a collection nobody declared has to report.
	if _, ok, err := s.DocumentCollection("app/approval-gate", "nothing"); err != nil || ok {
		t.Fatalf("undeclared collection: ok=%v err=%v", ok, err)
	}
}

// A declaration is replaced, not migrated: adding a queryable field leaves every
// stored document untouched and immediately queryable by the new field.
func TestRedeclaringAddsAQueryableFieldWithoutTouchingDocuments(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1,"urgent":false,"note":"hi"}`,
	})
	schema := requestsDeclaration()
	schema.Fields = append(schema.Fields, docstore.FieldSpec{Name: "note", Type: docstore.FieldString})
	if _, err := s.DefineDocumentCollection(schema, base); err != nil {
		t.Fatalf("redefine: %v", err)
	}
	ids := queryIDs(t, s, docstore.Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []docstore.Filter{{Field: "note", Op: docstore.OpEq, Value: "hi"}},
	})
	if len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("ids = %v, want [a]", ids)
	}
}

// A body is stored byte for byte, undeclared fields included. Nothing rewrites a
// stored document, which is what "no migrations for authors" rests on.
func TestABodyComesBackExactlyAsWritten(t *testing.T) {
	body := `{"status":"pending","nested":{"deep":[1,2,{"x":null}]},"undeclared":"kept"}`
	s, _ := storeWithRequests(t, map[string]string{"a": body})
	doc, ok, err := s.GetDocument(declOf(t, s, "app/approval-gate", "requests"), "a")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if string(doc.Body) != body {
		t.Fatalf("body = %s, want %s", doc.Body, body)
	}
}

// created_at is when the record appeared and survives a replacement; updated_at
// moves. A "newest first" query means the first of those.
func TestReplacingADocumentKeepsCreatedAtAndMovesUpdatedAt(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	later := base.Add(time.Hour)
	if _, err := s.PutDocument(declOf(t, s, "app/approval-gate", "requests"), "a", []byte(`{"status":"approved"}`), later, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	doc, _, err := s.GetDocument(declOf(t, s, "app/approval-gate", "requests"), "a")
	if err != nil {
		t.Fatal(err)
	}
	if !doc.CreatedAt.Equal(base) {
		t.Fatalf("created_at = %s, want %s", doc.CreatedAt, base)
	}
	if !doc.UpdatedAt.Equal(later) {
		t.Fatalf("updated_at = %s, want %s", doc.UpdatedAt, later)
	}
}

// The compiled query actually executes: filters read the body through
// json_extract, the sort orders, and the limit bounds.
func TestFiltersSortAndLimitExecuteAgainstRealJSON(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1,"urgent":false}`,
		"b": `{"status":"pending","attempts":5,"urgent":true}`,
		"c": `{"status":"approved","attempts":2,"urgent":false}`,
		"d": `{"status":"pending","attempts":9,"urgent":true}`,
	})
	ns, coll := "app/approval-gate", "requests"

	if got := queryIDs(t, s, docstore.Query{
		Namespace: ns, Collection: coll,
		Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}},
		Sort:    &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
	}); !equalStrings(got, []string{"d", "b", "a"}) {
		t.Fatalf("pending newest-first = %v", got)
	}

	if got := queryIDs(t, s, docstore.Query{
		Namespace: ns, Collection: coll,
		Filters: []docstore.Filter{{Field: "attempts", Op: docstore.OpGte, Value: 5}},
		Sort:    &docstore.Sort{Field: "attempts"},
	}); !equalStrings(got, []string{"b", "d"}) {
		t.Fatalf("attempts >= 5 = %v", got)
	}

	if got := queryIDs(t, s, docstore.Query{
		Namespace: ns, Collection: coll,
		Filters: []docstore.Filter{{Field: "urgent", Op: docstore.OpEq, Value: true}},
	}); !equalStrings(got, []string{"b", "d"}) {
		t.Fatalf("urgent = %v", got)
	}

	if got := queryIDs(t, s, docstore.Query{
		Namespace: ns, Collection: coll,
		Sort: &docstore.Sort{Field: docstore.FieldCreatedAt}, Limit: 2,
	}); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("limit 2 = %v", got)
	}
}

// The after cursor paginates: the last id of a page names the start of the next.
func TestTheAfterCursorPaginates(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1}`,
		"b": `{"status":"pending","attempts":2}`,
		"c": `{"status":"pending","attempts":3}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 2,
	}
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("first page = %v", got)
	}
	q.After = "b"
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"c"}) {
		t.Fatalf("second page = %v", got)
	}
}

// The boundary a range filter cannot express: every document shares one sort
// value, so the whole ordering is the id tiebreaker. Paged one at a time, the
// cursor must walk every document exactly once — `sort > value` would return
// nothing after the first page and `sort >= value` would return the same
// document forever.
func TestPagingAcrossDocumentsThatShareASortValue(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":7}`,
		"b": `{"status":"pending","attempts":7}`,
		"c": `{"status":"pending","attempts":7}`,
	})
	for _, tc := range []struct {
		name string
		desc bool
		want []string
	}{
		{"ascending", false, []string{"a", "b", "c"}},
		{"descending", true, []string{"c", "b", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := docstore.Query{
				Namespace: "app/approval-gate", Collection: "requests",
				Sort: &docstore.Sort{Field: "attempts", Desc: tc.desc}, Limit: 1,
			}
			var walked []string
			for range tc.want {
				page := queryIDs(t, s, q)
				if len(page) != 1 {
					t.Fatalf("page after %q = %v, want exactly one document", q.After, page)
				}
				walked = append(walked, page[0])
				q.After = page[0]
			}
			if !equalStrings(walked, tc.want) {
				t.Fatalf("walked %v, want %v", walked, tc.want)
			}
			if rest := queryIDs(t, s, q); len(rest) != 0 {
				t.Fatalf("page past the last document = %v, want none", rest)
			}
		})
	}
}

// Ties on a partially shared sort value: the cursor has to cross from one group
// into the next without losing the tied documents on either side of the seam.
func TestPagingCrossesATieGroupBoundary(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1}`,
		"b": `{"status":"pending","attempts":2}`,
		"c": `{"status":"pending","attempts":2}`,
		"d": `{"status":"pending","attempts":3}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 2, After: "b",
	}
	// "b" is the first of the pair sharing attempts=2, so its tie partner "c"
	// must survive the page break.
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"c", "d"}) {
		t.Fatalf("page after the first of a tied pair = %v, want [c d]", got)
	}
	q.After = "c"
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"d"}) {
		t.Fatalf("page after the last of a tied pair = %v, want [d]", got)
	}
}

// A declared field says what may be queried, not what a document must carry, so
// a document missing the sort field is a real row in the ordering. SQLite sorts
// its NULL first ascending and last descending, and the cursor has to agree in
// both directions or the missing-field documents vanish from paging.
func TestPagingOverDocumentsMissingTheSortField(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending"}`,
		"b": `{"status":"pending"}`,
		"c": `{"status":"pending","attempts":5}`,
	})
	for _, tc := range []struct {
		name string
		desc bool
		want []string
	}{
		{"ascending puts the missing field first", false, []string{"a", "b", "c"}},
		{"descending puts it last", true, []string{"c", "b", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := docstore.Query{
				Namespace: "app/approval-gate", Collection: "requests",
				Sort: &docstore.Sort{Field: "attempts", Desc: tc.desc}, Limit: 1,
			}
			var walked []string
			for range tc.want {
				page := queryIDs(t, s, q)
				if len(page) != 1 {
					t.Fatalf("page after %q = %v, want exactly one document", q.After, page)
				}
				walked = append(walked, page[0])
				q.After = page[0]
			}
			if !equalStrings(walked, tc.want) {
				t.Fatalf("walked %v, want %v", walked, tc.want)
			}
			if rest := queryIDs(t, s, q); len(rest) != 0 {
				t.Fatalf("page past the last document = %v, want none", rest)
			}
		})
	}
}

// An unsorted query is ordered by id alone, and the cursor is that one column.
func TestPagingAnUnsortedQuery(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending"}`,
		"b": `{"status":"pending"}`,
		"c": `{"status":"pending"}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Limit: 2, After: "a",
	}
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"b", "c"}) {
		t.Fatalf("page after a = %v", got)
	}
}

// Timestamps are the sort a live panel actually uses, and two documents written
// in the same instant tie there too.
func TestPagingByCreatedAtWithIdenticalTimestamps(t *testing.T) {
	s := New()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.PutDocument(declOf(t, s, "app/approval-gate", "requests"), id, []byte(`{"status":"pending"}`), base, nil); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true}, Limit: 1,
	}
	var walked []string
	for i := 0; i < 3; i++ {
		page := queryIDs(t, s, q)
		if len(page) != 1 {
			t.Fatalf("page after %q = %v, want exactly one document", q.After, page)
		}
		walked = append(walked, page[0])
		q.After = page[0]
	}
	if !equalStrings(walked, []string{"c", "b", "a"}) {
		t.Fatalf("walked %v, want [c b a]", walked)
	}
}

// A cursor pointing at a document that is gone is an error, not an empty page:
// silently returning nothing reads as "end of results" and quietly truncates.
func TestPagingAfterADeletedDocumentSaysSo(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema, _, err := s.DocumentCollection("app/approval-gate", "requests")
	if err != nil {
		t.Fatal(err)
	}
	q := docstore.Query{Namespace: "app/approval-gate", Collection: "requests", After: "gone"}
	if _, err := q.Compile(*schema, nil); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("cursor to a missing document: %v", err)
	}
}

// Namespace isolation is structural: two namespaces holding the same collection
// name never see each other's documents, because namespace is part of every
// address and every statement.
func TestTwoNamespacesWithTheSameCollectionNameStaySeparate(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"shared-id": `{"status":"pending"}`})
	other := requestsDeclaration()
	other.Namespace = "app/other"
	if _, err := s.DefineDocumentCollection(other, base); err != nil {
		t.Fatalf("define other: %v", err)
	}
	if _, err := s.PutDocument(declOf(t, s, "app/other", "requests"), "shared-id", []byte(`{"status":"approved"}`), base, nil); err != nil {
		t.Fatalf("put other: %v", err)
	}

	mine, _, err := s.GetDocument(declOf(t, s, "app/approval-gate", "requests"), "shared-id")
	if err != nil {
		t.Fatal(err)
	}
	if string(mine.Body) != `{"status":"pending"}` {
		t.Fatalf("the other namespace's write reached this one: %s", mine.Body)
	}
	if got := queryIDs(t, s, docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "approved"}},
	}); len(got) != 0 {
		t.Fatalf("query crossed the namespace boundary: %v", got)
	}
	// Deleting one namespace's document leaves the other's alone.
	if _, err := s.DeleteDocument(declOf(t, s, "app/other", "requests"), "shared-id", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetDocument(declOf(t, s, "app/approval-gate", "requests"), "shared-id"); !ok {
		t.Fatal("deleting in one namespace removed the other's document")
	}
}

// A delete reports whether anything was there, so a caller does not announce a
// change that did not happen.
func TestDeleteReportsWhetherADocumentWasThere(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	existed, err := s.DeleteDocument(declOf(t, s, "app/approval-gate", "requests"), "a", nil)
	if err != nil || !existed {
		t.Fatalf("first delete: existed=%v err=%v", existed, err)
	}
	existed, err = s.DeleteDocument(declOf(t, s, "app/approval-gate", "requests"), "a", nil)
	if err != nil || existed {
		t.Fatalf("second delete: existed=%v err=%v", existed, err)
	}
}

// ---------------------------------------------------------------------------
// Revisions and conditional writes
// ---------------------------------------------------------------------------

func rev(n int64) *int64 { return &n }

// requestsDecl is the declaration every conditional-write test writes through.
func requestsDecl(t *testing.T, s *Store) docstore.CollectionSchema {
	t.Helper()
	return declOf(t, s, "app/approval-gate", "requests")
}

// A revision is what a reader is handed and what a writer hands back, so it has
// to advance on every write and be visible on every read.
func TestARevisionAdvancesWithEveryWrite(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	doc, _, err := s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Rev != docstore.FirstRev {
		t.Fatalf("a freshly written document is at rev %d, want %d", doc.Rev, docstore.FirstRev)
	}

	next, err := s.PutDocument(schema, "a", []byte(`{"status":"approved"}`), base.Add(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next != docstore.FirstRev+1 {
		t.Fatalf("put returned rev %d, want %d", next, docstore.FirstRev+1)
	}
	doc, _, err = s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Rev != next {
		t.Fatalf("read back rev %d, want the %d the write reported", doc.Rev, next)
	}
}

// The whole point: a write built on a version somebody else has replaced is
// refused, and the document it would have clobbered is untouched.
func TestAWriteExpectingAStaleRevisionIsRefused(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	read, _, err := s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	// Somebody else writes between that read and the write below.
	if _, err := s.PutDocument(schema, "a", []byte(`{"status":"approved"}`), base.Add(time.Second), nil); err != nil {
		t.Fatal(err)
	}

	_, err = s.PutDocument(schema, "a", []byte(`{"status":"rejected"}`), base.Add(2*time.Second), rev(read.Rev))
	if !docstore.IsConflict(err) {
		t.Fatalf("stale write returned %v, want a conflict", err)
	}
	var conflict *docstore.ConflictError
	errors.As(err, &conflict)
	if conflict.Expected != read.Rev || !conflict.Found || conflict.Actual != read.Rev+1 {
		t.Fatalf("conflict does not name both revisions: %+v", conflict)
	}
	if !strings.Contains(err.Error(), "rev 1") || !strings.Contains(err.Error(), "rev 2") {
		t.Fatalf("conflict message names neither revision: %s", err)
	}

	doc, _, err := s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Body) != `{"status":"approved"}` {
		t.Fatalf("the refused write landed anyway: %s", doc.Body)
	}
	if doc.Rev != read.Rev+1 {
		t.Fatalf("the refused write moved the revision to %d", doc.Rev)
	}
}

func TestAWriteExpectingTheCurrentRevisionIsAccepted(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	read, _, err := s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.PutDocument(schema, "a", []byte(`{"status":"approved"}`), base.Add(time.Second), rev(read.Rev))
	if err != nil {
		t.Fatalf("write at the current revision: %v", err)
	}
	if next != read.Rev+1 {
		t.Fatalf("accepted write returned rev %d, want %d", next, read.Rev+1)
	}
}

// Expecting a revision expects a document. A missing one has to be refused
// rather than created: the caller asked to edit something, not to write it.
func TestExpectingARevisionOnAMissingDocumentCreatesNothing(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	_, err := s.PutDocument(schema, "gone", []byte(`{"status":"pending"}`), base, rev(1))
	if !docstore.IsConflict(err) {
		t.Fatalf("writing a missing document at a revision returned %v, want a conflict", err)
	}
	var conflict *docstore.ConflictError
	errors.As(err, &conflict)
	if conflict.Found {
		t.Fatalf("conflict claims a document was there: %+v", conflict)
	}
	if _, found, err := s.GetDocument(schema, "gone"); err != nil || found {
		t.Fatalf("the refused write created the document: found=%v err=%v", found, err)
	}
}

// Create-only, out of the same field: revisions start at 1, so expecting 0 is
// expecting nothing to be there.
func TestExpectingAbsentCreatesOnlyOnce(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	first, err := s.PutDocument(schema, "a", []byte(`{"status":"first"}`), base, rev(docstore.ExpectAbsent))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first != docstore.FirstRev {
		t.Fatalf("create returned rev %d, want %d", first, docstore.FirstRev)
	}

	_, err = s.PutDocument(schema, "a", []byte(`{"status":"second"}`), base.Add(time.Second), rev(docstore.ExpectAbsent))
	if !docstore.IsConflict(err) {
		t.Fatalf("second create returned %v, want a conflict", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("conflict does not say the document is already there: %s", err)
	}
	doc, _, err := s.GetDocument(schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Body) != `{"status":"first"}` {
		t.Fatalf("the losing create overwrote the winner: %s", doc.Body)
	}
}

func TestDeleteExpectingAStaleRevisionKeepsTheDocument(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	if _, err := s.PutDocument(schema, "a", []byte(`{"status":"approved"}`), base.Add(time.Second), nil); err != nil {
		t.Fatal(err)
	}
	existed, err := s.DeleteDocument(schema, "a", rev(docstore.FirstRev))
	if !docstore.IsConflict(err) {
		t.Fatalf("stale delete returned existed=%v err=%v, want a conflict", existed, err)
	}
	if _, found, _ := s.GetDocument(schema, "a"); !found {
		t.Fatal("the refused delete removed the document anyway")
	}

	if _, err := s.DeleteDocument(schema, "a", rev(docstore.FirstRev+1)); err != nil {
		t.Fatalf("delete at the current revision: %v", err)
	}
	if _, found, _ := s.GetDocument(schema, "a"); found {
		t.Fatal("delete at the current revision left the document")
	}
}

// "Delete this if it is not there" cannot be honoured, and must not quietly
// become an unconditional delete of the document the caller was protecting.
func TestDeleteCannotExpectTheDocumentToBeAbsent(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	_, err := s.DeleteDocument(schema, "a", rev(docstore.ExpectAbsent))
	if err == nil {
		t.Fatal("delete accepted an expectation it cannot act on")
	}
	if docstore.IsConflict(err) {
		t.Fatalf("a nonsensical expectation was reported as a conflict: %v", err)
	}
	if _, found, _ := s.GetDocument(schema, "a"); !found {
		t.Fatal("the refused delete removed the document")
	}
}

// The reason revisions exist, run as the race it protects against: several
// writers reading, changing and writing back the same document at once. Each one
// retries on conflict, and the counter has to end at the number of increments —
// a lost update shows up as a number that is short.
//
// Against a database file rather than the in-memory store the other tests use.
// The in-memory one is pinned to a single connection, so it would serialise the
// writers at the driver and never exercise two of them reaching SQLite at once.
func TestConcurrentReadModifyWritesLoseNoUpdate(t *testing.T) {
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	s, err := NewWithDB(filepath.Join(t.TempDir(), "contention.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := requestsDecl(t, s)
	if _, err := s.PutDocument(schema, "counter", []byte(`{"status":"pending","attempts":0}`), base, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const (
		writers    = 8
		perWriter  = 25
		maxRetries = 1000 // A tripwire: contention this deep means the loop is wrong.
	)

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				var attempt int
				for attempt = 0; attempt < maxRetries; attempt++ {
					doc, found, err := s.GetDocument(schema, "counter")
					if err != nil || !found {
						errs <- fmt.Errorf("writer %d read: found=%v err=%w", w, found, err)
						return
					}
					var body map[string]any
					if err := json.Unmarshal(doc.Body, &body); err != nil {
						errs <- fmt.Errorf("writer %d decode: %w", w, err)
						return
					}
					body["attempts"] = body["attempts"].(float64) + 1
					next, err := json.Marshal(body)
					if err != nil {
						errs <- fmt.Errorf("writer %d encode: %w", w, err)
						return
					}
					_, err = s.PutDocument(schema, "counter", next, base.Add(time.Second), rev(doc.Rev))
					if err == nil {
						break
					}
					if !docstore.IsConflict(err) {
						errs <- fmt.Errorf("writer %d write: %w", w, err)
						return
					}
				}
				if attempt == maxRetries {
					errs <- fmt.Errorf("writer %d gave up after %d conflicts on one increment", w, maxRetries)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	doc, _, err := s.GetDocument(schema, "counter")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(doc.Body, &body); err != nil {
		t.Fatal(err)
	}
	if got := int(body["attempts"].(float64)); got != writers*perWriter {
		t.Fatalf("counter ended at %d after %d increments; %d update(s) were lost",
			got, writers*perWriter, writers*perWriter-got)
	}
	// Every accepted write advanced the revision exactly once, so the revision is
	// an independent count of the writes that landed.
	if want := int64(writers*perWriter) + docstore.FirstRev; doc.Rev != want {
		t.Fatalf("document is at rev %d after %d accepted writes, want %d", doc.Rev, writers*perWriter, want)
	}
}

// Removing a collection takes its documents with it, in one transaction: a
// declaration without documents names nothing, and documents without a
// declaration cannot be queried.
func TestRemovingACollectionTakesItsDocuments(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending"}`,
		"b": `{"status":"pending"}`,
	})
	// Held from before the removal: reading through it afterwards is what proves
	// the storage went, rather than only the registry row that names it.
	schema := declOf(t, s, "app/approval-gate", "requests")

	n, err := s.DeleteDocumentCollection("app/approval-gate", "requests")
	if err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	if n != 2 {
		t.Fatalf("removed %d documents, want 2", n)
	}
	if _, ok, _ := s.DocumentCollection("app/approval-gate", "requests"); ok {
		t.Fatal("declaration survived")
	}
	if _, ok, err := s.GetDocument(schema, "a"); ok || err == nil {
		t.Fatalf("document survived its collection: ok=%v err=%v", ok, err)
	}
}

// The count behind the slow-query receipt sees one collection: two collections
// sharing a name under different namespaces are two tables, and neither counts
// the other's documents.
func TestCountIsScopedToTheCollection(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`, "b": `{"status":"pending"}`})
	other := requestsDeclaration()
	other.Namespace = "app/other"
	if _, err := s.DefineDocumentCollection(other, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutDocument(declOf(t, s, "app/other", "requests"), "z", []byte(`{"status":"x"}`), base, nil); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountDocuments(declOf(t, s, "app/approval-gate", "requests"))
	if err != nil || n != 2 {
		t.Fatalf("count = %d err = %v, want 2", n, err)
	}
}

// A collection with no matching documents returns an empty result, never nil:
// the wire carries an empty list, not a null.
func TestAnEmptyResultIsAnEmptyList(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{})
	schema, _, err := s.DocumentCollection("app/approval-gate", "requests")
	if err != nil {
		t.Fatal(err)
	}
	c, err := docstore.Query{Namespace: "app/approval-gate", Collection: "requests"}.Compile(*schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := s.queryDocuments(c)
	if err != nil {
		t.Fatal(err)
	}
	if docs == nil {
		t.Fatal("nil result set")
	}
	if raw, _ := json.Marshal(docs); string(raw) != "[]" {
		t.Fatalf("marshalled empty result = %s", raw)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Documents and their declarations survive the daemon that wrote them: reopening
// the same database returns both, which is what makes a live query's contents
// durable across a restart rather than an in-memory projection.
func TestDocumentsSurviveReopeningTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	first, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := first.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	if _, err := first.PutDocument(declOf(t, first, "app/approval-gate", "requests"), "a", []byte(`{"status":"pending"}`), base, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	first.Close()

	second, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	schema, ok, err := second.DocumentCollection("app/approval-gate", "requests")
	if err != nil || !ok {
		t.Fatalf("declaration after reopen: ok=%v err=%v", ok, err)
	}
	if len(schema.Fields) != 3 {
		t.Fatalf("declaration lost fields: %+v", schema.Fields)
	}
	if got := queryIDs(t, second, docstore.Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}},
	}); !equalStrings(got, []string{"a"}) {
		t.Fatalf("query after reopen = %v, want [a]", got)
	}
	doc, _, err := second.GetDocument(*schema, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !doc.CreatedAt.Equal(base) {
		t.Fatalf("created_at after reopen = %s, want %s", doc.CreatedAt, base)
	}
}

// A declaration says what may be queried, not what a document must hold, so a
// field declared `number` may legitimately contain an array or an object.
// json_extract yields those as JSON text and orders them with everything else,
// and the cursor has to walk them the same way — binding such a value from Go
// has no bindable equivalent and fails the statement outright.
func TestPagingOverCompoundValuesInADeclaredField(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":[1]}`,
		"b": `{"status":"pending","attempts":[1]}`,
		"c": `{"status":"pending","attempts":{"tries":2}}`,
		"d": `{"status":"pending","attempts":3}`,
	})
	for _, desc := range []bool{false, true} {
		name := "ascending"
		if desc {
			name = "descending"
		}
		t.Run(name, func(t *testing.T) {
			q := docstore.Query{
				Namespace: "app/approval-gate", Collection: "requests",
				Sort: &docstore.Sort{Field: "attempts", Desc: desc}, Limit: 1,
			}
			// Whatever order SQLite gives these four, the walk has to visit each
			// exactly once and then stop — the ordering is SQLite's, the
			// completeness is the cursor's.
			seen := map[string]bool{}
			for i := 0; i < 4; i++ {
				page := queryIDs(t, s, q)
				if len(page) != 1 {
					t.Fatalf("page after %q = %v, want exactly one document", q.After, page)
				}
				if seen[page[0]] {
					t.Fatalf("page after %q returned %q again", q.After, page[0])
				}
				seen[page[0]] = true
				q.After = page[0]
			}
			if len(seen) != 4 {
				t.Fatalf("walked %v, want all four documents", seen)
			}
			if rest := queryIDs(t, s, q); len(rest) != 0 {
				t.Fatalf("page past the last document = %v, want none", rest)
			}
		})
	}
}

// The same for the tie: two documents holding the identical compound value are
// separated by the id half of the tuple, which is the branch a bound value made
// unreachable by failing the statement first.
func TestPagingBetweenTwoIdenticalCompoundValues(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":[1,2]}`,
		"b": `{"status":"pending","attempts":[1,2]}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 1, After: "a",
	}
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"b"}) {
		t.Fatalf("page after the first of an identical pair = %v, want [b]", got)
	}
}

// A stored value that disagrees with the declared type is also ordinary: the
// declaration is not a storage schema. The cursor compares whatever json_extract
// yields, so the walk is complete regardless of how SQLite orders the mix.
func TestPagingWhenStoredValuesDisagreeWithTheDeclaredType(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":"seven"}`,
		"b": `{"status":"pending","attempts":7}`,
		"c": `{"status":"pending","attempts":true}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 1,
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		page := queryIDs(t, s, q)
		if len(page) != 1 {
			t.Fatalf("page after %q = %v, want exactly one document", q.After, page)
		}
		if seen[page[0]] {
			t.Fatalf("page after %q returned %q again", q.After, page[0])
		}
		seen[page[0]] = true
		q.After = page[0]
	}
	if rest := queryIDs(t, s, q); len(rest) != 0 {
		t.Fatalf("page past the last document = %v, want none", rest)
	}
}

// A JSON null is the same absence as a missing key: json_extract yields SQL NULL
// for both, so the cursor's NULL branch has to cover it.
func TestPagingOverAnExplicitJSONNull(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":null}`,
		"b": `{"status":"pending","attempts":2}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 1, After: "a",
	}
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"b"}) {
		t.Fatalf("page after a null-valued document = %v, want [b]", got)
	}
}

// ---------------------------------------------------------------------------
// Physical schema: a table per collection, an indexed column per declared field
// ---------------------------------------------------------------------------

// The point of the whole physical schema: a filtered and sorted query reaches an
// index rather than reading every document. Asserted through the query planner
// because the alternative — timing the query — is the kind of test that passes
// on a fast machine and fails in CI without either result meaning anything.
func TestAQueryOnADeclaredFieldUsesItsIndex(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1}`,
		"b": `{"status":"approved","attempts":2}`,
	})
	schema := declOf(t, s, "app/approval-gate", "requests")

	for _, tc := range []struct {
		name  string
		query docstore.Query
	}{
		{"filter", docstore.Query{
			Namespace: "app/approval-gate", Collection: "requests",
			Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}},
		}},
		{"sort", docstore.Query{
			Namespace: "app/approval-gate", Collection: "requests",
			Sort: &docstore.Sort{Field: "attempts"},
		}},
		{"sort descending", docstore.Query{
			Namespace: "app/approval-gate", Collection: "requests",
			Sort: &docstore.Sort{Field: "attempts", Desc: true},
		}},
		{"reserved sort", docstore.Query{
			Namespace: "app/approval-gate", Collection: "requests",
			Sort: &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := tc.query.Compile(schema, nil)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			plan, err := s.QueryPlan(c)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			joined := strings.Join(plan, " | ")
			if !strings.Contains(joined, "USING INDEX") && !strings.Contains(joined, "USING COVERING INDEX") {
				t.Fatalf("plan does not use an index: %s", joined)
			}
			// A sort served by an index needs no sorting pass. The temp B-tree is
			// what this schema exists to remove, so its absence is the assertion
			// that matters for an ordered query.
			if tc.query.Sort != nil && strings.Contains(joined, "TEMP B-TREE") {
				t.Fatalf("ordered query still sorts in a temp B-tree: %s", joined)
			}
		})
	}
}

// The declared type is not decoration: it is the affinity of the column the
// field is compared through, so a body that stores a number as text still
// compares as a number. Under the shared-table schema this compared as text
// against every real number, which put "10" before 9.
func TestADeclaredTypeDecidesHowStoredValuesCompare(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":9}`,
		"b": `{"status":"pending","attempts":"10"}`,
	})
	got := queryIDs(t, s, docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"},
	})
	if !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("ascending by attempts = %v, want [a b] — \"10\" must order as the number 10", got)
	}
	// And the same value is reachable by a numeric filter, which is the half a
	// caller notices first.
	got = queryIDs(t, s, docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "attempts", Op: docstore.OpGt, Value: 9}},
	})
	if !equalStrings(got, []string{"b"}) {
		t.Fatalf("attempts > 9 = %v, want [b]", got)
	}
}

// Two collections are two tables, so a field name means whatever each of them
// declared it to mean. Sharing one table made every declared name global.
func TestCollectionsWithTheSameFieldNameDoNotShareStorage(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, coll := range []string{"requests", "settings"} {
		schema := docstore.CollectionSchema{
			Namespace: "app/approval-gate", Collection: coll,
			Fields: []docstore.FieldSpec{{Name: "status", Type: docstore.FieldString}},
		}
		if _, err := s.DefineDocumentCollection(schema, now); err != nil {
			t.Fatalf("define %s: %v", coll, err)
		}
		if _, err := s.PutDocument(declOf(t, s, "app/approval-gate", coll), coll+"-1",
			[]byte(`{"status":"`+coll+`"}`), now, nil); err != nil {
			t.Fatalf("put into %s: %v", coll, err)
		}
	}
	for _, coll := range []string{"requests", "settings"} {
		got := queryIDs(t, s, docstore.Query{Namespace: "app/approval-gate", Collection: coll})
		if !equalStrings(got, []string{coll + "-1"}) {
			t.Fatalf("%s holds %v, want only its own document", coll, got)
		}
	}
}

// Redeclaring is DDL, so it has to remove as well as add. A field that leaves
// the declaration stops being queryable; the documents that carried it are
// untouched and it works again the moment it is redeclared.
func TestRedeclaringRemovesAFieldAndCanBringItBack(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1,"urgent":true}`,
	})
	withoutUrgent := requestsDeclaration()
	withoutUrgent.Fields = withoutUrgent.Fields[:2]
	if _, err := s.DefineDocumentCollection(withoutUrgent, base); err != nil {
		t.Fatalf("redeclare without urgent: %v", err)
	}

	schema := declOf(t, s, "app/approval-gate", "requests")
	_, err := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "urgent", Op: docstore.OpEq, Value: true}},
	}.Compile(schema, nil)
	if err == nil {
		t.Fatal("filtering on an undeclared field compiled; it must be rejected")
	}

	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("redeclare with urgent: %v", err)
	}
	got := queryIDs(t, s, docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "urgent", Op: docstore.OpEq, Value: true}},
	})
	if !equalStrings(got, []string{"a"}) {
		t.Fatalf("after redeclaring urgent = %v, want [a] — the stored body never changed", got)
	}
}

// Changing a declared field's type has to move its column, because the column's
// affinity is how two stored values compare. Leaving the old column in place
// would keep comparing numbers as text while the declaration said otherwise.
func TestRedeclaringAFieldsTypeChangesHowItCompares(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":9}`,
		"b": `{"status":"pending","attempts":10}`,
	})
	asText := requestsDeclaration()
	asText.Fields[1] = docstore.FieldSpec{Name: "attempts", Type: docstore.FieldString}
	if _, err := s.DefineDocumentCollection(asText, base); err != nil {
		t.Fatalf("redeclare attempts as string: %v", err)
	}
	got := queryIDs(t, s, docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"},
	})
	if !equalStrings(got, []string{"b", "a"}) {
		t.Fatalf("attempts declared string sorts %v, want [b a] — \"10\" sorts before \"9\" as text", got)
	}
}

// Undefining drops the collection's table, which is what returns the space, and
// declaring the same address again starts empty on a fresh table rather than
// finding the old documents.
func TestUndefiningDropsTheStorageAndRedeclaringStartsEmpty(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending"}`,
		"b": `{"status":"pending"}`,
	})
	before := declOf(t, s, "app/approval-gate", "requests")

	removed, err := s.DeleteDocumentCollection("app/approval-gate", "requests")
	if err != nil {
		t.Fatalf("undefine: %v", err)
	}
	if removed != 2 {
		t.Fatalf("undefine removed %d documents, want 2", removed)
	}
	if _, ok, err := s.DocumentCollection("app/approval-gate", "requests"); err != nil || ok {
		t.Fatalf("declaration after undefine: ok=%v err=%v", ok, err)
	}

	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("redefine: %v", err)
	}
	after := declOf(t, s, "app/approval-gate", "requests")
	if after.Table == before.Table {
		t.Fatalf("redeclared collection reuses table %s; a dropped table's name must not come back", after.Table)
	}
	if got := queryIDs(t, s, docstore.Query{Namespace: "app/approval-gate", Collection: "requests"}); len(got) != 0 {
		t.Fatalf("redeclared collection holds %v, want nothing", got)
	}
}

// A schema that did not come from a read of the registry has no table, and must
// fail loudly rather than reach a statement. This is the check standing between
// the registry and every identifier the store executes.
func TestAnUnmintedSchemaIsRefused(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	unminted := requestsDeclaration() // Table is empty: never read back.
	if _, _, err := s.GetDocument(unminted, "a"); err == nil {
		t.Fatal("a schema with no table was accepted")
	}
	forged := requestsDeclaration()
	forged.Table = "documents; DROP TABLE sessions"
	if _, _, err := s.GetDocument(forged, "a"); err == nil {
		t.Fatal("a forged table name was accepted")
	}
}

// ---------------------------------------------------------------------------
// Migration 89: carrying a populated v88 store across
// ---------------------------------------------------------------------------

// seedV88DocumentStore reshapes a head-schema database back into migration 88's
// document store — one shared `documents` table, a registry with no minting id —
// and rewinds the migration watermark so reopening replays 89 over real rows.
func seedV88DocumentStore(t *testing.T, dbPath string) {
	t.Helper()
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open for seeding: %v", err)
	}
	if _, err := db.Exec(`
		DROP TABLE document_collections;
		CREATE TABLE documents (
		    namespace  TEXT NOT NULL,
		    collection TEXT NOT NULL,
		    id         TEXT NOT NULL,
		    body       TEXT NOT NULL,
		    created_at TEXT NOT NULL,
		    updated_at TEXT NOT NULL,
		    PRIMARY KEY (namespace, collection, id)
		);
		CREATE TABLE document_collections (
		    namespace   TEXT NOT NULL,
		    collection  TEXT NOT NULL,
		    fields_json TEXT NOT NULL,
		    updated_at  TEXT NOT NULL,
		    PRIMARY KEY (namespace, collection)
		);
		INSERT INTO document_collections VALUES
		    ('app/approval-gate', 'requests',
		     '[{"name":"status","type":"string"},{"name":"attempts","type":"number"}]',
		     '2026-08-02T09:00:00Z'),
		    ('app/notes', 'scratch', '[]', '2026-08-02T09:30:00Z');
		INSERT INTO documents VALUES
		    ('app/approval-gate', 'requests', 'r1', '{"status":"open","attempts":2,"note":"kept"}',
		     '2026-08-02T10:00:00Z', '2026-08-02T10:00:00Z'),
		    ('app/approval-gate', 'requests', 'r2', '{"status":"done","attempts":"10"}',
		     '2026-08-02T10:01:00Z', '2026-08-02T10:05:00Z'),
		    ('app/approval-gate', 'requests', 'r3', '{"status":"open","attempts":7}',
		     '2026-08-02T10:02:00Z', '2026-08-02T10:02:00Z'),
		    ('app/notes', 'scratch', 'n1', '{"anything":true}',
		     '2026-08-02T11:00:00Z', '2026-08-02T11:00:00Z'),
		    ('app/ghost', 'lost', 'g1', '{"orphaned":true}',
		     '2026-08-02T12:00:00Z', '2026-08-02T12:00:00Z');
		DELETE FROM schema_migrations WHERE version >= 89;
	`); err != nil {
		t.Fatalf("seed v88 document store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
}

// TestAPopulatedV88StoreIsCarriedIntoItsOwnTables is migration 89's upgrade
// witness. `attn doc define` and `attn doc put` shipped with migration 88, so a
// populated v88 database is something an installed profile can already hold, and
// the rebuild has to bring it forward rather than start it over.
func TestAPopulatedV88StoreIsCarriedIntoItsOwnTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-89.db")
	seedV88DocumentStore(t, dbPath)

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	schema := declOf(t, s, "app/approval-gate", "requests")
	if len(schema.Fields) != 2 || schema.Fields[0].Name != "status" || schema.Fields[1].Type != docstore.FieldNumber {
		t.Fatalf("declaration did not survive: %+v", schema.Fields)
	}

	doc, found, err := s.GetDocument(schema, "r1")
	if err != nil || !found {
		t.Fatalf("r1 after migration: found=%v err=%v", found, err)
	}
	if string(doc.Body) != `{"status":"open","attempts":2,"note":"kept"}` {
		t.Fatalf("body was rewritten: %s", doc.Body)
	}
	want := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	if !doc.CreatedAt.Equal(want) || !doc.UpdatedAt.Equal(want) {
		t.Fatalf("timestamps moved: created=%s updated=%s", doc.CreatedAt, doc.UpdatedAt)
	}

	// The carry builds the collection's columns and indexes from its declaration,
	// so a field declared under v88 is queryable — and indexed — without anyone
	// redeclaring it.
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "open"}},
		Sort:    &docstore.Sort{Field: "attempts"},
	}
	if got := queryIDs(t, s, q); strings.Join(got, ",") != "r1,r3" {
		t.Fatalf("query over carried documents = %v, want [r1 r3]", got)
	}
	c, err := q.Compile(schema, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan, err := s.QueryPlan(c)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if joined := strings.Join(plan, " | "); !strings.Contains(joined, "INDEX") {
		t.Fatalf("carried collection has no index: %s", joined)
	}

	// A collection declaring no fields still gets its table.
	notes := declOf(t, s, "app/notes", "scratch")
	if _, found, err := s.GetDocument(notes, "n1"); err != nil || !found {
		t.Fatalf("n1 after migration: found=%v err=%v", found, err)
	}

	// Documents stored under an address no declaration named cannot happen
	// through the API, but deleting them would be the wrong answer if they ever
	// did: they arrive under an empty declaration, readable and one doc_define
	// away from queryable by field.
	ghost, ok, err := s.DocumentCollection("app/ghost", "lost")
	if err != nil || !ok {
		t.Fatalf("undeclared address was not carried: ok=%v err=%v", ok, err)
	}
	if len(ghost.Fields) != 0 {
		t.Fatalf("undeclared address arrived with fields: %+v", ghost.Fields)
	}
	if _, found, err := s.GetDocument(*ghost, "g1"); err != nil || !found {
		t.Fatalf("g1 after migration: found=%v err=%v", found, err)
	}

	if collections, err := s.ListDocumentCollections(); err != nil || len(collections) != 3 {
		t.Fatalf("collections after migration = %d (err=%v), want 3", len(collections), err)
	}
	if _, err := s.db.Exec(`SELECT 1 FROM documents LIMIT 1`); err == nil {
		t.Fatal("the shared v88 table is still there")
	}
}

// ---------------------------------------------------------------------------
// Migration 90: giving documents already stored a revision
// ---------------------------------------------------------------------------

// seedPreRevisionDocuments builds a database at migration 89's shape — tables
// per collection, but no revision column — with documents in it, and rewinds the
// watermark so reopening replays 90 over real rows.
func seedPreRevisionDocuments(t *testing.T, dbPath string) {
	t.Helper()
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open for seeding: %v", err)
	}
	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("seed declaration: %v", err)
	}
	schema := declOf(t, s, "app/approval-gate", "requests")
	for _, doc := range []struct{ id, body string }{
		{"r1", `{"status":"open","attempts":2}`},
		{"r2", `{"status":"done","attempts":9}`},
	} {
		if _, err := s.PutDocument(schema, doc.id, []byte(doc.body), base, nil); err != nil {
			t.Fatalf("seed %s: %v", doc.id, err)
		}
	}
	// Back to 89: drop the column this migration adds, and forget having run it.
	if _, err := s.db.Exec(`ALTER TABLE ` + schema.Table + ` DROP COLUMN rev;
		DELETE FROM schema_migrations WHERE version >= 90;`); err != nil {
		t.Fatalf("rewind to migration 89: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
}

// Documents written before revisions existed have to come back with one, and be
// usable as the token a conditional write hands in — otherwise the first
// read-modify-write against an upgraded profile is refused forever.
func TestDocumentsStoredBeforeRevisionsGetTheFirstOne(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-90.db")
	seedPreRevisionDocuments(t, dbPath)

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	schema := declOf(t, s, "app/approval-gate", "requests")
	doc, found, err := s.GetDocument(schema, "r1")
	if err != nil || !found {
		t.Fatalf("r1 after migration: found=%v err=%v", found, err)
	}
	if doc.Rev != docstore.FirstRev {
		t.Fatalf("a carried document is at rev %d, want %d", doc.Rev, docstore.FirstRev)
	}
	if string(doc.Body) != `{"status":"open","attempts":2}` {
		t.Fatalf("the migration rewrote a body: %s", doc.Body)
	}

	// The carried revision is a real one: writing against it is accepted, and
	// writing against the revision it replaces is then refused.
	next, err := s.PutDocument(schema, "r1", []byte(`{"status":"closed","attempts":2}`),
		time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC), rev(doc.Rev))
	if err != nil {
		t.Fatalf("conditional write against a carried revision: %v", err)
	}
	if next != docstore.FirstRev+1 {
		t.Fatalf("write returned rev %d, want %d", next, docstore.FirstRev+1)
	}
	if _, err := s.PutDocument(schema, "r1", []byte(`{"status":"stale"}`),
		time.Date(2026, 8, 4, 9, 1, 0, 0, time.UTC), rev(doc.Rev)); !docstore.IsConflict(err) {
		t.Fatalf("a second write at the carried revision returned %v, want a conflict", err)
	}

	// Every document in the collection came across, not just the one read above.
	if got := queryIDs(t, s, docstore.Query{Namespace: "app/approval-gate", Collection: "requests"}); len(got) != 2 {
		t.Fatalf("documents after migration = %v, want both", got)
	}
}
