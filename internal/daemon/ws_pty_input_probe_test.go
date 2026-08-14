package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHandlePtyInputAcknowledgesOnlySampledWrites(t *testing.T) {
	backend := &fakeSpawnBackend{}
	d := &Daemon{ptyBackend: backend}
	client := &wsClient{send: make(chan outboundMessage, 2)}
	automation := "automation" // Keeps unrelated auto-settle state out of this boundary test.

	d.handlePtyInput(client, &protocol.PtyInputMessage{
		Cmd:     protocol.CmdPtyInput,
		ID:      "runtime-1",
		Data:    "a",
		Source:  &automation,
		ProbeID: protocol.Ptr("probe-1"),
	})

	select {
	case outbound := <-client.send:
		var result protocol.PtyInputProbeResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode pty_input_probe_result: %v", err)
		}
		if result.Event != protocol.EventPtyInputProbeResult {
			t.Fatalf("event = %q, want %q", result.Event, protocol.EventPtyInputProbeResult)
		}
		if result.ID != "runtime-1" || result.ProbeID != "probe-1" || !result.Success {
			t.Fatalf("result = %+v", result)
		}
		if result.WriteDurationUs < 0 {
			t.Fatalf("write_duration_us = %d, want non-negative", result.WriteDurationUs)
		}
	case <-time.After(time.Second):
		t.Fatal("sampled PTY input was not acknowledged")
	}

	d.handlePtyInput(client, &protocol.PtyInputMessage{
		Cmd:    protocol.CmdPtyInput,
		ID:     "runtime-1",
		Data:   "b",
		Source: &automation,
	})
	select {
	case outbound := <-client.send:
		t.Fatalf("unsampled PTY input produced diagnostic traffic: %s", outbound.payload)
	default:
	}
}
