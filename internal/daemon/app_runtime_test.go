package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// The app runtime as the daemon sees it: a sidecar on the other end of a socket
// that answers `app.dispatch` and calls back for documents.
//
// Every test here stands up a fake sidecar rather than the compiled Bun host.
// What is being pinned is the daemon's half — which app a failure is charged to,
// what the invocation log records, which namespace a callback resolves to — and
// running real JavaScript to prove any of that would make the test slower and
// tell it less.

// ---------------------------------------------------------------------------
// The fake sidecar
// ---------------------------------------------------------------------------

// fakeAppRuntime is a sidecar that connects over a pipe and runs a Go function
// as every handler.
type fakeAppRuntime struct {
	t    *testing.T
	conn net.Conn

	// handler runs in place of the app's code. Returning an error is a handler
	// that threw; the sidecar reports it as ok:false, which is how an app's fault
	// reaches the daemon.
	handler func(*fakeAppRuntime, appDispatchRequest) error

	// command runs in place of a command handler. Nil answers every command with
	// no payload, which is what a handler returning nothing looks like on the
	// wire. Returning an error is a handler that threw.
	command func(*fakeAppRuntime, appCommandRequest) (json.RawMessage, error)

	writeMu sync.Mutex

	mu         sync.Mutex
	dispatches []appDispatchRequest
	commands   []appCommandRequest
	pending    map[string]chan jsonRPCMessage
	nextID     int
	// loopFrozen models a blocked event loop. A real host does everything off one
	// loop, so a handler that never yields stops all of it at once: pings go
	// unanswered, and a dispatch that arrives after the freeze is never read, never
	// announced, and never run.
	loopFrozen bool
}

// freezeLoop stops this sidecar the way a synchronous handler that never yields
// stops the real one.
func (f *fakeAppRuntime) freezeLoop() {
	f.mu.Lock()
	f.loopFrozen = true
	f.mu.Unlock()
}

func (f *fakeAppRuntime) frozen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loopFrozen
}

// startFakeAppRuntime connects a sidecar to d and waits until the daemon has
// adopted it, so a dispatch that follows cannot race the connection.
func startFakeAppRuntime(t *testing.T, d *Daemon, handler func(*fakeAppRuntime, appDispatchRequest) error) *fakeAppRuntime {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	runtime := &fakeAppRuntime{
		t:       t,
		conn:    clientConn,
		handler: handler,
		pending: make(map[string]chan jsonRPCMessage),
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		d.handleConnection(serverConn)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-served:
		case <-time.After(2 * time.Second):
			t.Error("the daemon did not let go of the app runtime connection")
		}
	})

	reader := bufio.NewReader(clientConn)
	runtime.sendRaw(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"hello"`),
		Method:  appRuntimeHelloMethod,
		Params:  mustJSON(t, appRuntimeHelloParams{Generation: 1, APIVersion: appRuntimeAPIVersion, PID: 4242}),
	})
	frame, err := readSocketFrame(reader)
	if err != nil {
		t.Fatalf("app runtime hello got no answer: %v", err)
	}
	var ack jsonRPCMessage
	if err := json.Unmarshal(frame, &ack); err != nil {
		t.Fatalf("decode hello answer: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("hello refused: %s", ack.Error.Message)
	}
	go runtime.serve(reader)

	// The daemon publishes the connection from the same goroutine that answered
	// hello, so an answered hello does not yet mean appRuntimeConnected() is set.
	waitFor(t, "the daemon to adopt the app runtime", func() bool {
		return d.appRuntimeConnected() != nil
	})
	return runtime
}

// serve is the sidecar's read loop: dispatches run the handler, everything else
// is an answer to a callback this sidecar made.
func (f *fakeAppRuntime) serve(reader *bufio.Reader) {
	for {
		data, err := readSocketFrame(reader)
		if err != nil {
			f.mu.Lock()
			for _, ch := range f.pending {
				close(ch)
			}
			f.pending = map[string]chan jsonRPCMessage{}
			f.mu.Unlock()
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Method == "" {
			f.mu.Lock()
			ch := f.pending[jsonRPCIDKey(msg.ID)]
			delete(f.pending, jsonRPCIDKey(msg.ID))
			f.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.Method == "app.runtime.ping" {
			if !f.frozen() {
				f.sendRaw(jsonRPCResult(msg.ID, appRuntimePingResult{OK: true}))
			}
			continue
		}
		if f.frozen() {
			// Nothing reaches app code past this point, so nothing is announced
			// either: a frozen loop cannot read its own socket.
			continue
		}
		if msg.Method == "app.command" {
			f.serveCommand(msg)
			continue
		}
		if msg.Method != "app.dispatch" {
			f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "the fake sidecar only serves app.dispatch, app.command and app.runtime.ping"))
			continue
		}
		var req appDispatchRequest
		if err := json.Unmarshal(msg.Params, &req); err != nil {
			f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
			continue
		}
		f.mu.Lock()
		f.dispatches = append(f.dispatches, req)
		f.mu.Unlock()
		// Announced here rather than inside the goroutine so the order the daemon
		// sees is the order dispatches arrived, which is what the real host's single
		// loop guarantees.
		f.sendRaw(jsonRPCMessage{
			JSONRPC: "2.0",
			Method:  appRuntimeEnteredMethod,
			Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
		})
		// On its own goroutine: a handler that calls back into the daemon would
		// otherwise deadlock against this loop, which is the answer's only reader.
		go func(id json.RawMessage, req appDispatchRequest) {
			result := appDispatchResult{OK: true}
			if f.handler != nil {
				if err := f.handler(f, req); err != nil {
					result = appDispatchResult{OK: false, Error: err.Error()}
				}
			}
			// The other half of the announcement, and the reason the daemon never
			// has to infer that a handler left.
			f.sendRaw(jsonRPCMessage{
				JSONRPC: "2.0",
				Method:  appRuntimeLeftMethod,
				Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
			})
			f.sendRaw(jsonRPCResult(id, result))
		}(msg.ID, req)
	}
}

// serveCommand is the command half of the loop above, with the same
// entered/left announcements: a command runs on the one event loop every
// dispatch runs on, so the daemon has to be able to name it when that loop
// stops turning.
func (f *fakeAppRuntime) serveCommand(msg jsonRPCMessage) {
	var req appCommandRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	f.mu.Lock()
	f.commands = append(f.commands, req)
	f.mu.Unlock()
	f.sendRaw(jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  appRuntimeEnteredMethod,
		Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
	})
	go func(id json.RawMessage, req appCommandRequest) {
		result := appCommandDispatchResult{OK: true}
		if f.command != nil {
			payload, err := f.command(f, req)
			if err != nil {
				result = appCommandDispatchResult{OK: false, Error: err.Error()}
			} else {
				result = appCommandDispatchResult{OK: true, Payload: payload}
			}
		}
		f.sendRaw(jsonRPCMessage{
			JSONRPC: "2.0",
			Method:  appRuntimeLeftMethod,
			Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
		})
		f.sendRaw(jsonRPCResult(id, result))
	}(msg.ID, req)
}

// commandLog is what this sidecar was asked to run, in arrival order.
func (f *fakeAppRuntime) commandLog() []appCommandRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appCommandRequest, len(f.commands))
	copy(out, f.commands)
	return out
}

func mustMarshalHandlerParams(t *testing.T, params appRuntimeHandlerParams) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal handler-movement params: %v", err)
	}
	return data
}

func (f *fakeAppRuntime) sendRaw(msg jsonRPCMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = f.conn.Write(append(data, '\n'))
}

// call makes a collection callback, the way a handler's ctx.collections does.
func (f *fakeAppRuntime) call(method string, params appCollectionParams) (json.RawMessage, error) {
	f.mu.Lock()
	f.nextID++
	id := json.RawMessage(fmt.Sprintf(`"cb-%d"`, f.nextID))
	answer := make(chan jsonRPCMessage, 1)
	f.pending[jsonRPCIDKey(id)] = answer
	f.mu.Unlock()

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	f.sendRaw(jsonRPCMessage{JSONRPC: "2.0", ID: id, Method: method, Params: body})

	select {
	case msg, ok := <-answer:
		if !ok {
			return nil, errors.New("the connection closed before the daemon answered")
		}
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("the daemon never answered %s", method)
	}
}

func (f *fakeAppRuntime) dispatchLog() []appDispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appDispatchRequest, len(f.dispatches))
	copy(out, f.dispatches)
	return out
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

// waitFor blocks until cond holds. It exists because several of the signals
// these tests wait on — the daemon adopting a connection, the bus advancing a
// cursor — are reached from another goroutine with no channel to hand back.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---------------------------------------------------------------------------
// App fixtures
// ---------------------------------------------------------------------------

// newAppDaemon is a daemon with the event bus running, which is what every app
// surface assumes: registration persists a consumer row only once the bus has
// started, and a test reading that row off a stopped bus would be asserting
// against a state production never sees.
func newAppDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	if err := d.eventBus.Start(); err != nil {
		t.Fatalf("start the event bus: %v", err)
	}
	// Registered first so it runs LAST: a sidecar or a parked handler released by
	// a later cleanup has to be gone before the bus stops waiting on it.
	t.Cleanup(d.stopEventBus)
	return d
}

// installApp writes the rows one apply produces: a version carrying the frozen
// declaration, and the app pointed at it.
func installApp(t *testing.T, d *Daemon, name string, manifest appbuild.Manifest) store.AppVersion {
	t.Helper()
	manifest.Name = name
	if manifest.AttnAppAPI == 0 {
		manifest.AttnAppAPI = appbuild.APIVersion
	}
	if manifest.Entrypoint == "" {
		manifest.Entrypoint = "src/index.ts"
	}
	declaration, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal declaration: %v", err)
	}
	now := time.Now().UTC()
	if err := d.store.SaveApp(name, now); err != nil {
		t.Fatalf("save app %s: %v", name, err)
	}
	hash := fmt.Sprintf("sha256:%s-%x", name, len(declaration))
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  hash,
		Declaration:  string(declaration),
		ArtifactPath: filepath.Join("apps", name, hash+".js"),
	}, now)
	if err != nil {
		t.Fatalf("commit version for %s: %v", name, err)
	}
	if err := d.store.SetAppCurrentVersion(name, version.ID, now); err != nil {
		t.Fatalf("point %s at version %d: %v", name, version.ID, err)
	}
	d.syncAppRuntimeForVersion(name)
	return version
}

func subscribing(events ...string) appbuild.Manifest {
	return appbuild.Manifest{Subscribe: []appbuild.Subscribe{{Events: events}}}
}

func appEvent(name, subject string, seq int64) bus.Event {
	return bus.Event{Name: name, Subject: subject, Seq: seq, CreatedAt: time.Now().UTC()}
}

func invocationsOf(t *testing.T, d *Daemon, name string) []store.AppInvocation {
	t.Helper()
	rows, err := d.store.ListAppInvocations(name, 50)
	if err != nil {
		t.Fatalf("list invocations for %s: %v", name, err)
	}
	return rows
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// The whole happy path, through the real bus: a published fact reaches the
// handler, the invocation is recorded against the version that ran, and the
// cursor moves past the event.
func TestAppConsumerDispatchesAndAdvancesItsCursor(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "greeter", subscribing("ticket.*"))
	runtime := startFakeAppRuntime(t, d, nil)

	d.publishFact("ticket.created", "tk-1", map[string]string{"title": "work"})

	waitFor(t, "the handler to be dispatched", func() bool { return len(runtime.dispatchLog()) == 1 })
	got := runtime.dispatchLog()[0]
	if got.App != "greeter" || got.Handler != "ticket.*" {
		t.Fatalf("dispatch = app %q handler %q, want greeter/ticket.*", got.App, got.Handler)
	}
	if got.VersionID != version.ID {
		t.Fatalf("dispatch carried version %d, want the RUNNING version %d", got.VersionID, version.ID)
	}
	if got.Event.Name != "ticket.created" || got.Event.Subject != "tk-1" {
		t.Fatalf("dispatch event = %+v", got.Event)
	}
	if got.Artifact == "" {
		t.Fatal("dispatch carried no artifact path, so the host has nothing to import")
	}

	waitFor(t, "the invocation to be recorded", func() bool { return len(invocationsOf(t, d, "greeter")) == 1 })
	inv := invocationsOf(t, d, "greeter")[0]
	if inv.Status != appInvocationStatusOK {
		t.Fatalf("status = %q (%s), want ok", inv.Status, inv.Error)
	}
	if inv.VersionID != version.ID {
		t.Fatalf("invocation stamped version %d, want %d", inv.VersionID, version.ID)
	}

	waitFor(t, "the cursor to advance past the delivered event", func() bool {
		consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
		return err == nil && ok && consumer.Cursor >= got.Event.Seq
	})
}

// A handler that throws is the app's fault: the text is recorded, and the bus
// is told to keep the event rather than skip it.
func TestAppHandlerThrowRecordsFailureAndKeepsTheEvent(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, _ appDispatchRequest) error {
		return errors.New("TypeError: cannot read properties of undefined\n    at handle (index.ts:4:11)")
	})

	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 7))
	if err == nil {
		t.Fatal("a thrown handler returned no error, so the bus would advance past the event")
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("the bus was told %q, which does not name what threw", err)
	}

	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 1 {
		t.Fatalf("recorded %d invocation(s), want 1", len(rows))
	}
	if rows[0].Status != appInvocationStatusError {
		t.Fatalf("status = %q, want %q", rows[0].Status, appInvocationStatusError)
	}
	// The whole text, not the first line: the stack is what a developer acts on,
	// and the bus error is the only place it gets shortened.
	if !strings.Contains(rows[0].Error, "at handle (index.ts:4:11)") {
		t.Fatalf("recorded error lost the stack: %q", rows[0].Error)
	}
	// The app is on the clock now, and the clock names the event it is stuck on.
	stall, ok := d.appStallSnapshot("greeter")
	if !ok || stall.seq != 7 || stall.attempts != 1 {
		t.Fatalf("stall = %+v (present=%t), want seq 7 attempt 1", stall, ok)
	}
}

// Rule 2: the sidecar dying is the runtime's failure. It stalls delivery like
// any other, but no app is closer to being switched off for it.
func TestSidecarDeathIsARuntimeFailureAndDoesNotBlameTheApp(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	// The app is already failing on this event when the runtime dies.
	var die atomic.Bool
	startFakeAppRuntime(t, d, func(f *fakeAppRuntime, _ appDispatchRequest) error {
		if die.Load() {
			// The process goes away mid-handler and never answers. Closing the
			// socket is exactly what the daemon sees when the sidecar is killed.
			_ = f.conn.Close()
		}
		return errors.New("boom")
	})
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 3)); err == nil {
		t.Fatal("the throwing handler reported success")
	}
	if _, ok := d.appStallSnapshot("greeter"); !ok {
		t.Fatal("a thrown handler did not start the stall clock")
	}

	die.Store(true)
	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 3))
	if err == nil {
		t.Fatal("a dispatch into a dead sidecar reported success")
	}
	if !isRuntimeFailure(err) {
		t.Fatalf("error %q was not classified as the runtime's", err)
	}

	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 2 {
		t.Fatalf("recorded %d invocation(s), want 2", len(rows))
	}
	if rows[0].Status != appInvocationStatusRuntimeError {
		t.Fatalf("status = %q, want %q — a dead sidecar is not the app's fault",
			rows[0].Status, appInvocationStatusRuntimeError)
	}
	// The clock is cleared, not merely left alone: an app must not be charged for
	// the minutes the runtime was down.
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("the runtime dying left the app on the auto-disable clock: %+v", stall)
	}
}

// A runtime that is not installed at all reaches the same class, and says where
// to look — this is what a broken install looks like from inside `app status`.
func TestMissingRuntimeBinaryIsARuntimeFailure(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, filepath.Join(t.TempDir(), "not-installed"))

	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1))
	if !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("a missing runtime binary put the app on the auto-disable clock: %+v", stall)
	}
	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusRuntimeError {
		t.Fatalf("invocations = %+v, want one runtime_error", rows)
	}
	if !strings.Contains(rows[0].Error, appRuntimeHostOverride) {
		t.Fatalf("the recorded error does not say how to point attn at a runtime: %q", rows[0].Error)
	}
}

// A delivery whose context is cancelled — `attn app remove`, daemon stop —
// returns promptly and records nothing. An interrupted delivery did not happen.
func TestCancelledDeliveryReturnsPromptlyAndRecordsNothing(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	entered := make(chan struct{})
	release := make(chan struct{})
	// LIFO cleanup: registered after the harness's own, so it runs FIRST and a
	// failing assert below cannot leave the handler parked forever (#793).
	t.Cleanup(func() { close(release) })
	startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, _ appDispatchRequest) error {
		close(entered)
		<-release
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.deliverAppEvent(ctx, "greeter", appEvent("ticket.created", "tk-1", 1)) }()
	<-entered
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled delivery returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled delivery did not return; removing an app would hang on its in-flight handler")
	}
	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("a cancelled delivery recorded %d invocation(s): %+v", len(rows), rows)
	}
}

// `attn app remove` has to come back while a handler is still running. The
// consumer's delivery loop is what Unregister waits for, and that loop is
// parked inside the dispatch — so an uninterruptible dispatch would hang the
// command for as long as the app's code felt like running.
func TestRemovingAnAppWithAnInFlightDispatchReturnsPromptly(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	entered := make(chan struct{})
	release := make(chan struct{})
	// LIFO: registered after the sidecar's cleanup below would be wrong — this
	// has to run FIRST, so a failed assert releases the handler instead of
	// reading as a hang (#793).
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var once sync.Once
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	})

	d.publishFact("ticket.created", "tk-1", nil)
	<-entered

	removed := make(chan protocol.Response, 1)
	go func() { removed <- appRemove(t, d, "greeter") }()
	select {
	case resp := <-removed:
		if !resp.Ok {
			t.Fatalf("app remove: %v", protocol.Deref(resp.Error))
		}
		if !resp.AppRemoveResult.ConsumerRemoved {
			t.Fatal("remove did not report deleting the consumer")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("app remove hung on an in-flight handler")
	}

	// The interrupted delivery recorded nothing: it did not happen.
	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("an interrupted delivery recorded %d invocation(s): %+v", len(rows), rows)
	}
	if _, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter")); err != nil || ok {
		t.Fatalf("the consumer row survived the remove (ok=%t, err=%v)", ok, err)
	}
}

// A sidecar answer that arrives after its caller gave up — a retired consumer,
// an abandoned timeout — is dropped without a word on the wire. There is
// nothing the host could do with a complaint about a request it has forgotten.
func TestALateAnswerWithNobodyWaitingIsDropped(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	// The far end has to be drained or the peer's write blocks: net.Pipe is
	// unbuffered, and this test is about what happens after a request goes out.
	go func() { _, _ = io.Copy(io.Discard, clientConn) }()
	peer := newJSONRPCPeer(serverConn, bufio.NewReader(serverConn))

	if routed := peer.routeResponse(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"gone"`),
		Result:  json.RawMessage(`{"ok":true}`),
	}); routed {
		t.Fatal("an answer nobody was waiting for was reported as routed")
	}

	// And a caller that gave up leaves nothing behind for the next one to trip on.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, "the abandoned request to go out", func() bool {
			peer.pendingMu.Lock()
			defer peer.pendingMu.Unlock()
			return len(peer.pending) == 1
		})
		cancel()
	}()
	var out appDispatchResult
	if err := peer.request(ctx, "app runtime", "app.dispatch", appDispatchRequest{}, &out); !errors.Is(err, context.Canceled) {
		t.Fatalf("a request whose caller gave up returned %v, want context.Canceled", err)
	}
	if routed := peer.routeResponse(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"ok":true}`),
	}); routed {
		t.Fatal("the abandoned request was still holding its slot in the pending map")
	}
}

// A callback that arrives after its dispatch ended is refused, not served
// against whatever app is running now. This is the same seam that makes a late
// answer from a retired consumer harmless.
func TestCollectionCallbackAfterTheHandlerReturnedIsRefused(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "seen"}},
	})

	var escaped string
	runtime := startFakeAppRuntime(t, d, func(f *fakeAppRuntime, req appDispatchRequest) error {
		escaped = req.Dispatch
		return nil
	})
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	_, err := runtime.call("app.collection.get", appCollectionParams{
		Dispatch: escaped, Collection: "seen", ID: "tk-1",
	})
	if err == nil {
		t.Fatal("a collection call from a finished handler was served")
	}
	if !strings.Contains(err.Error(), "after that handler returned") {
		t.Fatalf("the refusal does not say what went wrong: %q", err)
	}
}

// The isolation proof. There is no namespace on the wire, so the only way an
// app could reach another's documents is by naming a collection it did not
// declare — and that is refused by name.
func TestAppCannotReachACollectionItDidNotDeclare(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "neighbour", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "secrets"}},
	})
	installApp(t, d, "greeter", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "seen"}},
	})

	var refusal error
	var wrote appDocument
	runtime := startFakeAppRuntime(t, d, func(f *fakeAppRuntime, req appDispatchRequest) error {
		// Its own collection works, and lands in its own namespace.
		raw, err := f.call("app.collection.put", appCollectionParams{
			Dispatch: req.Dispatch, Collection: "seen", ID: "tk-1",
			Body: json.RawMessage(`{"note":"mine"}`),
		})
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &wrote); err != nil {
			return err
		}
		// The neighbour's is not reachable, by any spelling.
		_, refusal = f.call("app.collection.get", appCollectionParams{
			Dispatch: req.Dispatch, Collection: "secrets", ID: "anything",
		})
		return nil
	})
	_ = runtime

	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if wrote.ID != "tk-1" {
		t.Fatalf("the app could not write its own collection: %+v", wrote)
	}
	if refusal == nil {
		t.Fatal("greeter read a collection belonging to neighbour")
	}
	if !strings.Contains(refusal.Error(), "did not declare a collection") {
		t.Fatalf("the refusal does not teach: %q", refusal)
	}

	// And the document really is in the app's own namespace.
	read, declared, err := d.store.ReadDocument(apps.Namespace("greeter"), "seen", "tk-1")
	if err != nil || !declared || !read.Found {
		t.Fatalf("document not in %s: declared=%t found=%t err=%v", apps.Namespace("greeter"), declared, read.Found, err)
	}
}

// Hot reload: applying a new version re-points the next dispatch without
// touching the one already running. Content-addressed artifacts are what make
// that true — each version is its own module path.
func TestHotReloadStampsTheNewVersionOnTheNextDispatch(t *testing.T) {
	d := newAppDaemon(t)
	first := installApp(t, d, "greeter", subscribing("ticket.*"))

	inFlight := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	runtime := startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, req appDispatchRequest) error {
		if req.VersionID == first.ID {
			close(inFlight)
			<-release
		}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1))
	}()
	<-inFlight

	// A second apply lands while the first handler is still running.
	second := installApp(t, d, "greeter", subscribing("ticket.*", "session.*"))
	if second.ID == first.ID {
		t.Fatal("the second apply produced the same version, so this proves nothing")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the in-flight delivery failed: %v", err)
	}
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-2", 2)); err != nil {
		t.Fatalf("the second delivery failed: %v", err)
	}

	log := runtime.dispatchLog()
	if len(log) != 2 {
		t.Fatalf("dispatches = %d, want 2", len(log))
	}
	if log[0].VersionID != first.ID {
		t.Fatalf("the in-flight dispatch was re-stamped to %d, want the OLD version %d", log[0].VersionID, first.ID)
	}
	if log[1].VersionID != second.ID {
		t.Fatalf("the next dispatch used version %d, want the NEW version %d", log[1].VersionID, second.ID)
	}
	if log[0].Artifact == log[1].Artifact {
		t.Fatalf("both versions resolved to the same artifact %q, so import() would hand back the old module", log[0].Artifact)
	}
	// The re-apply also re-pointed the consumer's subscriptions, without going
	// through an unregister that would have dropped the cursor.
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
	if err != nil || !ok {
		t.Fatalf("consumer: %v ok=%t", err, ok)
	}
	if !strings.Contains(consumer.Filter, "session.*") {
		t.Fatalf("filter = %q, want the new version's subscriptions", consumer.Filter)
	}
}

// A fact an app's filter lets through but that matches no declared subscription
// advances rather than stalls: there is no handler to succeed on a retry, and a
// permanent stall would pin the log's retention floor.
func TestUnhandledFactAdvancesRatherThanStalling(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	runtime := startFakeAppRuntime(t, d, nil)

	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("session.state.changed", "s-1", 1)); err != nil {
		t.Fatalf("an unhandled fact stalled the consumer: %v", err)
	}
	if got := len(runtime.dispatchLog()); got != 0 {
		t.Fatalf("an unhandled fact produced %d dispatch(es)", got)
	}
	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("an unhandled fact recorded %d invocation(s)", len(rows))
	}
}

// Exact beats a wildcard, and the longest prefix beats a shorter one: an app
// declaring both gets the handler it wrote for the specific fact.
func TestHandlerResolutionPrefersTheMostSpecificSubscription(t *testing.T) {
	patterns := []string{"*", "session.*", "session.state.changed", "ticket.*"}
	for _, tc := range []struct{ event, want string }{
		{"session.state.changed", "session.state.changed"},
		{"session.spawned", "session.*"},
		{"ticket.created", "ticket.*"},
		{"pr.merged", "*"},
	} {
		if got := resolveAppHandler(patterns, tc.event); got != tc.want {
			t.Errorf("resolveAppHandler(%q) = %q, want %q", tc.event, got, tc.want)
		}
	}
	if got := resolveAppHandler([]string{"ticket.*"}, "session.spawned"); got != "" {
		t.Errorf("an unmatched fact resolved to handler %q", got)
	}
}

// An app with no subscriptions must not be woken by everything. bus.ParseFilter
// reads an empty expression as All, so the empty case needs its own answer.
func TestAppWithNoSubscriptionsSubscribesToNothing(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "quiet", appbuild.Manifest{})

	filter, err := d.appFilter("quiet")
	if err != nil {
		t.Fatalf("appFilter: %v", err)
	}
	if len(filter) != 1 || filter[0] != apps.NoSubscriptionsPattern {
		t.Fatalf("filter = %v, want the nothing-matches pattern", filter)
	}
	for _, name := range []string{"ticket.created", "session.state.changed", "app.enabled.changed"} {
		if bus.MatchPattern(filter[0], name) {
			t.Fatalf("an app that declared no subscriptions would be woken by %s", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Supervision surface
// ---------------------------------------------------------------------------

// writeExecutableStub writes a shell script the supervisor can launch as the
// runtime host. The real compiled sidecar is Bun and takes a second to build;
// what these tests need from it is a process that lives or dies on command.
func writeExecutableStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attn-app-runtime")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write runtime stub: %v", err)
	}
	return path
}

func appRuntimeStatus(t *testing.T, d *Daemon) *protocol.AppRuntimeStatusResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAppRuntimeStatus(c, &protocol.AppRuntimeStatusMessage{Cmd: protocol.CmdAppRuntimeStatus})
	})
	if !resp.Ok {
		t.Fatalf("app runtime status: %v", protocol.Deref(resp.Error))
	}
	return resp.AppRuntimeStatusResult
}

func appRuntimeRestart(t *testing.T, d *Daemon) *protocol.AppRuntimeRestartResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAppRuntimeRestart(c, &protocol.AppRuntimeRestartMessage{Cmd: protocol.CmdAppRuntimeRestart})
	})
	if !resp.Ok {
		t.Fatalf("app runtime restart: %v", protocol.Deref(resp.Error))
	}
	return resp.AppRuntimeRestartResult
}

// `attn app runtime status` before anything has started, and `restart` as the
// way to start one. "Never started" is a different answer from "stopped", and
// saying the second would send a reader looking for a fault.
func TestRuntimeStatusIsHonestBeforeAnythingHasStarted(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	host := writeExecutableStub(t, "sleep 60")
	t.Setenv(appRuntimeHostOverride, host)

	before := appRuntimeStatus(t, d)
	if before.Runtime != nil {
		t.Fatalf("a daemon that has never run an app reported a runtime: %+v", before.Runtime)
	}
	if before.HostPath == nil || *before.HostPath != host {
		t.Fatalf("host path = %v, want %s", before.HostPath, host)
	}
	if before.Apps != 1 || before.AppsEnabled != 1 {
		t.Fatalf("apps = %d installed / %d enabled, want 1/1", before.Apps, before.AppsEnabled)
	}
	if before.LogPath != AppRuntimeLogPath(d.socketPath) {
		t.Fatalf("log path = %q, want %q", before.LogPath, AppRuntimeLogPath(d.socketPath))
	}

	t.Cleanup(d.stopAppRuntime)
	started := appRuntimeRestart(t, d)
	if started.Was != "stopped" {
		t.Fatalf("was = %q, want stopped", started.Was)
	}
	if started.Runtime.Desired != "running" {
		t.Fatalf("after a restart the runtime is %+v, want desired running", started.Runtime)
	}
	if after := appRuntimeStatus(t, d); after.Runtime == nil {
		t.Fatal("status still reports no runtime after one was started")
	}
}

// A parked runtime is a whole-system outage: every app's status says so, a
// notification is written, and `attn app runtime restart` is the way back.
func TestParkedRuntimeIsVisibleOnEveryAppAndRevivable(t *testing.T) {
	d := newAppDaemon(t)
	// One restart, then park. The tripwire is ten in production; the point here
	// is the crossing, not the count.
	d.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, d, "greeter", subscribing("ticket.*"))
	installApp(t, d, "auditor", subscribing("pr.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 3"))
	t.Cleanup(d.stopAppRuntime)

	if err := d.ensureAppRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	waitFor(t, "the crash-looping runtime to be parked", func() bool {
		snapshot, ok := d.appRuntimeSnapshot()
		return ok && snapshot.Phase == supervise.PhaseParked
	})

	for _, name := range []string{"greeter", "auditor"} {
		resp := appStatus(t, d, name)
		if !resp.Ok {
			t.Fatalf("app status %s: %v", name, protocol.Deref(resp.Error))
		}
		runtime := resp.AppStatusResult.Runtime
		if runtime == nil {
			t.Fatalf("app status %s carried no runtime, so a reader cannot tell why nothing runs", name)
		}
		if runtime.Phase != string(supervise.PhaseParked) {
			t.Fatalf("app status %s reported runtime phase %q, want parked", name, runtime.Phase)
		}
		if runtime.LastExit == nil || !strings.Contains(*runtime.LastExit, "3") {
			t.Fatalf("app status %s does not say how the runtime died: %v", name, runtime.LastExit)
		}
	}

	// The only one of these three a person ever sees.
	notifications, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var parked *store.NotificationRecord
	for i := range notifications {
		if notifications[i].Kind == notificationKindAppRuntimeParked {
			parked = &notifications[i]
		}
	}
	if parked == nil {
		t.Fatalf("no app-runtime-parked notification among %d", len(notifications))
	}
	if !strings.Contains(parked.Body, "attn app runtime restart") {
		t.Fatalf("the notification does not name the way back: %q", parked.Body)
	}
	// No app's handlers run and nothing retries on its own, so this is the
	// notification that earns the ambient surface — and it has to reach the app
	// when it happens, not whenever the feed is re-pushed next.
	if parked.Severity != store.NotificationCritical {
		t.Fatalf("severity = %q, want critical", parked.Severity)
	}
	if created := appFacts(t, d, FactNotificationCreated); len(created) != 1 || created[0].Subject != parked.ID {
		t.Fatalf("notification.created facts = %+v, want one for %s", created, parked.ID)
	}

	revived := appRuntimeRestart(t, d)
	if revived.Was != string(supervise.PhaseParked) {
		t.Fatalf("was = %q, want parked — that answer is what tells a reader it was revived", revived.Was)
	}
	if revived.Runtime.Phase == string(supervise.PhaseParked) {
		t.Fatalf("restart left the runtime parked: %+v", revived.Runtime)
	}
	// The revived runtime gets its restart budget back rather than re-parking on
	// its first exit — TestStopClearsTheRestartBudget is where that is pinned
	// against a fake clock; here the stub crashes too fast to watch.
}

// Parked has to survive traffic. Dispatch is the loudest caller the runtime has
// — one per fact, for as long as facts keep arriving — so a dispatch that
// revived the child would give a broken host a fresh restart budget every few
// seconds and park it again at the end of each one. Measured on a broken host
// before the split: three parkings and three critical notifications in five and
// a half minutes.
func TestDispatchLeavesAParkedRuntimeParked(t *testing.T) {
	d := newAppDaemon(t)
	d.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 3"))
	t.Cleanup(d.stopAppRuntime)

	if err := d.ensureAppRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	waitFor(t, "the crash-looping runtime to be parked", func() bool {
		snapshot, ok := d.appRuntimeSnapshot()
		return ok && snapshot.Phase == supervise.PhaseParked
	})
	parked, _ := d.appRuntimeSnapshot()

	for seq := int64(1); seq <= 3; seq++ {
		err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", seq))
		if !isRuntimeFailure(err) {
			t.Fatalf("dispatch %d into a parked runtime returned %v, want a runtime failure", seq, err)
		}
		// The app's owner reads this line and nothing else: it has to name the
		// state and the one command that leaves it.
		if !strings.Contains(err.Error(), "parked") || !strings.Contains(err.Error(), "attn app runtime restart") {
			t.Fatalf("the dispatch error does not say what happened or how to fix it: %q", err)
		}
	}

	after, ok := d.appRuntimeSnapshot()
	if !ok || after.Phase != supervise.PhaseParked {
		t.Fatalf("three dispatches moved the runtime off parked: %+v", after)
	}
	if after.Generation != parked.Generation {
		t.Fatalf("generation went %d → %d, so a dispatch started the runtime again",
			parked.Generation, after.Generation)
	}
	// One outage, one notification. Re-parking would write one per revival, and
	// these are critical — the surface that cannot be ignored is the one that
	// must not repeat.
	if notes := appNotifications(t, d, notificationKindAppRuntimeParked); len(notes) != 1 {
		t.Fatalf("app-runtime-parked notifications = %d, want 1", len(notes))
	}
	// Not the app's fault, so it must not be on the auto-disable clock while the
	// runtime is down.
	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 3 {
		t.Fatalf("recorded %d invocation(s), want 3", len(rows))
	}
	for _, row := range rows {
		if row.Status != appInvocationStatusRuntimeError {
			t.Fatalf("status = %q, want %q", row.Status, appInvocationStatusRuntimeError)
		}
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("a parked runtime put the app on the auto-disable clock: %+v", stall)
	}

	// And the deliberate way in still revives it — the split must not turn parked
	// into a dead end.
	if revived := appRuntimeRestart(t, d); revived.Runtime.Phase == string(supervise.PhaseParked) {
		t.Fatalf("restart left the runtime parked: %+v", revived.Runtime)
	}
}

// A runtime whose api_version does not match is refused at hello rather than
// half-served, and the refusal says it is a stale install.
func TestRuntimeWithTheWrongAPIVersionIsRefusedAtHello(t *testing.T) {
	_, _, recognized, err := parseAppRuntimeHello([]byte(
		`{"jsonrpc":"2.0","id":"1","method":"app_runtime.hello","params":{"generation":1,"api_version":99,"pid":7}}`))
	if !recognized {
		t.Fatal("the app runtime hello was not recognized as one")
	}
	if err == nil {
		t.Fatal("a runtime speaking api version 99 was accepted")
	}
	if !strings.Contains(err.Error(), "stale install") {
		t.Fatalf("the refusal does not say what to do: %q", err)
	}
}

// A plugin's hello must not be mistaken for a runtime's, and the runtime sniff
// runs first — so it also has to leave everything else alone.
func TestAppRuntimeHelloSniffIgnoresEverythingElse(t *testing.T) {
	for _, frame := range []string{
		`{"jsonrpc":"2.0","id":"1","method":"hello","params":{"name":"worktree-provider"}}`,
		`{"cmd":"heartbeat","id":"sess"}`,
		`not json at all`,
	} {
		if _, _, recognized, _ := parseAppRuntimeHello([]byte(frame)); recognized {
			t.Fatalf("the app runtime sniff claimed %q", frame)
		}
	}
}

// Where a daemon looks for its sidecar. The profile-suffixed name exists for
// remotes: several profile-isolated daemons install into one ~/.local/bin, and
// each has to start the runtime built from the same source as the binary beside
// it — not whichever profile synced last.
func TestAppRuntimeHostCandidates(t *testing.T) {
	bundled := appRuntimeHostCandidates("/Applications/attn.app/Contents/MacOS/attn", "")
	if len(bundled) != 2 || bundled[0] != "/Applications/attn.app/Contents/Resources/app-runtime/attn-app-runtime" {
		t.Fatalf("bundled candidates = %v", bundled)
	}

	remote := appRuntimeHostCandidates("/home/v/.local/bin/attn-dev", "dev")
	want := []string{
		"/home/v/.local/bin/attn-app-runtime-dev",
		"/home/v/.local/bin/attn-app-runtime",
	}
	if len(remote) != len(want) {
		t.Fatalf("profile candidates = %v, want %v", remote, want)
	}
	for i := range want {
		if remote[i] != want[i] {
			t.Fatalf("profile candidates = %v, want %v", remote, want)
		}
	}

	// A checkout keeps working under any profile: `./attn` is one binary and the
	// staged sidecar beside it carries no suffix.
	checkout := appRuntimeHostCandidates("/src/attn/attn", "dev")
	if checkout[len(checkout)-1] != "/src/attn/attn-app-runtime" {
		t.Fatalf("checkout candidates = %v", checkout)
	}
}
