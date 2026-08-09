package daemon

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

// The daemon gives up on a client it cannot keep fed in two places: the hub,
// when a client misses maxSlowCount consecutive broadcasts, and the write pump,
// when a single hand-off runs past wsWriteTimeout. Either way two things have to
// happen, and they need different mechanisms:
//
//   - The client has to notice, fast. It cannot be told over the connection it
//     is being evicted from: the WebSocket close frame travels the same TCP
//     stream as the backlog that caused the eviction, behind everything already
//     handed to the kernel. Measured through a 10 KB/s link, the daemon hung up
//     after ~1s and the client read nothing for ~45s — its own timeout, not the
//     daemon's answer. So the close frame is attempted, briefly, and then the
//     transport is aborted. (Only the hub needs this: a write that runs out of
//     time leaves the library tearing the transport down on its own.)
//   - The client has to learn why, eventually. That reason arrives on the next
//     connection instead (client_eviction_notice), keyed on the client_id the
//     client repeats across reconnects.
const (
	// evictionCloseGrace bounds how long a hopeless connection is given to
	// accept a close frame before the transport is aborted underneath it. It is
	// a tripwire, not a budget: a client whose socket is writable takes
	// microseconds, and one that needs a full second is by definition the case
	// this exists for.
	evictionCloseGrace = 1 * time.Second
	// evictionMemoryTTL is how long the hub remembers an eviction for a client
	// that has not come back. The app reconnects on a backoff that caps at 5s
	// and a circuit breaker that resets after 30s, so ten minutes is two orders
	// of magnitude past the slowest return that is still the same visit.
	evictionMemoryTTL = 10 * time.Minute
	// maxRememberedEvictions bounds the map. One user, a handful of clients: a
	// number this far past the real fleet is only ever reached by a client that
	// evicts, reconnects with a fresh id, and evicts again — and dropping the
	// oldest of those is the right answer anyway.
	maxRememberedEvictions = 16
)

// evictionRecord is what the hub remembers about a client it hung up on, held
// until that client comes back or the memory ages out.
type evictionRecord struct {
	at     time.Time
	reason string
	// undelivered counts the messages the client never got: the ones the hub
	// could not enqueue, or the one the write pump could not hand over plus
	// whatever was still queued behind it.
	undelivered int
}

// rememberEviction files an eviction under the client's id so the next hello
// from that id can be answered with it. A client that never sent an id cannot
// be told anything on its return, so the log line is all there is.
func (h *wsHub) rememberEviction(clientID string, record evictionRecord) {
	if clientID == "" {
		return
	}
	h.evictionMu.Lock()
	defer h.evictionMu.Unlock()
	if h.evictions == nil {
		h.evictions = make(map[string]evictionRecord)
	}
	h.pruneEvictionsLocked(record.at)
	if len(h.evictions) >= maxRememberedEvictions {
		oldestID, oldest := "", time.Time{}
		for id, rec := range h.evictions {
			if oldest.IsZero() || rec.at.Before(oldest) {
				oldestID, oldest = id, rec.at
			}
		}
		h.logf(
			"eviction memory full (%d records); dropping the notice for client %s so client %s can be remembered",
			maxRememberedEvictions, oldestID, clientID,
		)
		delete(h.evictions, oldestID)
	}
	h.evictions[clientID] = record
}

// takeEviction returns and forgets the eviction filed for this client id, if
// there is one. Delivered once: a client that has been told does not need
// telling again on its next reconnect.
func (h *wsHub) takeEviction(clientID string) (evictionRecord, bool) {
	if clientID == "" {
		return evictionRecord{}, false
	}
	h.evictionMu.Lock()
	defer h.evictionMu.Unlock()
	h.pruneEvictionsLocked(time.Now())
	record, ok := h.evictions[clientID]
	if ok {
		delete(h.evictions, clientID)
	}
	return record, ok
}

func (h *wsHub) pruneEvictionsLocked(now time.Time) {
	for id, rec := range h.evictions {
		if now.Sub(rec.at) > evictionMemoryTTL {
			delete(h.evictions, id)
		}
	}
}

// evict drops a client the hub has given up on: it files the reason for the
// client's return, stops feeding a connection nobody is reading, and hangs up
// without waiting for the backlog to drain.
//
// Callers hold h.mu, so the hanging up runs on its own goroutine — closing a
// wedged socket must never stall the fan-out for every other client.
func (h *wsHub) evict(client *wsClient, reason string) {
	record := evictionRecord{
		at:          time.Now(),
		reason:      reason,
		undelivered: client.slowCount + len(client.send),
	}
	h.rememberEviction(client.ClientID(), record)
	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, reason)
	go client.hangUp(websocket.StatusPolicyViolation, reason, evictionCloseGrace)
}

// hangUp ends a connection the daemon has given up on, within grace.
//
// The close frame is attempted first and is pure courtesy: on a healthy socket
// it lands immediately and the client gets a proper status, which is why it is
// worth trying at all. On the socket this exists for it cannot land, because a
// close frame is ordinary stream data queued behind the backlog. So the
// transport is aborted afterwards: SO_LINGER 0 discards the daemon's unsent
// bytes and sends a RST, which is the only thing that reaches the peer without
// waiting in that queue.
func (c *wsClient) hangUp(code websocket.StatusCode, reason string, grace time.Duration) {
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = c.conn.Close(code, reason)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
	}
	c.abortTransport()
}

// abortTransport tears the connection down at the socket. The raw conn is the
// one the HTTP server accepted (stashed by withRawConn); without it — a test
// server, or any listener that does not run through initHTTPServer — this
// degrades to nhooyr's own immediate close, which still skips the close
// handshake but leaves the kernel to flush what it has queued.
func (c *wsClient) abortTransport() {
	if c.rawConn == nil {
		_ = c.conn.CloseNow()
		return
	}
	if tcp, ok := c.rawConn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.rawConn.Close()
}

// rawConnKey carries the accepted connection from the HTTP server down to the
// handler, which is the only way to reach it: the WebSocket handshake hijacks
// the connection and hands back a wrapper with no way out to the socket.
type rawConnKey struct{}

func withRawConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, rawConnKey{}, conn)
}

func rawConnFrom(ctx context.Context) net.Conn {
	conn, _ := ctx.Value(rawConnKey{}).(net.Conn)
	return conn
}

// sendEvictionNotice tells a returning client what happened to its last
// connection. Sent from the hello handler, which is where the client names
// itself; a client that has nothing waiting for it hears nothing, so hello
// stays fire-and-forget for everyone else.
func (d *Daemon) sendEvictionNotice(client *wsClient, record evictionRecord) {
	notice := &protocol.ClientEvictionNoticeMessage{
		Event:               protocol.EventClientEvictionNotice,
		EvictedAt:           record.at.Format(time.RFC3339),
		Reason:              record.reason,
		UndeliveredMessages: record.undelivered,
	}
	data, err := json.Marshal(notice)
	if err != nil {
		d.logf("eviction notice marshal error: %v", err)
		return
	}
	d.logf(
		"telling client %s it was evicted at %s (%s)",
		client.ClientID(), notice.EvictedAt, record.reason,
	)
	_ = d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
}
