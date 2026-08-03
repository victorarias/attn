package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
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
type docSubscription struct {
	id     string
	query  docstore.Query
	schema docstore.CollectionSchema
	target string
	// wake holds at most one pending nudge. A full channel means "already told,
	// not yet served", and dropping the extra is correct because the delivery it
	// would have caused is the same delivery the pending one will cause.
	wake chan struct{}
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
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) addDocSubscription(q docstore.Query, schema docstore.CollectionSchema) *docSubscription {
	d.docSubsMu.Lock()
	defer d.docSubsMu.Unlock()
	if d.docSubs == nil {
		d.docSubs = map[string]*docSubscription{}
	}
	d.docSubsSeq++
	sub := &docSubscription{
		id:     fmt.Sprintf("docsub-%d", d.docSubsSeq),
		query:  q,
		schema: schema,
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
		doc, found, err := d.store.GetDocument(q.Namespace, q.Collection, q.After)
		if err != nil {
			return docstore.Compiled{}, err
		}
		if found {
			anchor = doc
		}
	}
	return q.Compile(schema, anchor)
}

// runDocQuery compiles and runs a query, returning its documents on the wire.
func (d *Daemon) runDocQuery(q docstore.Query, schema docstore.CollectionSchema) ([]protocol.StoredDocument, error) {
	if d.store == nil {
		return nil, fmt.Errorf("no database")
	}
	compiled, err := d.compileDocQuery(q, schema)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	docs, err := d.store.QueryDocuments(compiled)
	if err != nil {
		return nil, err
	}
	d.logSlowDocQuery(q, time.Since(started))
	return storedDocumentsToProtocol(docs), nil
}

// slowDocQuery is the point past which a query's duration is worth a line in the
// log. v1 answers queries by scanning a collection, deliberately, because
// nothing has measured a need for secondary indexes. This is the tripwire that
// produces that measurement: it names the collection and how many documents were
// scanned, so the case for an index arrives with its receipt instead of a hunch.
const slowDocQuery = 50 * time.Millisecond

func (d *Daemon) logSlowDocQuery(q docstore.Query, took time.Duration) {
	if took < slowDocQuery {
		return
	}
	count, err := d.store.CountDocuments(q.Namespace, q.Collection)
	if err != nil {
		count = -1
	}
	d.logf("docstore: %s/%s query took %s over %d documents (slow past %s) — a collection this size may want an index",
		q.Namespace, q.Collection, took.Round(time.Millisecond), count, slowDocQuery)
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
		return nil, fmt.Errorf("docstore: %s/%s is not declared; declare it with `attn doc define` before reading or writing it",
			namespace, collection)
	}
	return schema, nil
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
	if _, err := d.collectionFor(msg.Namespace, msg.Collection); err != nil {
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
	if err := d.store.PutDocument(msg.Namespace, msg.Collection, msg.ID, []byte(msg.Body), time.Now()); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.publishDocumentChanged(msg.Namespace, msg.Collection, msg.ID, false)
	d.sendDocResponse(conn, protocol.Response{
		Ok:           true,
		DocPutResult: &protocol.DocPutResult{Namespace: msg.Namespace, Collection: msg.Collection, ID: msg.ID},
	})
}

func (d *Daemon) handleDocGet(conn net.Conn, msg *protocol.DocGetMessage) {
	if _, err := d.collectionFor(msg.Namespace, msg.Collection); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	doc, found, err := d.store.GetDocument(msg.Namespace, msg.Collection, msg.ID)
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
	if _, err := d.collectionFor(msg.Namespace, msg.Collection); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	existed, err := d.store.DeleteDocument(msg.Namespace, msg.Collection, msg.ID)
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
	schema, err := d.collectionFor(q.Namespace, q.Collection)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	docs, err := d.runDocQuery(q, *schema)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocQueryResult: &protocol.DocQueryResult{Documents: docs}})
}

// handleDocSubscribe is the only handler that keeps its connection. It writes
// the current result set immediately — a subscriber must be able to render from
// one round trip, which is what makes an extension UI's remount cheap — and then
// a fresh one every time a write to the collection could have changed it.
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
	sub := d.addDocSubscription(q, *schema)
	defer d.removeDocSubscription(sub.id)

	// The caller never speaks again after subscribing, so anything the read
	// returns — EOF, reset, the daemon closing the listener — means it is gone.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	encoder := json.NewEncoder(conn)
	for revision := 1; ; revision++ {
		docs, err := d.runDocQuery(q, *schema)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		resp := protocol.Response{
			Ok:                 true,
			DocSubscribeResult: &protocol.DocSubscribeResult{Revision: revision, Documents: docs},
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
