package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The document store's daemon half: the fact a write publishes, the live
// queries that fact wakes, and the IPC surface `attn doc` speaks.
//
// A subscription re-runs the whole query per delivery — never patches — and
// diffs bodies against what the subscriber holds. A delivery is safe to drop
// (each is computed from current state), which the one-slot wake channel relies
// on; the held-revisions record beside the connection is what must not be lost.
// The bus fan-out must not write the socket — the bus holds its publish lock
// across the inline fan-out — so the fact handler only pokes the wake channel.
//
// See docs/plans/2026-08-03-ext-a3-doc-store.md.

// docSubscription is one caller watching one query. It holds no declaration on
// purpose: the delivery loop re-reads it, since a captured one goes stale
// exactly when it matters.
type docSubscription struct {
	id     string
	query  docstore.Query
	target string
	// wake holds at most one pending nudge; dropping extras is correct because
	// the delivery they would cause is the one the pending nudge will cause.
	wake chan struct{}
	// siblings is how many subscriptions the last write woke on this collection,
	// stamped by the fan-out so the slow-fan-out log line stays off the lock.
	siblings atomic.Int64
}

// documentChanged is the fact's payload: the address in parts (consumers never
// parse the subject) plus `deleted`.
type documentChanged struct {
	Namespace  string `json:"namespace"`
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Deleted    bool   `json:"deleted,omitempty"`
}

// documentCollectionRemoved is the undefine fact's payload.
type documentCollectionRemoved struct {
	Namespace  string `json:"namespace"`
	Collection string `json:"collection"`
	Documents  int    `json:"documents"`
}

type documentCollectionRedeclared struct {
	Namespace  string `json:"namespace"`
	Collection string `json:"collection"`
}

// subscribeDocumentFacts registers the live-query fan-out as its own ephemeral
// bus consumer — deliberately not a projection, since deliveries go to IPC
// callers, not WebSocket clients.
func (d *Daemon) subscribeDocumentFacts() {
	if d.eventBus == nil || d.docUnsubHooks != nil {
		return
	}
	d.docUnsubHooks = d.eventBus.Subscribe(
		bus.Filter{FactDocumentChanged, FactDocumentCollectionRemoved, FactDocumentCollectionRedeclared},
		d.wakeDocumentSubscriptions)
}

func (d *Daemon) unsubscribeDocumentFacts() {
	if d.docUnsubHooks != nil {
		d.docUnsubHooks()
		d.docUnsubHooks = nil
	}
}

// documentChangedFact builds the fact for one write or removal; subject is the
// document's address. Built here, appended by the store inside the write's own
// transaction, so the store never learns attn's fact names.
func documentChangedFact(namespace, collection, id string, deleted bool) store.BusEvent {
	payload, _ := json.Marshal(documentChanged{
		Namespace: namespace, Collection: collection, ID: id, Deleted: deleted,
	})
	return store.BusEvent{
		Name:    FactDocumentChanged,
		Subject: docstore.Address(namespace, collection, id),
		Payload: string(payload),
	}
}

// announceCommittedWrite fans out a fact the store already made durable. The bus
// reads the log forward, so concurrent commits reach subscribers in seq order
// however the announce calls race.
func (d *Daemon) announceCommittedWrite(fact store.BusEvent, seq int64) {
	if d.eventBus == nil {
		d.projectToClients(bus.Event{
			Seq: seq, Name: fact.Name, Subject: fact.Subject, Payload: json.RawMessage(fact.Payload),
		})
		return
	}
	d.eventBus.Announce()
}

// publishCollectionRemoved announces a collection and everything in it is gone;
// no document write to commit with, so it takes the ordinary publish path.
func (d *Daemon) publishCollectionRemoved(namespace, collection string, documents int) {
	d.publishFact(FactDocumentCollectionRemoved, docstore.Target(namespace, collection),
		documentCollectionRemoved{Namespace: namespace, Collection: collection, Documents: documents})
}

// publishCollectionRedeclared announces a rewritten declaration. The delivery
// loop re-reads it per wake, so a surviving subscription pays one redundant
// delivery and a broken one ends now instead of at the next arbitrary write.
func (d *Daemon) publishCollectionRedeclared(namespace, collection string) {
	d.publishFact(FactDocumentCollectionRedeclared, docstore.Target(namespace, collection),
		documentCollectionRedeclared{Namespace: namespace, Collection: collection})
}

// wakeDocumentSubscriptions is the fact handler for every document fact. It does
// no I/O.
func (d *Daemon) wakeDocumentSubscriptions(ev bus.Event) {
	var namespace, collection string
	switch ev.Name {
	case FactDocumentCollectionRemoved:
		fact, ok := decodeFact[documentCollectionRemoved](d, ev)
		if !ok {
			return
		}
		namespace, collection = fact.Namespace, fact.Collection
	case FactDocumentCollectionRedeclared:
		fact, ok := decodeFact[documentCollectionRedeclared](d, ev)
		if !ok {
			return
		}
		namespace, collection = fact.Namespace, fact.Collection
	default:
		fact, ok := decodeFact[documentChanged](d, ev)
		if !ok {
			return
		}
		namespace, collection = fact.Namespace, fact.Collection
	}
	target := docstore.Target(namespace, collection)

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

// documentSubscriptionCount reports how many live queries are registered; tests
// use it to prove a subscription dies with its caller.
func (d *Daemon) documentSubscriptionCount() int {
	d.docSubsMu.Lock()
	defer d.docSubsMu.Unlock()
	return len(d.docSubs)
}

// runDocQuery answers a query, returning the store's read plus how long it
// took. The declaration comes back FROM the read: reading it separately first
// let a redeclare land between schema and SELECT; the store now resolves it
// inside the same transaction.
func (d *Daemon) runDocQuery(q docstore.Query) (store.QueryRead, time.Duration, error) {
	if d.store == nil {
		return store.QueryRead{}, 0, fmt.Errorf("no database")
	}
	if err := docstore.ValidateNamespace(q.Namespace); err != nil {
		return store.QueryRead{}, 0, docstore.InvalidQuery(err)
	}
	if err := docstore.ValidateCollection(q.Collection); err != nil {
		return store.QueryRead{}, 0, docstore.InvalidQuery(err)
	}
	started := time.Now()
	read, found, err := d.store.ReadQuery(q)
	if err != nil {
		return store.QueryRead{}, 0, err
	}
	if !found {
		return store.QueryRead{}, 0, undeclaredCollectionError(q.Namespace, q.Collection)
	}
	return read, time.Since(started), nil
}

// Two tripwires. slowDocQuery: a one-shot query this slow is pathological with
// every declared field indexed. slowDocFanOut matters more — a live query re-runs
// per committed write, so the cost is one query times the watching subscriptions,
// invisible to any per-query threshold. Its budget is one 60Hz frame; a healthy
// fan-out is microseconds.
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

// logSlowDocFanOut reports what one write costs across every subscription
// watching this collection.
func (d *Daemon) logSlowDocFanOut(sub *docSubscription, schema docstore.CollectionSchema, took time.Duration) {
	// The first delivery answers the subscribe, not a write; prices as one.
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

// undeclaredCollectionError is the one error every caller reports for a
// never-declared collection; typed so sendDocError codes it without text matching.
func undeclaredCollectionError(namespace, collection string) error {
	return &docstore.UndeclaredCollectionError{Namespace: namespace, Collection: collection}
}

// sendDocError is the document store's one error path: typed docstore errors
// become machine-readable codes beside the message. Consumers must never match
// on the English — including here.
func (d *Daemon) sendDocError(conn net.Conn, err error) {
	d.sendDocErrorAs(conn, err, docErrorCode(err))
}

// docErrorCode is the code a docstore error answers a request with.
func docErrorCode(err error) string {
	switch {
	case docstore.IsConflict(err):
		return protocol.ErrorCodeConflict
	case docstore.IsUndeclaredCollection(err):
		return protocol.ErrorCodeUndeclaredCollection
	case docstore.IsInvalidQuery(err):
		return protocol.ErrorCodeInvalidQuery
	}
	return ""
}

// subscriptionEndCode is the code the SAME errors carry once a subscription is
// accepted: no longer "you got it wrong" but "this happened TO it" — removed vs
// redeclared — which a UI host must tell apart.
func subscriptionEndCode(err error) string {
	switch {
	case docstore.IsUndeclaredCollection(err):
		return protocol.ErrorCodeCollectionUndefined
	case docstore.IsInvalidQuery(err):
		return protocol.ErrorCodeCollectionRedeclared
	}
	return docErrorCode(err)
}

func (d *Daemon) sendDocErrorAs(conn net.Conn, err error, code string) {
	resp := protocol.Response{Ok: false, Error: protocol.Ptr(err.Error())}
	if code != "" {
		resp.ErrorCode = protocol.Ptr(code)
	}
	var conflict *docstore.ConflictError
	if errors.As(err, &conflict) {
		resp.ErrorConflict = &protocol.DocumentConflict{
			Namespace:  conflict.Namespace,
			Collection: conflict.Collection,
			ID:         conflict.ID,
			Expected:   int(conflict.Expected),
			Found:      conflict.Found,
			Actual:     int(conflict.Actual),
		}
	}
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("docstore: writing error response: %v", err)
	}
}

// IPC handlers.

func (d *Daemon) handleDocDefine(conn net.Conn, msg *protocol.DocDefineMessage) {
	schema := collectionSchemaFromProtocol(msg.Schema)
	if err := schema.Validate(); err != nil {
		d.sendDocError(conn, err)
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	// A first declaration has no watchers; a redeclare can, and they must hear
	// now or hold a stale window until an unrelated write — or forever.
	if redeclared {
		d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok:              true,
		DocDefineResult: &protocol.DocDefineResult{Namespace: schema.Namespace, Collection: schema.Collection},
	})
}

func (d *Daemon) handleDocUndefine(conn net.Conn, msg *protocol.DocUndefineMessage) {
	if _, err := d.collectionFor(msg.Namespace, msg.Collection); err != nil {
		d.sendDocError(conn, err)
		return
	}
	removed, err := d.store.DeleteDocumentCollection(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	// Watchers must see the removal or keep showing records the store no longer holds.
	d.coalesceSnapshots(func() {
		d.publishCollectionRemoved(msg.Namespace, msg.Collection, removed)
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
		d.sendDocError(conn, err)
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
		d.sendDocError(conn, err)
		return
	}
	if err := docstore.ValidateDocumentID(msg.ID); err != nil {
		d.sendDocError(conn, err)
		return
	}
	if err := docstore.ValidateBody([]byte(msg.Body)); err != nil {
		d.sendDocError(conn, err)
		return
	}
	// The write and its fact are one commit, so the returned seq is the write's
	// position (comparable against any later read's as_of_seq). A refused write
	// publishes nothing — the store did not change.
	fact := documentChangedFact(msg.Namespace, msg.Collection, msg.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: msg.ID, Body: []byte(msg.Body), Expected: expectedRev(msg.ExpectedRev),
	}, fact, time.Now())
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		DocPutResult: &protocol.DocPutResult{
			Namespace: msg.Namespace, Collection: msg.Collection, ID: msg.ID,
			Rev: int(written.Rev), Seq: int(written.Seq),
		},
	})
}

// expectedRev converts the wire's optional int revision to the store's int64.
func expectedRev(wire *int) *int64 {
	if wire == nil {
		return nil
	}
	rev := int64(*wire)
	return &rev
}

func (d *Daemon) handleDocGet(conn net.Conn, msg *protocol.DocGetMessage) {
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	if err := docstore.ValidateNamespace(msg.Namespace); err != nil {
		d.sendDocError(conn, err)
		return
	}
	if err := docstore.ValidateCollection(msg.Collection); err != nil {
		d.sendDocError(conn, err)
		return
	}
	// One transaction for declaration, row, and log position, so the position
	// names the state the document was read from.
	read, declared, err := d.store.ReadDocument(msg.Namespace, msg.Collection, msg.ID)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	if !declared {
		d.sendDocError(conn, undeclaredCollectionError(msg.Namespace, msg.Collection))
		return
	}
	result := &protocol.DocGetResult{Found: read.Found, AsOfSeq: int(read.AsOfSeq)}
	if read.Found {
		wire := storedDocumentToProtocol(*read.Document)
		result.Document = &wire
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocGetResult: result})
}

func (d *Daemon) handleDocDelete(conn net.Conn, msg *protocol.DocDeleteMessage) {
	schema, err := d.collectionFor(msg.Namespace, msg.Collection)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	// A delete that removed nothing appends no fact, wakes nobody, and has no
	// position to report.
	fact := documentChangedFact(msg.Namespace, msg.Collection, msg.ID, true)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: msg.ID, Delete: true, Expected: expectedRev(msg.ExpectedRev),
	}, fact, time.Now())
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	if written.Changed {
		d.announceCommittedWrite(fact, written.Seq)
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		DocDeleteResult: &protocol.DocDeleteResult{
			Namespace: msg.Namespace, Collection: msg.Collection, ID: msg.ID,
			Existed: written.Changed, Seq: int(written.Seq),
		},
	})
}

func (d *Daemon) handleDocQuery(conn net.Conn, msg *protocol.DocQueryMessage) {
	q, err := documentQueryFromProtocol(msg.Query)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	read, took, err := d.runDocQuery(q)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	d.logSlowDocQuery(read.Schema, took)
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocQueryResult: &protocol.DocQueryResult{
		Documents: storedDocumentsToProtocol(read.Documents),
		AsOfSeq:   int(read.AsOfSeq),
	}})
}

// handleDocCount answers how many documents match, via the same compile the
// query uses — a badge must not fetch bodies to count, and cannot count past
// the limit if it does.
func (d *Daemon) handleDocCount(conn net.Conn, msg *protocol.DocCountMessage) {
	q, err := documentQueryFromProtocol(msg.Query)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	if err := docstore.ValidateNamespace(q.Namespace); err != nil {
		d.sendDocError(conn, docstore.InvalidQuery(err))
		return
	}
	if err := docstore.ValidateCollection(q.Collection); err != nil {
		d.sendDocError(conn, docstore.InvalidQuery(err))
		return
	}
	started := time.Now()
	read, found, err := d.store.CountQuery(q)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	if !found {
		d.sendDocError(conn, undeclaredCollectionError(q.Namespace, q.Collection))
		return
	}
	d.logSlowDocQuery(read.Schema, time.Since(started))
	d.sendDocResponse(conn, protocol.Response{Ok: true, DocCountResult: &protocol.DocCountResult{
		Count: read.Count, AsOfSeq: int(read.AsOfSeq),
	}})
}

// handleDocSubscribe is the only handler that keeps its connection: the current
// window immediately, then a fresh one per relevant write, ending on disconnect
// or when the collection can no longer answer. A delivery is ids in order plus
// only the bodies the subscriber does not hold, tracked as one {id: rev} map.
func (d *Daemon) handleDocSubscribe(conn net.Conn, msg *protocol.DocSubscribeMessage) {
	q, err := documentQueryFromProtocol(msg.Query)
	if err != nil {
		d.sendDocError(conn, err)
		return
	}
	if q.After != "" {
		d.sendDocError(conn, docstore.InvalidQuery(fmt.Errorf(
			"docstore: %s/%s cannot subscribe with the after cursor %q: a live query is a window and a cursor is a walk, so the document the cursor names moves out from under the subscription. Set a limit instead and render each delivery's window; a delivery already carries only what changed.",
			q.Namespace, q.Collection, q.After)))
		return
	}

	// Registered before the first query: the other order drops a write landing
	// in between; this one only risks a redundant ids-only delivery.
	sub := d.addDocSubscription(q)
	defer d.removeDocSubscription(sub.id)

	// The caller never speaks again, so anything the read returns means gone.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	// What the subscriber holds: seeded from `have`, replaced by each delivered
	// window, never growing past the query's limit.
	held := heldRevisions(msg.Have)

	encoder := json.NewEncoder(conn)
	for delivery := 1; ; delivery++ {
		// Re-read the declaration per delivery (inside the query's transaction): an
		// undefine or a field-dropping redeclare must END the subscription, told
		// apart by code, rather than answer an empty or silently changed window.
		read, took, err := d.runDocQuery(q)
		if err != nil {
			code := docErrorCode(err)
			if delivery > 1 {
				code = subscriptionEndCode(err)
			}
			d.sendDocErrorAs(conn, err, code)
			return
		}
		d.logSlowDocFanOut(sub, read.Schema, took)
		window, next := windowDelivery(delivery, read, held)
		if err := encoder.Encode(protocol.Response{Ok: true, DocSubscribeResult: window}); err != nil {
			return
		}
		held = next
		select {
		case <-sub.wake:
		case <-gone:
			return
		}
	}
}

// heldRevisions turns the subscriber's declared `have` into the diff map. A
// stale or invented claim simply differs from what the store finds, so the body
// is re-sent and the subscriber converges.
func heldRevisions(have []protocol.DocumentRevision) map[string]int64 {
	if len(have) == 0 {
		return nil
	}
	held := make(map[string]int64, len(have))
	for _, entry := range have {
		held[entry.ID] = int64(entry.Rev)
	}
	return held
}

// windowDelivery turns a read into one delivery plus the revisions held after
// applying it; a body travels only when the subscriber's revision differs.
func windowDelivery(delivery int, read store.QueryRead, held map[string]int64) (*protocol.DocSubscribeResult, map[string]int64) {
	out := &protocol.DocSubscribeResult{
		Delivery: delivery,
		AsOfSeq:  int(read.AsOfSeq),
		Order:    make([]string, 0, len(read.Documents)),
		Upsert:   make([]protocol.StoredDocument, 0),
	}
	next := make(map[string]int64, len(read.Documents))
	for _, doc := range read.Documents {
		out.Order = append(out.Order, doc.ID)
		next[doc.ID] = doc.Rev
		if rev, ok := held[doc.ID]; !ok || rev != doc.Rev {
			out.Upsert = append(out.Upsert, storedDocumentToProtocol(doc))
		}
	}
	return out, next
}

func (d *Daemon) sendDocResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("docstore: writing response: %v", err)
	}
}

// Wire mapping.

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

// documentQueryFromProtocol decodes a wire query; a filter's bound arrives as
// JSON text (the one polymorphic leaf) and is decoded here so docstore can
// type-check it.
func documentQueryFromProtocol(q protocol.DocumentQuery) (docstore.Query, error) {
	out := docstore.Query{Namespace: q.Namespace, Collection: q.Collection}
	for _, f := range q.Filters {
		var value any
		if err := json.Unmarshal([]byte(f.ValueJson), &value); err != nil {
			return docstore.Query{}, docstore.InvalidQuery(
				fmt.Errorf("docstore: filter on %q carries %q, which is not JSON: %w", f.Field, f.ValueJson, err))
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
