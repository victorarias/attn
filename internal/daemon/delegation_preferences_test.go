package daemon

import (
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

func readPreferencesResult(t *testing.T, client *wsClient) protocol.DelegationPreferencesResultMessage {
	t.Helper()
	var result protocol.DelegationPreferencesResultMessage
	select {
	case raw := <-client.send:
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("missing preferences response")
	}
	return result
}

func TestDelegationPreferencesSettingsRoundTripAndConflict(t *testing.T) {
	d := newDaemonForTest(t)
	client := newInternalWSClient()
	d.handleDelegationPreferencesGet(client, &protocol.DelegationPreferencesGetMessage{RequestID: "load"})
	got := readPreferencesResult(t, client)
	if !got.Success || got.RequestID != "load" || got.Preferences == nil || got.Preferences.Enabled {
		t.Fatalf("initial settings: %+v", got)
	}
	cfg := *got.Preferences
	cfg.Enabled = true
	cfg.Fallback.Selection = delegationprefs.Selection{Harness: "copilot"}
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "save", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if !got.Success || got.Preferences.Revision != 1 || got.Preferences.Fallback.Selection.Model != "" {
		t.Fatalf("save: %+v", got)
	}
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "stale", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if got.Success || got.Error == nil {
		t.Fatalf("stale save accepted: %+v", got)
	}
	stored, err := d.store.GetDelegationPreferences()
	if err != nil || stored.Revision != 1 {
		t.Fatalf("conflict changed settings: %+v %v", stored, err)
	}
	cfg = stored
	cfg.Enabled = false
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "off", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if !got.Success || got.Preferences.Enabled || got.Preferences.Fallback.Selection.Harness != "copilot" {
		t.Fatalf("disable lost fallback: %+v", got)
	}
	if len(docFacts(t, d, FactDelegationPreferencesChanged)) != 2 {
		t.Fatal("expected one fact for each successful save")
	}
}
