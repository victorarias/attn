package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func requestsDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "ext/approval-gate",
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
	if err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	i := 0
	for _, id := range sortedKeys(bodies) {
		if err := s.PutDocument("ext/approval-gate", "requests", id, []byte(bodies[id]), base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
		i++
	}
	return s, base
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
	c, err := q.Compile(*schema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	docs, err := s.QueryDocuments(c)
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
	if err := s.DefineDocumentCollection(requestsDeclaration(), now); err != nil {
		t.Fatalf("define: %v", err)
	}
	got, ok, err := s.DocumentCollection("ext/approval-gate", "requests")
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if len(got.Fields) != 3 || got.Fields[0].Name != "status" || got.Fields[1].Type != docstore.FieldNumber {
		t.Fatalf("declaration = %+v", got)
	}
	// An undeclared collection is absent, not an error: that is the difference a
	// query against a collection nobody declared has to report.
	if _, ok, err := s.DocumentCollection("ext/approval-gate", "nothing"); err != nil || ok {
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
	if err := s.DefineDocumentCollection(schema, base); err != nil {
		t.Fatalf("redefine: %v", err)
	}
	ids := queryIDs(t, s, docstore.Query{
		Namespace:  "ext/approval-gate",
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
	doc, ok, err := s.GetDocument("ext/approval-gate", "requests", "a")
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
	if err := s.PutDocument("ext/approval-gate", "requests", "a", []byte(`{"status":"approved"}`), later); err != nil {
		t.Fatalf("replace: %v", err)
	}
	doc, _, err := s.GetDocument("ext/approval-gate", "requests", "a")
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
	ns, coll := "ext/approval-gate", "requests"

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

// A range filter on the sort field is what paginates: the last id of a page is
// the cursor for the next, and the id tiebreaker keeps the order total.
func TestARangeFilterOnTheSortFieldPaginates(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"status":"pending","attempts":1}`,
		"b": `{"status":"pending","attempts":2}`,
		"c": `{"status":"pending","attempts":3}`,
	})
	q := docstore.Query{
		Namespace: "ext/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, Limit: 2,
	}
	first := queryIDs(t, s, q)
	if !equalStrings(first, []string{"a", "b"}) {
		t.Fatalf("first page = %v", first)
	}
	q.Filters = []docstore.Filter{{Field: "attempts", Op: docstore.OpGt, Value: 2}}
	if got := queryIDs(t, s, q); !equalStrings(got, []string{"c"}) {
		t.Fatalf("second page = %v", got)
	}
}

// Namespace isolation is structural: two namespaces holding the same collection
// name never see each other's documents, because namespace is part of every
// address and every statement.
func TestTwoNamespacesWithTheSameCollectionNameStaySeparate(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"shared-id": `{"status":"pending"}`})
	other := requestsDeclaration()
	other.Namespace = "ext/other"
	if err := s.DefineDocumentCollection(other, base); err != nil {
		t.Fatalf("define other: %v", err)
	}
	if err := s.PutDocument("ext/other", "requests", "shared-id", []byte(`{"status":"approved"}`), base); err != nil {
		t.Fatalf("put other: %v", err)
	}

	mine, _, err := s.GetDocument("ext/approval-gate", "requests", "shared-id")
	if err != nil {
		t.Fatal(err)
	}
	if string(mine.Body) != `{"status":"pending"}` {
		t.Fatalf("the other namespace's write reached this one: %s", mine.Body)
	}
	if got := queryIDs(t, s, docstore.Query{
		Namespace: "ext/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "approved"}},
	}); len(got) != 0 {
		t.Fatalf("query crossed the namespace boundary: %v", got)
	}
	// Deleting one namespace's document leaves the other's alone.
	if _, err := s.DeleteDocument("ext/other", "requests", "shared-id"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetDocument("ext/approval-gate", "requests", "shared-id"); !ok {
		t.Fatal("deleting in one namespace removed the other's document")
	}
}

// A delete reports whether anything was there, so a caller does not announce a
// change that did not happen.
func TestDeleteReportsWhetherADocumentWasThere(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	existed, err := s.DeleteDocument("ext/approval-gate", "requests", "a")
	if err != nil || !existed {
		t.Fatalf("first delete: existed=%v err=%v", existed, err)
	}
	existed, err = s.DeleteDocument("ext/approval-gate", "requests", "a")
	if err != nil || existed {
		t.Fatalf("second delete: existed=%v err=%v", existed, err)
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
	n, err := s.DeleteDocumentCollection("ext/approval-gate", "requests")
	if err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	if n != 2 {
		t.Fatalf("removed %d documents, want 2", n)
	}
	if _, ok, _ := s.DocumentCollection("ext/approval-gate", "requests"); ok {
		t.Fatal("declaration survived")
	}
	if _, ok, _ := s.GetDocument("ext/approval-gate", "requests", "a"); ok {
		t.Fatal("document survived its collection")
	}
}

// The count behind the slow-query receipt is per collection, not per table.
func TestCountIsScopedToTheCollection(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`, "b": `{"status":"pending"}`})
	other := requestsDeclaration()
	other.Namespace = "ext/other"
	if err := s.DefineDocumentCollection(other, base); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDocument("ext/other", "requests", "z", []byte(`{"status":"x"}`), base); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountDocuments("ext/approval-gate", "requests")
	if err != nil || n != 2 {
		t.Fatalf("count = %d err = %v, want 2", n, err)
	}
}

// A collection with no matching documents returns an empty result, never nil:
// the wire carries an empty list, not a null.
func TestAnEmptyResultIsAnEmptyList(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{})
	schema, _, err := s.DocumentCollection("ext/approval-gate", "requests")
	if err != nil {
		t.Fatal(err)
	}
	c, err := docstore.Query{Namespace: "ext/approval-gate", Collection: "requests"}.Compile(*schema)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := s.QueryDocuments(c)
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
	if err := first.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	if err := first.PutDocument("ext/approval-gate", "requests", "a", []byte(`{"status":"pending"}`), base); err != nil {
		t.Fatalf("put: %v", err)
	}
	first.Close()

	second, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	schema, ok, err := second.DocumentCollection("ext/approval-gate", "requests")
	if err != nil || !ok {
		t.Fatalf("declaration after reopen: ok=%v err=%v", ok, err)
	}
	if len(schema.Fields) != 3 {
		t.Fatalf("declaration lost fields: %+v", schema.Fields)
	}
	if got := queryIDs(t, second, docstore.Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters:    []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}},
	}); !equalStrings(got, []string{"a"}) {
		t.Fatalf("query after reopen = %v, want [a]", got)
	}
	doc, _, err := second.GetDocument("ext/approval-gate", "requests", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !doc.CreatedAt.Equal(base) {
		t.Fatalf("created_at after reopen = %s, want %s", doc.CreatedAt, base)
	}
}
