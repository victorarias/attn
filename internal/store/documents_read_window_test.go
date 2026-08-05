package store

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// Answering a query takes three reads — the declaration, the cursor anchor, and
// the SELECT — and the statement is compiled from the first two. While those
// were three separate transactions, a write landing in between produced answers
// that matched no state the collection was ever in, and reported no error:
//
//	anchor deleted mid-read       -> silently empty page, not "cannot page after"
//	anchor gained a sort value    -> the page handed back the anchor itself
//	a field's declared type moved -> the filter silently matched nothing
//
// ReadQuery does all three inside one transaction. These tests pin each of the
// three outcomes, and then the invariant underneath them.

func readIDs(t *testing.T, s *Store, q docstore.Query) ([]string, error) {
	t.Helper()
	read, found, err := s.ReadQuery(q)
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatalf("%s/%s is not declared", q.Namespace, q.Collection)
	}
	ids := make([]string, 0, len(read.Documents))
	for _, d := range read.Documents {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func pagedByAttempts(after string) docstore.Query {
	return docstore.Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Sort:       &docstore.Sort{Field: "attempts"},
		After:      after,
	}
}

// A cursor naming a document that is gone is an error with a way forward, not an
// empty page. An empty page is indistinguishable from "you have reached the end"
// and silently truncates whatever the caller was walking.
func TestPagingAfterADeletedAnchorSaysSoRatherThanEmptyingThePage(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	if _, err := s.DeleteDocument(requestsDecl(t, s), "a", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := readIDs(t, s, pagedByAttempts("a"))
	if err == nil {
		t.Fatalf("paging after a deleted anchor returned %v and no error; it must say the anchor is gone", got)
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("error does not name the missing anchor: %v", err)
	}
}

// The anchor is a position in the order, so a page after it never contains it.
func TestAPageNeverContainsItsOwnAnchor(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{}`,
		"b": `{"attempts":3}`,
		"c": `{"attempts":7}`,
	})

	// The anchor with no value for the sort field sorts first, so the page after
	// it is everything else.
	got, err := readIDs(t, s, pagedByAttempts("a"))
	if err != nil {
		t.Fatalf("page after a null-valued anchor: %v", err)
	}
	if want := []string{"b", "c"}; !sameIDs(got, want) {
		t.Fatalf("page after a null-valued anchor is %v, want %v", got, want)
	}

	// Once it has a value it sorts between b and c, and the page after it shrinks
	// to c. The anchor appears in neither answer.
	if _, err := s.PutDocument(requestsDecl(t, s), "a", []byte(`{"attempts":5}`), base.Add(time.Hour), nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err = readIDs(t, s, pagedByAttempts("a"))
	if err != nil {
		t.Fatalf("page after a valued anchor: %v", err)
	}
	if want := []string{"c"}; !sameIDs(got, want) {
		t.Fatalf("page after a valued anchor is %v, want %v", got, want)
	}
}

// A filter bound under one declared type must never be compared against a column
// that has since been redeclared as another. The affinity changes underneath the
// same column name, so the comparison silently stops matching instead of failing.
func TestAFilterIsBoundUnderTheDeclarationItRunsAgainst(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	numeric := docstore.Query{
		Namespace:  "ext/approval-gate",
		Collection: "requests",
		Filters:    []docstore.Filter{{Field: "attempts", Op: docstore.OpEq, Value: float64(2)}},
	}

	got, err := readIDs(t, s, numeric)
	if err != nil {
		t.Fatalf("numeric filter under a number declaration: %v", err)
	}
	if want := []string{"b"}; !sameIDs(got, want) {
		t.Fatalf("numeric filter matched %v, want %v", got, want)
	}

	next := requestsDeclaration()
	for i := range next.Fields {
		if next.Fields[i].Name == "attempts" {
			next.Fields[i].Type = docstore.FieldString
		}
	}
	if err := s.DefineDocumentCollection(next, base.Add(time.Hour)); err != nil {
		t.Fatalf("redeclare: %v", err)
	}

	got, err = readIDs(t, s, numeric)
	if err == nil {
		t.Fatalf("a numeric filter on a string field returned %v and no error", got)
	}
	if !strings.Contains(err.Error(), "needs a string value") {
		t.Fatalf("error does not name the type mismatch: %v", err)
	}
}

// The invariant the three tests above are instances of, run as the race it
// protects against: a reader and a writer going at the same collection at once.
//
// The writer flips one document between exactly two states, so every answer a
// reader can legitimately get is enumerable — the page after a null-valued
// anchor, or the page after that same anchor once it has a value. Any other
// answer describes a collection that never existed, which is what a read spread
// across several transactions produces. There is no timing here and nothing to
// wait for: the assertion holds for every interleaving, so the test only has to
// run enough of them.
func TestAQueryNeverSeesACollectionThatNeverExisted(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{}`,
		"b": `{"attempts":3}`,
		"c": `{"attempts":7}`,
	})
	schema := requestsDecl(t, s)

	const reads = 3000
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		bodies := []string{`{}`, `{"attempts":5}`}
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if _, err := s.PutDocument(schema, "a", []byte(bodies[i%2]), base.Add(time.Duration(i)*time.Millisecond), nil); err != nil {
				t.Errorf("writer: %v", err)
				return
			}
		}
	}()

	// "a" has no value for attempts, so it sorts first and the page after it is
	// everything else; with a value it sorts between b and c and the page is just
	// c. Nothing else is a state this collection passes through.
	legal := [][]string{{"b", "c"}, {"c"}}
	for i := 0; i < reads; i++ {
		got, err := readIDs(t, s, pagedByAttempts("a"))
		if err != nil {
			close(done)
			wg.Wait()
			t.Fatalf("read %d: %v", i, err)
		}
		ok := false
		for _, want := range legal {
			if sameIDs(got, want) {
				ok = true
				break
			}
		}
		if !ok {
			close(done)
			wg.Wait()
			t.Fatalf("read %d answered %v, which is the page after \"a\" in no state this collection was ever in; legal answers are %v",
				i, got, legal)
		}
	}
	close(done)
	wg.Wait()
}
