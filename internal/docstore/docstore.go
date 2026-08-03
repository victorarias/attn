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
// See docs/plans/2026-08-03-ext-a3-doc-store.md.
package docstore

import (
	"encoding/json"
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
// decides how a filter's bound is bound to the statement, and comparing a number
// field against a string bound would otherwise match nothing at all rather than
// failing.
type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
)

// Op is a filter comparison. Equality plus the four range operators is the whole
// surface: it covers both proof compositions (a pending-requests panel, and
// records after a cursor), and range on the sort field is what makes cursor
// pagination work.
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
// and sorted on. It is the API contract, and it is deliberately not a storage
// schema — a document's body is arbitrary JSON, and an undeclared field is
// stored and read back untouched. Declaring a field says "you may query this",
// not "this must exist".
//
// v1 executes queries by scanning within a collection, so a declaration creates
// no index today. It is still the contract, because it is what lets indexes
// appear later without any author-facing change.
type CollectionSchema struct {
	Namespace  string      `json:"namespace"`
	Collection string      `json:"collection"`
	Fields     []FieldSpec `json:"fields"`
}

// Filter is one comparison against a declared or reserved field.
type Filter struct {
	Field string `json:"field"`
	Op    Op     `json:"op"`
	Value any    `json:"value"`
}

// Sort names the ordering field. Results always carry a stable total order —
// Compile appends the document id as a tiebreaker — so paging through a sort
// with ties cannot skip or repeat a document.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Query is the one representation every surface carries.
//
// A zero Limit means DefaultLimit rather than "unbounded": a query with no
// ceiling is a wire message with no ceiling.
type Query struct {
	Namespace  string   `json:"namespace"`
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
	Sort       *Sort    `json:"sort,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// Document is one stored record. Body is the document as written, byte for byte
// — record-shape evolution is handled by the reader (parse tolerantly, render
// tolerantly), never by migrating stored documents.
type Document struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Compiled is a validated query as SQL. Where and Order are fragments the store
// splices into its own SELECT; Args binds Where's placeholders in order.
type Compiled struct {
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
	reservedField = map[string]bool{FieldCreatedAt: true, FieldUpdatedAt: true}
)

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
// identifiers, which is also what makes the compiled JSON path safe to build by
// concatenation: there is no quoting to get wrong because there is nothing to
// quote.
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
func (q Query) Compile(schema CollectionSchema) (Compiled, error) {
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

	where := []string{"namespace = ?", "collection = ?"}
	args := []any{q.Namespace, q.Collection}

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

	order := "id ASC"
	if q.Sort != nil {
		expr, _, err := q.fieldExpr(schema, q.Sort.Field, "sort")
		if err != nil {
			return Compiled{}, err
		}
		dir := "ASC"
		if q.Sort.Desc {
			dir = "DESC"
		}
		// The id tiebreaker is what makes the order total. Without it two
		// documents sharing a sort value have no defined relative position, and
		// a paginating reader can see one twice and the other never.
		order = expr + " " + dir + ", id ASC"
	}

	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d; a limit must be positive", q.Namespace, q.Collection, limit)
	case limit > MaxLimit:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d, above the maximum of %d; page with a range filter on the sort field instead",
			q.Namespace, q.Collection, limit, MaxLimit)
	}

	return Compiled{Where: strings.Join(where, " AND "), Args: args, Order: order, Limit: limit}, nil
}

// fieldExpr resolves a field reference to SQL. A reserved name is a real column;
// anything else must be declared, and reads out of the body. use names what the
// reference was for, so the error says whether the filter or the sort is wrong.
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
	return "json_extract(body, '$." + name + "')", spec, nil
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
	// A reserved field is a stored timestamp column, compared as text.
	if reservedField[f.Field] {
		switch v := f.Value.(type) {
		case string:
			return v, nil
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

// TimeFormat is the stored timestamp encoding, matching the job queue's: it
// keeps sub-second precision and sorts lexicographically, which is what lets
// created_at and updated_at be ordering terms as plain text columns.
const TimeFormat = time.RFC3339Nano

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
