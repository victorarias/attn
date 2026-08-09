package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
)

// jsonrpcPeer is the transport half of a connection to a supervised child:
// newline-delimited JSON-RPC 2.0, full duplex, over the daemon's unix socket.
//
// Two children speak it — a plugin (internal/daemon/plugin_rpc.go) and the shared
// app runtime (app_runtime_rpc.go) — and they share this rather than each owning
// a copy, because the parts that are easy to get subtly wrong are exactly the
// parts that are identical: id correlation, unblocking in-flight callers when the
// socket dies, and serializing writes against a reader on another goroutine.
//
// What is NOT here is everything that differs: who the child is, how it is
// authenticated, which methods it may call, and what happens when it disconnects.
// Those belong to each child's own file.
//
// Two rules the callers depend on:
//
//   - Ids are per-direction. The daemon numbers its own requests; the child
//     numbers its. The two spaces may collide harmlessly, because a frame with a
//     method is a call and a frame without one is an answer.
//   - Correlation is by the id's raw JSON text, so a child must echo the id
//     verbatim. Answering `"1"` to a request with id `1` does not match.
type jsonrpcPeer struct {
	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan jsonRPCMessage
	nextID    uint64
	closed    bool
}

func newJSONRPCPeer(conn net.Conn, reader *bufio.Reader) *jsonrpcPeer {
	return &jsonrpcPeer{
		conn:    conn,
		reader:  reader,
		pending: make(map[string]chan jsonRPCMessage),
	}
}

func (p *jsonrpcPeer) send(msg jsonRPCMessage) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return json.NewEncoder(p.conn).Encode(msg)
}

// closePending fails every in-flight request. Without it a caller waiting on a
// child that just died would wait out its whole context rather than learning the
// connection is gone.
func (p *jsonrpcPeer) closePending(err error) {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for key, ch := range p.pending {
		delete(p.pending, key)
		ch <- jsonRPCMessage{
			Error: &jsonRPCError{Code: jsonRPCInternalError, Message: err.Error()},
		}
	}
}

// routeResponse hands an answer to the caller waiting for it, reporting whether
// anyone was. A response nobody is waiting for is the caller's context having
// expired first, which the caller has already handled.
func (p *jsonrpcPeer) routeResponse(msg jsonRPCMessage) bool {
	key := jsonRPCIDKey(msg.ID)
	if key == "" {
		return false
	}

	p.pendingMu.Lock()
	ch, exists := p.pending[key]
	if exists {
		delete(p.pending, key)
	}
	p.pendingMu.Unlock()
	if !exists {
		return false
	}
	ch <- msg
	return true
}

// request calls the child and waits for its answer or the context, whichever
// comes first. `label` names the child in the error text ("plugin", "app
// runtime"), because the caller of a failed call is usually not near enough to
// the connection to know which one answered.
func (p *jsonrpcPeer) request(ctx context.Context, label, method string, params interface{}, result interface{}) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s request params: %w", label, err)
	}

	p.pendingMu.Lock()
	if p.closed {
		p.pendingMu.Unlock()
		return fmt.Errorf("%s connection is closed", label)
	}
	p.nextID++
	id := strconv.FormatUint(p.nextID, 10)
	responseCh := make(chan jsonRPCMessage, 1)
	p.pending[id] = responseCh
	p.pendingMu.Unlock()

	request := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  payload,
	}
	if err := p.send(request); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return fmt.Errorf("send %s request: %w", label, err)
	}

	select {
	case <-ctx.Done():
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return ctx.Err()
	case response := <-responseCh:
		if response.Error != nil {
			return fmt.Errorf("%s %s: %s", label, method, response.Error.Message)
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("%s %s returned no result", label, method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s %s result: %w", label, method, err)
		}
		return nil
	}
}
