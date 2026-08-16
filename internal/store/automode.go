package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/automode"
)

// Auto mode's persistence (migration 109): the promoted config, the proposals
// waiting on a human, and the denials a session reported.
//
// The asymmetry between the two write paths is the point, and it lives here
// rather than in any one caller: PromoteAutoModeProposal is the ONLY way a
// pattern or a model reaches the config row, and it is reachable from the app
// alone. Everything an agent can call writes a proposal, which changes nothing
// a session launches with.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

// AutoModeProposal is one proposed change and what became of it. Value is a
// pattern for allow/deny and a `provider/id` for model; Target names which model
// and is empty otherwise.
type AutoModeProposal struct {
	ID         int64
	Kind       string
	Target     string
	Value      string
	ProposedBy string
	State      string
	CreatedAt  time.Time
	ResolvedAt time.Time
}

// AutoModeDenial is one call auto mode refused. Nothing writes these yet —
// reporting arrives with slice 5 — but the shape ships now so the CLI's
// `denials` verb reads the table it will always read.
type AutoModeDenial struct {
	ID        int64
	SessionID string
	Tool      string
	Signature string
	Reason    string
	CreatedAt time.Time
}

// GetAutoModeConfig reads the promoted config, resolved: a machine with no row,
// or a row that never named a model, gets the shipped defaults rather than empty
// strings a caller would have to know how to fill in.
func (s *Store) GetAutoModeConfig() (automode.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readAutoModeConfig(s.db)
}

// readAutoModeConfig takes a rowQuerier (documents.go) so the same read serves a
// plain get and the read-back inside a promote's transaction.
func (s *Store) readAutoModeConfig(q rowQuerier) (automode.Config, error) {
	cfg := automode.Defaults()
	if s.db == nil {
		return cfg, nil
	}
	var (
		enabled                          int
		environment, allow, hardDeny     string
		classifierModel, escalationModel string
	)
	err := q.QueryRow(`
		SELECT enabled_default, environment, allow_patterns, hard_deny,
		       classifier_model, escalation_model
		FROM automode_config WHERE id = 1
	`).Scan(&enabled, &environment, &allow, &hardDeny, &classifierModel, &escalationModel)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	cfg.EnabledDefault = enabled != 0
	if cfg.Environment, err = decodeStringList(environment, "environment"); err != nil {
		return automode.Defaults(), err
	}
	if cfg.Allow, err = decodeStringList(allow, "allow"); err != nil {
		return automode.Defaults(), err
	}
	if cfg.HardDeny, err = decodeStringList(hardDeny, "hard_deny"); err != nil {
		return automode.Defaults(), err
	}
	if classifierModel != "" {
		cfg.ClassifierModel = classifierModel
	}
	if escalationModel != "" {
		cfg.EscalationModel = escalationModel
	}
	return cfg, nil
}

// SetAutoModeEnvironment replaces the classifier's environment prose. Unlike the
// pattern and model lists this is a direct write with no proposal in front of
// it, which is what the plan's public interface specifies.
func (s *Store) SetAutoModeEnvironment(entries []string, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.Environment = append([]string{}, entries...)
		return nil
	})
}

// SetAutoModeEnabledDefault flips whether new attn sessions start with auto mode
// on.
func (s *Store) SetAutoModeEnabledDefault(enabled bool, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.EnabledDefault = enabled
		return nil
	})
}

// CreateAutoModeProposal records a proposed change. It validates first, so a
// proposal that could never be promoted never reaches the app's review list.
func (s *Store) CreateAutoModeProposal(kind, target, value, proposedBy string, now time.Time) (AutoModeProposal, error) {
	if err := automode.ValidateProposal(kind, target, value); err != nil {
		return AutoModeProposal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, fmt.Errorf("store: no database")
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(`
		INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, target, value, proposedBy, automode.StatePending, stamp)
	if err != nil {
		return AutoModeProposal{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AutoModeProposal{}, err
	}
	return AutoModeProposal{
		ID: id, Kind: kind, Target: target, Value: value,
		ProposedBy: proposedBy, State: automode.StatePending, CreatedAt: now.UTC(),
	}, nil
}

// ListAutoModeProposals returns proposals in the state given, oldest first. An
// empty state means every proposal.
func (s *Store) ListAutoModeProposals(state string) ([]AutoModeProposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	query := `SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals`
	args := []any{}
	if state != "" {
		query += ` WHERE state = ?`
		args = append(args, state)
	}
	query += ` ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoModeProposal
	for rows.Next() {
		p, err := scanAutoModeProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PromoteAutoModeProposal applies a pending proposal to the config and marks it
// promoted, in one transaction. This is auto mode's trust boundary: nothing else
// writes a pattern or a model into the config row, and only the app reaches it.
//
// Promoting an allow re-validates the pattern rather than trusting the recorded
// row — the guard belongs on the path that changes what runs, not only on the
// path that records an intention.
func (s *Store) PromoteAutoModeProposal(id int64, now time.Time) (AutoModeProposal, automode.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	defer tx.Rollback()

	proposal, err := scanAutoModeProposal(tx.QueryRow(`
		SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("no auto mode proposal %d", id)
	}
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if proposal.State != automode.StatePending {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("auto mode proposal %d is already %s", id, proposal.State)
	}
	if err := automode.ValidateProposal(proposal.Kind, proposal.Target, proposal.Value); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}

	cfg, err := s.readAutoModeConfig(tx)
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	switch proposal.Kind {
	case automode.KindAllow:
		cfg.Allow = appendUnique(cfg.Allow, proposal.Value)
	case automode.KindDeny:
		cfg.HardDeny = appendUnique(cfg.HardDeny, proposal.Value)
	case automode.KindModel:
		if proposal.Target == automode.TargetClassifier {
			cfg.ClassifierModel = proposal.Value
		} else {
			cfg.EscalationModel = proposal.Value
		}
	}
	if err := writeAutoModeConfig(tx, cfg, now); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`UPDATE automode_proposals SET state = ?, resolved_at = ? WHERE id = ?`,
		automode.StatePromoted, stamp, id); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	proposal.State = automode.StatePromoted
	proposal.ResolvedAt = now.UTC()
	return proposal, cfg, nil
}

// DiscardAutoModeProposal closes a pending proposal without applying it.
func (s *Store) DiscardAutoModeProposal(id int64, now time.Time) (AutoModeProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, fmt.Errorf("store: no database")
	}
	proposal, err := scanAutoModeProposal(s.db.QueryRow(`
		SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return AutoModeProposal{}, fmt.Errorf("no auto mode proposal %d", id)
	}
	if err != nil {
		return AutoModeProposal{}, err
	}
	if proposal.State != automode.StatePending {
		return AutoModeProposal{}, fmt.Errorf("auto mode proposal %d is already %s", id, proposal.State)
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := s.db.Exec(`UPDATE automode_proposals SET state = ?, resolved_at = ? WHERE id = ?`,
		automode.StateDiscarded, stamp, id); err != nil {
		return AutoModeProposal{}, err
	}
	proposal.State = automode.StateDiscarded
	proposal.ResolvedAt = now.UTC()
	return proposal, nil
}

// RecordAutoModeDenial stores one refused call. Slice 5 wires the reports; this
// is the writer they land in.
func (s *Store) RecordAutoModeDenial(sessionID, tool, signature, reason string, now time.Time) (AutoModeDenial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeDenial{}, fmt.Errorf("store: no database")
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(`
		INSERT INTO automode_denials (session_id, tool, signature, reason, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, tool, signature, reason, stamp)
	if err != nil {
		return AutoModeDenial{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AutoModeDenial{}, err
	}
	return AutoModeDenial{
		ID: id, SessionID: sessionID, Tool: tool, Signature: signature,
		Reason: reason, CreatedAt: now.UTC(),
	}, nil
}

// ListAutoModeDenials returns the most recent denials, newest first.
func (s *Store) ListAutoModeDenials(limit int) ([]AutoModeDenial, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, session_id, tool, signature, reason, created_at
		FROM automode_denials ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoModeDenial
	for rows.Next() {
		var d AutoModeDenial
		var created string
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Tool, &d.Signature, &d.Reason, &created); err != nil {
			return nil, err
		}
		d.CreatedAt = parseStoredTime(created)
		out = append(out, d)
	}
	return out, rows.Err()
}

// mutateAutoModeConfig reads the config, applies a change, and writes it back
// under the store's write lock.
func (s *Store) mutateAutoModeConfig(now time.Time, apply func(*automode.Config) error) (automode.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return automode.Config{}, fmt.Errorf("store: no database")
	}
	cfg, err := s.readAutoModeConfig(s.db)
	if err != nil {
		return automode.Config{}, err
	}
	if err := apply(&cfg); err != nil {
		return automode.Config{}, err
	}
	if err := writeAutoModeConfig(s.db, cfg, now); err != nil {
		return automode.Config{}, err
	}
	return cfg, nil
}

// writeAutoModeConfig takes an execer (jobs.go) so it writes through either a
// *sql.DB or a promote's transaction.
func writeAutoModeConfig(e execer, cfg automode.Config, now time.Time) error {
	environment, err := encodeStringList(cfg.Environment)
	if err != nil {
		return err
	}
	allow, err := encodeStringList(cfg.Allow)
	if err != nil {
		return err
	}
	hardDeny, err := encodeStringList(cfg.HardDeny)
	if err != nil {
		return err
	}
	enabled := 0
	if cfg.EnabledDefault {
		enabled = 1
	}
	_, err = e.Exec(`
		INSERT INTO automode_config
			(id, enabled_default, environment, allow_patterns, hard_deny,
			 classifier_model, escalation_model, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled_default  = excluded.enabled_default,
			environment      = excluded.environment,
			allow_patterns   = excluded.allow_patterns,
			hard_deny        = excluded.hard_deny,
			classifier_model = excluded.classifier_model,
			escalation_model = excluded.escalation_model,
			updated_at       = excluded.updated_at
	`, enabled, environment, allow, hardDeny, cfg.ClassifierModel, cfg.EscalationModel,
		now.UTC().Format(sortableTimeFormat))
	return err
}

func scanAutoModeProposal(row interface{ Scan(...any) error }) (AutoModeProposal, error) {
	var p AutoModeProposal
	var created, resolved string
	if err := row.Scan(&p.ID, &p.Kind, &p.Target, &p.Value, &p.ProposedBy, &p.State, &created, &resolved); err != nil {
		return AutoModeProposal{}, err
	}
	p.CreatedAt = parseStoredTime(created)
	p.ResolvedAt = parseStoredTime(resolved)
	return p, nil
}

func parseStoredTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{sortableTimeFormat, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func encodeStringList(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeStringList(raw, field string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("automode config %s is not a JSON list: %w", field, err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
