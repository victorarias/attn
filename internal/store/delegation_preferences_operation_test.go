package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func TestDelegationAcceptedSnapshotSurvivesSettingsEdits(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	cfg, err := s.SaveDelegationPreferences(delegationprefs.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(delegationprefs.Resolved{Revision: cfg.Revision, Instructions: "Original", Selection: delegationprefs.Selection{Harness: "codex"}})
	first, claimed, err := s.ClaimDelegationOperationWithPreferences("request-a", "op-a", "session-a", "", "", `{"role":"build"}`, string(raw), time.Now())
	if err != nil || !claimed {
		t.Fatalf("%+v %v", first, err)
	}
	cfg.Enabled = false
	if _, err = s.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	retry, claimed, err := s.ClaimDelegationOperationWithPreferences("request-a", "op-b", "session-b", "", "", `{"role":"build"}`, string(raw), time.Now())
	if err != nil || claimed || retry.ResolvedPreferences != string(raw) || retry.Operation.OperationID != "op-a" {
		t.Fatalf("%+v %v", retry, err)
	}
	if _, _, err = s.ClaimDelegationOperationWithPreferences("request-new", "op-new", "session-new", "", "", `{}`, string(raw), time.Now()); !errors.Is(err, delegationprefs.ErrConflict) {
		t.Fatalf("stale new launch: %v", err)
	}
	if _, _, err = s.ClaimDelegationOperationWithPreferences("request-a", "op-c", "session-c", "", "", `{"role":"review"}`, string(raw), time.Now()); !errors.Is(err, ErrDelegationRequestConflict) {
		t.Fatalf("changed retry: %v", err)
	}
}
