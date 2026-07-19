package daemon

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestValidatePluginDriverCapabilities_AcceptsMessageDelivery(t *testing.T) {
	capabilities, err := validatePluginDriverCapabilities(map[string]bool{"message_delivery": true})
	if err != nil {
		t.Fatalf("validatePluginDriverCapabilities error=%v, want nil", err)
	}
	if !capabilities["message_delivery"] {
		t.Fatalf("capabilities=%+v, want message_delivery true", capabilities)
	}
}

func TestTypeDoorbell_DeliversViaPluginWhenDriverSupportsMessageDelivery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var inputRecorded bool
	backend.onInput = func(string, []byte) { inputRecorded = true }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-doorbell",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-doorbell", "pi-plugin", "run-doorbell") {
		t.Fatal("failed to begin plugin run")
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != "driver.deliver_message" {
			t.Errorf("method=%q, want driver.deliver_message", request.Method)
			return
		}
		var params pluginDeliverMessageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode deliver_message params: %v", err)
			return
		}
		if params.SessionID != "pi-doorbell" || params.RunID != "run-doorbell" || params.Text != "ping from the chief" {
			t.Errorf("deliver_message params=%+v, want session/run/text match", params)
		}
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: true})
	}()

	if err := d.typeDoorbell("pi-doorbell", "ping from the chief"); err != nil {
		t.Fatalf("typeDoorbell error=%v, want nil", err)
	}
	<-requestDone

	if inputRecorded {
		t.Fatal("typeDoorbell wrote to the PTY for a message_delivery driver, want in-band delivery only")
	}
}

func TestTypeDoorbell_KeepsPTYPasteWithoutMessageDeliveryCapability(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var recordedInput []byte
	backend.onInput = func(_ string, data []byte) { recordedInput = append([]byte(nil), data...) }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-no-delivery",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-no-delivery", "pi-plugin", "run-no-delivery") {
		t.Fatal("failed to begin plugin run")
	}

	if err := d.typeDoorbell("pi-no-delivery", "ping from the chief"); err != nil {
		t.Fatalf("typeDoorbell error=%v, want nil", err)
	}
	if len(recordedInput) == 0 {
		t.Fatal("typeDoorbell did not write to the PTY, want bracketed-paste fallback")
	}
	if got := string(recordedInput); len(got) < len(bracketedPasteStart) || got[:len(bracketedPasteStart)] != bracketedPasteStart {
		t.Fatalf("input=%q, want bracketed-paste prefix", got)
	}
}

func TestTypeDoorbell_DeliverMessageFailureSurfacesErrorWithoutPTYFallback(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var inputRecorded bool
	backend.onInput = func(string, []byte) { inputRecorded = true }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-delivery-fails",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-delivery-fails", "pi-plugin", "run-fails") {
		t.Fatal("failed to begin plugin run")
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: false})
	}()

	err := d.typeDoorbell("pi-delivery-fails", "ping from the chief")
	<-requestDone
	if err == nil {
		t.Fatal("typeDoorbell error=nil, want deliver_message ok=false to surface as an error")
	}
	if inputRecorded {
		t.Fatal("typeDoorbell fell back to the PTY after a message_delivery failure, want no fallback")
	}
}

func TestPluginClassifyStop_RejectsUnauthorizedRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-auth",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-auth", "pi-plugin", "run-real") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 30, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-auth",
		RunID:         "run-wrong",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error == nil {
		t.Fatal("attn.classify_stop with the wrong run_id succeeded, want ownership error")
	}
}

func TestPluginClassifyStop_RejectsEmptyAssistantText(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-empty",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-empty", "pi-plugin", "run-empty") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 31, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-empty",
		RunID:         "run-empty",
		AssistantText: "   ",
	})
	if response.Error == nil {
		t.Fatal("attn.classify_stop with blank assistant_text succeeded, want validation error")
	}
}

func TestPluginClassifyStop_HappyPathReturnsClassifierVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = NewFakeClassifier(protocol.StateWaitingInput)
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-happy",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-happy", "pi-plugin", "run-happy") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 32, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-happy",
		RunID:         "run-happy",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error != nil {
		t.Fatalf("attn.classify_stop error=%#v, want nil", response.Error)
	}
	var result pluginClassifyStopResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode classify_stop result: %v", err)
	}
	if result.Verdict != protocol.StateWaitingInput {
		t.Fatalf("verdict=%q, want waiting_input", result.Verdict)
	}
}

func TestPluginClassifyStop_ClassifierErrorYieldsUnknownVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = &errorClassifier{state: protocol.StateUnknown, err: errors.New("classifier execution failed")}
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-error",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-error", "pi-plugin", "run-error") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 33, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-error",
		RunID:         "run-error",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error != nil {
		t.Fatalf("attn.classify_stop error=%#v, want a successful unknown verdict, not a JSON-RPC failure", response.Error)
	}
	var result pluginClassifyStopResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode classify_stop result: %v", err)
	}
	if result.Verdict != protocol.StateUnknown {
		t.Fatalf("verdict=%q, want unknown after classifier error", result.Verdict)
	}
}
