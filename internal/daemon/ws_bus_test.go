package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The two bus handlers are the glue the app talks to, and their refusal branches
// are the ones a user meets when something is already wrong. A refusal that says
// nothing is worse than no command at all, so each is checked for the sentence
// it sends back — not just for "success is false".

func busTestClient() *wsClient {
	return &wsClient{send: make(chan outboundMessage, 4)}
}

func nextBusMessage(t *testing.T, client *wsClient, into any) {
	t.Helper()
	select {
	case outbound := <-client.send:
		if err := json.Unmarshal(outbound.payload, into); err != nil {
			t.Fatalf("decode outbound: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler answered nothing")
	}
}

func TestBusStatusGetAnswersTheAskingClient(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.ensureEventBus()
	if _, err := d.eventBus.Publish(FactSessionStateChanged, "sess-1", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	client := busTestClient()

	d.handleBusStatusGet(client, &protocol.BusStatusGetMessage{RequestID: "r1"})

	var result protocol.BusStatusResultMessage
	nextBusMessage(t, client, &result)
	if !result.Success {
		t.Fatalf("bus_status_result success=false error=%q", protocol.Deref(result.Error))
	}
	if result.RequestID != "r1" {
		t.Errorf("request_id = %q, want r1", result.RequestID)
	}
	if result.Rows != 1 || len(result.Producers) != 1 {
		t.Fatalf("rows=%d producers=%d, want the one published fact", result.Rows, len(result.Producers))
	}
	if result.Producers[0].Name != FactSessionStateChanged {
		t.Errorf("producer = %q, want %q", result.Producers[0].Name, FactSessionStateChanged)
	}
	// Windows travel with the numbers: a rate is meaningless without the span it
	// was measured over, and no surface should hardcode its own.
	if result.RecentWindowSeconds <= 0 || result.BaselineWindowSeconds <= 0 || result.SurgeRatePerHour <= 0 {
		t.Errorf("result does not carry the windows and the tripwire: %+v", result)
	}
}

// A request id is how the app pairs an answer with the promise waiting for it.
// Without one there is nothing to settle, so this fails loudly rather than
// sending a result nobody can match.
func TestBusStatusGetWithoutARequestIDIsACommandError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()

	d.handleBusStatusGet(client, &protocol.BusStatusGetMessage{RequestID: "  "})

	var msg protocol.CommandErrorMessage
	nextBusMessage(t, client, &msg)
	if protocol.Deref(msg.Cmd) != protocol.CmdBusStatusGet {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(msg.Cmd), protocol.CmdBusStatusGet)
	}
	if msg.Error == "" {
		t.Error("the refusal must say what was wrong")
	}
}

func TestBusSetConsumerEnabledFlipsTheDatabaseBit(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	consumer := store.BusConsumer{Name: "notifier", Cursor: 7, Filter: "session.*", Enabled: true}
	if err := d.store.SaveBusConsumer(consumer, time.Now()); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	client := busTestClient()

	d.handleBusSetConsumerEnabled(client, &protocol.BusSetConsumerEnabledMessage{
		RequestID: "r1", Consumer: "notifier", Enabled: false,
	})

	var result protocol.BusSetConsumerEnabledResultMessage
	nextBusMessage(t, client, &result)
	if !result.Success {
		t.Fatalf("success=false error=%q", protocol.Deref(result.Error))
	}
	stored, ok, err := d.store.GetBusConsumer("notifier")
	if err != nil || !ok {
		t.Fatalf("GetBusConsumer: %v, ok=%v", err, ok)
	}
	if stored.Enabled {
		t.Error("the row is still enabled; the kill switch must be the database bit itself")
	}
	// The cursor is kept: disabling stops delivery, it does not forget position.
	if stored.Cursor != 7 {
		t.Errorf("cursor = %d, want 7", stored.Cursor)
	}
}

func TestBusSetConsumerEnabledRefusesAnUnknownConsumerByName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()

	d.handleBusSetConsumerEnabled(client, &protocol.BusSetConsumerEnabledMessage{
		RequestID: "r1", Consumer: "nope", Enabled: true,
	})

	var result protocol.BusSetConsumerEnabledResultMessage
	nextBusMessage(t, client, &result)
	if result.Success {
		t.Fatal("a consumer that does not exist must not report success")
	}
	if got := protocol.Deref(result.Error); got != "no consumer named nope" {
		t.Errorf("error = %q, want it to name the consumer that was asked for", got)
	}
	if result.Consumer != "nope" {
		t.Errorf("consumer = %q, want the asked-for name echoed back", result.Consumer)
	}
}

func TestBusSetConsumerEnabledRefusesAnEmptyConsumer(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()

	d.handleBusSetConsumerEnabled(client, &protocol.BusSetConsumerEnabledMessage{
		RequestID: "r1", Consumer: "   ", Enabled: true,
	})

	var result protocol.BusSetConsumerEnabledResultMessage
	nextBusMessage(t, client, &result)
	if result.Success {
		t.Fatal("an empty consumer name must not report success")
	}
	if protocol.Deref(result.Error) == "" {
		t.Error("the refusal must say what was wrong")
	}
}
