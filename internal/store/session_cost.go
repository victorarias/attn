package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/victorarias/attn/internal/sessioncost"
)

// SessionCostObservation is one provider request's absolute token usage.
// ObservationID is stable across repeated transcript lines for the same request.
type SessionCostObservation struct {
	ObservationID string            `json:"observation_id"`
	Model         string            `json:"model"`
	Usage         sessioncost.Usage `json:"usage"`
}

// SessionCostState is the durable accounting fact for one session. Cursor is
// the next transcript position; observations make repeated provider messages
// idempotent while the ledger keeps pricing a cheap per-model reduction.
type SessionCostState struct {
	Initialized      bool                              `json:"initialized,omitempty"`
	Cursor           string                            `json:"cursor,omitempty"`
	UsageUnavailable bool                              `json:"usage_unavailable,omitempty"`
	Ledger           sessioncost.Ledger                `json:"ledger,omitempty"`
	Observations     map[string]SessionCostObservation `json:"observations,omitempty"`
}

func cloneSessionCostState(state SessionCostState) SessionCostState {
	clone := SessionCostState{
		Initialized: state.Initialized, Cursor: state.Cursor, UsageUnavailable: state.UsageUnavailable,
	}
	if state.Ledger != nil {
		clone.Ledger = make(sessioncost.Ledger, len(state.Ledger))
		for model, usage := range state.Ledger {
			clone.Ledger[model] = usage
		}
	}
	if state.Observations != nil {
		clone.Observations = make(map[string]SessionCostObservation, len(state.Observations))
		for id, observation := range state.Observations {
			clone.Observations[id] = observation
		}
	}
	return clone
}

func decodeSessionCostState(raw string) (SessionCostState, error) {
	state := SessionCostState{}
	if strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return SessionCostState{}, err
	}
	return state, nil
}

// SessionCost returns a copy of the durable cost state.
func (s *Store) SessionCost(sessionID string) (SessionCostState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return cloneSessionCostState(s.sessionCosts[sessionID]), nil
	}
	var raw string
	if err := s.db.QueryRow("SELECT session_cost_json FROM sessions WHERE id = ?", sessionID).Scan(&raw); err != nil {
		return SessionCostState{}, err
	}
	state, err := decodeSessionCostState(raw)
	if err != nil {
		return SessionCostState{}, fmt.Errorf("decode session cost for %s: %w", sessionID, err)
	}
	return state, nil
}

// SetSessionCostCursor moves the transcript checkpoint without changing usage.
// It seeds an existing transcript at head and records replacement/recovery
// boundaries before their next usage batch is committed.
func (s *Store) SetSessionCostCursor(sessionID, cursor string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
		state.Cursor = strings.TrimSpace(cursor)
	})
}

// InitializeSessionCostTracking marks a newly launched session as eligible to
// account from byte zero when its transcript is discovered. Existing live
// sessions lack this marker after migration and are seeded at transcript head.
func (s *Store) InitializeSessionCostTracking(sessionID string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
	})
}

// MarkSessionCostUsageUnavailable records that a transcript-bearing harness
// produced an assistant turn but exposes no token usage. It returns true only
// on the first transition so idle watcher ticks stay quiet on the wire.
func (s *Store) MarkSessionCostUsageUnavailable(sessionID, cursor string) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
		changed = !state.UsageUnavailable
		state.UsageUnavailable = true
		state.Cursor = strings.TrimSpace(cursor)
	})
	return changed, err
}

// ApplySessionCostObservations advances the transcript cursor and upserts
// absolute request usage atomically. The bool reports whether billable usage
// changed (cursor-only movement stays quiet on the wire).
func (s *Store) ApplySessionCostObservations(sessionID, cursor string, observations []SessionCostObservation) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
		if state.Ledger == nil {
			state.Ledger = make(sessioncost.Ledger)
		}
		if state.Observations == nil {
			state.Observations = make(map[string]SessionCostObservation)
		}
		for _, observation := range observations {
			observation.ObservationID = strings.TrimSpace(observation.ObservationID)
			observation.Model = strings.TrimSpace(observation.Model)
			if observation.ObservationID == "" || observation.Model == "" || !observation.Usage.HasUsage() {
				continue
			}
			prior, exists := state.Observations[observation.ObservationID]
			if exists && reflect.DeepEqual(prior, observation) {
				continue
			}
			if exists {
				state.Ledger[prior.Model] = state.Ledger[prior.Model].Subtract(prior.Usage)
			}
			state.Ledger[observation.Model] = state.Ledger[observation.Model].Add(observation.Usage)
			state.Observations[observation.ObservationID] = observation
			changed = true
		}
		state.Cursor = strings.TrimSpace(cursor)
	})
	return changed, err
}

func (s *Store) updateSessionCost(sessionID string, mutate func(*SessionCostState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if s.sessionCosts == nil {
			s.sessionCosts = make(map[string]SessionCostState)
		}
		state := cloneSessionCostState(s.sessionCosts[sessionID])
		mutate(&state)
		s.sessionCosts[sessionID] = state
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRow("SELECT session_cost_json FROM sessions WHERE id = ?", sessionID).Scan(&raw); err != nil {
		return err
	}
	state, err := decodeSessionCostState(raw)
	if err != nil {
		return fmt.Errorf("decode session cost for %s: %w", sessionID, err)
	}
	mutate(&state)
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode session cost for %s: %w", sessionID, err)
	}
	if _, err := tx.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", string(encoded), sessionID); err != nil {
		return err
	}
	return tx.Commit()
}
