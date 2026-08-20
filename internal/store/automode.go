package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
)

// Auto mode's persistence (migration 109): the promoted config, the proposals
// waiting on a human, and the denials a session reported.
//
// The asymmetry between the two write paths is the point, and it lives here
// rather than in any one caller: PromoteAutoModeProposal, AddAutoModePattern
// and RemoveAutoModePattern are the ONLY ways a pattern or a model reaches the
// config row, and all three are reachable from the app alone. Everything an
// agent can call writes a proposal, which changes nothing a session launches
// with.
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

// AutoModeDenial is one call auto mode refused. Signature is the blocked call
// in one line; Rule names who decided — a static envelope rule, the classifier
// layer that answered (`classifier-2a`/`-2b`), `classifier-unavailable` when
// no classifier model could be reached, or the circuit breaker.
type AutoModeDenial struct {
	ID        int64
	SessionID string
	Tool      string
	Signature string
	Reason    string
	Rule      string
	CreatedAt time.Time
}

// AutoModeDenialRows is how many denials the log keeps. A tripwire, not a
// budget: auto mode's own circuit breaker stops a session at 20 denials since
// the user last spoke (`totalDenialLimit`, plugins/attn-pi/automode/session.ts),
// so this is 25 breaker-limit episodes — past any real day of work, and short
// of a loop nobody is watching filling the database.
const AutoModeDenialRows = 500

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
	// The shipped hard denies are resolved in on every read, so a machine whose
	// row predates them — or whose row never mentioned them — still runs with
	// auto mode's own surfaces denied.
	wsPort := config.WSPort()
	defaults := func() automode.Config {
		cfg := automode.Defaults()
		cfg.HardDeny = automode.ResolveHardDeny(wsPort, nil)
		return cfg
	}
	cfg := defaults()
	if s.db == nil {
		return cfg, nil
	}
	var (
		enabled                            int
		environment, allow, hardDeny       string
		classifierModels, escalationModels string
	)
	err := q.QueryRow(`
		SELECT enabled_default, environment, allow_patterns, hard_deny,
		       classifier_models, escalation_models
		FROM automode_config WHERE id = 1
	`).Scan(&enabled, &environment, &allow, &hardDeny, &classifierModels, &escalationModels)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	cfg.EnabledDefault = enabled != 0
	if cfg.Environment, err = decodeStringList(environment, "environment"); err != nil {
		return defaults(), err
	}
	if cfg.Allow, err = decodeStringList(allow, "allow"); err != nil {
		return defaults(), err
	}
	stored, err := decodeStringList(hardDeny, "hard_deny")
	if err != nil {
		return defaults(), err
	}
	cfg.HardDeny = automode.ResolveHardDeny(wsPort, stored)
	// An empty list means "whichever models ship", the same way an empty string
	// did before migration 114 — a machine that never picked one is not a
	// machine whose classifier can judge nothing.
	if models, err := decodeStringList(classifierModels, "classifier_models"); err != nil {
		return defaults(), err
	} else if len(models) > 0 {
		cfg.ClassifierModels = models
	}
	if models, err := decodeStringList(escalationModels, "escalation_models"); err != nil {
		return defaults(), err
	} else if len(models) > 0 {
		cfg.EscalationModels = models
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

// AddAutoModePattern adds one entry to the allow or hard-deny list, and
// RemoveAutoModePattern takes one out. These are the app's direct-edit path:
// only the WebSocket carries them, so what they change is still only ever
// changed by a human, the same boundary PromoteAutoModeProposal sits on.
//
// Both go through mutateAutoModeConfig, which reads the config resolved and
// writes it back stripped — so a shipped hard-deny is never persisted by an
// edit that merely passed through the list it appears in.
func (s *Store) AddAutoModePattern(list, pattern string, now time.Time) (automode.Config, error) {
	pattern = strings.TrimSpace(pattern)
	if err := automode.ValidatePattern(list, pattern); err != nil {
		return automode.Config{}, err
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		values := autoModePatternList(cfg, list)
		for _, existing := range *values {
			if existing == pattern {
				return fmt.Errorf("%q is already in the %s list", pattern, list)
			}
		}
		*values = append(*values, pattern)
		return nil
	})
}

// RemoveAutoModePattern drops one entry. A shipped hard-deny is refused rather
// than quietly ignored: it is resolved in at read, so a caller looking at the
// list has every reason to think it is removable, and silence would read as a
// removal that worked.
func (s *Store) RemoveAutoModePattern(list, pattern string, now time.Time) (automode.Config, error) {
	pattern = strings.TrimSpace(pattern)
	if list != automode.ListAllow && list != automode.ListHardDeny {
		return automode.Config{}, fmt.Errorf("unknown pattern list %q (want %s or %s)",
			list, automode.ListAllow, automode.ListHardDeny)
	}
	if list == automode.ListHardDeny {
		for _, shipped := range automode.ShippedHardDeny(config.WSPort()) {
			if shipped == pattern {
				return automode.Config{}, fmt.Errorf(
					"%q is a built-in hard deny and cannot be removed: it is what stops a session "+
						"under auto mode from rewriting its own policy", pattern)
			}
		}
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		values := autoModePatternList(cfg, list)
		kept := make([]string, 0, len(*values))
		found := false
		for _, existing := range *values {
			if existing == pattern {
				found = true
				continue
			}
			kept = append(kept, existing)
		}
		if !found {
			return fmt.Errorf("%q is not in the %s list", pattern, list)
		}
		*values = kept
		return nil
	})
}

// autoModePatternList points at the field one list name means, so add and
// remove share the naming rather than each switching on it.
func autoModePatternList(cfg *automode.Config, list string) *[]string {
	if list == automode.ListAllow {
		return &cfg.Allow
	}
	return &cfg.HardDeny
}

// CreateAutoModeProposal records a proposed change. It validates first, so a
// proposal that could never be promoted never reaches the app's review list.
//
// The review list is a human's, so it is defended twice. An identical pending
// proposal from the same proposer returns the one already there rather than a
// second row — a session denied the same call twice has asked once. The
// proposer is part of that key on purpose: the list says who asked, and
// collapsing a second session's ask onto the first would credit the wrong one.
// Two askers are two rows until a promotion satisfies both. A unique index over
// pending rows holds the same key, so the answer does not depend on which
// process is doing the asking. Past that, one proposer holds at most
// automode.MaxPendingProposalsPerProposer unresolved proposals, and the refusal
// names the cap and what it was asked to add: a caller that hit it can say what
// to promote or discard, and the list stays reviewable.
func (s *Store) CreateAutoModeProposal(kind, target, value, proposedBy string, now time.Time) (AutoModeProposal, error) {
	if err := automode.ValidateProposal(kind, target, value); err != nil {
		return AutoModeProposal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, fmt.Errorf("store: no database")
	}
	findPending := func() (AutoModeProposal, error) {
		return scanAutoModeProposal(s.db.QueryRow(`
			SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
			FROM automode_proposals
			WHERE state = ? AND kind = ? AND target = ? AND value = ? AND proposed_by = ?
			ORDER BY id ASC LIMIT 1`,
			automode.StatePending, kind, target, value, proposedBy))
	}
	existing, err := findPending()
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return AutoModeProposal{}, err
	}
	var pending int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM automode_proposals WHERE state = ? AND proposed_by = ?`,
		automode.StatePending, proposedBy).Scan(&pending); err != nil {
		return AutoModeProposal{}, err
	}
	if pending >= automode.MaxPendingProposalsPerProposer {
		return AutoModeProposal{}, fmt.Errorf(
			"%s already holds %d pending auto mode proposals (the cap is %d); "+
				"promote or discard some in the app before proposing more. Asked to add: %s",
			describeProposer(proposedBy), pending, automode.MaxPendingProposalsPerProposer,
			describeProposedChange(kind, target, value))
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(`
		INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, target, value, proposedBy, automode.StatePending, stamp)
	if err != nil {
		// The index caught an asker this process did not see; that ask is the
		// row already there, not a failure to report.
		if existing, findErr := findPending(); findErr == nil {
			return existing, nil
		}
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
		// The proposal names the layer's whole list, so promotion replaces it.
		models, err := automode.ParseModelList(proposal.Value)
		if err != nil {
			return AutoModeProposal{}, automode.Config{}, err
		}
		if proposal.Target == automode.TargetClassifier {
			cfg.ClassifierModels = models
		} else {
			cfg.EscalationModels = models
		}
	}
	if err := writeAutoModeConfig(tx, cfg, now); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	// Every asker for this same change is answered by this one promotion, so
	// none of them stays pending asking for what the config already says.
	if _, err := tx.Exec(`
		UPDATE automode_proposals SET state = ?, resolved_at = ?
		WHERE state = ? AND kind = ? AND target = ? AND value = ?`,
		automode.StatePromoted, stamp,
		automode.StatePending, proposal.Kind, proposal.Target, proposal.Value); err != nil {
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

// RecordAutoModeDenial stores one refused call and trims the log back to
// AutoModeDenialRows, returning how many rows that dropped so the caller can
// say so. The insert and the trim share one transaction: a denial that reached
// the feed and then vanished on the next write is worse than one never stored.
func (s *Store) RecordAutoModeDenial(denial AutoModeDenial, now time.Time) (AutoModeDenial, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeDenial{}, 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	defer tx.Rollback()

	denial.CreatedAt = now.UTC()
	res, err := tx.Exec(`
		INSERT INTO automode_denials (session_id, tool, signature, reason, rule, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, denial.SessionID, denial.Tool, denial.Signature, denial.Reason, denial.Rule,
		denial.CreatedAt.Format(sortableTimeFormat))
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	if denial.ID, err = res.LastInsertId(); err != nil {
		return AutoModeDenial{}, 0, err
	}
	trimmed, err := tx.Exec(`
		DELETE FROM automode_denials
		WHERE id <= (SELECT id FROM automode_denials ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		AutoModeDenialRows)
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	dropped, err := trimmed.RowsAffected()
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeDenial{}, 0, err
	}
	return denial, dropped, nil
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
		SELECT id, session_id, tool, signature, reason, rule, created_at
		FROM automode_denials ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoModeDenial
	for rows.Next() {
		var d AutoModeDenial
		var created string
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Tool, &d.Signature, &d.Reason, &d.Rule, &created); err != nil {
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
	// Every config here came out of a read, which resolved the shipped denies in.
	// Persisting them would freeze today's list into the row and defeat the point
	// of resolving at read.
	cfg.HardDeny = automode.StripShippedHardDeny(config.WSPort(), cfg.HardDeny)
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
	classifierModels, err := encodeStringList(cfg.ClassifierModels)
	if err != nil {
		return err
	}
	escalationModels, err := encodeStringList(cfg.EscalationModels)
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
			 classifier_models, escalation_models, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled_default   = excluded.enabled_default,
			environment       = excluded.environment,
			allow_patterns    = excluded.allow_patterns,
			hard_deny         = excluded.hard_deny,
			classifier_models = excluded.classifier_models,
			escalation_models = excluded.escalation_models,
			updated_at        = excluded.updated_at
	`, enabled, environment, allow, hardDeny, classifierModels, escalationModels,
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

func describeProposer(proposedBy string) string {
	if proposedBy == "" {
		return "this caller"
	}
	return proposedBy
}

func describeProposedChange(kind, target, value string) string {
	if kind == automode.KindModel {
		return fmt.Sprintf("%s %s %s", kind, target, value)
	}
	return fmt.Sprintf("%s %s", kind, value)
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
