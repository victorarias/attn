package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/victorarias/attn/internal/sessioncost"
)

type SessionCostObservation struct {
	ObservationID string            `json:"observation_id"`
	Model         string            `json:"model"`
	Usage         sessioncost.Usage `json:"usage"`
}

type SessionCostState struct {
	Initialized           bool                              `json:"initialized,omitempty"`
	Cursor                string                            `json:"cursor,omitempty"`
	UsageUnavailable      bool                              `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool                              `json:"measurement_incomplete,omitempty"`
	Sources               map[string]SessionCostSourceState `json:"sources,omitempty"`
	Ledger                sessioncost.Ledger                `json:"ledger,omitempty"`
	Observations          map[string]SessionCostObservation `json:"observations,omitempty"`
}

type SessionCostSourceState struct {
	Cursor string `json:"cursor,omitempty"`
}

func cloneSessionCostState(state SessionCostState) SessionCostState {
	clone := SessionCostState{
		Initialized: state.Initialized, Cursor: state.Cursor, UsageUnavailable: state.UsageUnavailable,
		MeasurementIncomplete: state.MeasurementIncomplete,
	}
	if state.Sources != nil {
		clone.Sources = make(map[string]SessionCostSourceState, len(state.Sources))
		for id, source := range state.Sources {
			clone.Sources[id] = source
		}
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

func (s *Store) SetSessionCostCursor(sessionID, cursor string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
		state.Cursor = strings.TrimSpace(cursor)
	})
}

func (s *Store) InitializeSessionCostTracking(sessionID string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
	})
}

// InitializeSessionCostSources records one discovery pass atomically. An
// uninitialized resumed session baselines every source from the same snapshot.
func (s *Store) InitializeSessionCostSources(sessionID string, cursors map[string]string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		if state.Sources == nil {
			state.Sources = make(map[string]SessionCostSourceState)
		}
		for id, cursor := range cursors {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, exists := state.Sources[id]; !exists {
				state.Sources[id] = SessionCostSourceState{Cursor: strings.TrimSpace(cursor)}
			}
		}
		state.Initialized = true
	})
}

func (s *Store) SetSessionCostSourceCursor(sessionID, sourceID, cursor string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) {
		if state.Sources == nil {
			state.Sources = make(map[string]SessionCostSourceState)
		}
		state.Initialized = true
		state.Sources[strings.TrimSpace(sourceID)] = SessionCostSourceState{Cursor: strings.TrimSpace(cursor)}
	})
}

func (s *Store) MarkSessionCostMeasurementIncomplete(sessionID string) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) {
		changed = !state.MeasurementIncomplete
		state.MeasurementIncomplete = true
	})
	return changed, err
}

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

func (s *Store) ApplySessionCostObservations(sessionID, cursor string, observations []SessionCostObservation) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) {
		changed = applySessionCostObservations(state, observations)
		state.Cursor = strings.TrimSpace(cursor)
	})
	return changed, err
}

func (s *Store) ApplySessionCostSourceObservations(sessionID, sourceID, cursor string, observations []SessionCostObservation) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) {
		state.Initialized = true
		if state.Sources == nil {
			state.Sources = make(map[string]SessionCostSourceState)
		}
		state.Sources[strings.TrimSpace(sourceID)] = SessionCostSourceState{Cursor: strings.TrimSpace(cursor)}
		changed = applySessionCostObservations(state, observations)
	})
	return changed, err
}

func applySessionCostObservations(state *SessionCostState, observations []SessionCostObservation) bool {
	state.Initialized = true
	if state.Ledger == nil {
		state.Ledger = make(sessioncost.Ledger)
	}
	if state.Observations == nil {
		state.Observations = make(map[string]SessionCostObservation)
	}
	changed := false
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
	return changed
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
	if _, err := tx.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ? AND closed_at = ''", string(encoded), sessionID); err != nil {
		return err
	}
	return tx.Commit()
}
