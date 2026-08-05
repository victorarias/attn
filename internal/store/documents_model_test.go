package store

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// Differential harness for the document query compiler.
//
// The example tests above prove the query rules on cases someone thought of.
// This asks the same question a second way and checks the two answers match,
// over combinations nobody enumerated by hand.
//
// The second way is deliberately dumb: a shadow table with the same declared
// affinities but no generated columns and no indexes, populated by SQL out of
// the real table, queried with a hand-written statement and paginated by slicing
// a Go array. Everything the real path is clever about — the generated column,
// its index, and the tuple comparison that implements the After cursor — the
// shadow does the slow obvious way instead.
//
// It is deliberately NOT a Go re-implementation of the query semantics. The
// fiddly rules here are SQLite's — how "5" compares against 5, where a missing
// value sorts — and writing down our understanding of them in Go would mostly
// test that understanding. Both answers come out of SQLite, so nothing here
// models SQLite; what is compared is only the machinery we built on top of it.
//
// Three legs, each covering what the others structurally cannot:
//
//   - the sweep, which is every arrangement of a small alphabet across four
//     documents against every query — exhaustive, so no seed and no luck;
//   - the large corpus, big enough that SQLite reaches for an index, which is a
//     different code path over the same column and only exists at size;
//   - the moving corpus, where documents are written, deleted and the collection
//     redeclared while the queries run.
//
// What none of them catch: a misunderstanding shared by both paths. If the
// shadow orders the way the compiler orders and both are not what we meant, they
// agree and are both wrong. The named example tests above are what pin the
// intent; this covers the combinations those tests structurally cannot.

// modelField is the one declared field the exhaustive sweep varies. A single
// number field is the richest choice for the money: "number" is the type whose
// column affinity actually converts, so an alphabet over it reaches text that
// looks numeric, text that does not, and values with no numeric reading at all.
const modelField = "n"

func modelDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "ext/model",
		Collection: "docs",
		Fields:     []docstore.FieldSpec{{Name: modelField, Type: docstore.FieldNumber}},
	}
}

// modelBodies is the alphabet a document's body is drawn from. Every entry is a
// case the compiler's comments claim to handle, and the interesting ones are the
// ragged three: a declared field says what may be QUERIED, not what a document
// must contain, so a "number" field legitimately holds text, nothing, or a list.
//
// Keep this small and deliberate. The sweep is every arrangement of it, so one
// more entry multiplies the whole run.
var modelBodies = []string{
	`{"n":1}`,
	`{"n":2}`,
	`{"n":"1"}`,
	`{"n":null}`,
	`{}`,
	`{"n":[1]}`,
}

// modelIDs are the corpus's document ids, fixed so the sweep varies bodies only.
// They are already in ascending order, which is the tiebreaker the compiler
// appends, so a failure reads against a familiar order.
//
// Three is the default because it was measured, not assumed. The count is the
// sweep's one dial and it is steep — the corpus space is the alphabet raised to
// it, and the query space grows with it too, so each extra document costs about
// an order of magnitude:
//
//	3 documents:    216 corpora x 396 queries =  85,536 checks,  1.1s
//	4 documents:  1,296 corpora x 660 queries = 855,360 checks, 13.5s
//
// Receipt (2026-08-04): every mutation this harness was falsified against —
// including the two the example suite misses behaviourally, both of which live
// in the filter-and-cursor combination — is caught at three documents as well as
// at four. Four costs twelve times as much for no measured gain, and in CI it
// would put the Go check level with the frontend one that currently paces the
// build. It stays one environment variable away for a soak run, which is where
// the combinations three cannot reach are worth paying for.
var modelIDs = sweepIDs()

func sweepIDs() []string {
	size := 3
	if raw := os.Getenv("ATTN_DOCSTORE_SWEEP"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > 6 {
			panic(fmt.Sprintf("ATTN_DOCSTORE_SWEEP=%q: want a corpus size between 2 and 6 (the default is 3; 5 and up are a soak, not a suite run)", raw))
		}
		size = n
	}
	ids := make([]string, size)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	return ids
}

// modelWorld holds the store, the declaration and the shadow table for one run.
// It is built once and reused across every corpus: rebuilding a store per corpus
// would run all the migrations again and dominate the sweep.
type modelWorld struct {
	t      *testing.T
	s      *Store
	schema docstore.CollectionSchema
	table  string
	shadow string
	ids    []string

	// anchors caches the cursor lookup. Compiling an After cursor needs the
	// anchor document, and one corpus is asked about the same handful of anchors
	// by hundreds of queries that cannot have changed it; re-reading each one is
	// most of the statements the harness runs. Cleared whenever the corpus moves,
	// which is what refillShadow already marks — every path that writes, deletes
	// or redeclares goes through it.
	anchors map[string]*docstore.Document
}

func newModelWorld(t *testing.T) *modelWorld {
	return newModelWorldFor(t, modelDeclaration(), modelIDs)
}

func newModelWorldFor(t *testing.T, decl docstore.CollectionSchema, ids []string) *modelWorld {
	t.Helper()
	s := New()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := s.DefineDocumentCollection(decl, base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := declOf(t, s, decl.Namespace, decl.Collection)
	w := &modelWorld{t: t, s: s, schema: schema, table: schema.Table, shadow: "shadow_docs", ids: ids, anchors: map[string]*docstore.Document{}}
	w.createShadow()
	return w
}

// createShadow builds the dumb table. Same declared affinity per field — that is
// the collection's stated intent about how its values compare, and reusing it
// here is deliberate: what this harness tests is the machinery around the
// column, not the one-line type-to-affinity mapping, which has its own named
// test. No generated columns and no indexes, so the shadow reads the body the
// long way and can never be served from an index.
func (w *modelWorld) createShadow() {
	w.t.Helper()
	cols := []string{`id TEXT PRIMARY KEY`, `body TEXT`, `created_at TEXT`, `updated_at TEXT`}
	for _, f := range w.schema.Fields {
		cols = append(cols, fmt.Sprintf("%s %s", shadowColumn(f.Name), docstore.ColumnAffinity(f.Type)))
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (%s)", w.shadow, strings.Join(cols, ", "))
	if _, err := w.s.db.Exec(stmt); err != nil {
		w.t.Fatalf("create shadow: %v", err)
	}
}

// shadowColumn is where the dumb table keeps a field. The two reserved names are
// stamped columns the shadow copies verbatim, so they keep their own names;
// everything else is a declared field and gets the prefix, for the same reason
// the real table prefixes them — so a collection may declare a field called
// `id` without shadowing one.
func shadowColumn(field string) string {
	if field == docstore.FieldCreatedAt || field == docstore.FieldUpdatedAt {
		return field
	}
	return "s_" + field
}

// loadCorpus replaces the collection's documents with bodies, then refills the
// shadow from the real table.
//
// The refill is one INSERT ... SELECT, so SQLite does the extraction and the
// affinity conversion itself. Nothing about a stored value passes through Go on
// the way to the shadow, which is what keeps the two paths independent without
// making the test model anything.
func (w *modelWorld) loadCorpus(bodies []string) {
	w.t.Helper()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	for i, id := range w.ids {
		if _, err := w.s.PutDocument(w.schema, id, []byte(bodies[i]), base.Add(time.Duration(i)*time.Second), nil); err != nil {
			w.t.Fatalf("put %s: %v", id, err)
		}
	}
	w.refillShadow()
}

// refillShadow rebuilds the dumb table from the real one.
func (w *modelWorld) refillShadow() {
	w.t.Helper()
	w.anchors = map[string]*docstore.Document{}
	if _, err := w.s.db.Exec("DELETE FROM " + w.shadow); err != nil {
		w.t.Fatalf("clear shadow: %v", err)
	}
	names := []string{"id", "body", "created_at", "updated_at"}
	exprs := []string{"id", "body", "created_at", "updated_at"}
	for _, f := range w.schema.Fields {
		names = append(names, shadowColumn(f.Name))
		exprs = append(exprs, docstore.FieldExpression(f.Name))
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
		w.shadow, strings.Join(names, ", "), strings.Join(exprs, ", "), w.table)
	if _, err := w.s.db.Exec(stmt); err != nil {
		w.t.Fatalf("fill shadow: %v", err)
	}
}

// naiveOrder answers the query the dumb way: every matching id, in order, with
// no limit and no cursor. Paging is applied afterwards by slicing, because
// slicing an ordered list is the one description of "the next page" that cannot
// be subtly wrong — which is exactly what makes it worth comparing the
// compiler's tuple comparison against.
func (w *modelWorld) naiveOrder(q docstore.Query) []string {
	w.t.Helper()
	var where []string
	var args []any
	for _, f := range q.Filters {
		where = append(where, fmt.Sprintf("%s %s ?", shadowColumn(f.Field), naiveOp(w.t, f.Op)))
		args = append(args, naiveBind(w.t, w.schema, f))
	}
	order := "id ASC"
	if q.Sort != nil {
		dir := "ASC"
		if q.Sort.Desc {
			dir = "DESC"
		}
		order = fmt.Sprintf("%s %s, id %s", shadowColumn(q.Sort.Field), dir, dir)
	}
	stmt := "SELECT id FROM " + w.shadow
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	stmt += " ORDER BY " + order
	return w.scanIDs(stmt, args)
}

// naiveOp and naiveBind restate the two mappings the compiler also makes —
// operator to SQL, and a filter's bound to what the statement carries. They are
// written out here rather than shared so that getting either wrong in the
// compiler shows up as a disagreement instead of being cancelled out.
func naiveOp(t *testing.T, op docstore.Op) string {
	t.Helper()
	switch op {
	case docstore.OpEq:
		return "="
	case docstore.OpLt:
		return "<"
	case docstore.OpLte:
		return "<="
	case docstore.OpGt:
		return ">"
	case docstore.OpGte:
		return ">="
	}
	t.Fatalf("no naive SQL for operator %q", op)
	return ""
}

func naiveBind(t *testing.T, schema docstore.CollectionSchema, f docstore.Filter) any {
	t.Helper()
	// A reserved field is a stored timestamp column, compared as text.
	if f.Field == docstore.FieldCreatedAt || f.Field == docstore.FieldUpdatedAt {
		switch v := f.Value.(type) {
		case string:
			return v
		case time.Time:
			return v.UTC().Format(docstore.TimeFormat)
		}
		t.Fatalf("no naive binding for %v (%T) on %s", f.Value, f.Value, f.Field)
	}
	var declared docstore.FieldType
	for _, spec := range schema.Fields {
		if spec.Name == f.Field {
			declared = spec.Type
		}
	}
	switch declared {
	case docstore.FieldString:
		return f.Value.(string)
	case docstore.FieldNumber:
		switch v := f.Value.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		}
	case docstore.FieldBool:
		if f.Value.(bool) {
			return 1
		}
		return 0
	}
	t.Fatalf("no naive binding for %v (%T) on a %q field", f.Value, f.Value, declared)
	return nil
}

// naivePage applies the cursor and the limit to a full ordered list.
//
// The cursor is a POSITION in the order, not a member of the result: a query
// filtered to documents the anchor itself does not match still pages from where
// the anchor sits. So "past the anchor" is decided against the unfiltered order
// — where every document has a position — and the page is the matching ids that
// fall in that suffix.
//
// Both orders come from the same ORDER BY, so the filtered list is a subsequence
// of the unfiltered one and this stays pure slicing: no comparison of values
// happens in Go, which is the whole point of the dumb path.
func naivePage(matching, everything []string, q docstore.Query) []string {
	out := matching
	if q.After != "" {
		past := map[string]bool{}
		seen := false
		for _, id := range everything {
			if seen {
				past[id] = true
			}
			if id == q.After {
				seen = true
			}
		}
		out = nil
		for _, id := range matching {
			if past[id] {
				out = append(out, id)
			}
		}
	}
	limit := q.Limit
	if limit == 0 {
		limit = docstore.DefaultLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return append([]string{}, out...)
}

// anchorFor reads the document a cursor names — the same read the daemon makes
// before compiling a paged query — and remembers it for as long as the corpus
// holds still. A cursor naming a document that is not there stays a nil anchor,
// which is the case the compiler rejects and an example test pins; caching must
// not turn that into a miss that silently re-reads.
func (w *modelWorld) anchorFor(after string) (*docstore.Document, error) {
	if after == "" {
		return nil, nil
	}
	if doc, ok := w.anchors[after]; ok {
		return doc, nil
	}
	doc, found, err := w.s.GetDocument(w.schema, after)
	if err != nil {
		return nil, err
	}
	if !found {
		doc = nil
	}
	w.anchors[after] = doc
	return doc, nil
}

// realIDs runs the query through the compiler and the store — the path under
// test, exactly as the daemon drives it.
func (w *modelWorld) realIDs(q docstore.Query) ([]string, error) {
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		return nil, err
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		return nil, err
	}
	docs, err := w.s.queryDocuments(c)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// unindexedIDs runs the compiler's own statement with the index switched off.
//
// This is a different question from the shadow's. The shadow asks "did we
// compile the right query"; this asks "does the index agree with the column it
// indexes". Same SQL, same fragments, only the access path differs, so any
// disagreement is the index returning something the underlying values do not
// support — a class that cannot appear until there is enough data for SQLite to
// prefer the index at all.
func (w *modelWorld) unindexedIDs(q docstore.Query) ([]string, error) {
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		return nil, err
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		return nil, err
	}
	stmt := "SELECT id FROM " + c.Table + " NOT INDEXED"
	if c.Where != "" {
		stmt += " WHERE " + c.Where
	}
	stmt += " ORDER BY " + c.Order + " LIMIT ?"
	return w.scanIDs(stmt, append(append([]any{}, c.Args...), c.Limit)), nil
}

func (w *modelWorld) scanIDs(stmt string, args []any) []string {
	w.t.Helper()
	rows, err := w.s.db.Query(stmt, args...)
	if err != nil {
		w.t.Fatalf("%s: %v", stmt, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			w.t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatalf("rows: %v", err)
	}
	return out
}

// modelQueries enumerates every question the sweep asks of one corpus.
//
// The shape of the space, and why each axis is here: sort covers both directions
// plus the unsorted case whose whole order is the id tiebreaker; the filters
// cover every operator against a bound that sits inside the alphabet and one
// that sits at its edge; the limits cover every page size from "less than the
// corpus" to "the whole corpus"; the cursors cover starting after each document,
// including ones the filter removes.
func modelQueries() []docstore.Query {
	sorts := []*docstore.Sort{
		nil,
		{Field: modelField},
		{Field: modelField, Desc: true},
	}
	filterSets := [][]docstore.Filter{nil}
	for _, op := range []docstore.Op{docstore.OpEq, docstore.OpLt, docstore.OpLte, docstore.OpGt, docstore.OpGte} {
		for _, bound := range []float64{1, 2} {
			filterSets = append(filterSets, []docstore.Filter{{Field: modelField, Op: op, Value: bound}})
		}
	}
	afters := append([]string{""}, modelIDs...)

	var out []docstore.Query
	for _, s := range sorts {
		for _, filters := range filterSets {
			for limit := 1; limit <= len(modelIDs); limit++ {
				for _, after := range afters {
					out = append(out, docstore.Query{
						Namespace:  "ext/model",
						Collection: "docs",
						Filters:    filters,
						Sort:       s,
						Limit:      limit,
						After:      after,
					})
				}
			}
		}
	}
	return out
}

// describeQuery renders a query the way a failure needs to read it: everything
// needed to rebuild the case, in one line.
func describeQuery(q docstore.Query) string {
	parts := []string{}
	for _, f := range q.Filters {
		parts = append(parts, fmt.Sprintf("%s %s %v", f.Field, f.Op, f.Value))
	}
	if len(parts) == 0 {
		parts = append(parts, "no filter")
	}
	sortDesc := "no sort"
	if q.Sort != nil {
		dir := "asc"
		if q.Sort.Desc {
			dir = "desc"
		}
		sortDesc = fmt.Sprintf("sort %s %s", q.Sort.Field, dir)
	}
	after := "from the start"
	if q.After != "" {
		after = "after " + q.After
	}
	return fmt.Sprintf("%s, %s, limit %d, %s", strings.Join(parts, " and "), sortDesc, q.Limit, after)
}

func describeCorpus(bodies []string) string {
	parts := make([]string, len(bodies))
	for i, b := range bodies {
		parts[i] = modelIDs[i] + "=" + b
	}
	return strings.Join(parts, " ")
}

// TestEverySmallCorpusAgreesWithTheDumbQuery is the sweep: every arrangement of
// the alphabet across the corpus, against every query, checked for exact
// agreement on the ordered ids.
//
// It is exhaustive rather than random on purpose. There is no seed, so nothing
// passes by luck and no bug waits for someone to change a number; it cannot be
// flaky; and a failure is already down to four documents, so the case that
// broke is one you can read instead of one you have to shrink.
func TestEverySmallCorpusAgreesWithTheDumbQuery(t *testing.T) {
	w := newModelWorld(t)
	queries := modelQueries()

	corpora := 0
	checks := 0
	for _, bodies := range allCorpora() {
		w.loadCorpus(bodies)
		corpora++

		// Only the dumb comparison here, not the unindexed one: four rows never
		// tempt the planner into an index, so asking the same statement again
		// with NOT INDEXED would ask an identical question and cost the sweep
		// half its running time to learn nothing. The large leg is where that
		// comparison has something to say.
		cache := map[string][]string{}
		for _, q := range queries {
			matching, everything := w.dumbOrders(q, cache)
			want := naivePage(matching, everything, q)

			got, err := w.realIDs(q)
			if err != nil {
				t.Fatalf("corpus [%s]\nquery  %s\ncompiled or ran with an error: %v",
					describeCorpus(bodies), describeQuery(q), err)
			}
			if !sameIDs(got, want) {
				t.Fatalf("corpus [%s]\nquery   %s\nreal    %v\ndumb    %v\nmatched %v\norder   %v",
					describeCorpus(bodies), describeQuery(q), got, want, matching, everything)
			}
			checks++
		}
	}
	t.Logf("%d corpora x %d queries = %d checks", corpora, len(queries), checks)
}

// describeOrdering keys the queries that share a dumb answer: same filters, same
// sort, regardless of how they page through it.
func describeOrdering(q docstore.Query) string {
	var b strings.Builder
	for _, f := range q.Filters {
		fmt.Fprintf(&b, "%s|%s|%v;", f.Field, f.Op, f.Value)
	}
	if q.Sort != nil {
		fmt.Fprintf(&b, "sort:%s:%v", q.Sort.Field, q.Sort.Desc)
	}
	return b.String()
}

// allCorpora is every assignment of the alphabet to the corpus's documents.
func allCorpora() [][]string {
	out := [][]string{{}}
	for range modelIDs {
		next := make([][]string, 0, len(out)*len(modelBodies))
		for _, prefix := range out {
			for _, body := range modelBodies {
				grown := append(append([]string{}, prefix...), body)
				next = append(next, grown)
			}
		}
		out = next
	}
	return out
}

// answer is one query asked every way the harness knows how, so a disagreement
// says which of the three differed rather than only that something did.
type answer struct {
	real      []string
	dumb      []string
	unindexed []string
	matching  []string
	order     []string
}

// dumbOrders returns the dumb table's answer to a query and its answer to the
// same query with the filters removed, reusing anything cache already holds.
//
// Both are needed because the cursor is a position: "past the anchor" is read
// against the order every document has a place in, and the page is the matching
// ids that fall in that suffix. Neither depends on the limit or the cursor, so
// one ordering answers every page of itself.
func (w *modelWorld) dumbOrders(q docstore.Query, cache map[string][]string) (matching, everything []string) {
	w.t.Helper()
	orderOf := func(qq docstore.Query) []string {
		key := describeOrdering(qq)
		got, ok := cache[key]
		if !ok {
			got = w.naiveOrder(qq)
			cache[key] = got
		}
		return got
	}
	unfiltered := q
	unfiltered.Filters = nil
	return orderOf(q), orderOf(unfiltered)
}

// ask runs a query down the real path, the dumb path, and the compiler's own SQL
// with the index switched off, using cache for the orderings it has already
// asked the dumb table about.
func (w *modelWorld) ask(q docstore.Query, cache map[string][]string) answer {
	w.t.Helper()
	matching, everything := w.dumbOrders(q, cache)

	real, err := w.realIDs(q)
	if err != nil {
		w.t.Fatalf("query %s: %v", describeQuery(q), err)
	}
	unindexed, err := w.unindexedIDs(q)
	if err != nil {
		w.t.Fatalf("unindexed query %s: %v", describeQuery(q), err)
	}
	return answer{
		real:      real,
		dumb:      naivePage(matching, everything, q),
		unindexed: unindexed,
		matching:  matching,
		order:     everything,
	}
}

// check compares the three answers and fails with everything needed to rebuild
// the case. context names the corpus or the step, whichever the caller has.
func (w *modelWorld) check(context string, q docstore.Query, a answer) {
	w.t.Helper()
	if !sameIDs(a.real, a.dumb) {
		w.t.Fatalf("%s\nquery   %s\nreal    %v\ndumb    %v\nmatched %v\norder   %v",
			context, describeQuery(q), a.real, a.dumb, a.matching, a.order)
	}
	// Same compiled SQL, only the access path differs, so a disagreement here is
	// the index returning something the values it indexes do not support.
	if !sameIDs(a.real, a.unindexed) {
		w.t.Fatalf("%s\nquery     %s\nindexed   %v\nscanned   %v\nthe index disagrees with the column it indexes",
			context, describeQuery(q), a.real, a.unindexed)
	}
}

func sameIDs(a, b []string) bool {
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

// The large-corpus leg.
//
// The sweep above is exhaustive but tiny, and tiny is a regime: with four rows
// SQLite reads them all, so no query up there ever touches an index. Past a few
// thousand rows the planner starts preferring the index, which is a different
// code path inside SQLite over the same declared column — and if the index and
// the column ever disagree, that is the only place it can show.
//
// So this asks bigger, raggeder questions of the same two paths, and asks the
// compiler's own statement a third time with the index switched off.
const (
	largeCorpusSize  = 4000
	largeQueriesEach = 400
)

func largeDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "ext/model",
		Collection: "large",
		Fields: []docstore.FieldSpec{
			{Name: "n", Type: docstore.FieldNumber},
			{Name: "s", Type: docstore.FieldString},
			{Name: "b", Type: docstore.FieldBool},
		},
	}
}

// largeBody draws a body from a deliberately ragged distribution. Values repeat
// heavily so tie groups are long — ties are what the cursor's tuple comparison
// exists for, and a corpus of distinct values would never exercise it.
func largeBody(rng *rand.Rand) string {
	fields := []string{}
	switch rng.Intn(8) {
	case 0: // absent
	case 1:
		fields = append(fields, `"n":null`)
	case 2:
		fields = append(fields, fmt.Sprintf(`"n":"%d"`, rng.Intn(4)))
	case 3:
		fields = append(fields, fmt.Sprintf(`"n":[%d]`, rng.Intn(3)))
	case 4:
		fields = append(fields, `"n":{"deep":1}`)
	default:
		fields = append(fields, fmt.Sprintf(`"n":%d`, rng.Intn(4)))
	}
	switch rng.Intn(6) {
	case 0: // absent
	case 1:
		fields = append(fields, `"s":null`)
	case 2:
		fields = append(fields, fmt.Sprintf(`"s":%d`, rng.Intn(3)))
	default:
		fields = append(fields, fmt.Sprintf(`"s":"v%d"`, rng.Intn(4)))
	}
	switch rng.Intn(4) {
	case 0: // absent
	case 1:
		fields = append(fields, `"b":null`)
	default:
		fields = append(fields, fmt.Sprintf(`"b":%v`, rng.Intn(2) == 1))
	}
	// An undeclared key rides along: the store must carry it untouched and no
	// query may see it.
	fields = append(fields, fmt.Sprintf(`"note":"n%d"`, rng.Intn(3)))
	return "{" + strings.Join(fields, ",") + "}"
}

// largeQuery draws a query from the whole surface, reserved sort fields
// included. Cursors are drawn from ids that exist, since a cursor naming a
// document that is gone is an error the compiler owns and an example test pins.
//
// Fields and their bounds come from the schema passed in rather than from a
// fixed list: a collection can be redeclared mid-run, and a query naming a field
// the collection no longer declares is a rejection the compiler owns, not a
// disagreement worth generating.
func largeQuery(rng *rand.Rand, ids []string, schema docstore.CollectionSchema) docstore.Query {
	q := docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection}

	sortFields := []string{docstore.FieldCreatedAt, docstore.FieldUpdatedAt}
	for _, f := range schema.Fields {
		sortFields = append(sortFields, f.Name)
	}
	if rng.Intn(6) > 0 {
		q.Sort = &docstore.Sort{Field: sortFields[rng.Intn(len(sortFields))], Desc: rng.Intn(2) == 1}
	}

	ops := []docstore.Op{docstore.OpEq, docstore.OpLt, docstore.OpLte, docstore.OpGt, docstore.OpGte}
	for n := rng.Intn(3); n > 0 && len(schema.Fields) > 0; n-- {
		spec := schema.Fields[rng.Intn(len(schema.Fields))]
		f := docstore.Filter{Field: spec.Name, Op: ops[rng.Intn(len(ops))]}
		switch spec.Type {
		case docstore.FieldNumber:
			f.Value = float64(rng.Intn(5))
		case docstore.FieldString:
			f.Value = fmt.Sprintf("v%d", rng.Intn(5))
		case docstore.FieldBool:
			f.Value = rng.Intn(2) == 1
		}
		q.Filters = append(q.Filters, f)
	}

	q.Limit = 1 + rng.Intn(50)
	if len(ids) > 0 && rng.Intn(3) == 0 {
		q.After = ids[rng.Intn(len(ids))]
	}
	return q
}

// TestALargeRandomCorpusAgreesWithTheDumbQuery is the breadth leg: a few fixed
// seeds, each building a corpus big enough for the planner to reach for an
// index, then asking the same questions three ways.
//
// The seeds are fixed rather than drawn from the clock. A test that picks a new
// corpus every run is a test that fails for somebody else, on a case nobody can
// reproduce; these fail for everybody or for nobody.
func TestALargeRandomCorpusAgreesWithTheDumbQuery(t *testing.T) {
	for _, seed := range []int64{20260804, 7, 991, 40409, 1234567} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))

			ids := make([]string, largeCorpusSize)
			bodies := make([]string, largeCorpusSize)
			for i := range ids {
				// Ids are zero-padded so their lexicographic order — which is the
				// tiebreaker the compiler appends — matches the order they were
				// written in, keeping a failure readable.
				ids[i] = fmt.Sprintf("doc-%05d", i)
				bodies[i] = largeBody(rng)
			}

			w := newModelWorldFor(t, largeDeclaration(), ids)
			w.loadCorpus(bodies)

			cache := map[string][]string{}
			indexed := 0
			for i := 0; i < largeQueriesEach; i++ {
				q := largeQuery(rng, ids, w.schema)
				a := w.ask(q, cache)
				w.check(fmt.Sprintf("seed %d, query %d, %d documents", seed, i, largeCorpusSize), q, a)
				if w.usesAnIndex(q) {
					indexed++
				}
			}

			// Without this the third comparison is vacuous: if the planner never
			// chose an index, running the same statement with NOT INDEXED asked
			// the identical question twice and proved nothing.
			if indexed == 0 {
				t.Fatalf("not one of %d queries reached an index, so the unindexed comparison checked nothing; the corpus is too small or the declaration lost its indexes", largeQueriesEach)
			}
			t.Logf("%d of %d queries used an index over %d documents", indexed, largeQueriesEach, largeCorpusSize)
		})
	}
}

// usesAnIndex reports whether SQLite chose an index for the query, read out of
// its own plan rather than guessed from the shape of the SQL.
func (w *modelWorld) usesAnIndex(q docstore.Query) bool {
	w.t.Helper()
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		w.t.Fatalf("anchor: %v", err)
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		w.t.Fatalf("compile: %v", err)
	}
	plan, err := w.s.QueryPlan(c)
	if err != nil {
		w.t.Fatalf("plan: %v", err)
	}
	for _, step := range plan {
		if strings.Contains(step, "USING INDEX") || strings.Contains(step, "USING COVERING INDEX") {
			return true
		}
	}
	return false
}

// The moving-corpus leg.
//
// Both legs above ask questions of a frozen pile. Real use is not frozen:
// documents are written and deleted while queries run, and a field can be
// redeclared as a different type — or dropped and brought back — with documents
// already sitting there. Those are the operations where the physical schema does
// DDL under live data, and none of them are visible to a test that loads a
// corpus once.
//
// This mutates the collection and keeps comparing. The model it carries is
// deliberately thin: which ids are currently stored, and nothing about ordering
// or comparison, which stay the dumb query's job. That is the same discipline
// internal/ticketnotify's routing model uses — restate the rule under test and
// nothing else, so the test cannot drift into being a second copy of the code.
const movingSteps = 300

func TestAMovingCorpusAgreesWithTheDumbQuery(t *testing.T) {
	for _, seed := range []int64{20260804, 31337} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w := newModelWorldFor(t, largeDeclaration(), nil)
			base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

			live := map[string]bool{}
			liveIDs := func() []string {
				out := make([]string, 0, len(live))
				for id := range live {
					out = append(out, id)
				}
				sort.Strings(out)
				return out
			}

			// Seed enough documents that the first steps have something to page.
			for i := 0; i < 40; i++ {
				id := fmt.Sprintf("doc-%05d", i)
				if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), base.Add(time.Duration(i)*time.Second), nil); err != nil {
					t.Fatalf("seed put %s: %v", id, err)
				}
				live[id] = true
			}

			redeclarations := 0
			for step := 0; step < movingSteps; step++ {
				context := fmt.Sprintf("seed %d, step %d, %d documents", seed, step, len(live))
				now := base.Add(time.Duration(1000+step) * time.Second)

				switch n := rng.Intn(12); {
				case n < 5:
					id := fmt.Sprintf("doc-%05d", 40+step)
					if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), now, nil); err != nil {
						t.Fatalf("%s: put %s: %v", context, id, err)
					}
					live[id] = true
				case n < 7:
					// Rewrite one that is already there, which moves updated_at
					// and leaves created_at where it was.
					ids := liveIDs()
					if len(ids) == 0 {
						continue
					}
					id := ids[rng.Intn(len(ids))]
					if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), now, nil); err != nil {
						t.Fatalf("%s: rewrite %s: %v", context, id, err)
					}
				case n < 10:
					ids := liveIDs()
					if len(ids) == 0 {
						continue
					}
					id := ids[rng.Intn(len(ids))]
					existed, err := w.s.DeleteDocument(w.schema, id, nil)
					if err != nil {
						t.Fatalf("%s: delete %s: %v", context, id, err)
					}
					if !existed {
						t.Fatalf("%s: delete %s reported nothing was there, but it was written and never removed", context, id)
					}
					delete(live, id)
				default:
					// Redeclare, which is DDL over live documents: a field's type
					// changes how its stored values compare, and a field can leave
					// the declaration and come back. Both rebuild the collection's
					// columns without rewriting a single body.
					w.redeclare(randomDeclaration(rng), now)
					redeclarations++
				}

				w.refillShadow()

				ids := liveIDs()
				if got := w.storedIDs(); !sameIDs(got, ids) {
					t.Fatalf("%s: the collection holds %v, the model says %v", context, got, ids)
				}
				if len(ids) == 0 {
					continue
				}

				cache := map[string][]string{}
				for i := 0; i < 4; i++ {
					q := largeQuery(rng, ids, w.schema)
					w.check(context, q, w.ask(q, cache))
				}

				// Page all the way through one query. A cursor is only meaningful
				// against an order, and the order here has just moved underneath
				// it — so this is the leg that asks whether paging still lands on
				// every document exactly once when the corpus is not holding still.
				w.checkFullPagination(context, rng, ids)
			}
			if redeclarations == 0 {
				t.Fatalf("no step redeclared the collection, so this run never exercised DDL over live documents")
			}
			t.Logf("%d steps, %d redeclarations, %d documents left", movingSteps, redeclarations, len(live))
		})
	}
}

// checkFullPagination walks one query page by page and checks the walk against
// the dumb query's whole ordered answer. Every document exactly once, in order,
// no matter where the page boundaries fall.
func (w *modelWorld) checkFullPagination(context string, rng *rand.Rand, ids []string) {
	w.t.Helper()
	q := largeQuery(rng, ids, w.schema)
	q.After = ""
	q.Limit = 1 + rng.Intn(4)

	full := w.naiveOrder(q)
	var walked []string
	after := ""
	for page := 0; ; page++ {
		if page > len(ids)+2 {
			w.t.Fatalf("%s: paging %s never ran out after %d pages; it is repeating documents",
				context, describeQuery(q), page)
		}
		step := q
		step.After = after
		got, err := w.realIDs(step)
		if err != nil {
			w.t.Fatalf("%s: paging %s: %v", context, describeQuery(step), err)
		}
		if len(got) == 0 {
			break
		}
		walked = append(walked, got...)
		after = got[len(got)-1]
	}
	if !sameIDs(walked, full) {
		w.t.Fatalf("%s\nquery  %s\npaging through in pages of %d walked\n  %v\nbut the whole answer is\n  %v",
			context, describeQuery(q), q.Limit, walked, full)
	}
}

// redeclare changes the collection's declaration and rebuilds the shadow to
// match, since the dumb table's columns mirror the declared fields.
func (w *modelWorld) redeclare(decl docstore.CollectionSchema, now time.Time) {
	w.t.Helper()
	if err := w.s.DefineDocumentCollection(decl, now); err != nil {
		w.t.Fatalf("redeclare: %v", err)
	}
	w.schema = declOf(w.t, w.s, decl.Namespace, decl.Collection)
	if _, err := w.s.db.Exec("DROP TABLE " + w.shadow); err != nil {
		w.t.Fatalf("drop shadow: %v", err)
	}
	w.createShadow()
}

// randomDeclaration draws a declaration over the same three field names, so a
// field can change type, leave, and come back across a run.
func randomDeclaration(rng *rand.Rand) docstore.CollectionSchema {
	types := []docstore.FieldType{docstore.FieldString, docstore.FieldNumber, docstore.FieldBool}
	decl := largeDeclaration()
	decl.Fields = nil
	for _, name := range []string{"n", "s", "b"} {
		if rng.Intn(4) == 0 {
			continue
		}
		decl.Fields = append(decl.Fields, docstore.FieldSpec{Name: name, Type: types[rng.Intn(len(types))]})
	}
	if len(decl.Fields) == 0 {
		decl.Fields = []docstore.FieldSpec{{Name: "n", Type: docstore.FieldNumber}}
	}
	return decl
}

// storedIDs is every id the collection holds, read straight from its table.
func (w *modelWorld) storedIDs() []string {
	return w.scanIDs("SELECT id FROM "+w.table+" ORDER BY id ASC", nil)
}
