package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func TestSessionCostObservationsPersistAndReplaceAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(&protocol.Session{ID: "cost", Label: "cost"})
	first := SessionCostObservation{
		ObservationID: "claude:msg-1", Model: "claude-opus-4-8",
		Usage: sessioncost.Usage{InputTokens: 2, CacheWrite5mInputTokens: 100, OutputTokens: 3},
	}
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-1", []SessionCostObservation{first}); err != nil || !changed {
		t.Fatalf("first apply changed=%v err=%v", changed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.SessionCost("cost")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "cursor-1" || state.Ledger["claude-opus-4-8"] != first.Usage {
		t.Fatalf("reopened state = %+v", state)
	}

	revised := first
	revised.Usage.OutputTokens = 8
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-2", []SessionCostObservation{revised}); err != nil || !changed {
		t.Fatalf("revision changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if got := state.Ledger["claude-opus-4-8"].OutputTokens; got != 8 {
		t.Fatalf("output after absolute revision = %d, want 8 (not 11)", got)
	}
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-3", []SessionCostObservation{revised}); err != nil || changed {
		t.Fatalf("duplicate apply changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if state.Cursor != "cursor-3" {
		t.Fatalf("cursor = %q, want cursor-3", state.Cursor)
	}
	if changed, err := s.MarkSessionCostUsageUnavailable("cost", "cursor-4"); err != nil || !changed {
		t.Fatalf("mark unavailable changed=%v err=%v", changed, err)
	}
	if changed, err := s.MarkSessionCostUsageUnavailable("cost", "cursor-5"); err != nil || changed {
		t.Fatalf("repeat unavailable changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if !state.UsageUnavailable || state.Cursor != "cursor-5" {
		t.Fatalf("unavailable state = %+v", state)
	}
}
