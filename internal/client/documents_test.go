package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// Unix socket paths are length-limited (notably on macOS), and the default
// t.TempDir() path is long enough to blow the limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "attn-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func doc(id, body string, rev int) protocol.StoredDocument {
	return protocol.StoredDocument{ID: id, Body: body, Rev: rev}
}

// docServer is a unix socket that answers one doc_subscribe with the deliveries
// it is handed and then does whatever the test asked of it. The daemon's own
// handler is covered in internal/daemon; what is under test here is the client's
// read loop, so the server side is scripted rather than real.
func docServer(t *testing.T, serve func(conn net.Conn, subscribe protocol.DocSubscribeMessage)) string {
	t.Helper()
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var msg protocol.DocSubscribeMessage
		if err := json.NewDecoder(conn).Decode(&msg); err != nil {
			return
		}
		serve(conn, msg)
	}()
	return sockPath
}

func sendDelivery(t *testing.T, conn net.Conn, result protocol.DocSubscribeResult) {
	t.Helper()
	if err := json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DocSubscribeResult: &result}); err != nil {
		t.Errorf("send delivery: %v", err)
	}
}

// A live query that ends because the connection went reports it as an error too.
// Nothing distinguishes "the daemon stopped" from "nothing has changed lately"
// except this, and a watcher that cannot tell them apart shows a frozen list
// forever.
func TestASubscriptionEndingWithoutAnErrorIsStillAnError(t *testing.T) {
	sockPath := docServer(t, func(conn net.Conn, _ protocol.DocSubscribeMessage) {
		sendDelivery(t, conn, protocol.DocSubscribeResult{
			Delivery: 1, AsOfSeq: 7, Order: []string{"a"},
			Upsert: []protocol.StoredDocument{doc("a", `{}`, 1)},
		})
		// and then simply goes away, the way a daemon that was restarted does.
	})

	var seen int
	err := New(sockPath).DocSubscribe(protocol.DocumentQuery{}, nil, func(DocWindow) bool {
		seen++
		return true
	})
	if seen != 1 {
		t.Fatalf("the subscriber saw %d windows, want the one that was sent", seen)
	}
	code, ended := DocSubscriptionCode(err)
	if !ended {
		t.Fatalf("a daemon that went away reported %v", err)
	}
	if code != "" {
		t.Fatalf("a lost connection carried code %q, and there is nothing for a caller to branch on", code)
	}
	if !DocConnectionLost(err) {
		t.Fatal("a lost connection is the one ending worth resubscribing to, and it did not say so")
	}
}

// The three endings that are NOT worth retrying, kept together because what
// separates them from a lost connection is one bit that is easy to conflate
// with an empty code. A resuming watcher that retried any of them would spin:
// the daemon refuses the same query again, and an unapplicable delivery repeats
// with the same declared revisions.
func TestOnlyALostConnectionIsWorthResubscribing(t *testing.T) {
	unapplicable := docServer(t, func(conn net.Conn, _ protocol.DocSubscribeMessage) {
		// Orders a document whose body it never sent and the client never held.
		sendDelivery(t, conn, protocol.DocSubscribeResult{Delivery: 1, Order: []string{"ghost"}})
	})
	err := New(unapplicable).DocSubscribe(protocol.DocumentQuery{}, nil, func(DocWindow) bool { return true })
	if _, ended := DocSubscriptionCode(err); !ended {
		t.Fatalf("an unapplicable delivery reported %v", err)
	}
	if DocConnectionLost(err) {
		t.Fatal("an unapplicable delivery claimed the connection went; resubscribing repeats it forever")
	}

	coded := docServer(t, func(conn net.Conn, _ protocol.DocSubscribeMessage) {
		json.NewEncoder(conn).Encode(protocol.Response{
			Ok:        false,
			Error:     protocol.Ptr("collection undefined while subscribed"),
			ErrorCode: protocol.Ptr(protocol.ErrorCodeCollectionUndefined),
		})
	})
	err = New(coded).DocSubscribe(protocol.DocumentQuery{}, nil, func(DocWindow) bool { return true })
	if DocConnectionLost(err) {
		t.Fatal("a collection that was removed claimed the connection went")
	}

	if DocConnectionLost(errString("connection refused")) {
		t.Fatal("an ordinary error claimed to be a lost subscription")
	}
}

// The daemon's coded ending has to survive the read loop as a code, because a UI
// host branches on it: `collection_undefined` kills the tile, an empty code
// reconnects.
func TestACodedEndingKeepsItsCode(t *testing.T) {
	sockPath := docServer(t, func(conn net.Conn, _ protocol.DocSubscribeMessage) {
		json.NewEncoder(conn).Encode(protocol.Response{
			Ok:        false,
			Error:     protocol.Ptr("collection undefined while subscribed"),
			ErrorCode: protocol.Ptr(protocol.ErrorCodeCollectionUndefined),
		})
	})

	err := New(sockPath).DocSubscribe(protocol.DocumentQuery{}, nil, func(DocWindow) bool { return true })
	code, ended := DocSubscriptionCode(err)
	if !ended || code != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("ended=%v code=%q, want the daemon's %q", ended, code, protocol.ErrorCodeCollectionUndefined)
	}
	if got := err.Error(); got == "" {
		t.Fatalf("the ending carried no message")
	}
}

// Resuming declares revisions, not bodies. The wire carries the pairs; the
// caller hands over whole documents so that everything it declared is also
// something it can render.
func TestResumingDeclaresTheRevisionsOfWhatIsHeld(t *testing.T) {
	declared := make(chan []protocol.DocumentRevision, 1)
	sockPath := docServer(t, func(conn net.Conn, msg protocol.DocSubscribeMessage) {
		declared <- msg.Have
		sendDelivery(t, conn, protocol.DocSubscribeResult{Delivery: 1, Order: []string{"a"}})
	})

	held := []protocol.StoredDocument{doc("a", `{"n":1}`, 4)}
	window := make(chan DocWindow, 1)
	err := New(sockPath).DocSubscribe(protocol.DocumentQuery{}, held, func(w DocWindow) bool {
		window <- w
		return false
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	have := <-declared
	if len(have) != 1 || have[0].ID != "a" || have[0].Rev != 4 {
		t.Fatalf("declared %+v, want a@4", have)
	}
	// And the body it never re-sent came out of the cache the caller seeded.
	got := <-window
	if len(got.Documents) != 1 || got.Documents[0].Body != `{"n":1}` {
		t.Fatalf("resumed window is %+v, want the held body", got.Documents)
	}
	if len(got.Changed) != 0 {
		t.Fatalf("the resumed window reported %v as changed, and nothing did", got.Changed)
	}
}

func TestApplyingADeliveryFollowsTheClientRule(t *testing.T) {
	cache := map[string]protocol.StoredDocument{
		"a": doc("a", `{"v":"old"}`, 1),
		"b": doc("b", `{"v":"kept"}`, 1),
		"c": doc("c", `{"v":"gone"}`, 1),
	}
	window, err := applyDocDelivery(cache, &protocol.DocSubscribeResult{
		Delivery: 3,
		AsOfSeq:  42,
		Order:    []string{"b", "a"},
		Upsert:   []protocol.StoredDocument{doc("a", `{"v":"new"}`, 2)},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if window.Delivery != 3 || window.AsOfSeq != 42 {
		t.Fatalf("window is delivery %d as of %d", window.Delivery, window.AsOfSeq)
	}
	// Order decides the order, an upsert overrides the cache, and the cached
	// body is used untouched for what did not travel.
	if len(window.Documents) != 2 ||
		window.Documents[0].Body != `{"v":"kept"}` ||
		window.Documents[1].Body != `{"v":"new"}` {
		t.Fatalf("applied %+v", window.Documents)
	}
	if len(window.Changed) != 1 || window.Changed[0] != "a" {
		t.Fatalf("changed is %v, want only the body that travelled", window.Changed)
	}
	// Forget everything not named in order: the cache is the window, not a
	// history, which is what bounds a long-lived subscription's memory.
	if _, still := cache["c"]; still {
		t.Fatalf("a document that left the window is still cached")
	}
	if len(cache) != 2 {
		t.Fatalf("cache holds %d documents after a 2-document window", len(cache))
	}
}

// The one case a subscriber cannot render: the daemon believes it holds a body
// it does not. Reporting it beats drawing a window with a hole in it, and the
// message says how to recover.
func TestADeliveryOrderingAnUnheldDocumentEndsTheSubscription(t *testing.T) {
	_, err := applyDocDelivery(map[string]protocol.StoredDocument{}, &protocol.DocSubscribeResult{
		Delivery: 1,
		Order:    []string{"ghost"},
	})
	code, ended := DocSubscriptionCode(err)
	if !ended {
		t.Fatalf("apply returned %v, want a subscription ending", err)
	}
	if code != "" {
		t.Fatalf("a client-side failure carried daemon code %q", code)
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "resubscribe") {
		t.Fatalf("the ending does not say what is missing or what to do: %v", err)
	}
}

func TestDocSubscriptionCodeIgnoresOtherErrors(t *testing.T) {
	if _, ended := DocSubscriptionCode(nil); ended {
		t.Fatalf("nil is a subscription ending")
	}
	if _, ended := DocSubscriptionCode(errString("connection refused")); ended {
		t.Fatalf("an ordinary error is a subscription ending")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
