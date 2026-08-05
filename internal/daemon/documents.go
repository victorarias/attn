package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

// The document store's daemon half: the fact a write publishes, the live queries
// that fact wakes, and the IPC surface `attn doc` speaks.
//
// WHY A SUBSCRIPTION RE-RUNS RATHER THAN PATCHES. Every delivery is the query's
// current full result set. A patch stream would be smaller and it is where live
// query systems grow their bugs: a document falling out of a `limit 20` window
// forces a re-query to find the 21st anyway, so a patch path needs the re-run
// path underneath it and only some of the time. Re-running always is one path.
//
// That also makes a delivery safe to drop. Because each one supersedes the last,
// a subscriber that has not drained loses nothing when a newer result set
// replaces a queued one — which is how the one-slot wake channel below collapses
// a burst of writes into a single delivery with no coalescing window to manage.
//
// WHY THE BUS FAN-OUT DOES NOT WRITE THE SOCKET. The bus holds its publish lock
// across the inline fan-out, so anything slow inside a handler stalls every
// publisher in the daemon. A subscription therefore owns its goroutine: the fact
// handler only pokes the channel, and the goroutine runs the query and writes.
//
// See docs/plans/2026-08-03-ext-a3-doc-store.md.

// docSubscription is one caller watching one query.
// It deliberately holds no declaration: the delivery loop re-reads it, because
// a captured one goes stale exactly when it matters — when the collection is
// undefined or redeclared out from under the query.
type docSubscription struct {
	id     string
	query  docstore.Query
	target string
	// wake holds at most one pending nudge. A full channel means "already told,
	// not yet served", and dropping the extra is correct because the delivery it
	// would have caused is the same delivery the pending one will cause.
	wake chan struct{}
	// siblings is how many subscriptions the last write woke on this collection,
	// stamped by the fan-out because that is the one place already counting them.
	// The delivery goroutine reads it to price the write, and reading it from
	// here rather than counting again keeps a log line that almost never fires
	// off the delivery path's lock.
	siblings atomic.Int64
}

// documentChanged is the fact's payload. It carries the address in parts so a
// consumer never parses the subject, and `deleted` so a future consumer can tell
// a removal from a write without reading the store to find nothing there.
type documentChanged struct {
	Namespace  string `json:"namespace"`
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Deleted    bool   `json:"deleted,omitempty"`
}

// subscribeDocumentFacts registers the live-query fan-out as its own ephemeral
// bus consumer, beside the WebSocket hub's. It is deliberately not a projection:
// wireProjections maps facts to WebSocket traffic, and these deliveries go to
// IPC callers over their own connections.
func (d *Daemon) subscribeDocumentFacts() {
	if d.eventBus == nil || d.docUnsubHooks != nil {
		return
	}
	d.docUnsubHooks = d.eventBus.Subscribe(bus.Filter{FactDocumentChanged}, d.wakeDocumentSubscriptions)
}

func (d *Daemon) unsubscribeDocumentFacts() {
	if d.docUnsubHooks != nil {
		d.docUnsubHooks()
		d.docUnsubHooks = nil
	}
}

// publishDocumentChanged announces one document write or removal. The subject is
// the document's own address, so the log reads as a history of documents rather
// than of collections; subscriptions match on the collection carried in the
// payload.
func (d *Daemon) publishDocumentChanged(namespace, collection, id string, deleted bool) {
	d.publishFact(FactDocumentChanged, docstore.Address(namespace, collection, id),
		documentChanged{Namespace: namespace, Collection: collection, ID: id, Deleted: deleted})
}

// wakeDocumentSubscriptions is the fact handler. It does no I/O.
func (d *Daemon) wakeDocumentSubscriptions(ev bus.Event) {
	fact, ok := decodeFact[documentChanged](d, ev)
	if !ok {
		return
	}
	target := docstore.Target(fact.Namespace, fact.Collection)

	d.docSubsMu.Lock()
	woken := make([]*docSubscription, 0, len(d.docSubs))
	for _, sub := range d.docSubs {
		if sub.target == target {
			woken = append(woken, sub)
		}
	}
	d.docSubsMu.Unlock()

	for _, sub := range woken {
		sub.siblings.Store(int64(len(woken)))
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) addDocSubscription(q docstore.Query) *docSubscription {
	d.docSubsMu.Lock()
	defer d.docSubsMu.Unlock()
	if d.docSubs == nil {
		d.docSubs = map[string]*docSubscription{}
	}
	d.docSubsSeq++
	sub := &docSubscription{
		id:     fmt.Sprintf("docsub-%d", d.docSubsSeq),
		query:  q,
		target: q.Target(),
		wake:   make(chan struct{}, 1),
	}
	d.docSubs[sub.id] = sub
	return sub
}

func (d *Daemon) removeDocSubscription(id string) {
	d.docSubsMu.Lock()
	delete(d.docSubs, id)
	d.docSubsMu.Unlock()
}

// documentSubscriptionCount reports how many live queries are registered. Tests
// use it to prove a subscription is gone once its caller disconnects; a leaked
// subscription would keep re-running a query nobody reads.
func (d *Daemon) documentSubscriptionCount() int {
	d.docSubsMu.Lock()
	defer d.docSubsMu.Unlock()
	return len(d.docSubs)
}

// compileDocQuery resolves the query's after cursor and compiles it. The cursor
// is a document id, so the anchor has to be read here: docstore holds no
// database handle, and it needs the anchor's sort value to compare against the
// whole ordering tuple.
func (d *Daemon) compileDocQuery(q docstore.Query, schema docstore.CollectionSchema) (docstore.Compiled, error) {
	var anchor *docstore.Document
	if q.After != "" {
		doc, found, err := d.store.GetDocument(schema, q.After)
		if err != nil {
			return docstore.Compiled{}, err
		}
		if found {
			anchor = doc
		}
	}
	return q.Compile(schema, anchor)
}

// runDocQuery answers a query, returning its documents on the wire, the
// declaration they were computed against, and how long the read took. The
// caller decides what a slow one means: once for a one-shot query, per write
// for a live one.
//
// The declaration comes back from the read rather than being passed in. Reading
// it separately first is what let a redeclare land between the schema and the
// SELECT; the store now resolves it inside the same transaction, so the schema
// returned here is the one the answer actually means.
func (d *Daemon) runDocQuery(q docstore.Query) ([]protocol.StoredDocument, docstore.CollectionSchema, time.Duration, error) {
	if d.store == nil {
		return nil, docstore.CollectionSchema{}, 0, fmt.Errorf("no database")
	}
	if err := docstore.ValidateNamespace(q.Namespace); err != nil {
		return nil, docstore.CollectionSchema{}, 0, err
	}
	if err := docstore.ValidateCollection(q.Collection); err != nil {
		return nil, docstore.CollectionSchema{}, 0, err
	}
	started := time.Now()
	read, found, err := d.store.ReadQuery(q)
	if err != nil {
		return nil, docstore.CollectionSchema{}, 0, err
	}
	if !found {
		return nil, docstore.CollectionSchema{}, 0, undeclaredCollectionError(q.Namespace, q.Collection)
	}
	return storedDocumentsToProtocol(read.Documents), read.Schema, time.Since(started), nil
}

// Two tripwires, because a document query has two costs and only one of them is
// visible in a single query's duration.
//
// slowDocQuery is the coarse one: a one-shot query that takes this long is
// pathological now that every declared field is indexed, and the log names the
// collection size so the diagnosis starts with the receipt.
//
// slowDocFanOut is the one that matters. A live query re-runs on every committed
// write to its collection, so the daemon's real cost is one query multiplied by
// the number of subscriptions watching — a term no per-query threshold can see,
// because each of those queries is individually fast. The budget is one 60Hz
// frame: attn renders GPU terminals beside agents that run all day, so a single
// document write quietly costing a frame's worth of CPU in background query work
// is a defect worth a line in the log. A healthy fan-out is microseconds, which
// makes this a tripwire rather than a threshold — only something broken, or
// something that has outgrown this design, ever feels it.
const (
	slowDocQuery  = 50 * time.Millisecond
	slowDocFanOut = 16 * time.Millisecond
)

func (d *Daemon) logSlowDocQuery(schema docstore.CollectionSchema, took time.Duration) {
	if took < slowDocQuery {
		return
	}
	d.logf("docstore: %s/%s query took %s over %d documents (slow past %s)",
		schema.Namespace, schema.Collection, took.Round(time.Millisecond),
		d.documentCountFor(schema), slowDocQuery)
}

// logSlowDocFanOut reports what one write to this collection costs across every
// subscription watching it, which is the number that grows as extensions arrive.
func (d *Daemon) logSlowDocFanOut(sub *docSubscription, schema docstore.CollectionSchema, took time.Duration) {
	// The first delivery answers the subscribe rather than a write, so it has
	// woken nobody and prices as one.
	subs := sub.siblings.Load()
	if subs < 1 {
		subs = 1
	}
	perWrite := took * time.Duration(subs)
	if perWrite < slowDocFanOut {
		return
	}
	d.logf("docstore: %s/%s live query took %s over %d documents; %d subscription(s) make one write to this collection cost about %s (slow past %s)",
		schema.Namespace, schema.Collection, took.Round(time.Millisecond),
		d.documentCountFor(schema), subs, perWrite.Round(time.Millisecond), slowDocFanOut)
}

func (d *Daemon) documentCountFor(schema docstore.CollectionSchema) int {
	count, err := d.store.CountDocuments(schema)
	if err != nil {
		return -1
	}
	return count
}

// collectionFor reads a collection's declaration, turning "never declared" into
// the error every caller wants to report.
func (d *Daemon) collectionFor(namespace, collection string) (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, fmt.Errorf("no database")
	}
	if err := docstore.ValidateNamespace(namespace); err != nil {
		return nil, err
	}
	if err := docstore.ValidateCollection(collection); err != nil {
		return nil, err
	}
	schema, ok, err := d.store.DocumentCollection(namespace, collection)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, undeclaredCollectionError(namespace, collection)
	}
	return schema, nil
}

// undeclaredCollectionError is what every caller reports for a collection that
// was never declared, whether it read the declaration itself or had a query come
// back saying there was none.
func undeclaredCollectionError(namespace, collection string) error {
	return fmt.Errorf("docstore: %s/%s is not declared; declare it with `attn doc define` before reading or writing it",
		namespace, collection)
}

// ---------------------------------------------------------------------------
// IPC handlers
// ---------------------------------------------------------------------------

func (d *Daemon) handleDocDefine(conn net.Conn, msg *protocol.DocDefineMessage) {
	schema := collectionSchemaFromProtocol(msg.Schema)
	if err := schema.Validate(); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	if err := d.store.DefineDocumentCollection(schema, time.Now()); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok:              true,
		DocDefineResult: &protocol.DocDefineResult{Namespace: schema.Namespace, Collection: schema.Collection},
	})
}

func (d *Daemon) handleDocUndefine(conn net.Conn, msg *protocol.DocUndefineMessage) {
	if _, err := d.collectionFor(msg.Namespace, msg.Collection); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	removed, err := d.store.DeleteDocumentCollection(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	// Every removed document is a change its watchers must see; without this a
	// live query would keep showing records the store no longer holds.
	d.coalesceSnapshots(func() {
		d.publishDocumentChanged(msg.Namespace, msg.Collection, "", true)
	})
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		DocUndefineResult: &protocol.DocUndefineResult{
			Namespace: msg.Namespace, Collection: msg.Collection, DocumentsRemoved: removed,
		},
	})
}

func (d *Daemon) handleDocCollections(conn net.Conn, _ *protocol.DocCollectionsMessage) {
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	schemas, err := d.store.ListDocumentCollections()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	out := make([]protocol.DocumentCollectionSchema, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, collectionSchemaToProtocol(s))
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok:                   true,
		DocCollectionsResult: &protocol.DocCollectionsResult{Collections: out},
	})
}

func (d *Daemon) handleDocPut(conn net.Conn, msg *protocol.DocPutMessage) {
	schema, err := d.collectionFor(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := docstore.ValidateDocumentID(msg.ID); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := docstore.ValidateBody([]byte(msg.Body)); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	// A refused write publishes nothing: the store did not change, and waking
	// every live query on the collection to re-render an identical result set is
	// exactly the cost the conditional write exists to avoid.
	rev, err := d.store.PutDocument(*schema, msg.ID, []byte(msg.Body), time.Now(), expectedRev(msg.ExpectedRev))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.publishDocumentChanged(msg.Namespace, msg.Collection, msg.ID, false)
	d.sendDocResponse(conn, protocol.Response{
		Ok:           true,
		DocPutResult: &protocol.DocPutResult{Namespace: msg.Namespace, Collection: msg.Collection, ID: msg.ID, Rev: int(rev)},
	})
}

// expectedRev converts the wire's optional revision to the store's. The wire
// carries safeint, which generates as int; the store keeps int64 so the SQL side
// is the same width on every platform.
func expectedRev(wire *int) *int64 {
	if wire == nil {
		return nil
	}
	rev := int64(*wire)
	return &rev
}

func (d *Daemon) handleDocGet(conn net.Conn, msg *protocol.DocGetMessage) {
	schema, err := d.collectionFor(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	doc, found, err := d.store.GetDocument(*schema, msg.ID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result := &protocol.DocGetResult{Found: found}
	if found {
		wire := storedDocumentToProtocol(*doc)
		result.Document = &wire
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocGetResult: result})
}

func (d *Daemon) handleDocDelete(conn net.Conn, msg *protocol.DocDeleteMessage) {
	schema, err := d.collectionFor(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	existed, err := d.store.DeleteDocument(*schema, msg.ID, expectedRev(msg.ExpectedRev))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	// A delete that removed nothing changed nothing, and must not wake a
	// subscription with a result set identical to the one it already has.
	if existed {
		d.publishDocumentChanged(msg.Namespace, msg.Collection, msg.ID, true)
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		DocDeleteResult: &protocol.DocDeleteResult{
			Namespace: msg.Namespace, Collection: msg.Collection, ID: msg.ID, Existed: existed,
		},
	})
}

func (d *Daemon) handleDocQuery(conn net.Conn, msg *protocol.DocQueryMessage) {
	q, err := documentQueryFromProtocol(msg.Query)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	docs, schema, took, err := d.runDocQuery(q)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.logSlowDocQuery(schema, took)
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocQueryResult: &protocol.DocQueryResult{Documents: docs}})
}

// handleDocSubscribe is the only handler that keeps its connection. It writes
// the current result set immediately — a subscriber must be able to render from
// one round trip, which is what makes an extension UI's remount cheap — and then
// a fresh one every time a write to the collection could have changed it. It
// ends when the caller disconnects, or when the collection stops being able to
// answer the query at all.
func (d *Daemon) handleDocSubscribe(conn net.Conn, msg *protocol.DocSubscribeMessage) {
	q, err := documentQueryFromProtocol(msg.Query)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	schema, err := d.collectionFor(q.Namespace, q.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	// Compile once up front. A query that cannot compile must fail the subscribe
	// rather than fail on every delivery of a subscription that looked accepted.
	if _, err := d.compileDocQuery(q, *schema); err != nil {
		d.sendError(conn, err.Error())
		return
	}

	// Registered before the first query runs. The other order drops a write that
	// lands in between; this order can only cause one redundant delivery, and a
	// delivery is the whole result set, so a redundant one costs nothing.
	sub := d.addDocSubscription(q)
	defer d.removeDocSubscription(sub.id)

	// The caller never speaks again after subscribing, so anything the read
	// returns — EOF, reset, the daemon closing the listener — means it is gone.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	encoder := json.NewEncoder(conn)
	for delivery := 1; ; delivery++ {
		// Every delivery re-reads the declaration rather than capturing it,
		// because it can go out from under a subscription in two ways and both
		// have to end the subscription rather than mislead it. Undefining drops
		// the collection's table, and an empty result set would tell a watcher
		// the collection is still there holding nothing; redeclaring without a
		// field this query uses drops that field's column, and the query stops
		// meaning what the caller asked. Either way the caller is told which, and
		// the subscription ends instead of hanging on a collection it can never
		// serve again. The re-read happens inside the query's own transaction, so
		// a redeclare cannot land between reading the declaration and running the
		// statement compiled from it.
		docs, live, took, err := d.runDocQuery(q)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		d.logSlowDocFanOut(sub, live, took)
		resp := protocol.Response{
			Ok:                 true,
			DocSubscribeResult: &protocol.DocSubscribeResult{Delivery: delivery, Documents: docs},
		}
		if err := encoder.Encode(resp); err != nil {
			return
		}
		select {
		case <-sub.wake:
		case <-gone:
			return
		}
	}
}

func (d *Daemon) sendDocResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("docstore: writing response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Wire mapping
// ---------------------------------------------------------------------------

func collectionSchemaFromProtocol(s protocol.DocumentCollectionSchema) docstore.CollectionSchema {
	out := docstore.CollectionSchema{Namespace: s.Namespace, Collection: s.Collection}
	for _, f := range s.Fields {
		out.Fields = append(out.Fields, docstore.FieldSpec{Name: f.Name, Type: docstore.FieldType(f.Type)})
	}
	return out
}

func collectionSchemaToProtocol(s docstore.CollectionSchema) protocol.DocumentCollectionSchema {
	out := protocol.DocumentCollectionSchema{
		Namespace:  s.Namespace,
		Collection: s.Collection,
		Fields:     make([]protocol.DocumentFieldSpec, 0, len(s.Fields)),
	}
	for _, f := range s.Fields {
		out.Fields = append(out.Fields, protocol.DocumentFieldSpec{Name: f.Name, Type: string(f.Type)})
	}
	return out
}

// documentQueryFromProtocol decodes a wire query. A filter's bound arrives as
// JSON text because it is the one polymorphic leaf; decoding it here is what
// lets docstore check it against the field's declared type.
func documentQueryFromProtocol(q protocol.DocumentQuery) (docstore.Query, error) {
	out := docstore.Query{Namespace: q.Namespace, Collection: q.Collection}
	for _, f := range q.Filters {
		var value any
		if err := json.Unmarshal([]byte(f.ValueJson), &value); err != nil {
			return docstore.Query{}, fmt.Errorf("docstore: filter on %q carries %q, which is not JSON: %w", f.Field, f.ValueJson, err)
		}
		out.Filters = append(out.Filters, docstore.Filter{Field: f.Field, Op: docstore.Op(f.Op), Value: value})
	}
	if q.Sort != nil {
		sort := docstore.Sort{Field: q.Sort.Field}
		if q.Sort.Desc != nil {
			sort.Desc = *q.Sort.Desc
		}
		out.Sort = &sort
	}
	if q.Limit != nil {
		out.Limit = *q.Limit
	}
	if q.After != nil {
		out.After = *q.After
	}
	return out, nil
}

func storedDocumentToProtocol(doc docstore.Document) protocol.StoredDocument {
	return protocol.StoredDocument{
		ID:        doc.ID,
		Body:      string(doc.Body),
		Rev:       int(doc.Rev),
		CreatedAt: doc.CreatedAt.UTC().Format(docstore.TimeFormat),
		UpdatedAt: doc.UpdatedAt.UTC().Format(docstore.TimeFormat),
	}
}

func storedDocumentsToProtocol(docs []docstore.Document) []protocol.StoredDocument {
	out := make([]protocol.StoredDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, storedDocumentToProtocol(doc))
	}
	return out
}
