package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn doc` is the document store's operator surface, and until the extension
// runtime exists it is also its only consumer.
//
// Everything here goes through the daemon rather than opening the database
// directly, which is the opposite of `attn bus`. The reason is the change fact: a
// write that reached the table without publishing one would leave every live
// query rendering a result set the store no longer agrees with. `attn bus` can
// take the direct path precisely because its job is to work when the daemon does
// not.

func runDoc() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeDocHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "define":
		runDocDefine(args)
	case "undefine":
		runDocUndefine(args)
	case "collections":
		runDocCollections(args)
	case "put":
		runDocPut(args)
	case "get":
		runDocGet(args)
	case "delete":
		runDocDelete(args)
	case "query":
		runDocQuery(args)
	case "watch":
		runDocWatch(args)
	default:
		fmt.Fprintf(os.Stderr, "doc: unknown command %q\n", os.Args[2])
		writeDocHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeDocHelp(w io.Writer) {
	fmt.Fprintf(w, `usage: attn doc <command>

Documents are JSON objects addressed by <namespace> <collection> <id>. A
namespace is owner/name (for example ext/approval-gate).

A collection declares which fields may be filtered and sorted on; created_at and
updated_at are always available and are never declared. Everything else in a
document body is stored and returned untouched.

commands:
  define <namespace> <collection> [field:type ...]
        declare a collection, replacing any previous declaration. Types are
        string, number and bool. Redeclaring is how a collection gains a
        queryable field; no document is rewritten.

  undefine <namespace> <collection>
        remove a collection's declaration and every document under it.

  collections [--json]
        list every declared collection.

  put <namespace> <collection> <id> <body|->
        write a document. The body is a JSON object, or - to read stdin.

  get <namespace> <collection> <id> [--json]
  delete <namespace> <collection> <id>

  query <namespace> <collection> [query flags] [--json]
        run a query once.

  watch <namespace> <collection> [query flags] [--json]
        run a live query: print the current results, then print them again every
        time a write changes them. Runs until interrupted.

query flags:
  --where <expr>    repeatable. field=value, or field<value, field<=value,
                    field>value, field>=value. The value is read as JSON when it
                    parses as one, and as a string otherwise, so --where
                    status=pending sends "pending" and --where attempts>=5 sends 5.
  --sort <field>    order by a declared field, or created_at / updated_at.
  --desc            reverse the sort.
  --limit <n>       at most n documents (default %d, maximum %d).
`, docstore.DefaultLimit, docstore.MaxLimit)
}

func docClient() *client.Client {
	return client.New(config.SocketPath())
}

func docFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "doc %s: %v\n", verb, err)
	os.Exit(1)
}

// docTarget reads the leading <namespace> <collection> every command starts with.
func docTarget(verb string, args []string) (string, string, []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "doc %s: needs <namespace> <collection>\n", verb)
		os.Exit(2)
	}
	return args[0], args[1], args[2:]
}

func runDocDefine(args []string) {
	namespace, collection, rest := docTarget("define", args)
	schema := protocol.DocumentCollectionSchema{
		Namespace:  namespace,
		Collection: collection,
		Fields:     []protocol.DocumentFieldSpec{},
	}
	for _, spec := range rest {
		name, kind, ok := strings.Cut(spec, ":")
		if !ok {
			docFail("define", fmt.Errorf("field %q is not name:type (types: string, number, bool)", spec))
		}
		schema.Fields = append(schema.Fields, protocol.DocumentFieldSpec{Name: name, Type: kind})
	}
	result, err := docClient().DocDefine(schema)
	if err != nil {
		docFail("define", err)
	}
	fmt.Printf("declared %s/%s with %d queryable field(s)\n", result.Namespace, result.Collection, len(schema.Fields))
}

func runDocUndefine(args []string) {
	namespace, collection, _ := docTarget("undefine", args)
	result, err := docClient().DocUndefine(namespace, collection)
	if err != nil {
		docFail("undefine", err)
	}
	fmt.Printf("removed %s/%s and %d document(s)\n", result.Namespace, result.Collection, result.DocumentsRemoved)
}

func runDocCollections(args []string) {
	asJSON := hasFlag(args, "--json")
	result, err := docClient().DocCollections()
	if err != nil {
		docFail("collections", err)
	}
	if asJSON {
		writeJSON(result.Collections)
		return
	}
	if len(result.Collections) == 0 {
		fmt.Println("no collections declared")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tCOLLECTION\tQUERYABLE FIELDS")
	for _, c := range result.Collections {
		names := make([]string, 0, len(c.Fields))
		for _, f := range c.Fields {
			names = append(names, f.Name+":"+f.Type)
		}
		if len(names) == 0 {
			names = append(names, "-")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.Namespace, c.Collection, strings.Join(names, " "))
	}
	w.Flush()
}

func runDocPut(args []string) {
	namespace, collection, rest := docTarget("put", args)
	if len(rest) < 2 {
		docFail("put", fmt.Errorf("needs <id> and a JSON body (or - for stdin)"))
	}
	id, body := rest[0], rest[1]
	if body == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			docFail("put", fmt.Errorf("reading stdin: %w", err))
		}
		body = string(raw)
	}
	if _, err := docClient().DocPut(namespace, collection, id, body); err != nil {
		docFail("put", err)
	}
	fmt.Printf("wrote %s/%s/%s\n", namespace, collection, id)
}

func runDocGet(args []string) {
	namespace, collection, rest := docTarget("get", args)
	if len(rest) < 1 {
		docFail("get", fmt.Errorf("needs <id>"))
	}
	result, err := docClient().DocGet(namespace, collection, rest[0])
	if err != nil {
		docFail("get", err)
	}
	if !result.Found {
		fmt.Fprintf(os.Stderr, "doc get: %s/%s/%s does not exist\n", namespace, collection, rest[0])
		os.Exit(1)
	}
	if hasFlag(rest, "--json") {
		writeJSON(result.Document)
		return
	}
	fmt.Println(result.Document.Body)
}

func runDocDelete(args []string) {
	namespace, collection, rest := docTarget("delete", args)
	if len(rest) < 1 {
		docFail("delete", fmt.Errorf("needs <id>"))
	}
	result, err := docClient().DocDelete(namespace, collection, rest[0])
	if err != nil {
		docFail("delete", err)
	}
	if !result.Existed {
		fmt.Printf("%s/%s/%s did not exist\n", namespace, collection, rest[0])
		return
	}
	fmt.Printf("deleted %s/%s/%s\n", namespace, collection, rest[0])
}

func runDocQuery(args []string) {
	namespace, collection, rest := docTarget("query", args)
	query, asJSON := parseDocQueryFlags("query", namespace, collection, rest)
	result, err := docClient().DocQuery(query)
	if err != nil {
		docFail("query", err)
	}
	printDocuments(result.Documents, asJSON)
}

func runDocWatch(args []string) {
	namespace, collection, rest := docTarget("watch", args)
	query, asJSON := parseDocQueryFlags("watch", namespace, collection, rest)
	err := docClient().DocSubscribe(query, func(result *protocol.DocSubscribeResult) bool {
		if asJSON {
			writeJSON(result)
		} else {
			fmt.Printf("--- revision %d (%d document(s)) ---\n", result.Revision, len(result.Documents))
			printDocuments(result.Documents, false)
		}
		return true
	})
	if err != nil {
		docFail("watch", err)
	}
}

// parseDocQueryFlags reads the query flags shared by query and watch.
func parseDocQueryFlags(verb, namespace, collection string, args []string) (protocol.DocumentQuery, bool) {
	query := protocol.DocumentQuery{Namespace: namespace, Collection: collection}
	asJSON := false
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 >= len(args) {
				docFail(verb, fmt.Errorf("%s needs a value", args[i]))
			}
			i++
			return args[i]
		}
		switch args[i] {
		case "--json":
			asJSON = true
		case "--desc":
			desc := true
			if query.Sort == nil {
				query.Sort = &protocol.DocumentSort{}
			}
			query.Sort.Desc = &desc
		case "--sort":
			field := next()
			if query.Sort == nil {
				query.Sort = &protocol.DocumentSort{}
			}
			query.Sort.Field = field
		case "--limit":
			raw := next()
			n, err := strconv.Atoi(raw)
			if err != nil {
				docFail(verb, fmt.Errorf("--limit %q is not a number", raw))
			}
			query.Limit = &n
		case "--where":
			filter, err := parseDocWhere(next())
			if err != nil {
				docFail(verb, err)
			}
			query.Filters = append(query.Filters, filter)
		default:
			docFail(verb, fmt.Errorf("unknown flag %q", args[i]))
		}
	}
	if query.Sort != nil && query.Sort.Field == "" {
		docFail(verb, fmt.Errorf("--desc needs --sort <field>"))
	}
	return query, asJSON
}

// docWhereOps are matched longest-first so ">=" is not read as ">".
var docWhereOps = []struct {
	token string
	op    docstore.Op
}{
	{">=", docstore.OpGte},
	{"<=", docstore.OpLte},
	{"=", docstore.OpEq},
	{">", docstore.OpGt},
	{"<", docstore.OpLt},
}

// parseDocWhere reads one --where expression. The bound is taken as JSON when it
// parses as JSON and as a string otherwise, so `status=pending` and
// `attempts>=5` both mean what they look like without any quoting ceremony. A
// string that looks like a number can still be written as status='"5"'.
func parseDocWhere(expr string) (protocol.DocumentFilter, error) {
	for _, candidate := range docWhereOps {
		field, value, ok := strings.Cut(expr, candidate.token)
		if !ok {
			continue
		}
		if field == "" {
			return protocol.DocumentFilter{}, fmt.Errorf("--where %q has no field name", expr)
		}
		return protocol.DocumentFilter{
			Field:     field,
			Op:        string(candidate.op),
			ValueJson: docBoundAsJSON(value),
		}, nil
	}
	return protocol.DocumentFilter{}, fmt.Errorf("--where %q needs one of = < <= > >= (for example status=pending)", expr)
}

func docBoundAsJSON(raw string) string {
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		return raw
	}
	encoded, _ := json.Marshal(raw)
	return string(encoded)
}

func printDocuments(docs []protocol.StoredDocument, asJSON bool) {
	if asJSON {
		writeJSON(docs)
		return
	}
	if len(docs) == 0 {
		fmt.Println("no documents")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tBODY")
	for _, doc := range docs {
		fmt.Fprintf(w, "%s\t%s\t%s\n", doc.ID, doc.UpdatedAt, doc.Body)
	}
	w.Flush()
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func writeJSON(v any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "doc: encoding output: %v\n", err)
		os.Exit(1)
	}
}
