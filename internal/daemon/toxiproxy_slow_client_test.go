package daemon

import (
	"context"
	"errors"
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

// The hub evicts a client that misses maxSlowCount consecutive broadcasts,
// closing it with StatusPolicyViolation "client too slow". Nothing exercised
// that path over a real degraded connection — only by filling the buffer
// directly — so nothing showed what a client on a slow network actually
// experiences.
//
// What it experiences, as this test pins down: the daemon hangs up promptly,
// but the close frame explaining why is queued behind everything already
// written to that socket. On a link slow enough to trigger the eviction, the
// reason is slow to arrive for exactly the same reason the eviction happened.
// The client sees the disconnect; it learns the cause only once the link
// recovers.
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
	// It never reads, so nothing drains its socket either.
	slow := dialDaemonWS(t, ctx, proxy.addr)
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

	// Heal the link and the queued bytes drain at full speed, close frame
	// included. Only now can the evicted client read the reason it was dropped.
	proxy.healDownstream("molasses")
	closeErr := readUntilClosed(ctx, slow)
	var status websocket.CloseError
	if !errors.As(closeErr, &status) {
		t.Fatalf("evicted client ended with %v, want a WebSocket close", closeErr)
	}
	if status.Code != websocket.StatusPolicyViolation {
		t.Fatalf("evicted client closed with %v (%q), want StatusPolicyViolation", status.Code, status.Reason)
	}
	if status.Reason != "client too slow" {
		t.Errorf("close reason = %q, want %q", status.Reason, "client too slow")
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
	// ordinary client again.
	recovered := dialDaemonWS(t, ctx, proxy.addr)
	defer recovered.Close(websocket.StatusNormalClosure, "")
	recoveredReads := drainWS(ctx, recovered)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventInitialState})
	waitForCond(t, 10*time.Second, "the reconnected client to receive", func() bool {
		return recoveredReads.count() > 0
	})
	if err := recoveredReads.err(); err != nil {
		t.Fatalf("reconnected client failed over the healed link: %v", err)
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
