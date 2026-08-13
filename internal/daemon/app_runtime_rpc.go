package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/store"
)

// The sidecar's side of the socket: how the app runtime connects, what it may
// ask the daemon for, and how a dispatch travels.
//
// It speaks the same newline-delimited JSON-RPC as a plugin (jsonrpc_peer.go
// holds the transport both share) but it is not a plugin and does not enter the
// plugin registry: a plugin is an extension the user installed, and this is one
// process attn ships and owns.
//
// The methods the sidecar may call are exactly the collection operations, and
// each one is scoped by the daemon's own record of the dispatch in flight — see
// appDispatch. There is no namespace on the wire to get wrong.

const appRuntimeHelloMethod = "app_runtime.hello"

type appRuntimeHelloParams struct {
	Generation uint64 `json:"generation"`
	APIVersion int    `json:"api_version"`
	PID        int    `json:"pid"`
}

type appRuntimeHelloResult struct {
	OK bool `json:"ok"`
}

// appRuntimeConnection is the live sidecar.
type appRuntimeConnection struct {
	*jsonrpcPeer

	generation  uint64
	pid         int
	connectedAt time.Time
}

func (c *appRuntimeConnection) dispatch(ctx context.Context, req appDispatchRequest) (appDispatchResult, error) {
	var result appDispatchResult
	if err := c.request(ctx, "app runtime", "app.dispatch", req, &result); err != nil {
		return appDispatchResult{}, err
	}
	return result, nil
}

// appRuntimePingResult answers whether the sidecar's event loop is turning. The
// host serves it without touching app code, so a silent ping means the loop
// itself is blocked and no app's handler is being run.
type appRuntimePingResult struct {
	OK bool `json:"ok"`
}

func (c *appRuntimeConnection) ping(ctx context.Context) error {
	var result appRuntimePingResult
	if err := c.request(ctx, "app runtime", "app.runtime.ping", struct{}{}, &result); err != nil {
		return err
	}
	if !result.OK {
		return errors.New("the app runtime answered a ping without ok")
	}
	return nil
}

// parseAppRuntimeHello recognizes the sidecar's opening frame.
//
// It runs before the plugin hello sniff, because the plugin parser treats any
// JSON-RPC frame as a plugin's and would refuse this one with "first plugin
// method must be hello" — a true sentence about the wrong protocol.
func parseAppRuntimeHello(data []byte) (json.RawMessage, appRuntimeHelloParams, bool, error) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, appRuntimeHelloParams{}, false, nil
	}
	if msg.Method != appRuntimeHelloMethod {
		return nil, appRuntimeHelloParams{}, false, nil
	}
	if msg.JSONRPC != "2.0" {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New(`jsonrpc must be "2.0"`)
	}
	if jsonRPCIDKey(msg.ID) == "" {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New(appRuntimeHelloMethod + " requires an id")
	}
	var params appRuntimeHelloParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return msg.ID, appRuntimeHelloParams{}, true, fmt.Errorf("decode %s params: %w", appRuntimeHelloMethod, err)
	}
	if params.Generation == 0 {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New("params.generation is required; the supervisor fences stale runtimes by generation")
	}
	if params.APIVersion != appRuntimeAPIVersion {
		return msg.ID, appRuntimeHelloParams{}, true, fmt.Errorf(
			"app runtime api version %d, but this daemon speaks %d; the runtime binary and the daemon ship together, so this is a stale install rather than a configuration to change",
			params.APIVersion, appRuntimeAPIVersion)
	}
	return msg.ID, params, true, nil
}

// handleAppRuntimeConnection owns the sidecar's connection for its whole life.
func (d *Daemon) handleAppRuntimeConnection(conn net.Conn, reader *bufio.Reader, helloID json.RawMessage, params appRuntimeHelloParams) {
	runtime := &appRuntimeConnection{
		jsonrpcPeer: newJSONRPCPeer(conn, reader),
		generation:  params.Generation,
		pid:         params.PID,
		connectedAt: d.appNow(),
	}
	if !d.ensureAppRuntimeSupervisor().NoteConnected(appRuntimeChildName, runtime.generation) {
		_ = runtime.send(jsonRPCFailure(helloID, jsonRPCInvalidRequest,
			"this app runtime's generation is no longer current; a newer one has already been started"))
		return
	}
	d.setAppRuntimeConnection(runtime)
	defer func() {
		// Same ordering as the plugin path: note the disconnect before the
		// connection stops being reachable, so a replacement's NoteConnected
		// cancels the grace timer instead of an old defer arming it after the new
		// process is already healthy.
		d.ensureAppRuntimeSupervisor().NoteDisconnected(appRuntimeChildName, runtime.generation)
		d.clearAppRuntimeConnection(runtime)
		// Every dispatch waiting on this process learns now. Without it each one
		// would wait out its whole timeout for an answer that can never come.
		runtime.closePending(io.EOF)
	}()

	if err := runtime.send(jsonRPCResult(helloID, appRuntimeHelloResult{OK: true})); err != nil {
		return
	}
	d.logf("app runtime connected (generation %d, pid %d)", runtime.generation, runtime.pid)
	d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)

	for {
		data, err := readSocketFrame(reader)
		if err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = runtime.send(jsonRPCFailure(nil, jsonRPCParseError, "parse JSON-RPC message"))
			continue
		}
		if msg.JSONRPC != "2.0" {
			_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, `jsonrpc must be "2.0"`))
			continue
		}
		if msg.Method == "" {
			if !runtime.routeResponse(msg) {
				// The caller's context expired first and it has already given up.
				// Saying so on the wire would be noise the host cannot act on.
				d.logf("app runtime: answer to request %s arrived with nobody waiting", jsonRPCIDKey(msg.ID))
			}
			continue
		}
		if msg.Method == appRuntimeEnteredMethod || msg.Method == appRuntimeLeftMethod {
			// On the read loop on purpose: it is a map write, and its whole value is
			// the order it arrives in.
			d.appRuntimeHandlerMoved(runtime, msg)
			continue
		}
		// Off the read loop: several apps dispatch concurrently over this one
		// socket, and a collection read for one must not hold up another app's
		// answers behind it.
		go d.serveAppRuntimeMethod(runtime, msg)
	}
}

func (d *Daemon) setAppRuntimeConnection(runtime *appRuntimeConnection) {
	d.appRuntimeMu.Lock()
	d.appRuntimeConn = runtime
	// Wakes every delivery parked on the cold start.
	if d.appRuntimeReady != nil {
		close(d.appRuntimeReady)
		d.appRuntimeReady = nil
	}
	d.appRuntimeMu.Unlock()
}

// clearAppRuntimeConnection drops the connection only if it is still the current
// one. A slow teardown must not unpublish a replacement that has already
// connected.
func (d *Daemon) clearAppRuntimeConnection(runtime *appRuntimeConnection) {
	d.appRuntimeMu.Lock()
	if d.appRuntimeConn == runtime {
		d.appRuntimeConn = nil
	}
	d.appRuntimeMu.Unlock()
	// Whatever the daemon gave up waiting for died with the process, so nothing
	// of it is still holding an event loop that no longer exists.
	d.forgetEnteredHandlers()
	d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)
}

func (d *Daemon) serveAppRuntimeMethod(runtime *appRuntimeConnection, msg jsonRPCMessage) {
	if jsonRPCIDKey(msg.ID) == "" {
		_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "app runtime method calls require an id"))
		return
	}
	result, err := d.appRuntimeMethod(msg)
	if err != nil {
		_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	_ = runtime.send(jsonRPCResult(msg.ID, result))
}

// appRuntimeCrashedMethod is the host's last frame before it exits: an error
// escaped every handler and is about to take the process down, and this says
// whose code it came from.
//
// It is a report, not a request for permission — the host exits either way, and
// waits only briefly for the answer. Losing it costs the culprit a strike, which
// is why the host sends it before exiting rather than letting the daemon guess
// from a dead socket.
const appRuntimeCrashedMethod = "app_runtime.crashed"

type appRuntimeCrashParams struct {
	// App is empty when the error carried no stack naming a loaded bundle. Nothing
	// is charged then: guessing which app was running is how innocents get
	// disabled.
	App   string `json:"app"`
	Kind  string `json:"kind"`
	Error string `json:"error"`
}

func (d *Daemon) appRuntimeCrashed(msg jsonRPCMessage) (any, error) {
	var params appRuntimeCrashParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", appRuntimeCrashedMethod, err)
	}
	if params.App == "" {
		d.logf("app runtime: crashing on an unhandled %s that names no app: %s",
			params.Kind, firstLine(params.Error))
		return appRuntimeHelloResult{OK: true}, nil
	}
	d.noteAppRuntimeCrash(params.App, params.Kind, params.Error)
	return appRuntimeHelloResult{OK: true}, nil
}

// appRuntimeEnteredMethod and appRuntimeLeftMethod are the host saying it is
// about to call an app's handler, and that the handler has settled. They are
// notifications: there is nothing to answer, and the point of the first is that
// it reaches the daemon *before* the handler runs, so it is already on the wire
// when a handler that never yields freezes the loop behind it.
//
// Together they are what makes a frozen loop attributable — the daemon's own
// dispatch order is not the order handlers hold the loop, and nothing the daemon
// can observe tells it when a handler left. See attributeWedgedDispatch.
const (
	appRuntimeEnteredMethod = "app_runtime.entered"
	appRuntimeLeftMethod    = "app_runtime.left"
)

type appRuntimeHandlerParams struct {
	Dispatch string `json:"dispatch"`
	App      string `json:"app"`
}

// enteredHandler is one handler the host entered and has not left. order is the
// daemon's receive order, which is the host's entry order: the frames arrive on
// one socket and are recorded on its read loop.
type enteredHandler struct {
	app   string
	order uint64
}

func (d *Daemon) appRuntimeHandlerMoved(runtime *appRuntimeConnection, msg jsonRPCMessage) {
	var params appRuntimeHandlerParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		d.logf("app runtime: decode %s params: %v", msg.Method, err)
		return
	}
	if params.Dispatch == "" || params.App == "" {
		return
	}
	if msg.Method == appRuntimeLeftMethod {
		d.forgetEnteredHandler(params.Dispatch)
		return
	}
	d.noteEnteredHandler(runtime.generation, params.Dispatch, params.App)
}

// appCollectionParams is what every collection callback carries. There is
// deliberately no namespace: the daemon reads it off the dispatch record, so an
// app cannot name one — its own or anybody else's.
type appCollectionParams struct {
	Dispatch   string          `json:"dispatch"`
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Body       json.RawMessage `json:"body"`
	IfRev      *int64          `json:"if_rev"`
	Query      *docstore.Query `json:"query"`
}

func (d *Daemon) appRuntimeMethod(msg jsonRPCMessage) (any, error) {
	if msg.Method == appRuntimeCrashedMethod {
		return d.appRuntimeCrashed(msg)
	}

	switch msg.Method {
	case "app.collection.get", "app.collection.put", "app.collection.delete",
		"app.collection.query", "app.collection.count":
	default:
		return nil, fmt.Errorf("unknown method %q", msg.Method)
	}

	var params appCollectionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", msg.Method, err)
	}
	dispatch, err := d.lookupAppDispatch(params.Dispatch)
	if err != nil {
		return nil, err
	}
	if _, declared := dispatch.collections[params.Collection]; !declared {
		return nil, fmt.Errorf(
			"app %s did not declare a collection named %q; ctx.collections only carries what attn-app.toml declares, and adding a [[collections]] block plus `attn app apply` is what creates one",
			dispatch.app, params.Collection)
	}

	switch msg.Method {
	case "app.collection.get":
		return d.appCollectionGet(dispatch, params)
	case "app.collection.put":
		return d.appCollectionPut(dispatch, params)
	case "app.collection.delete":
		return d.appCollectionDelete(dispatch, params)
	case "app.collection.query":
		return d.appCollectionQuery(dispatch, params)
	default:
		return d.appCollectionCount(dispatch, params)
	}
}

// appDocument is one document as the SDK's Document type sees it.
type appDocument struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int64           `json:"rev"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func appDocumentOf(doc docstore.Document) appDocument {
	return appDocument{
		ID:        doc.ID,
		Body:      doc.Body,
		Rev:       doc.Rev,
		CreatedAt: stampForWire(doc.CreatedAt),
		UpdatedAt: stampForWire(doc.UpdatedAt),
	}
}

func (d *Daemon) appCollectionGet(dispatch *appDispatch, params appCollectionParams) (any, error) {
	read, declared, err := d.store.ReadDocument(dispatch.namespace, params.Collection, params.ID)
	if err != nil {
		return nil, err
	}
	if !declared {
		return nil, undeclaredCollectionError(dispatch.namespace, params.Collection)
	}
	if !read.Found {
		// The SDK types this as Document | null, so the absent case is a value
		// rather than a failure: "no such document" is an ordinary answer.
		return nil, nil
	}
	return appDocumentOf(*read.Document), nil
}

func (d *Daemon) appCollectionPut(dispatch *appDispatch, params appCollectionParams) (any, error) {
	schema, err := d.collectionFor(dispatch.namespace, params.Collection)
	if err != nil {
		return nil, err
	}
	if err := docstore.ValidateDocumentID(params.ID); err != nil {
		return nil, err
	}
	if err := docstore.ValidateBody(params.Body); err != nil {
		return nil, err
	}
	fact := documentChangedFact(dispatch.namespace, params.Collection, params.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: params.ID, Body: params.Body, Expected: params.IfRev,
	}, fact, d.appNow())
	if err != nil {
		return nil, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	// Read back rather than synthesize: the SDK hands the caller a Document, and
	// the timestamps on it are the store's, not this call's idea of now.
	read, _, err := d.store.ReadDocument(dispatch.namespace, params.Collection, params.ID)
	if err == nil && read.Found {
		return appDocumentOf(*read.Document), nil
	}
	return appDocument{ID: params.ID, Body: params.Body, Rev: written.Rev}, nil
}

func (d *Daemon) appCollectionDelete(dispatch *appDispatch, params appCollectionParams) (any, error) {
	schema, err := d.collectionFor(dispatch.namespace, params.Collection)
	if err != nil {
		return nil, err
	}
	fact := documentChangedFact(dispatch.namespace, params.Collection, params.ID, true)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: params.ID, Delete: true, Expected: params.IfRev,
	}, fact, d.appNow())
	if err != nil {
		return nil, err
	}
	if written.Changed {
		d.announceCommittedWrite(fact, written.Seq)
	}
	return written.Changed, nil
}

// appQuery fills in the two fields the app is not allowed to choose.
func (d *Daemon) appQuery(dispatch *appDispatch, params appCollectionParams) docstore.Query {
	q := docstore.Query{}
	if params.Query != nil {
		q = *params.Query
	}
	q.Namespace = dispatch.namespace
	q.Collection = params.Collection
	return q
}

func (d *Daemon) appCollectionQuery(dispatch *appDispatch, params appCollectionParams) (any, error) {
	read, took, err := d.runDocQuery(d.appQuery(dispatch, params))
	if err != nil {
		return nil, err
	}
	d.logSlowDocQuery(read.Schema, took)
	out := make([]appDocument, 0, len(read.Documents))
	for _, doc := range read.Documents {
		out = append(out, appDocumentOf(doc))
	}
	return out, nil
}

func (d *Daemon) appCollectionCount(dispatch *appDispatch, params appCollectionParams) (any, error) {
	q := d.appQuery(dispatch, params)
	if err := docstore.ValidateNamespace(q.Namespace); err != nil {
		return nil, docstore.InvalidQuery(err)
	}
	if err := docstore.ValidateCollection(q.Collection); err != nil {
		return nil, docstore.InvalidQuery(err)
	}
	read, found, err := d.store.CountQuery(q)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, undeclaredCollectionError(q.Namespace, q.Collection)
	}
	return read.Count, nil
}
