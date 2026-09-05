package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func (s *Store) GetDelegationPreferences() (delegationprefs.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readDelegationPreferences(s.db)
}

func (s *Store) readDelegationPreferences(q rowQuerier) (delegationprefs.Config, error) {
	cfg := delegationprefs.Defaults()
	if s.db == nil {
		return cfg, errors.New("preferences database is unavailable")
	}
	var raw string
	err := q.QueryRow(`SELECT config FROM delegation_preferences WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("read delegation preferences: %w", err)
	}
	if err := delegationprefs.Validate(cfg); err != nil {
		return cfg, fmt.Errorf("invalid stored delegation preferences: %w", err)
	}
	if cfg.Roles == nil {
		cfg.Roles = []delegationprefs.Role{}
	}
	return cfg, nil
}

func (s *Store) SaveDelegationPreferences(cfg delegationprefs.Config) (delegationprefs.Config, error) {
	if err := delegationprefs.Validate(cfg); err != nil {
		return cfg, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return cfg, errors.New("preferences database is unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return cfg, err
	}
	defer tx.Rollback()
	current, err := s.readDelegationPreferences(tx)
	if err != nil {
		return cfg, err
	}
	if cfg.Revision != current.Revision {
		return current, delegationprefs.ErrConflict
	}
	cfg.Revision++
	if cfg.Roles == nil {
		cfg.Roles = []delegationprefs.Role{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	_, err = tx.Exec(`INSERT INTO delegation_preferences (id, config) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET config = excluded.config`, string(raw))
	if err != nil {
		return cfg, err
	}
	return cfg, tx.Commit()
}
