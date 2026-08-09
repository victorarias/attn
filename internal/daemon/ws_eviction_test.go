package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

// sendClientHelloAs identifies a test client the way the app does: with an id
// that outlives the connection, so the daemon can recognise it on its return.
func sendClientHelloAs(t *testing.T, conn *websocket.Conn, clientID string) {
	t.Helper()
	if err := writeWS(conn, map[string]interface{}{
		"cmd":          protocol.CmdClientHello,
		"client_kind":  "daemon-test",
		"client_id":    clientID,
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	}); err != nil {
		t.Fatalf("send client hello: %v", err)
	}
}

// Eviction sizing for the direct link. One message per broadcast, big enough
// that the write pump blocks on the socket almost immediately, and 256 of them
// queued behind it — so the backlog the daemon is sitting on is enormous
// compared to anything the two kernels can hold.
//
// The receipt: an aborted connection on loopback delivers whatever the client's
// receive buffer already holds and then fails; measured on macOS that is ~400 KB
// (probe: 1 MB written into a stalled connection, SO_LINGER 0, client read
// 400,368 bytes and then ECONNRESET, 0 ms after the abort). Linux CI's autotuned
// buffers are larger but nowhere near the cap below.
const (
	stalledClientMessageBytes = 1 << 20  // 1 MB a broadcast
	maxBacklogDeliveredBytes  = 32 << 20 // a client that reads more than this was fed its backlog
)

// A client that stopped reading has a backlog in the daemon's send channel and
// in both kernels. When the hub gives up on it, none of that may be paid out
// first: the connection must end now, not after the daemon has spent the whole
// queue on it. Before the fix the write pump drained every queued message into
// the socket and only then closed, which is what made an eviction take as long
// as the backlog it was caused by.
func TestEvictedClientIsNotFedItsBacklogFirst(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	evicted := make(chan struct{}, 1)
	d.wsHub.logf = func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log("hub: " + line)
		if strings.Contains(line, "too slow") {
			select {
			case evicted <- struct{}{}:
			default:
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Connected, identified, and then silent: it never reads, so the daemon's
	// writes stall as soon as the kernels are full.
	stalled := dialDaemonWSAs(t, ctx, "127.0.0.1:"+wsPort, "stalled-client")
	defer stalled.Close(websocket.StatusNormalClosure, "")

	stopFlood := floodBroadcasts(d, stalledClientMessageBytes, time.Millisecond)
	select {
	case <-evicted:
	case <-ctx.Done():
		stopFlood()
		t.Fatal("the hub never evicted the stalled client")
	}
	stopFlood()

	// Read it out. What arrives is what the client's kernel already held; what
	// must not arrive is the queue the daemon was holding for it.
	readCtx, cancelRead := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelRead()
	start := time.Now()
	delivered := 0
	for {
		_, data, err := stalled.Read(readCtx)
		if err != nil {
			t.Logf("stalled client's read ended after %s having read %d bytes: %v",
				time.Since(start).Round(time.Millisecond), delivered, err)
			break
		}
		delivered += len(data)
		if delivered > maxBacklogDeliveredBytes {
			t.Fatalf(
				"evicted client was fed %d bytes of backlog (cap %d); the daemon is draining its queue into a connection it already gave up on",
				delivered, maxBacklogDeliveredBytes,
			)
		}
	}
	if readCtx.Err() != nil {
		t.Fatalf("the evicted client was still connected %s after the eviction", evictionDeathBudget)
	}
}

// An evicted connection cannot be told why it was evicted — the close frame
// queues behind the backlog that caused it. The daemon therefore remembers, and
// the client's next hello collects the answer. Over loopback here; the degraded
// link this exists for is covered in the toxiproxy test.
func TestEvictedClientLearnsWhyOnItsNextConnection(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	d := NewForTesting(filepath.Join(tmpDir, "test.sock"))
	go d.Start()
	defer d.Stop()
	waitForSocket(t, filepath.Join(tmpDir, "test.sock"), 5*time.Second)
	waitForRecovery(t, d)

	addr := "127.0.0.1:" + wsPort
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const clientID = "app-instance-1"
	first := dialDaemonWSAs(t, ctx, addr, clientID)
	defer first.Close(websocket.StatusNormalClosure, "")

	// Wait for the hello to land before evicting: the id is what the eviction is
	// filed under, and it exists only once the daemon has read it.
	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == clientID {
				found = true
			}
		})
		return found
	})

	// Evict it exactly as the fan-out does when a client misses maxSlowCount
	// broadcasts: drop it from the hub, then hang up.
	d.wsHub.mu.Lock()
	for client := range d.wsHub.clients {
		if client.ClientID() != clientID {
			continue
		}
		client.slowCount = maxSlowCount
		delete(d.wsHub.clients, client)
		d.wsHub.evict(client, slowClientCloseReason)
	}
	d.wsHub.mu.Unlock()

	// The connection ends without waiting for anything to drain.
	deathCtx, cancelDeath := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelDeath()
	if err := readUntilClosed(deathCtx, first); err == nil {
		t.Fatal("evicted client's read succeeded; want the connection to end")
	} else if deathCtx.Err() != nil {
		t.Fatalf("evicted client still connected after %s", evictionDeathBudget)
	}

	// Back with the same id, and the daemon volunteers what happened.
	second := dialDaemonWSAs(t, ctx, addr, clientID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < float64(maxSlowCount) {
		t.Errorf("eviction notice undelivered_messages = %v, want at least %d", notice["undelivered_messages"], maxSlowCount)
	}

	// Told once. A client that keeps reconnecting is not re-told about a
	// disconnect it has already been shown.
	if _, ok := d.wsHub.takeEviction(clientID); ok {
		t.Error("the eviction is still on file after being delivered")
	}
}

// The hub's slow-count rule is not how a real client dies. A client that has
// stopped draining takes one large snapshot straight past the write deadline
// long before 256 more messages pile up behind it, so the write pump gives up
// first. That is an eviction by any other name, and it has to be filed like
// one, or the client that comes back is the only one who never finds out why it
// went away.
//
// This is the shape the live app hit: a frozen socket, a snapshot the pump sat
// on for the whole deadline, and a hub whose slow-count never reached 2. The
// connection ending is the library's own doing — asserted here because the
// notice is worthless if the client never gets disconnected in the first place.
func TestWriteStallEndsTheConnectionAndIsRemembered(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	d.wsWriteTimeout = 300 * time.Millisecond
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	var logMu sync.Mutex
	var hubLog []string
	d.wsHub.logf = func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log("hub: " + line)
		logMu.Lock()
		hubLog = append(hubLog, line)
		logMu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const clientID = "stalled-app"
	addr := "127.0.0.1:" + wsPort
	stalled := dialDaemonWSAs(t, ctx, addr, clientID)
	defer stalled.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == clientID {
				found = true
			}
		})
		return found
	})

	// One message far larger than anything the two kernels will hold for a
	// client that never reads it, so the pump blocks on the socket itself. The
	// client stays silent throughout: reading is what it cannot do.
	d.wsHub.BroadcastRawText([]byte(strings.Repeat("x", 8<<20)))
	waitForCond(t, evictionDeathBudget, "the daemon to drop the stalled client", func() bool {
		return d.wsHub.ClientCount() == 0
	})

	// Only now does it read, and all that is left for it is the end.
	deathCtx, cancelDeath := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelDeath()
	if err := readUntilClosed(deathCtx, stalled); err == nil {
		t.Fatal("stalled client's read succeeded; want the connection to end")
	} else if deathCtx.Err() != nil {
		t.Fatalf("stalled client still connected %s after the write deadline", evictionDeathBudget)
	}

	// The hub never got the chance to count this client slow — which is exactly
	// why the write pump has to file the eviction itself.
	logMu.Lock()
	for _, line := range hubLog {
		if strings.Contains(line, "too slow") || strings.Contains(line, "client slow") {
			t.Errorf("the hub's slow-count fired (%q); this test no longer covers the write-stall path", line)
		}
	}
	logMu.Unlock()

	second := dialDaemonWSAs(t, ctx, addr, clientID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < 1 {
		t.Errorf("eviction notice undelivered_messages = %v, want at least 1", notice["undelivered_messages"])
	}
}

// The keepalive is the exit a stalled app actually takes — measured live, the
// unanswered ping beat both the slow-count and the write deadline to it. So it
// has to file the disconnect like an eviction, and it has to know when not to:
// an unanswered ping with nothing owed is a connection that died, and a client
// that comes back from that has nothing to be told.
func TestUnansweredPingIsAnEvictionOnlyWhenTheDaemonOwesTheClient(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	d.wsPingInterval, d.wsPingTimeout = 200*time.Millisecond, 200*time.Millisecond
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	addr := "127.0.0.1:" + wsPort
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A client that answers nothing and is owed nothing. Its ping goes
	// unanswered within the shrunk timeout and the daemon drops it — promptly,
	// which is the second half of this: closing a WebSocket means waiting out a
	// close handshake, and a peer that has stopped answering pings will not
	// answer that either. Budget: 200ms to the ping, 200ms to give up on it, up
	// to a second of grace. The same client held on for 5.4s when the daemon
	// waited out the handshake.
	const pingDeathBudget = 2 * time.Second
	const quietID = "went-away"
	quiet := dialDaemonWSAs(t, ctx, addr, quietID)
	defer quiet.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, pingDeathBudget, "the daemon to drop the silent client", func() bool {
		return d.wsHub.ClientCount() == 0
	})
	if record, ok := d.wsHub.takeEviction(quietID); ok {
		t.Errorf("a connection that died owing nothing was filed as an eviction: %+v", record)
	}

	// A client the daemon is mid-message to when the ping goes unanswered. Same
	// silence on the wire, a different fact about the client.
	const stalledID = "fell-behind"
	stalled := dialDaemonWSAs(t, ctx, addr, stalledID)
	defer stalled.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == stalledID {
				found = true
			}
		})
		return found
	})
	d.wsHub.BroadcastRawText([]byte(strings.Repeat("x", 8<<20)))
	waitForCond(t, pingDeathBudget, "the daemon to drop the stalled client", func() bool {
		return d.wsHub.ClientCount() == 0
	})

	// Back to the shipped keepalive before reconnecting: a 200ms pong deadline
	// is a trap for a healthy client too, and this leg is about what the daemon
	// says, not how fast it pings. The ping loops of the two dropped clients
	// captured their pacing at start, so this write races nothing.
	d.wsPingInterval, d.wsPingTimeout = 0, 0
	second := dialDaemonWSAs(t, ctx, addr, stalledID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
}

// The eviction memory is a small bounded map with no owner watching it: what
// keeps it honest is that records expire, are handed over once, and that a
// flood of unknown clients cannot grow it without bound.
func TestEvictionMemoryForgetsWhatItShould(t *testing.T) {
	h := newWSHub()
	logged := 0
	h.logf = func(string, ...interface{}) { logged++ }

	now := time.Now()

	t.Run("a record older than the TTL is gone", func(t *testing.T) {
		h.rememberEviction("stale", evictionRecord{at: now.Add(-evictionMemoryTTL - time.Minute)})
		if _, ok := h.takeEviction("stale"); ok {
			t.Error("an eviction older than the TTL was still delivered")
		}
	})

	t.Run("a client that never named itself files nothing", func(t *testing.T) {
		h.rememberEviction("", evictionRecord{at: now})
		if _, ok := h.takeEviction(""); ok {
			t.Error("an empty client id matched a record")
		}
	})

	t.Run("the map is bounded, and says so when it drops one", func(t *testing.T) {
		before := logged
		for i := 0; i < maxRememberedEvictions*2; i++ {
			h.rememberEviction(
				"client-"+time.Duration(i).String(),
				evictionRecord{at: now.Add(time.Duration(i) * time.Second), reason: slowClientCloseReason},
			)
		}
		h.evictionMu.Lock()
		held := len(h.evictions)
		h.evictionMu.Unlock()
		if held > maxRememberedEvictions {
			t.Errorf("eviction memory holds %d records, over the %d cap", held, maxRememberedEvictions)
		}
		if logged == before {
			t.Error("records were dropped silently; a notice nobody will receive must be logged")
		}
	})
}
