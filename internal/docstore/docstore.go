// Package docstore is attn's document primitive: JSON documents addressed by
// (namespace, collection, id), read through one JSON-serializable query object,
// with a collection's declared fields as the contract for what may be filtered
// and sorted.
//
// This package owns what a query MEANS — the vocabulary, the validation, and
// the SQL a validated query compiles to. It owns no database handle:
// internal/store executes what is compiled here and scans the rows. That is the
// same split internal/bus and internal/store/bus.go already use, semantics here
// and persistence there, and it is what lets the query rules be tested without
// a database.
//
// The query object is deliberately serializable end to end. Three surfaces will
// eventually carry it — the Bun sidecar's SDK, extension UI, and `attn doc` —
// and all three must carry the same thing, so a later transport adds transport
// and not semantics.
//
// The store knows nothing about extensions. A namespace is an opaque
// `owner/name` string; who may write under which owner is enforced where the
// namespace is granted, not here.
//
// A collection is physically its own table, and a declared field an indexed
// generated column in it, so this package also owns the naming those identifiers
// use — see TableName and FieldColumn, and
// docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md. The store builds
// its DDL from those names; it does not invent any.
//
// See docs/plans/2026-08-03-ext-a3-doc-store.md.
package docstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Limits on a result set. A live query's results are pushed as one message and
// rendered as one list, so the bound that matters is "how big is a list a person
// looks at".
//
// Receipt (2026-08-03, production ~/.attn): the lists attn already pushes whole
// are tickets 7, sessions 11, notifications 8, workspaces 8. DefaultLimit is an
// order of magnitude past that working set, MaxLimit two orders — a tripwire, so
// only something broken or something that means to paginate ever feels it.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)

// Reserved field names. Both are real columns the store stamps, so they are
// always available for filtering and sorting without being declared — "newest
// first" and "changed since" need no schema. A collection may not declare a body
// field under either name; the column would win and the declaration would be a
// lie.
const (
	FieldCreatedAt = "created_at"
	FieldUpdatedAt = "updated_at"
)

// FieldType is what a declared field holds. The type is not decoration: it
// decides how a filter's bound is bound to the statement — comparing a number
// field against a string bound would otherwise match nothing at all rather than
// failing — and it is the affinity of the column the field is stored through,
// so it also decides how two stored values compare with each other.
type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
)

// Op is a filter comparison. Equality plus the four range operators is the whole
// surface: it covers a pending-requests panel and a changed-since sweep.
// Pagination is deliberately not built out of these — see Query.After.
type Op string

const (
	OpEq  Op = "eq"
	OpLt  Op = "lt"
	OpLte Op = "lte"
	OpGt  Op = "gt"
	OpGte Op = "gte"
)

var opSQL = map[Op]string{OpEq: "=", OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">="}

// FieldSpec declares one queryable field of a collection.
type FieldSpec struct {
	Name string    `json:"name"`
	Type FieldType `json:"type"`
}

// CollectionSchema is a collection's declaration: which fields may be filtered
// and sorted on. It is the API contract for queries, and it is deliberately not
// a schema the body must satisfy — a document's body is arbitrary JSON, an
// undeclared key is stored and read back untouched, and a declared field the
// body omits is simply absent. Declaring a field says "you may query this", not
// "this must exist".
//
// Physically a declared field is an indexed generated column over the body, so
// the declaration is also what the store builds. Adding one is DDL and rewrites
// no document; see docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.
//
// Table is the collection's own table, minted by the store from the
// declaration's row id and filled in on read. It is not part of the declaration
// a caller writes, and Compile refuses a schema whose Table is not a minted
// name — that check is the whole defence for the one identifier in the compiled
// SQL that does not come from a validated field name.
type CollectionSchema struct {
	Namespace  string      `json:"namespace"`
	Collection string      `json:"collection"`
	Fields     []FieldSpec `json:"fields"`
	Table      string      `json:"-"`
}

// Filter is one comparison against a declared or reserved field.
type Filter struct {
	Field string `json:"field"`
	Op    Op     `json:"op"`
	Value any    `json:"value"`
}

// Sort names the ordering field. Results always carry a stable total order —
// Compile appends the document id as a tiebreaker, in the same direction as the
// sort — so two documents sharing a sort value have a defined relative position.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Query is the one representation every surface carries.
//
// A zero Limit means DefaultLimit rather than "unbounded": a query with no
// ceiling is a wire message with no ceiling.
//
// After is the id of the last document of the previous page, and is how a
// caller gets the next one. It has to be part of the query rather than a filter
// a caller writes by hand: the visible order is (sort field, id), and a filter
// can only constrain one of those, so `sort > value` skips every document tied
// with the anchor and `sort >= value` returns the anchor again. Compile turns
// After into a comparison against the whole ordering tuple, which is the only
// form that neither skips nor repeats.
type Query struct {
	Namespace  string   `json:"namespace"`
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
	Sort       *Sort    `json:"sort,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	After      string   `json:"after,omitempty"`
}

// Document is one stored record. Body is the document as written, byte for byte
// — record-shape evolution is handled by the reader (parse tolerantly, render
// tolerantly), never by migrating stored documents.
//
// Rev is what makes a read-modify-write safe. It comes back on every read, so a
// caller that read a document already holds the token that names the version it
// read; handing that token back to a write is how the store can refuse an edit
// built on a version somebody else has since replaced.
type Document struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int64           `json:"rev"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Revisions.
//
// A document's revision counts writes to that document, starting at FirstRev.
// It is per document rather than per collection or per store: what a caller
// needs to know is whether the record they read is the record they are about to
// overwrite, and a global counter would make every unrelated write look like a
// conflict.
//
// ExpectAbsent is the one magic value, and it is unambiguous because revisions
// start at 1: a caller that expects revision zero is a caller that expects no
// document at all. That makes create-only fall out of the same field rather
// than needing a second one.
//
// Width: the wire carries a revision as safeint (2^53-1) and the store as
// SQLite INTEGER, which is 8 bytes whatever it is declared as. int32 would have
// been 2.1 billion writes to ONE document — roughly seven years at ten writes a
// second, which attn's uptime can reach — and overflowing it would silently make
// a stale check pass, which is the exact failure this exists to prevent.
const (
	FirstRev     int64 = 1
	ExpectAbsent int64 = 0
)

// ConflictError is a write refused because the document was not at the revision
// the caller expected. It is a distinct type, not a string, because every
// surface above has to be able to tell it apart from a failure: a conflict means
// "read it again and retry", and everything else means "something is broken".
//
// Found and Actual describe what was there when the store looked, which is after
// the write was refused — so they name the version that won rather than the
// version that will still be current by the time the caller reads this. That is
// what the caller wants: it says which write it lost to.
type ConflictError struct {
	Namespace  string
	Collection string
	ID         string
	// Expected is the revision the caller asserted, or ExpectAbsent when the
	// caller asserted the document did not exist.
	Expected int64
	// Found reports whether a document was there at all; Actual is its revision
	// and is meaningless when Found is false.
	Found  bool
	Actual int64
}

func (e *ConflictError) Error() string {
	addr := Address(e.Namespace, e.Collection, e.ID)
	switch {
	case e.Expected == ExpectAbsent:
		return fmt.Sprintf("docstore: %s already exists at rev %d, and this write expected it not to exist yet", addr, e.Actual)
	case !e.Found:
		return fmt.Sprintf("docstore: %s expected rev %d but no document is there; it was removed since you read it", addr, e.Expected)
	default:
		return fmt.Sprintf("docstore: %s expected rev %d but is at rev %d; re-read it and apply your change to that version", addr, e.Expected, e.Actual)
	}
}

// IsConflict reports whether an error is a lost-update refusal. It is the check
// a retry loop makes.
func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

// Compiled is a validated query as SQL. Table is the collection's table, Where
// and Order are fragments the store splices into its own SELECT, and Args binds
// Where's placeholders in order. Where is empty when nothing constrains the
// query: the table already holds exactly one collection, so "everything" needs
// no predicate at all.
type Compiled struct {
	Table string
	Where string
	Args  []any
	Order string
	Limit int
}

var (
	// A namespace is `owner/name`: the owner segment is the isolation class a
	// grant hands out (`ext`, `core`), the name segment identifies the holder.
	namePart      = `[a-z0-9][a-z0-9_-]*`
	namespaceRe   = regexp.MustCompile(`^` + namePart + `/` + namePart + `$`)
	collectionRe  = regexp.MustCompile(`^` + namePart + `$`)
	documentIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	fieldNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableNameRe   = regexp.MustCompile(`^doc_[1-9][0-9]*$`)
	reservedField = map[string]bool{FieldCreatedAt: true, FieldUpdatedAt: true}
)

// Physical naming. A collection is one table and a declared field is one
// generated column in it, so these names end up spliced into SQL as
// identifiers. Every one of them is derived here from something already
// checked — an integer row id, or a field name matching fieldNameRe — so there
// is no caller-supplied text in any identifier the store executes.
const (
	// tablePrefix keeps minted tables recognisable in a schema dump and out of
	// the way of attn's own tables.
	tablePrefix = "doc_"
	// fieldColumnPrefix keeps a declared field from colliding with the columns
	// the store owns: a collection may declare a field called `id` or `body`
	// without shadowing either.
	fieldColumnPrefix = "f_"
)

// TableName is the table holding a collection's documents, derived from its
// declaration's row id. Derived rather than built from the address because a
// namespace contains a slash and a collection name is caller-chosen: minting
// from an integer means no identifier is ever a function of caller text.
func TableName(id int64) string {
	return fmt.Sprintf("%s%d", tablePrefix, id)
}

// ValidateTableName accepts a minted table name. Compile calls it before
// splicing, so a schema that reached the compiler from anywhere but the store's
// own read path fails loudly instead of composing SQL.
func ValidateTableName(name string) error {
	if name == "" {
		return fmt.Errorf("docstore: collection has no table; a declaration must be read from the store before it can be queried")
	}
	if !tableNameRe.MatchString(name) {
		return fmt.Errorf("docstore: %q is not a minted table name", name)
	}
	return nil
}

// FieldColumn is the generated column a declared field is queried through.
func FieldColumn(field string) string {
	return fieldColumnPrefix + field
}

// FieldExpression is what that column computes: the field read out of the body.
// The store builds its DDL from this, and it is the reason a declaration
// rewrites no document — the column is VIRTUAL, so it exists for every document
// already stored the moment it is added.
func FieldExpression(field string) string {
	return "json_extract(body, '$." + field + "')"
}

// ColumnAffinity is the SQLite affinity a declared type maps to. This is where
// the declared type stops being decoration: a body storing "5" in a number
// field reads, compares, and orders as the number 5, because the column that
// carries it is NUMERIC. A value with no such reading — an array, an object —
// keeps its JSON text, which is what makes those still orderable rather than an
// error.
func ColumnAffinity(t FieldType) string {
	switch t {
	case FieldNumber:
		return "NUMERIC"
	case FieldBool:
		return "INTEGER"
	default:
		return "TEXT"
	}
}

// quoteIdent renders an identifier for SQL. Every identifier here already
// matches a validating pattern, so this is belt-and-braces rather than the
// defence — but it is what makes a field named like a keyword harmless.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ValidateNamespace accepts an `owner/name` namespace. The store does not care
// which owners exist; it cares that the string is a well-formed two-part name it
// can index and a person can read.
func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("docstore: namespace is required, as owner/name (for example ext/approval-gate)")
	}
	if !namespaceRe.MatchString(ns) {
		return fmt.Errorf("docstore: namespace %q is not owner/name, where each part is lowercase letters, digits, - or _ (for example ext/approval-gate)", ns)
	}
	return nil
}

// ValidateCollection accepts a collection name.
func ValidateCollection(name string) error {
	if name == "" {
		return fmt.Errorf("docstore: collection is required")
	}
	if !collectionRe.MatchString(name) {
		return fmt.Errorf("docstore: collection %q must be lowercase letters, digits, - or _", name)
	}
	return nil
}

// ValidateDocumentID accepts a document id. Ids are caller-chosen so a workflow
// can address the record it owns by a name it already has, rather than storing a
// generated id somewhere to find it again.
func ValidateDocumentID(id string) error {
	if id == "" {
		return fmt.Errorf("docstore: document id is required")
	}
	if !documentIDRe.MatchString(id) {
		return fmt.Errorf("docstore: document id %q must start alphanumeric and contain only letters, digits, ., - or _", id)
	}
	return nil
}

// Validate checks a collection declaration. Field names are restricted to plain
// identifiers because a declared field becomes both a JSON path and a column
// name in the collection's table: this is the check that keeps every identifier
// the store goes on to execute free of caller-shaped text.
func (s CollectionSchema) Validate() error {
	if err := ValidateNamespace(s.Namespace); err != nil {
		return err
	}
	if err := ValidateCollection(s.Collection); err != nil {
		return err
	}
	seen := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		switch {
		case f.Name == "":
			return fmt.Errorf("docstore: %s/%s declares a field with no name", s.Namespace, s.Collection)
		case reservedField[f.Name]:
			return fmt.Errorf("docstore: %s/%s declares %q, which is reserved — %s and %s are always queryable and must not be declared",
				s.Namespace, s.Collection, f.Name, FieldCreatedAt, FieldUpdatedAt)
		case !fieldNameRe.MatchString(f.Name):
			return fmt.Errorf("docstore: %s/%s declares field %q, which must start with a letter or _ and contain only letters, digits or _",
				s.Namespace, s.Collection, f.Name)
		case seen[f.Name]:
			return fmt.Errorf("docstore: %s/%s declares field %q twice", s.Namespace, s.Collection, f.Name)
		}
		switch f.Type {
		case FieldString, FieldNumber, FieldBool:
		default:
			return fmt.Errorf("docstore: %s/%s declares field %q with type %q; use %s, %s or %s",
				s.Namespace, s.Collection, f.Name, f.Type, FieldString, FieldNumber, FieldBool)
		}
		seen[f.Name] = true
	}
	return nil
}

// field returns the declared spec for name, or false when the collection does
// not declare it.
func (s CollectionSchema) field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// declaredNames lists the collection's queryable fields for an error message,
// reserved names included, so a rejected query says what it could have asked
// for instead of only what it could not.
func (s CollectionSchema) declaredNames() string {
	names := make([]string, 0, len(s.Fields)+2)
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	names = append(names, FieldCreatedAt, FieldUpdatedAt)
	return strings.Join(names, ", ")
}

// Compile validates q against the collection's declaration and returns it as
// SQL. Every rejection names the collection, the offending part, and what is
// available — the reader is an agent that must fix the query from the error
// alone.
//
// anchor is the document q.After names, which the caller reads before compiling
// because this package holds no database handle. It is nil when q.After is
// empty, and its absence when q.After is set is an error rather than an empty
// page: a cursor pointing at a document that no longer exists is a caller
// mistake worth hearing about, not a silent end of results.
func (q Query) Compile(schema CollectionSchema, anchor *Document) (Compiled, error) {
	if err := ValidateNamespace(q.Namespace); err != nil {
		return Compiled{}, err
	}
	if err := ValidateCollection(q.Collection); err != nil {
		return Compiled{}, err
	}
	if schema.Namespace != q.Namespace || schema.Collection != q.Collection {
		return Compiled{}, fmt.Errorf("docstore: query targets %s/%s but was compiled against the declaration for %s/%s",
			q.Namespace, q.Collection, schema.Namespace, schema.Collection)
	}
	if err := ValidateTableName(schema.Table); err != nil {
		return Compiled{}, err
	}

	// No namespace or collection predicate: the table is the collection. That
	// isolation is structural — there is no statement here that could reach
	// another namespace's documents even if it were built wrong.
	var where []string
	var args []any

	for _, f := range q.Filters {
		op, ok := opSQL[f.Op]
		if !ok {
			return Compiled{}, fmt.Errorf("docstore: %s/%s filter on %q uses operator %q; use %s, %s, %s, %s or %s",
				q.Namespace, q.Collection, f.Field, f.Op, OpEq, OpLt, OpLte, OpGt, OpGte)
		}
		expr, spec, err := q.fieldExpr(schema, f.Field, "filter")
		if err != nil {
			return Compiled{}, err
		}
		val, err := q.bindValue(f, spec)
		if err != nil {
			return Compiled{}, err
		}
		where = append(where, expr+" "+op+" ?")
		args = append(args, val)
	}

	sortExpr := ""
	desc := false
	order := "id ASC"
	if q.Sort != nil {
		expr, _, err := q.fieldExpr(schema, q.Sort.Field, "sort")
		if err != nil {
			return Compiled{}, err
		}
		sortExpr, desc = expr, q.Sort.Desc
		dir := "ASC"
		if desc {
			dir = "DESC"
		}
		// The id tiebreaker is what makes the order total. Without it two
		// documents sharing a sort value have no defined relative position, and
		// a paginating reader can see one twice and the other never. It runs in
		// the sort's own direction so the visible order is one uniformly
		// directed tuple, which is what the After cursor compares against.
		order = expr + " " + dir + ", id " + dir
	}

	if q.After != "" || anchor != nil {
		clause, cursorArgs, err := q.afterTuple(schema.Table, sortExpr, desc, anchor)
		if err != nil {
			return Compiled{}, err
		}
		where = append(where, clause)
		args = append(args, cursorArgs...)
	}

	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d; a limit must be positive", q.Namespace, q.Collection, limit)
	case limit > MaxLimit:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d, above the maximum of %d; page instead, passing the last document's id as the query's after cursor",
			q.Namespace, q.Collection, limit, MaxLimit)
	}

	return Compiled{
		Table: schema.Table,
		Where: strings.Join(where, " AND "),
		Args:  args,
		Order: order,
		Limit: limit,
	}, nil
}

// afterTuple compiles the After cursor: "strictly past the anchor in the
// visible order". The visible order is (sort field, id) both running the same
// direction, so past-the-anchor is the tuple comparison
//
//	sort <cmp> value OR (sort = value AND id <cmp> anchorID)
//
// A range filter cannot express that second branch, which is why After exists:
// with ties on the sort field, `sort > value` loses every document that shares
// the anchor's value and `sort >= value` hands the anchor back.
//
// A missing or JSON-null sort value is a real case — a declared field says what
// may be queried, not what a document must contain — and NULL compares as
// nothing at all, so it is branched on rather than bound. SQLite sorts NULL
// first, so in ASC every non-NULL row is past a NULL anchor and in DESC none is.
func (q Query) afterTuple(table, sortExpr string, desc bool, anchor *Document) (string, []any, error) {
	if q.After == "" {
		return "", nil, fmt.Errorf("docstore: %s/%s was compiled with a cursor document but no after id", q.Namespace, q.Collection)
	}
	if err := ValidateDocumentID(q.After); err != nil {
		return "", nil, err
	}
	if anchor == nil {
		return "", nil, fmt.Errorf("docstore: %s/%s cannot page after %q, which no longer exists; page again from the start, or use the id of a document that is still stored",
			q.Namespace, q.Collection, q.After)
	}
	if anchor.ID != q.After {
		return "", nil, fmt.Errorf("docstore: %s/%s was compiled to page after %q but given document %q", q.Namespace, q.Collection, q.After, anchor.ID)
	}

	cmp := ">"
	if desc {
		cmp = "<"
	}
	if sortExpr == "" {
		// No sort: the whole order is id ASC, so the cursor is one comparison.
		return "id " + cmp + " ?", []any{q.After}, nil
	}

	isNull, err := q.anchorSortIsNull(anchor)
	if err != nil {
		return "", nil, err
	}
	if isNull {
		if desc {
			return "(" + sortExpr + " IS NULL AND id < ?)", []any{q.After}, nil
		}
		return "(" + sortExpr + " IS NOT NULL OR id > ?)", []any{q.After}, nil
	}

	// The anchor's sort value is read back out of the table rather than bound
	// from Go. A declared field says what may be queried, not what a document
	// must hold, so a "number" field may legitimately contain an array or an
	// object — values that have no bindable Go equivalent, and that the column
	// carries as JSON text. Reading the value through the same column the ORDER
	// BY uses makes the cursor compare exactly what the ordering compares, for
	// every JSON shape and under the column's own affinity, with no Go-side
	// reconstruction to disagree with either. The subquery is uncorrelated, so
	// it is evaluated once per statement.
	value := "(SELECT " + sortExpr + " FROM " + table + " WHERE id = ?)"
	valueArgs := []any{q.After}

	clause := "(" + sortExpr + " > " + value + " OR (" + sortExpr + " = " + value + " AND id > ?))"
	if desc {
		// Descending puts NULLs last, so they are past any non-NULL anchor.
		clause = "(" + sortExpr + " IS NULL OR " + sortExpr + " < " + value + " OR (" + sortExpr + " = " + value + " AND id < ?))"
	}
	args := make([]any, 0, len(valueArgs)*2+1)
	args = append(args, valueArgs...)
	args = append(args, valueArgs...)
	args = append(args, q.After)
	return clause, args, nil
}

// anchorSortIsNull reports whether the cursor document has no value for the sort
// field — the field is absent, or is JSON null — which is what json_extract
// yields as SQL NULL and what the branches above handle separately. It is a
// structural question about the body, so it needs no type mapping.
func (q Query) anchorSortIsNull(anchor *Document) (bool, error) {
	if reservedField[q.Sort.Field] {
		// Both are stamped columns and are never NULL.
		return false, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(anchor.Body, &body); err != nil {
		return false, fmt.Errorf("docstore: %s/%s cannot page after %q: its body is not a JSON object (%w)", q.Namespace, q.Collection, anchor.ID, err)
	}
	raw, ok := body[q.Sort.Field]
	if !ok {
		return true, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("docstore: %s/%s cannot page after %q: its %q is not valid JSON (%w)", q.Namespace, q.Collection, anchor.ID, q.Sort.Field, err)
	}
	return value == nil, nil
}

// fieldExpr resolves a field reference to SQL. A reserved name is one of the
// store's own stamped columns and is written literally; anything else must be
// declared, and resolves to that field's generated column, quoted because its
// name descends from caller text. use names what the reference was for, so the
// error says whether the filter or the sort is wrong.
func (q Query) fieldExpr(schema CollectionSchema, name, use string) (string, FieldSpec, error) {
	if name == "" {
		return "", FieldSpec{}, fmt.Errorf("docstore: %s/%s has a %s with no field name", q.Namespace, q.Collection, use)
	}
	if reservedField[name] {
		return name, FieldSpec{Name: name, Type: FieldString}, nil
	}
	spec, ok := schema.field(name)
	if !ok {
		return "", FieldSpec{}, fmt.Errorf("docstore: %s/%s cannot %s on %q, which the collection does not declare (queryable: %s)",
			q.Namespace, q.Collection, use, name, schema.declaredNames())
	}
	return quoteIdent(FieldColumn(name)), spec, nil
}

// bindValue converts a filter bound to what the statement should carry, and
// refuses a bound whose type cannot compare with the field's. A number field
// compared against "5" would silently match nothing — JSON numbers and text
// never compare equal in SQLite — which is the worst outcome of the three.
func (q Query) bindValue(f Filter, spec FieldSpec) (any, error) {
	mismatch := func(want string) error {
		return fmt.Errorf("docstore: %s/%s filter on %q needs a %s value, got %T (%v)",
			q.Namespace, q.Collection, f.Field, want, f.Value, f.Value)
	}
	if f.Value == nil {
		return nil, fmt.Errorf("docstore: %s/%s filter on %q has no value", q.Namespace, q.Collection, f.Field)
	}
	// A reserved field is a stored timestamp column, compared as text. The bound
	// is re-encoded rather than passed through, because text comparison only
	// means what the caller intends when both sides carry the same encoding: a
	// bound of "…T10:00:00Z" compared raw against stored stamps sorts above every
	// stamp in that second. A caller may write any RFC3339 form — including the
	// one an older store handed them — and get the comparison they asked for.
	if reservedField[f.Field] {
		switch v := f.Value.(type) {
		case string:
			t, err := ParseTime(v)
			if err != nil {
				return nil, fmt.Errorf("docstore: %s/%s filter on %q needs an RFC3339 timestamp, got %q",
					q.Namespace, q.Collection, f.Field, v)
			}
			return t.Format(TimeFormat), nil
		case time.Time:
			return v.UTC().Format(TimeFormat), nil
		default:
			return nil, mismatch("timestamp or string")
		}
	}
	switch spec.Type {
	case FieldString:
		s, ok := f.Value.(string)
		if !ok {
			return nil, mismatch("string")
		}
		return s, nil
	case FieldNumber:
		// JSON decoding yields float64; Go callers may pass any numeric kind.
		switch v := f.Value.(type) {
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case json.Number:
			n, err := v.Float64()
			if err != nil {
				return nil, mismatch("number")
			}
			return n, nil
		default:
			return nil, mismatch("number")
		}
	case FieldBool:
		b, ok := f.Value.(bool)
		if !ok {
			return nil, mismatch("bool")
		}
		// json_extract yields 1/0 for JSON booleans.
		if b {
			return 1, nil
		}
		return 0, nil
	}
	return nil, fmt.Errorf("docstore: %s/%s filter on %q has undeclared type %q", q.Namespace, q.Collection, f.Field, spec.Type)
}

// TimeFormat is the stored timestamp encoding. Stamps live in TEXT columns and
// are ordered and filtered as text, so the encoding has to make text order and
// time order the same thing — which takes a fraction of a fixed width, always
// present and always nine digits.
//
// time.RFC3339Nano, which this used to be, does not: it strips trailing zeros,
// so widths vary and "…:00.5Z" sorts below "…:00.1234Z" while "…:00Z" sorts
// above both ('Z' is 0x5A, above '.' and every digit). Only stamps inside the
// same second compared wrongly, which is exactly where bursty writes land, and
// it made every "changed since" filter drop rows in silence. Migration 91
// rewrote the stored stamps.
//
// Always formatted from a UTC time, so the zone is always "Z" and every stored
// stamp is the same 30 characters wide.
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// ParseTime decodes a stamp in any RFC3339 form — TimeFormat's own, the
// trailing-zero-stripped form stored before migration 91, a whole second with no
// fraction at all, or a non-UTC offset — and normalizes it to UTC. Callers hand
// timestamps in as strings from JSON, and the one they most often hand in is one
// this store gave them, so the accepted set has to be wider than the stored one.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// Target is the subject a change to this collection is published under, and the
// key a subscription is matched on. Collection-grained rather than
// document-grained: a live query's result set can change because a document it
// does not currently contain started matching, so a subscriber has to hear about
// the collection, not about the documents it happens to hold right now.
func Target(namespace, collection string) string {
	return namespace + "/" + collection
}

// Target is the subject changes to the query's collection arrive under.
func (q Query) Target() string { return Target(q.Namespace, q.Collection) }

// Address is one document's full name, and the subject a change to it is
// published under. The log then reads as a history of documents; matching a
// change to the subscriptions that care about it uses Target, carried alongside.
func Address(namespace, collection, id string) string {
	return Target(namespace, collection) + "/" + id
}

// ValidateBody accepts a document body: any JSON object. Objects only, because
// a declared field is read with a JSON path and a bare array or scalar has no
// place for one to live.
func ValidateBody(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("docstore: document body is required")
	}
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("docstore: document body is not valid JSON: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return fmt.Errorf("docstore: document body must be a JSON object, got %T", probe)
	}
	return nil
}
