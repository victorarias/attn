package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

// toxiProxy is an in-process Toxiproxy sitting between a test WebSocket client
// and the daemon's real listener, so a test can degrade one connection's network
// without touching the daemon or the other clients on it.
//
// It runs embedded: no external toxiproxy binary and no HTTP control API. The
// ApiServer exists only because NewProxy needs one for its logger and metrics
// registry; nothing listens on it.
type toxiProxy struct {
	t     *testing.T
	proxy *toxiproxy.Proxy
	addr  string
}

// newToxiProxy starts a proxy forwarding its own free port to upstream, stopped
// on cleanup.
func newToxiProxy(t *testing.T, upstream string) *toxiProxy {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate toxiproxy port: %v", err)
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)

	// Both loggers, not just the server's: the proxy carries its own, and it
	// traces every toxic interruption to stdout if left at its default.
	silent := zerolog.New(io.Discard)
	api := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), silent)
	p := toxiproxy.NewProxy(api, "attn-ws", listen, upstream)
	p.Logger = &silent
	if err := p.Start(); err != nil {
		t.Fatalf("start toxiproxy on %s: %v", listen, err)
	}
	t.Cleanup(p.Stop)
	return &toxiProxy{t: t, proxy: p, addr: listen}
}

// throttleDownstream caps daemon→client throughput, which is what makes the
// daemon's writes to this client back up. Downstream is the direction that
// matters: the client barely sends anything.
func (p *toxiProxy) throttleDownstream(name string, rateKBPerSec int64) {
	p.t.Helper()
	spec := fmt.Sprintf(
		`{"name":%q,"type":"bandwidth","stream":"downstream","toxicity":1.0,"attributes":{"rate":%d}}`,
		name, rateKBPerSec,
	)
	if _, err := p.proxy.Toxics.AddToxicJson(strings.NewReader(spec)); err != nil {
		p.t.Fatalf("add bandwidth toxic %s: %v", name, err)
	}
}

// healDownstream removes a toxic, restoring the link to a clean network.
func (p *toxiProxy) healDownstream(name string) {
	p.t.Helper()
	if err := p.proxy.Toxics.RemoveToxic(context.Background(), name); err != nil {
		p.t.Fatalf("remove toxic %s: %v", name, err)
	}
}

// Eviction sizing. The flood has to sit between two rates: fast enough to
// outrun the throttled link and fill that client's 256-message buffer, slow
// enough that the healthy client on loopback never misses maxSlowCount sends in
// a row and gets evicted alongside it. An unpaced flood does exactly that — the
// hub's fan-out outruns any client, degraded link or not — so the rate is the
// experiment, not a detail.
//
// The receipt, at 4 KB a message:
//   - throttled link drains 10 KB/s ⇒ 2.5 messages/s
//   - the flood offers 200 messages/s ⇒ 256 slots fill in ~1.3s
//   - the healthy client is offered 800 KB/s over loopback, which is three
//     orders of magnitude under what it sustains, so its slowCount keeps
//     resetting
//   - one 4 KB write over the throttled link takes 0.4s, well inside the ten
//     seconds wsWritePump allows a single conn.Write, so the eviction is what
//     ends that connection rather than a write timeout
const (
	slowLinkRateKBPerSec = 10
	floodMessageBytes    = 4 << 10
	floodInterval        = 5 * time.Millisecond
)

// evictionDeathBudget is how long an evicted client on a working transport may
// keep believing it is connected. The daemon offers the close frame
// evictionCloseGrace (1s) and then aborts the socket, so five seconds is a
// tripwire: a working eviction lands in milliseconds.
const evictionDeathBudget = 5 * time.Second

// The hub evicts a client that misses maxSlowCount consecutive broadcasts. The
// interesting part is not that it decides to — it is what the client on the
// other end of the degraded link learns, and when.
//
// It cannot be told over that connection. A WebSocket close frame is ordinary
// stream data, queued behind everything already handed to the pipe, and the
// pipe is slow by definition here. Nor can the daemon get ahead of that queue
// by force. Measured through this proxy: the daemon aborts its socket with
// SO_LINGER 0 one second after the eviction, and the throttled client still
// reads for another 65 seconds and then sees a plain EOF — because the bytes
// are no longer in either kernel, they are inside the proxy, and a userspace
// hop forwards no reset. (Directly between two kernels the same abort lands
// instantly: the client reads what its receive buffer already held and then
// gets ECONNRESET, 0ms after the abort — that case is
// TestEvictedClientIsNotFedItsBacklogFirst.)
//
// So what this test pins is the answer that does arrive: the daemon remembers
// the eviction, and hands the reason to the same client on its next connection.
func TestWebSocketSlowClientIsEvictedOverADegradedLink(t *testing.T) {
	wsPort := useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	// The hub announces the eviction on its own log. That line is the signal this
	// test waits on — no polling, no sleeping until something probably happened.
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

	daemonAddr := "127.0.0.1:" + wsPort
	proxy := newToxiProxy(t, daemonAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The control: a client on the real listener, draining continuously. It must
	// be unaffected by what happens to its neighbour on the same hub.
	healthy := dialDaemonWS(t, ctx, daemonAddr)
	defer healthy.Close(websocket.StatusNormalClosure, "")
	healthyReads := drainWS(ctx, healthy)

	// The subject: the same daemon, reached over a link we are about to throttle.
	// It never reads, so nothing drains its socket either. It names itself, which
	// is what lets the daemon explain the eviction when it comes back.
	const slowClientID = "slow-client-under-test"
	slow := dialDaemonWSAs(t, ctx, proxy.addr, slowClientID)
	defer slow.Close(websocket.StatusNormalClosure, "")
	proxy.throttleDownstream("molasses", slowLinkRateKBPerSec)

	stopFlood := floodBroadcasts(d, floodMessageBytes, floodInterval)
	select {
	case <-evicted:
	case <-ctx.Done():
		stopFlood()
		t.Fatal("the hub never evicted the throttled client")
	}
	stopFlood()

	// The hub dropped it, and kept the healthy one.
	d.wsHub.mu.RLock()
	remaining := len(d.wsHub.clients)
	d.wsHub.mu.RUnlock()
	if remaining != 1 {
		t.Fatalf("hub holds %d clients after the eviction, want 1 (the healthy one)", remaining)
	}

	// The eviction is per-client. The healthy client shares the hub, the
	// broadcast, and the wsHub.mu the eviction runs under.
	if err := healthyReads.err(); err != nil {
		t.Fatalf("healthy client was disturbed by the eviction: %v", err)
	}
	before := healthyReads.count()
	if before == 0 {
		t.Fatal("healthy client received nothing; the flood never reached it")
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventInitialState})
	waitForCond(t, 10*time.Second, "the healthy client to keep receiving", func() bool {
		return healthyReads.count() > before
	})

	// Recovery: over the healed link, a client on the same proxied path is an
	// ordinary client again — and the first thing it hears is why it lost the
	// last one. That reason could not travel the connection it describes; this
	// is the path that carries it.
	proxy.healDownstream("molasses")
	recovered := dialDaemonWSAs(t, ctx, proxy.addr, slowClientID)
	defer recovered.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, recovered, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < 1 {
		t.Errorf("eviction notice undelivered_messages = %v, want at least 1", notice["undelivered_messages"])
	}
	if _, err := time.Parse(time.RFC3339, asString(notice["evicted_at"])); err != nil {
		t.Errorf("eviction notice evicted_at = %q, want RFC3339: %v", notice["evicted_at"], err)
	}

	recoveredReads := drainWS(ctx, recovered)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventInitialState})
	waitForCond(t, 10*time.Second, "the reconnected client to receive", func() bool {
		return recoveredReads.count() > 0
	})
	if err := recoveredReads.err(); err != nil {
		t.Fatalf("reconnected client failed over the healed link: %v", err)
	}
}

// dialDaemonWSAs connects and identifies itself with a client id, which is how a
// client that reconnects can be recognised as the one that was evicted.
func dialDaemonWSAs(t *testing.T, ctx context.Context, addr, clientID string) *websocket.Conn {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.SetReadLimit(64 << 20)
	sendClientHelloAs(t, conn, clientID)
	return conn
}

// readEventUntil reads past whatever else the daemon is sending until the named
// event arrives, and fails if the connection ends first.
func readEventUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, event string) map[string]interface{} {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", event, err)
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // binary frames and anything unparsed are not what we are after
		}
		if asString(msg["event"]) == event {
			return msg
		}
	}
}

// dialDaemonWS connects to a daemon WebSocket listener — directly or through the
// proxy — and completes the handshake the daemon expects.
func dialDaemonWS(t *testing.T, ctx context.Context, addr string) *websocket.Conn {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.SetReadLimit(64 << 20)
	sendWorkspaceClientHello(t, conn)
	return conn
}

// wsReads is a running tally of what a draining client received, and the error
// that ended its read loop.
type wsReads struct {
	reads chan struct{}
	fail  chan error
	n     int
	e     error
}

func (r *wsReads) settle() {
	for {
		select {
		case <-r.reads:
			r.n++
		case err := <-r.fail:
			if r.e == nil {
				r.e = err
			}
		default:
			return
		}
	}
}

func (r *wsReads) count() int { r.settle(); return r.n }
func (r *wsReads) err() error { r.settle(); return r.e }

// drainWS reads from conn continuously, which is what makes a client healthy:
// its socket never backs up, so the daemon's writes to it never stall.
func drainWS(ctx context.Context, conn *websocket.Conn) *wsReads {
	r := &wsReads{
		reads: make(chan struct{}, 1<<16),
		fail:  make(chan error, 1),
	}
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				select {
				case r.fail <- err:
				default:
				}
				return
			}
			select {
			case r.reads <- struct{}{}:
			default:
			}
		}
	}()
	return r
}

// floodBroadcasts pushes sized messages through the hub's fan-out at a fixed
// rate until the returned func is called. It writes to the hub's broadcast
// channel rather than through Broadcast so the two things that decide whether a
// link keeps up — message size and offered rate — are the test's to set.
func floodBroadcasts(d *Daemon, payloadBytes int, every time.Duration) func() {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	payload := []byte(strings.Repeat("x", payloadBytes))
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case d.wsHub.broadcast <- outboundMessage{kind: messageKindText, payload: payload}:
				case <-stop:
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}

// readUntilClosed reads from conn until the read fails, returning the error that
// ended it — the daemon's close status, when the daemon is the one that hung up.
func readUntilClosed(ctx context.Context, conn *websocket.Conn) error {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return err
		}
	}
}

// waitForCond polls cond until it holds, failing the test if it never does.
func waitForCond(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
