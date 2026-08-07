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

// The document store's daemon half: the fact a write publishes, the live queries
// that fact wakes, and the IPC surface `attn doc` speaks.
//
// WHY A SUBSCRIPTION RE-RUNS RATHER THAN PATCHES. Every delivery re-runs the
// whole query. A patch stream would avoid the re-run and it is where live query
// systems grow their bugs: a document falling out of a `limit 20` window forces
// a re-query to find the 21st anyway, so a patch path needs the re-run path
// underneath it and only some of the time. Re-running always is one path.
//
// What a delivery carries is a different question from how it is computed. The
// re-run answers "which documents, in what order"; the bodies are then diffed
// against what this subscriber is known to hold, so a window of a hundred
// documents whose first one changed is one body and a hundred ids. That diff is
// pure bookkeeping over one map — no second notion of what changed, and nothing
// a patch stream's ordering hazards can reach.
//
// A delivery is still safe to drop, which is what the one-slot wake channel
// below relies on: each delivery is computed from current state at the moment it
// is sent, so a subscriber that has not drained loses nothing when a burst of
// writes collapses into one delivery. What it must NOT lose is the record of
// what it holds — that lives beside the connection, advances only when a
// delivery is actually encoded, and is what makes dropping the rest correct.
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

// documentCollectionRemoved is the undefine fact's payload: the collection that
// went, in parts, and how many documents went with it.
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
// bus consumer, beside the WebSocket hub's. It is deliberately not a projection:
// wireProjections maps facts to WebSocket traffic, and these deliveries go to
// IPC callers over their own connections.
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

// documentChangedFact builds the fact for one document write or removal. The
// subject is the document's own address, so the log reads as a history of
// documents rather than of collections; subscriptions match on the collection
// carried in the payload.
//
// It is built here and appended by the store, inside the write's own
// transaction: the store must not know what attn's facts are called, and the
// write must not be able to land without one.
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

// announceCommittedWrite fans out a fact the store already made durable.
//
// The bus reads the log forward rather than taking the event from here, so two
// writes that commit concurrently reach subscribers in seq order however their
// announce calls race. Without a bus — a Daemon assembled in a test — the
// matching path runs the projections directly, exactly as publishFact's
// bus-less path does, so a test exercises the same write with only the fan-out
// missing.
func (d *Daemon) announceCommittedWrite(fact store.BusEvent, seq int64) {
	if d.eventBus == nil {
		d.projectToClients(bus.Event{
			Seq: seq, Name: fact.Name, Subject: fact.Subject, Payload: json.RawMessage(fact.Payload),
		})
		return
	}
	d.eventBus.Announce()
}

// publishCollectionRemoved announces that a collection and everything in it is
// gone. Its subject is the collection rather than a document, so it goes through
// the ordinary publish path: there is no document write to commit it with.
func (d *Daemon) publishCollectionRemoved(namespace, collection string, documents int) {
	d.publishFact(FactDocumentCollectionRemoved, docstore.Target(namespace, collection),
		documentCollectionRemoved{Namespace: namespace, Collection: collection, Documents: documents})
}

// publishCollectionRedeclared announces that a collection's declaration was
// rewritten. The delivery loop re-reads the declaration on every wake, so this
// is all a redeclare needs to publish: a subscription whose queried fields
// survived pays one redundant delivery, and one whose field went gets its
// collection_redeclared ending now instead of at the next arbitrary write.
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

// documentSubscriptionCount reports how many live queries are registered. Tests
// use it to prove a subscription is gone once its caller disconnects; a leaked
// subscription would keep re-running a query nobody reads.
func (d *Daemon) documentSubscriptionCount() int {
	d.docSubsMu.Lock()
	defer d.docSubsMu.Unlock()
	return len(d.docSubs)
}

// runDocQuery answers a query, returning the store's read — documents, the
// declaration they were computed against, and the log position they were true
// at — plus how long it took. The caller decides what a slow one means: once
// for a one-shot query, per write for a live one.
//
// The declaration comes back from the read rather than being passed in. Reading
// it separately first is what let a redeclare land between the schema and the
// SELECT; the store now resolves it inside the same transaction, so the schema
// returned here is the one the answer actually means.
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
// back saying there was none. It is typed so sendDocError can put a code beside
// the text without matching on the text.
func undeclaredCollectionError(namespace, collection string) error {
	return &docstore.UndeclaredCollectionError{Namespace: namespace, Collection: collection}
}

// sendDocError is the document store's one error path. It turns the typed
// errors docstore raises into the machine-readable code beside the message —
// which is the whole point of them being types: an SDK retry loop must be able
// to tell "conflict, read it again" from "broken, stop", and a UI host must be
// able to tell "this collection is gone, kill the tile" from "that query was
// wrong". Neither may do it by matching English, including here.
//
// The message text is unchanged and stays the part a human or an agent reads.
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

// subscriptionEndCode is the code the SAME errors carry once a subscription has
// been accepted, where they mean something different. Answering a request,
// "this collection is not declared" and "this query is wrong" are both things
// the caller got wrong. Ending an accepted subscription they are things that
// happened TO it — the collection was removed, or redeclared without a field
// the query uses — and a UI host has to tell those apart to know whether to
// kill the tile or rewrite its query.
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

// ---------------------------------------------------------------------------
// IPC handlers
// ---------------------------------------------------------------------------

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
	// A first declaration has no watchers — nothing could subscribe to a
	// collection that did not exist. A redeclare can, and they must hear about
	// it now: a live query parked on its wake channel would otherwise hold a
	// stale window until an unrelated write happened to land, or forever.
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
	// Every removed document is a change its watchers must see; without this a
	// live query would keep showing records the store no longer holds.
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
	// The write and its fact are one commit, so the seq that comes back is the
	// write's position: a caller can compare it against the `as_of_seq` of any
	// later read and know whether that read includes this write.
	//
	// A refused write publishes nothing: the store did not change, and waking
	// every live query on the collection to re-render an identical result set is
	// exactly the cost the conditional write exists to avoid.
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
	// One transaction for the declaration, the row and the log position, so the
	// position names the state the document was read from rather than whatever
	// the log reached by the time a second read got there.
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
	// A delete that removed nothing changed nothing: the store appends no fact
	// for it, so it must not wake a subscription with a result set identical to
	// the one it already has, and it has no position to report.
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

// handleDocCount answers how many documents match, using the same compile the
// query itself uses. A caller that only wants the number — a badge, a "showing
// 20 of N" — must not have to fetch bodies to count them, and cannot count
// past the limit if it does.
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

// handleDocSubscribe is the only handler that keeps its connection. It writes
// the query's current window immediately — a subscriber must be able to render
// from one round trip, which is what makes an extension UI's remount cheap — and
// then a fresh one every time a write to the collection could have changed it.
// It ends when the caller disconnects, or when the collection stops being able
// to answer the query at all.
//
// A delivery is a window, not a result set: the ids in order, plus only the
// bodies the subscriber does not already hold. What it holds comes from `have`
// on the way in and from what has been delivered since, tracked below as one
// {id: rev} map. See DocSubscribeResult on the wire for the client's one rule.
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

	// Registered before the first query runs. The other order drops a write that
	// lands in between; this order can only cause one redundant delivery, which
	// costs the subscriber an ids-only message.
	sub := d.addDocSubscription(q)
	defer d.removeDocSubscription(sub.id)

	// The caller never speaks again after subscribing, so anything the read
	// returns — EOF, reset, the daemon closing the listener — means it is gone.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	// What the subscriber holds. Seeded from `have`, then replaced by each
	// delivered window: the client's forget rule means anything absent from a
	// window's order is gone from its cache too, so this map is the window and
	// never grows past the query's limit.
	held := heldRevisions(msg.Have)

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
		//
		// The first of these reads is also the acceptance check: a query that
		// cannot be answered fails the subscribe, in the same call that would
		// have served it. There is no separate compile up front, because a second
		// compile path is a second place for the rules to be applied differently.
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

// heldRevisions turns the subscriber's declared `have` into the map the
// delivery loop diffs against. A resume is exact rather than approximate: an id
// claiming a revision the store never issued simply differs from the one it
// finds, so that body is sent and the subscriber converges on the next delivery
// however stale its claim was.
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

// windowDelivery turns a read into one delivery and the revisions the subscriber
// holds once it applies it. A body travels only when the subscriber's revision
// for that id differs from the stored one; everything else is carried by order,
// which the client renders from its cache.
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
