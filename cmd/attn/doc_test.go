package main

import (
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

// A --where bound is read as JSON when it parses as JSON, and as a string
// otherwise, so the common cases need no quoting ceremony.
func TestWhereReadsTheBoundAsJSONWhenItIsJSON(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		field string
		op    docstore.Op
		value string
	}{
		{"status=pending", "status", docstore.OpEq, `"pending"`},
		{"attempts>=5", "attempts", docstore.OpGte, "5"},
		{"attempts>2", "attempts", docstore.OpGt, "2"},
		{"attempts<=9", "attempts", docstore.OpLte, "9"},
		{"attempts<9", "attempts", docstore.OpLt, "9"},
		{"urgent=true", "urgent", docstore.OpEq, "true"},
		{`status="5"`, "status", docstore.OpEq, `"5"`},
		{"updated_at>2026-08-03T00:00:00Z", "updated_at", docstore.OpGt, `"2026-08-03T00:00:00Z"`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := parseDocWhere(tc.expr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Field != tc.field || got.Op != string(tc.op) || got.ValueJson != tc.value {
				t.Fatalf("parsed %+v, want field=%s op=%s value=%s", got, tc.field, tc.op, tc.value)
			}
		})
	}
}

// ">=" must not be read as ">" with a value of "=5".
func TestWhereMatchesTheLongestOperatorFirst(t *testing.T) {
	got, err := parseDocWhere("attempts>=5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Op != string(docstore.OpGte) || got.ValueJson != "5" {
		t.Fatalf("parsed %+v", got)
	}
}

func TestWhereRejectsAnExpressionWithNoOperator(t *testing.T) {
	if _, err := parseDocWhere("status"); err == nil {
		t.Fatal("an expression with no operator was accepted")
	}
	if _, err := parseDocWhere("=pending"); err == nil {
		t.Fatal("an expression with no field was accepted")
	}
}

// The flag parser consumes values by advancing the index from inside a closure.
// Go 1.22 gave three-clause loops a per-iteration variable, so this pins that
// the advance still carries to the next iteration — otherwise a flag's value
// would be parsed again as if it were a flag.
func TestQueryFlagsConsumeTheirValues(t *testing.T) {
	query, asJSON := parseDocQueryFlags("query", "ext/approval-gate", "requests", []string{
		"--where", "status=pending",
		"--sort", "created_at",
		"--desc",
		"--limit", "7",
		"--json",
	})
	if !asJSON {
		t.Fatal("--json not seen")
	}
	if query.Namespace != "ext/approval-gate" || query.Collection != "requests" {
		t.Fatalf("target = %s/%s", query.Namespace, query.Collection)
	}
	if len(query.Filters) != 1 || query.Filters[0].Field != "status" || query.Filters[0].ValueJson != `"pending"` {
		t.Fatalf("filters = %+v", query.Filters)
	}
	if query.Sort == nil || query.Sort.Field != "created_at" || query.Sort.Desc == nil || !*query.Sort.Desc {
		t.Fatalf("sort = %+v", query.Sort)
	}
	if query.Limit == nil || *query.Limit != 7 {
		t.Fatalf("limit = %v", query.Limit)
	}
}

// --desc before --sort still applies to the sort that follows.
func TestDescBeforeSortStillReversesIt(t *testing.T) {
	query, _ := parseDocQueryFlags("query", "ext/a", "c", []string{"--desc", "--sort", "updated_at"})
	if query.Sort == nil || query.Sort.Field != "updated_at" || query.Sort.Desc == nil || !*query.Sort.Desc {
		t.Fatalf("sort = %+v", query.Sort)
	}
}

// Repeating --where accumulates, which is how a range on the sort field pages.
func TestWhereRepeatsToAccumulateFilters(t *testing.T) {
	query, _ := parseDocQueryFlags("query", "ext/a", "c", []string{
		"--where", "status=pending", "--where", "attempts>=2",
	})
	if len(query.Filters) != 2 {
		t.Fatalf("filters = %+v", query.Filters)
	}
	if query.Filters[1].Op != string(docstore.OpGte) || query.Filters[1].ValueJson != "2" {
		t.Fatalf("second filter = %+v", query.Filters[1])
	}
}
