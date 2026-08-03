package docstore

import (
	"encoding/json"
	"strings"
	"testing"
)

func requestsSchema() CollectionSchema {
	return CollectionSchema{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Fields: []FieldSpec{
			{Name: "status", Type: FieldString},
			{Name: "attempts", Type: FieldNumber},
			{Name: "urgent", Type: FieldBool},
		},
	}
}

func mustCompile(t *testing.T, q Query) Compiled {
	t.Helper()
	c, err := q.Compile(requestsSchema())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

// The proof composition's panel query — pending requests, newest first —
// compiles to a bounded scan within one collection.
func TestPendingRequestsNewestFirstCompiles(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: "status", Op: OpEq, Value: "pending"}},
		Sort:       &Sort{Field: FieldCreatedAt, Desc: true},
		Limit:      20,
	})
	want := "namespace = ? AND collection = ? AND json_extract(body, '$.status') = ?"
	if c.Where != want {
		t.Fatalf("where = %q, want %q", c.Where, want)
	}
	if got := []any{"ext/approval-gate", "requests", "pending"}; !equalArgs(c.Args, got) {
		t.Fatalf("args = %v, want %v", c.Args, got)
	}
	if c.Order != "created_at DESC, id ASC" {
		t.Fatalf("order = %q", c.Order)
	}
	if c.Limit != 20 {
		t.Fatalf("limit = %d", c.Limit)
	}
}

// Every sort carries the document id as a tiebreaker, so documents sharing a
// sort value have a defined relative position and paging cannot skip or repeat.
func TestEverySortIsMadeTotalByTheDocumentID(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "status"},
	})
	if c.Order != "json_extract(body, '$.status') ASC, id ASC" {
		t.Fatalf("order = %q", c.Order)
	}
	// And an unsorted query is still deterministic.
	if c := mustCompile(t, Query{Namespace: "ext/approval-gate", Collection: "requests"}); c.Order != "id ASC" {
		t.Fatalf("default order = %q", c.Order)
	}
}

// created_at and updated_at are real columns, always queryable, never declared —
// "newest first" and "changed since" need no schema.
func TestReservedTimestampsAreQueryableWithoutDeclaration(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: FieldUpdatedAt, Op: OpGt, Value: "2026-08-03T00:00:00Z"}},
	})
	if !strings.HasSuffix(c.Where, "updated_at > ?") {
		t.Fatalf("where = %q, want a bare column comparison", c.Where)
	}
}

// Declaring a field that shadows a reserved column is refused: the column would
// win every query, so the declaration would be a lie.
func TestACollectionCannotDeclareAReservedName(t *testing.T) {
	s := requestsSchema()
	s.Fields = append(s.Fields, FieldSpec{Name: FieldCreatedAt, Type: FieldString})
	err := s.Validate()
	if err == nil {
		t.Fatal("declaring created_at was accepted")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error does not explain the collision: %v", err)
	}
}

// An undeclared field is refused, and the refusal lists what the collection does
// offer — the reader has to be able to fix the query from the error alone.
func TestQueryingAnUndeclaredFieldSaysWhatIsQueryable(t *testing.T) {
	_, err := Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: "priority", Op: OpEq, Value: "high"}},
	}.Compile(requestsSchema())
	if err == nil {
		t.Fatal("filtering an undeclared field was accepted")
	}
	for _, want := range []string{"priority", "attempts", "status", "urgent", FieldCreatedAt, FieldUpdatedAt} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	// The same rule applies to sorting, and the error says which one was wrong.
	_, err = Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "priority"},
	}.Compile(requestsSchema())
	if err == nil || !strings.Contains(err.Error(), "sort") {
		t.Fatalf("sort on an undeclared field: %v", err)
	}
}

// A bound whose type cannot compare with the field's is refused rather than
// silently matching nothing, which is what SQLite would do with a number
// compared against text.
func TestAFilterBoundMustMatchTheDeclaredType(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"number field, string bound", "attempts", "5"},
		{"string field, number bound", "status", 5},
		{"bool field, string bound", "urgent", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Query{
				Namespace:  "ext/approval-gate",
				Collection: "requests",
				Filters:    []Filter{{Field: tc.field, Op: OpEq, Value: tc.value}},
			}.Compile(requestsSchema())
			if err == nil {
				t.Fatal("mismatched bound was accepted")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error does not name the field: %v", err)
			}
		})
	}
}

// Numbers arrive as float64 over the wire and as int from Go callers; booleans
// bind as the 1/0 json_extract yields.
func TestBoundsBindTheWayJSONExtractCompares(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters: []Filter{
			{Field: "attempts", Op: OpGte, Value: 3},
			{Field: "urgent", Op: OpEq, Value: true},
		},
	})
	if got := c.Args[2]; got != float64(3) {
		t.Fatalf("int bound = %#v, want float64(3)", got)
	}
	if got := c.Args[3]; got != 1 {
		t.Fatalf("true bound = %#v, want 1", got)
	}
}

// A query decoded from JSON is the same query — this is the representation the
// sidecar, the UI, and the CLI all carry.
func TestAQueryRoundTripsThroughJSON(t *testing.T) {
	raw := `{"namespace":"ext/approval-gate","collection":"requests",
	         "filters":[{"field":"attempts","op":"gte","value":2}],
	         "sort":{"field":"created_at","desc":true},"limit":5}`
	var q Query
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		t.Fatal(err)
	}
	c, err := q.Compile(requestsSchema())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if c.Limit != 5 || c.Order != "created_at DESC, id ASC" {
		t.Fatalf("compiled = %+v", c)
	}
	if got := c.Args[2]; got != float64(2) {
		t.Fatalf("json number bound = %#v", got)
	}
}

// An unset limit is the default, not "unbounded" — a query with no ceiling is a
// wire message with no ceiling. Asking past the maximum names both numbers and
// the way out.
func TestLimitDefaultsAndItsCeilingNamesTheAsk(t *testing.T) {
	if c := mustCompile(t, Query{Namespace: "ext/approval-gate", Collection: "requests"}); c.Limit != DefaultLimit {
		t.Fatalf("default limit = %d, want %d", c.Limit, DefaultLimit)
	}
	_, err := Query{Namespace: "ext/approval-gate", Collection: "requests", Limit: MaxLimit + 1}.Compile(requestsSchema())
	if err == nil {
		t.Fatal("a limit above the maximum was accepted")
	}
	for _, want := range []string{"1001", "1000"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not carry %s", err, want)
		}
	}
}

// The namespace is an opaque owner/name string. The store validates its shape,
// not which owners exist — who may write under an owner is enforced where the
// namespace is granted.
func TestNamespaceShapeIsTwoParts(t *testing.T) {
	for _, ok := range []string{"ext/approval-gate", "core/tickets", "ext/a"} {
		if err := ValidateNamespace(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "approval-gate", "ext/", "/name", "Ext/Name", "ext/a/b", "ext name/x"} {
		if err := ValidateNamespace(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// A field name is a plain identifier, which is also what makes the compiled JSON
// path safe: there is no quoting to get wrong because there is nothing to quote.
func TestFieldNamesAreIdentifiersSoThePathNeedsNoQuoting(t *testing.T) {
	for _, bad := range []string{"has space", "with'quote", "a.b", "$x", ""} {
		s := requestsSchema()
		s.Fields = []FieldSpec{{Name: bad, Type: FieldString}}
		if err := s.Validate(); err == nil {
			t.Fatalf("field name %q accepted", bad)
		}
	}
}

// A body is any JSON object. Objects only: a declared field is read with a JSON
// path, and an array or scalar has nowhere for one to live.
func TestBodyMustBeAJSONObject(t *testing.T) {
	if err := ValidateBody([]byte(`{"status":"pending"}`)); err != nil {
		t.Fatalf("object rejected: %v", err)
	}
	for _, bad := range []string{``, `[1,2]`, `"text"`, `7`, `{oops}`} {
		if err := ValidateBody([]byte(bad)); err == nil {
			t.Fatalf("body %q accepted", bad)
		}
	}
}

// A query compiled against another collection's declaration is a wiring mistake,
// and refusing it is what keeps a caller from reading one collection under
// another's rules.
func TestAQueryWillNotCompileAgainstAnotherCollectionsDeclaration(t *testing.T) {
	_, err := Query{Namespace: "ext/other", Collection: "requests"}.Compile(requestsSchema())
	if err == nil {
		t.Fatal("mismatched declaration was accepted")
	}
}

func equalArgs(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
