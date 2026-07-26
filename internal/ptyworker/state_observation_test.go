package ptyworker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

// TestStateObservationSurvivesWire is the contract the daemon's arbitration will
// rest on: which observer spoke, why, and when it observed must cross the worker
// RPC intact — including through JSON, since the daemon reads these off a socket
// and not from the same process.
func TestStateObservationSurvivesWire(t *testing.T) {
	observedAt := time.Date(2026, 7, 25, 10, 30, 0, 123456789, time.UTC)
	want := pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  "pending_approval",
		Detail: "watch subscribe replay",
		At:     observedAt,
	}

	encoded, err := json.Marshal(stateChangedEvent("sess-1", want))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var evt EventEnvelope
	if err := json.Unmarshal(encoded, &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Event != EventStateChanged || evt.SessionID != "sess-1" {
		t.Fatalf("envelope = %+v, want state_changed for sess-1", evt)
	}

	got := ObservationFromEvent(evt, *evt.State, time.Now())
	if got.Source != want.Source || got.Claim != want.Claim || got.Detail != want.Detail {
		t.Fatalf("observation = %+v, want %+v", got, want)
	}
	if !got.At.Equal(want.At) {
		t.Fatalf("observed-at = %s, want %s", got.At, want.At)
	}
}

// TestStateObservationFromLegacyWorker covers the version skew the additive
// fields buy us: a worker predating them sends state alone, and the daemon must
// still get a usable observation rather than an empty source it might mistake
// for a real one.
func TestStateObservationFromLegacyWorker(t *testing.T) {
	state := "pending_approval"
	arrived := time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)

	got := ObservationFromEvent(EventEnvelope{
		Type:      "evt",
		Event:     EventStateChanged,
		SessionID: "sess-legacy",
		State:     &state,
	}, state, arrived)

	if got.Source != pty.SourceUnknown {
		t.Fatalf("source = %q, want %q", got.Source, pty.SourceUnknown)
	}
	if got.Claim != state {
		t.Fatalf("claim = %q, want %q", got.Claim, state)
	}
	if !got.At.Equal(arrived) {
		t.Fatalf("observed-at = %s, want the receive time %s", got.At, arrived)
	}
}
