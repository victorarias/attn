package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

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
	case "count":
		runDocCount(args)
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

A declared field is indexed, so filtering and sorting on it reads an index
rather than every document. Declaring one rewrites nothing: the index is built
from the bodies already stored.

commands:
  define <namespace> <collection> [field:type ...]
        declare a collection, replacing any previous declaration. Types are
        string, number and bool, and the type decides how stored values compare:
        a body holding "5" in a number field sorts as the number 5. Redeclaring
        is how a collection gains or loses a queryable field, and builds or drops
        that field's index; no document is rewritten.

  undefine <namespace> <collection>
        remove a collection's declaration and every document under it.

  collections [--json]
        list every declared collection, and the indexed field each one offers.

  put <namespace> <collection> <id> <body|-> [--expect <rev|absent>]
        write a document. The body is a JSON object, or - to read stdin.
        Prints the revision the document now has.

        --expect makes the write conditional. Every read reports a document's
        revision, so pass the one you read to change a document only if nobody
        has changed it since; the write is refused, naming both revisions, if
        they have. --expect absent writes only if the document does not exist
        yet. Without --expect the write always wins, which is what you want for
        a value you are setting rather than editing.

  get <namespace> <collection> <id> [--json]
  delete <namespace> <collection> <id> [--expect <rev>]
        --expect removes the document only if it is still at that revision.

  query <namespace> <collection> [query flags] [--json]
        run a query once. Reports, beside the results, the log position the
        answer was true at: put and delete print the position their write
        landed at, so comparing the two tells you whether an answer already
        includes a write you made.

  count <namespace> <collection> [query flags]
        how many documents match, without fetching them. --sort, --desc and
        --limit are ignored: they decide which matches come back, never how
        many there are.

  watch <namespace> <collection> [query flags] [--json] [--resume]
        run a live query: print the current window, then print it again every
        time a write changes it. Runs until interrupted, and exits non-zero if
        the subscription ends — a watch that has stopped watching must not look
        like a watch that finished.

        What is printed is the whole window; what travels is the order plus only
        the bodies this watcher does not already hold, which --json reports as
        "changed". A live query takes no --after: it is a window, not a walk.

        --resume resubscribes when the connection goes — a daemon restart, for
        instance — waiting for the daemon to come back and declaring what it
        holds, so only what changed while it was away comes back. A collection
        that is removed or redeclared without a field the query uses ends the
        watch either way, and so does a daemon that was never there.

query flags:
  --where <expr>    repeatable. field=value, or field<value, field<=value,
                    field>value, field>=value. The value is read as JSON when it
                    parses as one, and as a string otherwise, so --where
                    status=pending sends "pending" and --where attempts>=5 sends 5.
  --sort <field>    order by a declared field, or created_at / updated_at.
  --desc            reverse the sort.
  --limit <n>       at most n documents (default %d, maximum %d).
  --after <id>      the next page: documents that come after this one in the
                    same order. Pass the id of the last document of the previous
                    page. Use this rather than a --where on the sort field —
                    documents sharing a sort value would otherwise be skipped or
                    repeated.
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
	// "indexed" rather than "queryable": everything in a body can be read, and
	// what declaring buys is that these are the ones a query may name — and the
	// ones it reaches through an index rather than a scan.
	fmt.Fprintln(w, "NAMESPACE\tCOLLECTION\tINDEXED FIELDS")
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
	rest, expect := takeExpectFlag("put", rest, true)
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
	result, err := docClient().DocPut(namespace, collection, id, body, expect)
	if err != nil {
		docFail("put", err)
	}
	// The seq is the write's position on the durable log, printed because it is
	// what a caller compares against a later read to know the read includes this
	// write.
	fmt.Printf("wrote %s/%s/%s (rev %d, seq %d)\n", namespace, collection, id, result.Rev, result.Seq)
}

// takeExpectFlag pulls `--expect <rev|absent>` out of a command's arguments and
// returns what is left, so the positional arguments can still be read by
// position. absent is offered only where "must not exist" is something the
// command can act on.
func takeExpectFlag(verb string, args []string, allowAbsent bool) ([]string, *int) {
	rest := make([]string, 0, len(args))
	var expect *int
	for i := 0; i < len(args); i++ {
		if args[i] != "--expect" {
			rest = append(rest, args[i])
			continue
		}
		if i+1 >= len(args) {
			docFail(verb, fmt.Errorf("--expect needs a revision%s", expectAbsentHint(allowAbsent)))
		}
		i++
		if args[i] == "absent" {
			if !allowAbsent {
				docFail(verb, fmt.Errorf("--expect absent asks to act on a document that is not there, which %s cannot do", verb))
			}
			absent := int(docstore.ExpectAbsent)
			expect = &absent
			continue
		}
		rev, err := strconv.Atoi(args[i])
		if err != nil || rev < int(docstore.FirstRev) {
			docFail(verb, fmt.Errorf("--expect %q is not a revision%s", args[i], expectAbsentHint(allowAbsent)))
		}
		expect = &rev
	}
	return rest, expect
}

func expectAbsentHint(allowAbsent bool) string {
	if allowAbsent {
		return ` (a number from a previous read, or "absent")`
	}
	return " (a number from a previous read)"
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
		writeJSON(docsForJSON([]protocol.StoredDocument{*result.Document})[0])
		return
	}
	fmt.Println(result.Document.Body)
	printPosition(false, result.AsOfSeq)
}

// printPosition reports the log position a read was true at, which is what a
// caller compares against the seq its own write returned to know whether the
// answer already includes it.
//
// It goes to stderr so it does not land in a body someone is piping into jq,
// and it is skipped under --json for the same reason: a machine reader gets the
// position from the wire, or from `doc count --json`, which carries it in band.
func printPosition(asJSON bool, seq int) {
	if asJSON {
		return
	}
	fmt.Fprintf(os.Stderr, "as of seq %d\n", seq)
}

func runDocDelete(args []string) {
	namespace, collection, rest := docTarget("delete", args)
	rest, expect := takeExpectFlag("delete", rest, false)
	if len(rest) < 1 {
		docFail("delete", fmt.Errorf("needs <id>"))
	}
	result, err := docClient().DocDelete(namespace, collection, rest[0], expect)
	if err != nil {
		docFail("delete", err)
	}
	if !result.Existed {
		fmt.Printf("%s/%s/%s did not exist\n", namespace, collection, rest[0])
		return
	}
	fmt.Printf("deleted %s/%s/%s (seq %d)\n", namespace, collection, rest[0], result.Seq)
}

func runDocQuery(args []string) {
	namespace, collection, rest := docTarget("query", args)
	query, opts := parseDocQueryFlags("query", namespace, collection, rest)
	result, err := docClient().DocQuery(query)
	if err != nil {
		docFail("query", err)
	}
	printDocuments(result.Documents, opts.asJSON)
	printPosition(opts.asJSON, result.AsOfSeq)
}

func runDocCount(args []string) {
	namespace, collection, rest := docTarget("count", args)
	query, opts := parseDocQueryFlags("count", namespace, collection, rest)
	result, err := docClient().DocCount(query)
	if err != nil {
		docFail("count", err)
	}
	if opts.asJSON {
		writeJSON(struct {
			Count   int `json:"count"`
			AsOfSeq int `json:"as_of_seq"`
		}{result.Count, result.AsOfSeq})
		return
	}
	fmt.Println(result.Count)
}

// runDocWatch prints applied windows: what the query holds right now, printed
// again whenever a write changes it. What travels is smaller than what is
// printed — a delivery carries ids plus only the bodies this watcher does not
// already hold — and `changed` is that receipt.
//
// It never exits 0 while it has stopped watching. A live query that ends has
// stopped reporting changes, and a watcher that returns success there leaves
// whoever ran it looking at a list frozen at the moment the subscription broke.
// --resume is the way to survive a daemon restart: it resubscribes carrying the
// revisions it holds, so the daemon sends back only what changed while it was
// away. A collection that was removed or redeclared out from under the query
// ends the watch anyway, because resubscribing would fail the same way.
func runDocWatch(args []string) {
	namespace, collection, rest := docTarget("watch", args)
	query, opts := parseDocQueryFlags("watch", namespace, collection, rest)

	var held []protocol.StoredDocument
	watching := false
	for {
		err := docClient().DocSubscribe(query, held, func(window client.DocWindow) bool {
			watching = true
			held = window.Documents
			printDocWindow(window, opts.asJSON)
			return true
		})
		code, ended := client.DocSubscriptionCode(err)
		switch {
		case opts.resume && ended && code == "":
			// The connection went, not the collection. Everything held stays
			// held, and the resubscribe declares it.
		case opts.resume && watching && !ended:
			// The daemon is not answering the door yet. Restarting one takes
			// long enough that a watch which gave up here would not survive the
			// thing --resume exists for; a watch that has never connected still
			// fails immediately, because that is a wrong socket, not an outage.
			time.Sleep(daemonReconnectInterval)
		default:
			docFail("watch", err)
		}
	}
}

// daemonReconnectInterval is how often a resuming watch retries a daemon that
// is not accepting connections. Measured: a stop plus ensure returns the socket
// in 0.49s, so this polls twice inside the outage it exists for, and costs one
// connect attempt per tick when the daemon is simply gone.
const daemonReconnectInterval = 200 * time.Millisecond

func printDocWindow(window client.DocWindow, asJSON bool) {
	if asJSON {
		writeJSON(struct {
			Delivery  int            `json:"delivery"`
			AsOfSeq   int64          `json:"as_of_seq"`
			Documents []jsonDocument `json:"documents"`
			Changed   []string       `json:"changed"`
		}{window.Delivery, window.AsOfSeq, docsForJSON(window.Documents), window.Changed})
		return
	}
	fmt.Printf("--- delivery %d (%d document(s), %d body(s) sent, as of seq %d) ---\n",
		window.Delivery, len(window.Documents), len(window.Changed), window.AsOfSeq)
	printDocuments(window.Documents, false)
}

// docQueryOptions are the flags that shape the output rather than the query.
type docQueryOptions struct {
	asJSON bool
	resume bool
}

// parseDocQueryFlags reads the query flags shared by query, count and watch.
func parseDocQueryFlags(verb, namespace, collection string, args []string) (protocol.DocumentQuery, docQueryOptions) {
	query := protocol.DocumentQuery{Namespace: namespace, Collection: collection}
	var opts docQueryOptions
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
			opts.asJSON = true
		case "--resume":
			if verb != "watch" {
				docFail(verb, fmt.Errorf("--resume is only for watch; a one-shot read has nothing to resume"))
			}
			opts.resume = true
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
		case "--after":
			after := next()
			query.After = &after
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
	return query, opts
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

// jsonDocument is what --json prints. The wire carries a body as a string
// because the protocol has no "arbitrary JSON" type, but a caller piping this
// into jq wants `.body.status`, not a string it has to decode a second time.
type jsonDocument struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int             `json:"rev"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func docsForJSON(docs []protocol.StoredDocument) []jsonDocument {
	out := make([]jsonDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, jsonDocument{
			ID:        doc.ID,
			Body:      json.RawMessage(doc.Body),
			Rev:       doc.Rev,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		})
	}
	return out
}

func printDocuments(docs []protocol.StoredDocument, asJSON bool) {
	if asJSON {
		writeJSON(docsForJSON(docs))
		return
	}
	if len(docs) == 0 {
		fmt.Println("no documents")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREV\tUPDATED\tBODY")
	for _, doc := range docs {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", doc.ID, doc.Rev, doc.UpdatedAt, doc.Body)
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
