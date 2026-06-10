package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestEncodePtyOutputMessagePicksFormatByCapability(t *testing.T) {
	event := ptybackend.OutputEvent{
		Kind: ptybackend.OutputEventKindOutput,
		Data: []byte("hi \x1b[31mthere\x1b[0m\r\n"),
		Seq:  42,
	}

	legacy := &wsClient{}
	legacy.setIdentity("test", "v", []string{protocol.CapabilityWorkspaceSessions})
	msg, err := encodePtyOutputMessage(legacy, "sess-1", event)
	if err != nil {
		t.Fatalf("encode legacy: %v", err)
	}
	if msg.kind != messageKindText {
		t.Fatalf("legacy client message kind = %v, want text", msg.kind)
	}
	var wsEvent protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &wsEvent); err != nil {
		t.Fatalf("legacy payload is not JSON: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(wsEvent.Data))
	if err != nil {
		t.Fatalf("legacy payload data is not base64: %v", err)
	}
	if wsEvent.Event != protocol.EventPtyOutput || protocol.Deref(wsEvent.ID) != "sess-1" ||
		protocol.Deref(wsEvent.Seq) != 42 || !bytes.Equal(decoded, event.Data) {
		t.Fatalf("legacy event mismatch: %+v data=%q", wsEvent, decoded)
	}

	binaryCapable := &wsClient{}
	binaryCapable.setIdentity("test", "v", []string{
		protocol.CapabilityWorkspaceSessions,
		protocol.CapabilityBinaryPtyOutput,
	})
	msg, err = encodePtyOutputMessage(binaryCapable, "sess-1", event)
	if err != nil {
		t.Fatalf("encode binary: %v", err)
	}
	if msg.kind != messageKindBinary {
		t.Fatalf("binary client message kind = %v, want binary", msg.kind)
	}
	id, seq, data, err := protocol.DecodePtyOutputFrame(msg.payload)
	if err != nil {
		t.Fatalf("decode binary frame: %v", err)
	}
	if id != "sess-1" || seq != 42 || !bytes.Equal(data, event.Data) {
		t.Fatalf("binary frame mismatch: id=%q seq=%d data=%q", id, seq, data)
	}
}
