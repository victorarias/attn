package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Live queries over the WebSocket. The delivery semantics themselves are pinned
// in documents_windowed_test.go against the IPC transport; what these assert is
// what the second transport adds — an identity per subscription, a way out, a
// ceiling, and a teardown that survives every way a client can leave.

// wsSubscriber is one WebSocket client's view of its live queries: the send
// channel the daemon queues into, decoded into the two envelopes this surface
// speaks.
type wsSubscriber struct {
	client *wsClient
}

func newWSSubscriber() *wsSubscriber {
	return &wsSubscriber{client: &wsClient{send: make(chan outboundMessage, 256)}}
}

// nextEvent reads the next queued message, failing rather than hanging if the
// daemon sends nothing. The timeout is a tripwire on a deadlock, not a wait for
// something slow: every assertion here is woken by a real write.
func (s *wsSubscriber) nextEvent(t *testing.T) map[string]any {
	t.Helper()
	select {
	case msg := <-s.client.send:
		var out map[string]any
		if err := json.Unmarshal(msg.payload, &out); err != nil {
			t.Fatalf("decode outbound message: %v", err)
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon sent nothing to this client")
		return nil
	}
}

// nextDelivery reads one doc_subscription_delivery for the given id.
func (s *wsSubscriber) nextDelivery(t *testing.T, id string) map[string]any {
	t.Helper()
	event := s.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionDelivery {
		t.Fatalf("expected a delivery, got %v", event)
	}
	if event["subscription_id"] != id {
		t.Fatalf("delivery carried subscription_id %v, want %q", event["subscription_id"], id)
	}
	return event
}

func deliveryOrder(t *testing.T, event map[string]any) []string {
	t.Helper()
	raw, _ := event["order"].([]any)
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		out = append(out, fmt.Sprint(id))
	}
	return out
}

func upsertIDs(t *testing.T, event map[string]any) []string {
	t.Helper()
	raw, _ := event["upsert"].([]any)
	out := make([]string, 0, len(raw))
	for _, doc := range raw {
		entry, _ := doc.(map[string]any)
		out = append(out, fmt.Sprint(entry["id"]))
	}
	return out
}

func wsSubscribe(d *Daemon, s *wsSubscriber, id string, have []protocol.DocumentRevision) {
	d.handleDocSubscribeWS(s.client, &protocol.DocSubscribeMessage{
		Cmd:            protocol.CmdDocSubscribe,
		Query:          testQuery(),
		Have:           have,
		SubscriptionID: protocol.Ptr(id),
	})
}

// A WebSocket subscription behaves exactly as the IPC one does, plus an id: the
// current window arrives immediately, and a write brings the next.
func TestWebSocketSubscriptionDeliversAndStaysLive(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "already-here", `{"status":"pending"}`)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)

	first := sub.nextDelivery(t, "tile-1")
	if got := deliveryOrder(t, first); len(got) != 1 || got[0] != "already-here" {
		t.Fatalf("first delivery ordered %v", got)
	}
	if first["delivery"] != float64(1) {
		t.Fatalf("first delivery counted %v, want 1", first["delivery"])
	}

	putDoc(t, d, "b", `{"status":"pending"}`)
	second := sub.nextDelivery(t, "tile-1")
	if got := deliveryOrder(t, second); len(got) != 2 {
		t.Fatalf("second delivery ordered %v, want both documents", got)
	}
	// Only the new body travels: the subscriber was already sent the other one.
	if got := upsertIDs(t, second); len(got) != 1 || got[0] != "b" {
		t.Fatalf("second delivery carried bodies %v, want only b", got)
	}
}

// One connection carries many, so a delivery has to say which subscription it
// belongs to — and each one runs its own query.
func TestWebSocketSubscriptionsAreIndependentOnOneConnection(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	if got := d.documentSubscriptionCount(); got != 2 {
		t.Fatalf("daemon holds %d subscriptions, want 2", got)
	}

	putDoc(t, d, "a", `{"status":"pending"}`)
	seen := map[string]bool{}
	for range 2 {
		event := sub.nextEvent(t)
		seen[fmt.Sprint(event["subscription_id"])] = true
	}
	if !seen["tile-1"] || !seen["tile-2"] {
		t.Fatalf("the write woke %v, want both subscriptions", seen)
	}
}

// The way out for the way in: an unsubscribed query stops costing the daemon a
// re-run on every write to its collection.
func TestUnsubscribeEndsTheSubscription(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")

	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
	})
	waitForSubscriptionCount(t, d, 0)

	// An id nobody holds is ignored rather than refused: an unsubscribe racing
	// the daemon's own ending is the ordinary case.
	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
	})
}

// A client that vanishes leaves loops that would re-run its queries forever.
func TestDisconnectDropsEveryLiveQueryTheClientHeld(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	d.dropDocSubscriptions(sub.client)
	waitForSubscriptionCount(t, d, 0)
	if got := sub.client.docSubscriptions.count(); got != 0 {
		t.Fatalf("the client still holds %d subscriptions", got)
	}
}

// The tripwire: past the per-client limit a subscription is refused by name,
// never silently dropped. A refusal a client cannot read is a tile that renders
// nothing forever with nothing to look up.
func TestTooManySubscriptionsIsARefusalThatNamesTheLimit(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	for i := range protocol.DocSubscriptionsPerClient {
		wsSubscribe(d, sub, fmt.Sprintf("tile-%d", i), nil)
		sub.nextDelivery(t, fmt.Sprintf("tile-%d", i))
	}

	wsSubscribe(d, sub, "one-too-many", nil)
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded {
		t.Fatalf("expected the subscription to be refused, got %v", event)
	}
	if event["code"] != protocol.ErrorCodeSubscriptionLimit {
		t.Fatalf("refusal code = %v, want %q", event["code"], protocol.ErrorCodeSubscriptionLimit)
	}
	message := fmt.Sprint(event["error"])
	if !strings.Contains(message, fmt.Sprint(protocol.DocSubscriptionsPerClient)) {
		t.Fatalf("refusal does not name the limit: %s", message)
	}
	if !strings.Contains(message, "one-too-many") {
		t.Fatalf("refusal does not name the ask: %s", message)
	}
	if got := sub.client.docSubscriptions.count(); got != protocol.DocSubscriptionsPerClient {
		t.Fatalf("the refused subscription changed the count to %d", got)
	}
}

// Two live subscriptions under one id would make every delivery ambiguous.
func TestASecondSubscriptionUnderOneIDIsRefused(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")

	wsSubscribe(d, sub, "tile-1", nil)
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded ||
		event["code"] != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("expected an invalid_query refusal, got %v", event)
	}
	if got := d.documentSubscriptionCount(); got != 1 {
		t.Fatalf("the refusal disturbed the live subscription: count = %d", got)
	}
}

// The WebSocket has one failure channel, so a query that never starts and a
// subscription that ends after acceptance both arrive as doc_subscription_ended
// — told apart by code, which is what lets a host pick between "kill the tile"
// and "the caller asked for the wrong thing".
func TestSubscribeRefusalsAndEndingsShareOneEnvelope(t *testing.T) {
	d := newDaemonForTest(t)

	sub := newWSSubscriber()
	// Never declared: a refusal, before the subscription exists.
	wsSubscribe(d, sub, "tile-1", nil)
	refusal := sub.nextEvent(t)
	if refusal["event"] != protocol.EventDocSubscriptionEnded ||
		refusal["code"] != protocol.ErrorCodeUndeclaredCollection {
		t.Fatalf("expected an undeclared_collection refusal, got %v", refusal)
	}

	// Declared, subscribed, then removed underneath it: an ending, after
	// acceptance.
	defineTestCollection(t, d)
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	undefineTestCollection(t, d)
	ended := sub.nextEvent(t)
	if ended["event"] != protocol.EventDocSubscriptionEnded ||
		ended["code"] != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("expected a collection_undefined ending, got %v", ended)
	}
	waitForSubscriptionCount(t, d, 0)
}

// A cursor names a document that moves out from under a live window, so a
// subscription refuses one — on both transports, from the one check.
func TestSubscribingWithACursorIsRefusedOnTheWebSocket(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	q := testQuery()
	q.After = protocol.Ptr("a")
	d.handleDocSubscribeWS(sub.client, &protocol.DocSubscribeMessage{
		Cmd: protocol.CmdDocSubscribe, Query: q, SubscriptionID: protocol.Ptr("tile-1"),
	})
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded ||
		event["code"] != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("expected an invalid_query refusal, got %v", event)
	}
	if !strings.Contains(fmt.Sprint(event["error"]), "after cursor") {
		t.Fatalf("refusal does not say what was wrong: %v", event["error"])
	}
}

// Resume is the same on this transport as on the other: what the client declares
// it holds is what the first delivery does not re-send.
func TestWebSocketResumeSendsOnlyWhatChanged(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "kept", `{"status":"pending"}`)
	putDoc(t, d, "moved", `{"status":"pending"}`)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	first := sub.nextDelivery(t, "tile-1")
	held := map[string]int{}
	for _, doc := range first["upsert"].([]any) {
		entry := doc.(map[string]any)
		held[fmt.Sprint(entry["id"])] = int(entry["rev"].(float64))
	}
	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
	})
	waitForSubscriptionCount(t, d, 0)

	// One of the two moved while nobody was watching.
	putDoc(t, d, "moved", `{"status":"approved"}`)

	have := []protocol.DocumentRevision{
		{ID: "kept", Rev: held["kept"]},
		{ID: "moved", Rev: held["moved"]},
	}
	wsSubscribe(d, sub, "tile-2", have)
	resumed := sub.nextDelivery(t, "tile-2")
	if got := len(deliveryOrder(t, resumed)); got != 2 {
		t.Fatalf("resumed window holds %d documents, want 2", got)
	}
	if got := upsertIDs(t, resumed); len(got) != 1 || got[0] != "moved" {
		t.Fatalf("resume carried bodies %v, want only the one that changed", got)
	}
}

// On the unix socket the connection IS the subscription, so an id there would
// name nothing — and a client that sends one has misread the surface badly
// enough that answering it would be worse than refusing.
func TestIPCSubscribeRefusesASubscriptionID(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocSubscribe(c, &protocol.DocSubscribeMessage{
			Cmd: protocol.CmdDocSubscribe, Query: testQuery(), SubscriptionID: protocol.Ptr("tile-1"),
		})
	})
	if resp.Ok {
		t.Fatal("the unix socket accepted a subscription_id")
	}
	if got := protocol.Deref(resp.ErrorCode); got != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("refusal code = %q, want %q", got, protocol.ErrorCodeInvalidQuery)
	}
}

// undefineTestCollection removes the collection these subscriptions watch, which
// is what ends an accepted one with collection_undefined.
func undefineTestCollection(t *testing.T, d *Daemon) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocUndefine(c, &protocol.DocUndefineMessage{
			Cmd: protocol.CmdDocUndefine, Namespace: testDocNS, Collection: testDocColl,
		})
	})
	if !resp.Ok {
		t.Fatalf("undefine: %v", protocol.Deref(resp.Error))
	}
}

// waitForSubscriptionCount watches the daemon's own registry: a subscription
// loop ends on its own goroutine, and the count moving IS the signal that it
// did.
func waitForSubscriptionCount(t *testing.T, d *Daemon, want int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("the daemon to hold %d live subscriptions", want), func() bool {
		return d.documentSubscriptionCount() == want
	})
}
