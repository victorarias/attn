package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// pluginFixturePeer is the plugin side of the daemon socket, as the end-to-end
// driver fixture speaks it.
//
// attn is a full-duplex JSON-RPC peer: it sends driver.session_closed the
// instant a session's PTY exits, which can land while one of the plugin's own
// reports is still waiting for its response — the daemon answers a report on
// its read loop after broadcasting the state change, so any pause between
// those two steps lets a close overtake the answer. Every message therefore
// arrives at one reader that routes by shape: a message carrying a method is a
// request to handle, a message carrying only an id is the response someone is
// waiting for. The shipped SDK (sdk/plugin) dispatches the same way. A peer
// that assumed its response came next would instead lose whichever message
// attn sent first, and because the ordering is a scheduling race it would lose
// it rarely — which is what this fixture used to do.
type pluginFixturePeer struct {
	t       *testing.T
	conn    net.Conn
	decoder *json.Decoder
	nextID  int
}

// newPluginFixturePeer takes over reading conn. Nothing else may read from it:
// the peer is the only place that knows which messages are responses it is
// waiting for and which are requests to answer.
func newPluginFixturePeer(t *testing.T, conn net.Conn) *pluginFixturePeer {
	// Ids 1 and 2 belong to the hello and driver.register handshake the fixture
	// sends before serving, so the first generated id starts after them.
	return &pluginFixturePeer{t: t, conn: conn, decoder: json.NewDecoder(conn), nextID: 2}
}

// call sends a request and returns its response, answering every request attn
// sends while that response is outstanding.
func (p *pluginFixturePeer) call(method string, params interface{}) jsonRPCMessage {
	p.t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		p.t.Fatalf("marshal %s params: %v", method, err)
	}
	p.nextID++
	id := strconv.Itoa(p.nextID)
	if err := p.write(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  payload,
	}); err != nil {
		p.t.Fatalf("send %s: %v", method, err)
	}
	return p.awaitResponse(id)
}

// callOK is call for the requests whose only interesting outcome is that attn
// accepted them.
func (p *pluginFixturePeer) callOK(method string, params interface{}) {
	p.t.Helper()
	if response := p.call(method, params); response.Error != nil {
		p.t.Fatalf("%s error=%#v", method, response.Error)
	}
}

// awaitResponse returns the response to a request already on the wire.
func (p *pluginFixturePeer) awaitResponse(id string) jsonRPCMessage {
	p.t.Helper()
	for {
		message, err := p.read()
		if err != nil {
			p.t.Fatalf("read response id=%s: %v", id, err)
		}
		if message.Method != "" {
			p.handle(message)
			continue
		}
		if jsonRPCIDKey(message.ID) != id {
			p.t.Fatalf("plugin response id=%s while waiting for id=%s", jsonRPCIDKey(message.ID), id)
		}
		return message
	}
}

// serve answers attn's requests until it closes the connection.
func (p *pluginFixturePeer) serve() {
	for {
		message, err := p.read()
		if err != nil {
			return
		}
		if message.Method == "" {
			continue
		}
		p.handle(message)
	}
}

func (p *pluginFixturePeer) handle(request jsonRPCMessage) {
	p.t.Helper()
	switch request.Method {
	case pluginHealthMethod:
		p.respond(request.ID, pluginHealthResult{OK: true})
	case "driver.session_closed":
		var params pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			p.t.Fatalf("decode fixture session close params: %v", err)
		}
		appendPluginFixtureCloseRecord(p.t, pluginDriverCloseRecord{Params: params})
		p.respond(request.ID, pluginDriverSessionClosedResult{OK: true})
	case "driver.spawn", "driver.resume":
		p.launch(request)
	default:
		// Answering instead of ignoring: an unanswered request holds the daemon's
		// caller for its full 30s timeout and says nothing about why.
		_ = p.write(jsonRPCFailure(request.ID, jsonRPCMethodNotFound, fmt.Sprintf("unknown method %q", request.Method)))
	}
}

// launch answers a launch request the way a driver plugin does: argv first, so
// attn can spawn the PTY, then the reports that move the session.
func (p *pluginFixturePeer) launch(request jsonRPCMessage) {
	p.t.Helper()
	var params pluginDriverSpawnParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		p.t.Fatalf("decode fixture launch params: %v", err)
	}
	appendPluginFixtureRecord(p.t, pluginDriverFixtureRecord{Method: request.Method, Params: params})
	script := `IFS= read -r input; printf 'PLUGIN_RUN method=%s cwd=%s input=%s\n' "$ATTN_PLUGIN_FIXTURE_METHOD" "$PWD" "$input"; trap 'exit 0' TERM INT; while :; do sleep 1; done`
	p.respond(request.ID, pluginDriverSpawnResult{
		Argv: []string{"/bin/sh", "-c", script},
		Env:  map[string]string{"ATTN_PLUGIN_FIXTURE_METHOD": request.Method},
		CWD:  os.Getenv("ATTN_DRIVER_FIXTURE_CWD"),
	})
	p.callOK("session.report_state", pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       1,
		State:     protocol.StateWorking,
	})
	p.callOK("session.report_metadata", pluginReportMetadataParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       2,
		Metadata:  json.RawMessage(`{"native_id":"` + request.Method + `-native"}`),
	})
	p.callOK("session.report_stop", pluginReportStopParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if request.Method != "driver.spawn" {
		return
	}
	waitForPluginFixtureStateTrigger(p.t)
	p.callOK("session.report_state", pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       4,
		State:     protocol.StateWorking,
	})
	p.callOK("session.report_stop", pluginReportStopParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       5,
		Verdict:   protocol.StateWaitingInput,
	})
}

func (p *pluginFixturePeer) respond(id json.RawMessage, result interface{}) {
	p.t.Helper()
	if err := p.write(jsonRPCResult(id, result)); err != nil {
		p.t.Fatalf("respond to request id=%s: %v", jsonRPCIDKey(id), err)
	}
}

func (p *pluginFixturePeer) read() (jsonRPCMessage, error) {
	var message jsonRPCMessage
	err := p.decoder.Decode(&message)
	return message, err
}

func (p *pluginFixturePeer) write(message jsonRPCMessage) error {
	return json.NewEncoder(p.conn).Encode(message)
}

// TestPluginFixturePeerAnswersRequestArrivingBeforeItsResponse pins the
// ordering the end-to-end fixture used to fail on. Under CI load the daemon
// wrote driver.session_closed before answering the report the fixture was
// waiting for; the fixture treated that as fatal, died, and the daemon's close
// notification came back EOF. Here the same order is scripted rather than
// raced, so the peer's duplex handling is proven on every run.
func TestPluginFixturePeerAnswersRequestArrivingBeforeItsResponse(t *testing.T) {
	closeLog := filepath.Join(t.TempDir(), "driver-close.jsonl")
	t.Setenv("ATTN_DRIVER_FIXTURE_CLOSE_LOG", closeLog)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	scripted := make(chan error, 1)
	go func() { scripted <- scriptCloseBeforeReportResponse(server) }()

	peer := newPluginFixturePeer(t, client)
	peer.callOK("session.report_stop", pluginReportStopParams{
		SessionID: "session-1",
		RunID:     "run-1",
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if err := <-scripted; err != nil {
		t.Fatalf("scripted daemon: %v", err)
	}

	records, ok := readPluginFixtureCloseRecords(closeLog, 1)
	if !ok {
		t.Fatal("peer recorded no close notification, want the one that arrived mid-request")
	}
	if records[0].Params.RunID != "run-1" || records[0].Params.Reason != "exited" {
		t.Fatalf("close record=%+v, want exited notification for run-1", records[0].Params)
	}
}

// scriptCloseBeforeReportResponse plays the daemon: read the peer's report,
// send driver.session_closed before answering it, and answer only once the
// close is acknowledged.
func scriptCloseBeforeReportResponse(conn net.Conn) error {
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var report jsonRPCMessage
	if err := decoder.Decode(&report); err != nil {
		return fmt.Errorf("read plugin report: %w", err)
	}
	if report.Method != "session.report_stop" {
		return fmt.Errorf("first plugin message method=%q, want session.report_stop", report.Method)
	}

	params, err := json.Marshal(pluginDriverSessionClosedParams{
		SessionID: "session-1",
		RunID:     "run-1",
		Reason:    "exited",
	})
	if err != nil {
		return fmt.Errorf("marshal close params: %w", err)
	}
	if err := encoder.Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("900"),
		Method:  "driver.session_closed",
		Params:  params,
	}); err != nil {
		return fmt.Errorf("send close notification: %w", err)
	}

	var ack jsonRPCMessage
	if err := decoder.Decode(&ack); err != nil {
		return fmt.Errorf("read close acknowledgement: %w", err)
	}
	if jsonRPCIDKey(ack.ID) != "900" || ack.Method != "" {
		return fmt.Errorf("close acknowledgement=%+v, want a response to id 900", ack)
	}
	if err := encoder.Encode(jsonRPCResult(report.ID, struct{}{})); err != nil {
		return fmt.Errorf("answer plugin report: %w", err)
	}
	return nil
}
