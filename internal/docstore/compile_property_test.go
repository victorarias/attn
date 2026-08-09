package docstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"
)

// This package's stated invariant is a security boundary, not a nicety: "every
// identifier the store executes is derived from an integer or a validated field
// name — never from caller text" (AGENTS.md). The example tests in
// docstore_test.go check that on the queries we thought of. A query, though, is
// a JSON object that arrives from an extension, a UI tile, or `attn doc`, and
// the string that breaks the rule is by definition one nobody wrote down.
//
// So rapid drives Compile with caller strings drawn from a pool of the things an
// attacker or a confused caller actually sends — quotes, semicolons, comment
// introducers, empty, unicode, absurdly long — in every position a caller
// controls, and the assertion is not "it did not blow up" but a grammar: the SQL
// that comes out is tokenized, and every token has to be a keyword, a stamped
// column, the minted table, a `?`, or a quoted `f_<declared field>`. Anything
// else fails, whatever it happens to say. There is no substring check to
// out-quote, and no database is involved: Compile is pure.

// hostile is what a caller string can be. Nothing here is legal in any position
// that reaches an identifier, so a compile that succeeds must have kept every
// one of them out of the SQL — or bound it as an argument, where it is inert.
var hostile = []string{
	"",
	" ",
	`"`,
	`x"; DROP TABLE doc_1; --`,
	`'; DELETE FROM doc_1; --`,
	"`backtick`",
	"[bracketed]",
	"a/*comment*/b",
	"a--comment",
	"f_title",
	"doc_1",
	"1",
	"-",
	"a b",
	"a\nb",
	"a\x00b",
	"日本語",
	"ns/coll",
	"SELECT",
	"*",
	strings.Repeat("a", 300),
}

// declaredFields is the collection under test: names that pass Validate, one of
// each type, so a filter has somewhere legitimate to land.
var declaredFields = []FieldSpec{
	{Name: "title", Type: FieldString},
	{Name: "count", Type: FieldNumber},
	{Name: "done", Type: FieldBool},
	{Name: "_private", Type: FieldString},
}

// canary values are what a filter bound may be. A bound is caller text with no
// restriction at all — the whole point of binding it — so the property is that
// it reaches Args and never the statement.
var canaryValues = []any{
	`'); DROP TABLE doc_1; --`,
	`" OR 1=1 --`,
	"ordinary",
	"",
	float64(42),
	int(7),
	true,
	false,
	nil,
	json.Number("3.5"),
	"2026-08-09T10:00:00Z",
	"not-a-timestamp",
	[]string{"array"},
	map[string]any{"object": true},
}

var (
	legalFieldRefs = func() []string {
		refs := []string{FieldCreatedAt, FieldUpdatedAt}
		for _, f := range declaredFields {
			refs = append(refs, f.Name)
		}
		return refs
	}()
	badOps = []Op{"like", "", "= 1 OR 1", "EQ", "<>"}
)

// sqlKeywords is every bare word Compile is allowed to write. The list is
// closed on purpose: a new one has to be added here deliberately, which is the
// review step that catches a splice added by accident.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true,
	"AND": true, "OR": true, "IS": true, "NOT": true, "NULL": true,
	"ASC": true, "DESC": true,
	// The store's own stamped columns, written literally because they are not
	// caller text.
	"id": true, FieldCreatedAt: true, FieldUpdatedAt: true,
}

// checkSQLIdentifiers walks one SQL fragment and reports the first token that is
// not something Compile is allowed to have written. allowedTable is the minted
// table name, allowedColumns the generated columns of the declared fields.
func checkSQLIdentifiers(fragment, allowedTable string, allowedColumns map[string]bool) error {
	for i := 0; i < len(fragment); {
		c := fragment[i]
		switch {
		case c == '"':
			// A quoted identifier. Read to the closing quote, undoubling as
			// SQLite does, and check what it actually names.
			var ident strings.Builder
			i++
			for {
				if i >= len(fragment) {
					return fmt.Errorf("unterminated quoted identifier in %q", fragment)
				}
				if fragment[i] == '"' {
					if i+1 < len(fragment) && fragment[i+1] == '"' {
						ident.WriteByte('"')
						i += 2
						continue
					}
					i++
					break
				}
				ident.WriteByte(fragment[i])
				i++
			}
			if !allowedColumns[ident.String()] {
				return fmt.Errorf("quoted identifier %q is not a declared field's column", ident.String())
			}
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			start := i
			for i < len(fragment) && (fragment[i] == '_' || isASCIIAlnum(fragment[i])) {
				i++
			}
			word := fragment[start:i]
			if !sqlKeywords[word] && word != allowedTable {
				return fmt.Errorf("bare identifier %q is neither a keyword, a stamped column, nor the minted table %q", word, allowedTable)
			}
		case c >= '0' && c <= '9':
			return fmt.Errorf("a literal number is spliced into %q; every value belongs in Args", fragment)
		case strings.IndexByte("?()<>=, ", c) >= 0:
			i++
		default:
			return fmt.Errorf("byte %q has no place in compiled SQL (%q)", string(rune(c)), fragment)
		}
	}
	return nil
}

func isASCIIAlnum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// TestCompiledSQLIsBuiltOnlyFromDerivedIdentifiers is the headline property: for
// any caller-controlled input, Compile either refuses — as a typed
// InvalidQueryError, which is what the surfaces above branch on — or produces
// SQL whose every identifier it derived itself.
func TestCompiledSQLIsBuiltOnlyFromDerivedIdentifiers(t *testing.T) {
	allowedColumns := make(map[string]bool, len(declaredFields))
	for _, f := range declaredFields {
		allowedColumns[FieldColumn(f.Name)] = true
	}

	attempts, compiles := 0, 0
	rapid.Check(t, func(t *rapid.T) {
		attempts++

		// Draw a query the store would accept, then corrupt exactly one part of
		// it — or none. Corrupting one position at a time is what keeps the
		// hostile string reaching the code that handles that position: a query
		// with a bad namespace AND a bad sort field is refused at the namespace
		// and says nothing about sorting.
		corrupt := rapid.SampledFrom([]string{
			"", "", "", "", // half the draws are left well-formed
			"table", "namespace", "collection", "limit",
			"filter_field", "filter_value", "filter_op", "sort_field", "after",
		}).Draw(t, "corrupt")
		spoil := func(position, legal string) string {
			if corrupt != position {
				return legal
			}
			return rapid.SampledFrom(hostile).Draw(t, position)
		}

		schema := CollectionSchema{
			Namespace:  "ext/props",
			Collection: "records",
			Fields:     declaredFields,
			Table: spoil("table", rapid.SampledFrom([]string{
				TableName(1), TableName(12), TableName(9007199254740991),
			}).Draw(t, "minted_table")),
		}
		// The declaration itself is always one the store would have accepted:
		// field names are validated at declare time, and that is the assumption
		// the identifier rule rests on. What varies here is everything a caller
		// sends afterwards.
		if err := (CollectionSchema{Namespace: schema.Namespace, Collection: schema.Collection, Fields: schema.Fields}).Validate(); err != nil {
			t.Fatalf("the fixture declaration does not validate: %v", err)
		}

		limit := rapid.SampledFrom([]int{0, 1, 10, DefaultLimit, MaxLimit}).Draw(t, "legal_limit")
		if corrupt == "limit" {
			limit = rapid.IntRange(-1000, MaxLimit*3).Draw(t, "limit")
		}
		q := Query{
			Namespace:  spoil("namespace", schema.Namespace),
			Collection: spoil("collection", schema.Collection),
			Limit:      limit,
			After:      spoil("after", rapid.SampledFrom([]string{"", "doc-1", "A.b_c-9"}).Draw(t, "legal_after")),
		}
		for range rapid.IntRange(0, 2).Draw(t, "filters") {
			field := spoil("filter_field", rapid.SampledFrom(legalFieldRefs).Draw(t, "legal_filter_field"))
			op := rapid.SampledFrom([]Op{OpEq, OpLt, OpLte, OpGt, OpGte}).Draw(t, "legal_filter_op")
			if corrupt == "filter_op" {
				op = rapid.SampledFrom(badOps).Draw(t, "filter_op")
			}
			value := typedValue(t, field)
			if corrupt == "filter_value" {
				value = rapid.SampledFrom(canaryValues).Draw(t, "filter_value")
			}
			q.Filters = append(q.Filters, Filter{Field: field, Op: op, Value: value})
		}
		if rapid.Bool().Draw(t, "sorted") {
			q.Sort = &Sort{
				Field: spoil("sort_field", rapid.SampledFrom(legalFieldRefs).Draw(t, "legal_sort_field")),
				Desc:  rapid.Bool().Draw(t, "sort_desc"),
			}
		}

		var anchor *Document
		if q.After != "" {
			anchor = &Document{
				ID:   q.After,
				Body: json.RawMessage(rapid.SampledFrom(anchorBodies).Draw(t, "anchor_body")),
			}
		}

		compiled, err := q.Compile(schema, anchor)
		if err != nil {
			if !IsInvalidQuery(err) {
				t.Fatalf("Compile refused the query with an untyped error, which no caller can branch on: %v", err)
			}
			return
		}
		compiles++

		if err := ValidateTableName(compiled.Table); err != nil {
			t.Fatalf("compiled against table %q: %v", compiled.Table, err)
		}
		for name, fragment := range map[string]string{"where": compiled.Where, "order": compiled.Order} {
			if err := checkSQLIdentifiers(fragment, compiled.Table, allowedColumns); err != nil {
				t.Fatalf("the %s fragment %q was compiled from caller text: %v", name, fragment, err)
			}
		}
		if got := strings.Count(compiled.Where, "?"); got != len(compiled.Args) {
			t.Fatalf("where %q has %d placeholders but %d args; a value was spliced or dropped",
				compiled.Where, got, len(compiled.Args))
		}
		if compiled.Limit < 1 || compiled.Limit > MaxLimit {
			t.Fatalf("compiled limit is %d, outside 1..%d", compiled.Limit, MaxLimit)
		}
	})

	// The tripwire against a vacuous property. Everything above only asserts
	// anything on a query that compiled; a generator that drifts into refusing
	// every draw would go green while checking nothing, which is the failure
	// mode a property test hides best. One in five is far below what the
	// generators produce and far above zero.
	if compiles*5 < attempts {
		t.Fatalf("only %d of %d generated queries compiled; the generators refuse too much to be checking anything",
			compiles, attempts)
	}
}

// typedValue draws a bound the field's declared type accepts, so a well-formed
// query actually compiles. The bounds are still nasty strings where the type
// allows one — a string field takes `'); DROP TABLE …` perfectly legitimately,
// and that is the case worth compiling, because it is the one that proves the
// value went to Args rather than into the statement.
func typedValue(t *rapid.T, field string) any {
	switch field {
	case "title", "_private":
		return rapid.SampledFrom([]any{"ordinary", "", `'); DROP TABLE doc_1; --`, `" OR 1=1 --`}).Draw(t, "string_value")
	case "count":
		return rapid.SampledFrom([]any{float64(42), int(7), json.Number("3.5")}).Draw(t, "number_value")
	case "done":
		return rapid.SampledFrom([]any{true, false}).Draw(t, "bool_value")
	case FieldCreatedAt, FieldUpdatedAt:
		return rapid.SampledFrom([]any{"2026-08-09T10:00:00Z", "2026-08-09T10:00:00.000000000Z"}).Draw(t, "time_value")
	default:
		// The field name was corrupted; nothing types against it.
		return rapid.SampledFrom(canaryValues).Draw(t, "filter_value")
	}
}

// anchorBodies are the cursor documents a paging query can be compiled against:
// the sort field present, absent, and explicitly null are three different
// branches in afterTuple, and a body that is not an object at all is a refusal.
var anchorBodies = []string{
	`{"title":"a","count":1,"done":true}`,
	`{}`,
	`{"title":null}`,
	`{"title":["array"]}`,
	`[]`,
	`not json`,
}

// quoteIdent is the one splice defended by quoting rather than by validation, so
// it has to be safe for a name that never passed a validator — a field declared
// before a validation rule existed, say. The property is that it round-trips:
// what SQLite reads back out of the quoted form is exactly the name that went
// in, so there is no string that closes the quote early and starts meaning
// something.
func TestQuotingAnIdentifierRoundTrips(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		quoted := quoteIdent(name)

		if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
			t.Fatalf("quoteIdent(%q) = %q is not a quoted identifier", name, quoted)
		}
		got, rest, err := readQuotedIdent(quoted)
		if err != nil {
			t.Fatalf("quoteIdent(%q) = %q does not parse back: %v", name, quoted, err)
		}
		if rest != "" {
			t.Fatalf("quoteIdent(%q) = %q ends the identifier early, leaving %q as SQL", name, quoted, rest)
		}
		if got != name {
			t.Fatalf("quoteIdent(%q) reads back as %q", name, got)
		}
	})
}

// readQuotedIdent decodes a SQLite double-quoted identifier: doubled quotes are
// one literal quote, the first single quote ends it. It returns the name and
// whatever followed, which is what tells a broken quoting apart from a working
// one.
func readQuotedIdent(s string) (string, string, error) {
	if len(s) == 0 || s[0] != '"' {
		return "", "", fmt.Errorf("does not start with a quote")
	}
	var out strings.Builder
	for i := 1; i < len(s); {
		if s[i] != '"' {
			out.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '"' {
			out.WriteByte('"')
			i += 2
			continue
		}
		return out.String(), s[i+1:], nil
	}
	return "", "", fmt.Errorf("unterminated")
}

// The two physical names are the whole derivation chain: a table comes from a
// row id and a column from a validated field name. Neither may produce something
// that stops being a plain identifier, whatever it is handed.
func TestPhysicalNamesStayPlainIdentifiers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.Int64Range(1, 1<<53-1).Draw(t, "row_id")
		table := TableName(id)
		if err := ValidateTableName(table); err != nil {
			t.Fatalf("TableName(%d) = %q is not accepted by its own validator: %v", id, table, err)
		}

		nonPositive := rapid.Int64Range(-1<<20, 0).Draw(t, "non_positive_row_id")
		if err := ValidateTableName(TableName(nonPositive)); err == nil {
			t.Fatalf("TableName(%d) = %q was accepted; only a real row id mints a table",
				nonPositive, TableName(nonPositive))
		}

		name := rapid.StringMatching(`[A-Za-z_][A-Za-z0-9_]{0,16}`).Draw(t, "field")
		column := FieldColumn(name)
		if !strings.HasPrefix(column, fieldColumnPrefix) {
			t.Fatalf("FieldColumn(%q) = %q lost its prefix, so it can collide with a column the store owns", name, column)
		}
		for _, r := range column {
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				t.Fatalf("FieldColumn(%q) = %q holds %q, which needs quoting to be an identifier", name, column, r)
			}
		}
		if expr := FieldExpression(name); strings.ContainsAny(expr, "'\"") != strings.Contains(expr, "'$.") {
			t.Fatalf("FieldExpression(%q) = %q carries a quote it did not put there", name, expr)
		}
	})
}
