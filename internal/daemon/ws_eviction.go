package daemon

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

// Eviction happens in the hub (maxSlowCount missed broadcasts) and the write
// pump (one hand-off past wsWriteTimeout). The close frame travels the same TCP
// stream as the backlog, so a wedged client never reads it — Measured: through a
// 10 KB/s link the daemon hung up after ~1s and the client read nothing for
// ~45s. So: attempt the close briefly, abort the transport, and deliver the
// reason on the next connection (client_eviction_notice), keyed on client_id.
const (
	// evictionCloseGrace bounds the close-frame attempt before the transport is
	// aborted. Tripwire: a writable socket takes microseconds.
	evictionCloseGrace = 1 * time.Second
	// evictionMemoryTTL: reconnect backoff caps at 5s and the circuit breaker
	// resets at 30s, so this is two orders of magnitude past a same-visit return.
	evictionMemoryTTL = 10 * time.Minute
	// maxRememberedEvictions bounds the map; only a client cycling fresh ids
	// reaches it, and dropping the oldest of those is right anyway.
	maxRememberedEvictions = 16
)

// evictionRecord is what the hub remembers about a client it hung up on.
type evictionRecord struct {
	at     time.Time
	reason string
	// undelivered counts messages the client never got.
	undelivered int
}

// rememberEviction files an eviction under the client's id so the next hello
// from that id can be answered with it; an id-less client only gets the log line.
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

// takeEviction returns and forgets the eviction filed for this client id;
// delivered once.
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

// evict drops a client the hub has given up on and files the reason for its
// return. Callers hold h.mu, so the hang-up runs on its own goroutine — closing
// a wedged socket must never stall the fan-out.
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

// hangUp attempts the close frame (lands only on a healthy socket), then aborts
// the transport: SO_LINGER 0 discards unsent bytes and sends a RST — the only
// thing that reaches the peer without queuing behind the backlog.
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

// abortTransport tears the connection down at the socket. Without the raw conn
// (test servers, listeners outside initHTTPServer) this degrades to nhooyr's
// CloseNow, which skips the handshake but lets the kernel flush its queue.
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

// rawConnKey carries the accepted connection down to the handler — the only way
// to it: the WebSocket handshake hijacks the conn behind a wrapper.
type rawConnKey struct{}

func withRawConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, rawConnKey{}, conn)
}

func rawConnFrom(ctx context.Context) net.Conn {
	conn, _ := ctx.Value(rawConnKey{}).(net.Conn)
	return conn
}

// sendEvictionNotice tells a returning client what happened to its last
// connection; sent from the hello handler, where the client names itself.
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
