// Package docstore is attn's document primitive: JSON documents addressed by
// (namespace, collection, id), queried through one serializable query object.
// It owns what a query MEANS and the SQL it compiles to, but no database
// handle — internal/store executes what is compiled here. It also owns the
// physical naming (TableName, FieldColumn); the store builds its DDL from
// those names and invents none.
//
// Query semantics and physical naming live here (TableName, FieldColumn);
// internal/store executes what this package compiles and never invents an
// identifier. Namespaces are opaque `owner/name` strings — write authority is
// enforced where a namespace is granted, not here.
//
// See docs/plans/2026-08-03-ext-a3-doc-store.md and
// docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md.
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

// Result-set limits. Measured (2026-08-03, production ~/.attn): the lists attn
// pushes whole are tickets 7, sessions 11, notifications 8, workspaces 8.
// DefaultLimit is an order of magnitude past that, MaxLimit two — a tripwire.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)

// Reserved field names: real columns the store stamps, always filterable and
// sortable without being declared. A collection may not declare a body field
// under either name.
const (
	FieldCreatedAt = "created_at"
	FieldUpdatedAt = "updated_at"
)

// FieldType decides how a filter's bound is bound (a number field against a
// string bound silently matches nothing) and the column's affinity.
type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
)

// Op is a filter comparison; pagination is deliberately not built out of these
// — see Query.After.
type Op string

const (
	OpEq  Op = "eq"
	OpLt  Op = "lt"
	OpLte Op = "lte"
	OpGt  Op = "gt"
	OpGte Op = "gte"
)

var opSQL = map[Op]string{OpEq: "=", OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">="}

// FilterOps is the operator set, in a stable order, for surfaces that have to
// render it — an error message listing what was accepted, or generated SDK
// types. Callers get it from here rather than writing the list out, so an
// operator added above cannot leave a stale copy behind.
func FilterOps() []Op { return []Op{OpEq, OpLt, OpLte, OpGt, OpGte} }

// FieldSpec declares one queryable field of a collection.
type FieldSpec struct {
	Name string    `json:"name"`
	Type FieldType `json:"type"`
}

// CollectionSchema declares which fields may be filtered and sorted on; the body
// stays arbitrary JSON. Table is minted by the store from the declaration's row
// id and filled in on read, never written by a caller — Compile refuses a
// non-minted Table, the whole defence for the one identifier not derived from a
// validated field name.
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

// Sort names the ordering field; Compile appends the document id as a tiebreaker
// in the sort's direction, so the order is always total.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Query is the one representation every surface carries; a zero Limit means
// DefaultLimit, never "unbounded". After (the previous page's last id) is part
// of the query rather than a filter: a filter can constrain only one of (sort
// field, id), so it either skips ties or repeats the anchor.
type Query struct {
	Namespace  string   `json:"namespace"`
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
	Sort       *Sort    `json:"sort,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	After      string   `json:"after,omitempty"`
}

// Document is one stored record. Body is byte-for-byte as written — shape
// evolution is handled by tolerant readers, never by migrating documents.
type Document struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int64           `json:"rev"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// A revision counts writes to one document, starting at FirstRev. ExpectAbsent
// is unambiguous because revisions start at 1: expecting rev zero is expecting
// no document, so create-only falls out of the same field. int64 because an
// int32 overflow (~7 years at 10 writes/s to one document) would silently make
// a stale check pass — the exact failure this exists to prevent.
const (
	FirstRev     int64 = 1
	ExpectAbsent int64 = 0
)

// ConflictError is a write refused because the document was not at the expected
// revision — a distinct type so surfaces can tell "read again and retry" from
// "broken".
type ConflictError struct {
	Namespace  string
	Collection string
	ID         string
	Expected   int64
	// Found reports whether a document was there at all; Actual is meaningless
	// when Found is false.
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

// IsConflict reports whether an error is a lost-update refusal.
func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

// UndeclaredCollectionError is a read or write against a collection nobody declared.
type UndeclaredCollectionError struct {
	Namespace  string
	Collection string
}

func (e *UndeclaredCollectionError) Error() string {
	return fmt.Sprintf("docstore: %s/%s is not declared; declare it with `attn doc define` before reading or writing it",
		e.Namespace, e.Collection)
}

// IsUndeclaredCollection reports whether an error is a missing declaration.
func IsUndeclaredCollection(err error) bool {
	var undeclared *UndeclaredCollectionError
	return errors.As(err, &undeclared)
}

// InvalidQueryError wraps a refusal: the message is what an agent fixes the
// query from, the type is what a program branches on.
type InvalidQueryError struct{ Err error }

func (e *InvalidQueryError) Error() string { return e.Err.Error() }
func (e *InvalidQueryError) Unwrap() error { return e.Err }

// IsInvalidQuery reports whether an error is a refused query.
func IsInvalidQuery(err error) bool {
	var invalid *InvalidQueryError
	return errors.As(err, &invalid)
}

// InvalidQuery marks an error as a refusal; exported because a query can be
// refused before it reaches Compile.
func InvalidQuery(err error) error {
	if err == nil {
		return nil
	}
	if IsInvalidQuery(err) {
		return err
	}
	return &InvalidQueryError{Err: err}
}

// Compiled is a validated query as SQL fragments the store splices into its own
// SELECT, Args binding Where's placeholders in order. Where is empty when
// nothing constrains the query — the table holds exactly one collection.
type Compiled struct {
	Table string
	Where string
	Args  []any
	Order string
	Limit int
}

var (
	// A namespace is `owner/name`: the owner segment is the isolation class a
	// grant hands out (`app`, `core`), the name segment identifies the holder.
	namePart      = `[a-z0-9][a-z0-9_-]*`
	namespaceRe   = regexp.MustCompile(`^` + namePart + `/` + namePart + `$`)
	collectionRe  = regexp.MustCompile(`^` + namePart + `$`)
	documentIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	fieldNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableNameRe   = regexp.MustCompile(`^doc_[1-9][0-9]*$`)
	reservedField = map[string]bool{FieldCreatedAt: true, FieldUpdatedAt: true}
)

// These names are spliced into SQL as identifiers; each derives from something
// already checked (an integer row id, or a field name matching fieldNameRe).
const (
	tablePrefix = "doc_"
	// fieldColumnPrefix keeps a declared field (`id`, `body`) from shadowing the
	// columns the store owns.
	fieldColumnPrefix = "f_"
)

// TableName is minted from the declaration's row id, so no identifier is ever a
// function of caller text.
func TableName(id int64) string {
	return fmt.Sprintf("%s%d", tablePrefix, id)
}

// ValidateTableName accepts a minted table name; Compile calls it before
// splicing, so a schema from anywhere but the store's read path fails loudly.
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

// FieldExpression is what that column computes; the column is VIRTUAL, which is
// why a declaration rewrites no document.
func FieldExpression(field string) string {
	return "json_extract(body, '$." + field + "')"
}

// ColumnAffinity maps a declared type to a SQLite affinity: "5" in a NUMERIC
// column orders as the number 5, while an array or object keeps its JSON text
// and stays orderable rather than erroring.
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

// quoteIdent is belt-and-braces over the validating patterns; it makes a
// keyword-named field harmless.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ValidateNamespace accepts a well-formed `owner/name`; which owners exist is
// not this package's concern.
func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("docstore: namespace is required, as owner/name (for example app/approval-gate)")
	}
	if !namespaceRe.MatchString(ns) {
		return fmt.Errorf("docstore: namespace %q is not owner/name, where each part is lowercase letters, digits, - or _ (for example app/approval-gate)", ns)
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

// ValidateDocumentID accepts a caller-chosen document id.
func ValidateDocumentID(id string) error {
	if id == "" {
		return fmt.Errorf("docstore: document id is required")
	}
	if !documentIDRe.MatchString(id) {
		return fmt.Errorf("docstore: document id %q must start alphanumeric and contain only letters, digits, ., - or _", id)
	}
	return nil
}

// Validate checks a declaration. Field names must be plain identifiers because a
// declared field becomes both a JSON path and an executed column name.
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

func (s CollectionSchema) field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// declaredNames lists the queryable fields, reserved included, so a rejected
// query says what it could have asked for.
func (s CollectionSchema) declaredNames() string {
	names := make([]string, 0, len(s.Fields)+2)
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	names = append(names, FieldCreatedAt, FieldUpdatedAt)
	return strings.Join(names, ", ")
}

// Compile validates q against the declaration and returns it as SQL; every
// rejection is an *InvalidQueryError, typed once here. anchor is the document
// q.After names, read by the caller (this package holds no DB handle); nil with
// q.After set is an error, not an empty page.
func (q Query) Compile(schema CollectionSchema, anchor *Document) (Compiled, error) {
	compiled, err := q.compile(schema, anchor)
	if err != nil {
		return Compiled{}, InvalidQuery(err)
	}
	return compiled, nil
}

func (q Query) compile(schema CollectionSchema, anchor *Document) (Compiled, error) {
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

	// No namespace/collection predicate: the table IS the collection, so the
	// isolation is structural.
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
		// The id tiebreaker runs in the sort's own direction, so the visible order
		// is one uniformly directed tuple — what After compares against.
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

// afterTuple compiles the After cursor as "strictly past the anchor in the
// visible order (sort field, id)":
//
//	sort <cmp> value OR (sort = value AND id <cmp> anchorID)
//
// A missing or JSON-null sort value is a real case; NULL compares as nothing,
// so it is branched on rather than bound. SQLite sorts NULL first: in ASC every
// non-NULL row is past a NULL anchor, in DESC none is.
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

	// The anchor's sort value is read back through the same column the ORDER BY
	// uses, not bound from Go: a "number" field may hold an array or object with
	// no bindable Go equivalent, and the column's own affinity must govern the
	// comparison. The subquery is uncorrelated, evaluated once per statement.
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

// anchorSortIsNull reports whether the cursor's sort field is absent or JSON
// null — what json_extract yields as SQL NULL.
func (q Query) anchorSortIsNull(anchor *Document) (bool, error) {
	if reservedField[q.Sort.Field] { // stamped columns, never NULL
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

// fieldExpr resolves a field reference to SQL: a reserved name literally,
// anything else through its declared generated column. use names whether the
// filter or the sort is wrong.
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

// bindValue refuses a bound that cannot compare with the field's type: a number
// field against "5" would silently match nothing.
func (q Query) bindValue(f Filter, spec FieldSpec) (any, error) {
	mismatch := func(want string) error {
		return fmt.Errorf("docstore: %s/%s filter on %q needs a %s value, got %T (%v)",
			q.Namespace, q.Collection, f.Field, want, f.Value, f.Value)
	}
	if f.Value == nil {
		return nil, fmt.Errorf("docstore: %s/%s filter on %q has no value", q.Namespace, q.Collection, f.Field)
	}
	// A reserved field is a timestamp compared as text; re-encode to TimeFormat
	// because a raw "…T10:00:00Z" bound sorts above every stamp in that second.
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

// TimeFormat is the stored timestamp encoding. Stamps are ordered as text, so
// the fraction must be fixed-width (nine digits, always present) and the zone
// always "Z": RFC3339Nano strips trailing zeros, which made same-second stamps
// compare wrongly and "changed since" filters drop rows in silence. Migration
// 91 rewrote the stored stamps.
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// ParseTime decodes a stamp in any RFC3339 form — including the pre-migration-91
// trailing-zero-stripped form this store once handed out — normalized to UTC.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// Target is the subject collection changes publish under and subscriptions match
// on. Collection-grained: a live query's result set can change because a document
// it does not contain started matching.
func Target(namespace, collection string) string {
	return namespace + "/" + collection
}

// Target is the subject changes to the query's collection arrive under.
func (q Query) Target() string { return Target(q.Namespace, q.Collection) }

// Address is the subject a change to one document is published under;
// subscription matching uses Target, carried alongside.
func Address(namespace, collection, id string) string {
	return Target(namespace, collection) + "/" + id
}

// ValidateBody accepts any JSON object; objects only, because a declared field
// is read with a JSON path a bare array or scalar lacks.
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
